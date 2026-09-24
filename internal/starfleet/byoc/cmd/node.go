package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// nodeColumns are the table headers for node list.
var nodeColumns = []string{
	"ID", "NAME", "REGION", "INSTANCE TYPE", "IP ADDRESS",
}

// NewNodeCmd builds the `pgedge starfleet byoc node` command group, which
// inspects the nodes that make up a cluster. The plural "nodes" is
// kept as a plural alias (unlisted in help).
func NewNodeCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node",
		Aliases: []string{"nodes"},
		Short:   "Inspect cluster nodes",
		Long: `node inspects the individual nodes that make up a
cluster: the instances that run your database.

Use these commands to list a cluster's nodes and read their region,
instance type, and address details, and to read the journald logs on
a single node.

Example:
  pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890
  pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 postgresql`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newNodeListCmd(rt),
		newNodeLogsCmd(rt),
	)
	return cmd
}

// --- list ---

func newNodeListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list <cluster_id>",
		Short: "List a cluster's nodes",
		Long: `list shows the nodes of a cluster.

Use it to see each node's name, region, instance type, and IP
address — for example, to find the node names to target with a
database deploy. The argument takes a cluster's full UUID.

Example:
  pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890
  pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListClusterNodesWithResponse(
				context.Background(), clusterID,
				&api.ListClusterNodesParams{})
			if err != nil {
				return fmt.Errorf("list cluster nodes: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			nodes := resp.JSON200
			if nodes == nil || len(*nodes) == 0 {
				fmt.Fprintln(rt.Stderr, "No nodes found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*nodes))
			for _, n := range *nodes {
				rows = append(rows, nodeRow{
					id:       n.Id,
					name:     n.Name,
					region:   n.Region,
					instance: n.InstanceType,
					ip:       n.IpAddress,
				})
			}
			return rt.Output.Print(rows, nodeColumns)
		},
	}
}

// resolveHostIDs maps requested node names to node IDs for a cluster.
// With no names and a single-node cluster it auto-selects that node;
// with multiple nodes it requires an explicit selection. Unknown names
// yield an error listing the valid ones. Used by database commands that
// target specific nodes (e.g. mcp deploy --target-nodes).
func resolveHostIDs(client *api.ClientWithResponses, clusterID uuid.UUID,
	nodeNames []string) ([]string, error) {
	resp, err := client.ListClusterNodesWithResponse(
		context.Background(), clusterID, &api.ListClusterNodesParams{})
	if err != nil {
		return nil, fmt.Errorf("list cluster nodes: %w", err)
	}

	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}

	nodes := resp.JSON200
	if nodes == nil || len(*nodes) == 0 {
		return nil, newExitError(
			fmt.Sprintf("cluster %s has no nodes", clusterID),
			ExitGeneral)
	}

	nodeList := *nodes

	if len(nodeNames) == 0 {
		if len(nodeList) == 1 {
			return []string{nodeList[0].Id}, nil
		}
		names := make([]string, len(nodeList))
		for i, n := range nodeList {
			names[i] = n.Name
		}
		return nil, newExitError(fmt.Sprintf(
			"cluster has %d nodes (%s) — specify --target-nodes",
			len(nodeList), strings.Join(names, ", ")), ExitGeneral)
	}

	nameToID := make(map[string]string, len(nodeList))
	for _, n := range nodeList {
		nameToID[n.Name] = n.Id
	}

	hostIDs := make([]string, 0, len(nodeNames))
	for _, name := range nodeNames {
		id, ok := nameToID[name]
		if !ok {
			valid := make([]string, 0, len(nameToID))
			for k := range nameToID {
				valid = append(valid, k)
			}
			return nil, newExitError(fmt.Sprintf(
				"node %q not found in cluster — valid names: %s",
				name, strings.Join(valid, ", ")), ExitGeneral)
		}
		hostIDs = append(hostIDs, id)
	}

	return hostIDs, nil
}

// --- row adapter ---

type nodeRow struct {
	id, name, region, instance, ip string
}

func (r nodeRow) Columns() []string {
	return []string{r.id, r.name, r.region, r.instance, r.ip}
}
