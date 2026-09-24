package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParseScheduledAt pins the helper directly, independently of the
// --scheduled-at flag paths that consume it.
func TestParseScheduledAt(t *testing.T) {
	t.Run("empty string yields nil", func(t *testing.T) {
		got, err := parseScheduledAt("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("want nil, got %v", got)
		}
	})

	t.Run("valid RFC3339", func(t *testing.T) {
		got, err := parseScheduledAt("2026-07-17T15:04:05Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("want non-nil time")
		}
		if want := "2026-07-17T15:04:05Z"; got.Format("2006-01-02T15:04:05Z") != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("malformed value is a usage error", func(t *testing.T) {
		_, err := parseScheduledAt("not-a-timestamp")
		if err == nil {
			t.Fatal("expected error")
		}
		exitErr, ok := err.(*ExitError)
		if !ok {
			t.Fatalf("want *ExitError, got %T: %v", err, err)
		}
		if exitErr.Code() != ExitUsage {
			t.Errorf("got code %d, want %d", exitErr.Code(), ExitUsage)
		}
	})
}

const opsTaskID = "55555555-5555-5555-5555-555555555555"

const upgradeResp = `{"task":{"task_id":"` + opsTaskID + `",` +
	`"status":"pending","type":"upgrade","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"},` +
	`"database":{"id":"storefront","state":"upgrading",` +
	`"created_at":"2026-06-18T00:00:00Z",` +
	`"updated_at":"2026-06-18T00:00:00Z"}}`

func TestDatabaseUpgradeRun(t *testing.T) {
	t.Run("force runs", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, upgradeResp))
		if err := runControlplane(t, rt, out, url, "database", "upgrade",
			"storefront", "--image", "pgedge/pg:16.4", "--force"); err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		if !strings.Contains(errb.String(), opsTaskID) {
			t.Errorf("want task id in output: %q", errb.String())
		}
	})

	t.Run("requires image", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, upgradeResp))
		err := runControlplane(t, rt, out, url, "database", "upgrade",
			"storefront", "--force")
		requireUsageError(t, err)
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, upgradeResp))
		err := runControlplane(t, rt, out, url, "database", "upgrade",
			"storefront", "--image", "pgedge/pg:16.4")
		requireUsageError(t, err)
	})
}

const restoreResp = `{"task":{"task_id":"` + opsTaskID + `",` +
	`"status":"pending","type":"restore","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"},` +
	`"node_tasks":[],"database":{"id":"storefront",` +
	`"state":"restoring","created_at":"2026-06-18T00:00:00Z",` +
	`"updated_at":"2026-06-18T00:00:00Z"}}`

// restoreNodeTasksResp carries a populated node_tasks array so the
// "Restore spawned N node task(s)." info-line branch is exercised.
const restoreNodeTasksResp = `{"task":{"task_id":"` + opsTaskID + `",` +
	`"status":"pending","type":"restore","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"},` +
	`"node_tasks":[{"task_id":"66666666-6666-6666-6666-666666666666",` +
	`"status":"pending","type":"restore","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"}],` +
	`"database":{"id":"storefront","state":"restoring",` +
	`"created_at":"2026-06-18T00:00:00Z",` +
	`"updated_at":"2026-06-18T00:00:00Z"}}`

const restoreSpecYAML = `restore_config:
  source_database_id: old-store
  source_database_name: store
  source_node_name: n1
  repository:
    type: s3
target_nodes: [n1]
`

func TestDatabaseRestoreRun(t *testing.T) {
	t.Run("force runs from file", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		dir := t.TempDir()
		specPath := filepath.Join(dir, "restore.yaml")
		if err := os.WriteFile(specPath,
			[]byte(restoreSpecYAML), 0o600); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		url := newServer(t, jsonHandler(200, restoreResp))
		if err := runControlplane(t, rt, out, url, "database", "restore",
			"storefront", "-f", specPath, "--force"); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if !strings.Contains(errb.String(), opsTaskID) {
			t.Errorf("want task id in output: %q", errb.String())
		}
	})

	t.Run("requires spec file", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, restoreResp))
		err := runControlplane(t, rt, out, url, "database", "restore",
			"storefront", "--force")
		requireUsageError(t, err)
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		dir := t.TempDir()
		specPath := filepath.Join(dir, "restore.yaml")
		if err := os.WriteFile(specPath,
			[]byte(restoreSpecYAML), 0o600); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		url := newServer(t, jsonHandler(200, restoreResp))
		err := runControlplane(t, rt, out, url, "database", "restore",
			"storefront", "-f", specPath)
		requireUsageError(t, err)
	})

	t.Run("reports node tasks", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		dir := t.TempDir()
		specPath := filepath.Join(dir, "restore.yaml")
		if err := os.WriteFile(specPath,
			[]byte(restoreSpecYAML), 0o600); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		url := newServer(t, jsonHandler(200, restoreNodeTasksResp))
		if err := runControlplane(t, rt, out, url, "database", "restore",
			"storefront", "-f", specPath, "--force"); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if !strings.Contains(errb.String(), "spawned 1 node task") {
			t.Errorf("want node-task info line: %q", errb.String())
		}
	})

	t.Run("nil json200 no-ops", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		dir := t.TempDir()
		specPath := filepath.Join(dir, "restore.yaml")
		if err := os.WriteFile(specPath,
			[]byte(restoreSpecYAML), 0o600); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		// A 200 with a non-JSON body leaves JSON200 nil; the command
		// must return cleanly without announcing a task.
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url, "database", "restore",
			"storefront", "-f", specPath, "--force"); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if strings.Contains(errb.String(), opsTaskID) {
			t.Errorf("did not expect a task announcement: %q",
				errb.String())
		}
	})
}

