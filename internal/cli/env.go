package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/projectlink"
)

// NewEnvCmd builds `pgedge env`, whose one verb runs the linked
// module's own `env pull`.
func NewEnvCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Write a linked database's connection into .env",
		Long: `env writes the connection of the database a folder is linked to
into the project's .env file.

Example:
  pgedge env pull`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newEnvPullCmd(rt))
	return cmd
}

func newEnvPullCmd(_ *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Write DATABASE_URL for the linked database into .env",
		Long: `pull finds the folder's .pgedge/link.yaml, here or in a folder
above, and runs the linked module's own env pull for it:
'pgedge starfleet managed database env pull' for a managed database.
That command's reference describes what is written and where.

It takes no database ID. A folder with no link is exit 2, naming the
command that creates one.

Example:
  pgedge env pull
  pgedge env pull --file .env.local --user-type app_read_only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			f, err := projectlink.Find(wd, home)
			if err != nil {
				return fmt.Errorf("read project link: %w", err)
			}
			if f == nil {
				return &UsageError{Msg: "no " + projectlink.Dir + "/" +
					projectlink.File + " found here or above; link this " +
					"folder with 'pgedge starfleet managed database link " +
					"<database_id>'"}
			}
			path := projectlink.CommandPaths[f.Module]
			target, rest, err := cmd.Root().Find(path)
			if err != nil || len(rest) != 0 || target.CommandPath() !=
				cmd.Root().Name()+" "+strings.Join(path, " ") {
				return fmt.Errorf("%s links a %s database, which this "+
					"build cannot pull", f.Path, f.Module)
			}
			// Merges the ancestors' persistent flags into target's
			// set, which cobra otherwise does only when parsing it.
			if err := target.ParseFlags(nil); err != nil {
				return err
			}
			if err := copyChangedFlags(cmd.Flags(), target.Flags()); err != nil {
				return err
			}
			return target.RunE(target, nil)
		},
	}
	projectlink.AddEnvPullFlags(cmd.Flags())
	return cmd
}

func copyChangedFlags(from, to *pflag.FlagSet) error {
	var err error
	from.Visit(func(f *pflag.Flag) {
		if err == nil && to.Lookup(f.Name) != nil {
			err = to.Set(f.Name, f.Value.String())
		}
	})
	return err
}
