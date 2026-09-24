package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/spf13/cobra"
)

// apiClientColumns are the table headers shared by client list, get
// and update. There is no secret column: only create's response type,
// CreateApiClientResponse, carries auth0_secret. Every other verb
// decodes into ApiClient, which has no such field, so a body carrying
// the key has it discarded at unmarshal.
// TestReadResponseTypeCannotCarryASecret fails if a re-vendor adds the
// field to ApiClient.
var apiClientColumns = []string{
	"ID", "NAME", "DESCRIPTION", "AUTH0 ID", "CREATED", "UPDATED",
}

// NewAPIClientCmd builds the `pgedge starfleet client` command group.
// The plural "clients" is an unlisted alias.
func NewAPIClientCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "client",
		Aliases: []string{"clients"},
		Short:   "Manage pgEdge Starfleet API clients",
		Long: `client manages the machine-to-machine API clients on your
pgEdge Starfleet account.

Use these commands to list API clients, inspect one by ID, and
create, update or delete one. There is no rotate verb: replacing a
client's credentials means creating a new client and deleting the
old one.

Example:
  pgedge starfleet client list
  pgedge starfleet client create --name ci --description "CI runner"`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newAPIClientListCmd(rt),
		newAPIClientGetCmd(rt),
		newAPIClientCreateCmd(rt),
		newAPIClientUpdateCmd(rt),
		newAPIClientDeleteCmd(rt),
	)
	return cmd
}

// --- list ---

func newAPIClientListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List API clients",
		Long: `list shows the API clients on the active account.

Use it to find an API client's ID before running get.

Example:
  pgedge starfleet client list
  pgedge starfleet client list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListClientsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list clients: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			clients := resp.JSON200
			if clients == nil || len(*clients) == 0 {
				fmt.Fprintln(rt.Stderr, "No API clients found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*clients))
			for _, c := range *clients {
				rows = append(rows, apiClientRowFrom(c))
			}
			return rt.Output.Print(rows, apiClientColumns)
		},
	}
}

// --- get ---

func newAPIClientGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <client_id>",
		Short: "Show API client details",
		Long: `get shows the details of a single API client.

Use it to read a client's name, description, and Auth0 ID. The
argument is the client's UUID.

Example:
  pgedge starfleet client get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet client get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "client ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetClientWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get client: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			c := resp.JSON200
			if c == nil {
				fmt.Fprintln(rt.Stderr, "No client data returned.")
				return nil
			}
			rows := []output.Row{apiClientRowFrom(*c)}
			return rt.Output.Print(rows, apiClientColumns)
		},
	}
}

// --- create ---

// newAPIClientCreateCmd builds `client create`, the one command that
// renders a credential. POST /account/v1/clients returns the secret
// once and no endpoint fetches it again, so a missing secret is
// reported loudly, never rendered as success. Under -o text the secret
// alone goes to stdout, so `secret=$(pgedge starfleet client create
// ...)` captures exactly it.
func newAPIClientCreateCmd(rt *module.Runtime) *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API client",
		Long: `create mints a new machine-to-machine API client.

The client secret is returned once, by this command only, and cannot
be fetched again — save it immediately. Under -o text the secret is
the only thing written to stdout (everything else goes to stderr), so
it can be captured or redirected; under -o json and -o yaml the whole
client object is written, secret included.

Both --name and --description are required by the API.

Example:
  pgedge starfleet client create --name ci --description "CI runner"
  pgedge starfleet client create --name ci --description "CI runner" \
    -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.CreateClientWithResponse(
				context.Background(),
				api.CreateClientJSONRequestBody{
					Name:        name,
					Description: description,
				})
			if err != nil {
				return fmt.Errorf("create client: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			ac := resp.JSON200
			// Before the format branch: a 2xx with no parseable body
			// means the secret was minted and is already lost. Under
			// -o json, rendering `null` and exiting 0 would tell a
			// script it had captured a credential.
			if ac == nil {
				return newExitError(
					"client created but the API returned no body — the "+
						"client secret is unrecoverable; delete the "+
						"client and retry", ExitGeneral)
			}

			noSecret := ac.Auth0Secret == nil || *ac.Auth0Secret == ""
			if rt.Output.Structured() {
				if noSecret {
					warnNoClientSecret(rt)
				}
				// Secret included: this is the only moment it exists.
				return rt.Output.Print(ac, nil)
			}

			fmt.Fprintf(rt.Stderr, "API client %q created (id: %s).\n",
				ac.Name, output.Sanitize(ac.Id))
			if noSecret {
				warnNoClientSecret(rt)
				return nil
			}
			fmt.Fprintf(rt.Stderr, "Client ID:     %s\n", output.Sanitize(ac.Auth0Id))
			fmt.Fprint(rt.Stderr, "Client secret: ")
			fmt.Fprintln(rt.Stdout, *ac.Auth0Secret)
			fmt.Fprintln(rt.Stderr,
				"Save the secret now — it is shown once and cannot be "+
					"retrieved again.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Name for the new API client")
	f.StringVar(&description, "description", "",
		"What this client is for")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("description")
	cli.MarkMutating(cmd)

	return cmd
}

// warnNoClientSecret reports a created-but-secretless client: it
// exists, but nothing can authenticate as it.
func warnNoClientSecret(rt *module.Runtime) {
	fmt.Fprintln(rt.Stderr,
		"Warning: the API returned no client secret. It cannot be "+
			"retrieved later — delete this client and create another.")
}

// --- update ---

func newAPIClientUpdateCmd(rt *module.Runtime) *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:   "update <client_id>",
		Short: "Update an API client",
		Long: `update changes an API client's name or description.

