// Package inspectlive drives the built pgedge binary's inspect verb
// against real Postgres servers and checks the cell VALUES each
// analysis returns against rows the suite created itself. The unit
// tests answer SQL from a scripted fake and never run it, so an alias
// bound to the wrong expression passes them; only a server
// catches it.
//
// Skipped unless PGEDGE_INSPECT_LIVE=1. The servers come from
// compose.yaml beside this file, and the default connection strings
// match its ports.
package inspectlive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	envEnable            = "PGEDGE_INSPECT_LIVE"
	envPublisher         = "PGEDGE_INSPECT_LIVE_PUBLISHER"
	envSubscriber        = "PGEDGE_INSPECT_LIVE_SUBSCRIBER"
	envPublisherInternal = "PGEDGE_INSPECT_LIVE_PUBLISHER_INTERNAL"
	// envPGVersion, when set, is the Postgres major both servers must
	// report, so a CI leg named for one version cannot pass against
	// another.
	envPGVersion = "PGEDGE_INSPECT_LIVE_PG_VERSION"

	defaultPublisher  = "postgres://inspect:inspect@localhost:55432/inspect?sslmode=disable"
	defaultSubscriber = "postgres://inspect:inspect@localhost:55433/inspect?sslmode=disable"
	// The publisher as the subscriber reaches it: over the compose
	// network, by service name.
	defaultPublisherInternal = "host=publisher port=5432 user=inspect password=inspect dbname=inspect"

	schema       = "inspectlive"
	publication  = "inspectlive_pub"
	subscription = "inspectlive_sub"
	physicalSlot = "inspectlive_phys"

	// pollTimeout bounds every wait on the servers: a stats flush, a
	// lock to be taken, the subscriber to catch up.
	pollTimeout = 90 * time.Second
)

// server is one Postgres the suite drives: the binary connects with
// dsn; the suite's own fixture and reference session is conn.
type server struct {
	name string
	dsn  string
	conn *pgx.Conn
}

var (
	enabled  bool
	pub, sub *server
	binPath  string
	// homeDir is the empty HOME the binary runs under, so the suite
	// reads no developer's config file and writes nothing to one.
	homeDir string
	// currentUser and currentDB are read from the publisher, so the
	// assertions hold under an overridden connection string too.
	currentUser, currentDB string

	// sleeper is the session TestLongRunningQueries reads. It starts
	// before the first test so that the five minutes the analysis
	// requires elapse while the other tests run.
	sleeper struct {
		pid     int
		started time.Time
		cancel  context.CancelFunc
		done    chan error
	}
)

func TestMain(m *testing.M) {
	flag.Parse()
	enabled = os.Getenv(envEnable) == "1"
	if !enabled {
		os.Exit(m.Run())
	}
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inspectlive:", err)
		code = 1
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	ctx := context.Background()
	var err error
	if pub, err = connect(ctx, "publisher", envPublisher, defaultPublisher); err != nil {
		return 1, err
	}
	defer func() { _ = pub.conn.Close(ctx) }()
	if sub, err = connect(ctx, "subscriber", envSubscriber, defaultSubscriber); err != nil {
		return 1, err
	}
	defer func() { _ = sub.conn.Close(ctx) }()

	dir, err := os.MkdirTemp("", "pgedge-inspectlive")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	binPath = filepath.Join(dir, "pgedge")
	if err := buildBinary(binPath); err != nil {
		return 1, err
	}
	homeDir = filepath.Join(dir, "home")
	if err := os.Mkdir(homeDir, 0o700); err != nil {
		return 1, err
	}
	if want := os.Getenv(envPGVersion); want != "" {
		for _, s := range []*server{pub, sub} {
			if err := requireMajor(ctx, s, want); err != nil {
				return 1, err
			}
		}
	}

	// A previous run that died mid-way leaves its subscription, slots
	// and schema behind; clear them before building the fixtures.
	if err := teardown(ctx); err != nil {
		return 1, fmt.Errorf("clearing a previous run: %w", err)
	}
	if err := setup(ctx); err != nil {
		return 1, fmt.Errorf("fixtures: %w", err)
	}
	if !testing.Short() {
		if err := startSleeper(ctx); err != nil {
			return 1, err
		}
	}
	code := m.Run()
	stopSleeper()
	if err := teardown(ctx); err != nil {
		return 1, fmt.Errorf("teardown: %w", err)
	}
	return code, nil
}

