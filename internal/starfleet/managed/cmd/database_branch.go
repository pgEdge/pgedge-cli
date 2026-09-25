package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// branchLimitMax is `limit`'s maximum on
// /managed/v1/databases/{id}/branches, which equals the declared
// default -- so --limit here can only narrow the page, the same shape
// as backupLimitMax.
const branchLimitMax = 100

// branchPageDefaults mirrors backupPageDefaults: Def and Cap both
// equal branchLimitMax because the CLI already refuses a --limit above
// it locally, so the cap is reachable only by omitting the flag.
var branchPageDefaults = cli.PageDefaults{
	Def: branchLimitMax, Cap: branchLimitMax,
}

// branchColumns are the table headers shared by branch list and get.
var branchColumns = []string{
	"ID", "DATABASE", "NAME", "STATUS", "REGION", "SIZE", "DEPTH", "CREATED",
}

// NewDatabaseBranchCmd builds `pgedge starfleet managed database
// branch`, a copy-on-write branch of a managed database.
func NewDatabaseBranchCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"branches"},
		Short:   "Manage branches of a managed database",
		Long: `branch manages copy-on-write branches of a managed
database: a full copy of the source's size, Postgres version, options
and service configuration at the moment it is taken. Nothing
propagates afterwards in either direction.

A branch bills from the moment it becomes connectable until its
deletion is requested. Deleting a branch is unrecoverable.

Example:
  pgedge starfleet managed database branch list <database_id>
  pgedge starfleet managed database branch create <database_id> \
    --display-name dev-copy
  pgedge starfleet managed database branch get <database_id> <branch_id>
  pgedge starfleet managed database branch metrics <database_id> <branch_id>
  pgedge starfleet managed database branch logs <database_id> <branch_id>
  pgedge starfleet managed database branch delete <database_id> <branch_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseBranchListCmd(rt),
		newDatabaseBranchGetCmd(rt),
		newDatabaseBranchCreateCmd(rt),
		newDatabaseBranchDeleteCmd(rt),
		newDatabaseBranchMetricsCmd(rt),
		newDatabaseBranchLogsCmd(rt),
	)
	return cmd
}

// --- list ---

func newDatabaseBranchListCmd(rt *module.Runtime) *cobra.Command {
	var (
		limit          int
		offset         int
		descending     bool
		includeDeleted bool
	)
	cmd := &cobra.Command{
		Use:   "list [<database_id>]",
		Short: "List a database's branches",
		Long: `list shows the branches taken from a managed database,
oldest first; --descending lists the newest first. Pass
--include-deleted to also see branches that have been torn down, the
audit view.

The server returns 100 rows by default, which is also the maximum,
so --limit can only narrow a page and a value above 100 is refused.
The argument takes a full UUID. In a folder linked with 'database link', the ID can be left out.

Example:
  pgedge starfleet managed database branch list e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database branch list e5f6a7b8-c9d0-1234-efab-567890123456 \
    --include-deleted`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, branchLimitMax)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListBranchesParams{}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if cmd.Flags().Changed("descending") {
				params.Descending = &descending
			}
			if cmd.Flags().Changed("include-deleted") {
				params.IncludeDeleted = &includeDeleted
			}

			resp, err := client.ListBranchesWithResponse(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("list branches: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			branches := resp.JSON200
			if branches == nil || len(*branches) == 0 {
				fmt.Fprintln(rt.Stderr, "No branches found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*branches))
			for _, b := range *branches {
				rows = append(rows, branchRowFrom(b))
			}
			if err := rt.Output.Print(rows, branchColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(
				rt, len(*branches), limit, branchPageDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0,
		fmt.Sprintf("Maximum number of results to return (%d-%d, "+
			"and %d is also the default)",
			cli.LimitLowest, branchLimitMax, branchLimitMax))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.BoolVar(&descending, "descending", false, "List the newest branches first")
	f.BoolVar(&includeDeleted, "include-deleted", false,
		"Also show deleted branches")
	return cmd
}

// --- get ---

func newDatabaseBranchGetCmd(rt *module.Runtime) *cobra.Command {
	var userType string
	cmd := &cobra.Command{
		Use:   "get <database_id> <branch_id>",
		Short: "Show branch details",
		Long: `get shows the details of a single branch.

Pass --user-type to choose which role's credentials come back in the
connection block: admin, app or app_read_only. Omit it and app's come
back. The connection is present only while the branch is
connectable. Both arguments take a full UUID.

