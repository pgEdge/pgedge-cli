package inspect

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Timeout bounds one analysis, connection included. Catalog queries
// answer in milliseconds; the bound is for a database that is not
// answering at all.
const Timeout = 30 * time.Second

// ErrMissingExtension is returned when an analysis needs an extension
// the database does not have. errors.Is distinguishes it from a
// failed connection so the caller can name the extension.
var ErrMissingExtension = errors.New("extension not installed")

// Row is one result row: the cell values as strings, in column order.
type Row struct {
	cols  []string
	cells []string
}

// Columns implements output.Row.
func (r Row) Columns() []string { return r.cells }

// MarshalJSON renders the row as an object keyed by column, in column
// order, so -o json reads as records rather than positional arrays.
// -o yaml goes through the same shape but sorts keys.
func (r Row) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range r.cols {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(r.cells[i])
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Open connects with the driver and verifies the connection within
// Timeout, so a wrong host fails here with the driver's message rather
// than on the first query.
func Open(ctx context.Context, dbURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Run executes one analysis and returns its rows. Every cell is read
// as text through the driver's own conversion, so a NULL is "" and a
// numeric keeps the server's digits.
func Run(ctx context.Context, db *sql.DB, a Analysis) ([]Row, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if a.Extension != "" {
		var n int
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_extension WHERE extname = $1`,
			a.Extension).Scan(&n)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("%w: %s", ErrMissingExtension, a.Extension)
		}
	}
	rows, err := db.QueryContext(ctx, a.SQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		raw := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{cols: cols, cells: make([]string, len(cols))}
		for i := range cols {
			r.cells[i] = raw[i].String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Headers upper-cases the analysis's columns for the text table, the
// way every other table in the CLI prints them.
func Headers(a Analysis) []string {
	h := make([]string, len(a.Columns))
	for i, c := range a.Columns {
		h[i] = strings.ToUpper(strings.ReplaceAll(c, "_", " "))
	}
	return h
}
