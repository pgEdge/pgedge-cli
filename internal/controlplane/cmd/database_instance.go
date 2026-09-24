package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// NewDatabaseInstanceCmd builds `pgedge controlplane database instance`:
// lifecycle control of individual database instances.
func NewDatabaseInstanceCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "instance",
		Aliases: []string{"instances"},
		Short:   "Control individual database instances",
		Long: `instance lists individual database instances and starts,
stops and restarts them (the per-host Postgres containers backing a
database). stop and restart interrupt service, so both prompt for
confirmation; --force skips the prompt, and nothing else. start does
not interrupt anything and does not prompt.

Example:
  pgedge controlplane database instance list
  pgedge controlplane database instance restart storefront storefront-n1-689qacsi`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newInstanceListCmd(rt),
		newInstanceStartCmd(rt),
		newInstanceStopCmd(rt),
		newInstanceRestartCmd(rt),
	)
	return cmd
}

func newInstanceStartCmd(rt *module.Runtime) *cobra.Command {
	var forceUnmodifiable bool
	cmd := &cobra.Command{
		Use:   "start <database_id> <instance_id>",
		Short: "Start a database instance",
		Long: `start brings a stopped instance back online. It interrupts
nothing, so it does not prompt. --force-unmodifiable starts the
instance even if the database is in an unmodifiable state.
Asynchronous; use --wait/--follow to track it.

Example:
  pgedge controlplane database instance start storefront storefront-n1-689qacsi
  pgedge controlplane database instance start storefront storefront-n1-689qacsi --wait`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().BoolVar(&forceUnmodifiable, "force-unmodifiable", false,
		"Start even if the database is in an unmodifiable state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.StartInstanceParams{}
		if forceUnmodifiable {
			f := true
			params.Force = &f
		}
		resp, err := client.StartInstanceWithResponse(
			context.Background(), args[0], args[1], params)
		if err != nil {
			return networkError("start instance", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Start", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newInstanceStopCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "stop <database_id> <instance_id>",
		Short: "Stop a database instance",
		Long: `stop takes an instance offline, removing it from the
database until restarted. Disruptive; prompts for confirmation (use
--force to skip it). --force-unmodifiable stops the instance even if
the database is in an unmodifiable state. Asynchronous; use
--wait/--follow to track it.

Example:
  pgedge controlplane database instance stop storefront storefront-n1-689qacsi
  pgedge controlplane database instance stop storefront storefront-n1-689qacsi \
    --force --wait`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	var forceUnmodifiable bool
	cmd.Flags().BoolVar(&forceUnmodifiable, "force-unmodifiable", false,
		"Stop even if the database is in an unmodifiable state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Stop instance %s in database %s?", args[1], args[0]),
			force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.StopInstanceParams{}
		if forceUnmodifiable {
			f := true
			params.Force = &f
		}
		resp, err := client.StopInstanceWithResponse(
			context.Background(), args[0], args[1], params)
		if err != nil {
			return networkError("stop instance", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Stop", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}

func newInstanceRestartCmd(rt *module.Runtime) *cobra.Command {
	var scheduledAt string
	var force bool
	cmd := &cobra.Command{
		Use:   "restart <database_id> <instance_id>",
		Short: "Restart a database instance",
		Long: `restart bounces an instance's Postgres process, dropping
its connections. Disruptive; prompts for confirmation (use --force to
skip it). Defer it with --scheduled-at (RFC3339) — a scheduled restart
is confirmed when you queue it, not when it runs. Asynchronous; use
--wait/--follow to track it.

Example:
  pgedge controlplane database instance restart storefront storefront-n1-689qacsi
  pgedge controlplane database instance restart storefront storefront-n1-689qacsi \
    --force --scheduled-at 2026-07-17T22:00:00Z`,
		Args: cobra.ExactArgs(2),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&scheduledAt, "scheduled-at", "",
		"Schedule the restart at an RFC3339 time (default now)")
	// Prompt-only, unlike start/stop: the restart endpoint has no
	// force parameter.
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Parse before prompting, so a bad --scheduled-at is a usage
		// error rather than a question about a restart that cannot run.
		when, err := parseScheduledAt(scheduledAt)
		if err != nil {
			return err
		}
		if err := cli.Confirm(rt, fmt.Sprintf(
			"Restart instance %s in database %s? This drops its "+
				"connections.", args[1], args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		body := api.RestartInstanceJSONRequestBody{}
		if when != nil {
			body.ScheduledAt = when
		}
		resp, err := client.RestartInstanceWithResponse(
			context.Background(), args[0], args[1], body)
		if err != nil {
			return networkError("restart instance", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Restart", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}
