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
// The node-logs path rejects a name ("invalid UUID length: 2"), and
// `cluster get`'s node objects carry no id, so without this a node
// could be named only by pasting a UUID from `node list`.
//
// A failed parse is control flow, not a bad argument: the caller typed
// a name, and a name matching nothing is exit 4, not 2. That is why
// this does not route through parseUUIDArg.
//
// Matching is exact on Name, the field `node list` prints in its NAME
// column and resolveHostIDs matches.
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

	// Report names, not ids: a mistyped name is the only mistake that
	// can reach here.
	if len(names) == 0 {
		return uuid.UUID{}, newExitError(fmt.Sprintf(
			"cluster %s has no nodes", clusterID), ExitNotFound)
	}
	return uuid.UUID{}, newExitError(fmt.Sprintf(
		"no node named %q in cluster %s — valid names: %s",
		input, clusterID, strings.Join(names, ", ")), ExitNotFound)
}
