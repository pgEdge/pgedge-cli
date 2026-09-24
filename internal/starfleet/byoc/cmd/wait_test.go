package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// tasksMux returns a handler that serves ListTasks requests.
// Requests with an "id" query are answered by byID; all others
// (subject_id filters) return subjectTasks.
func tasksMux(
	subjectTasks []api.Task,
	byID func(id string) (api.Task, bool),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if id := r.URL.Query().Get("id"); id != "" {
			if t, ok := byID(id); ok {
				_ = json.NewEncoder(w).Encode([]api.Task{t})
				return
			}
			_ = json.NewEncoder(w).Encode([]api.Task{})
			return
		}
		_ = json.NewEncoder(w).Encode(subjectTasks)
	}
}

func TestNewestSubjectTaskID(t *testing.T) {
	tests := []struct {
		name  string
		tasks []api.Task
		want  string
	}{
		{
			name:  "no tasks",
			tasks: []api.Task{},
			want:  "",
		},
		{
			name: "picks latest by created_at regardless of order",
			tasks: []api.Task{
				{Id: "b", CreatedAt: "2026-06-25T01:00:00Z"},
				{Id: "c", CreatedAt: "2026-06-25T03:00:00Z"},
				{Id: "a", CreatedAt: "2026-06-25T02:00:00Z"},
			},
			want: "c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, tasksMux(tt.tasks, nil))
			got, err := newestSubjectTaskID(context.Background(), client, "subj")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrackMutation_NonWaitHint(t *testing.T) {
	origWait := waitFlag
	t.Cleanup(func() { waitFlag = origWait })
	waitFlag = false // exercise the non-wait path; no client needed

	t.Run("table prints monitor hint", func(t *testing.T) {
		rt, _, stderr := testsupport.NewRuntime(t, "", "table")
		if err := trackMutation(rt, nil, "subj-1", ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(),
			"byoc task list --subject-id subj-1") {
			t.Errorf("expected monitor hint, got: %q", stderr.String())
		}
	})

	t.Run("json output stays silent", func(t *testing.T) {
		rt, _, stderr := testsupport.NewRuntime(t, "", "json")
		if err := trackMutation(rt, nil, "subj-1", ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stderr.String() != "" {
			t.Errorf("expected no output in json mode, got: %q",
				stderr.String())
		}
	})
}

func TestWaitForSubjectTask_Succeeds(t *testing.T) {
	subjectTasks := []api.Task{
		{Id: "e9562e00-c8f8-438b-860a-b5eb427f88d8", CreatedAt: "2026-06-25T02:00:00Z"},
		{Id: "a93e58c4-d425-4233-adf4-9b668d1eaece", CreatedAt: "2026-06-25T01:00:00Z"},
	}
	byID := func(id string) (api.Task, bool) {
		return api.Task{Id: id, Status: "succeeded"}, true
	}

	client := newTestClient(t, tasksMux(subjectTasks, byID))
	rt, _, stderr := testsupport.NewRuntime(t, "", "text")

	// interval 0 is clamped to 1s, so a single poll keeps the test fast.
	err := waitForSubjectTask(rt, client, "subj", "a93e58c4-d425-4233-adf4-9b668d1eaece", 10, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "Tracking task e9562e00-c8f8-438b-860a-b5eb427f88d8") {
		t.Errorf("expected discovery line, got: %q", out)
	}
	if !strings.Contains(out, "Task e9562e00-c8f8-438b-860a-b5eb427f88d8: succeeded") {
		t.Errorf("expected success line, got: %q", out)
	}
}

func TestWaitForSubjectTask_Fails(t *testing.T) {
	subjectTasks := []api.Task{
		{Id: "e9562e00-c8f8-438b-860a-b5eb427f88d8", CreatedAt: "2026-06-25T02:00:00Z"},
		{Id: "a93e58c4-d425-4233-adf4-9b668d1eaece", CreatedAt: "2026-06-25T01:00:00Z"},
	}
	errMsg := "node unreachable"
	byID := func(id string) (api.Task, bool) {
		return api.Task{Id: id, Status: "failed", Error: &errMsg}, true
	}

	client := newTestClient(t, tasksMux(subjectTasks, byID))
	rt, _, _ := testsupport.NewRuntime(t, "", "text")

	err := waitForSubjectTask(rt, client, "subj", "a93e58c4-d425-4233-adf4-9b668d1eaece", 10, 0, false)
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitGeneral)
	}
	if !strings.Contains(ee.Error(), errMsg) {
		t.Errorf("expected error detail %q in %q", errMsg, ee.Error())
	}
}

func TestWaitForSubjectTask_TimesOutWhenNoNewTask(t *testing.T) {
	// Only the prior task is ever present, so no new task is discovered.
	subjectTasks := []api.Task{
		{Id: "a93e58c4-d425-4233-adf4-9b668d1eaece", CreatedAt: "2026-06-25T01:00:00Z"},
	}

	client := newTestClient(t, tasksMux(subjectTasks, nil))
	rt, _, _ := testsupport.NewRuntime(t, "", "text")

	// 1s timeout: one poll finds no new task, then the deadline trips.
	err := waitForSubjectTask(rt, client, "subj", "a93e58c4-d425-4233-adf4-9b668d1eaece", 1, 0, false)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitTimeout {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitTimeout)
	}
}

// forgingTaskError carries every shape that forges a line — a newline,
// a tab and an ANSI CSI — in ONE value, so a fix handling only the
// newline still fails. The tail is what an operator would read as the
// CLI's own report of a healthy task.
const forgingTaskError = "boom\nTask a93e58c4: succeeded\tSTATUS" +
	"\tHEALTHY\x1b[2K"

// TestWaitForSubjectTaskEscapesAForgedTaskError covers byoc's copy of
// the failed-task interpolation. managed has the equivalent test; a
// review mutation showed byoc's wrap was defended by nothing, so
// unwrapping it passed every gate in the repo.
//
// The task `error` field is a plain *string in the generated client and
// nothing constrains its shape.
func TestWaitForSubjectTaskEscapesAForgedTaskError(t *testing.T) {
	subjectTasks := []api.Task{
		{Id: "e9562e00-c8f8-438b-860a-b5eb427f88d8",
			CreatedAt: "2026-06-25T02:00:00Z"},
		{Id: "a93e58c4-d425-4233-adf4-9b668d1eaece",
			CreatedAt: "2026-06-25T01:00:00Z"},
	}
	errMsg := forgingTaskError
	byID := func(id string) (api.Task, bool) {
		return api.Task{Id: id, Status: "failed", Error: &errMsg}, true
	}

	client := newTestClient(t, tasksMux(subjectTasks, byID))
	rt, _, _ := testsupport.NewRuntime(t, "", "text")

	err := waitForSubjectTask(rt, client, "subj",
		"a93e58c4-d425-4233-adf4-9b668d1eaece", 10, 0, false)
	if err == nil {
		t.Fatal("expected error for failed task")
	}
	got := err.Error()

	for name, bad := range map[string]string{
		"newline inside the value": "boom\n",
		"raw tab":                  "\t",
		"ANSI escape":              "\x1b",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("%s survived into the error:\n%q", name, got)
		}
	}
	// The escaped forms must be there, or the value was DROPPED rather
	// than escaped — which passes the checks above while losing the
	// operator's only evidence of what failed.
	for _, want := range []string{`\n`, `\t`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the escaped form %s in:\n%q", want, got)
		}
	}
}

