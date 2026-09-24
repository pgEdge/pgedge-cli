// Package testsupport holds the test fixtures every Starfleet module's
// command tree needs: an isolated Runtime, the stub token/resource
// server, and the authenticated-command runner.
//
// It gives the environment-isolation list, which keeps a developer's
// shell settings out of the suite, one home: per-module copies drifted,
// one tree gaining a variable the others lacked, and the gap shows only
// as a test behaving differently under a developer's own shell.
// ClearEnv is split from NewRuntime for tests that need the names
// cleared with no Runtime built.
//
// It is a non-test package importing "testing" because Go has no other
// way to share fixtures across packages; nothing outside a test calls
// it. It must not import any module's cmd package, because the cmd
// packages' tests import this one.
package testsupport

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// hostEnv is every variable production code reads that ClearEnv must
// neutralise: shell settings that differ between developers, plus the
// PGEDGE_CLIENT_ID/PGEDGE_CLIENT_SECRET pair, which would otherwise sign
// every test in as the developer's CI credential. A variable added to
// production's env reads goes here or in the gate's exemptEnv, where
// HOME lives: TestEnvIsolationCoversProductionReads scans production
// sources for os.Getenv/os.LookupEnv (and os.UserHomeDir, as HOME) and
// fails until every name read is in one of them.
//
// The leaks are latent, which is why they are easy to leave out. Every
// assertion today is positive (a test sets NO_COLOR and checks the
// effect), and an inherited value cannot break one. A negative one
// ("colour is on by default", "no shell detected") would fail for a
// developer with NO_COLOR exported and pass in CI, or the reverse.
//
// SHELL is read by `completion` (install-path detection) and `doctor`;
// NO_COLOR by internal/cli's colour setup and `starfleet doctor`'s
// report; XDG_CONFIG_HOME by `completion`'s fish path
// (completionScriptPath) and resolvePwshProfile's fallback, where a
// stray value would put those paths outside the temp HOME.
var hostEnv = []string{
	"NO_COLOR",
	"PGEDGE_CLIENT_ID",
	"PGEDGE_CLIENT_SECRET",
	"SHELL",
	"XDG_CONFIG_HOME",
}

// clearedEnv is every name ClearEnv neutralises.
func clearedEnv() []string {
	return append([]string{}, hostEnv...)
}

// ClearEnv neutralises every variable in hostEnv and leaves HOME alone.
// It is NewRuntime's environment half, for tests with no constructed
// Runtime: cmd/pgedge's cases hand a subprocess the parent's
// os.Environ(), and internal/starfleet/conn's build their
// *module.Runtime by hand.
//
// t.Setenv(name, "") rather than os.Unsetenv: every non-test reader
// compares a plain os.Getenv against "", and no os.LookupEnv exists
// outside _test.go files, so "" is a faithful clear. t.Setenv also
// restores the developer's value on cleanup, which os.Unsetenv would
// not.
//
// Call it BEFORE setting any variable the test wants to control: it
// wipes whatever was set before it ran.
func ClearEnv(t *testing.T) {
	t.Helper()
	for _, name := range clearedEnv() {
		t.Setenv(name, "")
	}
}

// NewRuntime returns a Runtime wired to an isolated HOME and captured
// output buffers, with every variable in hostEnv cleared. Both
// returned buffers are also the Runtime's Output writers, so a caller
// can assert on rendered output as well as on raw writes.
func NewRuntime(t *testing.T, stdin, format string) (
	rt *module.Runtime, stdout, stderr *bytes.Buffer,
) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ClearEnv(t)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	rt = &module.Runtime{
		Config:  cfg,
		Profile: "default",
		Output:  &output.Renderer{Format: format, Out: stdout, Err: stderr},
		Stdin:   strings.NewReader(stdin),
		Stdout:  stdout,
		Stderr:  stderr,
	}
	return rt, stdout, stderr
}

// TokenBody is the canned OAuth token payload the stub servers return,
// so authenticated resource commands can obtain a bearer token without
// a real identity provider.
const TokenBody = `{"access_token":"tok",` +
	`"token_type":"Bearer","expires_in":3600}`

// TokenPath is where the account API serves the OAuth token endpoint,
// and therefore the only path a stub server must answer itself. It is
// namespaced: the CLI does not reference the retired bare /v1 surface.
const TokenPath = "/account/v1/oauth/token" //nolint:gosec // G101: a URL path, not a credential

// NewAuthedServer starts an httptest server that answers TokenPath with
// TokenBody and delegates every other request to resource. It returns
// the server's base URL, which callers pass as --api-url, so a resource
// command can authenticate and run fully hermetically.
func NewAuthedServer(t *testing.T, resource http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == TokenPath {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, TokenBody)
				return
			}
			resource(w, r)
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// JSONHandler returns a handler that writes status and body with a JSON
// content type. It is the building block for the per-endpoint stub
// responses the resource command tests rely on.
func JSONHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// PathHandler is JSONHandler for one path: it serves status and body
// when the request is for path and fails tb otherwise. JSONHandler
// discards the request, so a command asking for the wrong resource
// gets the right answer and every assertion downstream still holds;
// this is the stub for a test whose point is which path the command
// asked for.
func PathHandler(tb testing.TB, path string, status int,
	body string) http.HandlerFunc {
	tb.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			tb.Errorf("request for %s, want %s", r.URL.Path, path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// BrokenHandler hijacks and immediately closes the connection so the API
// client sees a transport error (rather than an HTTP status). It drives
// the `if err != nil { return fmt.Errorf(...) }` branch that follows every
// generated *WithResponse call, and is shared because the byoc and
// account cmd tests table-test that branch across their whole
// resource-command surface.
func BrokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		c, _, err := hj.Hijack()
		if err == nil {
			_ = c.Close()
		}
	}
}

