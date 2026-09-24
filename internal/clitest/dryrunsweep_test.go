package clitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestNoMutatingRequestEscapesADryRun runs EVERY command marked mutating
// under --dry-run against a stub that records what it was asked to do,
// and fails if the stub was asked to change anything.
//
// The failure it guards: interception by HTTP method lets
// `controlplane task cancel --dry-run` send `GET .../cancel`, really
// cancel the task, and report success. A per-error-family sample
// cannot catch that, because the fault is per-operation.
//
// The assertion is deliberately about the SERVER, not about the report.
// "No state-changing request arrived" is the promise --dry-run makes, and
// it holds whether or not a given verb got far enough with synthesized
// arguments to build one. That makes the check robust to argument
// synthesis being imperfect, while still catching exactly the class of
// bug above.
//
// Coverage cannot silently collapse to nothing: reachedRequest counts the
// verbs that actually issued a request, and the floor below fails if that
// number drops.
func TestNoMutatingRequestEscapesADryRun(t *testing.T) {
	var (
		mu         sync.Mutex
		mutating   []string // method+path the stub was asked to change
		reads      int
		tokenPosts int
	)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			switch {
			case strings.HasSuffix(r.URL.Path, tokenPath):
				// The OAuth exchange is the one POST a dry run must
				// SEND — see conn.authHTTPClientFor. Counted so the
				// carve-out is asserted rather than assumed.
				tokenPosts++
			case r.Method != http.MethodGet && r.Method != http.MethodHead:
				mutating = append(mutating,
					r.Method+" "+r.URL.Path)
			case isMutatingGETPath(r.URL.Path):
				// A GET the API declares as state-changing. The whole
				// reason this gate exists.
				mutating = append(mutating,
					r.Method+" "+r.URL.Path+" (state-changing GET)")
			default:
				reads++
			}
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, tokenPath):
				_, _ = w.Write([]byte(
					`{"access_token":"tok","token_type":"Bearer",` +
						`"expires_in":3600}`))
			case strings.HasSuffix(r.URL.Path, "/v1/version"):
				_, _ = w.Write([]byte(`{"version":"0.10.0"}`))
			case strings.HasSuffix(r.URL.Path, "/regions"):
				// An ARRAY, not stubBody's object: `managed database
				// create` resolves --region from this list when the flag
				// is omitted, and the generated parser unmarshals into
				// []ManagedRegion. Answering the shared object kills the
				// verb before its write, which is how create silently
				// left this sweep once already.
				_, _ = w.Write([]byte(`[{"region":"us-east-1"}]`))
			case strings.HasSuffix(r.URL.Path, "/sizes"):
				// An ARRAY, and for the same reason /regions is one:
				// `managed database create` and `database resize` now
				// check --size against what the API publishes, so a
				// stub that answers the shared object kills both verbs
				// before their write. The name must be the one
				// valueFor synthesizes, exactly as /regions publishes
				// the region valueFor picks.
				_, _ = w.Write([]byte(`[{"name":"small"}]`))
			case strings.HasSuffix(r.URL.Path, "/nodes"):
				// An ARRAY, and exactly ONE element, for the same reason
				// /regions is special-cased. byoc's six service verbs
				// resolve target nodes before their write, and
				// resolveHostIDs auto-selects only on a single-node
				// cluster — two entries make it demand an explicit
				// --target-nodes and exit first. Answering the shared
				// object instead fails to unmarshal into []ClusterNode
				// and killed all six.
				_, _ = w.Write([]byte(stubNodes))
			case strings.Contains(r.URL.Path, sweepUUIDWithServices):
				// The database that already has a service deployed, for
				// the verbs that cannot reach a write without one.
				_, _ = w.Write([]byte(stubBodyWithServices))
			default:
				// Permissive enough that most verbs get past their reads.
				_, _ = w.Write([]byte(stubBody))
			}
		}))
	t.Cleanup(srv.Close)

	// Two passes: a bare base URL and one carrying a path prefix. The
	// prefixed pass is not hypothetical: a Control Plane behind a
	// reverse proxy at /api/ defeats interception patterns anchored at
	// the root, and nothing in the CLI rejects such a URL.
	home := t.TempDir()
	writeSweepConfig(t, home, srv.URL)
	t.Setenv("HOME", home)

	verbs := annotatedCommandPaths(t)
	if len(verbs) < 60 {
		t.Fatalf("only %d annotated verbs found; the sweep would prove "+
			"almost nothing", len(verbs))
	}

	var reached, silent []string
	for _, path := range verbs {
		if runSweepVerb(t, path) {
			reached = append(reached, path)
		} else {
			silent = append(silent, path)
		}
	}

	// Second pass, prefixed. Only the escape assertion matters here; the
	// coverage bookkeeping above already stands.
	writeSweepConfig(t, home, srv.URL+"/api")
	for _, path := range verbs {
		runSweepVerb(t, path)
	}

	mu.Lock()
	got := append([]string(nil), mutating...)
	mu.Unlock()
	sort.Strings(got)
	if len(got) > 0 {
		t.Errorf("%d state-changing request(s) reached the server "+
			"during a dry run:\n  %s", len(got),
			strings.Join(dedupe(got), "\n  "))
	}

	if tokenPosts == 0 {
		t.Error("the OAuth token endpoint was never called; the " +
			"reads-allowed half of the semantics is not being exercised")
	}
	// Negative control, for OVER-blocking. Without it, a reads() that
	// wrongly stopped ordinary GETs would make this test easier to pass:
	// every such verb would report an interception and count toward the
	// floor, while the stub sat idle.
	mu.Lock()
	sawReads := reads
	mu.Unlock()
	if sawReads == 0 {
		t.Error("the stub served no reads at all; --dry-run is " +
			"blocking requests it must let through")
	}

	// Positive control. Without it, a synthesis bug that made every verb
	// fail before its first request would look exactly like a pass.
	if len(reached) < sweepFloor {
		t.Errorf("only %d of %d verbs had a write intercepted; the "+
			"sweep is not exercising the transport (want >= %d). "+
			"Silent:\n  %s",
			len(reached), len(verbs), sweepFloor,
			strings.Join(silent, "\n  "))
	}
	// Verbs that must be reached BY NAME, because no floor value can
	// see them: a verb that never reached a write was never in the
	// tally, so nothing dips when it leaves. That reason holds
	// whatever sweepFloor is set to -- see sweepFloor for the floor's
	// own state -- so this list is not an argument about slack.
	//
	// The two state-changing GETs are why this gate exists. If argument
	// synthesis stops reaching them, the gate has quietly stopped
	// covering the bug it was written for.
	//
	// backup restore earns its place by having already fallen out once:
	// its request moved to a database-keyed path, so a field it
	// already read now had to PARSE as a UUID before the write, and
	// stubBody did not carry it. That dropped the tally from 42 to 41 —
	// silently, because 41 clears the floor.
	//
	// Its write is assembled from a PREVIOUS read's response, which is
	// what makes a stubBody gap able to lose it. The service verbs below
	// are in the same class and were lost the same way: each needs
	// something the stub had not answered, so each exited before building
	// its write. They are named here because what restored them — a
	// second stubbed database, a cluster_id, a one-element node list, all
	// addressed through sweepOverrides and the stub bodies — is exactly
	// the kind of arrangement a later edit can undo without noticing.
	for _, must := range []string{
		"pgedge controlplane task cancel", "pgedge controlplane cluster init",
		"pgedge starfleet managed backup restore",
		// create provisions billable infrastructure, and it fell out of
		// this sweep the moment --region stopped being a required flag:
		// synthesizeArgs fills required flags, so the annotation going
		// away took the argument with it. 45 cleared the floor, so
		// nothing went red.
		"pgedge starfleet managed database create",
		"pgedge starfleet managed database service remove",
		"pgedge starfleet managed database mcp update",
		"pgedge starfleet managed database rag update",
		"pgedge starfleet managed database postgrest update",
		// Both modules' rag deploy: seven required flags, one of them
		// naming a file that must exist, so generic synthesis cannot
		// reach either and only an override keeps them here.
		"pgedge starfleet managed database rag deploy",
		"pgedge starfleet byoc database rag deploy",
		// byoc's other five. All six were outside this sweep from the
		// day it was written, and no floor could ever have seen
		// them, so the must-reach list is the only thing that can.
		"pgedge starfleet byoc database mcp deploy",
		"pgedge starfleet byoc database mcp update",
		"pgedge starfleet byoc database rag update",
		"pgedge starfleet byoc database postgrest deploy",
		"pgedge starfleet byoc database postgrest update",
		// Reached only through an override, so the floor cannot hold
		// it either: it validates --token and --server-url by hand
		// before building its request. Adding that override moved the
		// tally from 52 to 53 — measured both ways — which also means
		// the "tally of 53" below was one ahead of the truth until
		// now. It is the one declared-204 operation whose verb generic
		// synthesis cannot reach; seventeen operations across the four
		// specs declare a 204, so that is not what is unusual here.
		"pgedge controlplane cluster join",
		// This verb's --provider is REQUIRED
		// and reaches its write only because nothing validates the
		// value, so a validation added later takes it out of the
		// tally silently. Holding it by name is the only thing that
		// notices, and not adding it would have left the reasoning
		// stated but unacted on.
		"pgedge starfleet byoc backup create",
	} {
		found := false
		for _, p := range reached {
			if p == must {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s had no write intercepted; it is named here "+
				"because the floor cannot notice it leaving, so the "+
				"sweep has quietly stopped covering it", must)
		}
	}
	t.Logf("swept %d verbs; %d had a write intercepted, %d never got "+
		"far enough", len(verbs), len(reached), len(silent))
}

