package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// regionColumns is the single-column table `region list` prints. The
// API returns objects rather than bare strings, so a one-column table
// keeps text output consistent with every other list verb.
var regionColumns = []string{"REGION"}

// NewRegionCmd builds the `pgedge starfleet managed region` command group.
func NewRegionCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "region",
		Aliases: []string{"regions"},
		Short:   "Discover pgEdge Managed regions",
		Long: `region lists the regions managed databases can be created
in.

These are the accepted values for 'database create --region'. The
list is what pgEdge currently operates, so it is worth reading rather
than guessing — a region that is not on it is rejected server-side.

Example:
  pgedge starfleet managed region list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newRegionListCmd(rt))
	return cmd
}

func newRegionListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available regions",
		Long: `list shows every region a managed database can be created
in.

Example:
  pgedge starfleet managed region list
  pgedge starfleet managed region list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListManagedRegionsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list regions: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			regions := resp.JSON200
			if regions == nil || len(*regions) == 0 {
				fmt.Fprintln(rt.Stderr, "No regions found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*regions))
			for _, r := range *regions {
				rows = append(rows, regionRow{region: r.Region})
			}
			return rt.Output.Print(rows, regionColumns)
		},
	}
}

// --- row adapter ---

type regionRow struct{ region string }

func (r regionRow) Columns() []string { return []string{r.region} }
