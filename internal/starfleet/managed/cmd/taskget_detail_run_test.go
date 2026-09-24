package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// taskWithStepsJSON is a task carrying a step trace, shaped like the
// live API's: each step is reported twice, once `running` and once
// `succeeded`, so the newest entry is the one worth showing. Captured
// from a real create-managed task on devapi, trimmed to four messages.
const taskWithStepsJSON = `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"create-managed",
	"status":"succeeded","subject_id":"db-1","subject_kind":"database",
	"messages":[
	{"level":"info","progress":5,"status":"running",
	 "step":"configure-system","text":"Configuring System",
	 "time":"2026-08-18T00:14:28Z"},
	{"level":"info","progress":15,"status":"succeeded",
	 "step":"configure-system","text":"Configuring System",
	 "time":"2026-08-18T00:14:28Z"},
	{"level":"info","progress":95,"status":"running",
	 "step":"configure-connection",
	 "text":"Configuring Database Connection",
	 "time":"2026-08-18T00:15:08Z"},
	{"level":"info","progress":100,"status":"succeeded",
	 "step":"configure-connection",
	 "text":"Configuring Database Connection",
	 "time":"2026-08-18T00:15:08Z"}],
	"created_at":"2026-08-18T00:14:25Z",
	"updated_at":"2026-08-18T00:15:08Z"}]`

// taskFailedJSON is a failed task carrying a reason. The reason is the
// single most useful thing `task get` has and text mode withheld it.
const taskFailedJSON = `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"restore-managed",
	"status":"failed","subject_id":"db-1","subject_kind":"database",
	"error":"pre-restore snapshot timed out after 900s",
	"messages":[{"level":"error","status":"failed",
	 "step":"snapshot","text":"Pre-Restore Snapshot",
	 "time":"2026-08-18T00:20:00Z"}],
	"created_at":"2026-08-18T00:14:25Z",
	"updated_at":"2026-08-18T00:20:00Z"}]`

// TestManagedTaskGetShowsDetail pins the detail block from #180.
//
// `task get`'s text output used to be one summary row, which told a
// reader less about a single task than `task list` tells them about
// every task. The step, the progress and — on a failure — the reason
// were reachable only through `-o json`.
//
// The assertions are on what a reader acts on rather than on the whole
// block, so re-spacing the labels does not fail. The negative
// assertions are the ones that matter: only the LATEST step is shown,
// and a task with no error grows no Error line.
func TestManagedTaskGetShowsDetail(t *testing.T) {
	t.Run("latest step, progress and full times", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, taskWithStepsJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		s := out.String()

		for _, want := range []string{
			"Configuring Database Connection",
			"succeeded",
			"100%",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("detail block is missing %q:\n%s", want, s)
			}
		}

		// The row's CREATED column is a date alone, so the block
		// prints the times in full — the whole point of the aside on
		// #180, that two tasks minutes apart are otherwise
		// indistinguishable.
		if !strings.Contains(s, "2026-08-18T00:14:25Z") {
			t.Errorf("no full created timestamp:\n%s", s)
		}
		if !strings.Contains(s, "2026-08-18T00:15:08Z") {
			t.Errorf("no full updated timestamp:\n%s", s)
		}

		// Only the newest message. An implementation that printed the
		// whole trace would pass every assertion above.
		if strings.Contains(s, "Configuring System") {
			t.Errorf("printed an earlier step; only the latest "+
				"belongs here:\n%s", s)
		}
		if strings.Contains(s, "Error") {
			t.Errorf("a task with no error grew an Error line:\n%s", s)
		}
	})

	t.Run("a failed task shows its reason", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, taskFailedJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		s := out.String()

		if !strings.Contains(s,
			"pre-restore snapshot timed out after 900s") {
			t.Errorf("the failure reason is still withheld:\n%s", s)
		}
		// The level is not "info", so it is tagged — that is what
		// distinguishes a failed step from a running one at a glance.
		if !strings.Contains(s, "[error]") {
			t.Errorf("the failed step is not tagged:\n%s", s)
		}
	})

	// The reason is trimmed before printing, following controlplane's
	// printTaskError. saas wraps these as chains of %w and one arriving
	// with surrounding whitespace would otherwise push a blank line into
	// the middle of the block. Without this case the TrimSpace pins
	// nothing.
	t.Run("a padded reason is trimmed", func(t *testing.T) {
		const padded = `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"restore-managed",
			"status":"failed","subject_id":"db-1",
			"subject_kind":"database",
			"error":"\n  restore failed: node unreachable\n\n",
			"messages":[],"created_at":"2026-08-18T00:14:25Z",
			"updated_at":"2026-08-18T00:20:00Z"}]`

		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, padded)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		s := out.String()

		if !strings.Contains(s, "restore failed: node unreachable") {
			t.Fatalf("the reason is missing:\n%q", s)
		}
		if strings.Contains(s, "\n\n\n") {
			t.Errorf("the padding was not trimmed:\n%q", s)
		}
		if !strings.HasSuffix(s, "node unreachable\n") {
			t.Errorf("trailing padding survived:\n%q", s)
		}
	})

	t.Run("a task with no messages prints no step", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, tasksJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		if strings.Contains(out.String(), "Step") {
			t.Errorf("empty messages produced a Step line:\n%s",
				out.String())
		}
	})
}