// requireMajor fails unless s runs the Postgres major named.
func requireMajor(ctx context.Context, s *server, want string) error {
	var num int
	if err := s.conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&num); err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	if got := strconv.Itoa(num / 10000); got != want {
		return fmt.Errorf("%s runs Postgres %s, %s=%s", s.name, got, envPGVersion, want)
	}
	return nil
}

func connect(ctx context.Context, name, envKey, fallback string) (*server, error) {
	dsn := os.Getenv(envKey)
	if dsn == "" {
		dsn = fallback
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &server{name: name, dsn: dsn, conn: conn}, nil
}

// buildBinary compiles the working tree, so the suite tests the code
// under review rather than an installed pgedge.
func buildBinary(path string) error {
	cmd := exec.Command("go", "build", "-o", path, "./cmd/pgedge")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build pgedge: %w\n%s", err, stderr.String())
	}
	return nil
}

// publisherFixtures are run one statement at a time: VACUUM refuses a
// transaction block, and one multi-statement Exec would be one.
var publisherFixtures = []string{
	`CREATE SCHEMA ` + schema,
	`CREATE EXTENSION IF NOT EXISTS pg_stat_statements`,

	// sized: the table every size analysis reads. Autovacuum is off on
	// each fixture table so nothing rewrites the statistics or the
	// pages between the fixture and the assertion.
	`CREATE TABLE ` + schema + `.sized (id int PRIMARY KEY, pad text, tag int, v2 int)
	   WITH (autovacuum_enabled = false)`,
	`INSERT INTO ` + schema + `.sized
	   SELECT g, repeat('x', 200), g % 100, g % 7 FROM generate_series(1, 20000) g`,
	`CREATE INDEX sized_tag_idx ON ` + schema + `.sized (tag)`,
	`CREATE INDEX sized_v2_idx ON ` + schema + `.sized (v2)`,
	`SELECT pg_stat_force_next_flush()`,
	`ANALYZE ` + schema + `.sized`,

	// scanned: seq-scans counts the scans TestSeqScans performs on it.
	`CREATE TABLE ` + schema + `.scanned (id int PRIMARY KEY, v int)
	   WITH (autovacuum_enabled = false)`,
	`INSERT INTO ` + schema + `.scanned SELECT g, g FROM generate_series(1, 1000) g`,

	// vacuumed and deleted: the same 1000 rows and 400 deletes; one
	// is vacuumed and the other never is.
	`CREATE TABLE ` + schema + `.vacuumed (id int) WITH (autovacuum_enabled = false)`,
	`INSERT INTO ` + schema + `.vacuumed SELECT g FROM generate_series(1, 1000) g`,
	`DELETE FROM ` + schema + `.vacuumed WHERE id <= 400`,
	// Flush before VACUUM and ANALYZE: each resets the table's counters
	// from what it saw, and a change still pending in this session
	// lands on top of that afterwards (1200 live rows, measured).
	`SELECT pg_stat_force_next_flush()`,
	`VACUUM ` + schema + `.vacuumed`,
	`CREATE TABLE ` + schema + `.deleted (id int) WITH (autovacuum_enabled = false)`,
	`INSERT INTO ` + schema + `.deleted SELECT g FROM generate_series(1, 1000) g`,
	`DELETE FROM ` + schema + `.deleted WHERE id <= 400`,

	// bloated: analysed full, then four rows in five deleted and
	// analysed again, so reltuples falls while relpages stands.
	`CREATE TABLE ` + schema + `.bloated (id int, pad text) WITH (autovacuum_enabled = false)`,
	`INSERT INTO ` + schema + `.bloated
	   SELECT g, repeat('b', 100) FROM generate_series(1, 20000) g`,
	`SELECT pg_stat_force_next_flush()`,
	`ANALYZE ` + schema + `.bloated`,
	`DELETE FROM ` + schema + `.bloated WHERE id % 5 <> 0`,
	`SELECT pg_stat_force_next_flush()`,
	`ANALYZE ` + schema + `.bloated`,

	`CREATE TABLE ` + schema + `.locked (id int PRIMARY KEY, v int)`,
	`INSERT INTO ` + schema + `.locked VALUES (1, 0)`,
	`CREATE TABLE ` + schema + `.calls_marker (id int)`,
	`INSERT INTO ` + schema + `.calls_marker VALUES (1)`,

	`CREATE TABLE ` + schema + `.replicated (id int PRIMARY KEY, v text)`,
	`INSERT INTO ` + schema + `.replicated SELECT g, 'seed' FROM generate_series(1, 10) g`,
	`CREATE PUBLICATION ` + publication + ` FOR TABLE ` + schema + `.replicated`,
	`SELECT pg_stat_force_next_flush()`,
}

