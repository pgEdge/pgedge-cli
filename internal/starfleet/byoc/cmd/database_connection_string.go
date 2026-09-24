package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/connstr"
)

func newDatabaseConnectionStringCmd(rt *module.Runtime) *cobra.Command {
	var (
		node       string
		internal   bool
		format     string
		noPassword bool
	)
	cmd := &cobra.Command{
		Use:   "connection-string <database_id>",
		Short: "Print a connection string for one node of a database",
		Long: `connection-string prints a libpq connection URI for a BYOC
database, assembled from a node's connection block in database get.

A BYOC database has one connection block per node, so the string
belongs to a node. With one node it is printed without further ado.
With several, --node <name> picks one; without it the nodes are
listed (label, name, host, internal host, port) and the command exits
2. A node is named by its name or its logical name, as database get
prints them; a label two nodes share is refused, and the node's own
name resolves it. The argument takes a full UUID.

A node reachable from the public internet carries host; a node on a
private cluster carries internal_host instead.
--internal builds the string from internal_host, for an application
that runs inside the cluster's network. Without it a node that has
only internal_host is refused with exit 1 and a sentence naming the
flag, rather than silently handed out on another network.

The string carries the role's password. That is what makes it a
connection string, and it is also a live credential on stdout: the
CLI warns on stderr when stdout is a terminal, and --no-password
leaves the password out for a log, a document or a paste. The
username and password are percent-encoded, so a password holding @,
:, / or ? produces a URI that parses back to what was meant.

--format chooses the shape. uri, the default, prints one line:
postgresql://user:password@host:port/database?sslmode=require. env
prints one PG* variable per line (PGHOST, PGPORT, PGDATABASE, PGUSER,
PGPASSWORD, PGSSLMODE), each single-quoted for a POSIX shell, so the
output can be sourced or written to an env file. Under -o json or
-o yaml the URI is returned alongside the parts it was built from,
and the node it belongs to; the several-nodes-no-flag listing is an
array of node rows in that format, still at exit 2.

Example:
  pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --node n2 --no-password
  pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --node n1 --internal --format env > .env`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := connstr.ValidateFormat(format); err != nil {
				return err
			}
			if cmd.Flags().Changed("node") && node == "" {
				return newExitError("--node given an empty value: name a "+
					"node, or omit the flag on a single-node database",
					ExitUsage)
			}
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetDatabaseWithResponse(
				context.Background(), id, &api.GetDatabaseParams{})
			if err != nil {
				return fmt.Errorf("get database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return newExitError(
					fmt.Sprintf("database %s not found", id), ExitNotFound)
			}

			n, err := pickNode(rt, resp.JSON200, node)
			if err != nil {
				return err
			}
			cs, err := buildNodeConnectionString(
				resp.JSON200.Id, n, internal, !noPassword)
			if err != nil {
				return err
			}

			// Before the format branch: a live password on a terminal
			// is the same exposure under -o json as under text.
			if !noPassword && connstr.StdoutIsTerminal(rt) {
				fmt.Fprintf(rt.Stderr, "Warning: this output carries "+
					"%s's password. Pass --no-password to leave it out.\n",
					output.Sanitize(cs.Username))
			}
			if rt.Output.Structured() {
				return rt.Output.Print(cs, nil)
			}
			if format == "env" {
				return connstr.PrintEnv(rt.Stdout, cs, !noPassword)
			}
			_, err = fmt.Fprintln(rt.Stdout, cs.URI)
			return err
		},
	}
	cmd.Flags().StringVar(&node, "node", "",
		"Node whose connection to print (required with several nodes)")
	cmd.Flags().BoolVar(&internal, "internal", false,
		"Use the node's internal_host, for a client inside the cluster")
	cmd.Flags().StringVar(&format, "format", "uri",
		"Shape of the text output: uri or env")
	cmd.Flags().BoolVar(&noPassword, "no-password", false,
		"Leave the password out of the string")
	_ = cmd.RegisterFlagCompletionFunc("format",
		func(*cobra.Command, []string, string) (
			[]string, cobra.ShellCompDirective,
		) {
			return connstr.FormatCompletion(),
				cobra.ShellCompDirectiveNoFileComp
		})
	return cmd
}

// nodeListColumns is the table printed when a multi-node database is
// asked for a string with no --node.
var nodeListColumns = []string{
	"NODE", "NAME", "HOST", "INTERNAL HOST", "PORT",
}

