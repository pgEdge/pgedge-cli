package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// backupRepositoryColumns are the table headers for backup-repository
// list.
var backupRepositoryColumns = []string{
	"ID", "DATABASE ID", "TYPE", "LOCATION", "RETENTION", "CREATED",
}

// backupColumns are the table headers for backup-repository get. They
// differ from list's because get reads the pgBackRest inventory held
// inside a repository, not the repository record.
var backupColumns = []string{
	"LABEL", "TYPE", "SIZE", "DATABASE SIZE", "STARTED", "FINISHED",
}

// NewBackupRepositoryCmd builds the `pgedge starfleet byoc backup-repository`
// command group. The plural "backup-repositories" is kept as a plural
// alias (unlisted in help) so existing scripts keep working.
func NewBackupRepositoryCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup-repository",
		Aliases: []string{"backup-repositories"},
		Short:   "Inspect pgEdge BYOC backup repositories",
		Long: `backup-repository inspects the pgBackRest repositories BYOC
creates for a database.

A repository is the per-database stanza written into a backup store,
so these commands are how you confirm a database's backups are
landing and read the pgBackRest inventory for one of its nodes. Use
backup-store to manage the cloud buckets themselves.

Example:
  pgedge starfleet byoc backup-repository list --database-id <database_id>
  pgedge starfleet byoc backup-repository get <repository_id> n1`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newBackupRepositoryListCmd(rt),
		newBackupRepositoryGetCmd(rt),
	)
	return cmd
}

// --- list ---

func newBackupRepositoryListCmd(rt *module.Runtime) *cobra.Command {
	var (
		databaseID string
		repoType   string
		limit      int
		offset     int
		descending bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backup repositories",
		Long: `list shows the backup repositories on the active account.

The API returns 10 repositories per page, so raise --limit to see
past the first page. Filter with --database-id to find the
repositories for one database, and use the ID it reports as the
first argument to get.

Example:
  pgedge starfleet byoc backup-repository list --limit 100
  pgedge starfleet byoc backup-repository list --database-id <database_id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client, so a bad --limit exits 2 rather than 5.
			// NoUpperBound: byoc.yaml declares no paging bounds, and the
			// server's clamp at 100 is measured, not published, so the
			// CLI must not refuse a value the API would accept.
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
			// Changed, not != "": `--database-id "$D"` with D unset
			// must exit 2, not silently widen the read to every
			// database, as byoc's reference promises.
			databaseID, sendDatabase, err := cli.OptionalStringFlag(
				cmd.Flags(), "database-id",
				"name a database, or omit the flag to list across all "+
					"of them")
			if err != nil {
				return err
			}
			params := &api.ListBackupRepositoriesParams{}
			if sendDatabase {
				id, err := parseUUIDArg(databaseID, "database ID")
				if err != nil {
					return err
				}
				params.DatabaseId = &id
			}
			if repoType != "" {
				params.Type = &repoType
			}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if descending {
				params.Descending = &descending
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListBackupRepositoriesWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list backup repositories: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			repos := resp.JSON200
			if repos == nil || len(*repos) == 0 {
				fmt.Fprintln(rt.Stderr, "No backup repositories found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*repos))
			for _, r := range *repos {
				rows = append(rows, backupRepositoryRowFrom(r))
			}
			if err := rt.Output.Print(rows, backupRepositoryColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*repos), limit,
				backupRepositoryDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&databaseID, "database-id", "",
		"Filter to one database's repositories (full UUID)")
	f.StringVar(&repoType, "type", "",
		"Filter by repository type (for example s3)")
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(backupRepositoryDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.BoolVar(&descending, "descending", false,
		"Sort in descending order")
	return cmd
}

// --- get ---

func newBackupRepositoryGetCmd(rt *module.Runtime) *cobra.Command {
	var (
		backupType string
		limit      int
		offset     int
		descending bool
	)
	cmd := &cobra.Command{
		Use:   "get <repository_id> <node_name>",
		Short: "Show a backup repository's backups",
		Long: `get reads the pgBackRest inventory a repository holds for
one node.

Use it to confirm a database's backups are landing and to read their
labels, types, and sizes. The first argument is the repository UUID
from list; the second is the node name, such as n1.

This endpoint contacts pgBackRest in the backup store itself, so it
reports an error rather than an empty result when the store is
unreachable or the database behind it is gone.

A repository holding no data for that node is NOT an error: the
command exits 0, says so on stderr, and prints nothing on stdout
under -o json and -o yaml. Read the exit status, not the byte count.

Example:
  pgedge starfleet byoc backup-repository get <repository_id> n1
  pgedge starfleet byoc backup-repository get <repository_id> n1 -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the client, as in list. backup_limit and
			// backup_offset declare no bounds, and unlike the list
			// endpoints the API never clamps this one (pagination.go),
			// so backupInfoDefaults carries cap 0.
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
			id, err := parseUUIDArg(args[0], "backup repository ID")
			if err != nil {
				return err
			}
			nodeName := args[1]

			params := &api.GetBackupRepositoryInfoParams{}
			if backupType != "" {
				bt := api.GetBackupRepositoryInfoParamsBackupType(
					backupType)
				if !bt.Valid() {
					return newExitError(fmt.Sprintf(
						"unknown backup type %q: must be full, diff, "+
							"or incr", backupType), ExitUsage)
				}
				params.BackupType = &bt
			}
			if sendLimit {
				params.BackupLimit = &limit
			}
			if sendOffset {
				params.BackupOffset = &offset
			}
			if descending {
				params.Descending = &descending
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// Untyped call: this operation declares a 204 for a node
			// with no backups yet, and the generated parser has no case
			// for it, so an empty 204 with a JSON content type reaches
			// the catch-all and fails as "unexpected end of JSON input"
			// (measured: exit 1). See checkEmptyBodyResponse.
			resp, err := client.GetBackupRepositoryInfo(
				context.Background(), id, nodeName, params)
			if err != nil {
				return fmt.Errorf("get backup repository: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf(
					"read get backup repository response: %w", err)
			}
			if err := checkResponse(resp.StatusCode,
				string(body)); err != nil {
				return err
			}

			// `null` counts as empty: json.Unmarshal of `null` into a
			// struct succeeds and leaves it zero, which would print
			// three blank fields at exit 0 that a script parses as a
			// real repository. The API sends `null` as a no-content
			// body elsewhere (restore, rotate-password).
			var info *api.PgBackrestRepositoryInfo
			trimmed := bytes.TrimSpace(body)
			if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
				var decoded api.PgBackrestRepositoryInfo
				if err := json.Unmarshal(trimmed, &decoded); err != nil {
					return fmt.Errorf(
						"decode get backup repository response: %w",
						err)
				}
				info = &decoded
			}

			if rt.Output.Structured() {
				// Printing `null` would hand a script a value the API
				// never sent.
				if info == nil {
					fmt.Fprintln(rt.Stderr,
						"No backup repository data returned.")
					return nil
				}
				return rt.Output.Print(info, nil)
			}

			if info == nil {
				fmt.Fprintln(rt.Stderr,
					"No backup repository data returned.")
				return nil
			}

			// Context for the table, not a row of it, so stderr. Every
			// field is a plain string in the generated client,
			// BackupRepositoryId included, and Fprintln escapes nothing,
			// so each is sanitized here.
			summary := fmt.Sprintf("Repository %s, node %s",
				output.Sanitize(info.BackupRepositoryId),
				output.Sanitize(info.NodeName))
			if info.Status != nil && *info.Status != "" {
				summary += fmt.Sprintf(", status %s",
					output.Sanitize(*info.Status))
			}
			if info.PgVersion != nil && *info.PgVersion != "" {
				summary += fmt.Sprintf(", PostgreSQL %s",
					output.Sanitize(*info.PgVersion))
			}
			fmt.Fprintln(rt.Stderr, summary+".")

			backups := info.Backups
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
			cli.PrintTruncationHint(rt, len(*backups), limit,
				backupInfoDefaults, "backups")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&backupType, "type", "",
		"Filter backups by type: full, diff, or incr")
	f.IntVar(&limit, "limit", 0,
		"Maximum number of backups to return")
	f.IntVar(&offset, "offset", 0,
		"Offset into the backups for pagination")
	f.BoolVar(&descending, "descending", false,
		"Sort backups in descending order")
	return cmd
}