// declaresArgument reports whether use names a positional argument
// after the command's name, which tells a hybrid like
// "restore <database_id>" (an action with a required argument) from a
// pure router like "cluster". Every token counts except "[flags]",
// cobra's suffix for a runnable command's usage line (Command.UseLine),
// which a Use may also spell out by hand.
//
// Counting an unrecognised token is the quiet default, not the safe
// one. True means hybrid, and IsPureRouter's callers skip hybrids, so it
// removes a command from both pure-router checks (internal/clitest's
// RunE-presence assertion and WalkPureRouters' bare-invocation
// assertion). Growing the "[flags]" exception puts more commands under
// the gate, not fewer.
//
// So a Use with another decorative token, "database [command]" say, is
// misclassified as a hybrid and silently dropped from both checks.
// Nothing in the tree does that today. If a router ever appears to be
// exempt for no reason, look here first.
func declaresArgument(use string) bool {
	fields := strings.Fields(use)
	for _, f := range fields[1:] {
		if f != "[flags]" {
			return true
		}
	}
	return false
}

// IsPureRouter reports whether c is a "pure router": a non-root group
// command whose Use declares no positional argument (see
// declaresArgument), unlike a hybrid such as "pgedge controlplane
// database restore <database_id>", which is itself an action.
//
// It is structural only; callers set their own policy for a pure router
// with neither RunE nor Run. internal/clitest fails it (absence is the
// bug it gates); WalkPureRouters skips it (it only exercises a real
// invocation).
func IsPureRouter(c *cobra.Command) bool {
	return c.HasParent() && c.HasSubCommands() && !declaresArgument(c.Use)
}

// RunBareCapture invokes c with no arguments via whichever of RunE or
// Run is set, capturing c's out and err in one buffer, and returns the
// buffer and RunE's error. With neither set it returns ("", nil):
// whether one must exist is a policy IsPureRouter's callers decide.
//
// Handling Run as well is essential: c.RunE(c, nil) on a Run-only router
// is a nil pointer dereference, and a panicking test takes its whole
// package's run down and reports nothing useful. Nothing in the shipped
// tree uses bare Run today.
func RunBareCapture(c *cobra.Command) (captured string, err error) {
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	switch {
	case c.RunE != nil:
		err = c.RunE(c, nil)
	case c.Run != nil:
		c.Run(c, nil)
	}
	return buf.String(), err
}

// LooksLikeHelp reports whether text resembles cobra's default help
// rendering, which a pure router's bare invocation ("return c.Help()")
// should print. Non-empty is not enough: one stray character or an
// unrelated message would pass that. Every cobra help template renders
// "Usage:" (command.go's default UsageTemplate), so its presence shows
// help was printed, not just something.
func LooksLikeHelp(text string) bool {
	return strings.Contains(text, "Usage:")
}

// pureRouterViolation names one pure-router command path whose bare
// invocation (no args) did not behave: it errored, or its output did
// not look like a help rendering.
type pureRouterViolation struct {
	Path    string
	Message string
}

// pureRouterViolations walks root's command tree and, for every pure
// router (IsPureRouter) with a RunE or Run to invoke, calls it with no
// arguments and records a violation unless it succeeds and its output
// looks like a help rendering (LooksLikeHelp).
//
// It is WalkPureRouters' *testing.T-free core, so this package's tests
// can assert that a bad tree does produce a violation: through a live
// *testing.T the check would fail the enclosing test itself.
func pureRouterViolations(root *cobra.Command) []pureRouterViolation {
	var out []pureRouterViolation
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if IsPureRouter(c) && (c.RunE != nil || c.Run != nil) {
			captured, err := RunBareCapture(c)
			switch {
			case err != nil:
				out = append(out, pureRouterViolation{c.CommandPath(),
					fmt.Sprintf("bare invocation returned an error: %v",
						err)})
			case !LooksLikeHelp(captured):
				out = append(out, pureRouterViolation{c.CommandPath(),
					"bare invocation did not print a help rendering"})
			}
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return out
}

// WalkPureRouters reports a pureRouterViolations(root) failure for every
// pure-router group under root whose bare invocation (no args) errors or
// does not print a help rendering.
//
// It is the runtime half of internal/clitest's structural gate
// (TestCommandTreeConformance), duplicated rather than called from
// there because `go test ./...` runs with no -coverpkg: a check living
// only in internal/clitest exercises the byoc, controlplane, account and
// cli packages but credits none of their coverage.out entries, so each
// runs this from its own test binary. It catches a no-op RunE (return
// nil, print nothing), which satisfies cobra and every static check yet
// silently succeeds on a bare invocation.
func WalkPureRouters(t *testing.T, root *cobra.Command) {
	t.Helper()
	for _, v := range pureRouterViolations(root) {
		t.Errorf("%s: %s", v.Path, v.Message)
	}
}

// RunAuthed executes root against the stub server at srvURL, supplying
// dummy credentials through the persistent --client-id / --client-secret
// flags and pointing --api-url at the stub. Output and errors are
// captured into out.
//
// It takes root rather than a Runtime because each module builds its own
// tree, and importing those constructors here would cycle. Callers wrap
// it in a three-line adapter naming their own root.
func RunAuthed(t *testing.T, root *cobra.Command, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	full := make([]string, 0, len(args)+6)
	full = append(full, args...)
	full = append(full,
		"--client-id", "id",
		"--client-secret", "secret",
		"--api-url", srvURL)
	root.SetArgs(full)
	root.SetOut(out)
	root.SetErr(out)
	return root.Execute()
}
