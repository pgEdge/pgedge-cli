package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// staleTaskID is the task that was already on the subject before the
// mutation. Every assertion below turns on whether it can be mistaken
// for the mutation's own task.
const staleTaskID = "11111111-1111-4111-8111-111111111111"

// freshTaskID is the task the mutation actually spawns.
const freshTaskID = "22222222-2222-4222-8222-222222222222"

// taskJSON renders one task the way GET /managed/v1/tasks does.
func taskJSON(id, status, createdAt string) string {
	return fmt.Sprintf(`{"id":%q,"name":"update-managed","status":%q,
		"subject_id":%q,"subject_kind":"database","messages":[],
		"created_at":%q,"updated_at":%q}`,
		id, status, testDatabaseID, createdAt, createdAt)
}

// failedBaselineHandler reproduces the bug: the pre-mutation task read
// returns 500 ONCE, and every later read succeeds.
//
// That is the whole reachability of the bug and it is why the stub
// fails exactly one read rather than a class of them: a persistent
// non-2xx would stop the mutation too, and auth or routing failures
// would never have got this far. What is left is a single transient
// error on a read issued immediately after a database GET that worked.
//
// includeFresh decides whether the mutation's own task ever appears.
// With it false the subject has nothing but the stale task, which is
// the case the CLI has to refuse rather than report.
func failedBaselineHandler(includeFresh bool) http.HandlerFunc {
	var mu sync.Mutex
	baselineFailed := false
	mutated := false

	// Old enough that no plausible clock skew reaches it, and it
	// mirrors the stale-task bug, whose newest task was two days
	// old.
	stale := taskJSON(staleTaskID, "succeeded", "2026-08-05T00:00:00Z")

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if id := r.URL.Query().Get("id"); id != "" {
				// A poll by id. Only the fresh task is ever polled;
				// the stale one being polled at all is the defect, so
				// answering for it is what lets the test SEE the
				// defect rather than erroring on it.
				status := "succeeded"
				created := time.Now().UTC().Format(time.RFC3339)
				if id == staleTaskID {
					created = "2026-08-05T00:00:00Z"
				}
				_, _ = w.Write([]byte(
					"[" + taskJSON(id, status, created) + "]"))
				return
			}
			mu.Lock()
			first := !baselineFailed
			baselineFailed = true
			done := mutated
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal"}`))
				return
			}
			if done && includeFresh {
				// created_at is stamped at read time, so the fresh
				// task is genuinely newer than the floor rather than
				// newer than a hardcoded date that will age.
				fresh := taskJSON(freshTaskID, "succeeded",
					time.Now().UTC().Format(time.RFC3339))
				_, _ = w.Write([]byte(
					"[" + fresh + "," + stale + "]"))
				return
			}
			_, _ = w.Write([]byte("[" + stale + "]"))

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

// TestWaitRefusesAStaleTaskWhenTheBaselineReadFailed is the gate for
// a failed baseline read, and it FAILS on the code this replaced.
//
// Before the fix a failed baseline read left no baseline at all, so
// discovery accepted the first task it saw. On a database with history
// that is an old task, and the command exited 0 reporting a success it
// never watched -- in one polling interval, on a write that had barely
// been submitted.
//
// The assertion is deliberately on all three of the exit code, the
// absence of the stale id, and the absence of the word "succeeded":
// the defect's signature is a SUCCESS, so a test that only pinned the
// exit code would pass against a version that printed
// "Task <stale>: succeeded." and then timed out for its own reasons.
func TestWaitRefusesAStaleTaskWhenTheBaselineReadFailed(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		withCatalog(failedBaselineHandler(false)))
	err := runAuthed(t, rt, out, url,
		"database", "resize", testDatabaseID, "--size", "large",
		"--force", "--wait", "--wait-interval", "1", "--wait-timeout", "2")
	if err == nil {
		t.Fatalf("expected a timeout, got success; stderr=%q",
			errb.String())
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitTimeout {
		t.Fatalf("want ExitTimeout, got %v", err)
	}
	if strings.Contains(errb.String(), staleTaskID) {
		t.Errorf("tracked the stale task: %q", errb.String())
	}
	if strings.Contains(errb.String(), "succeeded") {
		t.Errorf("reported a success it never watched: %q",
			errb.String())
	}
	// The floor is the only reason this timed out on a subject whose
	// tasks were readable throughout, so the message has to say so --
	// and it is the only way an operator can tell this apart from a
	// clock skewed further than baselineClockMargin.
	if !strings.Contains(err.Error(), "created at or after") {
		t.Errorf("timeout does not name the floor: %v", err)
	}
	if !strings.Contains(err.Error(), "task read failed") {
		t.Errorf("timeout does not name the cause: %v", err)
	}
}

