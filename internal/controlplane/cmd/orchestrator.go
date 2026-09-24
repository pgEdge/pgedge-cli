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

// detectOrchestratorTimeoutCap bounds the whole of `database init`'s
// orchestrator detection, not each request, and caps rather than
// replaces a shorter --timeout. It is chosen, not measured: long
// enough for a loopback or same-VPC Control Plane to answer, short
// enough that a wrong --base-url does not make a generator feel hung.
const detectOrchestratorTimeoutCap = 2 * time.Second

// classifyOrchestrator reports the orchestrator every host shares.
// It returns ok=false rather than guessing for an empty list, an
// unrecognised value or a mix: Host.Orchestrator is an unenumerated
// free string in the vendored spec, and a mixed cluster has no single
// correct template. The Control Plane itself defines "systemd"
// (config.OrchestratorSystemD); Docker Swarm hosts report "swarm".
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

// distinctOrchestrators lower-cases because classifyOrchestrator
// matches with EqualFold, so the stderr note should not depend on a
// host's casing.
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

// detectionResult feeds the template (specValues) and the choice among
// orchestratorNote's six stderr lines.
//
// orchestrator is "" unless detection was conclusive. reachable
// separates an inconclusive answer from no answer (transport error,
// non-2xx, nil body): both get the generic template but different
// notes. Among reachable answers, len(found) picks the note: more than
// one is a mixed cluster, one is an unrecognised orchestrator, none is
// an empty host inventory.
type detectionResult struct {
	orchestrator string
	baseURL      string
	hostCount    int
	reachable    bool
	found        []string
}

// detectOrchestrator never returns an error: 'database init' must stay
// usable with no Control Plane reachable, so every failure leaves
// orchestrator "".
//
// The failover walk and ListHosts share one deadline, so N unreachable
// candidates cost the cap once, not N times.
//
// With no profile configured it tries defaultBaseURL, which is safe: a
// loopback address with nothing listening refuses instantly. A remote
// URL only comes from a profile or flag, which is what the cap is for.
func detectOrchestrator(
	rt *module.Runtime, cmd *cobra.Command,
) detectionResult {
	// A profile whose timeout: will not parse is as unusable as an
	// unreachable server, so degrade; every verb that opens a
	// connection reports it. c is populated even on error, so the
	// stderr note can still name a URL.
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
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	baseURL, err := selectBaseURLContext(ctx, rt, c)
	if err != nil {
		// Every candidate failed /v1/version; name the first in the
		// stderr note.
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
	// No dry-run ledger entry: the only caller, `database init`, takes
	// no --dry-run, so rt.DryRun is always nil here. Add one when a
	// dry-runnable verb uses detection, with a test that reaches it.
	return detectionResult{
		orchestrator: orch,
		baseURL:      baseURL,
		hostCount:    len(hosts),
		reachable:    true,
	}
}

// orchestratorNote returns the stderr line 'init' prints about
// detection. TestOrchestratorNoteExactStrings pins all six;
// internal/controlplane/llms/database.txt paraphrases them, and no
// gate keeps the two in step.
//
// The fallback notes say "set ... on every node", never "uncomment":
// the Control Plane folds a top-level port into every node that does
// not override it, then requires per-host uniqueness, so uncommenting
// the template's top-level port lines collides as soon as two nodes
// share a host.
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
			// Server text rendered with %s rather than %q, and
			// Fprintln does not escape, so sanitize here.
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
