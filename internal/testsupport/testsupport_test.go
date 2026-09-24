package testsupport

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/spf13/cobra"
)

// capturedRun records what the throwaway command below saw when it ran.
type capturedRun struct {
	flags map[string]string
	args  []string
}

// newFlagCapturingRoot builds a minimal stand-in for a module root: the
// same three persistent credential flags every Starfleet module registers,
// plus one child that records the resolved flag values and its
// positional args.
//
// RunAuthed is deliberately generic over the root command so this
// package never imports a module's cmd package. This fixture is how that
// genericity gets tested without one.
func newFlagCapturingRoot() (*cobra.Command, *bytes.Buffer, *capturedRun) {
	seen := &capturedRun{flags: map[string]string{}}

	root := &cobra.Command{Use: "stub", SilenceUsage: true}
	pf := root.PersistentFlags()
	pf.String("api-url", "", "stub")
	pf.String("client-id", "", "stub")
	pf.String("client-secret", "", "stub")

	root.AddCommand(&cobra.Command{
		Use: "child",
		RunE: func(c *cobra.Command, args []string) error {
			for _, name := range []string{
				"api-url", "client-id", "client-secret",
			} {
				v, err := c.Flags().GetString(name)
				if err != nil {
					return err
				}
				seen.flags[name] = v
			}
			seen.args = args
			fmt.Fprintln(c.OutOrStdout(), "ran child")
			return nil
		},
	})

	out := &bytes.Buffer{}
	return root, out, seen
}

// TestNewRuntimeIsolatesHome guards NewRuntime's HOME isolation: config,
// cache and every other on-disk path a command touches are derived from
// HOME, so a test suite that failed to isolate it would read and write
// the developer's real ~/.pgedge. It asserts both that NewRuntime
// overrides HOME away from the real one and that a production path
// (config.DefaultPath) actually derives from the overridden value —
// the second is what stops a future NewRuntime from setting HOME
// without anything downstream honouring it.
func TestNewRuntimeIsolatesHome(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve the real home directory: %v", err)
	}

	rt, stdout, stderr := NewRuntime(t, "input", "json")

	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("HOME is empty")
	}
	if home == realHome {
		t.Fatalf("HOME = %q, want it overridden away from the real "+
			"home directory", home)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read HOME: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("HOME %q is not empty (%d entries) — not isolated",
			home, len(entries))
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		t.Fatalf("config.DefaultPath: %v", err)
	}
	if !strings.HasPrefix(cfgPath, home) {
		t.Errorf("config.DefaultPath() = %q, want it under the "+
			"overridden HOME %q", cfgPath, home)
	}
	if rt.Output.Format != "json" {
		t.Errorf("Output.Format = %q, want json", rt.Output.Format)
	}
	if rt.Stdout != stdout || rt.Stderr != stderr {
		t.Error("returned buffers are not the Runtime's writers")
	}
	buf := make([]byte, 5)
	if _, err := rt.Stdin.Read(buf); err != nil {
		t.Fatalf("read Stdin: %v", err)
	}
	if string(buf) != "input" {
		t.Errorf("Stdin = %q, want \"input\"", buf)
	}
}

// pureRouterLeaf is the child every synthetic router below needs so
// it satisfies HasSubCommands(); its own Run is irrelevant to the
// check under test.
func pureRouterLeaf() *cobra.Command {
	return &cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}}
}

