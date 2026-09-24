package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestClientNoCredentials verifies clientFromCmd -> newAPIClient
// surfaces an auth error when no credentials are available. No network
// is involved: credential resolution fails before any token fetch.
func TestClientNoCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	// runByoc (not runAuthed) so no --client-id/--client-secret are set.
	err := runByoc(t, rt, out, "cluster", "list",
		"--api-url", "http://127.0.0.1:0")
	if err == nil {
		t.Fatal("expected auth error with no credentials")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitAuth {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitAuth)
	}
}

// TestClientTokenEndpointFails verifies a failing /account/v1/oauth/token
// exchange turns into an auth error.
func TestClientTokenEndpointFails(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	// A NewAuthedServer handler is only reached for non-oauth paths;
	// here the token endpoint itself must fail, so bypass
	// testsupport.NewAuthedServer and serve 401 for everything,
	// /account/v1/oauth/token included.
	url := newRawServer(t, testsupport.JSONHandler(401, `denied`))
	err := runAuthed(t, rt, out, url, "cluster", "list")
	if err == nil {
		t.Fatal("expected auth error when token endpoint fails")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitAuth {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitAuth)
	}
}

// TestClientVerboseTransport exercises the verbose RoundTripper, which
// logs each request/response to stderr.
func TestClientVerboseTransport(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	rt.Verbose = true
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
	if err := runAuthed(t, rt, out, url, "cluster", "list"); err != nil {
		t.Fatalf("verbose cluster list: %v", err)
	}
	if !strings.Contains(errb.String(), "GET") {
		t.Errorf("verbose transport did not log request: %q", errb.String())
	}
}
