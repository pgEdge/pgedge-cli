package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// parseUUIDArg parses arg as a UUID the caller typed, naming the
// resource kind in the message (e.g. "database ID").
//
// It delegates to cli.ParseUUIDArg so every module reaches exit 2 for
// a malformed argument through one function. byoc carries the same
// wrapper; neither package can reach the other's ExitError, which is
// why the shared helper returns cli.UsageError instead.
func parseUUIDArg(arg, kind string) (uuid.UUID, error) {
	return cli.ParseUUIDArg(arg, kind)
}

// fetchDatabaseWith retrieves a ManagedDatabase using an existing API
// client. It is shared by the service, allowlist, mcp, rag and
// postgrest command groups, which all read-modify-write a database.
//
// It goes through GetManagedDatabase deliberately. saas hydrates the
// MCP service secrets on that path (hydrateManagedServiceSecrets) and
// not on the list path, so a service config built from a LIST entry
// silently arrives with its secrets blanked — indistinguishable from a
// service that has none set. Never build a merge base from a list.
func fetchDatabaseWith(
	rt *module.Runtime, client *api.ClientWithResponses, id uuid.UUID,
) (*api.ManagedDatabase, error) {
	resp, err := client.GetManagedDatabaseWithResponse(
		context.Background(), id, &api.GetManagedDatabaseParams{})
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
	// Recorded here rather than at each of the twelve callers: this is
	// the one place that proves the argument names a real database.
	rt.DryRun.Pass("%s", databaseResolvedNote(resp.JSON200.Id))
	return resp.JSON200, nil
}

// buildServiceList returns a services slice that preserves all existing
// services whose type differs from newSvc.ServiceType, then appends
// newSvc carrying the replaced service's ServiceId.
//
// UpdateManagedDatabaseInput.Services replaces the stored list
// wholesale, so a PATCH carrying only an MCP config would tear down the
// database's RAG and PostgREST services.
//
// The ServiceId is what makes the write an update. saas
// (managed_database_svc.go) treats a service whose ServiceID is empty
// or unknown as new, which must supply its API keys, and one naming an
// existing ServiceID as a reconfiguration, which may omit them for
// CarryForwardManagedSecrets to refill from stored state. RAG and
// PostgREST secrets never come back on GET, so without the id their
// write answers `400 services[1]: embedding_llm: api_key is required
// for provider "openai"`; MCP's do come back and are re-sent. Verified
// on devapi 2026-08-06, and needs saas #1845, which keys the split on
// the echoed pair. A stub answering 200 is indifferent to the field,
// which is why TestServiceWriteCarriesTheExistingServiceID asserts it
// is sent.
//
// Managed uses api.ServiceConfig for request and response alike, so
// existing entries are echoed as-is, including the fields the API
// declares readOnly (`uri` among them since saas #1868). saas's binder
// is strict, but on devapi 2026-08-17 a PATCH echoing uri, port,
// public_domain and state answered 200, while one undeclared field
// answered `400 unknown field`: a declared readOnly field binds and is
// ignored. Do not strip them; on an untouched service that half-sends
// its config.
//
// That probe carried a populated uri. The echo cannot produce
// `"uri":null`: the API omits these fields rather than nulling them
// ("Omitted until the database's domain is assigned, as public_domain
// is"), and an unspecified nullable.Nullable is a zero-length map that
// `omitempty` drops. Only an explicit null from saas would serialise as
// `"uri":null`, verified by marshalling both.
func buildServiceList(
	db *api.ManagedDatabase, newSvc api.ServiceConfig,
) []api.ServiceConfig {
	result := make([]api.ServiceConfig, 0)
	if db.Services != nil {
		for _, svc := range *db.Services {
			if svc.ServiceType != newSvc.ServiceType {
				result = append(result, svc)
				continue
			}
			// Same type: this is the service being reconfigured. Adopt
			// its identity so saas treats the write as in-place.
			if newSvc.ServiceId == nil {
				newSvc.ServiceId = svc.ServiceId
			}
			// Carry the allowlist too. saas keeps a reconfigured
			// service's rules when the field is omitted, but relying
			// on that is one server change away from a lockout.
			if newSvc.IpAllowlist == nil {
				newSvc.IpAllowlist = svc.IpAllowlist
			}
		}
	}
	return append(result, newSvc)
}

