package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// settleHandler serves a write whose task has already succeeded, and a
// subject (database or branch) whose reads after the write return
// statuses in turn, the last repeating; "404" answers not found. Reads
// before the write answer available, unless written says the write
// has already happened.
func settleHandler(written bool, statuses ...string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := written
	reads := 0
	task := fmt.Sprintf(`{"id":%q,"name":"t","status":"succeeded",
		"subject_id":%q,"subject_kind":"database","messages":[],
		"created_at":"2026-08-05T00:00:00Z",
		"updated_at":"2026-08-05T00:00:01Z"}`, testTaskID, testDatabaseID)

	return withCatalog(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if mutated || r.URL.Query().Get("id") != "" {
				_, _ = w.Write([]byte("[" + task + "]"))
				return
			}
			_, _ = w.Write([]byte(`[]`))
			return
		case r.Method != http.MethodGet:
			mutated = true
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
			return
		}
		status := "available"
		if mutated {
			status = statuses[min(reads, len(statuses)-1)]
			reads++
		}
		if status == "404" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
			return
		}
		body := databaseJSON(testDatabaseID, "")
		if strings.Contains(r.URL.Path, "/branches/") {
			body = branchJSON(testDatabaseID, testBranchID)
		}
		_, _ = w.Write([]byte(strings.Replace(body,
			`"status":"available"`, fmt.Sprintf(`"status":%q`, status), 1)))
	})
}

// TestWaitEndsOnTheSubjectStatus: --wait is done when the subject is,
// not when its task is. A succeeded task over a degraded database is a
// failure, and a status that lags the task is polled through.
func TestWaitEndsOnTheSubjectStatus(t *testing.T) {
	resize := []string{"database", "resize", testDatabaseID,
		"--size", "large", "--force"}
	branchDelete := []string{"database", "branch", "delete",
		testDatabaseID, testBranchID, "--force"}
	cases := []struct {
		name     string
		args     []string
		statuses []string
		timeout  string
		wantCode int
		wantText string
	}{
		{"lag then available", resize,
			[]string{"modifying", "available"}, "30", 0,
			"database " + testDatabaseID + ": available."},
		{"degraded after a succeeded task", resize,
			[]string{"degraded"}, "30", ExitGeneral, `"degraded"`},
		{"failed after a succeeded task", resize,
			[]string{"failed"}, "30", ExitGeneral, `"failed"`},
		{"never settles", resize,
			[]string{"modifying"}, "3", ExitTimeout, "last status: modifying"},
		{"branch delete ends at 404", branchDelete,
			[]string{"deleting", "404"}, "30", 0,
			"branch " + testBranchID + ": deleted."},
		{"branch delete that stops short", branchDelete,
			[]string{"available"}, "30", ExitGeneral, "not deleted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				settleHandler(false, tc.statuses...))
			args := append(append([]string{}, tc.args...), "--wait",
				"--wait-interval", "1", "--wait-timeout", tc.timeout)
			err := runAuthed(t, rt, out, url, args...)
			got := errb.String()
			if err != nil {
				got = err.Error()
			}
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("want exit 0, got %v", err)
				}
			} else {
				var ee *ExitError
				if !asExitError(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
			}
			if !strings.Contains(got, tc.wantText) {
				t.Errorf("want %q in %q", tc.wantText, got)
			}
		})
	}
}

// TestDatabaseWaitVerb covers the standalone wait's exit statuses.
func TestDatabaseWaitVerb(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		served   string
		wantCode int
	}{
		{"reaches available", "available", "available", 0},
		{"settles elsewhere", "available", "degraded", ExitGeneral},
		{"waits for another status", "degraded", "degraded", 0},
		{"unknown status", "ready", "available", ExitUsage},
		{"no such database", "available", "404", ExitNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				settleHandler(true, tc.served))
			err := runAuthed(t, rt, out, url, "database", "wait",
				testDatabaseID, "--status", tc.status,
				"--wait-timeout", "5", "--wait-interval", "1")
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("want exit 0, got %v", err)
				}
				return
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != tc.wantCode {
				t.Fatalf("want exit %d, got %v", tc.wantCode, err)
			}
		})
	}
}

// TestBackupCreateWaitFollowsTheRecord: the backup's task succeeds
// while its record is still pending, so --wait reads the record.
func TestBackupCreateWaitFollowsTheRecord(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		timeout  string
		wantCode int
	}{
		{"pending then completed", []string{"pending", "completed"},
			"30", 0},
		{"failed", []string{"running", "failed"}, "30", ExitGeneral},
		{"still pending", []string{"pending"}, "1", ExitTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			reads := 0
			taskReads := 0
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					mu.Lock()
					defer mu.Unlock()
					status := tc.statuses[min(reads, len(tc.statuses)-1)]
					body := strings.Replace(
						backupJSON(testBackupID, testDatabaseID),
						`"status":"completed"`,
						fmt.Sprintf(`"status":%q`, status), 1)
					switch {
					case strings.Contains(r.URL.Path, "/tasks"):
						taskReads++
						_, _ = w.Write([]byte(`[]`))
					case r.Method == http.MethodPost:
						w.WriteHeader(http.StatusAccepted)
						_, _ = w.Write([]byte(strings.Replace(body,
							`"status":"`+status+`"`,
							`"status":"pending"`, 1)))
					default:
						reads++
						_, _ = w.Write([]byte(body))
					}
				})
			err := runAuthed(t, rt, out, url, "backup", "create",
				"--database-id", testDatabaseID, "--kind", "hot",
				"--wait", "--wait-interval", "1",
				"--wait-timeout", tc.timeout)
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("want exit 0, got %v", err)
				}
				if !strings.Contains(errb.String(),
					"Backup "+testBackupID+": completed.") {
					t.Errorf("no completed line: %q", errb.String())
				}
			} else {
				var ee *ExitError
				if !asExitError(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
			}
			if taskReads != 0 {
				t.Errorf("read the task %d times; the record is "+
					"the signal", taskReads)
			}
		})
	}
}
