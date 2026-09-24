package cmd

import (
	"strings"
	"testing"
)

// Valid UUID so the generated parser can decode Task.TaskId.
const nodeTaskID = "33333333-3333-3333-3333-333333333333"

func nodeTaskResp(typ string) string {
	return `{"task":{"task_id":"` + nodeTaskID + `",` +
		`"status":"pending","type":"` + typ + `",` +
		`"scope":"database","entity_id":"storefront",` +
		`"created_at":"2026-06-18T00:00:00Z"}}`
}

func TestDatabaseNodeBackupRun(t *testing.T) {
	t.Run("default type full", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, nodeTaskResp("backup")))
		if err := runControlplane(t, rt, out, url,
			"database", "node", "backup", "storefront", "n1"); err != nil {
			t.Fatalf("backup: %v", err)
		}
		if !strings.Contains(errb.String(), nodeTaskID) {
			t.Errorf("want task id in output: %q", errb.String())
		}
	})

	t.Run("rejects bad type", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, nodeTaskResp("backup")))
		if err := runControlplane(t, rt, out, url, "database", "node",
			"backup", "storefront", "n1", "--type", "bogus"); err == nil {
			t.Fatal("expected error on invalid --type")
		}
	})
}

func TestDatabaseNodeSwitchoverRun(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, nodeTaskResp("switchover")))
	if err := runControlplane(t, rt, out, url, "database", "node",
		"switchover", "storefront", "n1", "--force"); err != nil {
		t.Fatalf("switchover: %v", err)
	}
	if !strings.Contains(errb.String(), nodeTaskID) {
		t.Errorf("want task id in output: %q", errb.String())
	}
}

func TestDatabaseNodeSwitchoverBadSchedule(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, nodeTaskResp("switchover")))
	err := runControlplane(t, rt, out, url, "database", "node", "switchover",
		"storefront", "n1", "--scheduled-at", "not-a-time", "--force")
	requireUsageError(t, err)
	// The MESSAGE, not just the code: the prompt refusal is also
	// ExitUsage, so drop --force from this fixture and the assertion
	// would pass having tested the confirmation instead.
	if !strings.Contains(err.Error(), "--scheduled-at") {
		t.Errorf("error = %v, want the --scheduled-at usage error", err)
	}
}

func TestDatabaseNodeFailoverRun(t *testing.T) {
	t.Run("force runs", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, nodeTaskResp("failover")))
		if err := runControlplane(t, rt, out, url, "database", "node",
			"failover", "storefront", "n1", "--force"); err != nil {
			t.Fatalf("failover: %v", err)
		}
		if !strings.Contains(errb.String(), nodeTaskID) {
			t.Errorf("want task id in output: %q", errb.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, nodeTaskResp("failover")))
		err := runControlplane(t, rt, out, url, "database", "node",
			"failover", "storefront", "n1")
		requireUsageError(t, err)
	})
}

// switchover drops the connections the old leader holds, so a client
// notices -- the bar #253 set for this tree. It was the only
// service-interrupting controlplane verb with neither a prompt nor --force
// (#284).
func TestDatabaseNodeSwitchoverRefusesWithoutForce(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, nodeTaskResp("switchover")))
	err := runControlplane(t, rt, out, url, "database", "node",
		"switchover", "storefront", "n1")
	if err == nil {
		t.Fatal("switchover ran unattended with no --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want the destructive-verb refusal", err)
	}
}
