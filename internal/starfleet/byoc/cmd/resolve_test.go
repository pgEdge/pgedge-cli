package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// resolveNodeID is the ONE resolver left in this module: a node can be
// named, so a failed parse there is control flow rather than a bad
// argument. Every other ID input now goes through parseUUIDArg.
//
// The ID-prefix fallback this file used to pin is gone with prefixes,
// which is why the third sub-test asserts a prefix is REFUSED. Without
// it, reinstating the fallback would leave every test in the repo
// green — the same hole a review found in the other
// direction, when deleting the fallback broke nothing.
func TestResolveNodeIDTakesAUUIDOrAName(t *testing.T) {
	const (
		nodeID  = "065e6997-1111-2222-3333-444455556666"
		cluster = "a1b2c3d4-1111-2222-3333-444455556666"
	)
	// The stub answers only this cluster's node list, so a resolver
	// that listed some other cluster's nodes fails here.
	nodes, err := json.Marshal([]api.ClusterNode{{Id: nodeID, Name: "n1"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(t *testing.T) http.HandlerFunc {
		return testsupport.PathHandler(t,
			"/byoc/v1/clusters/"+cluster+"/nodes", 200, string(nodes))
	}
	cid := uuid.MustParse(cluster)

	t.Run("full uuid skips the API", func(t *testing.T) {
		// nil client proves no list call happens for a full UUID.
		got, err := resolveNodeID(context.Background(), nil, cid, nodeID)
		if err != nil || got.String() != nodeID {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, nodeID)
		}
	})

	t.Run("name resolves via list", func(t *testing.T) {
		got, err := resolveNodeID(
			context.Background(), newTestClient(t, handler(t)), cid, "n1")
		if err != nil || got.String() != nodeID {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, nodeID)
		}
	})

	t.Run("an ID prefix is refused", func(t *testing.T) {
		_, err := resolveNodeID(
			context.Background(), newTestClient(t, handler(t)), cid, "065e6997")
		if err == nil {
			t.Fatal("an ID prefix resolved; prefixes are withdrawn " +
				"and only a full UUID or an exact name may match")
		}
	})

	t.Run("neither UUID nor name is not found", func(t *testing.T) {
		_, err := resolveNodeID(
			context.Background(), newTestClient(t, handler(t)), cid, "zzzz")
		if err == nil {
			t.Fatal("want an error for a value that is neither")
		}
		// Exit 4, not 2: a name that matches nothing is a lookup
		// failure, and only the list could have told us.
		if got := cli.ExitCode(err); got != ExitNotFound {
			t.Errorf("exit %d, want %d — a node NAME that matches "+
				"nothing is not-found, not a usage error",
				got, ExitNotFound)
		}
	})
}

// The cluster and database resolvers are gone, and what replaced them
// is the parse. This pins the half a runtime test cannot reach without
// credentials: parseUUIDArg answers exit 2 and spends no request.
func TestParseUUIDArgRefusesAPrefixWithoutAClient(t *testing.T) {
	for _, tc := range []struct {
		name, arg string
	}{
		{"cluster ID prefix", "a1b2c3d4"},
		{"database ID prefix", "b2c3"},
		{"a name", "prod-west"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseUUIDArg(tc.arg, "cluster ID"); err == nil {
				t.Fatalf("%q was accepted; every ID input takes a "+
					"full UUID", tc.arg)
			} else if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit %d, want %d", got, ExitUsage)
			}
		})
	}

	full := "a1b2c3d4-1111-2222-3333-444455556666"
	got, err := parseUUIDArg(full, "cluster ID")
	if err != nil || got.String() != full {
		t.Fatalf("got (%q, %v), want (%q, nil)", got, err, full)
	}
}
