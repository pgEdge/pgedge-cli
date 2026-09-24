package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

var hostColumns = []string{"ID", "ORCHESTRATOR", "STATE", "UPDATED"}

type hostRow struct{ id, orchestrator, state, updated string }

func (r hostRow) Columns() []string {
	return []string{r.id, r.orchestrator,
		output.ColorStatus(r.state), r.updated}
}

func hostRowFrom(h api.Host) hostRow {
	return hostRow{
		id:           h.Id,
		orchestrator: h.Orchestrator,
		state:        string(h.Status.State),
		updated:      formatDate(h.Status.UpdatedAt),
	}
}

// NewHostCmd builds `pgedge controlplane host`.
func NewHostCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "host",
		Aliases: []string{"hosts"},
		Short:   "Manage Control Plane hosts",
		Long: `host lists, inspects, and removes the hosts that make up
the control-plane cluster.

Example:
  pgedge controlplane host list
  pgedge controlplane host get host-1`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newHostListCmd(rt), newHostGetCmd(rt), newHostRemoveCmd(rt))
	return cmd
}

func newHostListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List hosts",
		Long: `list shows every host in the cluster.

Example:
  pgedge controlplane host list
  pgedge controlplane host list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.ListHostsWithResponse(
				context.Background())
			if err != nil {
				return networkError("list hosts", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			if resp.JSON200 == nil || len(resp.JSON200.Hosts) == 0 {
				fmt.Fprintln(rt.Stderr, "No hosts found.")
				return nil
			}
			rows := make([]output.Row, 0, len(resp.JSON200.Hosts))
			for _, h := range resp.JSON200.Hosts {
				rows = append(rows, hostRowFrom(h))
			}
			return rt.Output.Print(rows, hostColumns)
		},
	}
}

func newHostGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <host_id>",
		Short: "Show host details",
		Long: `get shows the details of a single host.

Example:
  pgedge controlplane host get host-1
  pgedge controlplane host get host-1 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetHostWithResponse(
				context.Background(), args[0])
			if err != nil {
				return networkError("get host", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			if resp.JSON200 == nil {
				fmt.Fprintln(rt.Stderr, "No host data returned.")
				return nil
			}
			return rt.Output.Print(
				[]output.Row{hostRowFrom(*resp.JSON200)}, hostColumns)
		},
	}
}

func newHostRemoveCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <host_id>",
		Short: "Remove a host from the cluster",
		Long: `remove takes a host out of the cluster. This is
destructive; databases on the host are affected. Use --force to skip
the confirmation prompt. --force-lost additionally removes the host
even if instances exist or quorum would be violated — only for
disaster recovery, when the host is permanently lost. Use
--wait/--follow to track the removal task.

Example:
  pgedge controlplane host remove host-3
  pgedge controlplane host remove host-3 --force
  pgedge controlplane host remove host-3 --force --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	var forceLost bool
	cmd.Flags().BoolVar(&forceLost, "force-lost", false,
		"Remove a permanently lost host even if instances exist or "+
			"quorum would be violated (disaster recovery only)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cli.Confirm(rt,
			fmt.Sprintf("Remove host %s?", args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.RemoveHostParams{}
		if forceLost {
			f := true
			params.Force = &f
		}
		resp, err := client.RemoveHostWithResponse(
			context.Background(), args[0], params)
		if err != nil {
			return networkError("remove host", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 != nil {
			fmt.Fprintf(rt.Stderr,
				"Host removal task %s accepted (%s).\n",
				resp.JSON200.Task.TaskId,
				output.Sanitize(string(resp.JSON200.Task.Status)))
			if err := emitAccepted(rt, resp.JSON200); err != nil {
				return err
			}
			return wf.run(rt, hostTaskSource(
				client, args[0], resp.JSON200.Task.TaskId))
		}
		return nil
	}
	cli.MarkMutating(cmd)

	return cmd
}
