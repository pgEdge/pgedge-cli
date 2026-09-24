package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// The isolated Runtime (testsupport.NewRuntime), the stub
// token/resource server (testsupport.NewAuthedServer,
// testsupport.JSONHandler, testsupport.TokenBody), the
// transport-error stub (testsupport.BrokenHandler) and the
// authenticated runner (testsupport.RunAuthed) are shared with the
// other module test suites. What stays here is managed-specific:
// fixtures over managed's own generated client and the adapters that
// name managed's root command.

// testOtherDBID and testOtherBackupID went with the resolvers: a
// SECOND id existed only so a prefix could be ambiguous between two of
// them, and there is no ambiguity in a full UUID (#194). newTestClient
// went the same way — it wired a client straight to a stub so the
// resolvers could be driven without a command, and what replaced them
// is parseUUIDArg, which needs no client at all.
const (
	testDatabaseID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	testSizeID     = "01952c3a-0000-7000-8000-000000000001"
	testTaskID     = "9b2ffb2e-3a1c-4c31-9d6f-0f2c3a4b5c6d"
	testBackupID   = "154ae1dd-e449-47e6-bb69-965cbb594e57"
	testBranchID   = "6f1c8b2a-2e3d-4c5b-9a1f-7d8e9c0b1a2d"
)

// newStarfleetRoot returns a synthetic `starfleet` root carrying the four
// persistent connection flags, with the managed sub-tree mounted
// under it. It stands in for cloudcmd.NewStarfleetCmd, which this
// package's tests cannot import: internal/starfleet/cmd imports this
// package, so importing it back would be a cycle. The flag names and
// the fact they are persistent are the whole contract being
// reproduced — internal/clitest gates the real root.
func newStarfleetRoot(rt *module.Runtime) *cobra.Command {
	root := &cobra.Command{Use: "starfleet"}
	pf := root.PersistentFlags()
	pf.String("api-url", "", "pgEdge Starfleet API base URL")
	pf.String("client-id", "", "API client ID")
	pf.String("client-secret", "", "API client secret")
	pf.Duration("timeout", conn.RequestTimeout,
		"Per-request timeout (Go duration; 0 disables)")
	root.AddCommand(NewManagedCmd(rt))
	return root
}

// managedArgs prefixes args with the sub-tree token, so every existing
// test case keeps its own argument vector unchanged.
func managedArgs(args []string) []string {
	return append([]string{"managed"}, args...)
}

// runManaged builds the starfleet tree for rt and executes its managed
// sub-tree with the given args.
func runManaged(t *testing.T, rt *module.Runtime,
	out *bytes.Buffer, args ...string,
) error {
	t.Helper()
	cmd := newStarfleetRoot(rt)
	cmd.SetArgs(managedArgs(args))
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

// withCatalog wraps a handler so the two catalog reads answer as
// arrays, and everything else falls through unchanged.
//
// `database create` and `database resize` check --size against
// `size list`, and `create` checks --region against `region list`, so
// a stub that answers one shared object for every path now kills both
// verbs before their write with an unmarshal error. Every test that
// exercises either verb needs the two lists to parse, and none of them
// is about the catalog -- so the fixture lives here once rather than
// in each handler.
//
// The names are the ones the tests pass: a catalog that published
// something else would turn every create in the suite into a
// usage error, which is the failure this helper exists to avoid
// rather than to cause.
func withCatalog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sizes"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(catalogSizesJSON))
			return
		case strings.HasSuffix(r.URL.Path, "/regions"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(catalogRegionsJSON))
			return
		}
		next(w, r)
	}
}

// The catalog every test in this suite creates and resizes against.
// Only the names matter to the checks, so the rows carry nothing else
// -- a fuller Size fixture lives in catalog_run_test.go, where the
// other fields are the thing under test.
const (
	catalogSizesJSON = `[{"name":"small"},{"name":"large"},` +
		`{"name":"xl"}]`
	catalogRegionsJSON = `[{"region":"us-east-1"},` +
		`{"region":"us-east-2"}]`
)

