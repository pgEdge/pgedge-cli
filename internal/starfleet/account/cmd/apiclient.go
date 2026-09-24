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
// and update. auth0_secret is deliberately absent: create is the only
// command that renders a secret, and it is the only verb whose response
// type can carry one.
var apiClientColumns = []string{
	"ID", "NAME", "DESCRIPTION", "AUTH0 ID", "CREATED", "UPDATED",
}

// "Only create renders a secret" is enforced structurally by saas's
// split response types: POST /clients answers CreateApiClientResponse,
// which carries auth0_secret, and every read answers ApiClient, which
// has no such field at all — a read cannot render a secret because its
// type cannot hold one, and a body that carries the key anyway has it
// discarded at unmarshal.
//
// TestReadResponseTypeCannotCarryASecret is what keeps that true: if a
// re-vendor ever puts the field back on ApiClient, it fails rather
// than quietly restoring the leak.

// NewAPIClientCmd builds the `pgedge starfleet client` command group.
// The plural "clients" is kept as a plural alias (unlisted in help)
// so existing scripts keep working.
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

// newAPIClientCreateCmd builds `client create`, the one command in
// this CLI that renders a credential.
//
// POST /account/v1/clients mints the client secret and returns it once; there
// is no endpoint that can fetch it again. Every branch below therefore
// treats a missing secret as a failure to report loudly rather than a
// success to render quietly, and the secret itself goes to STDOUT
// while all surrounding prose goes to stderr, so
// `secret=$(pgedge starfleet client create ...)` captures exactly the
// secret and nothing else.
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
			// Checked before the format branch, not after: a 2xx with
			// no parseable body means the secret was minted and is
			// already unreachable, which is just as fatal under -o
			// json as under -o text. Rendering `null` and exiting 0
			// would tell a script it had captured a credential.
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
				// The whole body, secret included: this is the only
				// moment the secret exists anywhere, so a scripted
				// caller must be able to capture it.
				return rt.Output.Print(ac, nil)
			}

			fmt.Fprintf(rt.Stderr, "API client %q created (id: %s).\n",
				ac.Name, output.Sanitize(ac.Id))
			if noSecret {
				warnNoClientSecret(rt)
				return nil
			}
			// Secret to STDOUT so it can be redirected or piped; the
			// surrounding prose goes to stderr. Shown once by the API
			// and never again.
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

// warnNoClientSecret reports a created-but-secretless client. The
// client exists and counts against the account, but nothing can
// authenticate as it, so the only remedy is to delete and recreate.
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

			// UpdateApiClientInput's fields are pointers precisely so
			// an omitted flag is omitted from the request rather than
			// sent as "". Overlay only what cobra reports as Changed —
			// reading the variables unconditionally would blank the
			// other field on every update.
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

			// PATCH answers with the same ApiClient schema POST does,
			// so the strip is not optional here.
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

			// This calls the untyped generated operation, DeleteClient,
			// and NOT its DeleteClientWithResponse wrapper. Do not
			// "restore" the WithResponse call — it reports a successful
			// delete as a failure.
			//
			// saas's handler is
			// `return ctx.JSON(http.StatusNoContent, nil)`
			// (internal/starfleet/api/clients.go:118, at the pinned SHA
			// in openapi/SOURCE and at saas HEAD). echo's
			// (*context).json writes the json Content-Type before
			// setting the status, and net/http suppresses only
			// Content-Length and Transfer-Encoding on a 204 — not
			// Content-Type. The wire shape is therefore 204 +
			// `Content-Type: application/json` + a 0-byte body.
			//
			// ParseDeleteClientResponse ends in a
			// `Content-Type contains "json" && true` catch-all that
			// unmarshals the body into the spec's Error model for ANY
			// status, 2xx included. json.Unmarshal of 0 bytes fails, so
			// the wrapper returns "unexpected end of JSON input" and
			// discards the 204 entirely.
			//
			// Bypassing that one response parser keeps the generated
			// request builder and the generated URL/param handling in
			// play; only the parse step is replaced. This is the same
			// bypass, for the same catch-all, that conn.Exchange applies
			// to the token endpoint (internal/starfleet/conn/conn.go).
			//
			// DeleteClient is the only ctx.JSON(204, …) in starfleet —
			// every other no-content handler uses RespondNoContent ->
			// ctx.NoContent, which sets no Content-Type — which is why
			// invite delete, membership delete and the byoc deletes do
			// not need this.
			resp, err := client.DeleteClient(context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete client: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read delete client response: %w", err)
			}
			// checkResponse accepts any 2xx, so this is correct whether
			// the server sends that Content-Type header or not.
			if err := checkResponse(resp.StatusCode,
				string(body)); err != nil {
				return err
			}

			// A 204 carries no body, and the operation has no success
			// schema at all, so there is nothing to render in any
			// format.
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

// apiClientRowFrom adapts an api.ApiClient into a table row. That type
// carries no secret field, so a column added later could not tabulate
// one even by mistake. create is the only command that renders a
// secret, it reads CreateApiClientResponse rather than ApiClient, and
// it does not build a row.
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
