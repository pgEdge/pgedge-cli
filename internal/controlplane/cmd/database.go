package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

var databaseColumns = []string{"ID", "STATE", "CREATED", "UPDATED"}

type databaseRow struct{ id, state, created, updated string }

func (r databaseRow) Columns() []string {
	return []string{r.id, output.ColorStatus(r.state),
		r.created, r.updated}
}

// NewDatabaseCmd builds `pgedge controlplane database`.
func NewDatabaseCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"databases", "db"},
		Short:   "Manage Control Plane databases",
		Long: `database lists, inspects, creates, updates, and deletes
the distributed Postgres databases managed by the control-plane.

Example:
  pgedge controlplane database list
  pgedge controlplane database get storefront
  pgedge controlplane database delete storefront --force`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseListCmd(rt),
		newDatabaseGetCmd(rt),
		newDatabaseInitCmd(rt),
		newDatabaseCreateCmd(rt),
		newDatabaseUpdateCmd(rt),
		newDatabaseDeleteCmd(rt),
		newDatabaseUpgradeCmd(rt),
		newDatabaseRestoreCmd(rt),
		NewDatabaseNodeCmd(rt),
		NewDatabaseInstanceCmd(rt),
	)
	return cmd
}

func newDatabaseListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List databases",
		Long: `list shows every database managed by the control-plane.

Example:
  pgedge controlplane database list
  pgedge controlplane database list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.ListDatabasesWithResponse(
				context.Background(), &api.ListDatabasesParams{})
			if err != nil {
				return networkError("list databases", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			if resp.JSON200 == nil ||
				len(resp.JSON200.Databases) == 0 {
				fmt.Fprintln(rt.Stderr, "No databases found.")
				return nil
			}
			rows := make([]output.Row, 0,
				len(resp.JSON200.Databases))
			for _, d := range resp.JSON200.Databases {
				rows = append(rows, databaseRow{
					id:      d.Id,
					state:   string(d.State),
					created: formatDate(d.CreatedAt),
					updated: formatDate(d.UpdatedAt),
				})
			}
			return rt.Output.Print(rows, databaseColumns)
		},
	}
}

func newDatabaseGetCmd(rt *module.Runtime) *cobra.Command {
	var upgrades bool
	cmd := &cobra.Command{
		Use:   "get <database_id>",
		Short: "Show database details",
		Long: `get shows a single database: its state, its instances
(node, host, role, Postgres/Spock versions, and connection info),
its running services with their readiness, health, image,
addresses and ports, and then a Details block (the database's
tenant, then the spec's name, versions, CPUs, memory and ports)
followed by the rest of the spec section by section: nodes, users,
configured services, backup repositories and schedules, and
restore config. A section the database has nothing for is omitted.

Text mode never prints a credential: a user's password, a
service's config, and a repository's keys are left out, and the
Control Plane strips the keys from its responses in any case.
The pg_hba, pg_ident and postgresql.conf entries, scripts and
orchestrator options are in -o yaml and -o json only.

--upgrades also asks for the newer images this database can be
upgraded to and lists them under "Available upgrades". The list
is what 'database upgrade --image' takes.

-o yaml and -o json both emit the whole database, including its
spec. 'database update -f' reads the spec section out of that
document, so the output round-trips without being restructured
first.

Example:
  pgedge controlplane database get storefront
  pgedge controlplane database get storefront --upgrades
  pgedge controlplane database get storefront -o yaml > spec.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			params := &api.GetDatabaseParams{}
			if upgrades {
				params.Include = &[]string{"available_upgrades"}
			}
			resp, err := client.GetDatabaseWithResponse(
				context.Background(), args[0], params)
			if err != nil {
				return networkError("get database", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			d := resp.JSON200
			if d == nil {
				fmt.Fprintln(rt.Stderr, "No database data returned.")
				return nil
			}
			return printDatabaseDetail(rt, d)
		},
	}
	cmd.Flags().BoolVar(&upgrades, "upgrades", false,
		"Also list the newer images this database can upgrade to")
	return cmd
}

var (
	serviceColumns = []string{"SERVICE", "STATE", "HOST", "READY",
		"HEALTH", "IMAGE", "ADDRESSES", "PORTS"}

	instanceErrorColumns = []string{"NODE", "ERROR"}
	serviceErrorColumns  = []string{"SERVICE", "ERROR"}
)

type serviceRow struct {
	service, state, host                   string
	ready, health, image, addresses, ports string
}

