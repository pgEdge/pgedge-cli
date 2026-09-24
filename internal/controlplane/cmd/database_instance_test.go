package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const instTaskID = "44444444-4444-4444-4444-444444444444"

func instTaskResp(typ string) string {
	return `{"task":{"task_id":"` + instTaskID + `",` +
		`"status":"pending","type":"` + typ + `",` +
		`"scope":"database","entity_id":"storefront",` +
		`"created_at":"2026-06-18T00:00:00Z"}}`
}

// TestInstanceRestartJSONEmitsTask is the contract for one verb: under
// -o json, the accepted task must land on stdout as one parseable
// document, at .task.task_id/.task.status, while the stderr
// acceptance line stays exactly as it is under text mode.
func TestInstanceRestartJSONEmitsTask(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, instTaskResp("restart")))
	if err := runControlplane(t, rt, out, url, "database", "instance",
		"restart", "storefront", "inst-1", "--force"); err != nil {
		t.Fatalf("restart: %v", err)
	}

	var doc struct {
		Task struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout did not parse as one JSON object: %v (%q)",
			err, out.String())
	}
	if doc.Task.TaskID != instTaskID {
		t.Errorf("task.task_id = %q, want %q", doc.Task.TaskID, instTaskID)
	}
	if doc.Task.Status != "pending" {
		t.Errorf("task.status = %q, want %q", doc.Task.Status, "pending")
	}
	if !strings.Contains(errb.String(),
		"Restart task "+instTaskID+" accepted (pending).") {
		t.Errorf("want acceptance line on stderr: %q", errb.String())
	}
}

// TestInstanceLifecycleForceContract pins the contract the three
// lifecycle verbs share, re-drawn by the D4 split (2026-08-24):
// --force exists on exactly the service-interrupting verbs, where it
// skips the confirmation prompt and nothing else. start never
// prompts, so it carries no --force at all — its old --force was
// really the server's unmodifiable-state override, which all the
// wire-force verbs now spell --force-unmodifiable.
//
// A closed (non-terminal) stdin is what every test gets, so the
// no-force arm exercises cli.Confirm's non-interactive refusal — the
// path a script or an agent actually hits.
func TestInstanceLifecycleForceContract(t *testing.T) {
	for _, tc := range []struct {
		verb string
		// confirms is whether the verb interrupts service and so must
		// refuse to proceed without --force on a non-terminal stdin.
		confirms bool
	}{
		{"start", false},
		{"stop", true},
		{"restart", true},
	} {
		t.Run(tc.verb+"/force", func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(200, instTaskResp(tc.verb)))
			err := runControlplane(t, rt, out, url, "database", "instance",
				tc.verb, "storefront", "inst-1", "--force")
			if !tc.confirms {
				// No prompt means no --force: the spelling must be
				// refused, not quietly accepted as the override. The
				// harness sees cobra's raw parse error; the shipped
				// binary maps it to exit 2 (measured), which the
				// flattening tests own.
				if err == nil || !strings.Contains(err.Error(),
					"unknown flag") {
					t.Fatalf("%s --force: want unknown-flag refusal, "+
						"got %v", tc.verb, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s --force: %v", tc.verb, err)
			}
			if !strings.Contains(errb.String(), instTaskID) {
				t.Errorf("want task id in output: %q", errb.String())
			}
		})

		t.Run(tc.verb+"/no force", func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(200, instTaskResp(tc.verb)))
			err := runControlplane(t, rt, out, url, "database", "instance",
				tc.verb, "storefront", "inst-1")
			if tc.confirms {
				requireUsageError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.verb, err)
			}
			if !strings.Contains(errb.String(), instTaskID) {
				t.Errorf("want task id in output: %q", errb.String())
			}
		})
	}
}

// TestInstanceRestartScheduledAtBeatsThePrompt pins the ordering the
// restart Long text promises and that three comments describe: with no
// --force and a bad --scheduled-at, the flag is what fails, not the
// missing confirmation. Both paths return ExitUsage, so an exit-code
// assertion cannot separate them — swapping parseScheduledAt and
// cli.Confirm left every other test in the tree green — and only the
// message distinguishes which check ran first.
func TestInstanceRestartScheduledAtBeatsThePrompt(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, instTaskResp("restart")))
	err := runControlplane(t, rt, out, url, "database", "instance", "restart",
		"storefront", "inst-1", "--scheduled-at", "not-a-time")
	requireUsageError(t, err)
	if !strings.Contains(err.Error(), "invalid --scheduled-at") {
		t.Errorf("err = %v, want the --scheduled-at usage error", err)
	}
}