// --- row adapters ---

type backupRepositoryRow struct {
	id, databaseID, typ, location, retention, created string
}

func (r backupRepositoryRow) Columns() []string {
	return []string{
		r.id, r.databaseID, r.typ, r.location, r.retention, r.created,
	}
}

// backupRepositoryRowFrom adapts an api.BackupRepository into a table
// row.
func backupRepositoryRowFrom(r api.BackupRepository) backupRepositoryRow {
	return backupRepositoryRow{
		id:         output.DerefString(r.Id),
		databaseID: output.DerefString(r.DatabaseId),
		typ:        output.DerefString(r.Type),
		location:   repositoryLocation(r),
		retention:  repositoryRetention(r),
		created:    output.FormatTime(output.DerefString(r.CreatedAt)),
	}
}

// repositoryLocation returns the bucket or container the repository
// writes to. Which field carries it depends on the provider, and only
// the one matching Type is ever set.
func repositoryLocation(r api.BackupRepository) string {
	for _, candidate := range []*string{
		r.S3Bucket, r.AzureContainer, r.GcsBucket,
	} {
		if candidate != nil && *candidate != "" {
			return *candidate
		}
	}
	return ""
}

// repositoryRetention renders the retention policy as a count and its
// unit, which the API reports in two separate optional fields.
func repositoryRetention(r api.BackupRepository) string {
	if r.RetentionFull == nil {
		return ""
	}
	unit := output.DerefString(r.RetentionFullType)
	if unit == "" {
		return fmt.Sprintf("%d", *r.RetentionFull)
	}
	return fmt.Sprintf("%d (%s)", *r.RetentionFull, unit)
}

type backupRow struct {
	label, typ, size, databaseSize, started, finished string
}

func (r backupRow) Columns() []string {
	return []string{
		r.label, r.typ, r.size, r.databaseSize, r.started, r.finished,
	}
}

// backupRowFrom adapts an api.PgBackrestBackupInfo into a table row.
func backupRowFrom(b api.PgBackrestBackupInfo) backupRow {
	return backupRow{
		label:        output.DerefString(b.Label),
		typ:          output.DerefString(b.Type),
		size:         output.FormatBytes(b.BackupSize),
		databaseSize: output.FormatBytes(b.DatabaseSize),
		started:      output.FormatTime(output.DerefString(b.CreatedAt)),
		finished:     output.FormatTime(output.DerefString(b.FinishedAt)),
	}
}
