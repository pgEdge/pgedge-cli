package cmd

import (
	"context"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// ingressServiceColumns are the table headers for ingress service list.
var ingressServiceColumns = []string{"SERVICE ID", "DATABASE ID", "URL"}

// NewIngressServiceCmd builds the `pgedge starfleet byoc ingress service`
// command group, which manages the services registered on an ingress.
// It stays a singular child of ingress.
func NewIngressServiceCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services registered on an ingress",
		Long: `service manages the database services exposed through an
ingress.

Use these commands to list the services registered on an ingress,
register a new one, or deregister an existing one.

Example:
  pgedge starfleet byoc ingress service list <ingress_id>
  pgedge starfleet byoc ingress service register <ingress_id> \
    --database-id <id> --service-id <id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newIngressServiceListCmd(rt),
		newIngressServiceRegisterCmd(rt),
		newIngressServiceDeregisterCmd(rt),
	)
	return cmd
}

// --- list ---

func newIngressServiceListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list <ingress_id>",
		Short: "List services registered on an ingress",
		Long: `list shows the services registered on an ingress.

Use it to find a service's ID before deregistering it. The
argument is the ingress's UUID.

Example:
  pgedge starfleet byoc ingress service list <ingress_id>
  pgedge starfleet byoc ingress service list <ingress_id> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "ingress ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListIngressServicesWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("list ingress services: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			svcs := resp.JSON200
			if svcs == nil || len(*svcs) == 0 {
				fmt.Fprintln(rt.Stderr, "No services found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*svcs))
			for _, svc := range *svcs {
				rows = append(rows, ingressServiceRowFrom(svc))
			}
			return rt.Output.Print(rows, ingressServiceColumns)
		},
	}
}

// --- register ---

func newIngressServiceRegisterCmd(rt *module.Runtime) *cobra.Command {
	var (
		databaseID string
		serviceID  string
	)
	cmd := &cobra.Command{
		Use:   "register <ingress_id>",
		Short: "Register a service on an ingress",
		Long: `register exposes a database service through an ingress.

Use it to make a running service reachable at the ingress's
domain. The argument is the ingress's UUID; both --database-id and
--service-id are required.

Example:
  pgedge starfleet byoc ingress service register <ingress_id> \
    --database-id <id> --service-id <id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "ingress ID")
			if err != nil {
				return err
			}

			dbID, err := parseUUIDArg(databaseID, "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateIngressServiceJSONRequestBody{
				DatabaseId: openapi_types.UUID(dbID),
				ServiceId:  serviceID,
			}

			resp, err := client.CreateIngressServiceWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("register ingress service: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			svc := resp.JSON200
			if rt.Output.Structured() {
				return rt.Output.Print(svc, nil)
			}
			if svc == nil {
				fmt.Fprintln(rt.Stderr,
					"Service registered (no details returned).")
				return nil
			}
			fmt.Fprintf(rt.Stderr,
				"Service %q registered on ingress %s (url: %s).\n",
				svc.ServiceId, output.Sanitize(args[0]), output.Sanitize(svc.Url))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&databaseID, "database-id", "",
		"Database to register (full UUID)")
	f.StringVar(&serviceID, "service-id", "", "Service ID to expose")
	_ = cmd.MarkFlagRequired("database-id")
	_ = cmd.MarkFlagRequired("service-id")
	cli.MarkMutating(cmd)

	return cmd
}

// --- deregister ---

func newIngressServiceDeregisterCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "deregister <ingress_id> <service_id>",
		Short: "Deregister a service from an ingress",
		Long: `deregister removes a service from an ingress.

Deregistration is destructive — the service stops being reachable
at the ingress domain — so it prompts for confirmation unless
--force is given. The arguments are the ingress's UUID and the
service's ID.

Example:
  pgedge starfleet byoc ingress service deregister <ingress_id> <service_id>
  pgedge starfleet byoc ingress service deregister <ingress_id> <service_id> \
    --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ingressID, err := parseUUIDArg(args[0], "ingress ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Deregister service %s from ingress %s?", args[1], args[0])
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteIngressService(
				context.Background(), ingressID, args[1])
			if err != nil {
				return fmt.Errorf("deregister ingress service: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "deregister ingress service"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr,
				"Service %s deregistered from ingress %s.\n",
				output.Sanitize(args[1]), output.Sanitize(args[0]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type ingressServiceRow struct {
	serviceID, databaseID, url string
}

func (r ingressServiceRow) Columns() []string {
	return []string{r.serviceID, r.databaseID, r.url}
}

// ingressServiceRowFrom adapts an api.ServiceRegistration into a row.
func ingressServiceRowFrom(svc api.ServiceRegistration) ingressServiceRow {
	return ingressServiceRow{
		serviceID:  svc.ServiceId,
		databaseID: svc.DatabaseId.String(),
		url:        svc.Url,
	}
}
