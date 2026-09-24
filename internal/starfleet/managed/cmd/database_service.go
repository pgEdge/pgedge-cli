package cmd

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// NewDatabaseServiceCmd builds the `pgedge starfleet managed database service`
// command group, which manages the services deployed alongside a
// managed database.
func NewDatabaseServiceCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Aliases: []string{"services"},
		Short:   "Manage services deployed on a database",
		Long: `service manages the services deployed alongside a managed
database: the MCP, RAG and PostgREST services attached to it.

A managed database carries at most one service of each type, so
these commands address a service by its type rather than by an ID.

Use these commands to list a database's services, inspect one, or
remove one.

Example:
  pgedge starfleet managed database service list <database_id>
  pgedge starfleet managed database service get <database_id> mcp`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseServiceListCmd(rt),
		newDatabaseServiceGetCmd(rt),
		newDatabaseServiceRemoveCmd(rt),
	)
	return cmd
}

// --- list ---

func newDatabaseServiceListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list <database_id>",
		Short: "List services deployed on a database",
		Long: `list shows the services deployed alongside a managed
database.

Use it to see which service types are deployed and what state they
are in. The argument takes a full UUID.

Example:
  pgedge starfleet managed database service list <database_id>
  pgedge starfleet managed database service list <database_id> -o json`,
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

			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(db.Services, nil)
			}

			if db.Services == nil || len(*db.Services) == 0 {
				fmt.Fprintln(rt.Stderr, "No services found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*db.Services))
			for _, svc := range *db.Services {
				rows = append(rows, serviceRow(svc))
			}
			return rt.Output.Print(rows, serviceColumns)
		},
	}
}

// --- get ---

func newDatabaseServiceGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <database_id> <type>",
		Short: "Show details of a service",
		Long: `get shows the details of one service on a managed database.

The service is named by type — mcp, rag or postgrest — because a
managed database carries at most one of each. The database argument
takes a full UUID.

Example:
  pgedge starfleet managed database service get <database_id> mcp
  pgedge starfleet managed database service get <database_id> rag -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svcType, err := parseServiceType(args[1])
			if err != nil {
				return err
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

			svc := findService(db, svcType)
			if svc == nil {
				return newExitError(fmt.Sprintf(
					"no %q service deployed on database %s",
					args[1], args[0]), ExitNotFound)
			}

			if rt.Output.Structured() {
				return rt.Output.Print(svc, nil)
			}
			rows := []output.Row{serviceRow(*svc)}
			return rt.Output.Print(rows, serviceColumns)
		},
	}
}

// --- remove ---

func newDatabaseServiceRemoveCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <database_id> <type>",
		Short: "Remove a service type from a database",
		Long: `remove tears down a service type (mcp, rag or postgrest)
from a managed database.

Removal is destructive — the service's configuration and
credentials are unrecoverable — so it prompts for confirmation
unless --force is given.

Example:
  pgedge starfleet managed database service remove <database_id> mcp
  pgedge starfleet managed database service remove <database_id> rag --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID := args[0]
			svcType, err := parseServiceType(args[1])
			if err != nil {
				return err
			}
			// Both parses precede the prompt: neither costs a request,
			// so a mistyped service type or a malformed ID is refused
			// rather than confirmed and then refused.
			id, err := parseUUIDArg(dbID, "database ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Remove %q service from database %s? This cannot be "+
					"undone; its configuration and credentials are "+
					"unrecoverable.", args[1], id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
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

			if findService(db, svcType) == nil {
				return newExitError(fmt.Sprintf(
					"no %q service deployed on database %s",
					args[1], dbID), ExitNotFound)
			}

			remaining := make([]api.ServiceConfig, 0)
			if db.Services != nil {
				for _, svc := range *db.Services {
					if svc.ServiceType != svcType {
						remaining = append(remaining, svc)
					}
				}
			}

			return applyServices(rt, client, db, remaining,
				fmt.Sprintf("Service type %q removed", args[1]), dbID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}
