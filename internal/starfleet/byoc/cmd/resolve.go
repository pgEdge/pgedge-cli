package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
)

// resolveNodeID returns a node's UUID within clusterID for a full
// UUID or a node NAME.
//
// Names are the reason this exists, and they are why this is the one
// resolver left after prefixes were withdrawn. The node-logs
// path takes a node UUID and rejects a name outright ("invalid UUID
// length: 2"), while `cluster get`'s node objects carry no id field at
// all — the UUID is only reachable through ListClusterNodes, which
// returns id alongside name. Without this step the only way to name a
// node would be to run `node list` first and paste a UUID.
//
// So a failed parse here is CONTROL FLOW, not a bad argument: it means
// the caller typed a name, and the answer for a name that matches
// nothing is exit 4 rather than exit 2. That is why this one keeps a
// discarded parse where the other ID inputs now route through
// parseUUIDArg.
//
// Name matching is exact and reads Name, the same field `node list`
// prints in its NAME column and the same one resolveHostIDs matches,
// so what the user sees is what they can type.
func resolveNodeID(ctx context.Context, client *api.ClientWithResponses,
	clusterID uuid.UUID, input string) (uuid.UUID, error) {
	if id, err := uuid.Parse(input); err == nil {
		return id, nil
	}
	resp, err := client.ListClusterNodesWithResponse(
		ctx, clusterID, &api.ListClusterNodesParams{})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("list cluster nodes: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return uuid.UUID{}, err
	}

	var names []string
	if resp.JSON200 != nil {
		for _, n := range *resp.JSON200 {
			if n.Name == input {
				return uuid.Parse(n.Id)
			}
			names = append(names, n.Name)
		}
	}

	// Report the names rather than the ids: a mistyped name is the
	// only mistake that can reach here now that an ID prefix is not a
	// thing, so listing ids would answer a question nobody asked.
	if len(names) == 0 {
		return uuid.UUID{}, newExitError(fmt.Sprintf(
			"cluster %s has no nodes", clusterID), ExitNotFound)
	}
	return uuid.UUID{}, newExitError(fmt.Sprintf(
		"no node named %q in cluster %s — valid names: %s",
		input, clusterID, strings.Join(names, ", ")), ExitNotFound)
}
