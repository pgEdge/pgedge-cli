package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// nodePorts is the neutral shape the systemd-port warning reads, so
// create's and update's distinct generated spec types (DatabaseSpec2,
// DatabaseSpec5) can share one check. It mirrors the nodeHosts/
// serviceConfig adapter pattern above: missingPort/missingPatroni
// replicate the Control Plane's own rule exactly (a node is missing a
// port iff spec.Port == nil && node.Port == nil, same for patroni_port)
// so a spec that already satisfies systemd never triggers a network
// call at all.
type nodePorts struct {
	name           string
	hostIDs        []string
	missingPort    bool
	missingPatroni bool
}

// createSpecNodePorts adapts a create spec into the neutral shape the
// systemd-port warning reads.
func createSpecNodePorts(spec *api.DatabaseSpec2) []nodePorts {
	out := make([]nodePorts, len(spec.Nodes))
	for i, n := range spec.Nodes {
		out[i] = nodePorts{
			name:           n.Name,
			hostIDs:        n.HostIds,
			missingPort:    spec.Port == nil && n.Port == nil,
			missingPatroni: spec.PatroniPort == nil && n.PatroniPort == nil,
		}
	}
	return out
}

// updateSpecNodePorts is createSpecNodePorts for update's own generated
// spec type.
func updateSpecNodePorts(spec *api.DatabaseSpec5) []nodePorts {
	out := make([]nodePorts, len(spec.Nodes))
	for i, n := range spec.Nodes {
		out[i] = nodePorts{
			name:           n.Name,
			hostIDs:        n.HostIds,
			missingPort:    spec.Port == nil && n.Port == nil,
			missingPatroni: spec.PatroniPort == nil && n.PatroniPort == nil,
		}
	}
	return out
}

// anyPortMissing reports whether any node in nodes is missing a port
// or a patroni_port. warnSystemdPortsMissing uses this to skip the
// ListHosts round trip entirely for an already-correct spec (including
// every systemd-detected 'database init' template) or a spec that sets
// both at the top level.
func anyPortMissing(nodes []nodePorts) bool {
	for _, n := range nodes {
		if n.missingPort || n.missingPatroni {
			return true
		}
	}
	return false
}

// warnSystemdPortsMissing is the create/update-side twin of
// detectOrchestrator (Task 7 in the plan): it shares
// classifyOrchestrator's EqualFold matching against the same ListHosts
// data, but keeps its own policy, since the two call sites differ on
// purpose --
//
//   - timeout: this uses the command's real, already-resolved timeout
//     (via the client clientFromCmd already built), never the 2s
//     detection cap, because a warning that fires here is diagnosing a
//     spec the user is about to submit for real, not shaping a
//     generator's output;
//   - failure posture: any error from ListHosts -- transport, 4xx, 5xx
//     -- skips the warning silently. A diagnostic must never turn a
//     working create/update into a failure, so this never returns an
//     error and the caller does not check one;
//   - cost: it calls ListHosts at all only when at least one node is
//     missing a port or patroni_port (anyPortMissing), so every
//     already-correct spec -- including every systemd-detected
//     'database init' template -- pays nothing.
//
// It warns, never refuses: the port/patroni_port rule is server-side
// and version-dependent, so a client-side hard refusal would be wrong
// the day Control Plane relaxes it, and the server's own rejection is
// already legible and per-field.
func warnSystemdPortsMissing(
	rt *module.Runtime, client *api.ClientWithResponses,
	c connConfig, nodes []nodePorts,
) {
	if !anyPortMissing(nodes) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	resp, err := client.ListHostsWithResponse(ctx)
	if err != nil || resp.StatusCode() < 200 || resp.StatusCode() >= 300 ||
		resp.JSON200 == nil {
		return
	}
	orchByHost := make(map[string]string, len(resp.JSON200.Hosts))
	for _, h := range resp.JSON200.Hosts {
		orchByHost[h.Id] = h.Orchestrator
	}
	for _, n := range nodes {
		if !n.missingPort && !n.missingPatroni {
			continue
		}
		for _, hid := range n.hostIDs {
			orch, ok := orchByHost[hid]
			if !ok || !strings.EqualFold(orch, "systemd") {
				continue
			}
			fmt.Fprintln(rt.Stderr, systemdPortWarning(n, hid))
			break // one warning per under-specified node is enough
		}
	}
}

// systemdPortWarning builds the warning naming node n, the systemd
// host it targets, and which of port/patroni_port the spec leaves
// unset.
func systemdPortWarning(n nodePorts, hostID string) string {
	var missing string
	switch {
	case n.missingPort && n.missingPatroni:
		missing = "no port or patroni_port"
	case n.missingPort:
		missing = "no port"
	default:
		missing = "no patroni_port"
	}
	return fmt.Sprintf(
		"⚠ node %q targets host %q (orchestrator: systemd), and the "+
			"spec sets %s. systemd requires both — set a distinct "+
			"port and patroni_port on every node. The top-level pair "+
			"is folded into every node that does not override it, so "+
			"two nodes on one host collide. The server will reject "+
			"this spec.", n.name, hostID, missing)
}
