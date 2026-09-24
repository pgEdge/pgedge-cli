package inspect

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/inspect/inspecttest"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// TestEveryAnalysisIsSelfConsistent pins the catalogue: distinct
// names, a Short, a SELECT or WITH statement that reads only, and a
// column list the statement's aliases can satisfy.
func TestEveryAnalysisIsSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Analyses {
		if seen[a.Name] {
			t.Errorf("duplicate analysis %q", a.Name)
		}
		seen[a.Name] = true
		if a.Short == "" || len(a.Columns) == 0 {
			t.Errorf("%s: missing Short or Columns", a.Name)
		}
		head := strings.ToUpper(strings.TrimSpace(a.SQL))
		if !strings.HasPrefix(head, "SELECT") && !strings.HasPrefix(head, "WITH") {
			t.Errorf("%s: statement does not read: %.30s", a.Name, a.SQL)
		}
		// DML and DDL, and the side-effecting functions a SELECT can
		// call: a statement that starts with SELECT is not read-only
		// because it says so.
		for _, w := range []string{"INSERT", "UPDATE", "DELETE", "DROP",
			"ALTER", "CREATE", "TRUNCATE", "PG_STAT_STATEMENTS_RESET",
			"PG_STAT_RESET", "PG_TERMINATE_BACKEND", "PG_CANCEL_BACKEND",
			"PG_PROMOTE", "PG_SWITCH_WAL", "SETVAL", "NEXTVAL",
			"PG_RELOAD_CONF", "SET_CONFIG", "LO_UNLINK", "LO_IMPORT", "COPY",
			"REFRESH", "VACUUM", "ANALYZE", "REINDEX", "CLUSTER", "LOCK",
			"GRANT", "REVOKE"} {
			if regexp.MustCompile(`\b` + w + `\b`).MatchString(head) {
				t.Errorf("%s: statement carries %q", a.Name, w)
			}
		}
		// The declared columns must be the outer select list's aliases,
		// in order: text output takes its header from Columns while the
		// cells arrive positionally, so a drift mislabels a column.
		outer := a.SQL
		if i := strings.LastIndex(outer, "\nSELECT "); i >= 0 {
			outer = outer[i:]
		}
		if j := strings.Index(outer, "\nFROM "); j >= 0 {
			outer = outer[:j]
		}
		last := -1
		for _, c := range a.Columns {
			m := regexp.MustCompile(`\b` + regexp.QuoteMeta(c) + `\b`).FindStringIndex(outer)
			if m == nil {
				t.Errorf("%s: column %q is not in the outer select list:\n%s", a.Name, c, outer)
				continue
			}
			if m[0] < last {
				t.Errorf("%s: column %q is out of order in the select list", a.Name, c)
			}
			last = m[0]
		}
		if n := topLevelItems(outer); n != len(a.Columns) {
			t.Errorf("%s: select list has %d items, Columns has %d", a.Name, n, len(a.Columns))
		}
		if _, ok := Lookup(a.Name); !ok {
			t.Errorf("Lookup(%s) failed", a.Name)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup accepted an unknown name")
	}
	if len(Names()) != len(Analyses) {
		t.Error("Names() length")
	}
}

// topLevelItems counts the comma-separated items of a select list,
// ignoring commas inside parentheses.
func topLevelItems(list string) int {
	depth, n := 0, 1
	for _, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}

