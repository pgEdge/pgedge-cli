package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

const tasksBody = `{"tasks":[{"task_id":"` + taskUUID + `",` +
	`"status":"completed","type":"create","scope":"database",` +
	`"entity_id":"db1","created_at":"2025-06-18T00:00:00Z"}]}`

const taskBody = `{"task_id":"` + taskUUID + `","status":"completed",` +
	`"type":"create","scope":"database","entity_id":"db1",` +
	`"created_at":"2025-06-18T00:00:00Z"}`

// failedTaskBody mirrors what the Control Plane really returns for a
// failed create: a wrapped chain of %w, several levels deep.
const failedTaskBody = `{"task_id":"` + taskUUID + `","status":"failed",` +
	`"type":"create","scope":"database","entity_id":"db1",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"error":"failed to execute plan update:\n  ` +
	`failed to get service image:\n  ` +
	`unsupported version \"99.99.99\" for service type \"mcp\""}`

const taskLogBody = `{"task_id":"` + taskUUID + `","entity_id":"db1",` +
	`"scope":"database","task_status":"completed","entries":[` +
	`{"timestamp":"2025-06-18T00:00:01Z","message":"provisioning"}]}`

func TestTaskListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, tasksBody))
		if err := runControlplane(t, rt, out, url, "task", "list"); err != nil {
			t.Fatalf("task list: %v", err)
		}
		if !strings.Contains(out.String(), taskUUID) {
			t.Errorf("missing task id: %q", out.String())
		}
		if !strings.Contains(out.String(), "create") {
			t.Errorf("missing type: %q", out.String())
		}
	})

	t.Run("with filters", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		var gotQuery string
		url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(tasksBody))
		})
		if err := runControlplane(t, rt, out, url, "task", "list",
			"--scope", "database", "--entity-id", "db1",
			"--limit", "20"); err != nil {
			t.Fatalf("task list filters: %v", err)
		}
		for _, want := range []string{
			"scope=database", "entity_id=db1", "limit=20",
		} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q missing %q", gotQuery, want)
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, `{"tasks":[]}`))
		if err := runControlplane(t, rt, out, url, "task", "list"); err != nil {
			t.Fatalf("task list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No tasks found") {
			t.Errorf("want 'No tasks found': %q", errb.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, tasksBody))
		if err := runControlplane(t, rt, out, url, "task", "list"); err != nil {
			t.Fatalf("task list json: %v", err)
		}
		if !strings.Contains(out.String(), taskUUID) {
			t.Errorf("missing task id in json: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(500, "boom"))
		if err := runControlplane(t, rt, out, url, "task", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestTaskListDatabaseHitsScopedEndpoint(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	var rec capturedRequest
	srv := captureServer(t, http.StatusOK, `{"tasks":[]}`, &rec)
	if err := runControlplane(t, rt, out, srv,
		"task", "list", "--database", "db1"); err != nil {
		t.Fatalf("list --database: %v", err)
	}
	if rec.path != "/v1/databases/db1/tasks" {
		t.Fatalf("path = %q, want /v1/databases/db1/tasks", rec.path)
	}
}

func TestTaskListHostHitsScopedEndpoint(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	var rec capturedRequest
	srv := captureServer(t, http.StatusOK, `{"tasks":[]}`, &rec)
	if err := runControlplane(t, rt, out, srv,
		"task", "list", "--host", "host-1"); err != nil {
		t.Fatalf("list --host: %v", err)
	}
	if rec.path != "/v1/hosts/host-1/tasks" {
		t.Fatalf("path = %q, want /v1/hosts/host-1/tasks", rec.path)
	}
}

func TestTaskListDatabaseAndHostConflict(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "list", "--database", "db1", "--host", "host-1")
	requireUsageError(t, err)
}

func TestTaskListScopedWithGlobalFilterConflict(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "list", "--database", "db1", "--scope", "database")
	requireUsageError(t, err)
}

func TestTaskGetRun(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskBody))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		if !strings.Contains(out.String(), "create") {
			t.Errorf("missing type: %q", out.String())
		}
	})

	t.Run("host", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskBody))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--host", "host-1", taskUUID); err != nil {
			t.Fatalf("task get host: %v", err)
		}
		if !strings.Contains(out.String(), taskUUID) {
			t.Errorf("missing task id: %q", out.String())
		}
	})

	// A failed task is where a provisioning failure explains itself:
	// a failed create marks its instances `failed` without a reason,
	// and a spec that fails during planning never creates one at all,
	// so `database get` has nothing to show.
	t.Run("failed task prints its reason", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, failedTaskBody))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task get failed task: %v", err)
		}
		s := out.String()
		if !strings.Contains(s, "Error") {
			t.Errorf("missing Error heading: %q", s)
		}
		if !strings.Contains(s, `unsupported version "99.99.99"`) {
			t.Errorf("missing the reason: %q", s)
		}
		// Printed verbatim, not flattened: the message has a line to
		// itself, and these are wrapped %w chains that read better
		// with their structure intact.
		if !strings.Contains(s, "failed to get service image:\n  "+
			`unsupported version "99.99.99"`) {
			t.Errorf("multi-line reason was not preserved: %q", s)
		}
	})

	t.Run("succeeded task prints no Error section", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskBody))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		s := out.String()
		if !strings.Contains(s, "completed") {
			t.Fatalf("control failed: no task rendered: %q", s)
		}
		if strings.Contains(s, "Error") {
			t.Errorf("unexpected Error section on a completed task: %q",
				s)
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskBody))
		err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", "not-a-uuid")
		if err == nil {
			t.Fatal("expected error on invalid uuid")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Errorf("want ExitUsage, got %v", err)
		}
	})

	t.Run("no scope", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskBody))
		err := runControlplane(t, rt, out, url, "task", "get", taskUUID)
		if err == nil {
			t.Fatal("expected error without --database/--host")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Errorf("want ExitUsage, got %v", err)
		}
	})

	t.Run("no data", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task get no data: %v", err)
		}
		if !strings.Contains(stderr.String(), "No task data returned") {
			t.Errorf("missing no-data notice: %q", stderr.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, taskBody))
		if err := runControlplane(t, rt, out, url,
			"task", "get", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task get json: %v", err)
		}
		if !strings.Contains(out.String(), taskUUID) {
			t.Errorf("missing task id in json: %q", out.String())
		}
	})
}