// applyServices PATCHes db's service list with services spliced in, and
// reports the outcome. It is the one write path shared by the mcp, rag
// and postgrest apply helpers, `service remove` and a service's
// allowlist.
func applyServices(
	rt *module.Runtime, client *api.ClientWithResponses,
	db *api.ManagedDatabase, services []api.ServiceConfig,
	what, dbID string,
) error {
	id, err := uuid.Parse(db.Id)
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid database ID %q: %v", db.Id, err), ExitGeneral)
	}

	body := api.UpdateManagedDatabaseJSONRequestBody{Services: &services}

	// Captured before the mutation so waiting can tell this write's
	// task apart from the database's earlier ones.
	// UpdateManagedDatabaseResponse carries no task id, so the task is
	// found as the newest one for this subject that is not the one
	// already seen. captureTaskBaseline reads only when waiting.
	//
	// db.Id rather than dbID, so the baseline read and the PATCH key on
	// the id the GET returned whatever was typed (uuid.Parse accepts
	// uppercase and the braced and urn forms). The endpoint was measured
	// normalising a non-canonical subject_id, but openapi/managed.yaml
	// declares it a bare string with no format, so that is observed
	// behaviour rather than a published promise.
	base := captureTaskBaseline(client, db.Id)

	resp, err := client.UpdateManagedDatabaseWithResponse(
		context.Background(), id, body)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}

	if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr, "%s on database %s.\n",
		output.Sanitize(what), output.Sanitize(dbID))
	return trackMutation(rt, client, db.Id, base)
}

// printUpdatedDatabase renders the database the API returns from a
// service apply, in machine-readable output modes only — the same
// contract `database update` follows for the same endpoint.
//
// Without it a service deploy writes nothing to stdout under -o json:
// the "applied" line goes to stderr. A caller then has no way to read
// back the service id or assigned port it just created, which is
// exactly what a script or an agent needs next.
func printUpdatedDatabase(
	rt *module.Runtime, db *api.ManagedDatabase,
) error {
	if !rt.Output.Structured() {
		return nil
	}
	if db == nil {
		return nil
	}
	return rt.Output.Print(db, nil)
}

