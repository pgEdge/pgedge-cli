package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

// --- classifyOrchestrator: pure, offline -----------------------------------

func TestClassifyOrchestrator(t *testing.T) {
	host := func(orch string) api.Host { return api.Host{Orchestrator: orch} }

	tests := []struct {
		name  string
		hosts []api.Host
		want  string
		ok    bool
	}{
		{"empty", nil, "", false},
		{"all systemd", []api.Host{host("systemd"), host("systemd")},
			"systemd", true},
		{"all swarm", []api.Host{host("swarm"), host("swarm")},
			"swarm", true},
		{"casing SystemD", []api.Host{host("SystemD")}, "systemd", true},
		{"casing SWARM", []api.Host{host("SWARM")}, "swarm", true},
		{"mixed", []api.Host{host("swarm"), host("systemd")}, "", false},
		{"unknown only", []api.Host{host("nomad")}, "", false},
		{"unknown among known", []api.Host{host("swarm"), host("nomad")},
			"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := classifyOrchestrator(tt.hosts)
			if got != tt.want || ok != tt.ok {
				t.Errorf("classifyOrchestrator(%v) = (%q, %v), want (%q, %v)",
					tt.hosts, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDistinctOrchestrators(t *testing.T) {
	hosts := []api.Host{
		{Orchestrator: "Swarm"}, {Orchestrator: "systemd"},
		{Orchestrator: "SWARM"},
	}
	got := distinctOrchestrators(hosts)
	want := []string{"swarm", "systemd"}
	if len(got) != len(want) {
		t.Fatalf("distinctOrchestrators = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("distinctOrchestrators = %v, want %v", got, want)
		}
	}
}

// --- helpers for the detection golden tests --------------------------------

// hostsHandler serves a ListHosts 200 response naming one host per
// orchestrator value given (host-1, host-2, ...). It takes *testing.T
// so a marshalling failure is a t.Fatalf, not a panic — the repo bans
// panic, tests included.
func hostsHandler(t *testing.T, orchestrators ...string) http.HandlerFunc {
	t.Helper()
	hosts := make([]api.Host, len(orchestrators))
	for i, o := range orchestrators {
		hosts[i] = api.Host{
			Id:           fmt.Sprintf("host-%d", i+1),
			Orchestrator: o,
		}
	}
	body, err := json.Marshal(api.ListHostsResponse{Hosts: hosts})
	if err != nil {
		t.Fatalf("marshal host list: %v", err)
	}
	return jsonHandler(200, string(body))
}

// closedServerURL starts and immediately closes an httptest server,
// returning a URL nothing listens on any more -- the "unreachable,
// closed listener" case from the plan's golden table, as distinct from
// an always-slow or always-erroring one.
func closedServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

// --- detectOrchestrator / init-level golden tests --------------------------

func TestDatabaseInitDetectsSystemd(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t, "systemd", "systemd", "systemd"))
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()

	spec := decodeSpec(t, got)
	if len(spec.Nodes) != 3 {
		t.Fatalf("want 3 nodes: %+v", spec.Nodes)
	}
	for i, n := range spec.Nodes {
		if n.Port == nil || *n.Port != int64(5432+i) {
			t.Errorf("node %d port = %v, want %d", i, n.Port, 5432+i)
		}
		if n.PatroniPort == nil || *n.PatroniPort != int64(8008+i) {
			t.Errorf("node %d patroni_port = %v, want %d",
				i, n.PatroniPort, 8008+i)
		}
	}
	if !strings.Contains(got, `orchestrator "systemd"`) {
		t.Errorf("header should name systemd:\n%s", got)
	}
	want := "controlplane: detected orchestrator \"systemd\" at " + url +
		" — wrote a systemd-ready template."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
}

func TestDatabaseInitDetectsSwarm(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t, "swarm", "swarm"))
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()

	spec := decodeSpec(t, got)
	for i, n := range spec.Nodes {
		if n.Port != nil {
			t.Errorf("node %d should have no port override: %+v", i, n)
		}
		if n.PatroniPort != nil {
			t.Errorf("node %d should have no patroni_port override: %+v",
				i, n)
		}
	}
	if !strings.Contains(got, "Docker Swarm") {
		t.Errorf("header should name Docker Swarm:\n%s", got)
	}
	if strings.Contains(got, "REQUIRED on systemd") {
		t.Errorf("swarm template should not carry the systemd nag:\n%s",
			got)
	}
	want := "controlplane: detected orchestrator \"swarm\" at " + url +
		" — wrote a swarm template."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
}

func TestDatabaseInitCasingSystemD(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t, "SystemD"))
	if err := runControlplane(t, rt, out, url, "database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	spec := decodeSpec(t, out.String())
	if spec.Nodes[0].Port == nil || *spec.Nodes[0].Port != 5432 {
		t.Errorf("casing variant should still classify as systemd: %+v",
			spec.Nodes[0])
	}
}