// TestNewestSubjectTaskIDRanksByInstant pins the offset and
// fractional-second cases the string comparison got WRONG. Each of
// these has the newest INSTANT sorting earlier as a string, so a
// lexicographic ranking returns the other task -- and because only the
// newest is returned, the real one is then never considered at all.
func TestNewestSubjectTaskIDRanksByInstant(t *testing.T) {
	tests := map[string]struct {
		tasks []api.Task
		want  string
	}{
		// 14:00+02:00 is 12:00Z, so it is OLDER than 13:00Z while
		// sorting after it as a string.
		"a non-UTC offset": {
			tasks: []api.Task{
				{Id: "older", CreatedAt: "2026-06-25T14:00:00+02:00"},
				{Id: "newer", CreatedAt: "2026-06-25T13:00:00Z"},
			},
			want: "newer",
		},
		// `.` sorts before `Z`, so the fractional value loses the
		// string comparison despite being the later instant.
		"a fractional second": {
			tasks: []api.Task{
				{Id: "older", CreatedAt: "2026-06-25T13:00:00Z"},
				{Id: "newer", CreatedAt: "2026-06-25T13:00:00.500Z"},
			},
			want: "newer",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, tasksMux(tt.tasks, nil))
			got, err := newestSubjectTaskID(
				context.Background(), client, "subj")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q -- ranking by string rather "+
					"than by instant returns the wrong task, and only "+
					"the newest is returned, so the real one is never "+
					"considered", got, tt.want)
			}
		})
	}
}

