package clitest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specDoc is the minimal shape needed to assert that a vendored
// OpenAPI spec carries canonical, product-scoped paths.
type specDoc struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]any `yaml:"paths"`
}

// starfleetSpecs are the three specs captured from the API's
// per-product public contracts. control-plane.json is deliberately
// absent: the self-hosted Control Plane is a different API with its own
// provenance.
var starfleetSpecs = []string{"byoc.yaml", "managed.yaml", "account.yaml"}

func specPath(name string) string {
	return filepath.Join("..", "..", "openapi", name)
}

func loadSpec(t *testing.T, name string) specDoc {
	t.Helper()
	raw, err := os.ReadFile(specPath(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc specDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

// TestVendoredSpecsAreCanonical guards the vendored specs against
// regressing to the retired bare `/v1` surface. Every Starfleet path is
// namespaced by product, and `prefix` below is asserted against *every*
// path in the file — so a single bare `/v1/...` path reintroduced by a
// re-vendor fails the build rather than 404ing in the field.
//
// The bare paths are not merely deprecated: the API retired the
// whole surface, along with the rewriting layer that had kept the old
// shapes reachable, so every one of them 404s upstream now. Of what used
// to sit there, `/v1/backups*` was legacy Developer-edition surface BYOC
// never used — three of its four operations are `/managed/v1/backups*`
// today and are managed's to cover, and the other was deleted;
// `/v1/available-clusters` was deleted with Developer Edition; and
// `/v1/sizes*` is the managed size catalog, which had been misfiled into
// the byoc bundle upstream. Do not "restore" any of them here.
//
// This is the whole-file invariant; control-plane.json is deliberately
// absent, since the self-hosted Control Plane owns its own `/v1`.
func TestVendoredSpecsAreCanonical(t *testing.T) {
	cases := []struct {
		file   string
		prefix string
		want   []string
		unwant []string
	}{
		{
			file:   "byoc.yaml",
			prefix: "/byoc/v1/",
			want: []string{
				"/byoc/v1/databases",
				"/byoc/v1/databases/{id}",
				"/byoc/v1/clusters",
				"/byoc/v1/backup-stores",
			},
			unwant: []string{
				"/databases", "/clusters", "/backups",
				"/v1/databases", "/v1/clusters",
				// deleted, not moved — see the doc comment
				"/v1/backups", "/v1/backups/{id}",
				"/v1/backups/{id}/restore", "/v1/backups/{id}/url",
				"/v1/available-clusters", "/v1/sizes", "/v1/sizes/{id}",
				"/byoc/v1/backups", "/byoc/v1/available-clusters",
				"/byoc/v1/sizes",
			},
		},
		{
			file:   "managed.yaml",
			prefix: "/managed/v1/",
			want: []string{
				"/managed/v1/databases",
				"/managed/v1/databases/{id}/size",
				// The backup surface, relocated off bare /v1 by the API.
				// Pinned here so a re-vendor that lost it fails
				// rather than quietly shrinking the managed contract.
				"/managed/v1/backups",
				"/managed/v1/backups/{id}",
				// Restore is keyed on the DATABASE, not the backup —
				// the API retired the backup-keyed route outright.
				// Pinned in both directions: the old key
				// also sits in unwant, so a re-vendor that rolled the
				// pin back would fail here rather than resurrect a
				// route the API answers 405 for.
				"/managed/v1/databases/{id}/restore",
			},
			unwant: []string{
				"/databases", "/v1/managed/databases",
				"/managed/v1/backups/{id}/restore",
			},
		},
		{
			file:   "account.yaml",
			prefix: "/account/v1/",
			want: []string{
				"/account/v1/oauth/token", "/account/v1/tenants",
				"/account/v1/invites", "/account/v1/memberships",
			},
			unwant: []string{
				"/oauth/token", "/tenants", "/invites",
				"/memberships",
				"/v1/oauth/token", "/v1/tenants", "/v1/invites",
				"/v1/memberships",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			doc := loadSpec(t, tc.file)

			if len(doc.Servers) == 0 {
				t.Fatal("spec declares no servers")
			}
			if got := doc.Servers[0].URL; got != "https://api.pgedge.com" {
				t.Errorf("server URL = %q, want https://api.pgedge.com",
					got)
			}
			for _, p := range tc.want {
				if _, ok := doc.Paths[p]; !ok {
					t.Errorf("missing expected path %s", p)
				}
			}
			for _, p := range tc.unwant {
				if _, ok := doc.Paths[p]; ok {
					t.Errorf("legacy path %s must not appear", p)
				}
			}
			// The whole-file invariant: no path escapes the product
			// prefix, so a bare /v1 path cannot creep back in via a
			// re-vendor or a hand edit.
			if len(doc.Paths) == 0 {
				t.Fatal("spec declares no paths")
			}
			for p := range doc.Paths {
				if !strings.HasPrefix(p, tc.prefix) {
					t.Errorf("path %s is outside %s; the bare /v1 "+
						"surface is retired", p, tc.prefix)
				}
			}
		})
	}
}

// findPgEdgeExtensions returns every `x-pgedge-*` key in a decoded
// spec, as slash-joined locations.
//
// x-go-type is not matched: it is oapi-codegen's type override, a
// generator hint with no bearing on what the API exposes.
func findPgEdgeExtensions(node any, where string) []string {
	var found []string
	switch v := node.(type) {
	case map[string]any:
		for name, child := range v {
			if strings.HasPrefix(name, "x-pgedge-") {
				found = append(found, where+"/"+name)
			}
			found = append(found,
				findPgEdgeExtensions(child, where+"/"+name)...)
		}
	case []any:
		for i, child := range v {
			found = append(found, findPgEdgeExtensions(
				child, fmt.Sprintf("%s/%d", where, i))...)
		}
	}
	return found
}

// TestVendoredSpecsCarryNoVisibilityMarkers is the committed-artefact
// half of the public-surface contract.
//
// cmd/vendorspec refuses to vendor a published contract carrying any
// pgEdge extension, but that only runs when someone re-captures. This
// runs on every build, over what is actually checked in, so a hand
// edit that pastes a path back in — bringing its `x-pgedge-plan` or
// `x-pgedge-omit-public` marker with it — fails here instead of
// shipping a client for an endpoint customers cannot call.
func TestVendoredSpecsCarryNoVisibilityMarkers(t *testing.T) {
	for _, name := range starfleetSpecs {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(specPath(name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			var doc any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			if found := findPgEdgeExtensions(doc, ""); len(found) > 0 {
				sort.Strings(found)
				t.Errorf("pgEdge visibility marker(s) in %s: %s\n"+
					"these are stripped by the public filter, so one "+
					"here means the spec was hand-edited; re-run "+
					"`make vendor-spec`",
					name, strings.Join(found, ", "))
			}
		})
	}

	// Positive control: an absence proves nothing unless the search
	// can find what it is looking for.
	t.Run("control", func(t *testing.T) {
		planted := map[string]any{
			"paths": map[string]any{
				"/byoc/v1/things": map[string]any{
					"x-pgedge-plan": []any{"enterprise"},
				},
			},
		}
		if got := findPgEdgeExtensions(planted, ""); len(got) != 1 {
			t.Fatalf("search found %v in a document that plants one "+
				"marker; the check above proves nothing", got)
		}
	})
}

// TestVendoredSpecsOmitPrivateOperations pins the one path the API's
// public filter drops today.
//
// acceptInvite is marked x-pgedge-omit-public, so it is absent from
// every published spec and must be absent here. Nothing is lost: the
// verb cannot work from an API client at all — the API's edge proxy sets
// X-User-ID or X-Client-ID and never both — so `starfleet invite accept`
// refuses locally with exit 5 and never reaches a generated method.
//
// If this ever fails because the path came back, the question to ask
// is whether the API un-marked it, not whether to re-add it by hand.
func TestVendoredSpecsOmitPrivateOperations(t *testing.T) {
	doc := loadSpec(t, "account.yaml")
	const private = "/account/v1/invites/{id}/accept"
	if _, ok := doc.Paths[private]; ok {
		t.Errorf("%s is marked x-pgedge-omit-public upstream and must "+
			"not be vendored", private)
	}
	// The control: the sibling paths that are public must be present,
	// so this cannot pass by having loaded an empty document.
	for _, p := range []string{
		"/account/v1/invites", "/account/v1/invites/{id}",
	} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("public path %s is missing", p)
		}
	}
}
