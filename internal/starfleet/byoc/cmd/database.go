package cmd

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// byocDatabaseNameMaxLen is the byoc API's ceiling for a database
// name, in bytes as the API counts it. A byoc name is only a Postgres
// identifier, so it gets Postgres's 63, not managed's 50, which leaves
// headroom for Kubernetes child resource names.
const byocDatabaseNameMaxLen = 63

// validateByocDatabaseName rejects a --name the byoc API would refuse,
// with ExitUsage, before any API call.
//
// It mirrors the API's enforced rule, not the documented one. The API
// validates strings.ToLower(strings.TrimSpace(name)), so `--name MyDB`
// creates `mydb`; enforcing the documented "lowercase" literally would
// reject invocations that work. The name is normalised for the check
// only, and the wire carries what the user typed.
//
// Unlike validateManagedDatabaseName, which narrows to the documented
// managed contract, this does not narrow: the documented and enforced
// byoc rules agree except on case, and a pre-check that refuses what
// the server accepts becomes a second, competing API. For the same
// reason it uses unicode.IsLetter and unicode.IsDigit, as the API does,
// so `café` is legal.
//
// ToLower changes no rune's class; it matters only to the byte-length
// check. Two runes grow under Go's ToLower (U+023A and U+023E, two
// bytes to three), so a name can fit as typed and not as stored, and
// the API measures the stored form. The "over the limit only once
// lowercased" case in TestValidateByocDatabaseName pins it.
//
// Messages quote the name as typed, never the normalised form.
func validateByocDatabaseName(name string) error {
	normalized := strings.ToLower(strings.TrimSpace(name))

	// Checked locally because the API handles it badly: it rejects an
	// empty name with 400 "name required", but a whitespace-only name
	// passes that guard and trims to empty inside the server, where the
	// name check indexes the first rune of an empty name.
	if normalized == "" {
		return newExitError(
			"database name is required and cannot be blank", ExitUsage)
	}
	if len(normalized) > byocDatabaseNameMaxLen {
		// Without "once lowercased", a user who counted 62 bytes is
		// told they wrote 93.
		return newExitError(fmt.Sprintf(
			"database name %q is %d bytes once lowercased, over the "+
				"%d-byte limit", name, len(normalized),
			byocDatabaseNameMaxLen), ExitUsage)
	}

	runes := []rune(normalized)
	if !unicode.IsLetter(runes[0]) && runes[0] != '_' {
		return newExitError(fmt.Sprintf(
			"database name %q is invalid: must start with a letter or "+
				"an underscore", name), ExitUsage)
	}
	for _, ch := range runes {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			continue
		}
		// Cluster, node and backup-store names in this module take
		// hyphens, so a hyphen here is the natural mistake.
		hint := ""
		if ch == '-' {
			hint = " (database names take underscores, not hyphens — " +
				"unlike cluster and node names)"
		}
		return newExitError(fmt.Sprintf(
			"database name %q is invalid: %q is not allowed; use "+
				"letters, digits and underscores only%s",
			name, string(ch), hint), ExitUsage)
	}
	return nil
}

// databaseGetColumns are the table headers for database get.
var databaseGetColumns = []string{
	"ID", "NAME", "STATUS", "PG VERSION", "CLUSTER", "CREATED",
}

// databaseListColumns omit PG VERSION because the list endpoint does
// not send it: measured on eight databases across three tenants, get
// answered `"pg_version": "18"` and every list row omitted the key. A
// blank cell cannot say whether it means unknown, unset or not sent,
// so the column is dropped from list (decided 2026-08-21).
//
// TestDatabaseRowMatchesItsColumnSet keeps cells and headers in step;
// a row with one more cell than its headers renders an unlabelled
// column rather than failing.
var databaseListColumns = []string{
	"ID", "NAME", "STATUS", "CLUSTER", "CREATED",
}