// TestWaitFindsTheRealTaskAfterAFailedBaselineRead is the other half:
// the floor must not turn a recoverable failed read into a wait that
// can never succeed.
//
// This one passes both before and after the fix, and is here for what
// it would catch LATER -- a floor stamped after the mutation instead
// of before it, or a margin too tight for the API's one-second
// created_at resolution, would both show up here and nowhere else.
func TestWaitFindsTheRealTaskAfterAFailedBaselineRead(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		withCatalog(failedBaselineHandler(true)))
	err := runAuthed(t, rt, out, url,
		"database", "resize", testDatabaseID, "--size", "large",
		"--force", "--wait", "--wait-interval", "1", "--wait-timeout", "30")
	if err != nil {
		t.Fatalf("resize --wait: %v (stderr=%q)", err, errb.String())
	}
	if !strings.Contains(errb.String(), freshTaskID) {
		t.Errorf("did not track the fresh task: %q", errb.String())
	}
	if strings.Contains(errb.String(), staleTaskID) {
		t.Errorf("tracked the stale task: %q", errb.String())
	}
}

// TestTaskBaselineAccepts covers the decision itself, including the
// two shapes no stub reaches: a created_at the API has never sent, and
// the boundary second.
func TestTaskBaselineAccepts(t *testing.T) {
	floor := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	task := func(id, createdAt string) api.Task {
		return api.Task{Id: id, CreatedAt: createdAt}
	}
	cases := []struct {
		name string
		base taskBaseline
		t    api.Task
		want bool
	}{{
		// The healthy path, unchanged: ids decide and no clock is
		// consulted. The stale created_at is the point -- every wait
		// fixture in this package carries one, and a floor that
		// leaked onto this path would refuse them all.
		name: "capture worked, different id, ancient created_at",
		base: taskBaseline{priorID: staleTaskID},
		t:    task(freshTaskID, "2026-08-05T00:00:00Z"),
		want: true,
	}, {
		name: "capture worked, same id",
		base: taskBaseline{priorID: staleTaskID},
		t:    task(staleTaskID, "2026-08-05T00:00:00Z"),
		want: false,
	}, {
		// A subject with no tasks at capture time: priorID is "" and
		// no real id can equal it.
		name: "capture worked, subject had no tasks",
		base: taskBaseline{},
		t:    task(freshTaskID, "2026-08-05T00:00:00Z"),
		want: true,
	}, {
		name: "capture failed, task older than the floor",
		base: taskBaseline{notBefore: floor},
		t:    task(staleTaskID, "2026-08-22T11:59:59Z"),
		want: false,
	}, {
		name: "capture failed, task at the floor",
		base: taskBaseline{notBefore: floor},
		t:    task(freshTaskID, "2026-08-22T12:00:00Z"),
		want: true,
	}, {
		name: "capture failed, task after the floor",
		base: taskBaseline{notBefore: floor},
		t:    task(freshTaskID, "2026-08-22T12:00:01Z"),
		want: true,
	}, {
		// A non-UTC offset is still RFC3339, and the comparison is on
		// the instant rather than the text: 13:30+02:00 is 11:30Z,
		// which is BEFORE a noon floor even though it sorts after it
		// as a string. The floor and newestOf agree on this now — both
		// compare instants — so what this pins is the FLOOR's own
		// parse, not a disagreement between them.
		name: "capture failed, offset timestamp before the floor",
		base: taskBaseline{notBefore: floor},
		t:    task(staleTaskID, "2026-08-22T13:30:00+02:00"),
		want: false,
	}, {
		// A shape the API has never sent must not be able to make
		// waiting impossible, so this falls back to the no-baseline
		// behaviour rather than refusing everything.
		name: "capture failed, unreadable created_at",
		base: taskBaseline{notBefore: floor},
		t:    task(freshTaskID, "not a timestamp"),
		want: true,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, degraded := tc.base.accepts(tc.t)
			if got != tc.want {
				t.Errorf("accepts(%+v) = %v, want %v",
					tc.t, got, tc.want)
			}
			// degraded must be reported for exactly the unreadable
			// case and no other, because it is what puts a warning on
			// stderr: reported too widely it becomes noise, and not at
			// all it is a silent global off-switch for the floor.
			wantDegraded := tc.t.CreatedAt == "not a timestamp" &&
				!tc.base.notBefore.IsZero()
			if degraded != wantDegraded {
				t.Errorf("degraded = %v, want %v for created_at %q",
					degraded, wantDegraded, tc.t.CreatedAt)
			}
		})
	}
}