// --- The async-json test matrix, covering all 13 verbs -----------

const (
	deleteMatrixTaskID     = "77777777-7777-7777-7777-777777777777"
	hostRemoveMatrixTaskID = "88888888-8888-8888-8888-888888888888"
	cancelMatrixTaskID     = "019783f4-0000-7000-8000-000000000099"
)

var deleteMatrixResp = `{"task":{"task_id":"` + deleteMatrixTaskID + `",` +
	`"status":"pending","type":"delete","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"}}`

var hostRemoveMatrixResp = `{"task":{"task_id":"` + hostRemoveMatrixTaskID +
	`","status":"pending","type":"remove_host","scope":"host",` +
	`"entity_id":"host-3","created_at":"2026-06-18T00:00:00Z"},` +
	`"update_database_tasks":[{"task_id":"` + opsTaskID + `",` +
	`"status":"pending","type":"update","scope":"database",` +
	`"entity_id":"storefront","created_at":"2026-06-18T00:00:00Z"}]}`

var cancelMatrixResp = `{"task_id":"` + cancelMatrixTaskID + `",` +
	`"type":"cancel","status":"pending","scope":"database",` +
	`"entity_id":"db1","created_at":"2026-06-18T00:00:00Z"}`

// asyncCase is one of the thirteen task-spawning verbs: enough to build
// its args, stub its response and know where its task id lives in the
// -o json document.
type asyncCase struct {
	name     string
	args     []string
	respBody string
	taskID   string
	// bare is set only for `task cancel`, whose 200 body is a bare
	// *api.Task with no wrapping envelope — the task id sits at the
	// document's top level (task_id) rather than under .task.task_id.
	bare bool
	// extraKey, when set, is a top-level key besides "task" that the
	// payload must also carry verbatim (restore's node_tasks, host
	// remove's update_database_tasks) — the reason the CLI emits the
	// API's own response shape rather than a synthetic
	// {"task_id":...} envelope.
	extraKey string
}

// asyncMatrixCases builds the 13-verb table. dir holds the two spec
// files create/update/restore each need via -f.
func asyncMatrixCases(t *testing.T, dir string) []asyncCase {
	t.Helper()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	restorePath := filepath.Join(dir, "restore.yaml")
	if err := os.WriteFile(
		restorePath, []byte(restoreSpecYAML), 0o600); err != nil {
		t.Fatalf("write restore spec: %v", err)
	}
	return []asyncCase{
		{
			name:     "database create",
			args:     []string{"database", "create", "storefront", "-f", specPath},
			respBody: createResp,
			taskID:   createTaskID,
		},
		{
			name:     "database update",
			args:     []string{"database", "update", "storefront", "-f", specPath},
			respBody: updateResp,
			taskID:   updateTaskID,
		},
		{
			name: "database delete",
			args: []string{
				"database", "delete", "storefront", "--force"},
			respBody: deleteMatrixResp,
			taskID:   deleteMatrixTaskID,
		},
		{
			name: "database upgrade",
			args: []string{"database", "upgrade", "storefront",
				"--image", "pgedge/pg:16.4", "--force"},
			respBody: upgradeResp,
			taskID:   opsTaskID,
		},
		{
			name: "database restore",
			args: []string{"database", "restore", "storefront",
				"-f", restorePath, "--force"},
			respBody: restoreResp,
			taskID:   opsTaskID,
			extraKey: "node_tasks",
		},
		{
			name: "database node backup",
			args: []string{
				"database", "node", "backup", "storefront", "n1"},
			respBody: nodeTaskResp("backup"),
			taskID:   nodeTaskID,
		},
		{
			name: "database node switchover",
			args: []string{"database", "node", "switchover",
				"storefront", "n1", "--force"},
			respBody: nodeTaskResp("switchover"),
			taskID:   nodeTaskID,
		},
		{
			name: "database node failover",
			args: []string{"database", "node", "failover",
				"storefront", "n1", "--force"},
			respBody: nodeTaskResp("failover"),
			taskID:   nodeTaskID,
		},
		{
			name: "database instance start",
			args: []string{
				"database", "instance", "start", "storefront", "inst-1"},
			respBody: instTaskResp("start"),
			taskID:   instTaskID,
		},
		{
			name: "database instance stop",
			args: []string{"database", "instance", "stop",
				"storefront", "inst-1", "--force"},
			respBody: instTaskResp("stop"),
			taskID:   instTaskID,
		},
		{
			name: "database instance restart",
			args: []string{"database", "instance", "restart",
				"storefront", "inst-1", "--force"},
			respBody: instTaskResp("restart"),
			taskID:   instTaskID,
		},
		{
			name:     "host remove",
			args:     []string{"host", "remove", "host-3", "--force"},
			respBody: hostRemoveMatrixResp,
			taskID:   hostRemoveMatrixTaskID,
			extraKey: "update_database_tasks",
		},
		{
			name: "task cancel",
			args: []string{"task", "cancel", cancelMatrixTaskID,
				"--database", "db1", "--force"},
			respBody: cancelMatrixResp,
			taskID:   cancelMatrixTaskID,
			bare:     true,
		},
	}
}

