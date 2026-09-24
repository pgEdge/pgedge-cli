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
// The four connection flags are declared here and nowhere else. One
// product means one connection, so byoc and managed no longer declare
// their own copies: every leaf reads them off its inherited flag set
// (connFlags in each sub-tree's client.go), which resolves against the
// nearest ancestor that declared them — this command. --timeout bounds
// one HTTP exchange, exactly as controlplane's flag of the same name does;
// the wait bound is the per-leaf --wait-timeout.
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

		// These two lines are PERMANENT and essential. Do not remove
		// them; they are the same idiom the `pgedge` root uses
		// (internal/cli/root.go:42-44), and the pure-router gates hold
		// them as the standard for every group command in the CLI.
		//
		// RunE earns its place twice over, both verified in the pinned
		// cobra v1.10.2:
		//
		//  1. IsAvailableCommand (command.go:1607-1621) is true only for
		//     a command that is runnable or has available subcommands,
		//     and defaultHelpTemplate gates the whole usage/flags block
		//     on the same predicate. Without RunE, a childless group is
		//     absent from `pgedge --help` and prints no flags.
		//  2. RunE is what makes Args reachable at all. execute()
		//     returns flag.ErrHelp for a non-runnable command at
		//     command.go:954-956, *before* it calls ValidateArgs at
		//     969-971. Drop RunE and cobra.NoArgs is dead code:
		//     `pgedge starfleet <stray>` stops being a usage error (exit
		//     2) and becomes a silent help dump that exits 0.
		//
		// So the pair holds even with children present — a parent with a
		// bad argument must fail, not print help and claim success.
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

	// No `cloud user` verbs, deliberately. GetCurrentUser returns
	// 401 when EITHER the tenant or the user is missing from the
	// request — not only when both are absent. A
	// client-credentials token carries a tenant but never a user,
	// so it is rejected by design, not by accident. The generated
	// client does carry GetCurrentUser/UpdateCurrentUser, so this
	// absence looks like an oversight without this note. Revisit
	// only when a user-token auth flow exists.

	cmd.AddCommand(byoccmd.NewByocCmd(rt))
	cmd.AddCommand(managedcmd.NewManagedCmd(rt))

	return cmd
}