// isMutatingGETPath mirrors the Control Plane operations that change
// state despite being GET. Kept as literal patterns here rather than
// imported from internal/controlplane/cmd (they are unexported there) so this gate
// fails if the CLI's own list is quietly narrowed — two independent
// statements of the same fact is the point.
func isMutatingGETPath(path string) bool {
	// Matched from the END, so a base URL carrying a path prefix is
	// covered. Anchoring at the start is exactly the mistake that left
	// the bug live for a Control Plane behind a reverse proxy, and this
	// detector had the same flaw in its first version.
	//
	// The segment walk is also done on the ESCAPED spelling, because a
	// "/" inside a path parameter decodes into a real separator and adds
	// a segment — which is how this detector missed a cancel the
	// transport also missed. Both are checked, matching reads().
	if strings.HasSuffix(path, "/v1/cluster/init") ||
		strings.HasSuffix(path, "/v1/cluster%2Finit") {
		return true
	}
	return isCancelPath(path) || isCancelPath(escapeSlashes(path))
}

// escapeSlashes re-encodes a decoded path-parameter separator, so a
// request that arrived as `db1%2F` — which net/http hands the handler
// DECODED, as `db1//` — is walked with its original segment count.
//
// The replacement is "%2F/" and not "/%2F/": the point is to COLLAPSE the
// pair back into one segment. Adding a segment instead left the detector
// permanently inert, which a review caught by showing it returned false
// for exactly the path the bug produces. Hence
// TestEscapeSlashesCollapsesTheSegment below — the helper now has its own
// test rather than being trusted inside a bigger one.
func escapeSlashes(path string) string {
	return strings.ReplaceAll(path, "//", "%2F/")
}

