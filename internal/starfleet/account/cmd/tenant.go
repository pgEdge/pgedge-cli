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

// tenantColumns are the table headers shared by tenant list and get.
var tenantColumns = []string{
	"ID", "NAME", "PLAN", "TRIAL", "DOMAIN", "CREATED",
}

// NewTenantCmd builds the `pgedge starfleet tenant` command group. The
// plural "tenants" is kept as a plural alias (unlisted in help) so
// existing scripts keep working. There is no create or delete verb:
// the spec exposes only GET /account/v1/tenants, GET /account/v1/tenants/{id} and
// PATCH /account/v1/tenants/{id}.
func NewTenantCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tenant",
		Aliases: []string{"tenants"},
		Short:   "Manage your pgEdge Starfleet tenant",
		Long: `tenant manages the tenant record for your pgEdge Starfleet
account.

Use these commands to list tenants, inspect one by ID, and rename
one. There is no create or delete verb — the API exposes only read
and rename operations on a tenant.

Example:
  pgedge starfleet tenant list
  pgedge starfleet tenant update b0c1d2e3-f4a5-6789-bcde-890123456789 --name acme`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newTenantListCmd(rt),
		newTenantGetCmd(rt),
		newTenantUpdateCmd(rt),
	)
	return cmd
}

// --- list ---

func newTenantListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tenants",
		Long: `list shows the tenants visible to the active account.

Use it to find a tenant's ID before running get or update.

Example:
  pgedge starfleet tenant list
  pgedge starfleet tenant list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListTenantsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list tenants: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			tenants := resp.JSON200
			if tenants == nil || len(*tenants) == 0 {
				fmt.Fprintln(rt.Stderr, "No tenants found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*tenants))
			for _, ten := range *tenants {
				rows = append(rows, tenantRowFrom(ten))
			}
			return rt.Output.Print(rows, tenantColumns)
		},
	}
}

// --- get ---

func newTenantGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <tenant_id>",
		Short: "Show tenant details",
		Long: `get shows the details of a single tenant.

Use it to read a tenant's name, plan, and domain. The argument is
the tenant's UUID.

Example:
  pgedge starfleet tenant get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet tenant get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "tenant ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetTenantWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get tenant: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			ten := resp.JSON200
			if ten == nil {
				fmt.Fprintln(rt.Stderr, "No tenant data returned.")
				return nil
			}
			rows := []output.Row{tenantRowFrom(*ten)}
			return rt.Output.Print(rows, tenantColumns)
		},
	}
}

// --- update ---

func newTenantUpdateCmd(rt *module.Runtime) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "update <tenant_id>",
		Short: "Rename a tenant",
		Long: `update changes a tenant's name.

UpdateTenantInput carries only Name, so --name is the only flag and
passing none is a usage error (exit 2) — there is nothing to send,
and nothing is. The argument is the tenant's UUID.

Example:
  pgedge starfleet tenant update b0c1d2e3-f4a5-6789-bcde-890123456789 --name acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := cli.ParseUUIDArg(args[0], "tenant ID")
			if err != nil {
				return err
			}

			// UpdateTenantInput has exactly one field, so this overlay
			// has exactly one branch — but it is still an overlay, not
			// an unconditional assignment: reading name unconditionally
			// would send an empty string when --name is omitted instead
			// of failing with "nothing to update".
			body := api.UpdateTenantJSONRequestBody{}
			changed := false
			if cmd.Flags().Changed("name") {
				body.Name = &name
				changed = true
			}
			if !changed {
				return newExitError(
					"nothing to update — pass --name", ExitUsage)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.UpdateTenantWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("update tenant: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			ten := resp.JSON200
			if ten == nil {
				fmt.Fprintln(rt.Stderr, "No tenant data returned.")
				return nil
			}
			rows := []output.Row{tenantRowFrom(*ten)}
			return rt.Output.Print(rows, tenantColumns)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name for the tenant")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type tenantRow struct {
	id, name, plan, trial, domain, created string
}

func (r tenantRow) Columns() []string {
	return []string{
		r.id, r.name, r.plan, r.trial, r.domain, r.created,
	}
}

// tenantRowFrom adapts an api.Tenant into a table row. Domain, Plan
// and ExternalId are all *string and PlanTrial is *bool — Tenant is
// pointer-heavy in a way ApiClient and Invite are not, so every
// optional field goes through output.DerefString or an explicit nil
// check rather than being read directly.
func tenantRowFrom(t api.Tenant) tenantRow {
	trial := ""
	if t.PlanTrial != nil {
		trial = output.BoolYesNo(*t.PlanTrial)
	}
	return tenantRow{
		id:      t.Id,
		name:    t.Name,
		plan:    output.DerefString(t.Plan),
		trial:   trial,
		domain:  output.DerefString(t.Domain),
		created: output.FormatTime(t.CreatedAt),
	}
}
