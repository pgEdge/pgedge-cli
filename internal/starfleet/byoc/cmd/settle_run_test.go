package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// subjectAfterWrite answers a subject read once a write has gone out:
// 404 after a delete, available otherwise.
func subjectAfterWrite(w http.ResponseWriter, deleted bool) {
	if deleted {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"available"}`))
}

// settleHandler serves a write whose task has already succeeded, and a
// subject whose reads after the write return statuses in turn, the last
// repeating; "404" answers not found. Reads before the write answer
// available, unless written says the write has already happened.
func settleHandler(written bool, statuses ...string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := written
	reads := 0
	task := `{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
		`"status":"succeeded","messages":[],` +
		`"created_at":"2026-06-25T02:00:00Z"}`
	return func(w http.ResponseWriter, r *http.Request) {
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
			_, _ = w.Write([]byte(`{}`))
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
		body := dbNoServiceBody
		if strings.Contains(r.URL.Path, "/clusters/") {
			body = clusterBody
		}
		_, _ = w.Write([]byte(strings.Replace(body,
			`"status":"available"`, fmt.Sprintf(`"status":%q`, status), 1)))
	}
}

// TestWaitEndsOnTheSubjectStatus: --wait is done when the subject is,
// not when its task is.
func TestWaitEndsOnTheSubjectStatus(t *testing.T) {
	restore := []string{"database", "restore", testDatabaseID,
		"--node-name", "n1", "--repository",
		"9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d", "--force"}
	dbDelete := []string{"database", "delete", testDatabaseID, "--force"}
	clusterDelete := []string{"cluster", "delete", testClusterID, "--force"}
	cases := []struct {
		name     string
		args     []string
		statuses []string
		timeout  string
		wantCode int
		wantText string
	}{
		{"lag then available", restore,
			[]string{"restoring", "available"}, "30", 0,
			"database " + testDatabaseID + ": available."},
		{"degraded after a succeeded task", restore,
			[]string{"degraded"}, "30", ExitGeneral, `"degraded"`},
		{"failed after a succeeded task", restore,
			[]string{"failed"}, "30", ExitGeneral, `"failed"`},
		{"never settles", restore,
			[]string{"modifying"}, "3", ExitTimeout, "last status: modifying"},
		{"delete ends at 404", dbDelete,
			[]string{"deleting", "404"}, "30", 0,
			"database " + testDatabaseID + ": deleted."},
		{"delete that stops short", dbDelete,
			[]string{"available"}, "30", ExitGeneral, "not deleted"},
		{"cluster delete ends at 404", clusterDelete,
			[]string{"404"}, "30", 0,
			"cluster " + testClusterID + ": deleted."},
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
				if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
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
		{"settles elsewhere", "available", "failed", ExitGeneral},
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
			if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
				t.Fatalf("want exit %d, got %v", tc.wantCode, err)
			}
		})
	}
}

// TestBusyRefusalNamesTheStatus: byoc's busy 400 reads the same for a
// failed database as for a modifying one, so the CLI reads the status
// once and names it, keeping the refusal's exit code.
func TestBusyRefusalNamesTheStatus(t *testing.T) {
	busy := `{"code":400,"message":"database update cannot be ` +
		`completed. database is not in available status"}`
	cases := []struct {
		name      string
		refusal   string
		status    string
		wantReads int
		wantText  string
	}{
		{"failed is not told to wait", busy, "failed", 1,
			`is "failed": waiting will not change it`},
		{"modifying is told to retry later", busy, "modifying", 1,
			`is "modifying": it is mid-operation`},
		{"other 400s pass through unread",
			`{"code":400,"message":"bad role"}`, "failed", 0,
			"bad role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			reads := 0
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(tc.refusal))
						return
					}
					mu.Lock()
					reads++
					mu.Unlock()
					_, _ = w.Write([]byte(strings.Replace(dbNoServiceBody,
						`"status":"available"`,
						`"status":"`+tc.status+`"`, 1)))
				})
			err := runAuthed(t, rt, out, url, "database",
				"rotate-password", testDatabaseID, "--role", "app",
				"--force")
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
				t.Fatalf("want exit %d, got %v", ExitGeneral, err)
			}
			if reads != tc.wantReads {
				t.Errorf("database reads = %d, want %d", reads,
					tc.wantReads)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("want %q in %q", tc.wantText, err.Error())
			}
		})
	}
}