// TestEscapeSlashesCollapsesTheSegment pins the helper directly, because
// a detector that silently matches nothing is indistinguishable from a
// clean run.
func TestEscapeSlashesCollapsesTheSegment(t *testing.T) {
	// What net/http hands the handler for a `--database "db1/"` cancel.
	const decoded = "/v1/databases/db1//tasks/" +
		"11111111-1111-4111-8111-111111111111/cancel"
	if isCancelPath(decoded) {
		t.Fatal("precondition: the decoded path should NOT match on its " +
			"own; that is the whole reason this helper exists")
	}
	if got := escapeSlashes(decoded); !isCancelPath(got) {
		t.Errorf("escapeSlashes(%q) = %q, which still does not match — "+
			"the detector would be inert", decoded, got)
	}
	if !isMutatingGETPath(decoded) {
		t.Error("isMutatingGETPath missed a cancel whose database id " +
			"carried a slash")
	}
	if !isMutatingGETPath("/api" + decoded) {
		t.Error("isMutatingGETPath missed the same path behind a " +
			"base-URL prefix")
	}
	// And it must not turn an ordinary path into a match.
	for _, safe := range []string{
		"/v1/databases", "/v1/databases/db1/tasks",
		"/v1/hosts", "/api/v1/version",
	} {
		if isMutatingGETPath(safe) {
			t.Errorf("%q treated as state-changing", safe)
		}
	}
}

// isCancelPath walks the trailing segments of the cancel operation.
func isCancelPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 6 || parts[len(parts)-1] != "cancel" {
		return false
	}
	tail := parts[len(parts)-6:]
	return tail[0] == "v1" && tail[1] == "databases" && tail[3] == "tasks"
}

// sweepUUID is a syntactically valid UUID, which several verbs validate
// before they build a request.
const sweepUUID = "7f3a5c1e-0000-4000-8000-000000000000"

// tokenPath is the OAuth endpoint, the one POST a dry run sends. Matched
// as a SUFFIX throughout: the prefixed pass sends it to
// /api/account/v1/oauth/token, and an equality check there would score
// the carve-out as a violation. It passed by luck the first time, because
// pass one had already cached the token.
const tokenPath = "/account/v1/oauth/token"

// sweepFloor is the number of verbs that must reach a request. See the
// positive-control note in the test.
//
// 54 against a measured tally of 54. The slack is GONE, and its
// removal is the point: at 51 against 54 three verbs could leave in
// silence, and review demonstrated one doing so — replacing the
// `kind` arm in valueFor dropped `managed backup create` out of the
// sweep, 54 to 53, with the whole build green.
//
// An exact floor means a new required flag reddens the build. That is
// the intended cost: the author then either fixes the synthesis or
// lowers this number deliberately and says why, which is the choice
// the slack was making silently on their behalf. It is a >= rather than
// an equality, so a verb that starts reaching is good news and stays
// green.
//
// RAISE THIS WHENEVER THE TALLY RISES. Exactness decays by growth: at
// 54 against a tally of 56 the slack is back, arrived at by nobody
// choosing it, and nothing here enforces the re-tightening. An
// equality assert would be stronger and would also redden on good
// news, which is why this is a note rather than a check.
//
// No floor value could have noticed the six byoc service verbs that sat
// outside this sweep from the beginning. A verb that never
// reached a write was never in the tally, so nothing ever dipped and a
// floor of 46 would have passed exactly as 44 did. Only the must-reach
// list can hold a named verb.
const sweepFloor = 54

