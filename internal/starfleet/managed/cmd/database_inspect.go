package cmd

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/inspect"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// inspectDeps is the seam the tests use to run an analysis without a
// Postgres; nil connects with the real driver.
var inspectDeps *cli.InspectDeps

func newDatabaseInspectCmd(rt *module.Runtime) *cobra.Command {
	var userType string
	cmd := &cobra.Command{
		Use:   "inspect [<database_id>] <analysis>",
		Short: "Run a read-only diagnostic against a managed database",
		Long: `inspect connects to a managed database with the credentials
database get returns and runs one diagnostic query, printing the rows.
Every analysis reads catalog and statistics views only; nothing is
written, and no API state changes.

The analyses are those of 'pgedge inspect', which lists each one:
table-sizes, index-sizes, unused-indexes, seq-scans,
long-running-queries, locks, vacuum-stats, bloat, calls, outliers,
replication-slots, replication-lag and subscriptions. calls and
outliers need the pg_stat_statements extension and are refused with
exit 1 naming it when the database does not have it.

--user-type chooses the role to connect as, admin, app or app_read_only, exactly as
database get does. Omitted, app is used, except for
long-running-queries, locks, calls, outliers and replication-lag,
which connect as admin: Postgres nulls other sessions' rows in the
statistics views for a role without pg_read_all_stats, so as app the
first two would answer empty and read as a quiet database, the next
two would show "<insufficient privilege>" in place of every query, and
replication-lag would return a row per replica with every column
blank but application. A database that refuses the connection is exit
1; one that accepts it and never answers is exit 3 after 30 seconds.
The database ID takes a full UUID. In a folder linked with 'database link', the ID can be left out.

Example:
  pgedge starfleet managed database inspect e5f6a7b8-c9d0-1234-efab-567890123456 table-sizes
  pgedge starfleet managed database inspect e5f6a7b8-c9d0-1234-efab-567890123456 \
    long-running-queries --user-type admin -o json`,
		Args: cobra.RangeArgs(1, 2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) (
			[]string, cobra.ShellCompDirective,
		) {
			if len(args) == 0 {
				return inspect.Names(), cobra.ShellCompDirectiveNoFileComp
			}
			if _, isAnalysis := inspect.Lookup(args[0]); len(args) == 1 && !isAnalysis {
				return inspect.Names(), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// A typed ID, then the analysis, then the link: each error
			// names what was typed before the link is consulted.
			var id uuid.UUID
			var err error
			if len(args) == 2 {
				if id, err = parseUUIDArg(args[0], "database ID"); err != nil {
					return err
				}
			}
			analysis := args[len(args)-1]
			a, ok := inspect.Lookup(analysis)
			if !ok {
				return newExitError(fmt.Sprintf(
					"unknown analysis %q: one of %s", analysis,
					strings.Join(inspect.Names(), ", ")), ExitUsage)
			}
			if len(args) == 1 {
				if id, _, err = databaseArg(rt, nil, 0); err != nil {
					return err
				}
			}
			var wireUserType api.GetManagedDatabaseParamsUserType
			if a.NeedsStats && !cmd.Flags().Changed("user-type") {
				// app cannot see other sessions' rows, so these
				// would read as a quiet database or as blank
				// columns rather than as a missing privilege.
				wireUserType = api.GetManagedDatabaseParamsUserTypeAdmin
			}
			if cmd.Flags().Changed("user-type") {
				if userType == "" {
					return newExitError(
						"--user-type given an empty value: name a role "+
							"(admin, app or app_read_only), or omit the flag for the "+
							"default", ExitUsage)
				}
				var err error
				wireUserType, err = parseUserType(userType)
				if err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			params := &api.GetManagedDatabaseParams{}
			if wireUserType != "" {
				params.UserType = &wireUserType
			}
			resp, err := client.GetManagedDatabaseWithResponse(
				context.Background(), id, params)
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
			cs, err := buildConnectionString(resp.JSON200, true)
			if err != nil {
				return err
			}
			// Returned as is: cli.ExitCode maps a plain error to 1 and
			// a usage error to 2, and rebuilding it here would flatten
			// the second.
			return cli.RunInspect(cmd.Context(), rt, inspectDeps, a, cs.URI)
		},
	}
	cmd.Flags().StringVar(&userType, "user-type", "",
		"Role to connect as: admin, app or app_read_only (default app; admin for the statistics analyses)")
	return cmd
}