// TestBaselineClockMarginIsPinnedAtBothEnds is the gate the margin did
// not have, and its absence let two mutations through untouched.
//
// Review set baselineClockMargin to 0 and to 240 hours; the whole
// package passed both times, 645 subtests, because every other test
// builds a taskBaseline from a literal floor and never reads the
// constant. That is the "round-number floor with slack" pattern: the
// value was unconstrained across at least ten days.
//
// So this builds the baseline the way captureTaskBaseline does — from
// the constant — and squeezes it from both sides. Neither bound is
// arbitrary: the lower one is the API's own one-second created_at
// truncation, the upper one is a chained write on the same database,
// which is the case the margin actually exposes.
func TestBaselineClockMarginIsPinnedAtBothEnds(t *testing.T) {
	now := time.Now().UTC()
	base := taskBaseline{notBefore: now.Add(-baselineClockMargin)}

	// TOO TIGHT. The API truncates created_at to the second, so this
	// write's own task can report the second before the attempt. A
	// margin that cannot absorb that rejects the real task and the
	// wait runs to its timeout.
	justBefore := api.Task{Id: freshTaskID,
		CreatedAt: now.Add(-2 * time.Second).Format(time.RFC3339)}
	if ok, _ := base.accepts(justBefore); !ok {
		t.Errorf("baselineClockMargin = %v is too tight: it rejects "+
			"a task created two seconds before the capture, which is "+
			"what one-second truncation and any clock skew produce",
			baselineClockMargin)
	}

	// TOO LOOSE. A chained script leaves a task from a minute or two
	// ago on the same subject -- `mcp deploy` without --wait, then
	// `rag deploy --wait`. The id path excludes it exactly; an age
	// floor can only exclude it by being smaller than the gap.
	//
	// Ninety seconds rather than five minutes, deliberately: five
	// squeezed the margin only to under five minutes, so four would
	// still have passed. This pins it to under 90s, which is the scale
	// a chained write actually happens on.
	ninetySecondsOld := api.Task{Id: staleTaskID,
		CreatedAt: now.Add(-90 * time.Second).Format(time.RFC3339)}
	if ok, _ := base.accepts(ninetySecondsOld); ok {
		t.Errorf("baselineClockMargin = %v is too loose: it accepts a "+
			"task from ninety seconds ago as this write's, which is "+
			"the scale a chained write leaves one on",
			baselineClockMargin)
	}
}