// stubBody answers most reads plausibly enough for a verb to get as far
// as building its write.
//
// database_id is here for `managed backup restore`, which reads the
// backup and puts its database in the request path. Without it the body
// still decodes — api.Backup has no generated UnmarshalJSON, so a
// missing required field is not a decode error — and DatabaseId is left
// empty; the verb then exits at uuid.Parse, BEFORE building its write,
// dropping silently out of this sweep's tally rather than failing it.
// Any field a verb needs in order to REACH its write belongs here for
// the same reason: what this gate cannot reach, it cannot check.
//
// cluster_id is here for byoc's three service `deploy` verbs. Each parses
// it out of the FETCHED database and puts it in the request path, so
// without it all three exited at uuid.Parse with `invalid cluster ID ""`
// — before their write, and so silently. managed's equivalents
// key on the database alone and never noticed.
const stubBody = `{"id":"7f3a5c1e-0000-4000-8000-000000000000",` +
	`"database_id":"7f3a5c1e-0000-4000-8000-000000000000",` +
	`"cluster_id":"7f3a5c1e-0000-4000-8000-00000000000c",` +
	`"name":"mydb","status":"available","services":[],` +
	`"databases":[],"hosts":[],"tasks":[],"clusters":[]}`

// stubNodes answers the cluster-nodes list with one node, which is what
// lets resolveHostIDs pick a target without an explicit --target-nodes.
//
// What matters here is the element COUNT, not the fields: `[{}]` keeps
// all six verbs, `[]` loses them. Only id is read on this path, and
// name is here to make the object a plausible node.
//
// The id must still be a real UUID: resolve.go parses this same field
// for `node logs`, so an id like "…0000n" would reintroduce exactly the
// defect this change removes.
const stubNodes = `[{"id":"7f3a5c1e-0000-4000-8000-000000000002",` +
	`"name":"n1"}]`

// sweepUUIDWithServices addresses a second stubbed database — one that
// already has an mcp service deployed.
//
// One shared body cannot serve both sides of the deploy/update guard.
// `mcp deploy` refuses when a service of its type EXISTS, while
// `mcp update` and `service remove` refuse when none does
// (guardServiceIntent, findService). With a single empty services list
// the deploy verbs reach their writes and the other four exit first;
// filling the list in place would simply swap which four are lost.
//
// Keying on the database ID rather than the path is what separates
// them: deploy and update read the SAME endpoint, so nothing about the
// request itself distinguishes them. The verbs that need a deployed
// service ask for this ID instead, via sweepOverrides.
const sweepUUIDWithServices = "7f3a5c1e-0000-4000-8000-000000000001"

// stubBodyWithServices answers for sweepUUIDWithServices.
//
// Its own `id` must be that UUID, not the default one: applyServices
// parses the id out of the FETCHED body rather than the argument, so a
// body echoing the wrong id would send every service write to the wrong
// database — and a body echoing a non-UUID would lose these verbs at
// uuid.Parse, the same way `backup restore` fell out.
// It carries all three service types, not just mcp: `rag update` and
// `postgrest update` are lost to the same guard as `mcp update`, and a
// list holding only mcp would leave them silent for no reason.
//
// The rag and postgrest entries carry their CONFIG blocks. An update
// merges the flags it was given over the deployed configuration, so a
// typed-but-configless entry passes guardServiceIntent and then fails
// the completeness check with "required to deploy a RAG service" — the
// update is asked for values it should have inherited. A real deployed
// service always carries its config, so an entry without one is not a
// smaller fixture, it is an impossible one.
//
// mcp has no config block because it needs none: unlike RAG and
// PostgREST it has no required config field, so `mcp update` reaches
// its write from the type alone.
//
// The pipeline carries a `tables` entry for the same reason the
// configs exist. RAGPipelineConfig declares Tables as a required
// non-pointer field with no custom UnmarshalJSON, so omitting it
// decodes silently to nil and describes a pipeline the CLI's own
// validatePipelines would refuse to create.
// cluster_id is here for the same reason as in stubBody: byoc's three
// `update` verbs read it off this body too, so a body without one loses
// them at uuid.Parse even once the guard is satisfied.
const stubBodyWithServices = `{"id":"` + sweepUUIDWithServices + `",` +
	`"database_id":"` + sweepUUIDWithServices + `",` +
	`"cluster_id":"7f3a5c1e-0000-4000-8000-00000000000c",` +
	`"name":"mydb","status":"available","services":[` +
	`{"service_type":"mcp","service_id":"a1b2c3d4","state":"running"},` +
	`{"service_type":"rag","service_id":"b2c3d4e5","state":"running",` +
	`"rag_config":{` +
	`"embedding_llm":{"provider":"openai","model":"text-embed"},` +
	`"completion_llm":{"provider":"openai","model":"gpt-4"},` +
	`"pipelines":[{"name":"docs",` +
	`"tables":[{"table":"public.documents"}]}]}},` +
	`{"service_type":"postgrest","service_id":"c3d4e5f6",` +
	`"state":"running","postgrest_config":{` +
	`"db_schemas":"public","db_anon_role":"web_anon"}}],` +
	`"databases":[],"hosts":[],"tasks":[],"clusters":[]}`

