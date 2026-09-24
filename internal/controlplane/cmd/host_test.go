package cmd

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const hostsBody = `{"hosts":[{"id":"host-1","orchestrator":"swarm",` +
	`"data_dir":"/data","peer_addresses":["10.0.0.1"],` +
	`"client_addresses":["10.0.0.1"],"status":{"state":"healthy",` +
	`"updated_at":"2025-06-17T00:00:00Z","components":{}}}]}`

const hostBody = `{"id":"host-1","orchestrator":"swarm",` +
	`"data_dir":"/data","peer_addresses":["10.0.0.1"],` +
	`"client_addresses":["10.0.0.1"],"status":{"state":"healthy",` +
	`"updated_at":"2025-06-17T00:00:00Z","components":{}}}`

func TestHostListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, hostsBody))
		if err := runControlplane(t, rt, out, url, "host", "list"); err != nil {
			t.Fatalf("host list: %v", err)
		}
		if !strings.Contains(out.String(), "host-1") {
			t.Errorf("missing host: %q", out.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, `{"hosts":[]}`))
		if err := runControlplane(t, rt, out, url, "host", "list"); err != nil {
			t.Fatalf("host list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No hosts found") {
			t.Errorf("want 'No hosts found': %q", errb.String())
		}
	})
}

func TestHostGetRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, hostBody))
		if err := runControlplane(t, rt, out, url,
			"host", "get", "host-1"); err != nil {
			t.Fatalf("host get: %v", err)
		}
		if !strings.Contains(out.String(), "swarm") {
			t.Errorf("missing orchestrator: %q", out.String())
		}
	})

	t.Run("no data", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url, "host", "get", "host-1"); err != nil {
			t.Fatalf("host get no data: %v", err)
		}
		if !strings.Contains(stderr.String(), "No host data returned") {
			t.Errorf("missing no-data notice: %q", stderr.String())
		}
	})

	t.Run("json output", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, hostBody))
		if err := runControlplane(t, rt, out, url, "host", "get", "host-1"); err != nil {
			t.Fatalf("host get json: %v", err)
		}
		if !strings.Contains(out.String(), "host-1") {
			t.Errorf("missing host id in json: %q", out.String())
		}
	})
}

func TestHostListJSON(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, hostsBody))
	if err := runControlplane(t, rt, out, url, "host", "list"); err != nil {
		t.Fatalf("host list json: %v", err)
	}
	if !strings.Contains(out.String(), "swarm") {
		t.Errorf("missing orchestrator in json: %q", out.String())
	}
}

func TestHostGetServerError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(500, "boom"))
	if err := runControlplane(t, rt, out, url, "host", "get", "host-1"); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestHostRemoveRun(t *testing.T) {
	// --force-lost is the quorum waiver, not a second prompt-skip:
	// the one flag that skips the safety prompt is --force, and a
	// second one is exactly the regression the --force split
	// forbids.
	t.Run("force-lost alone still refuses non-interactively",
		func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(200, hostRemoveMatrixResp))
			err := runControlplane(t, rt, out, url,
				"host", "remove", "host-1", "--force-lost")
			requireUsageError(t, err)
		})

	t.Run("force", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200,
			`{"task":{"task_id":"11111111-1111-1111-1111-111111111111","status":"pending","type":`+
				`"remove_host","scope":"host","entity_id":"host-1",`+
				`"created_at":"2025-06-18T00:00:00Z"},`+
				`"update_database_tasks":[]}`))
		if err := runControlplane(t, rt, out, url,
			"host", "remove", "host-1", "--force"); err != nil {
			t.Fatalf("host remove force: %v", err)
		}
		if !strings.Contains(stderr.String(), "accepted") {
			t.Errorf("missing accepted message: %q", stderr.String())
		}
		if !strings.Contains(stderr.String(), "11111111-1111-1111-1111-111111111111") {
			t.Errorf("missing task id: %q", stderr.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, `{}`))
		if err := runControlplane(t, rt, out, url,
			"host", "remove", "host-1"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

func TestHostRemoveWaitPollsToTerminal(t *testing.T) {
	// wf.run's status lines (accepted + terminal) go to rt.Stderr, not
	// the cobra-routed stdout buffer, so we assert on stderr here
	// (mirrors TestHostRemoveRun and byoc's wait_run_test.go).
	rt, out, stderr := newTestRuntime(t, "", "text")
	const tid = "019783f4-0000-7000-8000-000000000001"
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// DELETE returns the accepted task; the wait GET returns it
		// completed.
		if r.Method == http.MethodDelete {
			_, _ = io.WriteString(w, `{"task":{"task_id":"`+tid+
				`","type":"remove_host","status":"running",`+
				`"scope":"host","entity_id":"host-3",`+
				`"created_at":"2026-07-20T00:00:00Z"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"task_id":"`+tid+
			`","type":"remove_host","status":"completed",`+
			`"scope":"host","entity_id":"host-3",`+
			`"created_at":"2026-07-20T00:00:00Z"}`)
	})
	if err := runControlplane(t, rt, out, srv,
		"host", "remove", "host-3", "--force", "--wait"); err != nil {
		t.Fatalf("remove --wait: %v", err)
	}
	if !strings.Contains(stderr.String(), "completed") {
		t.Fatalf("want terminal status in output, got %q", stderr.String())
	}
}