// newRawServer starts an httptest server that routes every request,
// including the token endpoint, to handler.
func newRawServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// runAuthed runs a managed command against the stub server at srvURL,
// supplying dummy credentials through the persistent flags.
func runAuthed(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	return testsupport.RunAuthed(t, newStarfleetRoot(rt), out, srvURL,
		managedArgs(args)...)
}

// --- JSON fixtures ---

// databaseJSON renders a minimal ManagedDatabase with the given id and
// an optional services array (pass "" for none).
func databaseJSON(id, services string) string {
	svc := ""
	if services != "" {
		svc = fmt.Sprintf(`,"services":%s`, services)
	}
	return fmt.Sprintf(`{
		"id":%q,"name":"mydb","status":"available",
		"region":"us-east-1","size":"small","pg_version":"16",
		"created_at":"2026-07-31T10:00:00Z",
		"updated_at":"2026-07-31T10:00:00Z"%s}`, id, svc)
}

// branchJSON renders a minimal Branch with the given database and
// branch ids.
func branchJSON(databaseID, branchID string) string {
	return fmt.Sprintf(`{
		"id":%q,"database_id":%q,"depth":1,"name":"br-1",
		"status":"available","region":"us-east-1","size":"small",
		"pg_version":"16",
		"ip_allowlist":{"rules":[],"state":"closed"},
		"created_at":"2026-09-18T10:00:00Z",
		"updated_at":"2026-09-18T10:00:00Z"}`, branchID, databaseID)
}

// allowlistDatabaseJSON is databaseJSON with an ip_allowlist block.
// rules is the JSON array of rule objects; state is derived the way the
// server does it, so a test never has to keep the two in step.
func allowlistDatabaseJSON(id, rules, services string) string {
	state := "restricted"
	switch {
	case rules == "[]":
		state = "closed"
	case strings.Contains(rules, `"0.0.0.0/0"`):
		state = "open"
	}
	svc := ""
	if services != "" {
		svc = fmt.Sprintf(`,"services":%s`, services)
	}
	return fmt.Sprintf(`{
		"id":%q,"name":"mydb","status":"available",
		"region":"us-east-1","size":"small","pg_version":"16",
		"created_at":"2026-07-31T10:00:00Z",
		"updated_at":"2026-07-31T10:00:00Z",
		"ip_allowlist":{"rules":%s,"state":%q}%s}`,
		id, rules, state, svc)
}

// mcpWithAllowlistJSON is one deployed MCP service carrying its own
// closed allowlist, for the --service tests.
const mcpWithAllowlistJSON = `[
	{"service_id":"mcp00001","service_type":"mcp","state":"running",
	 "mcp_config":{"allow_writes":false},
	 "ip_allowlist":{"rules":[{"cidr":"198.51.100.0/24"}],
	                 "state":"restricted"}}
]`

// mcpAndRagAllowlistJSON is two deployed services, mcp and rag, each
// carrying its own restricted allowlist, so a write scoped to one
// service can be checked to leave the other's rules untouched.
const mcpAndRagAllowlistJSON = `[
	{"service_id":"mcp00001","service_type":"mcp","state":"running",
	 "mcp_config":{"allow_writes":false},
	 "ip_allowlist":{"rules":[{"cidr":"198.51.100.0/24"}],
	                 "state":"restricted"}},
	{"service_id":"rag00001","service_type":"rag","state":"running",
	 "ip_allowlist":{"rules":[{"cidr":"192.0.2.0/24"}],
	                 "state":"restricted"}}
]`

// mcpNoAllowlistJSON is one deployed MCP service that has never had its
// allowlist touched, so the server omits ip_allowlist entirely, for the
// test that get still derives a closed state from its absence.
const mcpNoAllowlistJSON = `[
	{"service_id":"mcp00001","service_type":"mcp","state":"running",
	 "mcp_config":{"allow_writes":false}}
]`