// writeSweepConfig gives every module a profile pointing at the stub.
func writeSweepConfig(t *testing.T, home, url string) {
	t.Helper()
	dir := filepath.Join(home, ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "current_profile: default\n" +
		"profiles:\n" +
		"  default:\n" +
		"    starfleet:\n" +
		"      api_url: " + url + "\n" +
		"      client_id: id\n" +
		"      client_secret: secret\n" +
		"    controlplane:\n" +
		"      base_url: " + url + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// annotatedCommandPaths returns every mutating command's path, sorted.
func annotatedCommandPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	for path, c := range actionCommands(t) {
		if cli.IsMutating(c) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// runSweepVerb executes one verb under --dry-run with synthesized
// arguments and reports whether its write was intercepted.
//
// It builds its own tree rather than calling FullTree() because it needs
// the Runtime afterwards. That is the whole point: an INTERCEPTED write
// never reaches the stub, so counting requests that arrive at the server
// scores exactly the verbs this gate cares about as having done nothing,
// `controlplane cluster init` among them.
//
// Errors are expected and ignored: a verb that rejects the synthesized
// values never reaches a request, which the caller accounts for.
func runSweepVerb(t *testing.T, path string) (intercepted bool) {
	t.Helper()
	rt := &module.Runtime{}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			t.Fatal(err)
		}
		root.AddCommand(c)
	}

	args := strings.Fields(strings.TrimPrefix(path, "pgedge "))
	leaf, _, err := root.Find(args)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	// Built into a fresh slice rather than appended onto args: append
	// may reuse args' backing array, and args is derived from the verb
	// path this loop reuses.
	full := make([]string, 0, len(args)+8)
	full = append(full, args...)
	full = append(full, synthesizeArgs(leaf)...)
	full = append(full, "--dry-run")
	root.SetArgs(full)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	// Panics are the repo's forbidden failure mode; a panic here should
	// name the verb rather than take the whole package down.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked under --dry-run: %v", path, r)
		}
	}()
	_ = root.Execute()
	return rt.DryRun != nil && rt.DryRun.Intercepted()
}

// sweepOverrides give the exact arguments for verbs the generic
// synthesis cannot satisfy. Each needs a reason, because an override is
// a place the sweep stops being self-maintaining.
var sweepOverrides = map[string][]string{
	// Refuses outright when --host is set ("cancel is only supported for
	// database tasks"), and the generic synthesis fills every selector
	// flag it finds, --host included.
	//
	// The trailing slash on --database is deliberate: it reaches the wire
	// as db1%2F, which url.URL.Path decodes back into a segment separator
	// and which therefore defeated the interception pattern. A clean UUID
	// here would never exercise that.
	"pgedge controlplane task cancel": {
		sweepUUID, "--database", "db1/", "--force",
	},

	// The four verbs that cannot reach a write against a database with
	// no services: guardServiceIntent turns each `update` away when none
	// of its type is deployed, and `service remove` exits 4 from
	// findService. They address the database that has all three
	// deployed. See sweepUUIDWithServices.
	//
	// The deploy verbs are deliberately NOT here — they need the
	// opposite, and the default stub still gives it to them.
	"pgedge starfleet managed database service remove": {
		sweepUUIDWithServices, "mcp", "--force",
	},
	"pgedge starfleet managed database mcp update": {
		sweepUUIDWithServices,
	},
	"pgedge starfleet managed database rag update": {
		sweepUUIDWithServices,
	},
	"pgedge starfleet managed database postgrest update": {
		sweepUUIDWithServices,
	},

	// byoc's three, turned away by the same guard for the same reason.
	// An override alone did not reach them: unlike managed's four,
	// byoc's verbs read a cluster_id off the database as well, so they
	// needed a stub-body fix before an override could help them.
	"pgedge starfleet byoc database mcp update": {
		sweepUUIDWithServices,
	},
	"pgedge starfleet byoc database rag update": {
		sweepUUIDWithServices,
	},
	"pgedge starfleet byoc database postgrest update": {
		sweepUUIDWithServices,
	},

	// Both modules' `rag deploy`. managed's was silent for the same
	// reason byoc's was and is fixed in the same place.
	"pgedge starfleet byoc database rag deploy":    ragDeployOverride(),
	"pgedge starfleet managed database rag deploy": ragDeployOverride(),

	// controlplane cluster join validates --token and --server-url by hand
	// before building its request, so generic synthesis never reaches
	// its write and the verb sat outside BOTH sweeps: this one, and
	// the empty-body contract in emptybody_stdout_test.go. It is the
	// only operation in any vendored spec that declares 204 with no
	// content, which makes it the one verb that contract most needs.
	//
	// --server-url is a StringArray, so one token is enough; the
	// hand-written check only requires the slice to be non-empty.
	"pgedge controlplane cluster join": {
		"--token", "PGEDGE-sweep", "--server-url", "http://cp-1:3000",
	},
}

