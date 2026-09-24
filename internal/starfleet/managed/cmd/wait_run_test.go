package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// waitFlowHandler simulates a mutation that spawns a task.
//
// Before the mutation the subject has no tasks, so the prior task id
// is empty; afterwards a new task appears. Lookups by id report
// terminalStatus, so the poll loop finishes in one pass.
//
// The handler has to route rather than answer everything with one
// body: waiting lists tasks, reads a task by id, and — for verbs that
// take an ID prefix — lists databases to resolve it. A single-body
// stub cannot drive that.
func waitFlowHandler(terminalStatus, taskError string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false

	errField := ""
	if taskError != "" {
		errField = fmt.Sprintf(`,"error":%q`, taskError)
	}
	newTask := fmt.Sprintf(`{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8","name":"create-managed",
		"status":%q,"subject_id":%q,"subject_kind":"database",
		"messages":[]%s,"created_at":"2026-08-05T00:00:00Z",
		"updated_at":"2026-08-05T00:00:01Z"}`,
		terminalStatus, testDatabaseID, errField)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if r.URL.Query().Get("id") != "" {
				_, _ = w.Write([]byte("[" + newTask + "]"))
				return
			}
			mu.Lock()
			done := mutated
			mu.Unlock()
			if done {
				_, _ = w.Write([]byte("[" + newTask + "]"))
			} else {
				// No prior tasks: whatever appears next is the one
				// this mutation spawned.
				_, _ = w.Write([]byte(`[]`))
			}

		case r.Method != http.MethodGet:
			mu.Lock()
			mutated = true
			mu.Unlock()
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))

		case strings.HasSuffix(r.URL.Path, "/databases"):
			_, _ = w.Write([]byte(
				"[" + databaseJSON(testDatabaseID, "") + "]"))

		default:
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		}
	}
}

// TestDatabaseMutationsWait covers the four asynchronous verbs. Each
// used to return as soon as the request was accepted, reporting a
// success it could not know.
func TestDatabaseMutationsWait(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"create", []string{"database", "create", "--name", "d",
			"--region", "us-east-2", "--size", "small"}},
		{"delete", []string{"database", "delete", testDatabaseID,
			"--force"}},
		{"resize", []string{"database", "resize", testDatabaseID,
			"--size", "large", "--force"}},
		{"rotate-password", []string{"database", "rotate-password",
			testDatabaseID, "--role", "app", "--force"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" succeeds", func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				withCatalog(waitFlowHandler("succeeded", "")))
			args := append(append([]string{}, tc.args...),
				"--wait", "--wait-interval", "1", "--wait-timeout", "30")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s --wait: %v", tc.name, err)
			}
			s := errb.String()
			if !strings.Contains(s, "Tracking task e9562e00-c8f8-438b-860a-b5eb427f88d8") {
				t.Errorf("did not report tracking a task: %q", s)
			}
			if !strings.Contains(s, "succeeded") {
				t.Errorf("did not report success: %q", s)
			}
		})

		t.Run(tc.name+" reports a failed task", func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				withCatalog(waitFlowHandler(
					"failed", "quota exceeded")))
			args := append(append([]string{}, tc.args...),
				"--wait", "--wait-interval", "1", "--wait-timeout", "30")
			err := runAuthed(t, rt, out, url, args...)
			if err == nil {
				t.Fatalf("%s --wait: expected an error", tc.name)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
				t.Fatalf("want ExitGeneral, got %v", err)
			}
			// Without the reason, --wait would only say that
			// something failed, which is barely better than not
			// waiting at all.
			if !strings.Contains(err.Error(), "quota exceeded") {
				t.Errorf("error does not carry the reason: %v", err)
			}
		})
	}
}

// TestDatabaseWaitTimesOut pins the third exit code. The deadline has
// to cover discovery as well as polling: a task that never appears
// must time out rather than hang.
func TestDatabaseWaitTimesOut(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "/tasks") {
				// A task is never spawned.
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		})

	err := runAuthed(t, rt, out, url, "database", "delete",
		testDatabaseID, "--force", "--wait",
		"--wait-interval", "1", "--wait-timeout", "0")
	if err == nil {
		t.Fatal("expected a timeout")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitTimeout {
		t.Fatalf("want ExitTimeout, got %v", err)
	}
	// With no task discovered the message names the subject, since
	// there is no task id to name.
	if !strings.Contains(err.Error(), testDatabaseID) {
		t.Errorf("timeout does not name the subject: %v", err)
	}
}

