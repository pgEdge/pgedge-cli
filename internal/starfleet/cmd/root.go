// Package cmd wires the pgedge starfleet command tree: the starfleet root,
// its persistent connection flags, the account-level commands (auth,
// doctor, tenant, client, invite, membership), and the byoc and
// managed sub-trees.
package cmd

import (
	"github.com/pgEdge/pgedge-cli/internal/module"
	accountcmd "github.com/pgEdge/pgedge-cli/internal/starfleet/account/cmd"
	byoccmd "github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/cmd"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	managedcmd "github.com/pgEdge/pgedge-cli/internal/starfleet/managed/cmd"
	"github.com/spf13/cobra"
)

// NewStarfleetCmd builds the `pgedge starfleet` command tree.
//
// The four connection flags are declared here and nowhere else: one
// product means one connection, and every byoc and managed leaf reads
// them off its inherited flag set (connFlags in each sub-tree's
// client.go). --timeout bounds one HTTP exchange, as controlplane's
// does; the wait bound is the per-leaf --wait-timeout.
func NewStarfleetCmd(rt *module.Runtime) *cobra.Command {
	f := &conn.Flags{}

	cmd := &cobra.Command{
		Use:   "starfleet",
		Short: "Manage pgEdge Starfleet",
		Long: `starfleet manages pgEdge Starfleet: authentication, account
resources (API clients, tenants, invites, memberships) and the two
infrastructure sub-trees — byoc for BYOC clusters and managed for
Managed databases.

Authenticate once with 'pgedge starfleet auth login'. Every starfleet
subcommand shares that one connection, taken from the active profile
in ~/.pgedge/cli/config.yaml and overridable per invocation with
--client-id, --client-secret, and --api-url.

Example:
  pgedge starfleet auth login
  pgedge starfleet byoc cluster list
  pgedge starfleet managed database list`,

		// Do not remove these two lines; the pure-router gates hold
		// them as the standard for every group command. In cobra
		// v1.10.2:
		//
		//  1. IsAvailableCommand, and the help template's usage/flags
		//     block, require a runnable command or available children,
		//     so a childless group without RunE vanishes from
		//     `pgedge --help` and prints no flags.
		//  2. execute() returns flag.ErrHelp for a non-runnable command
		//     before ValidateArgs, so without RunE cobra.NoArgs is dead:
		//     `pgedge starfleet <stray>` prints help and exits 0 instead
		//     of failing with exit 2.
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringVar(&f.APIURL, "api-url", "",
		"pgEdge Starfleet API base URL (default https://api.pgedge.com)")
	pf.StringVar(&f.ClientID, "client-id", "",
		"API client ID (overrides profile config)")
	pf.StringVar(&f.ClientSecret, "client-secret", "",
		"API client secret (overrides profile config)")
	pf.DurationVar(&f.Timeout, "timeout", conn.RequestTimeout,
		"Per-request timeout (Go duration; 0 disables)")

	cmd.AddCommand(accountcmd.NewAuthCmd(rt, f))
	cmd.AddCommand(accountcmd.NewDoctorCmd(rt, f))
	cmd.AddCommand(accountcmd.NewInviteCmd(rt))
	cmd.AddCommand(accountcmd.NewMembershipCmd(rt))
	cmd.AddCommand(accountcmd.NewAPIClientCmd(rt))
	cmd.AddCommand(accountcmd.NewTenantCmd(rt))
	cmd.AddCommand(newAPICmd(rt, f))

	// No `starfleet user` verbs, deliberately, although the generated
	// client carries GetCurrentUser/UpdateCurrentUser. The API answers
	// 401 when EITHER the tenant or the user is missing, and a
	// client-credentials token carries a tenant but never a user.
	// Revisit when a user-token auth flow exists.

	cmd.AddCommand(byoccmd.NewByocCmd(rt))
	cmd.AddCommand(managedcmd.NewManagedCmd(rt))

	return cmd
}