// TestCaptureTaskBaselineCostsNoReadWithoutWaiting pins the property
// the baseline capture has to keep: it is a round trip taken only
// because the caller asked to wait.
func TestCaptureTaskBaselineCostsNoReadWithoutWaiting(t *testing.T) {
	reads := 0
	url := testsupport.NewAuthedServer(t,
		withCatalog(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "/tasks") {
				reads++
			}
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		}))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url,
		"database", "resize", testDatabaseID, "--size", "large",
		"--force"); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if reads != 0 {
		t.Errorf("read the task list %d times without --wait", reads)
	}
}

// TestATwoXXWithNoReadableListIsNotAnEmptyList is the gate for the
// third baseline state, a 2xx with no readable list.
//
// ParseListManagedTasksResponse sets JSON200 only when the
// Content-Type contains "json" AND the status is exactly 200, and its
// catch-all arm is `Contains(Content-Type, "json") && true`, which
// unmarshals into an Error. So a 2xx whose Content-Type does NOT
// contain "json" leaves JSON200 nil with no error, and so does a
// non-200 json 2xx whose body is a JSON object or null; an empty json
// 204 or 202 fails at unmarshal instead. A gateway answering 204 with
// no Content-Type header, or 200 text/plain, is the real case.
//
// checkResponse accepts any 2xx, so without the guard that reaches
// captureTaskBaseline as `prior == nil`, read as "the subject has no
// tasks": no floor is set, discovery accepts the first task it sees,
// and `--wait` reports a stale success. A floor scoped to `err != nil`
// misses it, since this failure mode arrives without an error.
//
// The distinction the fix rests on: a genuinely empty list is
// `200 application/json []`, which parses to a non-nil pointer at
// length zero. Only an unreadable body yields nil.
func TestATwoXXWithNoReadableListIsNotAnEmptyList(t *testing.T) {
	stale := taskJSON(staleTaskID, "succeeded", "2026-08-05T00:00:00Z")
	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		// EVERY row must lack "json" in its Content-Type, and that is
		// not cosmetic. The generated parser's catch-all is
		// `Contains(Content-Type, "json") && true`, which unmarshals
		// whatever arrives into an Error — so a 204 or a 202 sent AS
		// json fails at unmarshal and returns an ordinary error,
		// exiting at the pre-existing `if err != nil` without ever
		// reaching the guard this test exists for. A first version of
		// this fixture did exactly that: two of its three rows passed
		// against the un-fixed code.
		{"200 with text/plain", 200, "text/plain", "OK"},
		{"204 with no content type", 204, "", ""},
		{"202 with text/html", 202, "text/html", "<html>accepted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			baselineDone := false
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, withCatalog(
				func(w http.ResponseWriter, r *http.Request) {
					if !strings.Contains(r.URL.Path, "/tasks") {
						w.Header().Set("Content-Type",
							"application/json")
						_, _ = w.Write([]byte(
							databaseJSON(testDatabaseID, "")))
						return
					}
					if r.URL.Query().Get("id") != "" {
						w.Header().Set("Content-Type",
							"application/json")
						_, _ = w.Write([]byte("[" + stale + "]"))
						return
					}
					mu.Lock()
					first := !baselineDone
					baselineDone = true
					mu.Unlock()
					if first {
						// The unreadable 2xx, on the capture only.
						w.Header().Set("Content-Type", tc.contentType)
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.body))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte("[" + stale + "]"))
				}))
			err := runAuthed(t, rt, out, url,
				"database", "resize", testDatabaseID, "--size",
				"large", "--force", "--wait", "--wait-interval", "1",
				"--wait-timeout", "2")
			if err == nil {
				t.Fatalf("reported success on a stale task; stderr=%q",
					errb.String())
			}
			if strings.Contains(errb.String(), "succeeded") {
				t.Errorf("reported a success it never watched: %q",
					errb.String())
			}
			if strings.Contains(errb.String(), staleTaskID) {
				t.Errorf("tracked the stale task: %q", errb.String())
			}
		})
	}
}

