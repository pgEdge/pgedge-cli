package cmd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/connstr"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// mcpConnection is the whole output: nothing else from the GET, which
// also carries the database password and the RAG keys, is copied in.
type mcpConnection struct {
	URI   string `json:"uri"`
	Token string `json:"token,omitempty"`
}

func newDatabaseMCPConnectionCmd(rt *module.Runtime) *cobra.Command {
	var (
		branch  string
		noToken bool
	)
	cmd := &cobra.Command{
		Use:   "connection [<database_id>]",
		Short: "Print the MCP endpoint and its bearer token",
		Long: `connection prints the endpoint URL of the MCP server deployed on
a managed database, and the bearer token a client sends to it.

Use it to configure an MCP client. It prints those two values and
nothing else from the database. In a folder linked with 'database
link', the ID can be left out, and a link naming a branch prints the
branch's values.

A branch's MCP server has its own URL and its own token. Pass
--branch with a branch ID to print them; the source database's token
is refused at a branch's endpoint.

The token is a live credential. The CLI warns on stderr when stdout
is a terminal, and --no-token prints the URL alone. Under -o json or
-o yaml the output is an object with uri and token fields, and
--no-token leaves the token field out.

A database or branch with no MCP service, no URL yet or no token
exits 1 with the reason on stderr and prints nothing on stdout.

Example:
  pgedge starfleet managed database mcp connection e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database mcp connection e5f6a7b8-c9d0-1234-efab-567890123456 \
    --branch 6f1c8b2a-2e3d-4c5b-9a1f-7d8e9c0b1a2d -o json
  pgedge starfleet managed database mcp connection --no-token`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var branchID uuid.UUID
			if cmd.Flags().Changed("branch") {
				var err error
				branchID, err = parseUUIDArg(branch, "branch ID")
				if err != nil {
					return err
				}
			}
			id, link, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}
			if branchID == uuid.Nil {
				branchID = linkedBranch(rt, link)
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			owner := "database " + id.String()
			var svc *api.ServiceConfig
			if branchID != uuid.Nil {
				owner = "branch " + branchID.String()
				svc, err = branchMCPService(client, id, branchID)
			} else {
				var db *api.ManagedDatabase
				db, err = fetchDatabaseWith(rt, client, id)
				if err == nil {
					svc = findService(db, api.Mcp)
				}
			}
			if err != nil {
				return err
			}

			mc, err := mcpConnectionFrom(svc, owner, !noToken)
			if err != nil {
				return err
			}
			if !noToken && connstr.StdoutIsTerminal(rt) {
				fmt.Fprintln(rt.Stderr, "Warning: this output carries the "+
					"MCP bearer token. Pass --no-token to leave it out.")
			}
			if rt.Output.Structured() {
				return rt.Output.Print(mc, nil)
			}
			fmt.Fprintf(rt.Output.Out, "URI:    %s\n", output.Sanitize(mc.URI))
			if !noToken {
				fmt.Fprintf(rt.Output.Out, "Token:  %s\n",
					output.Sanitize(mc.Token))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch ID whose MCP server to print instead of the database's")
	cmd.Flags().BoolVar(&noToken, "no-token", false,
		"Print the URL without the bearer token")
	return cmd
}

func branchMCPService(client *api.ClientWithResponses, id, branchID uuid.UUID,
) (*api.ServiceConfig, error) {
	resp, err := client.GetBranchWithResponse(context.Background(), id,
		branchID, &api.GetBranchParams{})
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, newExitError(fmt.Sprintf("branch %s not found",
			branchID), ExitNotFound)
	}
	return findService(&api.ManagedDatabase{Services: resp.JSON200.Services},
		api.Mcp), nil
}

// mcpConnectionFrom refuses an empty token rather than printing it: a
// client handed one sends "Authorization: Bearer " and reads the 401
// as a broken server.
func mcpConnectionFrom(svc *api.ServiceConfig, owner string,
	withToken bool,
) (*mcpConnection, error) {
	if svc == nil {
		return nil, newExitError(fmt.Sprintf("%s has no MCP service; "+
			"deploy one with 'pgedge starfleet managed database mcp "+
			"deploy'", owner), ExitGeneral)
	}
	uri, _ := svc.Uri.Get()
	if uri == "" {
		return nil, newExitError(fmt.Sprintf("%s's MCP service has no "+
			"URL yet; its domain is not assigned", owner), ExitGeneral)
	}
	mc := &mcpConnection{URI: uri}
	if !withToken {
		return mc, nil
	}
	if svc.McpConfig != nil && svc.McpConfig.InitTokens != nil {
		mc.Token = *svc.McpConfig.InitTokens
	}
	if mc.Token == "" {
		return nil, newExitError(fmt.Sprintf("%s's MCP service returned "+
			"no bearer token", owner), ExitGeneral)
	}
	return mc, nil
}