Example:
  pgedge starfleet managed database branch get <database_id> <branch_id>
  pgedge starfleet managed database branch get <database_id> <branch_id> \
    --user-type admin -o yaml`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var wireUserType api.GetBranchParamsUserType
			if cmd.Flags().Changed("user-type") {
				if userType == "" {
					return newExitError(
						"--user-type given an empty value: name a role "+
							"(admin, app or app_read_only), or omit the "+
							"flag to use app", ExitUsage)
				}
				var err error
				wireUserType, err = parseBranchUserType(userType)
				if err != nil {
					return err
				}
			}

			databaseID, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			branchID, err := parseUUIDArg(args[1], "branch ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.GetBranchParams{}
			if wireUserType != "" {
				params.UserType = &wireUserType
			}

			resp, err := client.GetBranchWithResponse(
				context.Background(), databaseID, branchID, params)
			if err != nil {
				return fmt.Errorf("get branch: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			b := resp.JSON200
			if b == nil {
				fmt.Fprintln(rt.Stderr, "No branch data returned.")
				return nil
			}
			if rt.Output.Structured() {
				return rt.Output.Print(b, nil)
			}
			return rt.Output.Print(
				[]output.Row{branchRowFrom(*b)}, branchColumns)
		},
	}
	cmd.Flags().StringVar(&userType, "user-type", "",
		"Role whose credentials to return: admin, app or "+
			"app_read_only (default app)")
	return cmd
}

// branchUserTypeAliases is userTypeAliases for GetBranch, whose
// user_type is a separate generated type with the same values.
var branchUserTypeAliases = map[string]api.GetBranchParamsUserType{
	"admin":                 api.GetBranchParamsUserTypeAdmin,
	"app":                   api.GetBranchParamsUserTypeApplication,
	"application":           api.GetBranchParamsUserTypeApplication,
	"app_read_only":         api.GetBranchParamsUserTypeApplicationReadOnly,
	"application_read_only": api.GetBranchParamsUserTypeApplicationReadOnly,
}

func parseBranchUserType(s string) (api.GetBranchParamsUserType, error) {
	if u, ok := branchUserTypeAliases[s]; ok {
		return u, nil
	}
	return "", unknownUserTypeError(s)
}

// --- create ---

func newDatabaseBranchCreateCmd(rt *module.Runtime) *cobra.Command {
	var displayName string
	cmd := &cobra.Command{
		Use:   "create <database_id>",
		Short: "Create a branch of a managed database",
		Long: `create takes a copy-on-write branch of a
managed database: its size, Postgres version, options and service
configuration at this moment, with nothing propagating afterwards in
either direction.

The branch's Postgres allowlist copies the source's current rules.
The argument takes a full UUID.

Example:
  pgedge starfleet managed database branch create e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database branch create e5f6a7b8-c9d0-1234-efab-567890123456 \
    --display-name dev-copy`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("display-name") {
				if err := conn.ValidateDisplayName(displayName); err != nil {
					return err
				}
			}

			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateBranchJSONRequestBody{}
			if cmd.Flags().Changed("display-name") {
				body.DisplayName = &displayName
			}

			resp, err := client.CreateBranchWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("create branch: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			b := resp.JSON201
			if b == nil {
				fmt.Fprintln(rt.Stderr,
					"Branch created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(b, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Branch %s created on database %s (status: %s).\n",
					output.Sanitize(b.Id), output.Sanitize(b.DatabaseId),
					output.Sanitize(b.Status))
			}
			// A brand-new branch has no prior tasks, so the first task
			// seen for it is the one this create spawned.
			return trackMutation(rt, client, b.Id, taskBaseline{})
		},
	}
	addWaitFlags(cmd)
	cmd.Flags().StringVar(&displayName, "display-name", "",
		fmt.Sprintf("Display name for the branch, at most %d "+
			"characters", conn.DisplayNameMaxLen))
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newDatabaseBranchDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <database_id> <branch_id>",
		Short: "Delete a branch",
		Long: `delete tears down a branch. Billing stops and the
branch's slot frees at the request, ahead of teardown completing.

Deletion is destructive, so it prompts for confirmation unless
--force is given. Both arguments take a full UUID.

Example:
  pgedge starfleet managed database branch delete <database_id> <branch_id>
  pgedge starfleet managed database branch delete <database_id> <branch_id> \
    --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			databaseID, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			branchID, err := parseUUIDArg(args[1], "branch ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete branch %s? This cannot be undone.", branchID)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			base := captureTaskBaseline(client, branchID.String())

			// Untyped call: delete has no typed 2xx case. See
			// checkEmptyBodyResponse.
			resp, err := client.DeleteBranch(
				context.Background(), databaseID, branchID)
			if err != nil {
				return fmt.Errorf("delete branch: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete branch"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Branch %s deleted.\n", branchID)
			return trackMutation(rt, client, branchID.String(), base)
		},
	}
	addWaitFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type branchRow struct {
	id, database, name, status, region, size, depth, created string
}

func (r branchRow) Columns() []string {
	return []string{
		r.id,
		r.database,
		r.name,
		output.ColorStatus(r.status),
		r.region,
		r.size,
		r.depth,
		r.created,
	}
}

func branchRowFrom(b api.Branch) branchRow {
	return branchRow{
		id:       b.Id,
		database: b.DatabaseId,
		name:     b.Name,
		status:   b.Status,
		region:   b.Region,
		size:     b.Size,
		depth:    fmt.Sprintf("%d", b.Depth),
		created:  output.FormatTime(b.CreatedAt),
	}
}