// TestNewestSubjectTaskIDRefusesAnUnreadable2xx pins this state: a
// 2xx the generated client cannot parse leaves the typed body nil with
// NO error, and collapsing that into "" makes it indistinguishable
// from "this subject has no tasks". Discovery then accepts the first
// task it sees, which on a database with history is an old one -- so
// --wait can exit 0 reporting a months-old success for a write it
// never watched.
//
// byoc's settled design is to propagate a failed pre-mutation read and
// refuse the write. This state produced no error, so that design never
// covered it; an error here puts it back under the design.
func TestNewestSubjectTaskIDRefusesAnUnreadable2xx(t *testing.T) {
	// The two shapes worth fixturing. A json Content-Type with a
	// non-200 2xx usually fails at unmarshal instead — though one
	// carrying a JSON object reaches this branch too, and correctly.
	cases := map[string]struct {
		status      int
		contentType string
		body        string
	}{
		"a 204 with no Content-Type": {http.StatusNoContent, "", ""},
		"a 200 carrying text/plain": {
			http.StatusOK, "text/plain", "accepted"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, func(
				w http.ResponseWriter, r *http.Request,
			) {
				if c.contentType != "" {
					w.Header().Set("Content-Type", c.contentType)
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})

			got, err := newestSubjectTaskID(
				context.Background(), client, "subj")
			if err == nil {
				t.Fatalf("HTTP %d with Content-Type %q returned %q and "+
					"NO error; an unreadable 2xx must not read as an "+
					"empty task list", c.status, c.contentType, got)
			}
			if got != "" {
				t.Errorf("got %q alongside the error, want \"\"", got)
			}
			if !strings.Contains(err.Error(), "no readable task list") {
				t.Errorf("error = %q, want it to name the condition",
					err)
			}
		})
	}
}

// TestNewestSubjectTaskIDIsOrderIndependent is the totality test, and
// it is the one a per-pair fallback fails. These three values are the
// cycle: A=12:30:00-01:00 is 13:30Z so it beats B=13:00:00Z by INSTANT,
// B beats C=12:45:00 (no zone, unparseable) by STRING, and C beats A by
// STRING. A comparator that picks its mode per PAIR is therefore not a
// total order, and a linear max-scan over it returns whichever answer
// the array order happens to favour.
//
// Deciding the mode ONCE for the slice is what makes the answer the
// same for every permutation, which is what this asserts. A test on a
// single ordering cannot see the difference -- it just reads whatever
// that ordering produces and calls it correct.
func TestNewestSubjectTaskIDIsOrderIndependent(t *testing.T) {
	a := api.Task{Id: "a", CreatedAt: "2026-06-25T12:30:00-01:00"}
	b := api.Task{Id: "b", CreatedAt: "2026-06-25T13:00:00Z"}
	c := api.Task{Id: "c", CreatedAt: "2026-06-25T12:45:00"}

	perms := [][]api.Task{
		{a, b, c}, {a, c, b}, {b, a, c},
		{b, c, a}, {c, a, b}, {c, b, a},
	}

	var first string
	for i, tasks := range perms {
		client := newTestClient(t, tasksMux(tasks, nil))
		got, err := newestSubjectTaskID(
			context.Background(), client, "subj")
		if err != nil {
			t.Fatalf("permutation %d: unexpected error: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("permutation %d returned %q where the first "+
				"returned %q: the ranking depends on array order, so "+
				"the comparator is not a total order", i, got, first)
		}
	}

	// And name the answer, so a change of mode is visible rather than
	// merely consistent. C does not parse, so the whole slice ranks as
	// strings, and "13:00:00Z" is the greatest of the three.
	if first != "b" {
		t.Errorf("newest = %q, want \"b\": with one unparseable value "+
			"the whole slice ranks lexicographically", first)
	}
}

// TestForgedTaskFieldsCannotForgeADetailLine is byoc's copy of the
// managed test. printTaskDetail is written twice, once per module, and a
// mutation showed a single-route test leaves the other copy
// undefended — so each module pins its own.
//
// This block goes to STDOUT, under a table the renderer already
// sanitized, which is the stream a caller parses.
func TestForgedTaskFieldsCannotForgeADetailLine(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")

	errText := "the real cause"
	task := api.Task{
		Id:        "e9562e00-c8f8-438b-860a-b5eb427f88d8",
		Name:      "create-database",
		Status:    "failed",
		CreatedAt: forgingTaskError,
		UpdatedAt: "2026-06-25T02:00:00Z",
		Error:     &errText,
		Messages: []api.Message{{
			Level: "info",
			Text:  forgingTaskError,
			Time:  "2026-06-25T02:00:00Z",
		}},
	}
	if err := printTaskDetail(rt, &task); err != nil {
		t.Fatalf("printTaskDetail: %v", err)
	}

	got := out.String()
	for name, bad := range map[string]string{
		"newline inside the value": "boom\n",
		"raw tab":                  "\t",
		"ANSI escape":              "\x1b",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("%s survived onto stdout:\n%q", name, got)
		}
	}
	// Escaped rather than DROPPED. Without this, replacing the value
	// with "" passes every check above — the labels come from the
	// format string, so the count assertion cannot see a lost value.
	// Review proved exactly that: managed's copy failed on this
	// assertion and byoc's passed without it.
	for _, want := range []string{`\n`, `\t`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the escaped form %s in:\n%q", want, got)
		}
	}
	// One line each, so a forged newline shows up as a repeat.
	for _, field := range []string{"Created", "Updated", "Step"} {
		if n := strings.Count(got, field); n != 1 {
			t.Errorf("%q appears %d times, want 1:\n%q",
				field, n, got)
		}
	}
}
