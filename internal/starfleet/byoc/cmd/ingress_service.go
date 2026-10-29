package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
		svcType    string
	)
	cmd := &cobra.Command{
		Use:   "register <ingress_id>",
		Short: "Register a service on an ingress",
		Long: `register exposes a database service through an ingress.

Use it to make a running service reachable at the ingress's
domain. The argument is the ingress's UUID, and --database-id is
required.

Name the service with --service (mcp, rag or postgrest), and the
service ID is read from the database, or with --service-id. Given
both, they must name the same service, or the command exits 2.

Example:
  pgedge starfleet byoc ingress service register <ingress_id> \
    --database-id <id> --service mcp
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

			f := cmd.Flags()
			byType := f.Changed("service")
			if !byType && !f.Changed("service-id") {
				return newExitError(
					"one of --service or --service-id is required",
					ExitUsage)
			}
			if byType && !slices.Contains(serviceTypes, svcType) {
				return newExitError(fmt.Sprintf(
					"unknown service %q (expected one of: %s)", svcType,
					strings.Join(serviceTypes, ", ")), ExitUsage)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			if byType {
				db, err := fetchDatabaseWith(rt, client, dbID)
				if err != nil {
					return err
				}
				given := ""
				if f.Changed("service-id") {
					given = serviceID
				}
				serviceID, err = serviceIDOfType(db, svcType, given)
				if err != nil {
					return err
				}
				rt.DryRun.Pass("%s service %s found on database %s",
					svcType, output.Sanitize(serviceID), dbID)
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
	f.StringVar(&svcType, "service", "",
		"Service type to expose, read from the database: "+
			strings.Join(serviceTypes, ", "))
	_ = cmd.MarkFlagRequired("database-id")
	cli.MarkMutating(cmd)

	return cmd
}

// serviceTypes is the spec's Service.service_type enum.
var serviceTypes = []string{
	string(api.ServiceServiceTypeMcp),
	string(api.ServiceServiceTypeRag),
	string(api.ServiceServiceTypePostgrest),
}

// serviceIDOfType returns the ID of the database's service of type t,
// or given when it is one of them. The database lists one entry per
// service instance, so a service on several hosts repeats its ID; only
// distinct IDs are ambiguous.
func serviceIDOfType(db *api.Database, t, given string) (string, error) {
	var ids []string
	if db.Services != nil {
		for _, svc := range *db.Services {
			if string(svc.ServiceType) == t &&
				!slices.Contains(ids, svc.ServiceId) {
				ids = append(ids, svc.ServiceId)
			}
		}
	}
	list := output.Sanitize(strings.Join(ids, ", "))
	switch {
	case len(ids) == 0:
		return "", newExitError(fmt.Sprintf(
			"no %q service deployed on database %s", t, db.Id),
			ExitGeneral)
	case given != "" && slices.Contains(ids, given):
		return given, nil
	case given != "":
		return "", newExitError(fmt.Sprintf(
			"--service-id %q is not a %q service on database %s (%s)",
			given, t, db.Id, list), ExitUsage)
	case len(ids) == 1:
		return ids[0], nil
	}
	return "", newExitError(fmt.Sprintf(
		"database %s has %d %q services (%s); pass --service-id",
		db.Id, len(ids), t, list),
		ExitUsage)
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
