package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// canonicalRoles are the documented role spellings, in display order.
// roleAliases also accepts the long forms managed's --user-type uses,
// so a value copied from there still works here.
var canonicalRoles = []string{"admin", "app", "app_read_only"}

// roleAliases maps every accepted spelling to the API's wire value,
// which for rotate-password is the short form.
var roleAliases = map[string]api.RotateDatabaseRolePasswordParamsRoleName{
	"admin":                 api.Admin,
	"app":                   api.App,
	"application":           api.App,
	"app_read_only":         api.AppReadOnly,
	"application_read_only": api.AppReadOnly,
}

// parseRole validates a user-supplied role name against the CLI's
// role vocabulary.
func parseRole(
	s string,
) (api.RotateDatabaseRolePasswordParamsRoleName, error) {
	if r, ok := roleAliases[s]; ok {
		return r, nil
	}
	return "", newExitError(fmt.Sprintf(
		"unknown role %q (expected one of: %s)",
		s, strings.Join(canonicalRoles, ", ")), ExitUsage)
}

// --- rotate-password ---

func newDatabaseRotatePasswordCmd(rt *module.Runtime) *cobra.Command {
	var (
		role  string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "rotate-password <database_id>",
		Short: "Rotate a built-in role's password",
		Long: `rotate-password issues a new password for a built-in role
on a BYOC database.

Rotation is destructive to existing sessions: anything still
connecting with the old password stops working the moment the new
one takes effect, so it prompts for confirmation unless --force is
given.

Both the database and its cluster must be in the available state;
the API refuses the rotation otherwise.

The new password is not printed. Read it back with
'database get <id>', which returns the role's current credentials
in the connection block.

The argument takes a full UUID.

Example:
  pgedge starfleet byoc database rotate-password f6a7b8c9-d0e1-2345-fabc-456789012345 --role app
  pgedge starfleet byoc database rotate-password f6a7b8c9-d0e1-2345-fabc-456789012345 --role admin --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			roleName, err := parseRole(role)
			if err != nil {
				return err
			}

			// Before the prompt, so a scripted run without --force
			// reports the bad ID rather than a prompt refusal, and so
			// TestShippedExamplesAreNotMalformed, which waives the
			// prompt refusal, can see a bad ID.
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Rotate the %q password on database %s? Existing "+
					"connections using the old password will fail.",
				role, id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, id.String())
				if err != nil {
					return err
				}
			}

			// Untyped, like the other bodiless verbs: the spec declares
			// a 204, managed answers 204, and byoc answers 200 with the
			// JSON literal `null`.
			// This operation's parser alone has no catch-all, so the
			// typed call would also have survived.
			resp, err := client.RotateDatabaseRolePassword(
				context.Background(), id, roleName)
			if err != nil {
				return fmt.Errorf("rotate role password: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "rotate role password"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr,
				"Password rotated for role %q on database %s.\n",
				role, output.Sanitize(args[0]))
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	f := cmd.Flags()
	f.StringVar(&role, "role", "",
		"Built-in role to rotate: admin, app, or app_read_only")
	f.BoolVar(&force, "force", false, "Skip the confirmation prompt")
	_ = cmd.MarkFlagRequired("role")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}
