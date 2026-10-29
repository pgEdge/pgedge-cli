package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// inviteColumns are the table headers shared by invite list and get.
var inviteColumns = []string{
	"ID", "EMAIL", "INVITED BY", "TEAM", "EXPIRES", "CREATED",
}

// errUserSessionRequired is what `invite accept` returns instead of a
// request. Accepting records which user joined, and the public spec
// declares no accept operation, so the CLI has nothing to send for any
// sign-in. Measured live: AcceptInvite answers 401 on an API client.
func errUserSessionRequired(verb, alternative string) error {
	return newExitError(fmt.Sprintf(
		"%s needs a signed-in user in the pgEdge Starfleet UI. An "+
			"API client identifies an application rather than a "+
			"person, so pgEdge Starfleet refuses it, and the CLI does "+
			"not send this request for a browser sign-in either. %s",
		verb, alternative), ExitAuth)
}

// errInviteCreateNeedsSession is `invite create` refused locally for an
// API client, which the API answers with `400 cannot create invites
// from an api client`. The env pair outranks every profile, so a login
// cannot help until it is unset, even when flags won this time.
func errInviteCreateNeedsSession(rt *module.Runtime) error {
	fix := fmt.Sprintf("run '%s' with no credential flags",
		conn.LoginCommand(rt))
	if os.Getenv(auth.EnvClientID) != "" ||
		os.Getenv(auth.EnvClientSecret) != "" {
		fix = fmt.Sprintf("unset %s and %s, then sign in with "+
			"'pgedge starfleet auth login'", auth.EnvClientID,
			auth.EnvClientSecret)
	}
	return newExitError("invite create needs a browser sign-in: "+fix+
		", or invite teammates from the Team page in the pgEdge "+
		"Starfleet UI. An API client identifies an application rather "+
		"than a person, so pgEdge Starfleet refuses it.", ExitAuth)
}

// NewInviteCmd builds the `pgedge starfleet invite` command group. The
// plural "invites" is an unlisted alias.
func NewInviteCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invite",
		Aliases: []string{"invites"},
		Short:   "Manage pgEdge Starfleet team invites",
		Long: `invite manages the invitations that add people to your
pgEdge Starfleet team.

Use these commands to list, inspect, create, delete, and accept
team invites.

Example:
  pgedge starfleet invite list
  pgedge starfleet invite create --email teammate@example.com`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newInviteListCmd(rt),
		newInviteGetCmd(rt),
		newInviteCreateCmd(rt),
		newInviteDeleteCmd(rt),
		newInviteAcceptCmd(rt),
	)
	return cmd
}

// --- list ---

func newInviteListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List invites",
		Long: `list shows the pending invites on the active account.

Use it to find an invite's ID before running get or delete.

Example:
  pgedge starfleet invite list
  pgedge starfleet invite list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListInvitesWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list invites: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			invites := resp.JSON200
			if invites == nil || len(*invites) == 0 {
				fmt.Fprintln(rt.Stderr, "No invites found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*invites))
			for _, inv := range *invites {
				rows = append(rows, inviteRowFrom(inv))
			}
			return rt.Output.Print(rows, inviteColumns)
		},
	}
}

// --- get ---

func newInviteGetCmd(rt *module.Runtime) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "get [<invite_id>]",
		Short: "Show invite details",
		Long: `get shows the details of a single invite.

Use it to read an invite's email, team, and expiry. The argument
is the invite's UUID.

--email, in place of the argument, finds the pending invites for
that address, compared without regard to case, and prints them as a
list (an array under -o json). It exits 4 when there is none, so a
failed read (any other non-zero code) never looks like "no invite".