func setup(ctx context.Context) error {
	if err := pub.conn.QueryRow(ctx,
		`SELECT current_user, current_database()`).Scan(&currentUser, &currentDB); err != nil {
		return err
	}
	for _, stmt := range publisherFixtures {
		if _, err := pub.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("publisher: %s: %w", firstLine(stmt), err)
		}
	}
	internal := os.Getenv(envPublisherInternal)
	if internal == "" {
		internal = defaultPublisherInternal
	}
	subscriberFixtures := []string{
		`CREATE SCHEMA ` + schema,
		`CREATE TABLE ` + schema + `.replicated (id int PRIMARY KEY, v text)`,
		`CREATE SUBSCRIPTION ` + subscription + ` CONNECTION '` + internal +
			`' PUBLICATION ` + publication,
	}
	for _, stmt := range subscriberFixtures {
		if _, err := sub.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("subscriber: %s: %w", firstLine(stmt), err)
		}
	}
	// The initial copy and the switch to streaming are what the
	// replication tests read, so wait for both here rather than in
	// each test.
	return poll(ctx, "subscriber streaming the seed rows", func() (bool, error) {
		var n int
		if err := sub.conn.QueryRow(ctx,
			`SELECT count(*) FROM `+schema+`.replicated`).Scan(&n); err != nil {
			return false, err
		}
		var state string
		err := pub.conn.QueryRow(ctx,
			`SELECT state FROM pg_stat_replication WHERE application_name = $1`,
			subscription).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return n == 10 && state == "streaming", nil
	})
}

// teardown removes everything setup made, in the order Postgres
// allows: the subscription first, detached from its slot so the drop
// needs no connection to the publisher, then the publisher's slots
// once no walsender holds them.
func teardown(ctx context.Context) error {
	var exists bool
	if err := sub.conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_subscription WHERE subname = $1)`,
		subscription).Scan(&exists); err != nil {
		return err
	}
	if exists {
		for _, stmt := range []string{
			`ALTER SUBSCRIPTION ` + subscription + ` DISABLE`,
			`ALTER SUBSCRIPTION ` + subscription + ` SET (slot_name = NONE)`,
			`DROP SUBSCRIPTION ` + subscription,
		} {
			if _, err := sub.conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("subscriber: %s: %w", stmt, err)
			}
		}
	}
	if _, err := sub.conn.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		return err
	}
	err := poll(ctx, "publisher slots inactive", func() (bool, error) {
		var active int
		err := pub.conn.QueryRow(ctx,
			`SELECT count(*) FROM pg_replication_slots
			  WHERE slot_name IN ($1, $2) AND active`,
			subscription, physicalSlot).Scan(&active)
		return active == 0, err
	})
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
		  WHERE slot_name IN ('` + subscription + `', '` + physicalSlot + `')`,
		`DROP PUBLICATION IF EXISTS ` + publication,
		`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`,
	} {
		if _, err := pub.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("publisher: %s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// startSleeper opens a session that stays active on one statement for
// the rest of the run, which is the only way to give
// long-running-queries a row: query_start cannot be forged.
func startSleeper(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, pub.dsn)
	if err != nil {
		return fmt.Errorf("sleeper: %w", err)
	}
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&sleeper.pid); err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	sleeper.cancel = cancel
	sleeper.done = make(chan error, 1)
	sleeper.started = time.Now()
	go func() {
		_, err := conn.Exec(sctx, `SELECT pg_sleep(1800) AS inspectlive_long`)
		sleeper.done <- err
		_ = conn.Close(context.Background())
	}()
	return nil
}

