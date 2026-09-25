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

func newDatabaseConnectionStringCmd(rt *module.Runtime) *cobra.Command {
	var (
		userType   string
		format     string
		noPassword bool
	)
	cmd := &cobra.Command{
		Use:   "connection-string [<database_id>]",
		Short: "Print a connection string for a managed database",
		Long: `connection-string prints a libpq connection URI for a managed
database, assembled from the connection block database get returns.

Use it to connect an application or a client without extracting the
parts from -o json by hand. The argument takes a full UUID. In a
folder linked with 'database link', the ID can be left out, and a
link naming a branch prints the branch's string.

The string carries the role's password. That is what makes it a
connection string, and it is also a live credential on stdout: the
CLI warns on stderr when stdout is a terminal, and --no-password
leaves the password out for a log, a document or a paste. The
username and password are percent-encoded, so a password holding @,
:, / or ? produces a URI that parses back to what was meant.

--format chooses the shape. uri, the default, prints one line:
postgresql://user:password@host:port/database?sslmode=require. env
prints one PG* variable per line (PGHOST, PGPORT, PGDATABASE, PGUSER,
PGPASSWORD, PGSSLMODE), each single-quoted for a POSIX shell, so the
output can be sourced or written to an env file.

--user-type chooses the role, admin, app or app_read_only, exactly as database get
does; omit it and app's credentials come back. Under -o json or
-o yaml the URI is returned alongside the parts it was built from.

A database whose connection block carries no host yet, which is the
case while it is still being created, is reported on stderr with
exit 1 rather than printed as a string with a hole in it.

Example:
  pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456 \
    --user-type admin --no-password
  pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456 \
    --format env > .env`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := connstr.ValidateFormat(format); err != nil {
				return err
			}
			var wireUserType api.GetManagedDatabaseParamsUserType
			// Changed, not `!= ""`, for the reason database get gives:
			// an explicitly empty value is outside the vocabulary.
			if cmd.Flags().Changed("user-type") {
				if userType == "" {
					return newExitError(
						"--user-type given an empty value: name a role "+
							"(admin, app or app_read_only), or omit the flag to use "+
							"app", ExitUsage)
				}
				var err error
				wireUserType, err = parseUserType(userType)
				if err != nil {
					return err
				}
			}

			id, link, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			var cs *connstr.String
			if link != nil && link.BranchID != "" {
				cs, err = branchConnectionString(client, id,
					uuid.MustParse(link.BranchID), userType, !noPassword)
			} else {
				cs, err = databaseConnectionString(client, id,
					wireUserType, !noPassword)
			}
			if err != nil {
				return err
			}

			// Before the format branch: a live password on a terminal
			// is the same exposure under -o json as under text.
			if !noPassword && connstr.StdoutIsTerminal(rt) {
				fmt.Fprintf(rt.Stderr, "Warning: this output carries "+
					"%s's password. Pass --no-password to leave it out.\n",
					output.Sanitize(cs.Username))
			}
			if rt.Output.Structured() {
				return rt.Output.Print(cs, nil)
			}
			if format == "env" {
				return connstr.PrintEnv(rt.Stdout, cs, !noPassword)
			}
			_, err = fmt.Fprintln(rt.Stdout, cs.URI)
			return err
		},
	}
	cmd.Flags().StringVar(&userType, "user-type", "",
		"Role whose credentials to use: admin, app or app_read_only (default app)")
	cmd.Flags().StringVar(&format, "format", "uri",
		"Shape of the text output: uri or env")
	cmd.Flags().BoolVar(&noPassword, "no-password", false,
		"Leave the password out of the string")
	_ = cmd.RegisterFlagCompletionFunc("format",
		func(*cobra.Command, []string, string) (
			[]string, cobra.ShellCompDirective,
		) {
			return connstr.FormatCompletion(),
				cobra.ShellCompDirectiveNoFileComp
		})
	return cmd
}

// buildConnectionString refuses a connection block with no host, the
// state of a database still being created, and hands the rest to
// connstr.Build, which carries the server bytes verbatim.
func buildConnectionString(
	d *api.ManagedDatabase, withPassword bool,
) (*connstr.String, error) {
	c := d.Connection
	if c == nil || c.Host == nil || *c.Host == "" {
		return nil, newExitError(fmt.Sprintf(
			"database %s has no connection host yet; it is still being "+
				"created, or its status is not available", d.Id),
			ExitGeneral)
	}
	return connstr.Build(*c.Host, c.Port, c.Database, c.Username,
		c.Password, withPassword), nil
}

func databaseConnectionString(client *api.ClientWithResponses, id uuid.UUID,
	userType api.GetManagedDatabaseParamsUserType, withPassword bool,
) (*connstr.String, error) {
	params := &api.GetManagedDatabaseParams{}
	if userType != "" {
		params.UserType = &userType
	}
	resp, err := client.GetManagedDatabaseWithResponse(
		context.Background(), id, params)
	if err != nil {
		return nil, fmt.Errorf("get database: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, newExitError(
			fmt.Sprintf("database %s not found", id), ExitNotFound)
	}
	return buildConnectionString(resp.JSON200, withPassword)
}

func branchConnectionString(client *api.ClientWithResponses, id,
	branchID uuid.UUID, userType string, withPassword bool,
) (*connstr.String, error) {
	c, err := branchConnection(client, id, branchID, userType)
	if err != nil {
		return nil, err
	}
	if c == nil || c.Host == nil || *c.Host == "" {
		return nil, newExitError(fmt.Sprintf(
			"branch %s has no connection host yet; it is still being "+
				"created, or its status is not available", branchID),
			ExitGeneral)
	}
	return connstr.Build(*c.Host, c.Port, c.Database, c.Username,
		c.Password, withPassword), nil
}
