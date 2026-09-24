package cmd

import (
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// NewControlplaneCmd builds the `pgedge controlplane` command tree: the
// module root, its persistent connection flags, and the resource
// subcommands.
func NewControlplaneCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controlplane",
		Short: "Manage a pgEdge Control Plane",
		Long: `controlplane manages a self-hosted pgEdge Control Plane through its
declarative HTTP API: clusters, hosts, and databases.

The Control Plane has no login. Point controlplane at a running control-plane
with --base-url (default http://localhost:3000). If the server has
mTLS enabled, supply --ca-cert, --client-cert, and --client-key.
These come from the active profile in ~/.pgedge/cli/config.yaml and can
be overridden per invocation.

Example:
  pgedge controlplane config set --base-url http://localhost:3000
  pgedge controlplane version
  pgedge controlplane database list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringArray("base-url", nil,
		"Control Plane base URL; repeat for HA failover "+
			"(default http://localhost:3000)")
	pf.String("ca-cert", "", "Path to the CA certificate (mTLS)")
	pf.String("client-cert", "", "Path to the client certificate (mTLS)")
	pf.String("client-key", "", "Path to the client key (mTLS)")
	pf.Bool("insecure", false,
		"Skip TLS certificate verification (dev only)")
	pf.Duration("timeout", 30*time.Second,
		"Per-request timeout (Go duration; 0 disables)")

	cmd.AddCommand(newConfigCmd(rt))
	cmd.AddCommand(newDoctorCmd(rt))
	cmd.AddCommand(newVersionCmd(rt))
	cmd.AddCommand(NewClusterCmd(rt))
	cmd.AddCommand(NewHostCmd(rt))
	cmd.AddCommand(newAPICmd(rt))
	cmd.AddCommand(NewDatabaseCmd(rt))
	cmd.AddCommand(NewTaskCmd(rt))
	return cmd
}
