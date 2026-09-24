package cmd

import (
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/inspect/inspecttest"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func withFakeInspect(t *testing.T) *string {
	t.Helper()
	var seen string
	prev := inspectDeps
	inspectDeps = &cli.InspectDeps{Open: inspecttest.Opener(t, &seen)}
	t.Cleanup(func() { inspectDeps = prev })
	return &seen
}

func TestDatabaseInspectConnectsWithTheResolvedString(t *testing.T) {
	seen := withFakeInspect(t)
	inspecttest.Script(t, "FROM pg_stat_user_tables\nORDER BY seq_scan",
		inspecttest.Result{
			Cols: []string{"schema", "table", "seq_scans", "seq_rows_read", "index_scans"},
			Rows: [][]driver.Value{{"public", "t", int64(1), int64(2), int64(3)}},
		})
	var query string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseWithConnectionJSON(testDatabaseID)))
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "inspect",
		testDatabaseID, "seq-scans", "--user-type", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "user_type=admin") {
		t.Errorf("query %q missing user_type=admin", query)
	}
	if !strings.HasPrefix(*seen, "postgresql://app:") ||
		!strings.Contains(*seen, testConnHost) {
		t.Errorf("connected with %q, want the resolved string", *seen)
	}
	if !strings.Contains(out.String(), "SEQ SCANS") ||
		!strings.Contains(out.String(), "public") {
		t.Errorf("table:\n%s", out.String())
	}
}

// The four statistics analyses connect as admin unless --user-type
// says otherwise, because app sees nulls in other sessions' rows and
// would report an empty, quiet-looking database.
func TestDatabaseInspectStatsAnalysesDefaultToAdmin(t *testing.T) {
	withFakeInspect(t)
	inspecttest.Script(t, "pg_blocking_pids", inspecttest.Result{
		Cols: []string{"blocked_pid", "blocked_by", "blocked_duration", "blocked_query"},
	})
	inspecttest.Script(t, "FROM pg_stat_user_tables\nORDER BY n_dead_tup", inspecttest.Result{
		Cols: []string{"schema", "table", "live_rows", "dead_rows", "last_vacuum", "last_autovacuum"},
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"locks"}, "user_type=admin"},
		{[]string{"locks", "--user-type", "app"}, "user_type=app"},
		{[]string{"vacuum-stats"}, ""},
	} {
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(databaseWithConnectionJSON(testDatabaseID)))
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		args := append([]string{"database", "inspect", testDatabaseID}, tc.args...)
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if tc.want == "" && strings.Contains(query, "user_type") {
			t.Errorf("%v: sent a user_type it was not asked for: %q", tc.args, query)
		}
		if tc.want != "" && !strings.Contains(query, tc.want) {
			t.Errorf("%v: query %q lacks %q", tc.args, query, tc.want)
		}
	}
}

func TestDatabaseInspectRefusals(t *testing.T) {
	withFakeInspect(t)
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"unknown analysis", []string{"nope"}, ExitUsage, "one of table-sizes"},
		{"empty user-type", []string{"table-sizes", "--user-type", ""}, ExitUsage, "empty value"},
		{"unknown user-type", []string{"table-sizes", "--user-type", "root"}, ExitUsage, "root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				})
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := append([]string{"database", "inspect", testDatabaseID}, tc.args...)
			err := runAuthed(t, rt, out, url, args...)
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != tc.code {
				t.Fatalf("want exit %d, got %v", tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if called {
				t.Error("a refused invocation reached the server")
			}
		})
	}
}

func TestDatabaseInspectWithoutHostIsExitOne(t *testing.T) {
	withFakeInspect(t)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))
	err := runAuthed(t, rt, out, url, "database", "inspect",
		testDatabaseID, "table-sizes")
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Fatalf("want exit %d, got %v", ExitGeneral, err)
	}
}

func TestDatabaseInspectMissingExtensionIsExitOne(t *testing.T) {
	withFakeInspect(t)
	inspecttest.Script(t, "FROM pg_extension", inspecttest.Result{
		Cols: []string{"count"}, Rows: [][]driver.Value{{int64(0)}},
	})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))
	err := runAuthed(t, rt, out, url, "database", "inspect",
		testDatabaseID, "calls")
	if cli.ExitCode(err) != ExitGeneral ||
		!strings.Contains(err.Error(), "pg_stat_statements") {
		t.Fatalf("want exit %d naming the extension, got %v", ExitGeneral, err)
	}
}