// findService returns the deployed service of the given type, or nil.
//
// Type is the whole identity of a managed service. saas matches an
// incoming config against the stored list by ServiceType
// (existingByType in PrepareManagedServiceUpdate), reusing that
// service's ServiceID and minting a new one otherwise, so a managed
// database carries at most one service of each type.
func findService(
	db *api.ManagedDatabase, svcType api.ServiceConfigServiceType,
) *api.ServiceConfig {
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

// serviceIntent distinguishes a `deploy` call from an `update` call at
// the one place both share: the apply helper. deploy and update invoke
// the same helper with identical arguments, so nothing else can tell
// them apart.
type serviceIntent int

const (
	intentDeploy serviceIntent = iota
	intentUpdate
)

// guardServiceIntent enforces the create/reconfigure split `deploy` and
// `update` promise: deploy refuses when a service of that type already
// exists, and update refuses when none does. Both are client-side
// checks over the GET fetchDatabaseWith already made: PATCH
// /databases/{id} has no conditional create, and saas's reconciler
// matches incoming services by type whatever the CLI asked for. This
// guard is the only thing standing between `mcp deploy --allow-writes`
// and a silent privilege escalation on a deployed read-only service.
//
// group is the command path through the service group (e.g. "pgedge
// starfleet managed database mcp"), derived by the caller from
// cmd.Parent().CommandPath() so the suggested commands hard-code no
// path.
func guardServiceIntent(
	rt *module.Runtime, db *api.ManagedDatabase,
	t api.ServiceConfigServiceType, intent serviceIntent, group string,
) error {
	svc := findService(db, t)
	dbGroup := strings.TrimSuffix(group, " "+string(t))

	switch {
	case intent == intentDeploy && svc != nil:
		state := "unknown"
		if svc.State != nil && *svc.State != "" {
			state = *svc.State
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

	// The check most worth reporting, for the escalation above, and the
	// reason a dry run performs reads at all: the guard cannot tell
	// deploy from update without the GET.
	if intent == intentDeploy {
		rt.DryRun.Pass("no %q service already deployed (deploy intent)", t)
	} else {
		rt.DryRun.Pass("a %q service exists to update (update intent)", t)
	}
	return nil
}

// serviceTypes are the service types a managed database accepts, used
// to validate the <type> argument before a request goes out.
var serviceTypes = []api.ServiceConfigServiceType{
	api.Mcp, api.Rag, api.Postgrest,
}

// parseServiceType validates a user-supplied service type.
func parseServiceType(s string) (api.ServiceConfigServiceType, error) {
	for _, t := range serviceTypes {
		if string(t) == s {
			return t, nil
		}
	}
	names := make([]string, 0, len(serviceTypes))
	for _, t := range serviceTypes {
		names = append(names, string(t))
	}
	return "", newExitError(fmt.Sprintf(
		"unknown service type %q (expected one of: %s)",
		s, strings.Join(names, ", ")), ExitUsage)
}

// joinFlags renders a flag list as "a", "a and b", or "a, b and c".
func joinFlags(flags []string) string {
	if len(flags) == 1 {
		return flags[0]
	}
	out := ""
	for i, f := range flags {
		switch {
		case i == 0:
			out = f
		case i == len(flags)-1:
			out += " and " + f
		default:
			out += ", " + f
		}
	}
	return out
}

// pluralIs agrees a verb with a count.
func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// --- row adapters ---

// serviceColumns are the table headers shared by service list and get.
// There is no DOMAIN column: managed's ServiceConfig carries no
// per-service domain, only the database-level one.
//
// There is no PORT column either, as in byoc: a managed service is
// always reached over HTTPS on 443 through the shared ingress, so the
// number tells a caller nothing they can act on. It is folded into
// ENDPOINT, the locator they can dial.
//
// saas derives STATE since e55b004e. The spec calls it the runtime
// state observed on the deployment serving the service, so two
// services on one database can disagree. It is not a readiness signal
// either way; see the reference.
var serviceColumns = []string{
	"SERVICE ID", "TYPE", "STATE", "ENDPOINT",
}

type svcRowData struct {
	id, typ, state, endpoint string
}

func (r svcRowData) Columns() []string {
	return []string{r.id, r.typ, output.ColorStatus(r.state), r.endpoint}
}

func serviceRow(svc api.ServiceConfig) svcRowData {
	row := svcRowData{
		typ:      string(svc.ServiceType),
		endpoint: serviceEndpoint(svc),
	}
	if svc.ServiceId != nil {
		row.id = *svc.ServiceId
	}
	if svc.State != nil {
		row.state = *svc.State
	}
	return row
}

// servicePathSegments maps a service type to the URL path segment it
// is served under on the database's hostname.
//
// It mirrors saas's dbspec.ServicePathSegment, which is the one
// definition of this vocabulary. The mapping is NOT the identity:
// postgrest routes under "rest", so deriving the segment from the type
// name would build a URL that 404s for one of the three types.
var servicePathSegments = map[api.ServiceConfigServiceType]string{
	api.Mcp:       "mcp",
	api.Rag:       "rag",
	api.Postgrest: "rest",
}

// serviceEndpoint renders the URL a caller can dial, preferring the
// `uri` the API reports (added in saas #1868), used verbatim. It
// carries no port even though `port` reports 443, so never rebuild it
// by joining domain and port. saas ac682b13 moved it onto the versioned
// path: devapi measured `https://<domain>/mcp` on 2026-08-17 and
// `https://<domain>/mcp/v1` on 2026-08-28, and passing it through
// needed no change for either.
//
// The fallback stays because `uri` is absent on any deployment
// predating #1868. Prod cannot be shown to be past that revision, since
// it holds no managed database to read one from, and an empty ENDPOINT
// there would regress a URL that is right today. The two disagree only
// if saas moves a service's path, and then the reported value is right.
//
// The fallback appends the service's path segment to public_domain, the
// database's bare hostname behind the shared TLS-terminating ingress,
// as the field's own description says: "consumers build the full
// service URL by appending the service path prefix". With no uri and
// either no domain or no known segment it is empty, since an empty cell
// beats a URL that 404s.
func serviceEndpoint(svc api.ServiceConfig) string {
	if uri, err := svc.Uri.Get(); err == nil && uri != "" {
		return uri
	}
	domain, err := svc.PublicDomain.Get()
	if err != nil || domain == "" {
		return ""
	}
	seg, ok := servicePathSegments[svc.ServiceType]
	if !ok {
		return ""
	}
	return "https://" + domain + "/" + seg
}

// databaseResolvedNote is the ledger line for a database the GET
// confirmed exists. It reports the id the server returned, the one the
// request carried, rather than what the caller typed, which can differ
// in spelling (`uuid.Parse` accepts uppercase and the braced and urn
// forms).
func databaseResolvedNote(id string) string {
	return fmt.Sprintf("database %s resolved", id)
}
