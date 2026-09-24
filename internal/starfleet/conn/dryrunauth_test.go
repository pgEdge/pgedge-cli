package conn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTokenExchangeIsNotInterceptedByDryRun is the carve-out gate.
//
// The OAuth token exchange is a POST. If the dry-run transport reached
// it, every dry run would fail at authentication before running a single
// read — and the reads are the whole point of the semantics, since the
// create-vs-reconfigure intent guard cannot decide anything without one.
//
// This asserts the property directly rather than checking that some URL
// path is exempt, which is why the carve-out is by client: there is no
// path string here to drift from the spec.
func TestTokenExchangeIsNotInterceptedByDryRun(t *testing.T) {
	var tokenPosts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != testsupport.TokenPath {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method != http.MethodPost {
				t.Errorf("token exchange used %s, want POST", r.Method)
			}
			tokenPosts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(testsupport.TokenBody))
		}))
	t.Cleanup(srv.Close)

	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()

	tok, err := Login(context.Background(), rt, srv.URL, "id", "secret", RequestTimeout)
	if err != nil {
		t.Fatalf("Login under --dry-run failed: %v", err)
	}
	if tok.AccessToken == "" {
		t.Error("no token returned")
	}
	if n := tokenPosts.Load(); n != 1 {
		t.Errorf("token endpoint hit %d times, want 1", n)
	}
	// The exchange must not show up in the report either: it is not a
	// write the user asked for, and reporting it would be a lie about
	// what the command would have done.
	if rt.DryRun.Intercepted() {
		t.Errorf("the token exchange was recorded as the stopped "+
			"write: %+v", rt.DryRun.Request())
	}
}

// TestHTTPClientForInstallsDryRunWithoutVerbose covers the trap in the
// Off short-circuit: testing the log level alone would mean --dry-run
// silently did nothing unless the user also passed --verbose.
func TestHTTPClientForInstallsDryRunWithoutVerbose(t *testing.T) {
	rt := &module.Runtime{DryRun: dryrun.New()} // Verbose/Debug false
	if got := HTTPClientFor(rt, RequestTimeout); got == http.DefaultClient {
		t.Fatal("a dry run got the plain default client: the Off " +
			"short-circuit ignored rt.DryRun, so nothing would be " +
			"intercepted")
	}
}

// TestHTTPClientForCarriesOnlyTheRepairWhenNothingAsked pins the quiet
// path. It used to be the SHARED DEFAULT CLIENT — no wrapper, no
// allocation — but the error-body repair has to run on every command
// so the quiet path now carries exactly one layer and no
// more. Neither the logger nor the dry-run interceptor may appear.
func TestHTTPClientForCarriesOnlyTheRepairWhenNothingAsked(t *testing.T) {
	rt := &module.Runtime{}
	got := HTTPClientFor(rt, RequestTimeout)
	eb, ok := got.Transport.(*errbody.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *errbody.Transport",
			got.Transport)
	}
	if eb.Base != http.DefaultTransport {
		t.Errorf("quiet path stacked an extra layer under the repair: "+
			"%T", eb.Base)
	}
}

// TestAuthHTTPClientForNeverCarriesDryRun asserts the carve-out at the
// constructor, so a future edit that "tidied" the two functions into one
// fails here rather than in a live dry run.
func TestAuthHTTPClientForNeverCarriesDryRun(t *testing.T) {
	rt := &module.Runtime{DryRun: dryrun.New()}
	c := authHTTPClientFor(rt, RequestTimeout)
	// The carve-out is asserted on the transport rather than on
	// client identity: the quiet branch builds its own client, since
	// a Timeout on the shared global would bound every other caller
	// in the process.
	if c.Transport != nil {
		t.Errorf("authHTTPClientFor returned a wrapped client (%T); "+
			"with no --verbose/--debug it must add no transport",
			c.Transport)
	}
	if c == http.DefaultClient {
		t.Error("authHTTPClientFor handed out the shared " +
			"http.DefaultClient, which cannot be bounded")
	}

	// And with --debug on, it must carry the diagnostic transport but
	// still not the dry-run one.
	rt.Debug = true
	if _, ok := authHTTPClientFor(rt, RequestTimeout).Transport.(*dryrun.Transport); ok {
		t.Error("authHTTPClientFor installed the dry-run transport")
	}
}

// TestHTTPClientForWrapsDryRunOutsideHTTPLog pins the ordering. Inside
// out, a write would be logged by httplog and then refused, so the user
// would see the request twice in two formats — and the log line would
// claim a request had been sent.
func TestHTTPClientForWrapsDryRunOutsideHTTPLog(t *testing.T) {
	rt := &module.Runtime{DryRun: dryrun.New(), Debug: true}
	c := HTTPClientFor(rt, RequestTimeout)
	if _, ok := c.Transport.(*dryrun.Transport); !ok {
		t.Fatalf("outermost transport is %T, want *dryrun.Transport",
			c.Transport)
	}
}
