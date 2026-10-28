package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/dotenv"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/connstr"
)

func newDatabaseEnvCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Write a database's connection into a .env file",
		Long: `env writes one node's connection of a BYOC database into a
project's .env file, where frameworks and ORMs read DATABASE_URL.

Example:
  pgedge starfleet byoc database env pull f6a7b8c9-d0e1-2345-fabc-456789012345`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newDatabaseEnvPullCmd(rt))
	return cmd
}

func newDatabaseEnvPullCmd(rt *module.Runtime) *cobra.Command {
	var (
		file     string
		varName  string
		node     string
		internal bool
	)
	cmd := &cobra.Command{
		Use:   "pull <database_id>",
		Short: "Write DATABASE_URL for a database node into .env",
		Long: `pull sets DATABASE_URL in .env to a connection URI for one node
of a BYOC database, and leaves every other line of the file as it
was. An existing DATABASE_URL line is replaced; without one the line
is appended, and a missing file is created readable by you alone.
--file names another file, and --var another variable.

The node is chosen as connection-string chooses it. With one node no
flag is needed. With several, --node <name> picks one; without it the
nodes are listed on stdout, nothing is written, and the command exits
2. --internal builds the URI from the node's internal_host, for an
application inside the cluster's network.

The value is the role's live password in a URI. Every character a
.env loader could expand or cut, $ and # among them, is
percent-encoded, so the value is written unquoted and every loader
reads the same string. Inside a git work tree, a file git does not
ignore draws a warning on stderr naming the line to add to
.gitignore. Apart from the node listing, the command writes nothing
to stdout.

Example:
  pgedge starfleet byoc database env pull f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database env pull f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --node n1 --internal --file .env.local`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dotenv.ValidName(varName) {
				return newExitError(fmt.Sprintf("--var %q is not a variable "+
					"name: use letters, digits and underscores, not "+
					"starting with a digit", varName), ExitUsage)
			}
			if file == "" {
				return newExitError("--file given an empty value: name a "+
					"file, or omit the flag to use .env", ExitUsage)
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
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			n, err := pickNode(rt, db, node)
			if err != nil {
				return err
			}
			cs, err := buildNodeConnectionString(db.Id, n, internal, true)
			if err != nil {
				return err
			}
			if err := dotenv.Set(file, varName,
				connstr.EnvFileURI(cs)); err != nil {
				return newExitError(fmt.Sprintf("write %s: %v", file, err),
					ExitGeneral)
			}
			fmt.Fprintf(rt.Stderr, "Set %s in %s to node %s of database "+
				"%s, as %s.\n", output.Sanitize(varName),
				output.Sanitize(file), output.Sanitize(cs.Node),
				output.Sanitize(id.String()),
				output.Sanitize(cs.Username))
			dotenv.WarnIfCommittable(rt, file)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", ".env", "File to write")
	cmd.Flags().StringVar(&varName, "var", "DATABASE_URL",
		"Variable name to set")
	cmd.Flags().StringVar(&node, "node", "",
		"Node whose connection to write (required with several nodes)")
	cmd.Flags().BoolVar(&internal, "internal", false,
		"Use the node's internal_host, for a client inside the cluster")
	return cmd
}
