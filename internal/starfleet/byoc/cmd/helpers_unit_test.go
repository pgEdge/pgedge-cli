package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
)

// TestFindService pins the byoc mirror of managed's helper of the same
// name (managed/cmd/helpers_unit_test.go): a deployed service of the
// given type is found, an undeployed one is not, and a database with no
// services at all does not panic.
func TestFindService(t *testing.T) {
	db := &api.Database{
		Services: &[]api.Service{
			{ServiceType: api.ServiceServiceTypeRag},
		},
	}
	if findService(db, api.ServiceServiceTypeRag) == nil {
		t.Error("deployed service not found")
	}
	if findService(db, api.ServiceServiceTypeMcp) != nil {
		t.Error("an undeployed service was found")
	}
	if findService(&api.Database{}, api.ServiceServiceTypeMcp) != nil {
		t.Error("a service was found on a database with none")
	}
}

// TestGuardServiceIntent covers all four intent×presence combinations
// plus the different-type-deployed case: deploy refuses an
// existing service of the SAME type, and must not be fooled by a
// different type being present. Mirrors managed's test of the same
// name over its own generated types.
func TestGuardServiceIntent(t *testing.T) {
	group := "pgedge starfleet byoc database mcp"
	present := &api.Database{
		Id: testDatabaseID,
		Services: &[]api.Service{
			{
				ServiceType: api.ServiceServiceTypeMcp,
				State:       nullable.NewNullableWithValue("running"),
			},
		},
	}
	absent := &api.Database{Id: testDatabaseID}
	otherType := &api.Database{
		Id: testDatabaseID,
		Services: &[]api.Service{
			{ServiceType: api.ServiceServiceTypeRag},
		},
	}

	cases := []struct {
		name     string
		db       *api.Database
		intent   serviceIntent
		wantErr  bool
		wantText string
	}{
		{"deploy, absent: allowed", absent, intentDeploy, false, ""},
		{"deploy, present: refused", present, intentDeploy, true,
			"mcp update"},
		{"update, present: allowed", present, intentUpdate, false, ""},
		{"update, absent: refused", absent, intentUpdate, true,
			"mcp deploy"},
		{"deploy, different type deployed: allowed",
			otherType, intentDeploy, false, ""},
		{"update, different type deployed: refused",
			otherType, intentUpdate, true, "mcp deploy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardServiceIntent(&module.Runtime{},
				tc.db, api.ServiceServiceTypeMcp, tc.intent, group)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
				t.Errorf("want exit %d, got %v", ExitGeneral, err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error %q does not mention %q", err, tc.wantText)
			}
		})
	}
}

// TestGuardServiceIntentDeployMessageNamesTheState pins that the
// deploy-on-existing refusal names the deployed service's state — free
// information off the same GET, and the first thing a user asks.
func TestGuardServiceIntentDeployMessageNamesTheState(t *testing.T) {
	db := &api.Database{
		Id: testDatabaseID,
		Services: &[]api.Service{
			{
				ServiceType: api.ServiceServiceTypeMcp,
				State:       nullable.NewNullableWithValue("running"),
			},
		},
	}
	err := guardServiceIntent(&module.Runtime{}, db, api.ServiceServiceTypeMcp, intentDeploy,
		"pgedge starfleet byoc database mcp")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("error does not name the deployed state: %v", err)
	}
}
