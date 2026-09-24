package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestNodeLocationsMatchTheSpecEnum pins nodeLocations to the vendored
// byoc contract.
//
// The generated enum type has a Valid() method but no way to
// enumerate its members, so a Valid()-only check could not notice a
// third location arriving and the CLI would go on refusing, at exit 2,
// a value the API accepts. Reading the spec is the only assertion that
// fails in that direction.
//
// An absent or empty enum is a hard failure rather than a skip: a test
// that has lost its truth source passes against anything, which is how
// a mirrored list rots without telling anyone.
func TestNodeLocationsMatchTheSpecEnum(t *testing.T) {
	path := filepath.Join(
		"..", "..", "..", "..", "openapi", "byoc.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	// CreateClusterInput, not Cluster: this is the vocabulary the
	// create body accepts, which is the one the flag writes into. The
	// response schema declares the same two today and could legitimately
	// diverge.
	schema, ok := doc.Components.Schemas["CreateClusterInput"]
	if !ok {
		t.Fatalf("no CreateClusterInput schema in %s; this test has "+
			"lost its truth source and would pass against anything",
			path)
	}
	prop, ok := schema.Properties["node_location"]
	if !ok {
		t.Fatalf("no CreateClusterInput.node_location in %s; this "+
			"test has lost its truth source", path)
	}
	if len(prop.Enum) == 0 {
		t.Fatalf("CreateClusterInput.node_location carries no enum "+
			"in %s; this test has lost its truth source", path)
	}

	want := append([]string{}, prop.Enum...)
	sort.Strings(want)
	if !slices.Equal(nodeLocations, want) {
		t.Errorf("nodeLocations = %v, spec declares %v",
			nodeLocations, want)
	}
}
