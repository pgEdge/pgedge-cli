package cmd

import (
	"strings"
	"testing"
)

const clusterBody = `{"id":"prod","status":{"state":"available"},` +
	`"hosts":[{"id":"host-1","orchestrator":"swarm",` +
	`"data_dir":"/data","peer_addresses":["10.0.0.1"],` +
	`"client_addresses":["10.0.0.1"],` +
	`"status":{"state":"healthy","updated_at":"2025-06-17T00:00:00Z",` +
	`"components":{}}}]}`

const joinTokenBody = `{"token":"PGEDGE-abc","server_urls":` +
	`["http://10.0.0.1:3000"]}`

func TestClusterInfoRun(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, clusterBody))
	if err := runControlplane(t, rt, out, url, "cluster", "info"); err != nil {
		t.Fatalf("cluster info: %v", err)
	}
	if !strings.Contains(out.String(), "prod") {
		t.Errorf("missing cluster id: %q", out.String())
	}
}

func TestClusterInfoNoData(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	url := newServer(t, plainHandler(200, "ok"))
	if err := runControlplane(t, rt, out, url, "cluster", "info"); err != nil {
		t.Fatalf("cluster info: %v", err)
	}
	if !strings.Contains(stderr.String(), "No cluster data returned") {
		t.Errorf("missing no-data notice: %q", stderr.String())
	}
}

func TestClusterInfoJSON(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, clusterBody))
	if err := runControlplane(t, rt, out, url, "cluster", "info"); err != nil {
		t.Fatalf("cluster info json: %v", err)
	}
	if !strings.Contains(out.String(), "available") {
		t.Errorf("missing state: %q", out.String())
	}
}

func TestClusterInfoServerError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(500, "boom"))
	if err := runControlplane(t, rt, out, url, "cluster", "info"); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestClusterInitRun(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, joinTokenBody))
	if err := runControlplane(t, rt, out, url, "cluster", "init"); err != nil {
		t.Fatalf("cluster init: %v", err)
	}
	if !strings.Contains(out.String(), "PGEDGE-abc") {
		t.Errorf("missing token: %q", out.String())
	}
}

func TestClusterInitWithClusterID(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, joinTokenBody))
	if err := runControlplane(t, rt, out, url, "cluster", "init",
		"--cluster-id", "prod"); err != nil {
		t.Fatalf("cluster init: %v", err)
	}
	if !strings.Contains(out.String(), "PGEDGE-abc") {
		t.Errorf("missing token: %q", out.String())
	}
}

func TestClusterInitNoData(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	url := newServer(t, plainHandler(200, "ok"))
	if err := runControlplane(t, rt, out, url, "cluster", "init"); err != nil {
		t.Fatalf("cluster init: %v", err)
	}
	if !strings.Contains(stderr.String(), "No join token returned") {
		t.Errorf("missing no-data notice: %q", stderr.String())
	}
}

func TestClusterInitServerError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(500, "boom"))
	if err := runControlplane(t, rt, out, url, "cluster", "init"); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestClusterJoinTokenRun(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, joinTokenBody))
	if err := runControlplane(t, rt, out, url,
		"cluster", "join-token"); err != nil {
		t.Fatalf("cluster join-token: %v", err)
	}
	if !strings.Contains(out.String(), "PGEDGE-abc") {
		t.Errorf("missing token: %q", out.String())
	}
}

func TestClusterJoinTokenServerError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(500, "boom"))
	if err := runControlplane(t, rt, out, url,
		"cluster", "join-token"); err == nil {
		t.Fatal("expected error on 500")
	}
}

// TestClusterJoinRun covers the redesigned `cluster join` command:
// --token and --server-url (repeatable), no --host-id/--address/
// --embedded-etcd (those fields don't exist in the generated API).
func TestClusterJoinRun(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, "{}"))
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--token", "PGEDGE-abc",
		"--server-url", "http://existing:3000")
	if err != nil {
		t.Fatalf("cluster join: %v", err)
	}
	if !strings.Contains(stderr.String(), "Join request accepted") {
		t.Errorf("missing success message: %q", stderr.String())
	}
}

func TestClusterJoinMissingToken(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, "{}"))
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--server-url", "http://existing:3000")
	if err == nil {
		t.Fatal("expected error for missing --token")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T", err)
	}
	if ee.Code() != ExitUsage {
		t.Errorf("code = %d, want ExitUsage", ee.Code())
	}
}

func TestClusterJoinMissingServerURL(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, "{}"))
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--token", "PGEDGE-abc")
	if err == nil {
		t.Fatal("expected error for missing --server-url")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T", err)
	}
	if ee.Code() != ExitUsage {
		t.Errorf("code = %d, want ExitUsage", ee.Code())
	}
}

func TestClusterJoinServerError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(500, "boom"))
	err := runControlplane(t, rt, out, url, "cluster", "join",
		"--token", "PGEDGE-abc",
		"--server-url", "http://existing:3000")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
