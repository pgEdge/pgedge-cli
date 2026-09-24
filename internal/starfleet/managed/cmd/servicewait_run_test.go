package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// serviceWaitHandler is waitFlowHandler for a SERVICE write. It
// differs in one way that matters: the GET must carry a deployed
// services array, because guardServiceIntent turns an `update` away
// on a database with no service of that type, and the write would
// never happen.
//
// The task it spawns is named `update-managed`, which is what a
// services write really spawns — a fixture reusing `create-managed`
// could not tell a task discovered for this write from one discovered
// for a create.
func serviceWaitHandler(terminalStatus, taskError string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false

	errField := ""
	if taskError != "" {
		errField = fmt.Sprintf(`,"error":%q`, taskError)
	}
	newTask := fmt.Sprintf(`{"id":"new-task","name":"update-managed",
		"status":%q,"subject_id":%q,"subject_kind":"database",
		"messages":[]%s,"created_at":"2026-08-05T00:00:00Z",
		"updated_at":"2026-08-05T00:00:01Z"}`,
		terminalStatus, testDatabaseID, errField)
	// One of each type, so `rag update` and `postgrest update` get past
	// guardServiceIntent too — an mcp-only fixture turns them away
	// before the write, and the wait would never be reached.
	withSvc := databaseJSON(testDatabaseID, threeServicesJSON)

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
				_, _ = w.Write([]byte(`[]`))
			}

		case r.Method != http.MethodGet:
			mu.Lock()
			mutated = true
			mu.Unlock()
			_, _ = w.Write([]byte(withSvc))

		default:
			_, _ = w.Write([]byte(withSvc))
		}
	}
}

// Every managed service write spawns an `update-managed` task with real
// duration — measured at ~15s, through reconcile-services and
// wait-for-services — and none of them took --wait, while the identical
// byoc commands all did. The reference stated the absence correctly, so
// this was a missing capability rather than a doc defect.
//
// All SEVEN service leaves get the flags, not only the five
// deploy/update verbs: applyServices is the single write for all of them, so
// registering on fewer would leave a verb reaching trackMutation and
// printing `Monitor with:` without declaring the flag that line names.
//
// That invariant is pinned for BYOC by example_output_test.go and not
// for managed, whose reference pastes no output — so the managed arm of
// that test examines zero command sections. What pins it here is
// TestManagedWaitFlagsFollowTheCode in internal/clitest, which derives
// the population from the package's own call graph: every constructor
// whose RunE reaches trackMutation must call addWaitFlags, and vice
// versa. It exists because THIS test cannot: its fixture carries every
// service type, so guardServiceIntent turns the three DEPLOY verbs away
// and only four of the seven are reachable from here.
func TestManagedServiceMutationsWait(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"mcp update", []string{"database", "mcp", "update",
			testDatabaseID, "--allow-writes"}},
		{"rag update", []string{"database", "rag", "update",
			testDatabaseID, "--top-n", "5"}},
		{"postgrest update", []string{"database", "postgrest", "update",
			testDatabaseID, "--max-rows", "500"}},
		{"service remove", []string{"database", "service", "remove",
			testDatabaseID, "mcp", "--force"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" succeeds", func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				serviceWaitHandler("succeeded", ""))
			args := append(append([]string{}, tc.args...),
				"--wait", "--wait-interval", "1", "--wait-timeout", "30")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s --wait: %v", tc.name, err)
			}
			s := errb.String()
			if !strings.Contains(s, "Tracking task new-task") {
				t.Errorf("%s: did not report tracking a task: %q",
					tc.name, s)
			}
			if !strings.Contains(s, "succeeded") {
				t.Errorf("%s: did not report success: %q", tc.name, s)
			}
		})

		t.Run(tc.name+" reports a failed task", func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				serviceWaitHandler("failed", "reconcile timed out"))
			args := append(append([]string{}, tc.args...),
				"--wait", "--wait-interval", "1", "--wait-timeout", "30")
			err := runAuthed(t, rt, out, url, args...)
			if err == nil {
				t.Fatalf("%s --wait: expected an error", tc.name)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
				t.Fatalf("%s: want ExitGeneral, got %v", tc.name, err)
			}
			// Without the reason a caller learns only that something
			// failed, which is barely better than not waiting.
			if !strings.Contains(err.Error(), "reconcile timed out") {
				t.Errorf("%s: error carries no reason: %v", tc.name, err)
			}
		})

		t.Run(tc.name+" without --wait names the task list",
			func(t *testing.T) {
				rt, out, errb := testsupport.NewRuntime(t, "", "text")
				url := testsupport.NewAuthedServer(t,
					serviceWaitHandler("succeeded", ""))
				if err := runAuthed(
					t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				want := "Monitor with: pgedge starfleet managed task list " +
					"--subject-id " + testDatabaseID
				if !strings.Contains(errb.String(), want) {
					t.Errorf("%s: stderr = %q, want %q",
						tc.name, errb.String(), want)
				}
			})

		t.Run(tc.name+" stays silent in json mode", func(t *testing.T) {
			// stdout must stay parseable, so the hint is suppressed
			// rather than moved. This is trackMutation's own contract
			// and the reason it is reached rather than reimplemented.
			rt, out, errb := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				serviceWaitHandler("succeeded", ""))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if strings.Contains(errb.String(), "Monitor with") {
				t.Errorf("%s: json mode printed the hint: %q",
					tc.name, errb.String())
			}
		})
	}
}

// The subject is db.Id, the id the GET returned, not the caller's
// spelling. uuid.Parse accepts uppercase and the braced and urn forms,
// so `mcp update {ID}` and `mcp update ID` name one database with two
// different strings, and only one of them is what the PATCH carried.
//
// NOT because the other spelling would find nothing: the endpoint
// normalises the filter, so a non-canonical subject_id finds exactly
// what the canonical one finds. What this assertion buys is that
// discovery does not RELY on that. openapi/managed.yaml declares
// subject_id a bare string with no format, so the normalisation is
// observed behaviour rather than a published promise, and a filter that
// stopped normalising would strand `--wait` on every write whose id was
// pasted in another case.
//
// Nothing else covers it: every byoc argument is already canonical by
// the time it is used.
func TestManagedServiceWaitUsesTheCanonicalSubjectID(t *testing.T) {
	var mu sync.Mutex
	var subjects []string
	handler := serviceWaitHandler("succeeded", "")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/tasks") {
				if s := r.URL.Query().Get("subject_id"); s != "" {
					mu.Lock()
					subjects = append(subjects, s)
					mu.Unlock()
				}
			}
			handler(w, r)
		})

	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url,
		"database", "mcp", "update", strings.ToUpper(testDatabaseID),
		"--allow-writes", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("mcp update --wait: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(subjects) == 0 {
		t.Fatal("no task list was filtered by subject_id; --wait " +
			"cannot have discovered the task by subject")
	}
	for _, s := range subjects {
		if s != testDatabaseID {
			t.Errorf("subject_id = %q, want the canonical %q — the "+
				"wait must send the id the GET returned, so discovery "+
				"does not depend on the endpoint normalising the "+
				"filter, which nothing published promises",
				s, testDatabaseID)
		}
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("did not report success: %q", errb.String())
	}
}