// realRouter builds a pure router whose RunE is the real
// "return c.Help()" idiom every fixed group command in this task
// uses, under the given Use. Real cobra help output naturally
// contains "Usage:", which is what LooksLikeHelp checks for — using
// the real idiom here (rather than an Fprintln stand-in) is what
// makes the fixture representative of production commands.
func realRouter(use string) *cobra.Command {
	c := &cobra.Command{
		Use: use,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	c.AddCommand(pureRouterLeaf())
	return c
}

// TestPureRouterViolationsPassesRealRouter proves the check does not
// flag a router whose RunE actually does something (the "return
// c.Help()" idiom every fixed group command in this task uses).
// Asserted directly against pureRouterViolations, not through a live
// *testing.T: a passing case has nothing to hide from propagation,
// but its sibling below does, and the two must use the same call
// shape to be a meaningful pair.
func TestPureRouterViolationsPassesRealRouter(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(realRouter("good"))

	if got := pureRouterViolations(root); len(got) != 0 {
		t.Errorf("violations = %+v, want none", got)
	}
}

// TestPureRouterViolationsCatchesNoOpRouter is the mutation proof: a
// no-op RunE (return nil, print nothing) satisfies cobra and every
// static check while silently succeeding on a bare invocation —
// exactly the bug this task closes, just triggered by zero args
// instead of a stray one.
//
// This asserts against pureRouterViolations directly rather than
// running WalkPureRouters through a live *testing.T: a failing
// subtest created via t.Run always propagates failure to every
// ancestor test up to the top-level `go test` result, so there is no
// way to assert "this check must fail" through the reporting path
// itself without failing the whole suite. Splitting the *testing.T
// reporting from the underlying computation is what makes this
// provable at all.
func TestPureRouterViolationsCatchesNoOpRouter(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	bad := &cobra.Command{
		Use:  "bad",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	bad.AddCommand(pureRouterLeaf())
	root.AddCommand(bad)

	got := pureRouterViolations(root)
	if len(got) != 1 {
		t.Fatalf("violations = %+v, want exactly 1", got)
	}
	if got[0].Path != "root bad" {
		t.Errorf("violation path = %q, want %q", got[0].Path, "root bad")
	}
	const want = "bare invocation did not print a help rendering"
	if got[0].Message != want {
		t.Errorf("violation message = %q, want %q", got[0].Message, want)
	}
}

// TestPureRouterViolationsCatchesNonHelpOutput proves the Minor fix:
// a router that prints *something* non-empty but not a help
// rendering must still be flagged. Before this fix, the check only
// asserted buf.Len() != 0, which a stray character or an unrelated
// message would also have satisfied.
func TestPureRouterViolationsCatchesNonHelpOutput(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	bad := &cobra.Command{
		Use: "bad",
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprintln(c.OutOrStdout(), "x")
			return nil
		},
	}
	bad.AddCommand(pureRouterLeaf())
	root.AddCommand(bad)

	got := pureRouterViolations(root)
	if len(got) != 1 {
		t.Fatalf("violations = %+v, want exactly 1", got)
	}
	const want = "bare invocation did not print a help rendering"
	if got[0].Message != want {
		t.Errorf("violation message = %q, want %q", got[0].Message, want)
	}
}

// TestPureRouterViolationsHandlesRunOnly is Important 1's mutation
// proof: a pure router written with Run instead of RunE must be
// invoked and checked like any other, not skipped (the bug in this
// package's earlier guard, c.RunE != nil) and not panicked on (the
// bug in internal/clitest's earlier guard, which called
// c.RunE(c, nil) unconditionally once EITHER hook was non-nil). A
// panic here would take this whole test binary down, which is itself
// part of what this test proves by completing at all.
func TestPureRouterViolationsHandlesRunOnly(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use: "good",
		Run: func(c *cobra.Command, _ []string) {
			_ = c.Help()
		},
	})
	good := root.Commands()[0]
	good.AddCommand(pureRouterLeaf())

	bad := &cobra.Command{
		Use: "bad",
		Run: func(*cobra.Command, []string) {},
	}
	bad.AddCommand(pureRouterLeaf())
	root.AddCommand(bad)

	got := pureRouterViolations(root)
	if len(got) != 1 {
		t.Fatalf("violations = %+v, want exactly 1 (only 'bad')", got)
	}
	if got[0].Path != "root bad" {
		t.Errorf("violation path = %q, want %q", got[0].Path, "root bad")
	}
}

// TestDeclaresArgument pins both directions of the classification.
//
// Read the direction carefully — the predicate is NOT fail-closed:
// "restore [database_id]" is reachable ONLY through the catch-all
// default, is therefore not positively recognised, and yields true,
// which makes the command a hybrid and EXEMPTS it from both
// pure-router checks. See declaresArgument's own comment for the
// direction and the residual it leaves.
//
// What the table actually pins:
//
//   - false for the three cosmetic shapes — a bare name, a stray
//     trailing space, cobra's own "[flags]" suffix — so none of them
//     can quietly exempt a router from being checked.
//   - true for both genuine argument spellings, "<id>" and "[id]".
//
// That last row is also why swapping the catch-all default for a
// "<"-prefix recogniser is not a drop-in: it would return false for
// "restore [database_id]" and turn this row red.
func TestDeclaresArgument(t *testing.T) {
	cases := []struct {
		name string
		use  string
		want bool
	}{
		{"plain single-word router", "cluster", false},
		{"trailing space", "cluster ", false},
		{"cobra's own [flags] suffix", "cluster [flags]", false},
		{"genuine required-arg hybrid", "restore <database_id>", true},
		{"genuine optional-arg hybrid", "restore [database_id]", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := declaresArgument(tc.use); got != tc.want {
				t.Errorf("declaresArgument(%q) = %v, want %v",
					tc.use, got, tc.want)
			}
		})
	}
}