// TestDatabaseMutationsWithoutWaitHintAtTheTask pins the default. The
// hint goes to stderr, and only in human output, so stdout stays
// parseable for a script reading -o json.
func TestDatabaseMutationsWithoutWaitHintAtTheTask(t *testing.T) {
	t.Run("text hints", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			waitFlowHandler("succeeded", ""))
		if err := runAuthed(t, rt, out, url, "database", "delete",
			testDatabaseID, "--force"); err != nil {
			t.Fatalf("database delete: %v", err)
		}
		if !strings.Contains(errb.String(), "managed task list") {
			t.Errorf("no hint at how to monitor: %q", errb.String())
		}
		if strings.Contains(out.String(), "managed task list") {
			t.Errorf("hint leaked onto stdout: %q", out.String())
		}
	})

	t.Run("json stays silent", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			waitFlowHandler("succeeded", ""))
		if err := runAuthed(t, rt, out, url, "database", "delete",
			testDatabaseID, "--force"); err != nil {
			t.Fatalf("database delete json: %v", err)
		}
		if strings.Contains(errb.String(), "managed task list") {
			t.Errorf("hint printed in machine output: %q",
				errb.String())
		}
	})
}

// TestPriorTaskIsCapturedBeforeTheMutation is the subtle half of
// waiting. A database that already has tasks must not have an old one
// mistaken for the new one — otherwise --wait returns instantly,
// reporting the *previous* operation's result.
func TestPriorTaskIsCapturedBeforeTheMutation(t *testing.T) {
	// Table-driven over every verb whose wiring this fixture can
	// exercise, because it is the ONLY fixture whose database carries
	// task HISTORY: deleting rotate-password's captureTaskBaseline
	// call once left the whole suite green — the other wait tests
	// answer [] to the pre-mutation task list, so a dropped baseline is
	// invisible to them by construction.
	cases := []struct {
		name string
		args []string
	}{
		{"delete", []string{"database", "delete", testDatabaseID,
			"--force"}},
		{"rotate-password", []string{"database", "rotate-password",
			testDatabaseID, "--role", "app", "--force"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			mutated := false
			// The new task is NOT visible on the first post-write
			// poll — the real-world lag between the API accepting a
			// write and its task appearing. That lag is what makes a
			// dropped baseline observable at all: with the old task
			// alone on the wire, a zero baseline accepts it and
			// tracks the WRONG operation, while a captured baseline
			// rejects it and finds the real task one poll later.
			// Dropping the capture against a lag-free version of this
			// fixture left everything green.
			listsAfterWrite := 0
			oldTask := fmt.Sprintf(`{"id":"a93e58c4-d425-4233-adf4-9b668d1eaece","name":"update-managed",
		"status":"succeeded","subject_id":%q,"subject_kind":"database",
		"messages":[],"created_at":"2026-08-01T00:00:00Z",
		"updated_at":"2026-08-01T00:00:01Z"}`, testDatabaseID)
			newTask := fmt.Sprintf(`{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8","name":"delete-managed",
		"status":"succeeded","subject_id":%q,"subject_kind":"database",
		"messages":[],"created_at":"2026-08-05T00:00:00Z",
		"updated_at":"2026-08-05T00:00:01Z"}`, testDatabaseID)

			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.Contains(r.URL.Path, "/tasks"):
						if id := r.URL.Query().Get("id"); id != "" {
							if id == "a93e58c4-d425-4233-adf4-9b668d1eaece" {
								_, _ = w.Write([]byte("[" + oldTask + "]"))
							} else {
								_, _ = w.Write([]byte("[" + newTask + "]"))
							}
							return
						}
						mu.Lock()
						done := mutated
						if done {
							listsAfterWrite++
						}
						lagged := done && listsAfterWrite > 1
						mu.Unlock()
						if lagged {
							_, _ = w.Write([]byte(
								"[" + newTask + "," + oldTask + "]"))
						} else {
							_, _ = w.Write([]byte("[" + oldTask + "]"))
						}
					case r.Method != http.MethodGet:
						mu.Lock()
						mutated = true
						mu.Unlock()
						_, _ = w.Write([]byte(`{}`))
					default:
						_, _ = w.Write([]byte(
							databaseJSON(testDatabaseID, "")))
					}
				})

			args := append(append([]string{}, tc.args...), "--wait",
				"--wait-interval", "1", "--wait-timeout", "30")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s --wait: %v", tc.name, err)
			}
			s := errb.String()
			if !strings.Contains(s, "Tracking task e9562e00-c8f8-438b-860a-b5eb427f88d8") {
				t.Errorf("tracked the wrong task: %q", s)
			}
			if strings.Contains(s, "Tracking task a93e58c4-d425-4233-adf4-9b668d1eaece") {
				t.Errorf("mistook the database's earlier task for "+
					"this one: %q", s)
			}
		})
	}
}
