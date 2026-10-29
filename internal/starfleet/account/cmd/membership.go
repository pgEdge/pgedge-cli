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

// membershipColumns are the table headers for membership list.
var membershipColumns = []string{
	"ID", "USER NAME", "USER EMAIL", "OWNER", "CREATED",
}

// NewMembershipCmd builds the `pgedge starfleet membership` command
// group. The plural "memberships" is an unlisted alias.
func NewMembershipCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "membership",
		Aliases: []string{"memberships"},
		Short:   "Manage pgEdge Starfleet team memberships",
		Long: `membership manages the members of your pgEdge Starfleet team.

Use these commands to list team members and remove a member from
the team.

Example:
  pgedge starfleet membership list
  pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newMembershipListCmd(rt),
		newMembershipDeleteCmd(rt),
	)
	return cmd
}

// --- list ---

func newMembershipListCmd(rt *module.Runtime) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List team memberships",
		Long: `list shows the members of the active team.

Use it to find a membership's ID before removing a member. The
OWNER column marks the member who owns the tenant.

--email keeps only the members with that address, compared without
regard to case. It exits 4 when no member has it, so a failed read
(any other non-zero code) never looks like "not a member".

Example:
  pgedge starfleet membership list
  pgedge starfleet membership list -o json
  pgedge starfleet membership list --email teammate@example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			byEmail := cmd.Flags().Changed("email")
			if byEmail {
				var err error
				if email, err = emailArg(email); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListMembershipsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list memberships: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if byEmail {
				if resp.JSON200 == nil || *resp.JSON200 == nil {
					return errNoListBody("memberships")
				}
				members := matchEmail(*resp.JSON200, email,
					func(m api.Membership) string { return m.UserEmail })
				if len(members) == 0 {
					return errNoEmailMatch("member", email)
				}
				resp.JSON200 = &members
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			members := resp.JSON200
			if members == nil || len(*members) == 0 {
				fmt.Fprintln(rt.Stderr, "No memberships found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*members))
			for _, m := range *members {
				rows = append(rows, membershipRowFrom(m))
			}
			return rt.Output.Print(rows, membershipColumns)
		},
	}
	cmd.Flags().StringVar(&email, "email", "",
		"Show only the members with this email address")
	return cmd
}

// --- delete ---

func newMembershipDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <membership_id>",
		Short: "Remove a team member",
		Long: `delete removes a member from your team.

Removal is destructive — the user loses access to the team's
resources — so it prompts for confirmation unless --force is
given. The argument is the membership's UUID.

Example:
  pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet membership delete b0c1d2e3-f4a5-6789-bcde-890123456789 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "membership ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf("Remove team member %s?", id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteMembership(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete membership: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete membership"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Membership %s deleted.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type membershipRow struct {
	id, userName, userEmail, owner, created string
}

func (r membershipRow) Columns() []string {
	return []string{r.id, r.userName, r.userEmail, r.owner, r.created}
}

// membershipRowFrom adapts an api.Membership into a table row.
func membershipRowFrom(m api.Membership) membershipRow {
	return membershipRow{
		id:        m.Id,
		userName:  m.UserName,
		userEmail: m.UserEmail,
		owner:     output.BoolYesNo(m.IsOwner),
		created:   output.FormatTime(m.CreatedAt),
	}
}
