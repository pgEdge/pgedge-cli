package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// NewDatabaseNodeCmd builds `pgedge controlplane database node`: per-node
// operations (backup, switchover, failover) on a database.
func NewDatabaseNodeCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node",
		Aliases: []string{"nodes"},
		Short:   "Run per-node database operations",
		Long: `node runs operations scoped to a single database node:
back it up, or change its replication role with switchover/failover.

Example:
  pgedge controlplane database node backup storefront n1 --type full
  pgedge controlplane database node switchover storefront n1`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newNodeBackupCmd(rt),
		newNodeSwitchoverCmd(rt),
		newNodeFailoverCmd(rt),
	)
	return cmd
}

func newNodeBackupCmd(rt *module.Runtime) *cobra.Command {
	var backupType string
	var forceUnmodifiable bool
	cmd := &cobra.Command{
		Use:   "backup <database_id> <node_name>",
		Short: "Back up a database node",
		Long: `backup starts a pgBackRest backup of a single node. Choose
the backup type with --type (full, diff, or incr; default full). The
operation is asynchronous; use --wait/--follow to track it.

Example:
  pgedge controlplane database node backup storefront n1 --type full
  pgedge controlplane database node backup storefront n1 --type incr --wait`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&backupType, "type", "full",
		"Backup type: full, diff, or incr")
	cmd.Flags().BoolVar(&forceUnmodifiable, "force-unmodifiable", false,
		"Attempt the backup even in an unmodifiable state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		bt := api.BackupOptionsType(backupType)
		if !bt.Valid() {
			return &ExitError{
				msg: fmt.Sprintf(
					"invalid --type %q: want full, diff, or incr",
					backupType),
				code: ExitUsage,
			}
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.BackupDatabaseNodeParams{}
		if forceUnmodifiable {
			f := true
			params.Force = &f
		}
		body := api.BackupDatabaseNodeJSONRequestBody{Type: bt}
		resp, err := client.BackupDatabaseNodeWithResponse(
			context.Background(), args[0], args[1], params, body)
		if err != nil {
			return networkError("back up node", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Backup", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newNodeSwitchoverCmd(rt *module.Runtime) *cobra.Command {
	var candidate, scheduledAt string
	var force bool
	cmd := &cobra.Command{
		Use:   "switchover <database_id> <node_name>",
		Short: "Switch over a database node",
		Long: `switchover performs a graceful, planned role change on a
node, promoting a replica to leader. It drops the connections the old
leader was holding, so a connected client notices. Prompts for
confirmation; use --force to skip it. Optionally target a replica with
--candidate or defer the change with --scheduled-at (RFC3339). The
operation is asynchronous; use --wait/--follow to track it.

Example:
  pgedge controlplane database node switchover storefront n1
  pgedge controlplane database node switchover storefront n1 \
    --candidate storefront-n2-9ptayhma --force --wait`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&candidate, "candidate", "",
		"Instance ID of the replica candidate to promote")
	cmd.Flags().StringVar(&scheduledAt, "scheduled-at", "",
		"Schedule the switchover at an RFC3339 time (default now)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Parse before prompting, so a bad --scheduled-at is a usage
		// error rather than a question about a switchover that cannot
		// run.
		when, err := parseScheduledAt(scheduledAt)
		if err != nil {
			return err
		}
		// Planned is gentler to the cluster, not invisible to a
		// connected client, and "a client notices" is the bar for a
		// prompt. The endpoint has no force parameter, so --force only
		// skips the prompt.
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Switch over node %s in database %s? This drops the "+
				"connections the current leader is holding.",
			args[1], args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		body := api.SwitchoverDatabaseNodeJSONRequestBody{}
		if candidate != "" {
			body.CandidateInstanceId = &candidate
		}
		if when != nil {
			body.ScheduledAt = when
		}
		resp, err := client.SwitchoverDatabaseNodeWithResponse(
			context.Background(), args[0], args[1], body)
		if err != nil {
			return networkError("switch over node", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Switchover", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newNodeFailoverCmd(rt *module.Runtime) *cobra.Command {
	var candidate string
	var skipValidation, force bool
	cmd := &cobra.Command{
		Use:   "failover <database_id> <node_name>",
		Short: "Fail over a database node",
		Long: `failover forces an unplanned promotion of a replica when a
node's leader is unhealthy. This can interrupt writes and, on a
healthy cluster, is usually unnecessary — prefer switchover. Prompts
for confirmation; use --force to skip it. --candidate targets a
specific replica; --skip-validation bypasses the health checks that
normally block failover on a healthy cluster.

Example:
  pgedge controlplane database node failover storefront n1
  pgedge controlplane database node failover storefront n1 --force --wait`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&candidate, "candidate", "",
		"Instance ID of the replica to promote")
	cmd.Flags().BoolVar(&skipValidation, "skip-validation", false,
		"Skip health checks that block failover on a healthy cluster")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Fail over node %s in database %s? This is an unplanned "+
				"promotion.", args[1], args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		body := api.FailoverDatabaseNodeJSONRequestBody{}
		if candidate != "" {
			body.CandidateInstanceId = &candidate
		}
		if skipValidation {
			sv := true
			body.SkipValidation = &sv
		}
		resp, err := client.FailoverDatabaseNodeWithResponse(
			context.Background(), args[0], args[1], body)
		if err != nil {
			return networkError("fail over node", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Failover", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}
