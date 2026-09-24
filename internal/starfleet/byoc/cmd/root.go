package cmd

import (
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// NewByocCmd builds the `pgedge starfleet byoc` command tree: the
// sub-tree root and its resource subcommands.
//
// byoc declares no connection flags of its own. The starfleet root owns
// --api-url, --client-id and --client-secret for the whole product,
// and every leaf here reads them off its inherited flag set
// (connFlags in client.go).
//
// byoc has no doctor of its own either: it owns no connection. The
// Starfleet connection it borrows is diagnosed by `pgedge starfleet doctor`,
// and the install itself by `pgedge doctor`.
func NewByocCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "byoc",
		Short: "Manage pgEdge BYOC resources",
		Long: `byoc manages pgEdge BYOC clusters, databases, and
services through the pgEdge BYOC REST API.

Authenticate once with 'pgedge starfleet auth login', then use the
resource subcommands. The connection flags are inherited from
'pgedge starfleet' — see 'pgedge starfleet --help'.

Example:
  pgedge starfleet auth login
  pgedge starfleet byoc cluster list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}

	cmd.AddCommand(NewClusterCmd(rt))
	cmd.AddCommand(NewNodeCmd(rt))
	cmd.AddCommand(NewTaskCmd(rt))
	cmd.AddCommand(NewDatabaseCmd(rt))
	cmd.AddCommand(NewBackupCmd(rt))
	cmd.AddCommand(NewBackupStoreCmd(rt))
	cmd.AddCommand(NewBackupRepositoryCmd(rt))
	cmd.AddCommand(NewCloudAccountCmd(rt))
	cmd.AddCommand(NewConfigVersionCmd(rt))
	cmd.AddCommand(NewIngressCmd(rt))
	cmd.AddCommand(NewSSHKeyCmd(rt))
	return cmd
}