// ragDeployOverride is `rag deploy`'s argument list in either module.
//
// Generic synthesis cannot reach this verb, for exactly one reason:
// --pipeline-config names a FILE that must exist and parse. The other
// six required flags accept any value — measured, nothing on either
// module's rag path validates a provider, a model or an api key, and
// synthesis-equivalent values reach the write once the file exists.
//
// The values here are realistic anyway. A fixture describing a request
// the API would reject is the trap this whole change exists to remove.
//
// valueFor does not hand a UUID to the two provider flags -- its id
// test matches whole segments, so "prov-id-er" does not match. These
// explicit values stay regardless: this override exists for
// --pipeline-config either way, and a realistic provider is worth
// more than a synthesized one whatever valueFor would now return.
//
// The file is a checked-in fixture rather than one written at run time,
// because sweepOverrides is consulted from synthesizeArgs, which has no
// testing.T to hang a TempDir on. `go test` runs with the package
// directory as the working directory, so the relative path resolves.
func ragDeployOverride() []string {
	return []string{
		sweepUUID,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "text-embedding-3-small",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "openai",
		"--completion-llm-model", "gpt-4o",
		"--completion-llm-api-key", "sk-c",
		"--pipeline-config", ragPipelineFixture,
	}
}

// ragPipelineFixture is a minimal valid --pipeline-config document. It
// carries a `tables` entry for the reason stubBodyWithServices gives
// above.
const ragPipelineFixture = "testdata/sweep-pipelines.json"

// synthesizeArgs invents plausible positional arguments and required
// flag values for a leaf, from its Use string and flag definitions.
//
// It does not need to satisfy every verb. A verb whose synthesized
// values fail validation simply never reaches a request, and the floor
// in the caller is what keeps that from hiding a broken sweep.
func synthesizeArgs(c *cobra.Command) []string {
	if override, ok := sweepOverrides[c.CommandPath()]; ok {
		return override
	}
	var out []string
	// Positionals: every <token> or [token] in Use after the verb.
	for _, f := range strings.Fields(c.Use)[1:] {
		if f == "[flags]" {
			continue
		}
		out = append(out, valueFor(strings.Trim(f, "<>[]")))
	}
	// Required flags, --force so destructive verbs do not prompt, and any
	// declared SELECTOR flag.
	//
	// The selector list is what makes controlplane verbs reachable at all: cp marks
	// nothing required and validates by hand (see internal/docgen's note),
	// so `controlplane task cancel` rejects a missing --database long before it
	// builds a request. Filling only these named flags rather than every
	// flag keeps the synthesis from feeding "sweepvalue" to a flag with a
	// strict format and pushing a verb that currently works back into
	// silence.
	c.Flags().VisitAll(func(f *pflag.Flag) {
		switch {
		case f.Name == "force":
			out = append(out, "--force")
		case f.Value.Type() == "bool":
			if f.Annotations[cobra.BashCompOneRequiredFlag] != nil {
				out = append(out, "--"+f.Name)
			}
		case f.Annotations[cobra.BashCompOneRequiredFlag] != nil,
			selectorFlags[f.Name]:
			out = append(out, "--"+f.Name, valueFor(f.Name))
		}
	})
	return out
}

// selectorFlags name a resource the verb acts on, so filling them moves
// a verb past its own hand-written validation and on to a request.
var selectorFlags = map[string]bool{
	"database": true, "cluster": true, "host": true, "node": true,
	"cluster-id": true, "database-id": true, "host-id": true,
	"cloud-account-id": true, "backup-store-id": true,
}

