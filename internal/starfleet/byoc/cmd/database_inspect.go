package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/inspect"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
)

// inspectDeps is the seam the tests use to run an analysis without a
// Postgres; nil connects with the real driver.
var inspectDeps *cli.InspectDeps

func newDatabaseInspectCmd(rt *module.Runtime) *cobra.Command {
	var (
		node     string
		internal bool
	)
	cmd := &cobra.Command{
		Use:   "inspect <database_id> <analysis>",
		Short: "Run a read-only diagnostic against one node of a database",
		Long: `inspect connects to one node of a BYOC database with the credentials
database get returns and runs one diagnostic query, printing the rows.
Every analysis reads catalog and statistics views only; nothing is
written, and no API state changes.

The analyses are those of 'pgedge inspect', which lists each one:
table-sizes, index-sizes, unused-indexes, seq-scans,
long-running-queries, locks, vacuum-stats, bloat, calls, outliers,
replication-slots, replication-lag and subscriptions. calls and
outliers need the pg_stat_statements extension and are refused with
exit 1 naming it when the database does not have it.

The node is chosen as connection-string chooses it: with one node no
flag is needed, with several --node <name> picks one and without it
the nodes are listed at exit 2, and --internal connects over
internal_host for a client inside the cluster's network. Statistics
are per node, so the same analysis on another node reads differently.
The replication analyses are per node in the same way: a node's slots
and its connected replicas describe what that node sends, so reading
the whole database means running them once per node. The argument
takes a full UUID.

Example:
  pgedge starfleet byoc database inspect f6a7b8c9-d0e1-2345-fabc-456789012345 table-sizes
  pgedge starfleet byoc database inspect f6a7b8c9-d0e1-2345-fabc-456789012345 \
    locks --node n2 --internal -o json`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) (
			[]string, cobra.ShellCompDirective,
		) {
			if len(args) == 1 {
				return inspect.Names(), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			a, ok := inspect.Lookup(args[1])
			if !ok {
				return newExitError(fmt.Sprintf(
					"unknown analysis %q: one of %s", args[1],
					strings.Join(inspect.Names(), ", ")), ExitUsage)
			}
			if cmd.Flags().Changed("node") && node == "" {
				return newExitError("--node given an empty value: name a "+
					"node, or omit the flag on a single-node database",
					ExitUsage)
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetDatabaseWithResponse(
				context.Background(), id, &api.GetDatabaseParams{})
			if err != nil {
				return fmt.Errorf("get database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return newExitError(
					fmt.Sprintf("database %s not found", id), ExitNotFound)
			}
			n, err := pickNode(rt, resp.JSON200, node)
			if err != nil {
				return err
			}
			cs, err := buildNodeConnectionString(
				resp.JSON200.Id, n, internal, true)
			if err != nil {
				return err
			}
			// Returned as is: cli.ExitCode maps a plain error to 1 and
			// a usage error to 2, and rebuilding it here would flatten
			// the second (#309).
			return cli.RunInspect(cmd.Context(), rt, inspectDeps, a, cs.URI)
		},
	}
	cmd.Flags().StringVar(&node, "node", "",
		"Node to connect to (required with several nodes)")
	cmd.Flags().BoolVar(&internal, "internal", false,
		"Connect over the node's internal_host, from inside the cluster")
	return cmd
}