func stopSleeper() {
	if sleeper.cancel == nil {
		return
	}
	sleeper.cancel()
	<-sleeper.done
}

// requireLive skips a test in a run that did not enable the suite.
func requireLive(t *testing.T) {
	t.Helper()
	if !enabled {
		t.Skipf("set %s=1 to run the live inspect suite", envEnable)
	}
}

// result is one run of the binary.
type result struct {
	stdout, stderr string
	code           int
}

// run executes `pgedge inspect <analysis> --db-url <s.dsn> args...`.
func run(t *testing.T, s *server, analysis string, args ...string) result {
	t.Helper()
	full := append([]string{"inspect", analysis, "--db-url", s.dsn}, args...)
	cmd := exec.Command(binPath, full...)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		t.Fatalf("pgedge %s: %v", strings.Join(full, " "), err)
	}
	return r
}

// rows runs an analysis with -o json and decodes the records. Every
// cell is a string: the verb prints the server's own text.
func rows(t *testing.T, s *server, analysis string) []map[string]string {
	t.Helper()
	r := run(t, s, analysis, "-o", "json")
	if r.code != 0 {
		t.Fatalf("%s %s: exit %d\nstdout: %s\nstderr: %s",
			s.name, analysis, r.code, r.stdout, r.stderr)
	}
	var out []map[string]string
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("%s %s: decode: %v\n%s", s.name, analysis, err, r.stdout)
	}
	return out
}

// inSchema keeps the rows whose schema column names the fixture
// schema, so tables left by anything else on the server do not count.
func inSchema(all []map[string]string) []map[string]string {
	var out []map[string]string
	for _, r := range all {
		if r["schema"] == schema {
			out = append(out, r)
		}
	}
	return out
}

// find returns the one row whose key column equals value.
func find(t *testing.T, all []map[string]string, key, value string) map[string]string {
	t.Helper()
	var hits []map[string]string
	for _, r := range all {
		if r[key] == value {
			hits = append(hits, r)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want one row with %s=%q, got %d in %v", key, value, len(hits), all)
	}
	return hits[0]
}

// ref runs a reference query on the suite's own session and returns
// every cell as text. Reference SQL casts to text itself, so a NULL
// arrives as "" the way the binary prints it.
func ref(t *testing.T, s *server, sql string, args ...any) [][]string {
	t.Helper()
	rs, err := s.conn.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %s: %v", s.name, firstLine(sql), err)
	}
	defer rs.Close()
	var out [][]string
	for rs.Next() {
		vals, err := rs.Values()
		if err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			if v != nil {
				row[i] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("%s: %s: %v", s.name, firstLine(sql), err)
	}
	return out
}

// stable runs an analysis and the reference query that mirrors it,
// and returns both only from an attempt where the reference read the
// same before and after the binary ran. Statistics move under the
// suite (a checkpoint, a keepalive), and an exact comparison against a
// moving reference would be a flake, not a finding.
func stable(t *testing.T, s *server, analysis, refSQL string) (got []map[string]string, want [][]string) {
	t.Helper()
	for attempt := 0; attempt < 5; attempt++ {
		before := ref(t, s, refSQL)
		got = rows(t, s, analysis)
		after := ref(t, s, refSQL)
		if fmt.Sprint(before) == fmt.Sprint(after) {
			return got, before
		}
	}
	t.Fatalf("%s: reference for %s kept changing across five runs", s.name, analysis)
	return nil, nil
}

// exec runs a fixture statement on the suite's session.
func exec1(t *testing.T, s *server, sql string, args ...any) {
	t.Helper()
	if _, err := s.conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %s: %v", s.name, firstLine(sql), err)
	}
}

