package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/spf13/cobra"
)

// taskUUID is a valid UUID used across the wait/task tests. Task IDs
// unmarshal into openapi_types.UUID, so fixtures must use a real UUID.
const taskUUID = "11111111-1111-1111-1111-111111111111"

func newRawClient(
	t *testing.T, h http.HandlerFunc,
) *api.ClientWithResponses {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c
}

func TestWaitForDatabaseTaskSucceeds(t *testing.T) {
	rt, _, stderr := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"completed","type":"create",`+
			`"scope":"database","entity_id":"db1",`+
			`"created_at":"2025-06-18T00:00:00Z"}`))
	tid := uuid.MustParse(taskUUID)
	if err := waitForTask(
		rt, databaseTaskSource(client, "db1", tid), 5, 1); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(stderr.String(), "completed") {
		t.Errorf("want terminal notice on stderr: %q", stderr.String())
	}
}

func TestWaitForDatabaseTaskFails(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"failed","type":"create",`+
			`"scope":"database","entity_id":"db1","error":"boom",`+
			`"created_at":"2025-06-18T00:00:00Z"}`))
	tid := uuid.MustParse(taskUUID)
	err := waitForTask(rt, databaseTaskSource(client, "db1", tid), 5, 1)
	if err == nil {
		t.Fatal("expected error on failed task")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("want error to include cause: %q", err.Error())
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
		t.Errorf("want ExitGeneral, got %v", err)
	}
}

func TestWaitForDatabaseTaskCanceled(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"canceled","type":"create",`+
			`"scope":"database","entity_id":"db1",`+
			`"created_at":"2025-06-18T00:00:00Z"}`))
	tid := uuid.MustParse(taskUUID)
	err := waitForTask(rt, databaseTaskSource(client, "db1", tid), 5, 1)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("want canceled error, got %v", err)
	}
}

// TestWaitForDatabaseTaskPolls exercises the non-terminal poll path:
// the first response is pending, the second completed. interval 0 is
// clamped to 1s, so the loop sleeps once. Progress goes to stderr.
func TestWaitForDatabaseTaskPolls(t *testing.T) {
	rt, _, stderr := newTestRuntime(t, "", "text")
	var calls atomic.Int32
	client := newRawClient(t, func(
		w http.ResponseWriter, _ *http.Request,
	) {
		status := "completed"
		if calls.Add(1) == 1 {
			status = "pending"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(
			`{"task_id":"` + taskUUID + `","status":"` + status +
				`","type":"create","scope":"database","entity_id":"db1",` +
				`"created_at":"2025-06-18T00:00:00Z"}`))
	})
	tid := uuid.MustParse(taskUUID)
	if err := waitForTask(
		rt, databaseTaskSource(client, "db1", tid), 30, 0); err != nil {
		t.Fatalf("wait polls: %v", err)
	}
	if calls.Load() < 2 {
		t.Errorf("expected at least 2 polls, got %d", calls.Load())
	}
	if !strings.Contains(stderr.String(), "pending") {
		t.Errorf("want progress line on stderr: %q", stderr.String())
	}
}

// TestWaitForDatabaseTaskTimeout hits the deadline branch immediately:
// timeout 0 means the very first deadline check fails, before any
// request or sleep. Hermetic and instant.
func TestWaitForDatabaseTaskTimeout(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"pending","type":"create",`+
			`"scope":"database","entity_id":"db1",`+
			`"created_at":"2025-06-18T00:00:00Z"}`))
	tid := uuid.MustParse(taskUUID)
	err := waitForTask(rt, databaseTaskSource(client, "db1", tid), 0, 1)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitTimeout {
		t.Errorf("want ExitTimeout, got %v", err)
	}
}

func TestWaitForDatabaseTaskServerError(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(500, "boom"))
	tid := uuid.MustParse(taskUUID)
	if err := waitForTask(
		rt, databaseTaskSource(client, "db1", tid), 5, 1); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestFollowDatabaseTaskTerminal(t *testing.T) {
	rt, _, stderr := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","entity_id":"db1","scope":"database",`+
			`"task_status":"completed","entries":[`+
			`{"timestamp":"2025-06-18T00:00:01Z","message":"provisioning"}]}`))
	tid := uuid.MustParse(taskUUID)
	if err := followTask(rt, databaseTaskSource(client, "db1", tid)); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if !strings.Contains(stderr.String(), "provisioning") {
		t.Errorf("want entry message on stderr: %q", stderr.String())
	}
}

func TestFollowDatabaseTaskFailed(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","entity_id":"db1","scope":"database",`+
			`"task_status":"failed","entries":[`+
			`{"timestamp":"2025-06-18T00:00:01Z","message":"kaboom"}]}`))
	tid := uuid.MustParse(taskUUID)
	err := followTask(rt, databaseTaskSource(client, "db1", tid))
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("want failed error, got %v", err)
	}
}

func TestFollowDatabaseTaskCanceled(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","entity_id":"db1","scope":"database",`+
			`"task_status":"canceled","entries":[`+
			`{"timestamp":"2025-06-18T00:00:01Z","message":"stopping"}]}`))
	tid := uuid.MustParse(taskUUID)
	err := followTask(rt, databaseTaskSource(client, "db1", tid))
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("want canceled error, got %v", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
		t.Errorf("want ExitGeneral, got %v", err)
	}
}

