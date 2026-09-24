package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// announceAndWait is the shared close-out for task-returning database
// mutations. task and payload come from the same response: task drives
// the message and the poller, and payload is the whole envelope (e.g.
// *api.ApplyUpgradeResponse2), printed verbatim so it carries whatever
// else the endpoint returned.
func announceAndWait(
	rt *module.Runtime, client *api.ClientWithResponses,
	wf *waitFollowOpts, verb, dbID string, task api.Task, payload any,
) error {
	fmt.Fprintf(rt.Stderr, "%s task %s accepted (%s).\n",
		output.Sanitize(verb), task.TaskId, output.Sanitize(string(task.Status)))
	if err := emitAccepted(rt, payload); err != nil {
		return err
	}
	return wf.run(rt, databaseTaskSource(client, dbID, task.TaskId))
}

func parseScheduledAt(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, &ExitError{
			msg: fmt.Sprintf(
				"invalid --scheduled-at %q: want RFC3339 "+
					"(e.g. 2026-07-17T15:04:05Z): %v", s, err),
			code: ExitUsage,
		}
	}
	return &t, nil
}

func newDatabaseUpgradeCmd(rt *module.Runtime) *cobra.Command {
	var image string
	var force bool
	cmd := &cobra.Command{
		Use:   "upgrade <database_id>",
		Short: "Upgrade a database to a new image",
		Long: `upgrade applies a minor-version upgrade, moving the
database to the container image given by --image. The image must be a
newer build in the same Postgres/Spock major bucket. This restarts the
database; prompts for confirmation (use --force to skip). Asynchronous;
use --wait/--follow to track it.

Example:
  pgedge controlplane database upgrade storefront --image pgedge/pgedge:16.4-1
  pgedge controlplane database upgrade storefront --image pgedge/pgedge:16.4-1 \
    --force --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&image, "image", "",
		"Target container image reference (required)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if image == "" {
			return &ExitError{
				msg:  "an upgrade target is required: --image <ref>",
				code: ExitUsage,
			}
		}
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Upgrade database %s to %s? This restarts the database.",
			args[0], image),
			force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		body := api.ApplyUpgradeJSONRequestBody{Image: image}
		resp, err := client.ApplyUpgradeWithResponse(
			context.Background(), args[0], body)
		if err != nil {
			return networkError("upgrade database", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Upgrade", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newDatabaseRestoreCmd(rt *module.Runtime) *cobra.Command {
	var specPath string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <database_id>",
		Short: "Restore a database from a spec file",
		Long: `restore rebuilds a database from a backup repository
described by a restore spec (-f restore.yaml, restore.json, or - for
stdin). The spec's restore_config names the source repository, database,
and node; target_nodes optionally limits which nodes are restored.
A spec still holding the CHANGE-ME placeholder in restore_config is
refused before the prompt, naming the field. This overwrites the
database's current data; prompts for confirmation (use --force to
skip it). --force-unmodifiable restores even if the
database is in an unmodifiable state. Asynchronous; use
--wait/--follow to track it.

A restore clears backup_config from the database and from every node it
restores, so backups are off while no block is in place. A database
update carrying a backup_config re-enables them, and pointing that
repository at a new base_path or id keeps pgBackRest from being asked
to reuse the old location.

Example:
  pgedge controlplane database restore storefront -f restore.yaml
  pgedge controlplane database restore storefront -f restore.yaml --force --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVarP(&specPath, "file", "f", "",
		"Restore spec file path, or - for stdin (required)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	var forceUnmodifiable bool
	cmd.Flags().BoolVar(&forceUnmodifiable, "force-unmodifiable", false,
		"Restore even if the database is in an unmodifiable state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if specPath == "" {
			return &ExitError{
				msg: "a restore spec file is required: -f <path> " +
					"(or - for stdin)",
				code: ExitUsage,
			}
		}
		var body api.RestoreDatabaseRequest
		if err := loadSpecFile(rt, specPath, &body); err != nil {
			return err
		}
		if err := requireRestoreContent(
			specPath, body.RestoreConfig); err != nil {
			return err
		}
		if err := checkUnfilledPlaceholders(nil, nil, nil,
			&body.RestoreConfig, nil, "restore"); err != nil {
			return err
		}
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Restore database %s? This overwrites its current data.",
			args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.RestoreDatabaseParams{}
		if forceUnmodifiable {
			f := true
			params.Force = &f
		}
		resp, err := client.RestoreDatabaseWithResponse(
			context.Background(), args[0], params, body)
		if err != nil {
			return networkError("restore database", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		if n := len(resp.JSON200.NodeTasks); n > 0 {
			fmt.Fprintf(rt.Stderr,
				"Restore spawned %d node task(s).\n", n)
		}
		return announceAndWait(
			rt, client, wf, "Restore", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cmd.AddCommand(newDatabaseRestoreTemplateCmd(rt))
	cli.MarkMutating(cmd)

	return cmd
}
