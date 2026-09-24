package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// The isolated Runtime (testsupport.NewRuntime), the stub token/resource
// server (testsupport.NewAuthedServer, testsupport.JSONHandler,
// testsupport.TokenBody), the transport-error stub
// (testsupport.BrokenHandler) and the authenticated runner
// (testsupport.RunAuthed) are shared with internal/starfleet/account/cmd. What
// stays here is byoc-specific: fixtures over byoc's own generated
// client, and the two adapters that name byoc's root command.

// newTestClient returns an API client wired to an httptest server
// running handler. It is the fixture ported resolve/wait tests use
// to exercise resolveClusterID, resolveDatabaseID, and the task-wait
// helpers against canned HTTP responses.
func newTestClient(
	t *testing.T, handler http.HandlerFunc,
) *api.ClientWithResponses {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("create test client: %v", err)
	}
	return client
}

// newStarfleetRoot returns a synthetic `starfleet` root carrying the four
// persistent connection flags, with the byoc sub-tree mounted under
// it. It stands in for cloudcmd.NewStarfleetCmd, which this package's
// tests cannot import: internal/starfleet/cmd imports this package, so
// importing it back would be a cycle. The flag names and the fact
// they are persistent are the whole contract being reproduced —
// internal/clitest gates the real root.
func newStarfleetRoot(rt *module.Runtime) *cobra.Command {
	root := &cobra.Command{Use: "starfleet"}
	pf := root.PersistentFlags()
	pf.String("api-url", "", "pgEdge Starfleet API base URL")
	pf.String("client-id", "", "API client ID")
	pf.String("client-secret", "", "API client secret")
	pf.Duration("timeout", conn.RequestTimeout,
		"Per-request timeout (Go duration; 0 disables)")
	root.AddCommand(NewByocCmd(rt))
	return root
}

// byocArgs prefixes args with the sub-tree token, so every existing
// test case keeps its own argument vector unchanged.
func byocArgs(args []string) []string {
	return append([]string{"byoc"}, args...)
}

// runByoc builds the starfleet tree for rt and executes its byoc sub-tree
// with the given args, returning combined help/stdout and any error.
// The rt buffers still capture command output written via rt.Stdout /
// rt.Output.
func runByoc(t *testing.T, rt *module.Runtime,
	out *bytes.Buffer, args ...string,
) error {
	t.Helper()
	cmd := newStarfleetRoot(rt)
	cmd.SetArgs(byocArgs(args))
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

// newRawServer starts an httptest server that routes every request,
// including /account/v1/oauth/token, to handler. Use it to exercise failures
// in the token exchange itself.
func newRawServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// runAuthed runs a byoc command against the stub server at srvURL,
// supplying dummy credentials through the persistent --client-id /
// --client-secret flags and pointing --api-url at the stub. It is the
// entry point for exercising a resource command's RunE end to end.
func runAuthed(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	return testsupport.RunAuthed(t, newStarfleetRoot(rt), out, srvURL,
		byocArgs(args)...)
}
