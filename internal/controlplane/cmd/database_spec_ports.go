package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// nodePorts is the neutral shape the systemd-port warning reads, so
// create's DatabaseSpec2 and update's DatabaseSpec5 share one check,
// like nodeHosts and serviceConfig in database_spec.go. The missing
// flags copy the Control Plane's rule exactly (spec.Port == nil &&
// node.Port == nil, likewise patroni_port), so a spec that satisfies
// systemd never triggers a network call.
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

// anyPortMissing reports whether any node lacks a port or patroni_port.
func anyPortMissing(nodes []nodePorts) bool {
	for _, n := range nodes {
		if n.missingPort || n.missingPatroni {
			return true
		}
	}
	return false
}

// warnSystemdPortsMissing is the create/update counterpart of
// detectOrchestrator. It reads the same ListHosts data and matches
// "systemd" case-insensitively as classifyOrchestrator does, with its
// own policy:
//
//   - the command's resolved timeout, not the 2s detection cap, since
//     it diagnoses a spec about to be submitted for real;
//   - any ListHosts failure (transport, 4xx, 5xx) skips the warning
//     silently, so a diagnostic never fails a working create or update;
//   - ListHosts runs only when anyPortMissing, so a correct spec,
//     including every systemd 'database init' template, costs nothing.
//
// It warns rather than refuses: the rule is server-side and
// version-dependent, so a refusal would be wrong the day Control Plane
// relaxes it, and the server's rejection is already legible per field.
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
