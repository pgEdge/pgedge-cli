package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// managedLogsBody is a real response trimmed to two records, verified
// 2026-08-17. Note the shape: a FLAT list of records, newest first,
// with time as an epoch in milliseconds. This is NOT byoc's per-node
// block shape, despite the API describing the two as mirrors.
const managedLogsBody = `{"logs":[` +
	`{"level":"log","message":"checkpoint complete",` +
	`"time":1786988160069},` +
	`{"level":"log","message":"checkpoint starting: time",` +
	`"time":1786988160062}]}`

// managedLogsFirstTime is what 1786988160069 ms renders as. Confirmed
// against Python's own epoch conversion rather than against this
// package's, so the assertion does not rest on its own premise.
const managedLogsFirstTime = "2026-08-17T17:36:00Z"

func TestDatabaseLogsRun(t *testing.T) {
	t.Run("text prints timestamped lines in API order",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, managedLogsBody))
			if err := runAuthed(t, rt, out, url, "database", "logs",
				testDatabaseID); err != nil {
				t.Fatalf("database logs: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, managedLogsFirstTime) {
				t.Errorf("epoch was not rendered as RFC3339:\n%s", got)
			}
			if strings.Contains(got, "1786988160069") {
				t.Errorf("raw epoch leaked into text output:\n%s", got)
			}
			complete := strings.Index(got, "checkpoint complete")
			starting := strings.Index(got, "checkpoint starting")
			if complete < 0 || starting < 0 {
				t.Fatalf("missing a log line:\n%s", got)
			}
			// The API sends newest first and the CLI does not re-sort.
			if complete > starting {
				t.Errorf("records were re-ordered:\n%s", got)
			}
		})

	// Levels vary in width — a real database mixes "log" and "error" —
	// so the level column is padded and the messages line up. Asserted
	// on the message's own column position rather than on a spacing
	// literal, which would pass for any two records padded alike.
	t.Run("messages align across levels", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"logs":[`+
				`{"level":"error","message":"MSGA","time":1786988160069},`+
				`{"level":"log","message":"MSGB","time":1786988160062}]}`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID); err != nil {
			t.Fatalf("database logs: %v", err)
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("got %d lines, want 2:\n%s", len(lines), out.String())
		}
		a := strings.Index(lines[0], "MSGA")
		b := strings.Index(lines[1], "MSGB")
		if a < 0 || b < 0 {
			t.Fatalf("a message is missing:\n%s", out.String())
		}
		if a != b {
			t.Errorf("messages start at columns %d and %d; the level "+
				"column is not padded:\n%s", a, b, out.String())
		}
	})

	t.Run("json carries the raw records", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, managedLogsBody))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID); err != nil {
			t.Fatalf("database logs json: %v", err)
		}
		var got api.DatabaseLogsResponse
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not the generated body: %v (%q)",
				err, out.String())
		}
		if len(got.Logs) != 2 {
			t.Fatalf("json output has %d records, want 2", len(got.Logs))
		}
		// The epoch must survive structured output unconverted.
		if got.Logs[0]["time"] != float64(1786988160069) {
			t.Errorf("json time = %v, want the raw epoch",
				got.Logs[0]["time"])
		}
	})

	t.Run("empty reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"logs":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID); err != nil {
			t.Fatalf("database logs empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No logs found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("api error is reported", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusBadRequest,
				`{"code":400,"message":"max_lines must be between 1 and 1000"}`))
		if err := runAuthed(t, rt, out, url, "database", "logs",
			testDatabaseID); err == nil {
			t.Fatal("a 400 was reported as success")
		}
	})
}

// TestDatabaseLogRecordsFromToleratesPartialRecords covers the branch
// the spec makes reachable: each item is a bare `type: object` with no
// declared properties, so the three keys are observed rather than
// contracted and any of them may be absent or wrongly typed.
func TestDatabaseLogRecordsFromToleratesPartialRecords(t *testing.T) {
	recs := databaseLogRecordsFrom([]map[string]interface{}{
		{"level": "log", "message": "full", "time": float64(1786988160069)},
		{"message": "no level or time"},
		{"level": "log", "time": "not-a-number"},
		{},
		{"level": "log", "message": 123, "time": float64(1786988160069)},
		{"level": "", "message": "", "time": float64(1786988160069)},
	})
	if len(recs) != 6 {
		t.Fatalf("got %d records, want 6", len(recs))
	}
	if recs[0].Time != managedLogsFirstTime || recs[0].Level != "log" ||
		recs[0].Message != "full" {
		t.Errorf("record 0 = %+v, want fully populated", recs[0])
	}
	if recs[1].Time != logRecordMissing ||
		recs[1].Level != logRecordMissing ||
		recs[1].Message != "no level or time" {
		t.Errorf("record 1 = %+v, want placeholders for the two "+
			"missing keys", recs[1])
	}
	if recs[2].Time != logRecordMissing {
		t.Errorf("record 2 time = %q, want a placeholder for a "+
			"wrongly typed value", recs[2].Time)
	}
	if recs[3].Time != logRecordMissing ||
		recs[3].Level != logRecordMissing ||
		recs[3].Message != logRecordMissing {
		t.Errorf("record 3 = %+v, want a placeholder in every field",
			recs[3])
	}
	// A wrongly typed message gets the same placeholder the other two
	// fields get, rather than an empty cell and a trailing space.
	if recs[4].Message != logRecordMissing {
		t.Errorf("record 4 message = %q, want a placeholder",
			recs[4].Message)
	}
	// An empty string is treated as absent, for both level and message,
	// so a record cannot render as a bare timestamp with trailing
	// whitespace where the two fields should be.
	if recs[5].Level != logRecordMissing ||
		recs[5].Message != logRecordMissing {
		t.Errorf("record 5 = %+v, want placeholders for the two empty "+
			"strings", recs[5])
	}
}

func TestValidateLogsMaxLines(t *testing.T) {
	tests := []struct {
		name string
		in   int
		ok   bool
	}{
		{"minimum", 1, true},
		{"maximum", 1000, true},
		{"middle", 100, true},
		{"below minimum", 0, false},
		{"above maximum", 1001, false},
		{"negative", -1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			err := validateLogsMaxLines(rt, tc.in)
			if tc.ok {
				if err != nil {
					t.Fatalf("rejected %d: %v", tc.in, err)
				}
				if got := rt.DryRun.Checks(); len(got) != 1 {
					t.Errorf("a passing check recorded %q, want one entry",
						got)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %d", tc.in)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Errorf("error for %d = %v, want exit %d",
					tc.in, err, ExitUsage)
			}
			if got := rt.DryRun.Checks(); len(got) != 0 {
				t.Errorf("a failed check recorded %q", got)
			}
		})
	}
}

// See TestManagedMetricsChecksPrecedeTheClient for why this runs with
// no credentials.
func TestManagedLogsChecksPrecedeTheClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "logs",
		testDatabaseID, "--max-lines", "5000")
	if err == nil {
		t.Fatal("a bad --max-lines was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d", err, ExitUsage)
	}
}

// TestManagedLogsRejectsABadTimeFlag covers the two window flags,
// which share parseTimeFlag with the backup verbs.
func TestManagedLogsRejectsABadTimeFlag(t *testing.T) {
	for _, flag := range []string{"--start-time", "--end-time"} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runManaged(t, rt, out, "database", "logs",
				testDatabaseID, flag, "yesterday")
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
