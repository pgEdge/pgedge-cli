package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// NewBackupCmd builds the `pgedge starfleet byoc backup` command group.
// The plural "backups" stays as an alias so existing scripts keep
// working.
//
// Only `create` lives here. Backup list and get are under
// `/managed/v1/backups`, so a read verb belongs to the managed sub-tree,
// and the API has no backup delete or download operation.
func NewBackupCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup",
		Aliases: []string{"backups"},
		Short:   "Manage pgEdge BYOC backups",
		Long: `backup manages the backups taken of your pgEdge BYOC
databases.

Use it to trigger an on-demand backup of a database.

Example:
  pgedge starfleet byoc backup create --database-id <database_id> \
    --provider pgbackrest`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newBackupCreateCmd(rt),
	)

	return cmd
}

// --- create ---

func newBackupCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		databaseID  string
		provider    string
		name        string
		backupType  string
		targetNodes []string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a backup",
		Long: `create takes a new backup of a database.

Use it to trigger an on-demand backup. The --database-id and
--provider are required; --target-nodes restricts the backup to
specific nodes.

Example:
  pgedge starfleet byoc backup create --database-id <database_id> \
    --provider pgbackrest
  pgedge starfleet byoc backup create --database-id <database_id> \
    --provider pgbackrest --type full --name nightly`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbID, err := parseUUIDArg(databaseID, "database ID")
			if err != nil {
				return err
			}
			// Without it the dry-run report would claim no client-side
			// checks, which this parse makes false.
			rt.DryRun.Pass("database ID %s well-formed", dbID)

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.BackupDatabaseJSONRequestBody{
				Provider: provider,
			}
			if name != "" {
				body.Name = &name
			}
			if backupType != "" {
				body.Type = &backupType
			}
			if len(targetNodes) > 0 {
				trimmed := trimSpaces(targetNodes)
				body.TargetNodes = &trimmed
			}

			resp, err := client.BackupDatabase(
				context.Background(), dbID, body)
			if err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "create backup"); err != nil {
				return err
			}

			fmt.Fprintln(rt.Stderr, "Backup initiated.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&databaseID, "database-id", "",
		"Database to back up (full UUID)")
	f.StringVar(&provider, "provider", "", "Backup provider")
	f.StringVar(&name, "name", "", "Optional backup name")
	f.StringVar(&backupType, "type", "", "Optional backup type")
	f.StringSliceVar(&targetNodes, "target-nodes", nil,
		"Comma-separated list of target nodes")
	_ = cmd.MarkFlagRequired("database-id")
	_ = cmd.MarkFlagRequired("provider")
	cli.MarkMutating(cmd)

	return cmd
}