// flush pushes the session's pending statistics to shared memory, so
// the binary's own session reads what the suite just did.
func flush(t *testing.T, s *server) {
	t.Helper()
	exec1(t, s, `SELECT pg_stat_force_next_flush()`)
}

// poll waits for cond, checking every 200 ms up to pollTimeout.
func poll(ctx context.Context, what string, cond func() (bool, error)) error {
	deadline := time.Now().Add(pollTimeout)
	for {
		ok, err := cond()
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: not within %s", what, pollTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func waitFor(t *testing.T, what string, cond func() (bool, error)) {
	t.Helper()
	if err := poll(context.Background(), what, cond); err != nil {
		t.Fatal(err)
	}
}

// refInt reads one integer from a reference query.
func refInt(t *testing.T, s *server, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.conn.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %s: %v", s.name, firstLine(sql), err)
	}
	return n
}

var sizeUnits = map[string]int64{
	"bytes": 1, "kB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40,
}

// parseSize reads pg_size_pretty output ("2224 kB") back to bytes.
func parseSize(t *testing.T, s string) int64 {
	t.Helper()
	n, unit, ok := strings.Cut(s, " ")
	mult, known := sizeUnits[unit]
	v, err := strconv.ParseInt(n, 10, 64)
	if !ok || !known || err != nil {
		t.Fatalf("not a pg_size_pretty value: %q", s)
	}
	return v * mult
}

var intervalRE = regexp.MustCompile(`^(?:(\d+) days? )?(\d+):(\d\d):(\d\d)(?:\.(\d{1,6}))?$`)

// parseInterval reads an interval's default text form ("00:05:02.1")
// as a duration.
func parseInterval(t *testing.T, s string) time.Duration {
	t.Helper()
	m := intervalRE.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("not an interval: %q", s)
	}
	atoi := func(x string) int64 {
		if x == "" {
			return 0
		}
		v, _ := strconv.ParseInt(x, 10, 64)
		return v
	}
	frac := m[5] + strings.Repeat("0", 6-len(m[5]))
	d := time.Duration(atoi(m[1]))*24*time.Hour +
		time.Duration(atoi(m[2]))*time.Hour +
		time.Duration(atoi(m[3]))*time.Minute +
		time.Duration(atoi(m[4]))*time.Second +
		time.Duration(atoi(frac))*time.Microsecond
	return d
}

var lsnRE = regexp.MustCompile(`^[0-9A-F]{1,8}/[0-9A-F]{1,8}$`)

// isLSN reports whether s reads as a pg_lsn.
func isLSN(s string) bool { return lsnRE.MatchString(s) }

// lsnValue orders two pg_lsn texts: the segment half above the offset.
func lsnValue(t *testing.T, s string) uint64 {
	t.Helper()
	hi, lo, ok := strings.Cut(s, "/")
	h, err1 := strconv.ParseUint(hi, 16, 32)
	l, err2 := strconv.ParseUint(lo, 16, 32)
	if !ok || err1 != nil || err2 != nil {
		t.Fatalf("not a pg_lsn: %q", s)
	}
	return h<<32 | l
}

// parseTimestamp reads a timestamptz's text form in the server's
// zone, which the compose servers keep at UTC.
func parseTimestamp(t *testing.T, s string) time.Time {
	t.Helper()
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999-07:00",
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts
		}
	}
	t.Fatalf("not a timestamptz: %q", s)
	return time.Time{}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
