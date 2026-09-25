package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/dotenv"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/projectlink"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/connstr"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// findLink is the link that applies to the working directory, or nil.
func findLink() (*projectlink.Found, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	f, err := projectlink.Find(wd, home)
	if err != nil {
		return nil, newExitError(fmt.Sprintf("read project link: %v", err), ExitGeneral)
	}
	return f, nil
}

// databaseArg returns args[i] as a database ID, or the linked
// database's when args holds no i-th argument. Only read verbs call
// it: a write must name its database, so an unset variable in a script
// fails instead of reaching whatever the folder is linked to.
func databaseArg(rt *module.Runtime, args []string, i int) (
	uuid.UUID, *projectlink.Found, error,
) {
	if len(args) > i {
		id, err := parseUUIDArg(args[i], "database ID")
		return id, nil, err
	}
	f, err := findLink()
	if err != nil {
		return uuid.UUID{}, nil, err
	}
	if f == nil {
		return uuid.UUID{}, nil, &cli.UsageError{Msg: "no database ID " +
			"given and no " + projectlink.Dir + "/" + projectlink.File +
			" found; pass the ID, or link this folder with " +
			"'pgedge starfleet managed database link <database_id>'"}
	}
	if f.Module != projectlink.ModuleManaged {
		return uuid.UUID{}, nil, newExitError(fmt.Sprintf(
			"%s links a %s database, not a managed one; pass the ID",
			f.Path, f.Module), ExitGeneral)
	}
	fmt.Fprintf(rt.Stderr, "Using database %s from %s\n",
		output.Sanitize(f.DatabaseID), output.Sanitize(f.Path))
	return uuid.MustParse(f.DatabaseID), f, nil
}

// projectFolder is the working directory, refused when it is the home
// folder: ~/.pgedge is the shared config root, and Find never reads a
// link there.
func projectFolder() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if projectlink.IsHome(wd) {
		return "", newExitError("the home folder cannot be linked, because "+
			"~/.pgedge is the shared pgEdge config folder; run this from a "+
			"project folder", ExitUsage)
	}
	return wd, nil
}

// --- link ---

func newDatabaseLinkCmd(rt *module.Runtime) *cobra.Command {
	var (
		branch string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "link <database_id>",
		Short: "Link the current folder to a managed database",
		Long: `link writes .pgedge/link.yaml in the current folder, naming the
database the folder's project uses. The file holds the database ID,
and the branch ID with --branch, and nothing secret, so it can be
committed for everyone who clones the project.

Inside a linked folder, and any folder below it, 'pgedge env pull'
writes the database's DATABASE_URL into .env, and the read verbs
(get, connection-string, inspect, logs, metrics, allowlist get and
branch list) take the linked database when the ID is left out. A
verb that changes a database always needs its ID.

The database, and the branch with --branch, are read first, so an ID
the active profile cannot see is refused and no file is written. A
folder already linked to something else is refused unless --force is
given.

Example:
  pgedge starfleet managed database link e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database link e5f6a7b8-c9d0-1234-efab-567890123456 \
    --branch 0a1b2c3d-4e5f-6789-abcd-ef0123456789`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			var branchID uuid.UUID
			if cmd.Flags().Changed("branch") {
				if branchID, err = parseUUIDArg(branch, "branch ID"); err != nil {
					return err
				}
			}
			wd, err := projectFolder()
			if err != nil {
				return err
			}
			l := projectlink.Link{
				Module:     projectlink.ModuleManaged,
				DatabaseID: id.String(),
			}
			if branchID != uuid.Nil {
				l.BranchID = branchID.String()
			}

			existing, err := projectlink.Read(wd)
			if err != nil && !force {
				return newExitError(fmt.Sprintf("read project link: %v; "+
					"pass --force to replace it", err), ExitGeneral)
			}
			if existing != nil && existing.Link != l && !force {
				return newExitError(fmt.Sprintf(
					"%s already links database %s; pass --force to replace it",
					existing.Path, existing.DatabaseID), ExitGeneral)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			name, err := linkTargetName(client, id, branchID)
			if err != nil {
				return err
			}
			path, err := projectlink.Write(wd, l)
			if err != nil {
				return newExitError(fmt.Sprintf("write %s: %v",
					filepath.Join(wd, projectlink.Dir, projectlink.File), err), ExitGeneral)
			}
			fmt.Fprintf(rt.Stderr, "Linked %s to %s. Wrote %s.\n",
				output.Sanitize(wd), output.Sanitize(name), output.Sanitize(path))
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Link a branch of the database instead of the database itself")
	cmd.Flags().BoolVar(&force, "force", false,
		"Replace a link to another database")
	return cmd
}

// linkTargetName reads the database, and the branch when one is
// given, and names what was found for the acknowledgement.
func linkTargetName(client *api.ClientWithResponses, id, branchID uuid.UUID) (string, error) {
	resp, err := client.GetManagedDatabaseWithResponse(
		context.Background(), id, &api.GetManagedDatabaseParams{})
	if err != nil {
		return "", fmt.Errorf("get database: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", newExitError(fmt.Sprintf("database %s not found", id), ExitNotFound)
	}
	name := fmt.Sprintf("database %s (%s)", resp.JSON200.Name, id)
	if branchID == uuid.Nil {
		return name, nil
	}
	b, err := client.GetBranchWithResponse(
		context.Background(), id, branchID, &api.GetBranchParams{})
	if err != nil {
		return "", fmt.Errorf("get branch: %w", err)
	}
	if err := checkResponse(b.StatusCode(), string(b.Body)); err != nil {
		return "", err
	}
	return fmt.Sprintf("branch %s of %s", branchID, name), nil
}

// --- unlink ---

func newDatabaseUnlinkCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink",
		Short: "Remove the current folder's database link",
		Long: `unlink removes .pgedge/link.yaml from the current folder, and the
.pgedge folder when nothing else is left in it. It changes nothing in
the database and leaves .env alone.

A folder with no link of its own is exit 1, even when a folder above
it is linked: unlink removes only the link it was run beside.

Example:
  pgedge starfleet managed database unlink`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			wd, err := projectFolder()
			if err != nil {
				return err
			}
			removed, err := projectlink.Remove(wd)
			if err != nil {
				return newExitError(fmt.Sprintf("remove project link: %v", err), ExitGeneral)
			}
			if !removed {
				return newExitError(fmt.Sprintf("%s has no %s/%s to remove",
					wd, projectlink.Dir, projectlink.File), ExitGeneral)
			}
			fmt.Fprintf(rt.Stderr, "Removed the database link from %s.\n",
				output.Sanitize(wd))
			return nil
		},
	}
}