// nodeListRow is one node of that table; it carries no credential.
// NAME is the node's own name, which --node also accepts, so a label
// two nodes share can still be resolved from the listing. The renderer
// escapes every cell, so none is escaped here.
type nodeListRow struct {
	Node         string `json:"node"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	InternalHost string `json:"internal_host"`
	Port         int    `json:"port"`
}

func (r nodeListRow) Columns() []string {
	return []string{r.Node, r.Name, r.Host, r.InternalHost,
		strconv.Itoa(r.Port)}
}

// pickNode chooses the node the string belongs to. A database GET
// (measured 2026-08-29, two databases) carried no database-level
// connection block, only one per node, so the node list is the
// source and the database-level block is not consulted.
func pickNode(
	rt *module.Runtime, d *api.Database, name string,
) (*api.DatabaseNode, error) {
	var nodes []api.DatabaseNode
	if d.Nodes != nil {
		nodes = *d.Nodes
	}
	if len(nodes) == 0 {
		return nil, newExitError(fmt.Sprintf(
			"database %s has no nodes yet; it is still being created, "+
				"or its status is not available", d.Id), ExitGeneral)
	}
	if name != "" {
		var hits []*api.DatabaseNode
		for i := range nodes {
			n := &nodes[i]
			if n.Name == name ||
				(n.LogicalName != nil && *n.LogicalName == name) {
				hits = append(hits, n)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
			return nil, newExitError(fmt.Sprintf(
				"database %s has no node %q; its nodes are %s", d.Id,
				output.Sanitize(name),
				output.Sanitize(strings.Join(nodeNames(nodes), ", "))),
				ExitUsage)
		}
		// Two nodes answer to one label, so a match by array order
		// would hand out a string for a node the caller did not name.
		return nil, newExitError(fmt.Sprintf(
			"database %s has %d nodes answering to %q; name one by its "+
				"name rather than its logical name", d.Id, len(hits),
			output.Sanitize(name)), ExitUsage)
	}
	if len(nodes) == 1 {
		return &nodes[0], nil
	}
	rows := make([]output.Row, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, nodeListRow{
			Node:         nodeLabel(n),
			Name:         n.Name,
			Host:         output.DerefString(n.Connection.Host),
			InternalHost: output.DerefString(n.Connection.InternalHost),
			Port:         n.Connection.Port,
		})
	}
	if err := rt.Output.Print(rows, nodeListColumns); err != nil {
		return nil, err
	}
	return nil, newExitError(fmt.Sprintf(
		"database %s has %d nodes; pass --node <name> to choose one",
		d.Id, len(nodes)), ExitUsage)
}

// buildNodeConnectionString picks the host by network and refuses,
// rather than switches, when the network asked for is not the one the
// node advertises. Measured 2026-08-29: a public node
// carries host and no internal_host, a private-cluster node the
// reverse.
func buildNodeConnectionString(
	dbID string, n *api.DatabaseNode, internal, withPassword bool,
) (*connstr.String, error) {
	c := n.Connection
	host := output.DerefString(c.Host)
	other := output.DerefString(c.InternalHost)
	if internal {
		host, other = other, host
	}
	if host == "" {
		if other != "" {
			if internal {
				return nil, newExitError(fmt.Sprintf(
					"node %s of database %s has no internal_host; it "+
						"has host %s, reachable without --internal",
					output.Sanitize(nodeLabel(*n)), dbID,
					output.Sanitize(other)), ExitGeneral)
			}
			return nil, newExitError(fmt.Sprintf(
				"node %s of database %s has no host; it has "+
					"internal_host %s, reachable from inside the "+
					"cluster's network with --internal",
				output.Sanitize(nodeLabel(*n)), dbID,
				output.Sanitize(other)), ExitGeneral)
		}
		return nil, newExitError(fmt.Sprintf(
			"node %s of database %s has no connection host yet; it is "+
				"still being created, or its status is not available",
			output.Sanitize(nodeLabel(*n)), dbID), ExitGeneral)
	}
	cs := connstr.Build(host, c.Port, c.Database, c.Username,
		c.Password, withPassword)
	cs.Node = nodeLabel(*n)
	return cs, nil
}

// nodeLabel is the name database get prints: the logical name when
// one is set, otherwise the node's own.
func nodeLabel(n api.DatabaseNode) string {
	if n.LogicalName != nil && *n.LogicalName != "" {
		return *n.LogicalName
	}
	return n.Name
}

func nodeNames(nodes []api.DatabaseNode) []string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, nodeLabel(n))
	}
	sort.Strings(names)
	return names
}