// taskIDAndStatus reads the accepted document's task id and status,
// honoring the bare/.task split in the API's own response shapes
// rather than papering over it.
func taskIDAndStatus(t *testing.T, raw []byte, bare bool) (id, status string) {
	t.Helper()
	if bare {
		var doc struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("stdout did not parse as one JSON object: %v (%q)",
				err, string(raw))
		}
		return doc.TaskID, doc.Status
	}
	var doc struct {
		Task struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("stdout did not parse as one JSON object: %v (%q)",
			err, string(raw))
	}
	return doc.Task.TaskID, doc.Task.Status
}

// sameKeyShape reports whether a and b (both decoded from
// encoding/json-compatible documents) carry the same set of keys at
// every level, regardless of scalar values. It is how the yaml case
// below pins the "same key set as json" guarantee without asserting
// on yaml's own (immaterial) scalar formatting.
func sameKeyShape(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, aval := range av {
			bval, ok := bv[k]
			if !ok || !sameKeyShape(aval, bval) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sameKeyShape(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// TestAsyncVerbsEmitAcceptedJSON is the test matrix for all 13
// task-spawning controlplane verbs: -o json parses with the task id in the
// right place, -o yaml carries the identical key set, -o text stays
// silent on stdout, and the stderr acceptance line is unaffected.
func TestAsyncVerbsEmitAcceptedJSON(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range asyncMatrixCases(t, dir) {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				rt, out, errb := newTestRuntime(t, "", "json")
				url := newServer(t, jsonHandler(200, tc.respBody))
				if err := runControlplane(t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				gotID, gotStatus := taskIDAndStatus(t, out.Bytes(), tc.bare)
				if gotID != tc.taskID {
					t.Errorf("task id = %q, want %q (stdout %q)",
						gotID, tc.taskID, out.String())
				}
				if gotStatus == "" {
					t.Errorf("status empty in stdout: %q", out.String())
				}
				if tc.extraKey != "" &&
					!strings.Contains(out.String(), `"`+tc.extraKey+`"`) {
					t.Errorf("missing %q key in json stdout: %q",
						tc.extraKey, out.String())
				}
				if !strings.Contains(errb.String(), tc.taskID) {
					t.Errorf("missing task id on stderr: %q", errb.String())
				}
			})

			t.Run("yaml", func(t *testing.T) {
				rt, out, _ := newTestRuntime(t, "", "yaml")
				url := newServer(t, jsonHandler(200, tc.respBody))
				if err := runControlplane(t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				var yamlDoc map[string]any
				if err := yaml.Unmarshal(
					out.Bytes(), &yamlDoc); err != nil {
					t.Fatalf("yaml did not parse: %v (%q)",
						err, out.String())
				}

				jrt, jout, _ := newTestRuntime(t, "", "json")
				jurl := newServer(t, jsonHandler(200, tc.respBody))
				if err := runControlplane(
					t, jrt, jout, jurl, tc.args...); err != nil {
					t.Fatalf("%s (json for parity): %v", tc.name, err)
				}
				var jsonDoc map[string]any
				if err := json.Unmarshal(
					jout.Bytes(), &jsonDoc); err != nil {
					t.Fatalf("json did not parse: %v", err)
				}

				if !sameKeyShape(jsonDoc, yamlDoc) {
					t.Errorf(
						"yaml key shape differs from json: yaml=%#v json=%#v",
						yamlDoc, jsonDoc)
				}
			})

			t.Run("text stdout stays silent", func(t *testing.T) {
				rt, out, errb := newTestRuntime(t, "", "text")
				url := newServer(t, jsonHandler(200, tc.respBody))
				if err := runControlplane(t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				if out.Len() != 0 {
					t.Errorf(
						"want empty stdout under text, got %q", out.String())
				}
				if !strings.Contains(errb.String(), tc.taskID) {
					t.Errorf("missing task id on stderr: %q", errb.String())
				}
			})
		})
	}
}

// acceptThenTerminal routes the accept call (any non-/tasks/ path) to
// respond with accept, and any poll of a task's own endpoint
// (.../tasks/<id>, hit only by --wait) to respond with terminal. It
// lets a --wait test use one stub server for both legs of the
// request without inspecting HTTP method, which differs per verb
// (POST for restart, DELETE for host remove, ...).
func acceptThenTerminal(accept, terminal string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "/tasks/") {
			_, _ = io.WriteString(w, terminal)
			return
		}
		_, _ = io.WriteString(w, accept)
	}
}

