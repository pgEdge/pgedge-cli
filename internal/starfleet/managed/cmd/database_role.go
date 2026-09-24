package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// canonicalRoles is rotate-password's --role vocabulary, in display
// order.
var canonicalRoles = []string{"admin", "app", "app_read_only"}

// roleAliases maps every spelling --role accepts to the wire value
// rotate-password takes: the canonicalRoles, plus the long-form aliases
// application and application_read_only.
var roleAliases = map[string]api.RotateManagedDatabaseRolePasswordParamsRoleName{
	"admin":                 api.Admin,
	"app":                   api.App,
	"application":           api.App,
	"app_read_only":         api.AppReadOnly,
	"application_read_only": api.AppReadOnly,
}

// unknownRoleError reports a --role value outside rotate-password's
// vocabulary, exit code ExitUsage, before any round trip is spent.
func unknownRoleError(s string) error {
	return newExitError(fmt.Sprintf(
		"unknown role %q (expected one of: %s)",
		s, strings.Join(canonicalRoles, ", ")), ExitUsage)
}

// parseRole validates a user-supplied role name against the CLI's
// role vocabulary and returns the API's wire value for rotate-password.
func parseRole(
	s string,
) (api.RotateManagedDatabaseRolePasswordParamsRoleName, error) {
	if r, ok := roleAliases[s]; ok {
		return r, nil
	}
	return "", unknownRoleError(s)
}

// userTypeAliases maps every spelling --user-type accepts to the wire
// value the get endpoint's user_type query parameter takes. The CLI
// spellings are rotate-password's role names; the wire spells them
// application and application_read_only, so those are accepted too.
var userTypeAliases = map[string]api.GetManagedDatabaseParamsUserType{
	"admin":                 api.GetManagedDatabaseParamsUserTypeAdmin,
	"app":                   api.GetManagedDatabaseParamsUserTypeApplication,
	"application":           api.GetManagedDatabaseParamsUserTypeApplication,
	"app_read_only":         api.GetManagedDatabaseParamsUserTypeApplicationReadOnly,
	"application_read_only": api.GetManagedDatabaseParamsUserTypeApplicationReadOnly,
}

// unknownUserTypeError reports a --user-type value outside get's
// vocabulary, exit code ExitUsage, before any round trip is spent.
func unknownUserTypeError(s string) error {
	return newExitError(fmt.Sprintf(
		"unknown user type %q (expected one of: %s)",
		s, strings.Join(canonicalRoles, ", ")), ExitUsage)
}

// parseUserType validates a user-supplied --user-type value and
// returns the wire value the get endpoint's user_type query parameter
// expects.
func parseUserType(s string) (api.GetManagedDatabaseParamsUserType, error) {
	if u, ok := userTypeAliases[s]; ok {
		return u, nil
	}
	return "", unknownUserTypeError(s)
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
on a managed database.

Rotation is destructive to existing sessions: anything still
connecting with the old password stops working the moment the new
one takes effect, so it prompts for confirmation unless --force is
given.

The new password is not printed. Read it back with
'database get <id> --user-type <role>', which returns that role's
current credentials in the connection block. Rotating the role a
deployed service connects as also restarts that service.

The argument takes a full UUID.

Example:
  pgedge starfleet managed database rotate-password e5f6a7b8-c9d0-1234-efab-567890123456 --role app
  pgedge starfleet managed database rotate-password e5f6a7b8-c9d0-1234-efab-567890123456 --role admin --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			roleName, err := parseRole(role)
			if err != nil {
				return err
			}

			// Before the prompt: a parse is free, and on a scripted run
			// without --force a prompt refusal would bury the real
			// fault. It also lets TestShippedExamplesAreNotMalformed,
			// which waives the destructive-verb refusal, see a bad ID.
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

			base := captureTaskBaseline(client, id.String())

			// Untyped call: rotate has no typed 2xx case. See
			// checkEmptyBodyResponse.
			resp, err := client.RotateManagedDatabaseRolePassword(
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
			return trackMutation(rt, client, id.String(), base)
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