// --- env ---

func newDatabaseEnvCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Write a database's connection into a .env file",
		Long: `env writes a managed database's connection into a project's .env
file, where frameworks and ORMs read DATABASE_URL.

Example:
  pgedge starfleet managed database env pull`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newDatabaseEnvPullCmd(rt))
	return cmd
}

func newDatabaseEnvPullCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [<database_id>]",
		Short: "Write DATABASE_URL for a database into .env",
		Long: `pull sets DATABASE_URL in .env to a connection URI for the
database, and leaves every other line of the file as it was. An
existing DATABASE_URL line is replaced; without one the line is
appended, and a missing file is created readable by you alone.

With no ID, the folder's link names the database, and the branch when
the link names one, and .env is written beside the .pgedge folder.
With an ID, .env is written in the current folder. --file names
another file, and --var another variable.

The value is the role's live password in a URI. Every character a
.env loader could expand or cut, $ and # among them, is
percent-encoded, so the value is written unquoted and every loader
reads the same string. Inside a git work tree, a file git does not
ignore draws a warning on stderr naming the line to add to
.gitignore. The command writes nothing to stdout.

Example:
  pgedge starfleet managed database env pull
  pgedge starfleet managed database env pull e5f6a7b8-c9d0-1234-efab-567890123456 \
    --file .env.local --user-type app_read_only`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvPull(rt, cmd, args)
		},
	}
	projectlink.AddEnvPullFlags(cmd.Flags())
	return cmd
}

