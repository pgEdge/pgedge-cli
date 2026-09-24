package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// serviceColumns are the table headers shared by service list and get.
//
// No PORT column: Service.Port is the host's internal port (e.g.
// 14052), and dialing it fails with SSL:WRONG_VERSION_NUMBER because it
// is not the TLS-terminating ingress. ENDPOINT carries what a caller
// can dial instead.
var serviceColumns = []string{
	"SERVICE ID", "TYPE", "STATE", "ENDPOINT",
}

// NewDatabaseServiceCmd builds the `pgedge starfleet byoc database service`
// command group, which manages the services deployed alongside a
// database. The plural "services" stays as an alias.
func NewDatabaseServiceCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Aliases: []string{"services"},
		Short:   "Manage services deployed on a database",
		Long: `service manages the services deployed alongside a database:
the MCP, RAG and PostgREST services attached to it.

Use these commands to list a database's services, inspect a single
service, or remove a service type.

Example:
  pgedge starfleet byoc database service list <database_id>
  pgedge starfleet byoc database service get <database_id> <service_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newDatabaseServiceListCmd(rt),
		newDatabaseServiceGetCmd(rt),
		newDatabaseServiceRemoveCmd(rt),
	)
	return cmd
}

// --- list ---

func newDatabaseServiceListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list <database_id>",
		Short: "List services deployed on a database",
		Long: `list shows the services deployed alongside a database.

Use it to find a service's ID and type before running get or
remove. The argument is the database's UUID.

Example:
  pgedge starfleet byoc database service list <database_id>
  pgedge starfleet byoc database service list <database_id> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(db.Services, nil)
			}

			if db.Services == nil || len(*db.Services) == 0 {
				fmt.Fprintln(rt.Stderr, "No services found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*db.Services))
			for _, svc := range *db.Services {
				rows = append(rows, serviceRow(svc))
			}
			return rt.Output.Print(rows, serviceColumns)
		},
	}
}

// --- get ---

func newDatabaseServiceGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <database_id> <service_id>",
		Short: "Show details of a service",
		Long: `get shows the details of a single service on a database.

Use it to read a service's type, state, and the endpoint a caller
can dial. The arguments are the database's UUID and the service's
ID.

Example:
  pgedge starfleet byoc database service get <database_id> <service_id> -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}

			svcID := args[1]
			if db.Services != nil {
				for _, svc := range *db.Services {
					if svc.ServiceId != svcID {
						continue
					}
					if rt.Output.Structured() {
						return rt.Output.Print(&svc, nil)
					}
					rows := []output.Row{serviceRow(svc)}
					return rt.Output.Print(rows, serviceColumns)
				}
			}

			return newExitError(fmt.Sprintf(
				"service %q not found on database %s", svcID, args[0]), ExitNotFound)
		},
	}
}

// --- remove ---

func newDatabaseServiceRemoveCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <database_id> <type>",
		Short: "Remove a service type from a database",
		Long: `remove tears down a service type (mcp, rag or postgrest)
from a database.

Removal is destructive — the service's configuration and
credentials are unrecoverable — so it prompts for confirmation
unless --force is given. Pass --wait to block until the change
finishes.

