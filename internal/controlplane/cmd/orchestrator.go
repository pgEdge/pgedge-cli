package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// detectOrchestratorTimeoutCap bounds how long 'database init' will
// spend detecting a Control Plane's orchestrator IN TOTAL -- not per
// request. A multi-URL profile whose servers are all unreachable
// therefore still costs this much once, never this much per candidate.
//
// This is a CHOSEN bound, not a measurement: long enough for a
// loopback or same-VPC Control Plane to answer (the common case --
// 'make dev-detached' serves :3000 with nothing else listening), short
// enough that a wrong --base-url does not make a generator feel hung.
// Revisit if live testing says otherwise.
// It is a CAP, not a fixed wait: detectOrchestrator uses
// min(resolved-timeout, this), so a user who set a shorter --timeout
// still gets that shorter value.
const detectOrchestratorTimeoutCap = 2 * time.Second

// classifyOrchestrator reports the orchestrator every host in hosts
// shares, matching case-insensitively (strings.EqualFold) against the
// two literals Control Plane defines
// (config.OrchestratorSystemD = "systemd"; Docker Swarm reports
// "swarm"). It returns ok=false -- never a guess -- for an empty list,
// a single unrecognised value, or more than one distinct value:
// Host.Orchestrator is an unenumerated free string in the vendored
// spec, and a mixed cluster has no single correct template.
func classifyOrchestrator(hosts []api.Host) (orch string, ok bool) {
	if len(hosts) == 0 {
		return "", false
	}
	var canonical string
	for i, h := range hosts {
		var this string
		switch {
		case strings.EqualFold(h.Orchestrator, "systemd"):
			this = "systemd"
		case strings.EqualFold(h.Orchestrator, "swarm"):
			this = "swarm"
		default:
			return "", false
		}
		if i == 0 {
			canonical = this
		} else if this != canonical {
			return "", false
		}
	}
	return canonical, true
}

