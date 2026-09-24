package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// pgVersionColumns is the table `pg-version list` prints. DEFAULT
// renders yes/no through output.BoolYesNo, as every other boolean
// column in the CLI does.
var pgVersionColumns = []string{"VERSION", "DEFAULT"}

// NewPgVersionCmd builds the `pgedge starfleet managed pg-version`
// command group.
func NewPgVersionCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pg-version",
		Aliases: []string{"pg-versions"},
		Short:   "Discover Postgres versions for managed databases",
		Long: `pg-version lists the Postgres major versions a managed
database can be created on.

These are the accepted values for 'database create --pg-version'.
The version is fixed for the life of the database, so it is worth
reading this before creating one.

Example:
  pgedge starfleet managed pg-version list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newPgVersionListCmd(rt))
	return cmd
}

func newPgVersionListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available Postgres versions",
		Long: `list shows every Postgres major version a managed database
can be created on, and which one a create that omits --pg-version
provisions.

The list is platform-level rather than tenant-scoped, and does not
vary by region.

Example:
  pgedge starfleet managed pg-version list
  pgedge starfleet managed pg-version list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListManagedPGVersionsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list Postgres versions: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			versions := resp.JSON200
			if versions == nil || len(*versions) == 0 {
				fmt.Fprintln(rt.Stderr, "No Postgres versions found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*versions))
			for _, v := range *versions {
				rows = append(rows, pgVersionRow{
					version:   v.Version,
					isDefault: output.BoolYesNo(v.Default),
				})
			}
			return rt.Output.Print(rows, pgVersionColumns)
		},
	}
}

// --- row adapter ---

type pgVersionRow struct{ version, isDefault string }

func (r pgVersionRow) Columns() []string {
	return []string{r.version, r.isDefault}
}
