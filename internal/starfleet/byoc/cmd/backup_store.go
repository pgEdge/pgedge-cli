package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// backupStoreColumns are the table headers shared by backup-store list
// and get.
var backupStoreColumns = []string{
	"ID", "NAME", "STATUS", "CLOUD ACCOUNT ID", "CREATED AT",
}

// NewBackupStoreCmd builds the `pgedge starfleet byoc backup-store` command
// group. The plural "backup-stores" is kept as a plural alias
// (unlisted in help) so existing scripts keep working.
func NewBackupStoreCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup-store",
		Aliases: []string{"backup-stores"},
		Short:   "Manage pgEdge BYOC backup stores",
		Long: `backup-store manages the backup stores that hold your
databases' backups: the cloud object storage a cluster writes to.

Use these commands to list, inspect, create, and delete backup
stores. A cluster needs at least one attached store to host a
database.

Example:
  pgedge starfleet byoc backup-store list
  pgedge starfleet byoc backup-store create --name store1 --cloud-account-id <account_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newBackupStoreListCmd(rt),
		newBackupStoreGetCmd(rt),
		newBackupStoreCreateCmd(rt),
		newBackupStoreDeleteCmd(rt),
	)
	return cmd
}

// --- list ---

func newBackupStoreListCmd(rt *module.Runtime) *cobra.Command {
	var (
		limit         int
		offset        int
		createdAfter  string
		createdBefore string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backup stores",
		Long: `list shows the backup stores in the active account.

Use it to find a store's ID before running get or delete, or to
find a store to attach when creating a cluster. Filter with the
--created-after / --created-before timestamp range.

Example:
  pgedge starfleet byoc backup-store list
  pgedge starfleet byoc backup-store list --limit 20 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client: a bad --limit is knowable locally, so
			// it answers 2 rather than 5 for credentials it never needed.
			// byoc.yaml declares no paging bounds on any list endpoint,
			// hence NoUpperBound: the server clamps at 100 today, but a
			// measured clamp is not a published contract and the CLI must
			// not refuse a value the API would accept.
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
			params := &api.ListBackupStoresParams{}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if err := conn.ApplyCreatedRange(cmd.Flags(),
				&params.CreatedAfter, &params.CreatedBefore); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListBackupStoresWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list backup stores: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			stores := resp.JSON200
			if stores == nil || len(*stores) == 0 {
				fmt.Fprintln(rt.Stderr, "No backup stores found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*stores))
			for _, s := range *stores {
				rows = append(rows, backupStoreRowFrom(s))
			}
			if err := rt.Output.Print(rows, backupStoreColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*stores), limit,
				backupStoreDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(backupStoreDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.StringVar(&createdAfter, "created-after", "",
		"Filter: created after this RFC3339 timestamp")
	f.StringVar(&createdBefore, "created-before", "",
		"Filter: created before this RFC3339 timestamp")
	return cmd
}

// --- get ---

func newBackupStoreGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <backup_store_id>",
		Short: "Show backup store details",
		Long: `get shows the details of a single backup store.

Use it to check a store's status, cloud account, and creation date.
The argument is the store's UUID.

Example:
  pgedge starfleet byoc backup-store get <backup_store_id> -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "backup store ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetBackupStoreWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get backup store: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			s := resp.JSON200
			if s == nil {
				fmt.Fprintln(rt.Stderr, "No backup store data returned.")
				return nil
			}
			rows := []output.Row{backupStoreRowFrom(*s)}
			return rt.Output.Print(rows, backupStoreColumns)
		},
	}
}

// --- create ---

func newBackupStoreCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name           string
		cloudAccountID string
		region         string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a backup store",
		Long: `create provisions a new backup store in a cloud account.

Use it to set up object storage a cluster can write backups to.
The --name and --cloud-account-id are required. Pass --wait to
block until provisioning finishes.

Example:
  pgedge starfleet byoc backup-store create --name store1 \
    --cloud-account-id <account_id>
  pgedge starfleet byoc backup-store create --name store1 \
    --cloud-account-id <account_id> --region us-east-1 --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The parsed value is kept and sent. uuid.Parse accepts
			// `{uuid}` and `urn:uuid:uuid`, so discarding it and
			// forwarding the raw flag put a braced id on the wire
			// verbatim -- passing the check and then sending something
			// else, the unchecked-ID defect in another spelling.
			parsedAccount, err := parseUUIDArg(
				cloudAccountID, "cloud account ID")
			if err != nil {
				return err
			}
			cloudAccountID = parsedAccount.String()

			// Before the client, because nothing is sent: a store's
			// region is settable exactly once. /byoc/v1/backup-stores
			// exposes no put or patch, the generated client emits no
			// update operation, and the BackupStore response schema
			// does not even carry region -- so `--region "$UNSET"`
			// silently created the store wherever the API chose, for
			// good.
			region, sendRegion, err := cli.OptionalStringFlag(
				cmd.Flags(), "region",
				"name a region, or omit the flag to let the API choose")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateBackupStoreJSONRequestBody{
				Name:           name,
				CloudAccountId: cloudAccountID,
			}
			if sendRegion {
				body.Region = &region
			}

			resp, err := client.CreateBackupStoreWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create backup store: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			s := resp.JSON200
			if s == nil {
				// Accepted, but no body to read an id from — nothing to
				// track.
				fmt.Fprintln(rt.Stderr,
					"Backup store created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(s, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Backup store %q created (id: %s, status: %s).\n",
					s.Name, output.Sanitize(s.Id), output.Sanitize(s.Status))
			}
			return trackMutation(rt, client, s.Id, "")
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Backup store name")
	f.StringVar(&cloudAccountID, "cloud-account-id", "",
		"Cloud account to attach (full UUID)")
	f.StringVar(&region, "region", "",
		"Region for the backup store; cannot be changed later "+
			"(omit to let the API choose)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cloud-account-id")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newBackupStoreDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <backup_store_id>",
		Short: "Delete a backup store",
		Long: `delete removes a backup store.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument is the store's UUID. Pass --wait to
block until teardown finishes.

Example:
  pgedge starfleet byoc backup-store delete <backup_store_id>
  pgedge starfleet byoc backup-store delete <backup_store_id> --force --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "backup store ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete backup store %s? This cannot be undone.", id)
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

			resp, err := client.DeleteBackupStore(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete backup store: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete backup store"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Backup store %s deleted.\n", id)
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

type backupStoreRow struct {
	id, name, status, cloudAccountID, createdAt string
}

func (r backupStoreRow) Columns() []string {
	return []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
		r.cloudAccountID,
		r.createdAt,
	}
}

// backupStoreRowFrom adapts an api.BackupStore into a table row.
func backupStoreRowFrom(s api.BackupStore) backupStoreRow {
	return backupStoreRow{
		id:             s.Id,
		name:           s.Name,
		status:         s.Status,
		cloudAccountID: s.CloudAccountId,
		createdAt:      output.FormatTime(s.CreatedAt),
	}
}