func TestTaskLogsRun(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskLogBody))
		if err := runControlplane(t, rt, out, url,
			"task", "logs", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task logs: %v", err)
		}
		if !strings.Contains(out.String(), "provisioning") {
			t.Errorf("missing log message: %q", out.String())
		}
	})

	t.Run("host", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskLogBody))
		if err := runControlplane(t, rt, out, url,
			"task", "logs", "--host", "host-1", taskUUID); err != nil {
			t.Fatalf("task logs host: %v", err)
		}
		if !strings.Contains(out.String(), "provisioning") {
			t.Errorf("missing log message: %q", out.String())
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskLogBody))
		err := runControlplane(t, rt, out, url,
			"task", "logs", "--database", "db1", "not-a-uuid")
		if err == nil {
			t.Fatal("expected error on invalid uuid")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Errorf("want ExitUsage, got %v", err)
		}
	})

	t.Run("no scope", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, taskLogBody))
		err := runControlplane(t, rt, out, url, "task", "logs", taskUUID)
		if err == nil {
			t.Fatal("expected error without --database/--host")
		}
	})

	t.Run("no data", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url,
			"task", "logs", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task logs no data: %v", err)
		}
		if !strings.Contains(stderr.String(), "No task log returned") {
			t.Errorf("missing no-data notice: %q", stderr.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, taskLogBody))
		if err := runControlplane(t, rt, out, url,
			"task", "logs", "--database", "db1", taskUUID); err != nil {
			t.Fatalf("task logs json: %v", err)
		}
		if !strings.Contains(out.String(), "provisioning") {
			t.Errorf("missing log message in json: %q", out.String())
		}
	})
}