// hasIDSegment reports whether name carries "id" as a whole SEGMENT,
// rather than merely containing those two letters somewhere.
//
// The test was once strings.Contains(n, "id"), and "provider" contains
// p-r-o-v-**id**-e-r, so every provider flag was handed a UUID. byoc
// backup create's --provider is REQUIRED and reached its write only
// because nothing validates the value, which means a validation added
// later would have dropped that verb out of the sweep silently -- the
// same class as the --region regression this file already records.
// rag deploy's two vocabulary-checked provider flags were refused
// outright, and were worked around with an explicit override
// rather than narrowing the rule here. "candidate" carries the same
// accident (cand-**id**-ate), and so does "skip-validation", which is
// spared only because bool flags never reach valueFor.
//
// BOTH separators matter, and the ORDER of the separators does not.
// Flags are kebab-case (--database-id) while positionals are mostly
// snake_case (<database_id>) and occasionally kebab ([sub-reference]),
// and valueFor is called for both -- see synthesizeArgs, which trims
// the brackets off a Use token and passes the name straight in.
// Splitting on only the hyphen would stop filling 18 of the 29
// positional tokens in the tree, a larger regression than the one
// being fixed.
//
// The rule is SEGMENT MEMBERSHIP -- not a prefix, not a suffix, not a
// substring of a segment, and not "at either end". Nothing in the tree
// exercises any of those distinctions: no name spells "id" as a
// non-final segment, and none carries a segment that contains "id"
// without being it. So every weaker rule passed the whole derived
// population, and the table below carries SYNTHETIC names for no other
// purpose than to vary those dimensions. Each says so where it sits.
//
// The dimensions below were checked and are NOT gaps, so nobody adds a
// row for them:
//
//   - CASE. No name carries an uppercase character, and valueFor
//     lowercases before this is called, so the case-sensitivity is
//     unreachable by construction rather than merely unexercised. For
//     FLAGS it is also enforced -- TestCommandTreeConformance rejects a
//     non-lowercase flag name -- so that half cannot regress. For
//     POSITIONALS nothing checks the name at all, so it stays an
//     observation.
//
//   - CHARACTER SET. No name contains anything outside [a-z0-9-_], so a
//     wider separator predicate is invisible. Enforced for flags by the
//     same gate, which also rejects an underscore in a flag name;
//     observed for positionals.
//
//   - EMPTY SEGMENTS. No name has a leading, trailing or doubled
//     separator, and an empty segment could not match "id" in any case.
//
//   - SEPARATOR ALTERNATION. Splitting on "-" and falling back to "_"
//     only when no "-" is present survives the whole repository, so
//     "the ORDER of the separators does not matter" is asserted above
//     and NOT pinned. It is recorded rather than rowed because the
//     boundary is degenerate: ZERO names mix the two separators, since
//     flags cannot (the gate forbids "_") and every positional uses one
//     style or the other, never both -- <database_id> is snake and
//     [sub-reference] is kebab.
//
//     REACHABILITY, not absence, is what separates this from the rows
//     below: none of those names exists in the tree either, but a
//     four-segment kebab id flag passes TestCommandTreeConformance
//     untouched, so they are shapes the tree can GROW, while a
//     mixed-separator flag cannot exist at all and a mixed positional
//     would break the convention this file documents. A row here would
//     pin a distinction the tree cannot grow -- until a positional
//     MIXES the two, which nothing enforces.
//
// One weaker rule IS caught by real data: strings.Contains(seg, "id")
// fails on "provider", a single segment containing "id". It is the
// one-sided partial matches that needed synthetic rows.
func hasIDSegment(name string) bool {
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	}) {
		if seg == "id" {
			return true
		}
	}
	return false
}

// valueFor picks a value that will parse for a name.
func valueFor(name string) string {
	n := strings.ToLower(name)
	switch {
	case n == "database" || n == "cluster" || n == "host" ||
		n == "node" || n == "task":
		return sweepUUID
	case hasIDSegment(n):
		return sweepUUID
	case strings.Contains(n, "type"):
		return "mcp"
	// A real backup kind. With "sweepvalue" the kind check refuses
	// first and masks whatever else the sweep was testing on that verb
	// — which is how the ID-flag sweep lost `managed backup create`.
	case n == "kind":
		return "hot"
	// --repository names a backup repository, whose id byoc publishes
	// only as a UUID, and the restore verb now parses it. It does not
	// contain "id", so nothing above catches it and the verb would
	// stop before its write.
	case n == "repository":
		return sweepUUID
	case strings.Contains(n, "role"):
		return "app"
	case strings.Contains(n, "region"):
		return "us-east-1"
	// Exact, not Contains: --volume-size is an integer flag and would
	// take "small" the moment this matched on a substring.
	case n == "size":
		return "small"
	case n == "node-location":
		return "public"
	case strings.Contains(n, "email"):
		return "someone@example.test"
	case strings.Contains(n, "count") || strings.Contains(n, "nodes"):
		return "1"
	default:
		return "sweepvalue"
	}
}