// NewDatabaseCmd builds the `pgedge starfleet byoc database` command
// group.
func NewDatabaseCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"databases"},
		Short:   "Manage pgEdge BYOC databases",
		Long: `database manages the pgEdge BYOC databases that run on a
cluster: the Postgres databases your applications connect to.

Use these commands to list, inspect, create, update, and delete
databases, and to manage the services (MCP, RAG) deployed alongside
them.

Example:
  pgedge starfleet byoc database list
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseListCmd(rt),
		newDatabaseGetCmd(rt),
		newDatabaseConnectionStringCmd(rt),
		newDatabaseInspectCmd(rt),
		newDatabaseCreateCmd(rt),
		newDatabaseUpdateCmd(rt),
		newDatabaseDeleteCmd(rt),
		newDatabaseRotatePasswordCmd(rt),
		newDatabaseRestoreCmd(rt),
		newDatabaseLogsCmd(rt),
		newDatabaseMetricsCmd(rt),
		NewDatabaseServiceCmd(rt),
		NewDatabaseMCPCmd(rt),
		NewDatabasePostgRESTCmd(rt),
		NewDatabaseRAGCmd(rt),
	)
	return cmd
}

// --- list ---

func newDatabaseListCmd(rt *module.Runtime) *cobra.Command {
	var (
		clusterID string
		limit     int
		offset    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List databases",
		Long: `list shows the databases in the active account.

Use it to find a database's ID before running get, update, or
delete. Filter with --cluster-id, and page through large accounts
with --limit and --offset.

