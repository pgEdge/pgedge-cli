package cmd

import (
	"bytes"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// The isolated Runtime, the stub token/resource server and the
// authenticated runner all live in internal/testsupport, shared with
// the byoc and managed test suites. Only the synthetic root and the
// two adapters over it are local.

// newStarfleetRoot returns a synthetic `starfleet` root: the three persistent
// connection flags this package's commands read, plus every
// account-level command mounted directly under it, exactly as
// cloudcmd.NewStarfleetCmd mounts them.
//
// It stands in for cloudcmd.NewStarfleetCmd rather than calling it,
// because internal/starfleet/cmd imports this package — importing it back
// from an in-package test file would be an import cycle. What it
// reproduces is the flag contract (three flags, persistent, these
// names); the real root is gated by internal/clitest.
func newStarfleetRoot(rt *module.Runtime) *cobra.Command {
	f := &conn.Flags{}
	root := &cobra.Command{
		Use:  "starfleet",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&f.APIURL, "api-url", "", "pgEdge Starfleet API base URL")
	pf.StringVar(&f.ClientID, "client-id", "", "API client ID")
	pf.StringVar(&f.ClientSecret, "client-secret", "", "API client secret")

	root.AddCommand(NewAuthCmd(rt, f))
	root.AddCommand(NewDoctorCmd(rt, f))
	root.AddCommand(NewInviteCmd(rt))
	root.AddCommand(NewMembershipCmd(rt))
	root.AddCommand(NewAPIClientCmd(rt))
	root.AddCommand(NewTenantCmd(rt))
	return root
}

// runAccount builds the starfleet tree for rt and executes it with the
// given args, returning any error. out captures cobra's own output;
// the rt buffers still capture command output written via rt.Stdout /
// rt.Output.
func runAccount(t *testing.T, rt *module.Runtime,
	out *bytes.Buffer, args ...string,
) error {
	t.Helper()
	cmd := newStarfleetRoot(rt)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

// runAuthedAccount runs an account-level command against the stub
// server at srvURL with dummy credentials supplied through the
// persistent flags.
func runAuthedAccount(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	return testsupport.RunAuthed(t, newStarfleetRoot(rt), out, srvURL, args...)
}