func TestFollowDatabaseTaskServerError(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(500, "boom"))
	tid := uuid.MustParse(taskUUID)
	if err := followTask(rt, databaseTaskSource(client, "db1", tid)); err == nil {
		t.Fatal("expected error on 500")
	}
}

// TestHostTaskSourceWiring verifies hostTaskSource routes waitForTask
// and followTask to the host-scoped endpoints (not the database ones),
// so the later host wait/follow wiring rides the correct paths.
func TestHostTaskSourceWiring(t *testing.T) {
	tid := uuid.MustParse(taskUUID)

	t.Run("wait hits host task endpoint", func(t *testing.T) {
		rt, _, stderr := newTestRuntime(t, "", "text")
		var gotPath string
		client := newRawClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"task_id":"` + taskUUID + `","status":"completed",` +
					`"type":"remove","scope":"host","entity_id":"host1",` +
					`"created_at":"2025-06-18T00:00:00Z"}`))
		})
		if err := waitForTask(
			rt, hostTaskSource(client, "host1", tid), 5, 1); err != nil {
			t.Fatalf("wait: %v", err)
		}
		if !strings.Contains(gotPath, "/hosts/host1/") {
			t.Errorf("want host-scoped path, got %q", gotPath)
		}
		if !strings.Contains(stderr.String(), "completed") {
			t.Errorf("want terminal notice on stderr: %q", stderr.String())
		}
	})

	t.Run("follow hits host log endpoint", func(t *testing.T) {
		rt, _, stderr := newTestRuntime(t, "", "text")
		var gotPath string
		client := newRawClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"task_id":"` + taskUUID + `","entity_id":"host1",` +
					`"scope":"host","task_status":"completed","entries":[` +
					`{"timestamp":"2025-06-18T00:00:01Z","message":"detaching"}]}`))
		})
		if err := followTask(
			rt, hostTaskSource(client, "host1", tid)); err != nil {
			t.Fatalf("follow: %v", err)
		}
		if !strings.Contains(gotPath, "/hosts/host1/") {
			t.Errorf("want host-scoped path, got %q", gotPath)
		}
		if !strings.Contains(stderr.String(), "detaching") {
			t.Errorf("want entry message on stderr: %q", stderr.String())
		}
	})
}

func TestAddWaitFollowFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	o := addWaitFollowFlags(cmd)
	if o == nil {
		t.Fatal("nil opts")
	}
	for _, name := range []string{
		"wait", "follow", "wait-timeout", "wait-interval",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag --%s", name)
		}
	}
	if o.timeout != 600 || o.interval != 3 {
		t.Errorf("unexpected defaults: %+v", o)
	}
}

func TestWaitFollowOptsRun(t *testing.T) {
	tid := uuid.MustParse(taskUUID)
	completedTask := jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"completed","type":"create",`+
			`"scope":"database","entity_id":"db1",`+
			`"created_at":"2025-06-18T00:00:00Z"}`)
	completedLog := jsonHandler(200,
		`{"task_id":"`+taskUUID+`","entity_id":"db1","scope":"database",`+
			`"task_status":"completed","entries":[]}`)

	t.Run("default no-op", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		client := newRawClient(t, completedTask)
		o := &waitFollowOpts{}
		if err := o.run(rt, databaseTaskSource(client, "db1", tid)); err != nil {
			t.Fatalf("run default: %v", err)
		}
	})

	t.Run("wait", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		client := newRawClient(t, completedTask)
		o := &waitFollowOpts{wait: true, timeout: 5, interval: 1}
		if err := o.run(rt, databaseTaskSource(client, "db1", tid)); err != nil {
			t.Fatalf("run wait: %v", err)
		}
	})

	t.Run("follow", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		client := newRawClient(t, completedLog)
		o := &waitFollowOpts{follow: true}
		if err := o.run(rt, databaseTaskSource(client, "db1", tid)); err != nil {
			t.Fatalf("run follow: %v", err)
		}
	})
}

func TestIsTerminal(t *testing.T) {
	for _, s := range []string{"completed", "failed", "canceled"} {
		if !isTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"pending", "running", ""} {
		if isTerminal(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

// TestWaitForDatabaseTaskEscapesAForgedError covers controlplane's copy of the
// failed-task interpolation. byoc and managed have equivalents; a
// review mutation showed all three wraps were defended by nothing,
// because the fmt.Sprintf-into-error shape sits outside the AST gate.
//
// The forging value carries a newline, a tab and an ANSI CSI together,
// so a fix handling only the newline still fails. It is written as JSON
// escapes here because that is how a server delivers it: the client
// decodes them back into real bytes.
func TestWaitForDatabaseTaskEscapesAForgedError(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, jsonHandler(200,
		`{"task_id":"`+taskUUID+`","status":"failed","type":"create",`+
			`"scope":"database","entity_id":"db1",`+
			`"error":"boom\nTask succeeded\tSTATUS\tHEALTHY\u001b[2K",`+
			`"created_at":"2025-06-18T00:00:00Z"}`))
	tid := uuid.MustParse(taskUUID)
	err := waitForTask(rt, databaseTaskSource(client, "db1", tid), 5, 1)
	if err == nil {
		t.Fatal("expected error on failed task")
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
	// Escaped rather than dropped: stripping would pass the checks
	// above while losing the operator's evidence.
	for _, want := range []string{`\n`, `\t`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the escaped form %s in:\n%q", want, got)
		}
	}
}
