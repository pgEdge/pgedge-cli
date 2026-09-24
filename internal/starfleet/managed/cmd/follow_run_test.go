package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// followFlowHandler simulates a mutation whose task accumulates step
// messages across polls: the first by-id lookup answers a running
// task carrying one message, every later lookup the terminal task
// carrying three. A --follow that cursors correctly prints each
// message exactly once; one that re-prints the whole slice per poll
// duplicates the first.
func followFlowHandler(terminalStatus, taskError string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false
	byIDCalls := 0

	// Derived from the message, not from a call counter: early and
	// full[0] are the same message, so a counter would hand the fixture
	// two times for one message and leave a gap in the whole-line
	// literals TestFollowStepLinesAreByteIdentical asserts. The cursor
	// assertions would not notice — they match substrings that carry no
	// timestamp.
	msg := func(progress int, status, step, text string) string {
		return fmt.Sprintf(`{"level":"info","progress":%d,"status":%q,
			"step":%q,"text":%q,"time":"2026-08-05T20:49:%02dZ"}`,
			progress, status, step, text, 50+progress/10)
	}
	early := "[" + msg(5, "running", "configure-system",
		"Configuring System") + "]"
	full := "[" +
		msg(5, "running", "configure-system", "Configuring System") + "," +
		msg(15, "succeeded", "configure-system", "Configuring System") + "," +
		msg(65, "running", "provision-database", "Provisioning Database") +
		"]"

	errField := ""
	if taskError != "" {
		// json.Marshal, not %q: Go quoting spells a control byte
		// `\x1b`, which JSON has no escape for, so a fixture built
		// with %q serves invalid JSON the moment a test passes one.
		enc, err := json.Marshal(taskError)
		if err != nil {
			panic(err)
		}
		errField = `,"error":` + string(enc)
	}
	task := func(status, messages string) string {
		return fmt.Sprintf(`{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8","name":"create-managed",
			"status":%q,"subject_id":%q,"subject_kind":"database",
			"messages":%s%s,"created_at":"2026-08-05T00:00:00Z",
			"updated_at":"2026-08-05T00:00:01Z"}`,
			status, testDatabaseID, messages, errField)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if r.URL.Query().Get("id") != "" {
				mu.Lock()
				byIDCalls++
				n := byIDCalls
				mu.Unlock()
				if n == 1 {
					_, _ = w.Write([]byte(
						"[" + task("running", early) + "]"))
				} else {
					_, _ = w.Write([]byte(
						"[" + task(terminalStatus, full) + "]"))
				}
				return
			}
			mu.Lock()
			done := mutated
			mu.Unlock()
			if done {
				_, _ = w.Write([]byte(
					"[" + task("running", early) + "]"))
			} else {
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

// TestFollowStreamsTaskMessages pins --follow's contract: the task's
// step messages stream to stderr as they appear, each exactly once
// (the cursor, not a per-poll re-print), and the terminal state keeps
// --wait's exit behaviour. --follow alone must track without --wait.
func TestFollowStreamsTaskMessages(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		withCatalog(followFlowHandler("succeeded", "")))

	err := runAuthed(t, rt, out, url,
		"database", "create", "--name", "d",
		"--region", "us-east-2", "--size", "small",
		"--follow", "--wait-interval", "1", "--wait-timeout", "30")
	if err != nil {
		t.Fatalf("create --follow: %v", err)
	}

	s := errb.String()
	for _, want := range []string{
		"Configuring System: running (5%)",
		"Configuring System: succeeded (15%)",
		"Provisioning Database: running (65%)",
		"Task e9562e00-c8f8-438b-860a-b5eb427f88d8: succeeded.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr is missing %q:\n%s", want, s)
		}
	}
	if n := strings.Count(s, "Configuring System: running (5%)"); n != 1 {
		t.Errorf("first message printed %d times, want exactly 1:\n%s",
			n, s)
	}
	if strings.Contains(s, "Task e9562e00-c8f8-438b-860a-b5eb427f88d8: running...") {
		t.Errorf("--follow printed --wait's status polls too:\n%s", s)
	}
}

// TestFollowFailedTaskExitsGeneral pins that a failed task under
// --follow reports the task's own error with ExitGeneral, after
// streaming the messages that led up to it.
func TestFollowFailedTaskExitsGeneral(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		withCatalog(followFlowHandler(
			"failed", "quota exceeded")))

	err := runAuthed(t, rt, out, url,
		"database", "create", "--name", "d",
		"--region", "us-east-2", "--size", "small",
		"--follow", "--wait-interval", "1", "--wait-timeout", "30")
	if err == nil {
		t.Fatal("create --follow: expected an error for a failed task")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Fatalf("want ExitGeneral, got %v", err)
	}
	if !strings.Contains(err.Error(), "quota exceeded") {
		t.Errorf("error does not carry the task's reason: %v", err)
	}
	if !strings.Contains(errb.String(), "Provisioning Database") {
		t.Errorf("messages before the failure were not streamed:\n%s",
			errb.String())
	}
}
