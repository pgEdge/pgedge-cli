package cli

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/inspect/inspecttest"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func runInspect(t *testing.T, format string, args ...string,
) (stdout, stderr string, err error) {
	t.Helper()
	rt, out, errOut := testsupport.NewRuntime(t, "", format)
	var seen string
	cmd := NewInspectCmd(rt, &InspectDeps{Open: inspecttest.Opener(t, &seen)})
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestInspectPrintsTheAnalysisTable(t *testing.T) {
	inspecttest.Script(t, "FROM pg_stat_user_tables\nORDER BY seq_scan",
		inspecttest.Result{
			Cols: []string{"schema", "table", "seq_scans", "seq_rows_read", "index_scans"},
			Rows: [][]driver.Value{{"public", "orders", int64(12), int64(900), int64(3)}},
		})
	out, _, err := runInspect(t, "text", "seq-scans", "--db-url", "postgresql://x")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SCHEMA", "SEQ SCANS", "public", "orders", "900"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	out, _, err = runInspect(t, "json", "seq-scans", "--db-url", "postgresql://x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), `[{"schema":"public","table":"orders"`) {
		t.Errorf("json = %s", out)
	}
}

func TestInspectRefusalsHappenBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown analysis", []string{"nope", "--db-url", "x"}, "one of table-sizes"},
		{"no db-url", []string{"table-sizes"}, "--db-url is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runInspect(t, "text", tc.args...)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("want a usage error, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestInspectNamesAMissingExtension(t *testing.T) {
	inspecttest.Script(t, "FROM pg_extension", inspecttest.Result{
		Cols: []string{"count"}, Rows: [][]driver.Value{{int64(0)}},
	})
	_, _, err := runInspect(t, "text", "calls", "--db-url", "postgresql://x")
	if err == nil || !strings.Contains(err.Error(), "pg_stat_statements") ||
		ExitCode(err) != ExitError {
		t.Errorf("err = %v (exit %d)", err, ExitCode(err))
	}
}

func TestInspectNoRowsIsExitZero(t *testing.T) {
	inspecttest.Script(t, "pg_blocking_pids", inspecttest.Result{
		Cols: []string{"blocked_pid", "blocked_by", "blocked_duration", "blocked_query"},
	})
	out, errOut, err := runInspect(t, "text", "locks", "--db-url", "postgresql://x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "no rows") {
		t.Errorf("stderr = %q", errOut)
	}
	if strings.Contains(out, "BLOCKED") {
		t.Errorf("a header printed with no rows:\n%s", out)
	}
	out, _, err = runInspect(t, "json", "locks", "--db-url", "postgresql://x")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("json no rows = %q err=%v", out, err)
	}
}

func TestInspectCompletesTheVocabulary(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	cmd := NewInspectCmd(rt, nil)
	got, _ := cmd.ValidArgsFunction(cmd, nil, "")
	if len(got) < 8 || got[0] != "table-sizes" {
		t.Errorf("completion = %v", got)
	}
}
