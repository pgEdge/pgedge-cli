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

// branchMetricsBody mirrors managedMetricsBody's shape (two disagreeing
// samples, so a renderer that picked the first rather than the newest
// would fail) at the branch path instead of the database one.
const branchMetricsBody = `{"series":[{"name":"","columns":[` +
	`"cpu_seconds_total","instance_name","memory_used_bytes","time"],` +
	`"values":[` +
	`[270.734698,"db-1",931725312,1786991610000],` +
	`[283.303779,"db-1",930353152,1786992420000]]}]}`

func TestDatabaseBranchMetricsRun(t *testing.T) {
	t.Run("text transposes the newest sample", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, branchMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch metrics: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"METRIC", "VALUE", "cpu_seconds_total", "283.303779",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		if strings.Contains(got, "270.734698") {
			t.Errorf("rendered the oldest sample, not the newest:\n%s", got)
		}
	})

	t.Run("json carries every sample", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, branchMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch metrics json: %v", err)
		}
		var got api.MetricSeriesContainer
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not the generated body: %v (%q)",
				err, out.String())
		}
		if len(got.Series) != 1 || len(got.Series[0].Values) != 2 {
			t.Errorf("json output = %+v, want one series of 2 samples",
				got)
		}
	})

	t.Run("empty reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"series":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch metrics empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("window is sent as the interval param", func(t *testing.T) {
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"series":[]}`))
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID,
			"--window", "5,minutes"); err != nil {
			t.Fatalf("branch metrics: %v", err)
		}
		if !strings.Contains(query, "interval=5%2Cminutes") {
			t.Errorf("query = %q, want the window sent as interval", query)
		}
	})

	t.Run("path carries both ids", func(t *testing.T) {
		var path string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"series":[]}`))
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID); err != nil {
			t.Fatalf("branch metrics: %v", err)
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
				`{"code":400,"message":"invalid interval"}`))
		if err := runAuthed(t, rt, out, url, "database", "branch",
			"metrics", testDatabaseID, testBranchID); err == nil {
			t.Fatal("a 400 was reported as success")
		}
	})
}

// See TestManagedMetricsRejectsAZeroWindow / ...ChecksPrecedeTheClient:
// both run with no credentials so exit 2, rather than exit 5, is the
// evidence the check runs before the client is built.
func TestManagedBranchMetricsRejectsAZeroWindow(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "branch", "metrics",
		testDatabaseID, testBranchID, "--window", "0,minutes")
	if err == nil {
		t.Fatal("a zero --window was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d", err, ExitUsage)
	}
}

func TestManagedBranchMetricsChecksPrecedeTheClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "branch", "metrics",
		testDatabaseID, testBranchID, "--window", "15,fortnights")
	if err == nil {
		t.Fatal("a bad --window was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d", err, ExitUsage)
	}
}

func TestManagedBranchMetricsRejectsABadTimeFlag(t *testing.T) {
	for _, flag := range []string{"--start-time", "--end-time"} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runManaged(t, rt, out, "database", "branch", "metrics",
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

func TestManagedBranchMetricsRejectsABadBranchID(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "branch", "metrics",
		testDatabaseID, "not-a-uuid")
	if err == nil {
		t.Fatal("a non-UUID branch id was reported as success")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage(%d); err=%v", got, ExitUsage, err)
	}
}
