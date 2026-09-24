package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/spf13/cobra"
)

// inviteColumns are the table headers shared by invite list and get.
var inviteColumns = []string{
	"ID", "EMAIL", "INVITED BY", "TEAM", "EXPIRES", "CREATED",
}

// errUserSessionRequired is what `invite create` and `invite accept`
// return instead of a request. Both operations need to know which
// *user* is acting, and the CLI cannot tell the API that.
//
// The API derives identity from the token's claims: a user token
// identifies a user, a client token a client, never both. The CLI's
// only credential is a client ID and secret, so its token is always a
// machine token. CreateInvite answers `400 cannot create invites from
// an api client`, and AcceptInvite answers 401 on the empty user ID. Both were measured live against a dev tenant.
//
// This is the same reason `pgedge starfleet user` does not exist: a
// client-credentials token carries a tenant but never a user, so the API
// rejects it by design rather than by accident. These two verbs
// predate that understanding, so rather than 404-ing a command that
// looks like it should work, they explain the constraint and point at
// the UI.
//
// Revival condition: if the CLI ever gains an interactive user login,
// its token would carry a user and both verbs would start working —
// delete this guard then. TestInviteGuardPremiseStillHolds fails if
// the auth surface grows in a way that suggests that has happened.
func errUserSessionRequired(verb, alternative string) error {
	return newExitError(fmt.Sprintf(
		"%s needs a signed-in user. The CLI authenticates with a "+
			"client ID and secret, which identifies an application "+
			"rather than a person, so pgEdge Starfleet rejects this "+
			"operation whatever the credentials. %s",
		verb, alternative), ExitAuth)
}

// NewInviteCmd builds the `pgedge starfleet invite` command group. The
// plural "invites" is kept as a plural alias (unlisted in help) so
// existing scripts keep working.
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
	return &cobra.Command{
		Use:   "get <invite_id>",
		Short: "Show invite details",
		Long: `get shows the details of a single invite.

Use it to read an invite's email, team, and expiry. The argument
is the invite's UUID.

Example:
  pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet invite get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
}

// --- create ---

// newInviteCreateCmd builds `invite create`, which cannot succeed with
// the credentials the CLI supports — see errUserSessionRequired. The
// flags are still declared so the help text stays honest about what
// the operation takes, and so a future user-login flow only has to
// restore the body (git history has it, from commit fc8cf11's parent).
func newInviteCreateCmd(_ *module.Runtime) *cobra.Command {
	var (
		email      string
		expiration int
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an invite (needs the web UI)",
		Long: `create sends a team invite to an email address.

This needs a signed-in user, so it cannot run with a CLI client ID
and secret: pgEdge Starfleet identifies that as an application rather
than a person and refuses the request. Invite teammates from the
Team page in the pgEdge Starfleet UI instead. Exits 5.

'invite list', 'invite get' and 'invite delete' do work here.

Example:
  pgedge starfleet invite list`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errUserSessionRequired("invite create",
				"Invite teammates from the Team page in the pgEdge "+
					"Starfleet UI.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "Email address to invite")
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

// newInviteAcceptCmd builds `invite accept`, which cannot succeed with
// the credentials the CLI supports — see errUserSessionRequired. Of the
// two guarded verbs this is the more clear-cut: accepting an invite has
// to record *which user* joined, and a machine credential names no user.
func newInviteAcceptCmd(_ *module.Runtime) *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "accept <invite_id>",
		Short: "Accept an invite (needs the web UI)",
		Long: `accept joins the team an invite was issued for.

This needs a signed-in user, so it cannot run with a CLI client ID
and secret: accepting an invite records which person joined, and a
client credential names an application rather than a person. Accept
the invite from the link in your invitation email, or in the pgEdge
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