func TestTaskCancelRequiresDatabase(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	const tid = "019783f4-0000-7000-8000-000000000009"
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "cancel", tid)
	requireUsageError(t, err)
}

func TestTaskCancelRejectsHost(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	const tid = "019783f4-0000-7000-8000-000000000009"
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "cancel", tid, "--host", "host-1")
	requireUsageError(t, err)
}

func TestTaskCancelRejectsBadID(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "cancel", "not-a-uuid", "--database", "db1")
	requireUsageError(t, err)
}

func TestTaskCancelNoForceRefuses(t *testing.T) {
	// stdin "n" declines the confirm; no network call must happen.
	rt, out, _ := newTestRuntime(t, "n\n", "text")
	const tid = "019783f4-0000-7000-8000-000000000009"
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"task", "cancel", tid, "--database", "db1")
	requireUsageError(t, err)
}

func TestTaskCancelForceHitsCancelPath(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	const tid = "019783f4-0000-7000-8000-000000000009"
	var rec capturedRequest
	srv := captureServer(t, http.StatusOK,
		`{"task_id":"`+tid+`","type":"cancel","status":"canceled",`+
			`"scope":"database","entity_id":"db1",`+
			`"created_at":"2026-07-20T00:00:00Z"}`, &rec)
	if err := runControlplane(t, rt, out, srv,
		"task", "cancel", tid, "--database", "db1", "--force"); err != nil {
		t.Fatalf("cancel --force: %v", err)
	}
	wantPath := "/v1/databases/db1/tasks/" + tid + "/cancel"
	if rec.path != wantPath {
		t.Fatalf("cancel path = %q, want %q", rec.path, wantPath)
	}
}

// TestTaskCancelWaitSucceedsOnCanceled proves cancel --wait treats the
// polled task reaching "canceled" as success (exit 0, non-error
// message), not as the ExitGeneral failure every other verb reports for
// a canceled task. The cancel POST reports the task still pending; the
// subsequent GET (driven by --wait) reports it canceled.
func TestTaskCancelWaitSucceedsOnCanceled(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	const tid = "019783f4-0000-7000-8000-000000000009"
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			_, _ = w.Write([]byte(
				`{"task_id":"` + tid + `","type":"cancel",` +
					`"status":"pending","scope":"database",` +
					`"entity_id":"db1",` +
					`"created_at":"2026-07-20T00:00:00Z"}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"task_id":"` + tid + `","type":"cancel",` +
				`"status":"canceled","scope":"database",` +
				`"entity_id":"db1",` +
				`"created_at":"2026-07-20T00:00:00Z"}`))
	})
	err := runControlplane(t, rt, out, url,
		"task", "cancel", tid, "--database", "db1", "--force", "--wait",
		"--wait-timeout", "5", "--wait-interval", "1")
	if err != nil {
		t.Fatalf("cancel --wait on a canceled task: %v", err)
	}
	if strings.Contains(stderr.String(), "was canceled") {
		t.Errorf("want success wording, not the failure phrasing: %q",
			stderr.String())
	}
	if !strings.Contains(stderr.String(), "canceled") {
		t.Errorf("want a success indication mentioning canceled: %q",
			stderr.String())
	}
}

// TestTaskScopeIsRequiredAndSaysWhereToFindIt covers #251's dead end:
// the reference handed a stuck user `task get <task_id>` with no scope
// flag. The command must refuse it as usage, name where the value
// comes from, and refuse it WITHOUT a server — the check runs before
// the client is built, so an unreachable Control Plane cannot turn the
// usage error into a connection error.
func TestTaskScopeIsRequiredAndSaysWhereToFindIt(t *testing.T) {
	for _, verb := range []string{"get", "logs"} {
		t.Run(verb, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			err := runControlplane(t, rt, out, "http://127.0.0.1:1",
				"task", verb, taskUUID)
			requireUsageError(t, err)
			for _, want := range []string{
				"--database", "--host", "task list", "ENTITY",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message does not mention %q: %q",
						want, err.Error())
				}
			}
		})
	}
}
