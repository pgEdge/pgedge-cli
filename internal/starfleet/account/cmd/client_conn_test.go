package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// newProbeCmd returns a starfleet tree carrying one extra child that calls
// clientFromCmd and then issues one read against the Accounts API,
// recording what it got back. clientFromCmd must be exercised through
// a real cobra command so the inherited-flag lookup is under test,
// not bypassed.
func newProbeCmd(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	captured *probeResult,
) *cobra.Command {
	t.Helper()
	root := newStarfleetRoot(rt)
	root.AddCommand(&cobra.Command{
		Use:   "probe",
		Short: "Test-only probe",
		RunE: func(c *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, c)
			if err != nil {
				return err
			}
			resp, err := client.ListTenantsWithResponse(c.Context())
			if err != nil {
				return err
			}
			captured.status = resp.StatusCode()
			captured.parsed = resp.JSON200 != nil
			return nil
		},
	})
	root.SetOut(out)
	root.SetErr(out)
	return root
}

type probeResult struct {
	status int
	parsed bool
}

// TestClientFromCmd drives clientFromCmd end to end: it must read the
// account root's persistent overrides off the child command's
// inherited flag set, exchange the credentials for a token, and attach
// that token as a bearer header on the resource request.
func TestClientFromCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "json")

	var gotAuth, gotPath string
	srvURL := testsupport.NewAuthedServer(t, func(
		w http.ResponseWriter, r *http.Request,
	) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		// An empty JSON array is what the vendored spec declares for
		// GET /account/v1/tenants (the generated JSON200 is *[]Tenant). Only
		// the shape matters here, not the contents: this test asserts
		// the request side plus the fact that the generated parser ran.
		testsupport.JSONHandler(http.StatusOK, `[]`)(w, r)
	})

	out := &bytes.Buffer{}
	got := &probeResult{}
	root := newProbeCmd(t, rt, out, got)
	root.SetArgs([]string{"probe",
		"--client-id", "id",
		"--client-secret", "secret",
		"--api-url", srvURL})

	if err := root.Execute(); err != nil {
		t.Fatalf("probe: %v\n%s", err, out.String())
	}
	if gotPath != "/account/v1/tenants" {
		t.Errorf("request path = %q, want /account/v1/tenants", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want \"Bearer tok\"", gotAuth)
	}
	if got.status != http.StatusOK {
		t.Errorf("status = %d, want 200", got.status)
	}
	if !got.parsed {
		t.Error("JSON200 is nil — the generated response parser " +
			"did not run, so the client is not fully wired")
	}
}

// TestClientFromCmdReusesCachedToken proves a command does not re-login
// when the profile already holds a live token. The stub answers the
// token endpoint with a 500, so any exchange attempt would fail the
// test outright; the request must instead carry the cached token.
func TestClientFromCmdReusesCachedToken(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "json")

	var gotAuth string
	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.URL.Path == "/account/v1/oauth/token" {
			tokenCalls++
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		testsupport.JSONHandler(http.StatusOK, `[]`)(w, r)
	}))
	t.Cleanup(srv.Close)

	// probe is invoked below with --client-id id --client-secret secret
	// and --api-url srv.URL, so the seeded cache must be bound to that
	// same connection or the MintedBy check on the hot path would
	// (correctly) treat it as a mismatch and force a fresh exchange,
	// defeating this test's point. That is also why the seed happens
	// after the stub exists: the binding covers the endpoint, so
	// it cannot be written before the endpoint has an address.
	if err := conn.Store(rt).SaveToken(&auth.CachedToken{
		AccessToken: "cached-tok",
		ExpiresAt:   time.Now().Add(time.Hour),
		Fingerprint: auth.Fingerprint(srv.URL, "id", "secret"),
	}); err != nil {
		t.Fatalf("seed token cache: %v", err)
	}

	out := &bytes.Buffer{}
	root := newProbeCmd(t, rt, out, &probeResult{})
	root.SetArgs([]string{"probe",
		"--client-id", "id",
		"--client-secret", "secret",
		"--api-url", srv.URL})

	if err := root.Execute(); err != nil {
		t.Fatalf("probe: %v\n%s", err, out.String())
	}
	if tokenCalls != 0 {
		t.Errorf("token endpoint called %d times, want 0 — the cached "+
			"token was not reused", tokenCalls)
	}
	if gotAuth != "Bearer cached-tok" {
		t.Errorf("Authorization = %q, want \"Bearer cached-tok\"", gotAuth)
	}
}

// TestClientFromCmdNoCredentials pins the failure mode: with no flags
// and an empty profile there is nothing to authenticate with, and the
// error must carry the auth exit code rather than the generic one.
//
// It asserts the message too, not just the exit code. A leaked profile
// (see testsupport.NewRuntime, which points HOME at a fresh t.TempDir()
// so no real ~/.pgedge/cli/config.yaml can be read) also produces an
// ExitAuth error — from a *failed token exchange over the network* —
// so the exit code alone cannot tell "nothing configured" apart from
// "configured and rejected", and this test would have passed while
// making a live call.
func TestClientFromCmdNoCredentials(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "json")

	out := &bytes.Buffer{}
	root := newProbeCmd(t, rt, out, &probeResult{})
	root.SetArgs([]string{"probe"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error with no credentials, got nil")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error is %T, want *ExitError: %v", err, err)
	}
	if exitErr.Code() != ExitAuth {
		t.Errorf("Code() = %d, want ExitAuth (%d)",
			exitErr.Code(), ExitAuth)
	}
	if !strings.Contains(err.Error(), "no credentials found") {
		t.Errorf("error = %q, want it to report no credentials — an "+
			"\"authentication failed\" message here means credentials "+
			"leaked in and were exchanged over the network", err)
	}
}

// TestCheckResponse covers the aliases this tree re-exports from conn,
// so a future edit that repoints them at a different implementation
// still has to map statuses to the documented exit codes.
func TestCheckResponse(t *testing.T) {
	// body defaults to "body" for every row except 404: a bare,
	// code-less string is no longer a resource miss, so
	// the 404 row needs a realistic handler-shaped body to still
	// exercise ExitNotFound here.
	tests := []struct {
		name   string
		status int
		body   string
		want   int // -1 means "expect no error"
	}{
		{"ok", http.StatusOK, "body", -1},
		{"created", http.StatusCreated, "body", -1},
		{"unauthorized", http.StatusUnauthorized, "body", ExitAuth},
		{"forbidden", http.StatusForbidden, "body", ExitAuth},
		{"not found", http.StatusNotFound,
			`{"code":404,"message":"body"}`, ExitNotFound},
		{"gateway timeout", http.StatusGatewayTimeout, "body", ExitTimeout},
		{"server error", http.StatusInternalServerError, "body", ExitGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkResponse(tt.status, tt.body)
			if tt.want == -1 {
				if err != nil {
					t.Fatalf("checkResponse(%d) = %v, want nil",
						tt.status, err)
				}
				return
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("checkResponse(%d) = %v, want *ExitError",
					tt.status, err)
			}
			if exitErr.Code() != tt.want {
				t.Errorf("Code() = %d, want %d",
					exitErr.Code(), tt.want)
			}
		})
	}
}

func TestNewExitError(t *testing.T) {
	err := newExitError("boom", ExitTimeout)
	if err.Error() != "boom" {
		t.Errorf("Error() = %q, want \"boom\"", err.Error())
	}
	if err.Code() != ExitTimeout {
		t.Errorf("Code() = %d, want %d", err.Code(), ExitTimeout)
	}
}