func TestDatabaseInitUnreachableFallsBack(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := closedServerURL(t)
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v (want exit 0 on the fallback path)", err)
	}
	got := out.String()
	if !strings.Contains(got, "REQUIRED on systemd") {
		t.Errorf("fallback template should carry the systemd nag:\n%s", got)
	}
	// The advice is "set ... on every node", never "uncomment": the
	// generic template's own inline comment warns that a single
	// top-level pair folds into every node and collides under the
	// Control Plane's per-host uniqueness rule, so a note telling the
	// user to uncomment those two lines would contradict the file it
	// just wrote.
	want := "controlplane: no Control Plane reachable at " + url +
		" — wrote the generic template; on systemd, set a distinct " +
		"port and patroni_port on every node."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
	if strings.Contains(errb.String(), "uncomment") {
		t.Errorf("the fallback note must not advise uncommenting the "+
			"top-level pair: %q", errb.String())
	}
	// Still create-consumable.
	spec := decodeSpec(t, got)
	if spec.DatabaseName == "" || len(spec.Nodes) != 3 {
		t.Errorf("fallback template not create-consumable: %+v", spec)
	}
}

func TestDatabaseInitMixedOrchestratorsFallsBack(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t, "swarm", "systemd"))
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "REQUIRED on systemd") {
		t.Errorf("mixed cluster should fall back to the generic " +
			"template")
	}
	want := "controlplane: hosts report more than one orchestrator (swarm, " +
		"systemd) — wrote the generic template; set a distinct port " +
		"and patroni_port on every node that lands on a systemd host."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
}

func TestDatabaseInitEmptyHostListFallsBack(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t))
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "REQUIRED on systemd") {
		t.Errorf("empty host list should fall back to the generic " +
			"template")
	}
	// A reachable Control Plane answering 200 {"hosts": []} is NOT
	// unreachable, and saying so was a lie the user could not act on.
	// Its own note names the real situation: it answered, and its
	// inventory is empty.
	want := "controlplane: Control Plane at " + url + " reports no hosts — wrote " +
		"the generic template; set a distinct port and patroni_port on " +
		"every node that lands on a systemd host."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
	if strings.Contains(errb.String(), "no Control Plane reachable") {
		t.Errorf("a 200 with an empty host list must not be reported as "+
			"unreachable: %q", errb.String())
	}
}

func TestDatabaseInitListHostsErrorFallsBack(t *testing.T) {
	for _, status := range []int{500, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(status, `{}`))
			if err := runControlplane(t, rt, out, url,
				"database", "init"); err != nil {
				t.Fatalf("init: %v (want exit 0)", err)
			}
			if !strings.Contains(out.String(), "REQUIRED on systemd") {
				t.Errorf("a %d from ListHosts should fall back", status)
			}
		})
	}
}