func TestRunReadsEveryCellAsText(t *testing.T) {
	db := inspecttest.Open(t)
	a, _ := Lookup("seq-scans")
	inspecttest.Script(t, "FROM pg_stat_user_tables\nORDER BY seq_scan", inspecttest.Result{Cols: []string{"schema", "table", "seq_scans", "seq_rows_read", "index_scans"},
		Rows: [][]driver.Value{
			{"public", "orders", int64(120), int64(9000), nil},
		},
	})
	rows, err := Run(context.Background(), db, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	want := []string{"public", "orders", "120", "9000", ""}
	if strings.Join(rows[0].Columns(), "|") != strings.Join(want, "|") {
		t.Errorf("cells = %v", rows[0].Columns())
	}
	b, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"schema":"public","table":"orders","seq_scans":"120","seq_rows_read":"9000","index_scans":""}` {
		t.Errorf("json = %s", b)
	}
	var out strings.Builder
	r := &output.Renderer{Out: &out, Format: "text"}
	rs := make([]output.Row, len(rows))
	for i := range rows {
		rs[i] = rows[i]
	}
	if err := r.Print(rs, Headers(a)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SEQ ROWS READ") || !strings.Contains(out.String(), "orders") {
		t.Errorf("table:\n%s", out.String())
	}
}

func TestRunNamesAMissingExtension(t *testing.T) {
	db := inspecttest.Open(t)
	a, _ := Lookup("calls")
	inspecttest.Script(t, "FROM pg_extension", inspecttest.Result{Cols: []string{"count"}, Rows: [][]driver.Value{{int64(0)}}})
	_, err := Run(context.Background(), db, a)
	if !errors.Is(err, ErrMissingExtension) || !strings.Contains(err.Error(), "pg_stat_statements") {
		t.Errorf("err = %v", err)
	}
	// Present: the query runs.
	inspecttest.Script(t, "FROM pg_extension", inspecttest.Result{Cols: []string{"count"}, Rows: [][]driver.Value{{int64(1)}}})
	inspecttest.Script(t, "FROM pg_stat_statements\nORDER BY calls", inspecttest.Result{Cols: []string{"calls", "total_time", "mean_time", "query"},
		Rows: [][]driver.Value{{int64(3), "1.5", "0.5", "select 1"}},
	})
	rows, err := Run(context.Background(), db, a)
	if err != nil || len(rows) != 1 {
		t.Errorf("rows=%d err=%v", len(rows), err)
	}
}

func TestRunSurfacesAQueryError(t *testing.T) {
	db := inspecttest.Open(t)
	a, _ := Lookup("locks")
	inspecttest.Script(t, "pg_blocking_pids", inspecttest.Result{Err: errors.New("permission denied")})
	if _, err := Run(context.Background(), db, a); err == nil ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v", err)
	}
}

// TestOpenRejectsAnUnreachableHost also pins that the driver's error
// never echoes the password: the URI is the one place a live
// credential travels, and the error is what reaches stderr.
func TestOpenRejectsAnUnreachableHost(t *testing.T) {
	const pw = "s3cr3t-never-printed"
	_, err := Open(context.Background(),
		"postgresql://u:"+pw+"@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("open succeeded against a closed port")
	}
	if strings.Contains(err.Error(), pw) {
		t.Fatalf("the error carries the password: %v", err)
	}
}

func TestStatsAnalysesAreMarked(t *testing.T) {
	want := map[string]bool{"long-running-queries": true, "locks": true,
		"calls": true, "outliers": true, "replication-lag": true}
	for _, a := range Analyses {
		if a.NeedsStats != want[a.Name] {
			t.Errorf("%s: NeedsStats = %v", a.Name, a.NeedsStats)
		}
	}
}

// TestReplicationAnalysesRenderCapturedRows drives the three
// replication analyses with rows captured from a Postgres 18.6
// publisher and its subscriber. cols is the outer select list's
// aliases as the server reported them in that capture, held apart
// from Analysis.Columns on purpose: the two are separate hand-written
// lists, and comparing them is what catches a column added or
// reordered in one but not the other.
func TestReplicationAnalysesRenderCapturedRows(t *testing.T) {
	cases := []struct {
		analysis string
		key      string
		cols     []string
		row      []driver.Value
		want     []string
	}{
		{
			analysis: "replication-slots",
			key:      "FROM pg_replication_slots",
			cols: []string{"slot", "type", "plugin", "database", "active",
				"wal_status", "retained_wal"},
			row: []driver.Value{"s", "logical", "pgoutput", "postgres",
				"true", "reserved", "56 bytes"},
			want: []string{"s", "logical", "pgoutput", "postgres",
				"true", "reserved", "56 bytes"},
		},
		{
			// The three lag intervals are NULL on a caught-up
			// subscriber, which is the common reading.
			analysis: "replication-lag",
			key:      "FROM pg_stat_replication",
			cols: []string{"client", "application", "state", "sent_lsn",
				"replay_lsn", "write_lag", "flush_lag", "replay_lag",
				"sync_state"},
			row: []driver.Value{"172.19.0.3", "s", "streaming",
				"0/17AA710", "0/17AA710", nil, nil, nil, "async"},
			want: []string{"172.19.0.3", "s", "streaming",
				"0/17AA710", "0/17AA710", "", "", "", "async"},
		},
		{
			analysis: "subscriptions",
			key:      "FROM pg_stat_subscription",
			cols: []string{"subscription", "pid", "received_lsn",
				"last_msg_sent", "last_msg_received", "latest_end_lsn"},
			row: []driver.Value{"s", "91", "0/17AA710",
				"2026-09-01 16:47:40.57226+00",
				"2026-09-01 16:47:40.572283+00", "0/17AA710"},
			want: []string{"s", "91", "0/17AA710",
				"2026-09-01 16:47:40.57226+00",
				"2026-09-01 16:47:40.572283+00", "0/17AA710"},
		},
	}
	for _, c := range cases {
		t.Run(c.analysis, func(t *testing.T) {
			a, ok := Lookup(c.analysis)
			if !ok {
				t.Fatalf("%s is not in the vocabulary", c.analysis)
			}
			if strings.Join(a.Columns, "|") != strings.Join(c.cols, "|") {
				t.Fatalf("Columns = %v, the statement returns %v",
					a.Columns, c.cols)
			}
			db := inspecttest.Open(t)
			inspecttest.Script(t, c.key, inspecttest.Result{
				Cols: c.cols, Rows: [][]driver.Value{c.row}})
			rows, err := Run(context.Background(), db, a)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d", len(rows))
			}
			if got := strings.Join(rows[0].Columns(), "|"); got !=
				strings.Join(c.want, "|") {
				t.Errorf("cells = %s", got)
			}
			// Keyed by the server's own column names, so a cell that
			// moved lands under a header it does not belong to.
			b, err := json.Marshal(rows[0])
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]string
			if err := json.Unmarshal(b, &obj); err != nil {
				t.Fatal(err)
			}
			for i, col := range a.Columns {
				if obj[col] != c.want[i] {
					t.Errorf("%s = %q, want %q", col, obj[col], c.want[i])
				}
			}
			if len(obj) != len(c.cols) {
				t.Errorf("json has %d keys, the statement returns %d",
					len(obj), len(c.cols))
			}
		})
	}
}