func runEnvPull(rt *module.Runtime, cmd *cobra.Command, args []string) error {
	file, _ := cmd.Flags().GetString("file")
	varName, _ := cmd.Flags().GetString("var")
	userType, _ := cmd.Flags().GetString("user-type")
	if cmd.Flags().Changed("user-type") && userType == "" {
		return newExitError("--user-type given an empty value: name a role "+
			"(admin, app or app_read_only), or omit the flag to use app", ExitUsage)
	}
	if userType != "" {
		if _, err := parseUserType(userType); err != nil {
			return err
		}
	}
	if !dotenv.ValidName(varName) {
		return newExitError(fmt.Sprintf("--var %q is not a variable name: "+
			"use letters, digits and underscores, not starting with a digit",
			varName), ExitUsage)
	}
	if cmd.Flags().Changed("file") && file == "" {
		return newExitError("--file given an empty value: name a file, "+
			"or omit the flag to use .env", ExitUsage)
	}

	id, link, err := databaseArg(rt, args, 0)
	if err != nil {
		return err
	}
	if file == "" {
		file = ".env"
		if link != nil {
			file = filepath.Join(link.Root, ".env")
		}
	}

	client, err := clientFromCmd(rt, cmd)
	if err != nil {
		return err
	}
	var c *api.ManagedConnection
	what := fmt.Sprintf("database %s", id)
	if link != nil && link.BranchID != "" {
		what = fmt.Sprintf("branch %s", link.BranchID)
		c, err = branchConnection(client, id, uuid.MustParse(link.BranchID), userType)
	} else {
		c, err = databaseConnection(client, id, userType)
	}
	if err != nil {
		return err
	}
	if c == nil || c.Host == nil || *c.Host == "" {
		return newExitError(fmt.Sprintf("%s has no connection host yet; it "+
			"is still being created, or its status is not available", what), ExitGeneral)
	}
	cs := connstr.Build(*c.Host, c.Port, c.Database, c.Username, c.Password, true)
	if err := dotenv.Set(file, varName, connstr.EnvFileURI(cs)); err != nil {
		return newExitError(fmt.Sprintf("write %s: %v", file, err), ExitGeneral)
	}

	role := userType
	if role == "" {
		role = "app"
	}
	fmt.Fprintf(rt.Stderr, "Set %s in %s to %s's connection, as %s.\n",
		output.Sanitize(varName), output.Sanitize(file),
		output.Sanitize(what), output.Sanitize(role))
	switch gitIgnoreState(file) {
	case tracked:
		fmt.Fprintf(rt.Stderr, "Warning: git tracks %s, which now holds a "+
			"password. Run 'git rm --cached %s', then add it to .gitignore.\n",
			output.Sanitize(file), output.Sanitize(filepath.Base(file)))
	case notIgnored:
		fmt.Fprintf(rt.Stderr, "Warning: git does not ignore %s, which now "+
			"holds a password. Add %s to .gitignore.\n",
			output.Sanitize(file), output.Sanitize(filepath.Base(file)))
	}
	return nil
}

func databaseConnection(client *api.ClientWithResponses, id uuid.UUID, userType string) (
	*api.ManagedConnection, error,
) {
	params := &api.GetManagedDatabaseParams{}
	if userType != "" {
		u, _ := parseUserType(userType)
		params.UserType = &u
	}
	resp, err := client.GetManagedDatabaseWithResponse(context.Background(), id, params)
	if err != nil {
		return nil, fmt.Errorf("get database: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, newExitError(fmt.Sprintf("database %s not found", id), ExitNotFound)
	}
	return resp.JSON200.Connection, nil
}

func branchConnection(client *api.ClientWithResponses, id, branchID uuid.UUID, userType string) (
	*api.ManagedConnection, error,
) {
	params := &api.GetBranchParams{}
	if userType != "" {
		u, _ := parseBranchUserType(userType)
		params.UserType = &u
	}
	resp, err := client.GetBranchWithResponse(context.Background(), id, branchID, params)
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, newExitError(fmt.Sprintf("branch %s not found", branchID), ExitNotFound)
	}
	return resp.JSON200.Connection, nil
}

type ignoreState int

const (
	ignoreUnknown ignoreState = iota
	ignored
	notIgnored
	tracked
)

// gitIgnoreState asks git whether it tracks or ignores path. Anything
// but a clear answer, such as no git or no work tree, is ignoreUnknown
// and draws no warning.
func gitIgnoreState(path string) ignoreState {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ignoreUnknown
	}
	dir := filepath.Dir(abs)
	switch gitExit(dir, "ls-files", "--error-unmatch", "--", abs) {
	case 0:
		return tracked
	case 1:
	default:
		return ignoreUnknown
	}
	switch gitExit(dir, "check-ignore", "-q", "--", abs) {
	case 0:
		return ignored
	case 1:
		return notIgnored
	default:
		return ignoreUnknown
	}
}

// gitExit runs git in dir and returns its exit status, or -1 when git
// could not run or ran past the deadline.
func gitExit(dir string, args ...string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: fixed git subcommands, path is an argument
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		return exitErr.ExitCode()
	default:
		return -1
	}
}
