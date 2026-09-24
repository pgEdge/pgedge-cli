package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// requireUsageError fails unless err is non-nil and maps to the usage
// exit code. It unifies the two shapes a usage error can take: a
// cmd-package *ExitError{code: ExitUsage} (bad flag value, missing
// required flag) and a cli.UsageError from a declined confirmation —
// cli.ExitCode maps both to ExitUsage. Asserting the code, not just
// err != nil, guards against a refusal path that returns some unrelated
// error while still being non-nil.
func requireUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage): %v",
			got, ExitUsage, err)
	}
}

// newTestRuntime returns a Runtime wired to an isolated HOME and
// captured buffers, with every host variable testsupport.ClearEnv
// clears neutralised. Control Plane resolution reads no environment
// variable, so isolating HOME (which this delegates to testsupport.NewRuntime
// for) is what keeps a developer's real config — including whatever
// control-plane its own cp: section names — out of this test.
func newTestRuntime(t *testing.T, stdin, format string) (
	rt *module.Runtime, stdout, stderr *bytes.Buffer,
) {
	t.Helper()
	return testsupport.NewRuntime(t, stdin, format)
}

// jsonHandler writes status and body with a JSON content type.
func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// plainHandler writes status and body with a text/plain content type,
// so the generated parser leaves the typed JSON field nil.
func plainHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// capturedRequest records one HTTP request so tests can assert on what
// actually went over the wire (query params, body), not merely that the
// command exited without error.
type capturedRequest struct {
	method string
	path   string
	query  string
	body   string
}

// captureServer starts a test server that records the request into rec
// and replies with (status, respBody) as JSON. Callers assert on the
// recorded query/body to prove flags reach the wire.
func captureServer(
	t *testing.T, status int, respBody string, rec *capturedRequest,
) string {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	})
}

// newServer starts an httptest server routing all requests to handler
// and returns its base URL, which callers pass as --base-url.
func newServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// runControlplane builds the cp tree for rt and runs it with --base-url pointed
// at srvURL plus the given args.
func runControlplane(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	cmd := NewControlplaneCmd(rt)
	full := append([]string{}, args...)
	full = append(full, "--base-url", srvURL)
	cmd.SetArgs(full)
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

// TestEmitAccepted pins the three outcomes emitAccepted must produce:
// silence under text, the payload verbatim under json, and silence
// for a nil payload even under json (the belt-and-braces guard).
func TestEmitAccepted(t *testing.T) {
	type payload struct {
		TaskID string `json:"task_id"`
	}

	t.Run("text writes nothing", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		if err := emitAccepted(rt, payload{TaskID: "t1"}); err != nil {
			t.Fatalf("emitAccepted: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("want empty stdout under text, got %q", out.String())
		}
	})

	t.Run("json writes the payload", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		if err := emitAccepted(rt, payload{TaskID: "t1"}); err != nil {
			t.Fatalf("emitAccepted: %v", err)
		}
		if !strings.Contains(out.String(), `"task_id":"t1"`) {
			t.Errorf("want payload in stdout, got %q", out.String())
		}
	})

	t.Run("nil payload writes nothing under json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		if err := emitAccepted(rt, nil); err != nil {
			t.Fatalf("emitAccepted: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("want empty stdout for nil payload, got %q",
				out.String())
		}
	})
}