Example:
  pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
  pgedge starfleet invite get --email teammate@example.com`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			byEmail := cmd.Flags().Changed("email")
			if (len(args) == 1) == byEmail {
				return newExitError("pass exactly one of an invite ID "+
					"or --email", ExitUsage)
			}
			if byEmail {
				addr, err := emailArg(email)
				if err != nil {
					return err
				}
				return inviteGetByEmail(rt, cmd, addr)
			}
			id, err := cli.ParseUUIDArg(args[0], "invite ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetInviteWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get invite: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			inv := resp.JSON200
			if inv == nil {
				fmt.Fprintln(rt.Stderr, "No invite data returned.")
				return nil
			}
			rows := []output.Row{inviteRowFrom(*inv)}
			return rt.Output.Print(rows, inviteColumns)
		},
	}
	cmd.Flags().StringVar(&email, "email", "",
		"Find the invites for this email address instead of an ID")
	return cmd
}

// inviteGetByEmail filters the full invite list: the API has no
// lookup by address.
func inviteGetByEmail(
	rt *module.Runtime, cmd *cobra.Command, email string,
) error {
	client, err := clientFromCmd(rt, cmd)
	if err != nil {
		return err
	}
	resp, err := client.ListInvitesWithResponse(context.Background())
	if err != nil {
		return fmt.Errorf("list invites: %w", err)
	}
	if err := listReadErr(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}
	if resp.JSON200 == nil || *resp.JSON200 == nil {
		return errNoListBody("invites")
	}
	invites := matchEmail(*resp.JSON200, email,
		func(inv api.Invite) string { return inv.Email })
	if len(invites) == 0 {
		return errNoEmailMatch("invite", email)
	}
	if rt.Output.Structured() {
		return rt.Output.Print(invites, nil)
	}
	rows := make([]output.Row, 0, len(invites))
	for _, inv := range invites {
		rows = append(rows, inviteRowFrom(inv))
	}
	return rt.Output.Print(rows, inviteColumns)
}

// --- create ---

func newInviteCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		email      string
		expiration int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an invite",
		Long: `create sends a team invite to an email address and prints the
invite.

It needs a browser sign-in ('pgedge starfleet auth login' with no
credential flags; unset PGEDGE_CLIENT_ID and PGEDGE_CLIENT_SECRET
first, since they outrank every profile). With an API client it exits
5 without sending the request; invite from the Team page in the
pgEdge Starfleet UI instead.

The token and accept URL let anyone holding them join the team. The
table omits them; -o json and -o yaml print them.

Example:
  pgedge starfleet invite create --email teammate@example.com
  pgedge starfleet invite create --email teammate@example.com --expiration 48`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := emailArg(email)
			if err != nil {
				return err
			}
			body := api.CreateInviteJSONRequestBody{Email: addr}
			if cmd.Flags().Changed("expiration") {
				body.Expiration = &expiration
			}

			f := conn.ReadFlags(cmd.Flags())
			res, err := conn.ResolveCredentials(
				rt, f.ClientID, f.ClientSecret, f.APIURL)
			if err != nil {
				code := ExitAuth
				if conn.IsUsageError(err) {
					code = ExitUsage
				}
				return newExitError(err.Error(), code)
			}
			if !res.Session {
				return errInviteCreateNeedsSession(rt)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.CreateInviteWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create invite: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			if resp.JSON200 == nil {
				fmt.Fprintln(rt.Stderr, "No invite data returned.")
				return nil
			}
			return rt.Output.Print(
				[]output.Row{inviteRowFrom(*resp.JSON200)}, inviteColumns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "Email address to invite")
	_ = cmd.MarkFlagRequired("email")
	f.IntVar(&expiration, "expiration", 0,
		"Invite expiration in hours (optional)")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newInviteDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <invite_id>",
		Short: "Delete an invite",
		Long: `delete revokes a pending invite.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument is the invite's UUID.

Example:
  pgedge starfleet invite delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet invite delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "invite ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete invite %s? This cannot be undone.", id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteInvite(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete invite: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete invite"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Invite %s deleted.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- accept ---

func newInviteAcceptCmd(_ *module.Runtime) *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "accept <invite_id>",
		Short: "Accept an invite (needs the web UI)",
		Long: `accept joins the team an invite was issued for.

This needs a signed-in user in the pgEdge Starfleet UI: accepting an
invite records which person joined. An API client names an
application rather than a person, and the CLI does not send the
request for a browser sign-in either. Accept the invite from the link in your invitation email, or in the pgEdge
Starfleet UI. Exits 5.

Example:
  pgedge starfleet invite list`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return errUserSessionRequired("invite accept",
				"Accept it from the link in your invitation email, or "+
					"in the pgEdge Starfleet UI.")
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Invite token")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type inviteRow struct {
	id, email, invitedBy, team, expires, created string
}

func (r inviteRow) Columns() []string {
	return []string{
		r.id, r.email, r.invitedBy, r.team, r.expires, r.created,
	}
}

// inviteRowFrom adapts an api.Invite into a table row.
func inviteRowFrom(inv api.Invite) inviteRow {
	return inviteRow{
		id:        inv.Id,
		email:     inv.Email,
		invitedBy: output.DerefString(inv.InvitedBy),
		team:      inv.TeamName,
		expires:   output.FormatTime(inv.ExpiresAt),
		created:   output.FormatTime(inv.CreatedAt),
	}
}
