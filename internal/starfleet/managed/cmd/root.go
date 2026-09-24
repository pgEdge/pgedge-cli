package cmd

import (
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// NewManagedCmd builds the `pgedge starfleet managed` command tree: the
// sub-tree root and its resource subcommands.
//
// managed declares no connection flags of its own. The starfleet root
// owns the connection flags (--api-url, --client-id, --client-secret
// and the per-request --timeout) for the whole product, and every
// leaf here reads them off its inherited flag set (connFlags in
// client.go).
//
// managed has no doctor of its own either: it owns no connection. The
// Starfleet connection it borrows is diagnosed by `pgedge starfleet doctor`,
// and the install itself by `pgedge doctor`.
func NewManagedCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "managed",
		Short: "Manage pgEdge Managed databases",
		Long: `managed manages pgEdge Managed databases: single-master
Postgres databases hosted and operated by pgEdge.

Authenticate once with 'pgedge starfleet auth login', then use the
resource subcommands. The connection flags are inherited from
'pgedge starfleet' — see 'pgedge starfleet --help'.

Managed databases run on infrastructure pgEdge operates, so there
are no clusters or nodes to place them on. For databases in your
own cloud account, use 'pgedge starfleet byoc'.

Example:
  pgedge starfleet auth login
  pgedge starfleet managed database list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}

	cmd.AddCommand(
		NewBackupCmd(rt),
		NewClientIPCmd(rt),
		NewDatabaseCmd(rt),
		NewPgVersionCmd(rt),
		NewRegionCmd(rt),
		NewSizeCmd(rt),
		NewTaskCmd(rt),
	)
	return cmd
}