func (r serviceRow) Columns() []string {
	return []string{r.service, output.ColorStatus(r.state), r.host,
		r.ready, r.health, r.image, r.addresses, r.ports}
}

type errorRow struct{ subject, message string }

func (r errorRow) Columns() []string {
	return []string{r.subject, r.message}
}

// errorRows flattens each message to one line: the Control Plane folds
// Postgres DETAIL lines and command output into these, and a newline in
// a cell breaks every column to its right. -o json keeps it verbatim.
func errorRows(subjects []string, messages []*string) []output.Row {
	rows := make([]output.Row, 0, len(subjects))
	for i, msg := range messages {
		text := output.OneLine(output.DerefString(msg))
		if text == "" {
			continue
		}
		rows = append(rows, errorRow{subject: subjects[i], message: text})
	}
	return rows
}

// printDatabaseDetail renders text mode only; json/yaml marshal the
// raw struct.
//
// The error sections are the only text view of either error field, so
// without them a failed instance reads `STATE=failed` with no reason.
func printDatabaseDetail(rt *module.Runtime, d *api.Database3) error {
	if err := rt.Output.Print([]output.Row{databaseRow{
		id:      d.Id,
		state:   string(d.State),
		created: formatDate(d.CreatedAt),
		updated: formatDate(d.UpdatedAt),
	}}, databaseColumns); err != nil {
		return err
	}

	if d.Instances != nil && len(*d.Instances) > 0 {
		rows := make([]output.Row, 0, len(*d.Instances))
		names := make([]string, 0, len(*d.Instances))
		errs := make([]*string, 0, len(*d.Instances))
		for _, inst := range *d.Instances {
			rows = append(rows, instanceRow{
				f: newInstanceFields(d.Id, inst)})
			names = append(names, inst.NodeName)
			errs = append(errs, inst.Error)
		}
		fmt.Fprintf(rt.Output.Out, "\nInstances\n")
		if err := rt.Output.Print(rows, instanceColumns); err != nil {
			return err
		}
		if err := printSection(rt, "Instance errors",
			instanceErrorColumns, errorRows(names, errs)); err != nil {
			return err
		}
	}

	if d.ServiceInstances != nil && len(*d.ServiceInstances) > 0 {
		rows := make([]output.Row, 0, len(*d.ServiceInstances))
		names := make([]string, 0, len(*d.ServiceInstances))
		errs := make([]*string, 0, len(*d.ServiceInstances))
		for _, svc := range *d.ServiceInstances {
			row := serviceRow{
				service: svc.ServiceId,
				state:   string(svc.State),
				host:    svc.HostId,
			}
			row.ready, row.health, row.image, row.addresses, row.ports =
				serviceStatusCells(svc.Status)
			rows = append(rows, row)
			names = append(names, svc.ServiceId)
			errs = append(errs, svc.Error)
		}
		fmt.Fprintf(rt.Output.Out, "\nServices\n")
		if err := rt.Output.Print(rows, serviceColumns); err != nil {
			return err
		}
		if err := printSection(rt, "Service errors",
			serviceErrorColumns, errorRows(names, errs)); err != nil {
			return err
		}
	}
	if err := printSpecSections(rt, d); err != nil {
		return err
	}
	return printAvailableUpgrades(rt, d)
}

func newDatabaseDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <database_id>",
		Short: "Delete a database",
		Long: `delete removes a database and all its instances. This is
destructive and irreversible. Use --force to skip the prompt,
--force-unmodifiable to delete even in an unmodifiable state, and
--wait/--follow to track the delete task.

Example:
  pgedge controlplane database delete storefront
  pgedge controlplane database delete storefront --force --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	var forceUnmodifiable bool
	cmd.Flags().BoolVar(&forceUnmodifiable, "force-unmodifiable", false,
		"Delete even if the database is in an unmodifiable state")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cli.Confirm(rt,
			fmt.Sprintf("Delete database %s?", args[0]), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		params := &api.DeleteDatabaseParams{}
		if forceUnmodifiable {
			f := true
			params.Force = &f
		}
		resp, err := client.DeleteDatabaseWithResponse(
			context.Background(), args[0], params)
		if err != nil {
			return networkError("delete database", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return nil
		}
		return announceAndWait(
			rt, client, wf, "Delete", args[0],
			resp.JSON200.Task, resp.JSON200)
	}
	cli.MarkMutating(cmd)

	return cmd
}
