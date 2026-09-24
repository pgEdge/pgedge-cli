// Package inspecttest is a database/sql driver that answers queries
// from a script, so a command that runs an inspect analysis can be
// tested without a Postgres.
package inspecttest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// Result is one scripted answer: the columns and rows to return, or
// the error to fail with.
type Result struct {
	Cols []string
	Rows [][]driver.Value
	Err  error
}

type fakeDriver struct {
	mu      sync.Mutex
	answers map[string]Result
	opens   int
}

var fake = &fakeDriver{answers: map[string]Result{}}

// DriverName is the name the fake is registered under.
const DriverName = "inspectfake"

func init() { sql.Register(DriverName, fake) }

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	d.mu.Lock()
	d.opens++
	d.mu.Unlock()
	return &conn{d: d}, nil
}

type conn struct{ d *fakeDriver }

func (c *conn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *conn) Close() error                        { return nil }
func (c *conn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *conn) Ping(context.Context) error          { return nil }

// QueryContext answers with the first scripted Result whose key is a
// substring of the statement.
func (c *conn) QueryContext(_ context.Context, q string, _ []driver.NamedValue,
) (driver.Rows, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	for k, r := range c.d.answers {
		if strings.Contains(q, k) {
			if r.Err != nil {
				return nil, r.Err
			}
			return &rows{cols: r.Cols, rows: r.Rows}, nil
		}
	}
	short := q
	if len(short) > 60 {
		short = short[:60]
	}
	return nil, errors.New("inspecttest: no answer scripted for: " + short)
}

type rows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// Script registers an answer for statements containing key, for the
// life of the test.
func Script(t *testing.T, key string, r Result) {
	t.Helper()
	fake.mu.Lock()
	fake.answers[key] = r
	fake.mu.Unlock()
	t.Cleanup(func() {
		fake.mu.Lock()
		delete(fake.answers, key)
		fake.mu.Unlock()
	})
}

// Open returns a *sql.DB on the fake, closed when the test ends.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(DriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Opener is an inspect opener that ignores the URL and returns the
// fake, recording the URL it was handed.
func Opener(t *testing.T, seen *string) func(context.Context, string) (*sql.DB, error) {
	return func(_ context.Context, url string) (*sql.DB, error) {
		if seen != nil {
			*seen = url
		}
		return Open(t), nil
	}
}