Example:
  pgedge starfleet byoc database service remove <database_id> mcp
  pgedge starfleet byoc database service remove <database_id> postgrest --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID := args[0]
			svcType := args[1]

			// Before the prompt, so a bad ID is refused rather than
			// confirmed and then refused.
			id, err := parseUUIDArg(dbID, "database ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Remove %q service from database %s? This cannot be "+
					"undone; its configuration and credentials are "+
					"unrecoverable.", svcType, id)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}

			remaining := make([]api.ServiceConfig, 0)
			if db.Services != nil {
				for _, svc := range *db.Services {
					if string(svc.ServiceType) != svcType {
						remaining = append(remaining, svcToConfig(svc))
					}
				}
			}

			body := api.UpdateDatabaseJSONRequestBody{
				Services: nullable.NewNullableWithValue(remaining),
			}

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, dbID)
				if err != nil {
					return err
				}
			}

			resp, err := client.UpdateDatabaseWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("remove service: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
				return err
			}
			fmt.Fprintf(rt.Stderr,
				"Service type %q removal requested for database %s.\n",
				svcType, output.Sanitize(dbID))
			return trackMutation(rt, client, dbID, priorTaskID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- shared helpers ---

// fetchDatabaseWith retrieves a Database using an existing API client.
// It is shared by the service, mcp, rag and postgrest command groups,
// which all read-modify-write a database's service list.
func fetchDatabaseWith(
	rt *module.Runtime, client *api.ClientWithResponses, id uuid.UUID,
) (*api.Database, error) {
	resp, err := client.GetDatabaseWithResponse(
		context.Background(), id, &api.GetDatabaseParams{})
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
	// Recorded here rather than at each caller: this is the one place
	// that proves the argument names a real database.
	rt.DryRun.Pass("%s", databaseResolvedNote(resp.JSON200.Id))
	return resp.JSON200, nil
}

// buildServiceList returns the existing services whose type differs
// from newSvc.ServiceType, then newSvc.
func buildServiceList(
	db *api.Database, newSvc api.ServiceConfig,
) []api.ServiceConfig {
	result := make([]api.ServiceConfig, 0)
	if db.Services != nil {
		for _, svc := range *db.Services {
			if string(svc.ServiceType) != string(newSvc.ServiceType) {
				result = append(result, svcToConfig(svc))
			}
		}
	}
	result = append(result, newSvc)
	return result
}

// findService returns the deployed service of the given type, or nil.
// It mirrors managed's helper of the same name.
func findService(
	db *api.Database, svcType api.ServiceServiceType,
) *api.Service {
	if db.Services == nil {
		return nil
	}
	for i := range *db.Services {
		if (*db.Services)[i].ServiceType == svcType {
			return &(*db.Services)[i]
		}
	}
	return nil
}

// serviceIntent tells `deploy` from `update` inside the apply helper
// they share, which they call with otherwise identical arguments.
type serviceIntent int

const (
	intentDeploy serviceIntent = iota
	intentUpdate
)

// guardServiceIntent enforces the create/reconfigure split `deploy` and
// `update` promise: deploy refuses when a service of that type already
// exists, and update refuses when none does. PATCH /databases/{id} has
// no conditional-create primitive, so this client-side check is the
// only thing standing between `mcp deploy --allow-writes` and a silent
// privilege escalation on a deployed read-only service. Parallel to
// managed's guard of the same name.
//
// group is the caller's cmd.Parent().CommandPath() (e.g. "pgedge
// starfleet byoc database mcp"), so suggested commands need no
// hard-coded module name.
func guardServiceIntent(
	rt *module.Runtime, db *api.Database, t api.ServiceServiceType,
	intent serviceIntent, group string,
) error {
	svc := findService(db, t)
	dbGroup := strings.TrimSuffix(group, " "+string(t))

	switch {
	case intent == intentDeploy && svc != nil:
		state := "unknown"
		if v, err := svc.State.Get(); err == nil && v != "" {
			state = v
		}
		return newExitError(fmt.Sprintf(
			`a %q service is already deployed on database %s `+
				`(state: %s); use %q to reconfigure it, or %q to tear `+
				`it down first`,
			t, db.Id, state,
			fmt.Sprintf("%s update", group),
			fmt.Sprintf("%s service remove %s %s", dbGroup, db.Id, t),
		), ExitGeneral)
	case intent == intentUpdate && svc == nil:
		return newExitError(fmt.Sprintf(
			`no %q service deployed on database %s; use %q to create one`,
			t, db.Id, fmt.Sprintf("%s deploy", group)), ExitGeneral)
	}

	// The check a dry run most needs to report, and the reason a dry
	// run performs the GET at all.
	if intent == intentDeploy {
		rt.DryRun.Pass("no %q service already deployed (deploy intent)", t)
	} else {
		rt.DryRun.Pass("a %q service exists to update (update intent)", t)
	}
	return nil
}

// existingServiceHostIDs returns the cluster hosts the deployed service
// of the given type currently runs on, or nil when none is deployed.
func existingServiceHostIDs(
	db *api.Database, svcType api.ServiceServiceType,
) []string {
	if db.Services == nil {
		return nil
	}
	var ids []string
	for _, svc := range *db.Services {
		if svc.ServiceType != svcType {
			continue
		}
		if hostID, err := svc.HostId.Get(); err == nil && hostID != "" {
			ids = append(ids, hostID)
		}
	}
	return ids
}

// resolveServicePlacement decides which hosts a service apply targets:
// the nodes named by --target-nodes when the caller passed any,
// otherwise the hosts the service is already on, falling back to
// resolveHostIDs when there is nothing deployed to inherit from.
//
// Every service verb must call this rather than resolveHostIDs
// directly: a hand-rolled call drops the existing placement on a
// partial update.
//
// The empty-hostIDs fallback keeps a first deploy on a multi-node
// cluster requiring explicit placement: resolveHostIDs refuses to
// guess.
func resolveServicePlacement(
	client *api.ClientWithResponses, db *api.Database,
	clusterID uuid.UUID, svcType api.ServiceServiceType,
	targetNodes []string,
) ([]string, error) {
	hostIDs := existingServiceHostIDs(db, svcType)
	if len(targetNodes) > 0 || len(hostIDs) == 0 {
		// Trimming here covers all three service verbs at once;
		// backup and restore trim at their body sites the same way.
		return resolveHostIDs(client, clusterID, trimSpaces(targetNodes))
	}
	return hostIDs, nil
}

// printUpdatedDatabase renders the database the API returns from a
// service apply, in machine-readable output modes only — the same
// contract `database update` follows for the same endpoint.
//
// Without it a service deploy under -o json writes nothing to stdout,
// leaving a script no way to read back the service id, port or public
// domain it just created.
func printUpdatedDatabase(rt *module.Runtime, db *api.Database) error {
	if !rt.Output.Structured() {
		return nil
	}
	if db == nil {
		return nil
	}
	return rt.Output.Print(db, nil)
}

func svcToConfig(svc api.Service) api.ServiceConfig {
	cfg := api.ServiceConfig{
		ServiceId:       &svc.ServiceId,
		ServiceType:     api.ServiceConfigServiceType(svc.ServiceType),
		McpConfig:       svc.McpConfig,
		PostgrestConfig: svc.PostgrestConfig,
		RagConfig:       svc.RagConfig,
		TargetNodes:     svc.TargetNodes,
	}
	if hostID, err := svc.HostId.Get(); err == nil && hostID != "" {
		cfg.HostIds = &[]string{hostID}
	}
	return cfg
}

// --- row adapter ---

type svcRowData struct {
	id, typ, state, endpoint string
}

func (r svcRowData) Columns() []string {
	return []string{r.id, r.typ, output.ColorStatus(r.state), r.endpoint}
}

func serviceRow(svc api.Service) svcRowData {
	state := ""
	if v, err := svc.State.Get(); err == nil {
		state = v
	}

	return svcRowData{
		id:       svc.ServiceId,
		typ:      string(svc.ServiceType),
		state:    state,
		endpoint: serviceEndpoint(svc),
	}
}

// serviceEndpoint renders the locator a caller can actually dial,
// preferring the public one:
//
//   - PublicDomain set: TLS-terminating ingress at https://<domain>
//     on 443. Port is the internal one behind it, so it is dropped.
//   - PrivateDomain only: the internal host:port is the locator, for
//     a caller on the same private network.
//   - Neither: only the internal port is known, labelled so it is not
//     mistaken for a dialable address.
func serviceEndpoint(svc api.Service) string {
	port, hasPort := "", false
	if v, err := svc.Port.Get(); err == nil {
		port = fmt.Sprintf("%d", v)
		hasPort = true
	}

	if v, err := svc.PublicDomain.Get(); err == nil && v != "" {
		return "https://" + v
	}
	if v, err := svc.PrivateDomain.Get(); err == nil && v != "" {
		if hasPort {
			return fmt.Sprintf("%s:%s", v, port)
		}
		return v
	}
	if hasPort {
		return port + " (internal port, no domain)"
	}
	return ""
}

// databaseResolvedNote is the ledger line for a database the GET
// confirmed exists. managed's copy is identical on purpose: the two
// modules cannot share a helper, and the report must not spell one fact
// two ways.
//
// It reports the id the server returned, not the one typed: uuid.Parse
// accepts uppercase and the braced and urn forms, and the canonical id
// is the one the request carried.
func databaseResolvedNote(id string) string {
	return fmt.Sprintf("database %s resolved", id)
}
