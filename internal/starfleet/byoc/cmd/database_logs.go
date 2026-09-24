package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// databaseLogBlock is one node's slice of the log response, named for
// the text renderer's benefit.
//
// The spec declares each block as a bare `type: object`, so the
// generated Logs is []map[string]interface{} and "node" is undeclared.
// Structured output prints the generated body as-is; this type exists
// only so `-o text` can read fields without inline type assertions.
type databaseLogBlock struct {
	Logs []string
	Node string
}

// databaseLogBlocksFrom lifts the two fields the text renderer needs out
// of the generated bare-object blocks.
//
// A block missing either key yields an empty section rather than an
// error: the spec constrains none of this shape, and a partial answer
// is still worth printing.
func databaseLogBlocksFrom(
	raw []map[string]interface{},
) []databaseLogBlock {
	out := make([]databaseLogBlock, 0, len(raw))
	for _, block := range raw {
		var b databaseLogBlock
		if node, ok := block["node"].(string); ok {
			b.Node = node
		}
		if lines, ok := block["logs"].([]interface{}); ok {
			for _, line := range lines {
				if s, ok := line.(string); ok {
					b.Logs = append(b.Logs, s)
				}
			}
		}
		out = append(out, b)
	}
	return out
}

// --- logs ---

func newDatabaseLogsCmd(rt *module.Runtime) *cobra.Command {
	var (
		componentName string
		nodes         string
		maxLines      int
	)
	cmd := &cobra.Command{
		Use:   "logs <database_id>",
		Short: "Read a database component's logs",
		Long: `logs reads log lines from a component running on a
database's nodes.

Both filters are required, because the API requires them: a call
missing either answers 400. --component-name names the component to
read — 'postgres' is the one every database runs — and --nodes takes a
comma-separated list of node names, as shown by 'node list'.

The API does not validate either name. An unknown component and an
unknown node both answer 200 with no log blocks, exactly as a real
component that has logged nothing does, so 'No logs found.' cannot tell
you which of the three happened.

Text output prints each node's lines verbatim under an '==> node <=='
header, so it pipes into grep unchanged. Choose json or yaml to get the
blocks structured.

The argument takes a full UUID.

Example:
  pgedge starfleet byoc database logs f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --component-name postgres --nodes n1
  pgedge starfleet byoc database logs f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --component-name postgres \
    --nodes n1,n2 --max-lines 500`,
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

			params := &api.GetDatabaseLogsParams{
				ComponentName: componentName,
				Nodes:         nodes,
			}
			if cmd.Flags().Changed("max-lines") {
				params.MaxLines = &maxLines
			}

			resp, err := client.GetDatabaseLogsWithResponse(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("get database logs: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			if resp.JSON200 == nil || len(resp.JSON200.Logs) == 0 {
				fmt.Fprintln(rt.Stderr, "No logs found.")
				return nil
			}
			for _, block := range databaseLogBlocksFrom(resp.JSON200.Logs) {
				// The node name is a label in a header, so sanitized;
				// log lines are content and pass through verbatim.
				fmt.Fprintf(rt.Stdout, "==> %s <==\n",
					output.Sanitize(block.Node))
				for _, line := range block.Logs {
					fmt.Fprintln(rt.Stdout, line)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&componentName, "component-name", "",
		"Component to read logs from, such as postgres")
	f.StringVar(&nodes, "nodes", "",
		"Comma-separated node names to read logs from")
	f.IntVar(&maxLines, "max-lines", 0,
		"Maximum number of log lines to return (API default 100)")
	_ = cmd.MarkFlagRequired("component-name")
	_ = cmd.MarkFlagRequired("nodes")
	return cmd
}