// TestAnEmptyTaskListIsStillAnEmptyList is the inverse of the gate
// above, and it is what stops the fix turning a normal answer into an
// error. A freshly created database has no tasks, and that must remain
// a baseline of "nothing captured" rather than a failure.
func TestAnEmptyTaskListIsStillAnEmptyList(t *testing.T) {
	var mu sync.Mutex
	mutated := false
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, withCatalog(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(r.URL.Path, "/tasks"):
				fresh := taskJSON(freshTaskID, "succeeded",
					time.Now().UTC().Format(time.RFC3339))
				if r.URL.Query().Get("id") != "" {
					_, _ = w.Write([]byte("[" + fresh + "]"))
					return
				}
				mu.Lock()
				done := mutated
				mu.Unlock()
				if done {
					_, _ = w.Write([]byte("[" + fresh + "]"))
					return
				}
				// The capture: a real, readable, EMPTY list.
				_, _ = w.Write([]byte(`[]`))
			case r.Method != http.MethodGet:
				mu.Lock()
				mutated = true
				mu.Unlock()
				_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
			default:
				_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
			}
		}))
	if err := runAuthed(t, rt, out, url,
		"database", "resize", testDatabaseID, "--size", "large",
		"--force", "--wait", "--wait-interval", "1", "--wait-timeout", "30",
	); err != nil {
		t.Fatalf("an empty capture broke waiting: %v (stderr=%q)",
			err, errb.String())
	}
	if !strings.Contains(errb.String(), freshTaskID) {
		t.Errorf("did not track the task: %q", errb.String())
	}
}

// TestNewestOfIsATotalOrder covers the cycle review supplied, which the
// per-pair comparator this replaced could not answer consistently.
//
// A beats B by instant, B beats C by string, C beats A by string. A
// linear max-scan over a non-transitive comparator returns whichever
// element the array order favours, so the same three tasks in two
// orders gave two different answers. Deciding the mode ONCE for the
// slice restores the property; the assertion is that every permutation
// agrees, not that any particular task wins.
func TestNewestOfIsATotalOrder(t *testing.T) {
	a := api.Task{Id: "A", CreatedAt: "2026-08-22T12:30:00-01:00"}
	b := api.Task{Id: "B", CreatedAt: "2026-08-22T13:00:00Z"}
	c := api.Task{Id: "C", CreatedAt: "2026-08-22T12:45:00"}
	perms := [][]api.Task{
		{a, b, c}, {a, c, b}, {b, a, c},
		{b, c, a}, {c, a, b}, {c, b, a},
	}
	var first string
	for i, p := range perms {
		got := newestOf(p)
		if got == nil {
			t.Fatalf("permutation %d returned nil", i)
		}
		if i == 0 {
			first = got.Id
			continue
		}
		if got.Id != first {
			t.Errorf("permutation %d chose %s, permutation 0 chose "+
				"%s: the comparator is not a total order",
				i, got.Id, first)
		}
	}

	// With every value parseable, the INSTANT decides -- which is the
	// half the string comparison got wrong. 12:30-01:00 is 13:30Z, so
	// it is later than 13:00Z despite sorting earlier as text.
	if got := newestOf([]api.Task{a, b}); got == nil || got.Id != "A" {
		t.Errorf("parseable pair chose %v, want A: the offset was not "+
			"resolved to an instant", got)
	}

	// And the string fallback still answers when nothing parses,
	// rather than returning nothing.
	x := api.Task{Id: "X", CreatedAt: "not a time"}
	y := api.Task{Id: "Y", CreatedAt: "zzz also not a time"}
	if got := newestOf([]api.Task{x, y}); got == nil || got.Id != "Y" {
		t.Errorf("unparseable pair chose %v, want Y", got)
	}

	if newestOf(nil) != nil {
		t.Error("an empty slice should have no newest task")
	}
}
