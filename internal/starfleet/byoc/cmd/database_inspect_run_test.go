package cmd

import (
	"database/sql/driver"
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

func runInspect(t *testing.T, format, body string, args ...string,
) (stdout string, err error) {
	t.Helper()
	rt, out, _ := testsupport.NewRuntime(t, "", format)
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
	full := append([]string{"database", "inspect", testDatabaseID}, args...)
	err = runAuthed(t, rt, out, url, full...)
	return out.String(), err
}

func TestByocInspectConnectsToTheChosenNode(t *testing.T) {
	seen := withFakeInspect(t)
	inspecttest.Script(t, "FROM pg_stat_user_tables\nORDER BY seq_scan",
		inspecttest.Result{
			Cols: []string{"schema", "table", "seq_scans", "seq_rows_read", "index_scans"},
			Rows: [][]driver.Value{{"public", "t", int64(1), int64(2), int64(3)}},
		})
	out, err := runInspect(t, "text",
		byocDatabaseWithNodesJSON(publicNode, privateNode),
		"seq-scans", "--node", "east", "--internal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*seen, testPrivateHost) {
		t.Errorf("connected to %q, want the private node", *seen)
	}
	if !strings.Contains(out, "SEQ SCANS") || !strings.Contains(out, "public") {
		t.Errorf("table:\n%s", out)
	}
	// One node: no flag needed, and the public host is used.
	_, err = runInspect(t, "text", byocDatabaseWithNodesJSON(publicNode), "seq-scans")
	if err != nil || !strings.Contains(*seen, testPublicHost) {
		t.Errorf("single node: err=%v seen=%q", err, *seen)
	}
}

func TestByocInspectRefusals(t *testing.T) {
	withFakeInspect(t)
	db := byocDatabaseWithNodesJSON(publicNode, privateNode)
	for _, tc := range []struct {
		name string
		body string
		args []string
		code int
		want string
	}{
		{"unknown analysis", db, []string{"nope"}, ExitUsage, "one of table-sizes"},
		{"several nodes, no flag", db, []string{"locks"}, ExitUsage, "--node"},
		{"empty node", db, []string{"locks", "--node", ""}, ExitUsage, "empty value"},
		{"private node without --internal", byocDatabaseWithNodesJSON(privateNode),
			[]string{"locks"}, ExitGeneral, "--internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runInspect(t, "text", tc.body, tc.args...)
			requireExitCode(t, err, tc.code)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestByocInspectMissingExtensionIsExitOne(t *testing.T) {
	withFakeInspect(t)
	inspecttest.Script(t, "FROM pg_extension", inspecttest.Result{
		Cols: []string{"count"}, Rows: [][]driver.Value{{int64(0)}},
	})
	_, err := runInspect(t, "text", byocDatabaseWithNodesJSON(publicNode), "outliers")
	if cli.ExitCode(err) != ExitGeneral || !strings.Contains(err.Error(), "pg_stat_statements") {
		t.Errorf("err = %v", err)
	}
}