Example:
  pgedge starfleet byoc database list
  pgedge starfleet byoc database list --cluster-id <cluster_id> -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client, so a bad value exits 2, not 5 for
			// credentials it never needed. NoUpperBound because byoc.yaml
			// declares no paging bounds: the server clamps at 100 today,
			// but a measured clamp is not a published contract.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			cluster, sendCluster, err := cli.OptionalStringFlag(
				cmd.Flags(), "cluster-id",
				"pass the full UUID from `cluster list`, or omit the "+
					"flag to list databases in every cluster")
			if err != nil {
				return err
			}
			// The parse belongs before the client too, for the same
			// exit-code reason.
			var clusterUUID uuid.UUID
			if sendCluster {
				clusterUUID, err = parseUUIDArg(cluster, "cluster ID")
				if err != nil {
					return err
				}
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListDatabasesParams{}
			if sendCluster {
				params.ClusterId = &clusterUUID
			}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}

			resp, err := client.ListDatabasesWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list databases: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			databases := resp.JSON200
			if databases == nil || len(*databases) == 0 {
				fmt.Fprintln(rt.Stderr, "No databases found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*databases))
			for _, d := range *databases {
				rows = append(rows, databaseListRowFrom(d))
			}
			if err := rt.Output.Print(
				rows, databaseListColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*databases), limit,
				databaseDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&clusterID, "cluster-id", "",
		"Filter by cluster (full UUID)")
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(databaseDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	return cmd
}

// --- get ---

func newDatabaseGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <database_id>",
		Short: "Show database details",
		Long: `get shows the details of a single database.

Use it to check a database's status, Postgres version, and
cluster. The argument takes a full UUID.

Example:
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
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

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			d := resp.JSON200
			if d == nil {
				fmt.Fprintln(rt.Stderr, "No database data returned.")
				return nil
			}
			rows := []output.Row{databaseRowFrom(*d)}
			return rt.Output.Print(rows, databaseGetColumns)
		},
	}
}

// --- create ---

func newDatabaseCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name      string
		clusterID string
		pgVersion string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a database",
		Long: `create provisions a new database on an existing cluster.

Use it to stand up a Postgres database applications can connect
to. Pass --wait to block until provisioning finishes.

Example:
  pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id>
  pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id> \
    --pg-version 16 --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateByocDatabaseName(name); err != nil {
				return err
			}
			// The spec declares pg_version as a bare string, so emptiness
			// is the only local check. It is worth making: the version is
			// fixed at create, and `--pg-version "$PGV"` with the
			// variable unset would otherwise take the API's default
			// silently.
			if cmd.Flags().Changed("pg-version") && pgVersion == "" {
				return newExitError(
					"--pg-version given an empty value: name a version "+
						"(e.g. 16), or omit the flag to use the API "+
						"default", ExitUsage)
			}
			// Recorded so a dry run does not report "no client-side
			// checks" for this verb.
			rt.DryRun.Pass("database name %q accepted", name)

			// The contract declares cluster_id as a bare string, and the
			// API answers an ID prefix with "cluster not found or not
			// available", which reads like a busy cluster, not a short
			// ID.
			cluster, err := parseUUIDArg(clusterID, "cluster ID")
			if err != nil {
				return err
			}
			clusterID = cluster.String()
			rt.DryRun.Pass("cluster ID %s well-formed", clusterID)

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateDatabaseJSONRequestBody{
				Name:      name,
				ClusterId: &clusterID,
			}
			if cmd.Flags().Changed("pg-version") {
				body.PgVersion = &pgVersion
			}

			resp, err := client.CreateDatabaseWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			d := resp.JSON200
			if d == nil {
				// No body means no id, so nothing to track.
				fmt.Fprintln(rt.Stderr,
					"Database created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(d, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Database %q created (id: %s, status: %s).\n",
					d.Name, output.Sanitize(d.Id), output.Sanitize(d.Status))
			}
			return trackMutation(rt, client, d.Id, "")
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Database name")
	f.StringVar(&clusterID, "cluster-id", "",
		"Cluster to deploy the database on (full UUID)")
	f.StringVar(&pgVersion, "pg-version", "",
		"Postgres version (e.g. 16)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cluster-id")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newDatabaseUpdateCmd(rt *module.Runtime) *cobra.Command {
	var (
		displayName string
		options     []string
	)
	cmd := &cobra.Command{
		Use:   "update <database_id>",
		Short: "Update a database",
		Long: `update changes a database's display name or options.

Use it to rename a database or adjust its option list after
creation. The argument takes a full UUID.

Example:
  pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --display-name "My Database"
  pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --options key1,key2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the client, so a too-long name exits 2, not 5.
			if cmd.Flags().Changed("display-name") {
				if err := conn.ValidateDisplayName(
					displayName); err != nil {
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

			body := api.UpdateDatabaseJSONRequestBody{}
			if cmd.Flags().Changed("display-name") {
				body.DisplayName.Set(displayName)
			}
			if cmd.Flags().Changed("options") {
				body.Options = &options
			}

			resp, err := client.UpdateDatabaseWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("update database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			d := resp.JSON200
			if d == nil {
				fmt.Fprintf(rt.Stderr, "Database %s updated.\n", output.Sanitize(args[0]))
				return nil
			}
			fmt.Fprintf(rt.Stderr,
				"Database %q updated (id: %s, status: %s).\n",
				d.Name, output.Sanitize(d.Id), output.Sanitize(d.Status))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&displayName, "display-name", "",
		fmt.Sprintf("Display name for the database, at most %d "+
			"characters", conn.DisplayNameMaxLen))
	f.StringSliceVar(&options, "options", nil,
		"Comma-separated list of options")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newDatabaseDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <database_id>",
		Short: "Delete a database",
		Long: `delete tears down a database.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument takes a full UUID. Pass --wait to
block until teardown finishes.

Example:
  pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --force --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the prompt, so a scripted run without --force
			// reports the bad ID, not a prompt refusal, and
			// TestShippedExamplesAreNotMalformed can reach it.
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete database %s? This cannot be undone.", id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, id.String())
				if err != nil {
					return err
				}
			}

			resp, err := client.DeleteDatabase(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete database: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete database"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Database %s deleted.\n", id)
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type databaseRow struct {
	id, name, status, pgVersion, clusterID, created string
	// showPGVersion picks the header set. A field rather than two row
	// types, so the cell order has one definition.
	showPGVersion bool
}

func (r databaseRow) Columns() []string {
	cells := []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
	}
	if r.showPGVersion {
		cells = append(cells, r.pgVersion)
	}
	return append(cells, r.clusterID, r.created)
}

// databaseRowFrom adapts an api.Database into a table row.
func databaseRowFrom(d api.Database) databaseRow {
	return databaseRow{
		id:            d.Id,
		name:          d.Name,
		status:        d.Status,
		pgVersion:     output.DerefString(d.PgVersion),
		clusterID:     d.ClusterId,
		created:       output.FormatTime(d.CreatedAt),
		showPGVersion: true,
	}
}

// databaseListRowFrom is databaseRowFrom for list, which renders no
// PG VERSION. pgVersion is still carried, so the day list sends it
// the fix is one bool.
func databaseListRowFrom(d api.Database) databaseRow {
	r := databaseRowFrom(d)
	r.showPGVersion = false
	return r
}
