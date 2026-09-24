package cmd

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// waitFlowHandler simulates a resource mutation that spawns a task.
// Before the mutation, the subject has no tasks (prior task id is
// empty); after it, a new task appears and reports success. Task-by-id
// lookups always report success so the poll loop terminates quickly.
func waitFlowHandler(mutationPath string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if id := r.URL.Query().Get("id"); id != "" {
				_, _ = w.Write([]byte(`[{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
					`"status":"succeeded","messages":[],` +
					`"created_at":"2026-06-25T02:00:00Z"}]`))
				return
			}
			mu.Lock()
			done := mutated
			mu.Unlock()
			if done {
				_, _ = w.Write([]byte(`[{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
					`"subject_id":"x","subject_kind":"cluster",` +
					`"created_at":"2026-06-25T02:00:00Z"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method != http.MethodGet:
			mu.Lock()
			mutated = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}
}

func TestClusterDeleteWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "cluster", "delete",
		testClusterID, "--force", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("cluster delete --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestBackupStoreDeleteWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "backup-store", "delete",
		testStoreID, "--force", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("backup-store delete --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestDatabaseDeleteWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "database", "delete",
		testDatabaseID, "--force", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("database delete --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestIngressDeleteWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "ingress", "delete",
		testIngressID, "--force", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("ingress delete --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestClusterUpdateWaitRun(t *testing.T) {
	// Update first GETs the cluster, then PUTs, then tracks the task.
	wf := waitFlowHandler("")
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet &&
			!strings.Contains(r.URL.Path, "/tasks") {
			testsupport.JSONHandler(200, clusterBody)(w, r)
			return
		}
		wf(w, r)
	}
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, handler)
	if err := runAuthed(t, rt, out, url, "cluster", "update",
		testClusterID, "--regions", "eu-west-1", "--wait",
		"--wait-interval", "1", "--wait-timeout", "30"); err != nil {
		t.Fatalf("cluster update --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestDatabaseMCPDeployWaitRun(t *testing.T) {
	nodes := `[{"id":"host-1","name":"n1","region":"us-east-1",` +
		`"instance_type":"r7g.medium","ip_address":"10.0.0.1"}]`
	wf := waitFlowHandler("")
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			testsupport.JSONHandler(200, nodes)(w, r)
		case r.Method == http.MethodGet &&
			!strings.Contains(r.URL.Path, "/tasks"):
			// No MCP service deployed: the deploy/update guard
			// refuses `deploy` outright against a database that already
			// carries one, so a genuine deploy test needs a fixture
			// without it.
			testsupport.JSONHandler(200, dbNoServiceBody)(w, r)
		default:
			wf(w, r)
		}
	}
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, handler)
	if err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
		testDatabaseID, "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("mcp deploy --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

// Both rotate-password and restore leave the database in "modifying"
// and spawn an update task — confirmed live against the API on
// 2026-08-03, where a second rotate answered 400 "database is not in
// available status" while the first was still running. Neither
// response carries a task id, which is exactly the shape
// trackMutation's prior-task discovery exists for.

func TestDatabaseRotatePasswordWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "database", "rotate-password",
		testDatabaseID, "--role", "app", "--force", "--wait",
		"--wait-interval", "1", "--wait-timeout", "30"); err != nil {
		t.Fatalf("database rotate-password --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

func TestDatabaseRestoreWaitRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "database", "restore",
		testDatabaseID, "--node-name", "n1", "--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
		"--force", "--wait", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("database restore --wait: %v", err)
	}
	if !strings.Contains(errb.String(), "succeeded") {
		t.Errorf("expected task success line: %q", errb.String())
	}
}

// Without --wait the command must say how to follow the task, so exit
// 0 is not mistaken for "the work finished".
func TestDatabaseRotatePasswordWithoutWaitPrintsMonitorHint(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, waitFlowHandler(""))
	if err := runAuthed(t, rt, out, url, "database", "rotate-password",
		testDatabaseID, "--role", "app", "--force"); err != nil {
		t.Fatalf("database rotate-password: %v", err)
	}
	if !strings.Contains(errb.String(), "task list --subject-id") {
		t.Errorf("expected monitor hint: %q", errb.String())
	}
}