func TestDatabaseInitUnknownOrchestratorFallsBack(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, hostsHandler(t, "nomad"))
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out.String(), "REQUIRED on systemd") {
		t.Errorf("unknown orchestrator should fall back")
	}
	// One orchestrator is not "more than one". A single unrecognized
	// value gets its own wording; the mixed wording is reserved for
	// genuinely mixed sets (see the test above).
	want := "controlplane: hosts report an unrecognized orchestrator \"nomad\" — " +
		"wrote the generic template; set a distinct port and " +
		"patroni_port on every node that lands on a systemd host."
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want to contain %q", errb.String(), want)
	}
	if strings.Contains(errb.String(), "more than one orchestrator") {
		t.Errorf("a single unrecognized orchestrator must not be "+
			"reported as more than one: %q", errb.String())
	}
}

// TestOrchestratorNoteExactStrings pins all five notes as whole strings
// in one place, so a wording change has to be deliberate and shows up
// as one diff rather than being spread across the per-case tests above
// (which assert the same strings against a live stub, and so also prove
// each detectionResult shape is really reachable from the wire).
func TestOrchestratorNoteExactStrings(t *testing.T) {
	const u = "http://cp.example:3000"
	for _, tc := range []struct {
		name string
		det  detectionResult
		want string
	}{
		{
			name: "systemd detected",
			det: detectionResult{
				orchestrator: "systemd", baseURL: u,
				hostCount: 3, reachable: true,
			},
			want: `controlplane: detected orchestrator "systemd" at ` + u +
				" — wrote a systemd-ready template.",
		},
		{
			name: "swarm detected",
			det: detectionResult{
				orchestrator: "swarm", baseURL: u,
				hostCount: 2, reachable: true,
			},
			want: `controlplane: detected orchestrator "swarm" at ` + u +
				" — wrote a swarm template.",
		},
		{
			name: "genuinely mixed",
			det: detectionResult{
				baseURL: u, hostCount: 2, reachable: true,
				found: []string{"swarm", "systemd"},
			},
			want: "controlplane: hosts report more than one orchestrator (swarm, " +
				"systemd) — wrote the generic template; set a distinct " +
				"port and patroni_port on every node that lands on a " +
				"systemd host.",
		},
		{
			name: "single unrecognized",
			det: detectionResult{
				baseURL: u, hostCount: 2, reachable: true,
				found: []string{"nomad"},
			},
			want: "controlplane: hosts report an unrecognized orchestrator " +
				"\"nomad\" — wrote the generic template; set a distinct " +
				"port and patroni_port on every node that lands on a " +
				"systemd host.",
		},
		{
			name: "reachable but no hosts",
			det: detectionResult{
				baseURL: u, hostCount: 0, reachable: true,
			},
			want: "controlplane: Control Plane at " + u + " reports no hosts — " +
				"wrote the generic template; set a distinct port and " +
				"patroni_port on every node that lands on a systemd host.",
		},
		{
			name: "unreachable",
			det:  detectionResult{baseURL: u},
			want: "controlplane: no Control Plane reachable at " + u + " — wrote " +
				"the generic template; on systemd, set a distinct port " +
				"and patroni_port on every node.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orchestratorNote(tc.det); got != tc.want {
				t.Errorf("orchestratorNote()\n got: %q\nwant: %q",
					got, tc.want)
			}
		})
	}
}