// distinctOrchestrators returns the sorted, de-duplicated, lower-cased
// orchestrator values reported across hosts, for naming in the mixed/
// unknown stderr note. Lower-cased because the match is EqualFold and
// the note should read the same regardless of a host's exact casing.
func distinctOrchestrators(hosts []api.Host) []string {
	seen := map[string]bool{}
	for _, h := range hosts {
		seen[strings.ToLower(h.Orchestrator)] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// detectionResult is everything 'database init' learns while trying to
// detect an orchestrator, threaded into specValues so the template's
// header and per-node port fields can react, and into orchestratorNote
// so the right one of the five pinned stderr lines gets picked.
//
// orchestrator is "systemd", "swarm", or "" when nothing was
// conclusively detected (unreachable, empty host list, mixed
// orchestrators, or an unrecognised one). reachable distinguishes "the
// Control Plane answered but gave an inconclusive answer" (empty host
// list, mixed, unknown) from "nothing answered at all" (transport
// error, non-2xx, nil body) -- all fall back to the same generic
// template, but they print different stderr notes. found carries the
// distinct orchestrator values seen, and its length is what separates
// the two inconclusive-but-reachable notes: more than one value is a
// genuinely mixed cluster, exactly one is a single orchestrator this
// CLI does not recognise, and none at all means the Control Plane
// answered with an empty host inventory. It is always empty when
// reachable is false.
type detectionResult struct {
	orchestrator string
	baseURL      string
	hostCount    int
	reachable    bool
	found        []string
}

// detectOrchestrator resolves a Control Plane connection the same way
// every other controlplane verb does (resolveConnection -> selectBaseURLContext),
// then asks it for its hosts and classifies their orchestrator. It
// NEVER returns an error: 'database init' must stay usable with no
// Control Plane reachable anywhere, so every failure path here -- a
// multi-URL profile with nothing live, a transport error, a non-2xx
// status, a nil body -- reports ok=false via
// detectionResult.orchestrator == "" rather than surfacing an error.
//
// It also never blocks longer than
// min(resolved-timeout, detectOrchestratorTimeoutCap) IN TOTAL. That
// bound is a single deadline threaded through both network steps, not a
// per-request timeout: the failover walk over a multi-URL profile and
// the ListHosts call that follows it share one context, so N
// unreachable candidates cost the budget once between them rather than
// once each. Capping only the per-request timeout would leave an N-URL
// profile costing N times the cap, which is what this shape exists to
// prevent.
//
// Probing the implicit default (no controlplane profile configured) is
// deliberate and safe: with nothing configured, selectBaseURLContext
// returns defaultBaseURL ("http://localhost:3000") without probing, so
// detection tries localhost for free, and a loopback address with
// nothing listening refuses instantly -- there is no firewall-drop
// path to hang on. A remote URL only ever comes from an explicit
// profile or flag, and that is what the timeout cap protects against.
func detectOrchestrator(
	rt *module.Runtime, cmd *cobra.Command,
) detectionResult {
	// Degrade rather than propagate: this function's contract is that
	// 'database init' stays usable with no Control Plane reachable,
	// and a profile whose timeout: will not parse is as unusable as
	// an unreachable one. Every verb that actually opens a connection
	// reports it.
	//
	// resolveConnection returns a populated config even when it
	// errors, so the stderr note can still name a URL -- the same
	// courtesy the selectBaseURLContext branch below extends, and the
	// note is the user's only signal on this path.
	c, err := resolveConnection(rt, cmd)
	if err != nil {
		u := defaultBaseURL
		if len(c.baseURLs) > 0 {
			u = c.baseURLs[0]
		}
		return detectionResult{baseURL: u}
	}
	if c.timeout > detectOrchestratorTimeoutCap {
		c.timeout = detectOrchestratorTimeoutCap
	}
	// One deadline for the whole detection, shared by the failover walk
	// and the ListHosts call below.
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	baseURL, err := selectBaseURLContext(ctx, rt, c)
	if err != nil {
		// selectBaseURL only errors when every candidate in a
		// multi-URL profile failed to answer /v1/version. There is no
		// live baseURL to name in the stderr note, so fall back to
		// the first configured candidate purely for that purpose.
		u := defaultBaseURL
		if len(c.baseURLs) > 0 {
			u = c.baseURLs[0]
		}
		return detectionResult{baseURL: u}
	}
	client, err := newAPIClient(rt, c, baseURL)
	if err != nil {
		return detectionResult{baseURL: baseURL}
	}
	resp, err := client.ListHostsWithResponse(ctx)
	if err != nil || resp.StatusCode() < 200 || resp.StatusCode() >= 300 ||
		resp.JSON200 == nil {
		return detectionResult{baseURL: baseURL}
	}
	hosts := resp.JSON200.Hosts
	orch, ok := classifyOrchestrator(hosts)
	if !ok {
		return detectionResult{
			baseURL:   baseURL,
			hostCount: len(hosts),
			reachable: true,
			found:     distinctOrchestrators(hosts),
		}
	}
	// No dry-run ledger entry here, deliberately. detectOrchestrator's
	// only callers are in `controlplane database init`, which sends no request of
	// its own and therefore does not take --dry-run, so rt.DryRun is
	// always nil at this point. A Pass call here would be dead code kept
	// alive by a test that injected a Run by hand — which is what the
	// first version of this did. When a dry-runnable verb starts using
	// detection, add the entry then, with a test that reaches it for
	// real.
	return detectionResult{
		orchestrator: orch,
		baseURL:      baseURL,
		hostCount:    len(hosts),
		reachable:    true,
	}
}

// orchestratorNote returns the exact, test-pinned stderr line 'init'
// prints describing what orchestrator detection found. There are six,
// one per row of the detection table: systemd detected, swarm detected,
// and the four distinguishable ways detection can come back
// inconclusive.
//
// internal/controlplane/llms.txt's init section paraphrases these, so re-word
// them there too. No gate enforces that pairing;
// TestOrchestratorNoteExactStrings pins the strings themselves, which
// is what makes a change here impossible to make by accident.
//
// Each fallback note ends in the action that is actually correct for
// the template it just wrote. The generic template comments BOTH port
// and patroni_port at the top level and says, inline, that systemd
// needs distinct values on every node -- because the Control Plane
// folds a top-level port into every node that does not override it and
// then requires per-host uniqueness, so literally uncommenting the two
// top-level lines produces colliding ports the moment two nodes share a
// host. So these notes say "set ... on every node", never "uncomment".
func orchestratorNote(det detectionResult) string {
	switch {
	case det.orchestrator == "systemd":
		return fmt.Sprintf(
			"controlplane: detected orchestrator %q at %s — wrote a systemd-ready "+
				"template.", det.orchestrator, det.baseURL)
	case det.orchestrator == "swarm":
		return fmt.Sprintf(
			"controlplane: detected orchestrator %q at %s — wrote a swarm "+
				"template.", det.orchestrator, det.baseURL)
	case det.reachable && len(det.found) > 1:
		return fmt.Sprintf(
			"controlplane: hosts report more than one orchestrator (%s) — wrote "+
				"the generic template; set a distinct port and "+
				"patroni_port on every node that lands on a systemd "+
				"host.",
			// api.Host.Orchestrator, lowercased -- server text, and
			// this branch is the one that renders it with %s rather
			// than %q. The note reaches stderr through Fprintln, so
			// the builder escapes.
			output.Sanitize(strings.Join(det.found, ", ")))
	case det.reachable && len(det.found) == 1:
		return fmt.Sprintf(
			"controlplane: hosts report an unrecognized orchestrator %q — wrote "+
				"the generic template; set a distinct port and "+
				"patroni_port on every node that lands on a systemd "+
				"host.", det.found[0])
	case det.reachable:
		return fmt.Sprintf(
			"controlplane: Control Plane at %s reports no hosts — wrote the "+
				"generic template; set a distinct port and patroni_port "+
				"on every node that lands on a systemd host.",
			det.baseURL)
	default:
		return fmt.Sprintf(
			"controlplane: no Control Plane reachable at %s — wrote the generic "+
				"template; on systemd, set a distinct port and "+
				"patroni_port on every node.", det.baseURL)
	}
}
