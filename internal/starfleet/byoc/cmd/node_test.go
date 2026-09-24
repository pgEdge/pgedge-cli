package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewNodeCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewNodeCmd(rt)

	if cmd.Use != "node" {
		t.Errorf("Use = %q, want \"node\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "nodes" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"nodes\"", cmd.Aliases)
	}

	hasList := false
	for _, sub := range cmd.Commands() {
		if strings.Fields(sub.Use)[0] == "list" {
			hasList = true
		}
	}
	if !hasList {
		t.Error("node command missing subcommand \"list\"")
	}
}

func nodesHandler(nodes []api.ClusterNode) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)
	}
}

func TestResolveHostIDs_SingleNodeAutoSelect(t *testing.T) {
	client := newTestClient(t, nodesHandler([]api.ClusterNode{
		{Id: "aaa-111", Name: "n1"},
	}))

	ids, err := resolveHostIDs(client, uuid.New(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "aaa-111" {
		t.Errorf("got %v, want [aaa-111]", ids)
	}
}

func TestResolveHostIDs_MultiNodeRequiresFlag(t *testing.T) {
	client := newTestClient(t, nodesHandler([]api.ClusterNode{
		{Id: "aaa-111", Name: "n1"},
		{Id: "bbb-222", Name: "n2"},
	}))

	_, err := resolveHostIDs(client, uuid.New(), nil)
	if err == nil {
		t.Fatal("expected error for multi-node without --target-nodes")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitGeneral)
	}
}

func TestResolveHostIDs_NameResolution(t *testing.T) {
	client := newTestClient(t, nodesHandler([]api.ClusterNode{
		{Id: "aaa-111", Name: "n1"},
		{Id: "bbb-222", Name: "n2"},
		{Id: "ccc-333", Name: "n3"},
	}))

	ids, err := resolveHostIDs(client, uuid.New(), []string{"n2", "n1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 || ids[0] != "bbb-222" || ids[1] != "aaa-111" {
		t.Errorf("got %v, want [bbb-222 aaa-111]", ids)
	}
}

func TestResolveHostIDs_InvalidName(t *testing.T) {
	client := newTestClient(t, nodesHandler([]api.ClusterNode{
		{Id: "aaa-111", Name: "n1"},
	}))

	_, err := resolveHostIDs(client, uuid.New(), []string{"n99"})
	if err == nil {
		t.Fatal("expected error for invalid node name")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("exit code = %d, want %d", ee.Code(), ExitGeneral)
	}
}

// Keeps the context import in use (compile-time check).
var _ = context.Background