// TestDatabaseInitDetectionRespectsTimeoutCap proves the 2s cap is
// essential, not merely inert: a stub that sleeps well past it (6s,
// versus the resolved connection's 30s default and the cap's 2s) must
// still make init return in a window that is only consistent with the
// cap firing (< 4s -- comfortably above 2s for scheduling slack,
// comfortably below the 6s the stub actually takes to answer). Without
// the cap, the client would simply wait out the stub's full response
// and this assertion would fail even though init "worked" -- catching
// exactly the M10 mutation (using the uncapped connection timeout).
func TestDatabaseInitDetectionRespectsTimeoutCap(t *testing.T) {
	const stubDelay = 6 * time.Second
	const maxAcceptable = 4 * time.Second
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(stubDelay):
		case <-r.Context().Done():
		}
		w.WriteHeader(200)
	})
	rt, out, _ := newTestRuntime(t, "", "text")
	start := time.Now()
	if err := runControlplane(t, rt, out, url,
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if elapsed := time.Since(start); elapsed > maxAcceptable {
		t.Errorf("init took %s, want under %s -- the 2s detection cap "+
			"should have fired well before the stub's %s delay",
			elapsed, maxAcceptable, stubDelay)
	}
	if !strings.Contains(out.String(), "REQUIRED on systemd") {
		t.Errorf("a slow stub should fall back to the generic template")
	}
}

// TestDatabaseInitDetectionBoundsWholeMultiURLProbe is the multi-URL
// half of the timeout guarantee, and the reason the cap is threaded
// through a single context rather than only clamping the per-request
// timeout. selectBaseURLContext walks a multi-URL profile SERIALLY, so
// a per-request cap alone leaves detection costing cap x len(base-urls)
// -- six unreachable candidates at the resolved timeout, not one.
//
// The stubs hang until their client disconnects rather than sleeping a
// fixed span, so the test is driven entirely by the CLI's own deadline
// and finishes the moment that fires: no multi-second sleeps and
// nothing to race. --timeout 200ms resolves the budget below the 2s cap
// (min(200ms, 2s)), which keeps the run fast while leaving the two
// outcomes far apart: ~200ms bounded overall versus ~1.2s if each of
// the six candidates gets its own 200ms.
func TestDatabaseInitDetectionBoundsWholeMultiURLProbe(t *testing.T) {
	const budget = 200 * time.Millisecond
	const candidates = 6
	// 3.5x the bounded expectation, and well under the 1.2s an
	// unbounded serial walk would take.
	const maxAcceptable = 700 * time.Millisecond

	var hits int32
	hang := func(_ http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-r.Context().Done()
	}
	urls := make([]string, candidates)
	for i := range urls {
		urls[i] = newServer(t, hang)
	}

	rt, out, errb := newTestRuntime(t, "", "text")
	start := time.Now()
	err := runControlplaneURLs(t, rt, out,
		[]string{"database", "init", "--timeout", budget.String()},
		urls...)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("init: %v (detection must never fail the command)", err)
	}
	if elapsed > maxAcceptable {
		t.Errorf("init took %s over %d unreachable base URLs, want under "+
			"%s -- the detection budget must bound the WHOLE failover "+
			"walk, not each candidate (%d x %s = %s unbounded)",
			elapsed, candidates, maxAcceptable, candidates, budget,
			time.Duration(candidates)*budget)
	}
	// Positive controls: a detection that silently did no work at all
	// would satisfy the bound above trivially.
	if got := atomic.LoadInt32(&hits); got < 1 {
		t.Errorf("no candidate was probed (%d hits) -- the timing "+
			"assertion above proves nothing", got)
	}
	if elapsed < budget/2 {
		t.Errorf("init returned in %s, faster than half the %s budget -- "+
			"detection cannot have reached the network", elapsed, budget)
	}
	// And it still falls back correctly, naming the first candidate.
	if !strings.Contains(out.String(), "REQUIRED on systemd") {
		t.Errorf("an all-unreachable profile should fall back to the "+
			"generic template:\n%s", out.String())
	}
	if !strings.Contains(errb.String(),
		"controlplane: no Control Plane reachable at "+urls[0]) {
		t.Errorf("stderr = %q, want the unreachable note naming %s",
			errb.String(), urls[0])
	}
}