// TestIsPureRouterKeepsCosmeticUseInScope carries the property the
// pure-router checks depend on: a command whose Use gains a trailing
// space or a "[flags]" suffix must remain a pure router — still
// checked — while a real argument declaration exempts it. (The name
// says "keeps in scope", not "fails closed", on purpose: classifying
// a command as a hybrid removes it from both checks, so the catch-all
// default is the permissive direction, not the closed one.)
//
// Each case carries a RunE and a child, so IsPureRouter's other two
// conditions (HasParent, HasSubCommands) are satisfied and
// declaresArgument's contribution is what is under test.
func TestIsPureRouterKeepsCosmeticUseInScope(t *testing.T) {
	cases := []struct {
		name string
		use  string
		want bool
	}{
		{"plain single-word router", "cluster", true},
		{"trailing space", "cluster ", true},
		{"cobra's own [flags] suffix", "cluster [flags]", true},
		{"genuine required-arg hybrid", "restore <database_id>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "root"}
			c := &cobra.Command{
				Use: tc.use,
				RunE: func(c *cobra.Command, _ []string) error {
					return c.Help()
				},
			}
			c.AddCommand(pureRouterLeaf())
			root.AddCommand(c)

			if got := IsPureRouter(c); got != tc.want {
				t.Errorf("IsPureRouter(Use=%q) = %v, want %v",
					tc.use, got, tc.want)
			}
		})
	}
}

// TestNewAuthedServer pins both halves of the stub: the token endpoint
// answers TokenBody, and everything else reaches the caller's handler.
func TestNewAuthedServer(t *testing.T) {
	gotPath := ""
	url := NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		JSONHandler(http.StatusTeapot, `{"ok":false}`)(w, r)
	})

	tok, err := http.Post(url+"/account/v1/oauth/token", "application/json", nil)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	defer func() { _ = tok.Body.Close() }()
	body, err := io.ReadAll(tok.Body)
	if err != nil {
		t.Fatalf("read token body: %v", err)
	}
	if string(body) != TokenBody {
		t.Errorf("token body = %q, want TokenBody", body)
	}
	if ct := tok.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("token Content-Type = %q, want application/json", ct)
	}
	if gotPath != "" {
		t.Errorf("the resource handler saw the token path %q", gotPath)
	}

	res, err := http.Get(url + "/byoc/v1/clusters")
	if err != nil {
		t.Fatalf("resource request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if gotPath != "/byoc/v1/clusters" {
		t.Errorf("resource handler saw path %q, want /byoc/v1/clusters", gotPath)
	}
	if res.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d (the handler's own status, so "+
			"JSONHandler is wired through)", res.StatusCode,
			http.StatusTeapot)
	}
}

// TestRunAuthed proves the runner appends all three credential flags and
// routes cobra's output into out, using a throwaway root so no module
// package is imported here.
func TestRunAuthed(t *testing.T) {
	root, out, seen := newFlagCapturingRoot()

	if err := RunAuthed(t, root, out, "https://stub.example",
		"child", "positional"); err != nil {
		t.Fatalf("RunAuthed: %v", err)
	}

	want := map[string]string{
		"client-id":     "id",
		"client-secret": "secret",
		"api-url":       "https://stub.example",
	}
	for name, wantVal := range want {
		if seen.flags[name] != wantVal {
			t.Errorf("--%s = %q, want %q", name, seen.flags[name], wantVal)
		}
	}
	if len(seen.args) != 1 || seen.args[0] != "positional" {
		t.Errorf("positional args = %v, want [positional] — RunAuthed "+
			"must append its flags after the caller's args", seen.args)
	}
	if !strings.Contains(out.String(), "ran child") {
		t.Errorf("out did not capture the command's output: %q",
			out.String())
	}
}

// recordingTB records Errorf calls so a handler that fails the test
// can be tested without failing this one.
type recordingTB struct {
	testing.TB
	failures []string
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// TestPathHandler pins both halves: the right path is served like
// JSONHandler, and the wrong path still answers but fails the test.
func TestPathHandler(t *testing.T) {
	for _, tc := range []struct {
		name         string
		request      string
		wantFailures int
	}{
		{"matching path", "/v1/things/abc", 0},
		{"other path", "/v1/things/xyz", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingTB{TB: t}
			srv := httptest.NewServer(
				PathHandler(rec, "/v1/things/abc", 200, `{"ok":true}`))
			defer srv.Close()

			resp, err := http.Get(srv.URL + tc.request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
				t.Errorf("got %d %q, want the stub body", resp.StatusCode, body)
			}
			if len(rec.failures) != tc.wantFailures {
				t.Errorf("recorded %d failures, want %d: %v",
					len(rec.failures), tc.wantFailures, rec.failures)
			}
			if tc.wantFailures == 1 && !strings.Contains(rec.failures[0], tc.request) {
				t.Errorf("failure does not name the path asked for: %q", rec.failures[0])
			}
		})
	}
}