Only the flags you pass are sent, so an omitted flag leaves that
field untouched, and passing neither is a usage error (exit 2) that
sends nothing. update never changes credentials: there is no rotate
endpoint, so replacing a secret means creating a new client and
deleting the old one. The argument is the client's UUID.

Example:
  pgedge starfleet client update b0c1d2e3-f4a5-6789-bcde-890123456789 \
    --name ci-runner
  pgedge starfleet client update b0c1d2e3-f4a5-6789-bcde-890123456789 \
    --description "Nightly CI"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "client ID")
			if err != nil {
				return err
			}

			// Only Changed flags: reading both variables would send ""
			// and blank the field the caller left out.
			body := api.UpdateClientJSONRequestBody{}
			changed := false
			if cmd.Flags().Changed("name") {
				body.Name = &name
				changed = true
			}
			if cmd.Flags().Changed("description") {
				body.Description = &description
				changed = true
			}
			if !changed {
				return newExitError(
					"nothing to update — pass --name and/or "+
						"--description", ExitUsage)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.UpdateClientWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("update client: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			c := resp.JSON200
			if c == nil {
				fmt.Fprintln(rt.Stderr, "No client data returned.")
				return nil
			}
			rows := []output.Row{apiClientRowFrom(*c)}
			return rt.Output.Print(rows, apiClientColumns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "New name for the API client")
	f.StringVar(&description, "description", "",
		"New description for the API client")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newAPIClientDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <client_id>",
		Short: "Delete an API client",
		Long: `delete removes an API client and revokes its credentials.

Deletion is destructive, so it prompts for confirmation unless
--force is given. Anything still authenticating with this client's
credentials — including this CLI, if you delete the client it is
configured with — stops working immediately. The argument is the
client's UUID.

Example:
  pgedge starfleet client delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet client delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "client ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete API client %s? Any application using its "+
					"credentials will stop being able to "+
					"authenticate.", args[0])
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// The untyped DeleteClient, not DeleteClientWithResponse:
			// the API answers 204 with `Content-Type: application/json`
			// and a 0-byte body, and ParseDeleteClientResponse's json
			// catch-all unmarshals that into Error for any status, so
			// the wrapper reports a successful delete as "unexpected
			// end of JSON input". This is the checkEmptyBodyResponse
			// bypass, written inline.
			resp, err := client.DeleteClient(context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete client: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read delete client response: %w", err)
			}
			if err := checkResponse(resp.StatusCode,
				string(body)); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "API client %s deleted.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type apiClientRow struct {
	id, name, description, auth0ID, created, updated string
}

func (r apiClientRow) Columns() []string {
	return []string{
		r.id, r.name, r.description, r.auth0ID, r.created, r.updated,
	}
}

// apiClientRowFrom adapts an api.ApiClient into a table row.
func apiClientRowFrom(c api.ApiClient) apiClientRow {
	return apiClientRow{
		id:          c.Id,
		name:        c.Name,
		description: c.Description,
		auth0ID:     c.Auth0Id,
		created:     output.FormatTime(c.CreatedAt),
		updated:     output.FormatTime(c.UpdatedAt),
	}
}
