package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// configVersionColumns are the table headers shared by config-version
// list and get. The image map is deliberately not a column: it is a
// variable-width map that only reads well in json or yaml.
var configVersionColumns = []string{
	"NAME", "PG VERSIONS", "MANAGED EXTENSIONS",
}

// NewConfigVersionCmd builds the `pgedge starfleet byoc config-version` command
// group. The plural "config-versions" is kept as a plural alias
// (unlisted in help) so existing scripts keep working.
func NewConfigVersionCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config-version",
		Aliases: []string{"config-versions"},
		Short:   "Inspect pgEdge BYOC configuration versions",
		Long: `config-version inspects the platform configuration versions
BYOC can deploy.

A configuration version pins the container images a cluster runs and
the Postgres major versions it supports, so these commands turn
'which config version do I ask for?' into something you can read
rather than guess.

Example:
  pgedge starfleet byoc config-version list
  pgedge starfleet byoc config-version get 15.6.0`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newConfigVersionListCmd(rt),
		newConfigVersionGetCmd(rt),
	)
	return cmd
}

// --- list ---

func newConfigVersionListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configuration versions",
		Long: `list shows the configuration versions available to your
account, newest first.

Use it to find the version name to pass to a cluster or database
create, and to check which Postgres majors that version supports.

Example:
  pgedge starfleet byoc config-version list
  pgedge starfleet byoc config-version list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListConfigVersionsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list config versions: %w", err)
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
				fmt.Fprintln(rt.Stderr, "No config versions found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*versions))
			for _, v := range *versions {
				rows = append(rows, configVersionRowFrom(v))
			}
			return rt.Output.Print(rows, configVersionColumns)
		},
	}
}

// --- get ---

func newConfigVersionGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <version>",
		Short: "Show configuration version details",
		Long: `get shows the details of a single configuration version.

Use it to confirm a version exists and read the Postgres majors it
supports. The argument is the version name, such as 15.6.0. Choose
json or yaml output to see the container images it pins.

Example:
  pgedge starfleet byoc config-version get 15.6.0
  pgedge starfleet byoc config-version get 15.6.0 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetConfigVersionWithResponse(
				context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get config version: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			v := resp.JSON200
			if v == nil {
				fmt.Fprintln(rt.Stderr, "No config version data returned.")
				return nil
			}
			rows := []output.Row{configVersionRowFrom(*v)}
			return rt.Output.Print(rows, configVersionColumns)
		},
	}
}

// --- row adapter ---

type configVersionRow struct {
	name, pgVersions, extensions string
}

func (r configVersionRow) Columns() []string {
	return []string{r.name, r.pgVersions, r.extensions}
}

// configVersionRowFrom adapts an api.ConfigVersion into a table row.
// Both list fields are optional AND explicitly null on dev's current
// 15.x line, so each is dereferenced through derefStrings rather than
// indexed directly.
func configVersionRowFrom(v api.ConfigVersion) configVersionRow {
	return configVersionRow{
		name:       v.Name,
		pgVersions: joinStrings(derefStrings(v.SupportedPgVersions)),
		extensions: joinStrings(derefStrings(v.ManagedExtensions)),
	}
}