// TestDatabaseInitNonTTYInteractiveSkipsDetection pins that the -i
// non-TTY usage error is decided before any network work: the test
// harness's stdin is never a terminal, so `init -i` here always takes
// that path, and a server that counts requests proves detection did not
// run first. A pure usage error must not cost a probe.
func TestDatabaseInitNonTTYInteractiveSkipsDetection(t *testing.T) {
	var hits int32
	url := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"hosts":[]}`)
	})
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, url, "database", "init", "-i")
	requireUsageError(t, err)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("non-TTY 'init -i' made %d request(s); the usage error "+
			"must be decided before detection", got)
	}
}

// TestDatabaseInitStdoutStaysPureYAML pins that every one of the notes
// above lands on stderr only: stdout must still decode as a spec and
// must never carry the "controlplane:" prefix.
func TestDatabaseInitStdoutStaysPureYAML(t *testing.T) {
	for name, url := range map[string]string{
		"systemd":     newServer(t, hostsHandler(t, "systemd")),
		"swarm":       newServer(t, hostsHandler(t, "swarm")),
		"unreachable": closedServerURL(t),
		"mixed":       newServer(t, hostsHandler(t, "swarm", "systemd")),
	} {
		t.Run(name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			if err := runControlplane(t, rt, out, url,
				"database", "init"); err != nil {
				t.Fatalf("init: %v", err)
			}
			got := out.String()
			// A line-start check: the note prefix must open the line, not
			// merely appear in it.
			for _, ln := range strings.Split(got, "\n") {
				if strings.HasPrefix(strings.TrimSpace(ln), "controlplane:") {
					t.Errorf("stdout leaked a detection note line %q:\n%s",
						ln, got)
				}
			}
			decodeSpec(t, got) // must still parse
		})
	}
}

// TestDatabaseInitJSONSystemdCarriesIntegerPorts is the -i -o json row:
// port/patroni_port must survive emitSpecJSON as real integers, since
// (unlike YAML comments) they are live spec values once systemd is
// detected. The interactive command path itself requires a real TTY
// (term.IsTerminal), which a test harness cannot provide (see the
// runInterview-level tests elsewhere in this package for the same
// workaround), so this drives runInterview + emitSpecJSON directly with
// a detectionResult shaped like a genuine systemd detection would
// produce it -- detectOrchestrator's own wiring to a live stub is
// covered separately by TestDatabaseInitDetectsSystemd.
func TestDatabaseInitJSONSystemdCarriesIntegerPorts(t *testing.T) {
	det := detectionResult{
		orchestrator: "systemd",
		baseURL:      "http://localhost:3000",
		hostCount:    2,
		reachable:    true,
	}
	in := "db\n2\nn1\nhost-1\nn2\nhost-2\nadmin\n\nn\nn\nn\nn\n"
	var errBuf strings.Builder
	specYAML, err := runInterview(strings.NewReader(in), &errBuf, 2, det)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	var out strings.Builder
	if err := emitSpecJSON(&out, specYAML); err != nil {
		t.Fatalf("emitSpecJSON: %v", err)
	}
	var spec api.DatabaseSpec2
	if err := json.Unmarshal([]byte(out.String()), &spec); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(spec.Nodes) != 2 {
		t.Fatalf("want 2 nodes: %+v", spec.Nodes)
	}
	for i, n := range spec.Nodes {
		if n.Port == nil || *n.Port != int64(5432+i) {
			t.Errorf("node %d port = %v, want %d", i, n.Port, 5432+i)
		}
		if n.PatroniPort == nil || *n.PatroniPort != int64(8008+i) {
			t.Errorf("node %d patroni_port = %v, want %d",
				i, n.PatroniPort, 8008+i)
		}
	}
}

// TestDatabaseInitSystemdAllocationScalesWithNodes covers --nodes 1 and
// --nodes 5: the port/patroni_port allocation must stay distinct and
// contiguous regardless of node count.
func TestDatabaseInitSystemdAllocationScalesWithNodes(t *testing.T) {
	url := newServer(t, hostsHandler(t, "systemd"))
	for _, n := range []int{1, 5} {
		t.Run(fmt.Sprintf("nodes=%d", n), func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			if err := runControlplane(t, rt, out, url, "database", "init",
				"--nodes", fmt.Sprintf("%d", n)); err != nil {
				t.Fatalf("init: %v", err)
			}
			spec := decodeSpec(t, out.String())
			if len(spec.Nodes) != n {
				t.Fatalf("want %d nodes, got %d", n, len(spec.Nodes))
			}
			seenPorts := map[int64]bool{}
			seenPatroni := map[int64]bool{}
			for i, node := range spec.Nodes {
				if node.Port == nil || *node.Port != int64(5432+i) {
					t.Errorf("node %d port = %v, want %d",
						i, node.Port, 5432+i)
				}
				if node.PatroniPort == nil ||
					*node.PatroniPort != int64(8008+i) {
					t.Errorf("node %d patroni_port = %v, want %d",
						i, node.PatroniPort, 8008+i)
				}
				seenPorts[*node.Port] = true
				seenPatroni[*node.PatroniPort] = true
			}
			if len(seenPorts) != n || len(seenPatroni) != n {
				t.Errorf("ports/patroni_ports are not all distinct: "+
					"%v / %v", seenPorts, seenPatroni)
			}
		})
	}
}

// --- the interviewed-port allocation (planSystemdPorts) --------------------

// TestPlanSystemdPorts pins the allocator directly, including the two
// shadowing bugs it exists to prevent. Every row must come out with all
// 2N values distinct, because the Control Plane keys port and
// patroni_port uniqueness per host and `init` cannot know which nodes
// share one.
func TestPlanSystemdPorts(t *testing.T) {
	node := func(port string) nodeValue { return nodeValue{port: port} }
	for _, tc := range []struct {
		name        string
		v           specValues
		count       int
		wantPort    []int // 0 => rendered by the node's own override
		wantPatroni []int
	}{
		{
			name:        "defaults",
			count:       3,
			wantPort:    []int{5432, 5433, 5434},
			wantPatroni: []int{8008, 8009, 8010},
		},
		{
			name:        "interviewed top-level port seeds the sequence",
			v:           specValues{port: "6000"},
			count:       3,
			wantPort:    []int{6000, 6001, 6002},
			wantPatroni: []int{8008, 8009, 8010},
		},
		{
			name: "explicit per-node port is honoured and skipped over",
			v: specValues{
				nodes: []nodeValue{node(""), node("5432"), node("")},
			},
			count:       3,
			wantPort:    []int{5433, 0, 5434},
			wantPatroni: []int{8008, 8009, 8010},
		},
		{
			name:  "an interviewed port near the patroni base cannot collide",
			v:     specValues{port: "8008"},
			count: 3,
			// The patroni sequence shares the claimed set, so it walks
			// past 8008-8010 rather than duplicating them.
			wantPort:    []int{8008, 8009, 8010},
			wantPatroni: []int{8011, 8012, 8013},
		},
		{
			name:        "a non-numeric top-level port falls back to the base",
			v:           specValues{port: "random"},
			count:       2,
			wantPort:    []int{5432, 5433},
			wantPatroni: []int{8008, 8009},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planSystemdPorts(tc.v, tc.count)
			if len(got) != tc.count {
				t.Fatalf("got %d plans, want %d", len(got), tc.count)
			}
			seen := map[int]bool{}
			for i, p := range got {
				if p.port != tc.wantPort[i] {
					t.Errorf("node %d port = %d, want %d",
						i, p.port, tc.wantPort[i])
				}
				if p.patroniPort != tc.wantPatroni[i] {
					t.Errorf("node %d patroni_port = %d, want %d",
						i, p.patroniPort, tc.wantPatroni[i])
				}
				for _, v := range []int{p.port, p.patroniPort} {
					if v == 0 {
						continue
					}
					if seen[v] {
						t.Errorf("value %d allocated twice: %+v", v, got)
					}
					seen[v] = true
				}
			}
		})
	}
}

// TestInterviewedPortSeedsSystemdNodePorts is the end-to-end version of
// the seeding row: an interviewed cluster-wide port of 6000 under a
// detected systemd Control Plane must reach the nodes. Before this, the
// interviewed value was written at the top level and then shadowed on
// every node by an allocated 5432+i, silently discarding the answer.
func TestInterviewedPortSeedsSystemdNodePorts(t *testing.T) {
	det := detectionResult{
		orchestrator: "systemd",
		baseURL:      "http://localhost:3000",
		hostCount:    3,
		reachable:    true,
	}
	in := "db\n3\nn1\nh1\nn2\nh2\nn3\nh3\nadmin\n6000\nn\nn\nn\nn\n"
	var errBuf strings.Builder
	specYAML, err := runInterview(strings.NewReader(in), &errBuf, 3, det)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	spec := decodeSpec(t, specYAML)
	if spec.Port == nil || *spec.Port != 6000 {
		t.Errorf("top-level port = %v, want 6000", spec.Port)
	}
	if len(spec.Nodes) != 3 {
		t.Fatalf("want 3 nodes: %+v", spec.Nodes)
	}
	for i, n := range spec.Nodes {
		if n.Port == nil || *n.Port != int64(6000+i) {
			t.Errorf("node %d port = %v, want %d -- the interviewed port "+
				"must seed the per-node allocation, not be shadowed by it",
				i, n.Port, 6000+i)
		}
		if n.PatroniPort == nil || *n.PatroniPort != int64(8008+i) {
			t.Errorf("node %d patroni_port = %v, want %d",
				i, n.PatroniPort, 8008+i)
		}
	}
}

// TestInterviewedNodePortNeverDuplicated is the other half: a per-node
// port the user answered explicitly must not collide with another node's
// ALLOCATED port. Answering 5432 for node 2 used to sit next to node 1's
// allocated 5432 while the template header promised distinct values.
func TestInterviewedNodePortNeverDuplicated(t *testing.T) {
	det := detectionResult{
		orchestrator: "systemd",
		baseURL:      "http://localhost:3000",
		hostCount:    3,
		reachable:    true,
	}
	// ... admin, blank cluster port, no backups/services/restore, then
	// "customize nodes": n1 no, n2 yes (port 5432, no version, no conf,
	// no hba), n3 no.
	in := "db\n3\nn1\nh1\nn2\nh2\nn3\nh3\nadmin\n\nn\nn\nn\n" +
		"y\nn\ny\n5432\n\nn\nn\nn\n"
	var errBuf strings.Builder
	specYAML, err := runInterview(strings.NewReader(in), &errBuf, 3, det)
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	spec := decodeSpec(t, specYAML)
	if len(spec.Nodes) != 3 {
		t.Fatalf("want 3 nodes: %+v\n%s", spec.Nodes, specYAML)
	}
	if spec.Nodes[1].Port == nil || *spec.Nodes[1].Port != 5432 {
		t.Errorf("node 1's explicit answer must survive verbatim: %v",
			spec.Nodes[1].Port)
	}
	seen := map[int64]bool{}
	for i, n := range spec.Nodes {
		if n.Port == nil || n.PatroniPort == nil {
			t.Fatalf("node %d missing a port under systemd: %+v\n%s",
				i, n, specYAML)
		}
		for _, v := range []int64{*n.Port, *n.PatroniPort} {
			if seen[v] {
				t.Errorf("value %d appears on two nodes -- the allocation "+
					"must skip values an explicit answer already "+
					"claimed:\n%s", v, specYAML)
			}
			seen[v] = true
		}
	}
	// And exactly one `port:` key per node -- the override, or an
	// allocated value, never both.
	if n := strings.Count(specYAML, "    port: "); n != 3 {
		t.Errorf("want exactly 3 per-node port keys, got %d:\n%s",
			n, specYAML)
	}
}