// mcpServiceJSON is a deployed MCP service carrying secrets, as
// GetManagedDatabase returns them.
// The port here is deliberately NOT 443. saas always sends 443 for a
// managed service, so a fixture using it could not tell a real
// pass-through from a hardcoded constant. public_domain is the bare
// database domain, as saas sends it.
//
// uri is what saas #1868 added and is the locator the CLI renders. It
// is reproduced here exactly as observed live on 2026-08-17 — the
// database domain plus the service segment, and no port, even though
// port is sent alongside. TestServiceEndpoint is where a uri that
// DISAGREES with the derived segment is exercised; this fixture's job
// is to look like the real response.
const mcpServiceJSON = `[{
	"service_id":"abc12345","service_type":"mcp","state":"running",
	"port":8080,
	"public_domain":"demo-db.use2.example.com",
	"uri":"https://demo-db.use2.example.com/mcp",
	"mcp_config":{
		"allow_writes":true,
		"embedding_provider":"openai",
		"embedding_model":"text-embedding-3-small",
		"embedding_api_key":"sk-stored",
		"init_tokens":"tok-stored"
	}}]`

// threeServicesJSON is a database carrying one of each service type,
// used to prove a write to one preserves the other two.
//
// The rag entry carries a uri so a preservation test can see that an
// untouched service is echoed back WHOLE, readOnly fields included —
// the decision buildServiceList documents and that a well-meant
// "strip the readOnly fields" refactor would quietly undo.
const threeServicesJSON = `[
	{"service_id":"mcp00001","service_type":"mcp","state":"running",
	 "mcp_config":{"allow_writes":false}},
	{"service_id":"rag00001","service_type":"rag","state":"running",
	 "uri":"https://demo-db.use2.example.com/rag",
	 "rag_config":{
		"embedding_llm":{"provider":"openai","model":"em"},
		"completion_llm":{"provider":"anthropic","model":"cm"},
		"pipelines":[{"name":"docs","tables":[
			{"table":"t","text_column":"c","vector_column":"v"}]}]}},
	{"service_id":"pgr00001","service_type":"postgrest","state":"running",
	 "postgrest_config":{"db_schemas":"public","db_anon_role":"anon"}}
]`

// captureRequest records the body and method/path of the last non-token
// request the stub received, so a test can assert what was actually
// sent. get is served first, then the recorded write is answered with
// writeStatus and writeBody.
type captureRequest struct {
	Method string
	Path   string
	Body   string
	Calls  int
}

// stubGetThenWrite answers any GET of a managed database with getBody,
// and records + answers the first non-GET with writeStatus/writeBody.
func stubGetThenWrite(
	rec *captureRequest, getBody string, writeStatus int, writeBody string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(getBody))
			return
		}
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		rec.Method = r.Method
		rec.Path = r.URL.Path
		rec.Body = buf.String()
		rec.Calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(writeStatus)
		_, _ = w.Write([]byte(writeBody))
	}
}

// stubAllowlist is stubGetThenWrite plus the client-ip endpoint: a GET
// ending in /client-ip answers {"ip_address": clientIP}, every other
// GET answers dbBody, and the first write is recorded.
func stubAllowlist(
	rec *captureRequest, dbBody, clientIP string,
	writeStatus int, writeBody string,
) http.HandlerFunc {
	inner := stubGetThenWrite(rec, dbBody, writeStatus, writeBody)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/client-ip") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ip_address":"` + clientIP + `"}`))
			return
		}
		inner(w, r)
	}
}

// asExitError unwraps err into an *ExitError, reporting whether it is
// one. Tests use it to assert the exit CODE rather than the message,
// since the codes are the contract.
func asExitError(err error, target **ExitError) bool {
	return errors.As(err, target)
}

// writeFile overwrites path with body, for tests that need a file's
// contents replaced after writePipelineConfig created it.
func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