// dedupe collapses repeats so a failure message names each offending
// operation once.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// TestIDSegmentDoesNotMatchAnAccident is the gate for hasIDSegment.
//
// BEFORE EDITING THIS TABLE: each row states its own purpose AT THE
// ROW, and nothing up here counts them or characterises them
// collectively. A fact about the table, stated anywhere but the table,
// has no referent a reader can check -- and every prose defect this
// gate has had was of that shape.
//
// The rule it guards decides WHAT FILLS A FLAG, which decides which
// verbs this sweep reaches at all. The tally cannot stand in for it:
// the floor was slack when the --region regression slipped through, and
// no floor can see a verb that was never in the tally to begin with.
// So the rule gets a test of its own.
//
// Both directions are asserted. A test that only checked the accidents
// were excluded would pass against a rule that matched nothing, which
// would silently unfill every id flag and positional in the tree.
func TestIDSegmentDoesNotMatchAnAccident(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// The accidents. Each contains the letters i-d and names
		// nothing identifying.
		{"provider", false},
		{"embedding-provider", false},
		{"embedding-llm-provider", false},
		{"completion-llm-provider", false},
		{"candidate", false},
		{"skip-validation", false},
		// Synthetic rows. None of these names exists in the tree;
		// each is here because a mutation varying the dimension it
		// names survived the whole suite without it.
		//
		// POSITION, first: a rule matching only a TRAILING "id" passed
		// the entire table and the entire derived population.
		{"id-source", true},
		// POSITION, interior: "id-source" only proves "id" need not be
		// LAST. A rule accepting it at either END survived too, so
		// this is the row that means membership rather than
		// either-end.
		{"backup-id-store", true},
		// ARITY. Every other true row puts "id" at segment index 0, 1
		// or 2, so a rule that stops SCANNING after three segments was
		// indistinguishable from membership and passed the whole
		// repository. Review bounded it: a two-segment window is
		// caught by real data -- cloud-account-id and backup-store-id
		// are live flags with "id" at index 2 -- so the boundary is
		// exactly at three and the gap is index 3 and beyond.
		// Four-segment names already exist in the tree, so an id at
		// index 3 is one rename away.
		{"source-backup-store-id", true},
		// WIDTH: no real name carries a segment that CONTAINS "id"
		// without BEING "id", so a one-sided partial match --
		// HasPrefix(seg, "id") or HasSuffix -- also survived. Under
		// HasPrefix these two would be handed a UUID, which re-opens
		// exactly the "prov-id-er" class.
		{"identity-provider", false},
		{"grid-size", false},
		// SYNTHETIC, and the degenerate arity case: there is no --id
		// flag and no <id> positional. It is the only single-segment
		// true row, so it is what a rule requiring at least two
		// segments fails on -- the FLOOR of the arity dimension whose
		// ceiling source-backup-store-id holds.
		{"id", true},
		// Real identifier flags, kebab-case, all present in the tree.
		{"database-id", true},
		{"cluster-id", true},
		{"cloud-account-id", true},
		{"backup-store-id", true},
		// SYNTHETIC, despite reading like a sibling of the real flags
		// above: there is no --ssh-key-id flag. The tree spells this
		// positional <ssh_key_id>, and the snake form is covered
		// below. Kept because a kebab flag of this shape is one rename
		// away.
		{"ssh-key-id", true},
		// Real identifier POSITIONALS, snake_case. synthesizeArgs
		// trims the brackets off a Use token and passes the bare name
		// in, so a rule splitting only on the hyphen would stop
		// filling every one of these -- a larger regression than the
		// one this rule fixes.
		{"database_id", true},
		{"cluster_id", true},
		{"task_id", true},
		{"backup_id", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIDSegment(tc.name); got != tc.want {
				t.Errorf("hasIDSegment(%q) = %v, want %v",
					tc.name, got, tc.want)
			}
			// The rule's whole purpose is which value comes back, so
			// assert that too rather than trusting the predicate is
			// wired in -- and assert it in BOTH directions.
			//
			// One direction is not enough, measured rather than
			// assumed: a first version of this test checked only that
			// a real id flag GETS the UUID, and reverting valueFor's
			// case to strings.Contains(n, "id") passed it. The
			// predicate was still correct in isolation, so the
			// accident half of the test never looked at valueFor at
			// all, and the exact "prov-id-er" defect walked straight
			// through the gate written for it.
			gotUUID := valueFor(tc.name) == sweepUUID
			if gotUUID != tc.want {
				t.Errorf("valueFor(%q) returned the sweep UUID = %v, "+
					"want %v", tc.name, gotUUID, tc.want)
			}
		})
	}
}
