package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// branchLogsBody mirrors managedLogsBody at the branch path.
const branchLogsBody = `{"logs":[` +
	`{"level":"log","message":"checkpoint complete",` +
	`"time":1786988160069},` +
	`{"level":"log","message":"checkpoint starting: time",` +
	`"time":1786988160062}]}`

func TestDatabaseBranchLogsRun(t *testing.T) {
	t.Run("text prints timestamped lines in API order", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, branchLogsBody))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch logs: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, managedLogsFirstTime) {
			t.Errorf("epoch was not rendered as RFC3339:\n%s", got)
		}
		if strings.Contains(got, "1786988160069") {
			t.Errorf("raw epoch leaked into text output:\n%s", got)
		}
	})

	t.Run("json carries the raw records", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, branchLogsBody))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch logs json: %v", err)
		}
		var got api.DatabaseLogsResponse
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not the generated body: %v (%q)",
				err, out.String())
		}
		if len(got.Logs) != 2 {
			t.Fatalf("json output has %d records, want 2", len(got.Logs))
		}
	})

	t.Run("empty reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"logs":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch logs empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No logs found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("max-lines is sent as the query param", func(t *testing.T) {
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"logs":[]}`))
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID,
			"--max-lines", "500"); err != nil {
			t.Fatalf("branch logs: %v", err)
		}
		if !strings.Contains(query, "max_lines=500") {
			t.Errorf("query = %q, want max_lines sent", query)
		}
	})

	t.Run("path carries both ids", func(t *testing.T) {
		var path string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"logs":[]}`))
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch logs: %v", err)
		}
		if !strings.Contains(path, testDatabaseID) ||
			!strings.Contains(path, testBranchID) {
			t.Errorf("path = %q, want both %s and %s",
				path, testDatabaseID, testBranchID)
		}
	})

	t.Run("api error is reported", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusBadRequest,
				`{"code":400,"message":"max_lines must be between 1 and 1000"}`))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"logs", testDatabaseID, testBranchID); err == nil {
			t.Fatal("a 400 was reported as success")
		}
	})
}

func TestManagedBranchLogsChecksPrecedeTheClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "branch", "logs",
		testDatabaseID, testBranchID, "--max-lines", "5000")
	if err == nil {
		t.Fatal("a bad --max-lines was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d", err, ExitUsage)
	}
}

func TestManagedBranchLogsRejectsABadTimeFlag(t *testing.T) {
	for _, flag := range []string{"--start-time", "--end-time"} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runManaged(t, rt, out, "database", "branch", "logs",
				testDatabaseID, testBranchID, flag, "yesterday")
			if err == nil {
				t.Fatalf("%s yesterday was reported as success", flag)
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want ExitUsage(%d); err=%v",
					got, ExitUsage, err)
			}
		})
	}
}

func TestManagedBranchLogsRejectsABadBranchID(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "branch", "logs",
		testDatabaseID, "not-a-uuid")
	if err == nil {
		t.Fatal("a non-UUID branch id was reported as success")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage(%d); err=%v", got, ExitUsage, err)
	}
}
