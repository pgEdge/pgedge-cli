package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// backupColumns are the table headers shared by backup list and get.
// NAME is absent: the CR name is derived from database_id, kind and
// created_at, which have their own columns. It and the metadata map,
// whose keys vary by backup method, stay in -o json/yaml.
var backupColumns = []string{
	"ID", "DATABASE", "KIND", "PURPOSE", "STATUS", "CREATED", "FINISHED",
}

// NewBackupCmd builds the `pgedge starfleet managed backup` command
// group. There is no delete verb: retention belongs to the platform,
// and the CNPG Backup CR is the source of truth the API only mirrors.
func NewBackupCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup",
		Aliases: []string{"backups"},
		Short:   "Inspect, take and restore pgEdge Managed backups",
		Long: `backup inspects the backups of a managed database, takes an
on-demand one, and restores from one.

The platform backs up on its own schedule; create adds one on
demand. Use these commands to see what exists, read one backup's
detail, and recover a database from a backup.

Example:
  pgedge starfleet managed backup list --database-id <database_id>
  pgedge starfleet managed backup create --database-id <database_id> --kind hot
  pgedge starfleet managed backup restore <backup_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newBackupListCmd(rt),
		newBackupGetCmd(rt),
		newBackupCreateCmd(rt),
		newBackupRestoreCmd(rt),
	)

	return cmd
}

// --- create ---

// backupKinds are the tiers the create endpoint accepts, in display
// order. CreateBackupRequest types Kind as a bare string, but the API
// rejects any other value, so checking here gives exit 2 without a
// round trip. The values come from the list filter's enum so the two
// cannot drift.
var backupKinds = []string{string(api.Hot), string(api.Durable)}

func newBackupCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		databaseID string
		kind       string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Take an on-demand backup",
		Long: `create takes an on-demand backup of a managed database,
alongside whatever the platform's own schedule has taken.

--kind picks the tier. "hot" is the fastest to restore from.
"durable" is kept apart from the database's own storage and is
slower to restore.

The backup runs in the background: the API accepts it and returns
the new backup, already listable and gettable by its id. Poll
'backup get <id>' or 'backup list --database-id <id>' to watch it
reach a terminal state — this verb reports acceptance, not
completion.

--database-id takes a full UUID.

Example:
  pgedge starfleet managed backup create \
    --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind hot
  pgedge starfleet managed backup create \
    --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind durable`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains(backupKinds, kind) {
				return newExitError(fmt.Sprintf(
					"unknown backup kind %q (expected one of: %s)",
					kind, strings.Join(backupKinds, ", ")), ExitUsage)
			}
			rt.DryRun.Pass("backup kind %q accepted", kind)

			id, err := parseUUIDArg(databaseID, "database ID")
			if err != nil {
				return err
			}
			// Recorded so the dry-run report lists both of this verb's
			// checks: an operator reads that list to know what a clean
			// dry run actually verified.
			rt.DryRun.Pass("database ID %s well-formed", id)

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.CreateBackupWithResponse(
				context.Background(), id,
				api.CreateBackupJSONRequestBody{Kind: kind})
			if err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON202, nil)
			}
			// The 202 carries the backup, not the database. The two
			// schemas share id, name, status and created_at, so take
			// each id from the field that names it: DatabaseId for the
			// database, Id for the backup.
			if b := resp.JSON202; b != nil {
				fmt.Fprintf(rt.Stderr,
					"%s backup started on database %s "+
						"(backup id: %s, status: %s).\n",
					output.Sanitize(kind),
					output.Sanitize(b.DatabaseId),
					output.Sanitize(b.Id), output.Sanitize(b.Status))
			} else {
				fmt.Fprintf(rt.Stderr,
					"%s backup started on database %s.\n", output.Sanitize(kind), id)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&databaseID, "database-id", "",
		"Database to back up (full UUID)")
	f.StringVar(&kind, "kind", "",
		"Backup tier to take: "+strings.Join(backupKinds, " or "))
	_ = cmd.MarkFlagRequired("database-id")
	_ = cmd.MarkFlagRequired("kind")
	cli.MarkMutating(cmd)

	return cmd
}

// --- list ---

func newBackupListCmd(rt *module.Runtime) *cobra.Command {
	var (
		databaseID    string
		kind          string
		createdAfter  string
		createdBefore string
		limit         int
		offset        int
		descending    bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backups",
		Long: `list shows the backups in the active account, newest first:
the backup you restore is usually the newest, so this is the one
list verb whose --descending defaults true. Pass --descending=false
for oldest first.

Filter with --database-id (a full UUID) and
--kind, or bound the window with --created-after/--created-before
(RFC3339 timestamps). The server returns 100 rows by default, which
is also the maximum, so --limit can only narrow a page and a value
above 100 is refused. A full page prints a "Showing first N results"
hint to stderr. Reach older backups with --created-before,
--descending=false or --offset.

The table omits the backup's derived name; it, and the per-backup
metadata map, are in the -o json and -o yaml output.

Example:
  pgedge starfleet managed backup list
  pgedge starfleet managed backup list \
    --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind durable
  pgedge starfleet managed backup list --created-after 2026-08-01T00:00:00Z`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.ListBackupsParams{}
			// Before the client: a malformed timestamp is answered
			// without resolving a connection, so the caller is not
			// told their credentials are wrong when their flag is.
			if err := conn.ApplyCreatedRange(cmd.Flags(),
				&params.CreatedAfter, &params.CreatedBefore); err != nil {
				return err
			}
			// backupLimitMax is also this endpoint's declared DEFAULT,
			// so --limit here can only narrow the page.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, backupLimitMax)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			database, sendDatabase, err := cli.OptionalStringFlag(
				cmd.Flags(), "database-id",
				"pass the full UUID from `database list`, or omit the "+
					"flag to list every database's backups")
			if err != nil {
				return err
			}

			if sendDatabase {
				id, err := parseUUIDArg(database, "database ID")
				if err != nil {
					return err
				}
				params.DatabaseId = &id
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			if kind != "" {
				k := api.ListBackupsParamsKind(kind)
				params.Kind = &k
			}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			// The server sorts newest first by default; the param is
			// sent only when the flag is given, so --descending=false
			// still reaches it.
			if cmd.Flags().Changed("descending") {
				params.Descending = &descending
			}

			resp, err := client.ListBackupsWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list backups: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			backups := resp.JSON200
			if backups == nil || len(*backups) == 0 {
				fmt.Fprintln(rt.Stderr, "No backups found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*backups))
			for _, b := range *backups {
				rows = append(rows, backupRowFrom(b))
			}
			if err := rt.Output.Print(rows, backupColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(
				rt, len(*backups), limit, backupPageDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&databaseID, "database-id", "",
		"Filter by database (full UUID)")
	f.StringVar(&kind, "kind", "",
		"Filter by kind (e.g. hot, durable)")
	f.StringVar(&createdAfter, "created-after", "",
		"Only backups created at or after this RFC3339 time")
	f.StringVar(&createdBefore, "created-before", "",
		"Only backups created at or before this RFC3339 time")
	f.IntVar(&limit, "limit", 0,
		fmt.Sprintf("Maximum number of results to return (%d-%d, "+
			"and %d is also the default)",
			cli.LimitLowest, backupLimitMax, backupLimitMax))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.BoolVar(&descending, "descending", true,
		"Sort newest first (--descending=false for oldest first)")
	return cmd
}

// --- get ---

func newBackupGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <backup_id>",
		Short: "Show backup details",
		Long: `get shows the details of a single backup.

The argument takes a full UUID. Use
-o yaml to read the metadata map — its keys vary by backup method,
so the text output does not show it.

Example:
  pgedge starfleet managed backup get <backup_id>
  pgedge starfleet managed backup get b8c9d0e1-f2a3-4567-bcde-678901234567 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "backup ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			b, err := fetchBackupWith(client, id)
			if err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(b, nil)
			}
			if b == nil {
				fmt.Fprintln(rt.Stderr, "No backup data returned.")
				return nil
			}
			return rt.Output.Print(
				[]output.Row{backupRowFrom(*b)}, backupColumns)
		},
	}
}

// --- restore ---

func newBackupRestoreCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <backup_id>",
		Short: "Restore a database from a backup",
		Long: `restore recovers a managed database from one of its backups.

The database keeps its ID and connection details, but its current
data is replaced by the backup's contents — anything written since
the backup was taken is no longer in the database. That is
destructive, so restore prompts for confirmation unless --force is
given.

A hot backup of the current state is taken first, so a restore run
by mistake can be undone by restoring that backup. It appears in
"backup list" within about a minute, looking like any other hot
backup, so identify it by when it was created. No retention is
published for it.

The API accepts the restore and recovers in the background; the
database reports status "modifying" until the cutover completes.
Pass --wait to block until the restore's task reaches a terminal
state. The argument takes a full UUID,
and the backup must be in the completed state — the API refuses
any other restore point upfront.

Example:
  pgedge starfleet managed backup restore <backup_id>
  pgedge starfleet managed backup restore <backup_id> --force --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the prompt: a parse is free, so a malformed ID is
			// refused rather than confirmed and then refused.
			id, err := parseUUIDArg(args[0], "backup ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Restore backup %s? The database's current data will "+
					"be replaced by the backup's contents.", id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// The backup names the database, which is what --wait
			// polls, so read it before the mutation; that also captures
			// the prior task before the restore's own task exists.
			b, err := fetchBackupWith(client, id)
			if err != nil {
				return err
			}
			if b == nil {
				return newExitError(fmt.Sprintf(
					"backup %q not found", args[0]), ExitNotFound)
			}

			// Restore is keyed on the database, with the backup in the
			// body, and both ids come from the fetched backup: the
			// database id is not in the argument at all.
			//
			// The parses check values the server returned. The path
			// needs a uuid.UUID; backup_id is a bare string in the
			// contract, so its parse buys a clean local exit instead of
			// a server-side error. b.Id is sent as received, so the
			// wire and the messages below spell it the same way.
			//
			// The API checks that the backup belongs to the database in
			// the path, so transposing the two is a 404 rather than a
			// restore of the wrong database.
			dbID, err := uuid.Parse(b.DatabaseId)
			if err != nil {
				return newExitError(fmt.Sprintf(
					"backup %s names an invalid database ID %q: %v",
					b.Id, b.DatabaseId, err), ExitGeneral)
			}
			if _, err := uuid.Parse(b.Id); err != nil {
				return newExitError(fmt.Sprintf(
					"invalid backup ID %q: %v", b.Id, err), ExitGeneral)
			}
			base := captureTaskBaseline(client, b.DatabaseId)

			resp, err := client.RestoreManagedDatabaseWithResponse(
				context.Background(), dbID,
				api.RestoreManagedDatabaseJSONRequestBody{
					BackupId: b.Id,
				})
			if err != nil {
				return fmt.Errorf("restore backup: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(resp.JSON202, nil); err != nil {
					return err
				}
			} else if d := resp.JSON202; d != nil {
				fmt.Fprintf(rt.Stderr,
					"Restore of backup %s started on database %q "+
						"(id: %s, status: %s).\n",
					output.Sanitize(b.Id), d.Name, output.Sanitize(d.Id), output.Sanitize(d.Status))
			} else {
				fmt.Fprintf(rt.Stderr,
					"Restore of backup %s started on database %s.\n",
					output.Sanitize(b.Id), output.Sanitize(b.DatabaseId))
			}
			return trackMutation(rt, client, b.DatabaseId, base)
		},
	}
	addWaitFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- helpers ---

// parseTimeFlag parses an RFC3339 flag value, turning a bad format
// into a usage error that names the flag and the expected shape.
func parseTimeFlag(flag, value string) (time.Time, error) {
	return cli.ParseTimeFlag(flag, value)
}

// fetchBackupWith retrieves a Backup by its UUID, shared by get and
// restore.
func fetchBackupWith(
	client *api.ClientWithResponses, id uuid.UUID,
) (*api.Backup, error) {
	resp, err := client.GetBackupWithResponse(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("get backup: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	return resp.JSON200, nil
}

// --- row adapter ---

type backupRow struct {
	id, database, kind, purpose, status, created, finished string
}

func (r backupRow) Columns() []string {
	return []string{
		r.id,
		r.database,
		r.kind,
		r.purpose,
		output.ColorStatus(r.status),
		r.created,
		r.finished,
	}
}

func backupRowFrom(b api.Backup) backupRow {
	finished := ""
	if b.FinishedAt != nil {
		finished = output.FormatDate(*b.FinishedAt)
	}
	// purpose is an open set: an unrecognized value renders as-is. It
	// is absent when provenance is unknown, and renders blank as an
	// absent FinishedAt does.
	purpose := ""
	if b.Purpose != nil {
		purpose = *b.Purpose
	}
	return backupRow{
		id:       b.Id,
		database: b.DatabaseId,
		kind:     b.Kind,
		purpose:  purpose,
		status:   b.Status,
		created:  output.FormatDate(b.CreatedAt),
		finished: finished,
	}
}