// TestAsyncVerbsWaitEmitsSinglePendingDocument is the single-document
// --wait contract for two verbs: under --wait, exactly one JSON
// document reaches stdout — the accepted (pending) object, emitted once
// before the poll loop starts — never the terminal task, and never two
// concatenated documents.
func TestAsyncVerbsWaitEmitsSinglePendingDocument(t *testing.T) {
	t.Run("database instance restart", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "json")
		terminal := `{"task_id":"` + instTaskID + `","status":"completed",` +
			`"type":"restart","scope":"database","entity_id":"storefront",` +
			`"created_at":"2026-06-18T00:00:00Z"}`
		url := newServer(t, acceptThenTerminal(
			instTaskResp("restart"), terminal))
		if err := runControlplane(t, rt, out, url, "database", "instance", "restart",
			"storefront", "inst-1", "--force", "--wait",
			"--wait-timeout", "5", "--wait-interval", "1"); err != nil {
			t.Fatalf("restart --wait: %v", err)
		}
		if n := strings.Count(out.String(), "\n"); n != 1 {
			t.Fatalf("want exactly one JSON document on stdout, got %d "+
				"newlines: %q", n, out.String())
		}
		id, status := taskIDAndStatus(t, out.Bytes(), false)
		if id != instTaskID || status != "pending" {
			t.Errorf("want the accepted (pending) doc, got id=%q status=%q",
				id, status)
		}
		if !strings.Contains(stderr.String(), "completed") {
			t.Errorf("want the terminal verdict on stderr: %q",
				stderr.String())
		}
	})

	t.Run("host remove", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "json")
		terminal := `{"task_id":"` + hostRemoveMatrixTaskID + `",` +
			`"status":"completed","type":"remove_host","scope":"host",` +
			`"entity_id":"host-3","created_at":"2026-06-18T00:00:00Z"}`
		url := newServer(t, acceptThenTerminal(
			hostRemoveMatrixResp, terminal))
		if err := runControlplane(t, rt, out, url, "host", "remove", "host-3",
			"--force", "--wait",
			"--wait-timeout", "5", "--wait-interval", "1"); err != nil {
			t.Fatalf("host remove --wait: %v", err)
		}
		if n := strings.Count(out.String(), "\n"); n != 1 {
			t.Fatalf("want exactly one JSON document on stdout, got %d "+
				"newlines: %q", n, out.String())
		}
		id, status := taskIDAndStatus(t, out.Bytes(), false)
		if id != hostRemoveMatrixTaskID || status != "pending" {
			t.Errorf("want the accepted (pending) doc, got id=%q status=%q",
				id, status)
		}
		if !strings.Contains(stderr.String(), "completed") {
			t.Errorf("want the terminal verdict on stderr: %q",
				stderr.String())
		}
	})
}

// TestDatabaseInstanceRestartJSONGoldenBytes is the golden-byte test:
// stdout for `database instance restart -o json` must equal this exact
// string. json.Encoder emits compact JSON with a trailing newline
// (internal/output/output.go), and encoding/json orders struct fields
// by declaration order, not alphabetically — the only test in this
// matrix that would catch a silent key reordering or a stray added
// field, since every other assertion here decodes before comparing.
func TestDatabaseInstanceRestartJSONGoldenBytes(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, instTaskResp("restart")))
	if err := runControlplane(t, rt, out, url, "database", "instance",
		"restart", "storefront", "inst-1", "--force"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	want := `{"task":{"created_at":"2026-06-18T00:00:00Z",` +
		`"entity_id":"storefront","scope":"database","status":"pending",` +
		`"task_id":"` + instTaskID + `","type":"restart"}}` + "\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}
