package clitest

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The allowlist in paging_sweep_test.go used to certify itself. Its
// comment claimed that "adding a row here without a matching maximum:
// in the spec is the mistake this catches", and nothing in that file
// read a spec — so a reviewer added `controlplane task list` to the allowlist
// AND a ceiling of 100 to the code, and every gate stayed green while
// the binary shipped:
//
//	--limit must be at most 100 (got 500): that is the maximum this
//	endpoint's contract declares
//
// control-plane.json declares no maximum on any of its five limit
// parameters, so that sentence was false. This file is what makes the
// comment true.

// declaredMaximum is one spec-declared paging bound.
type declaredMaximum struct {
	spec  string
	path  string
	param string
	max   int
}

// pagingSpecs are every vendored description, including
// control-plane.json — which is JSON, and therefore also valid YAML,
// so one parser reads all four. The Control Plane is deliberately
// absent from starfleetSpecs above because it has different provenance;
// here it must be present, because the question is "does ANY contract
// declare a paging maximum", and an unread spec is a spec whose
// silence cannot be asserted.
var pagingSpecs = []string{
	"byoc.yaml", "managed.yaml", "account.yaml", "control-plane.json",
}

// declaredPagingMaxima returns every limit/offset maximum declared by
// any vendored spec.
//
// Path-level parameters are read as well as operation-level ones: a
// spec is free to hoist a shared parameter, and reading only the
// operation level would report a declared bound as absent.
func declaredPagingMaxima(t *testing.T) []declaredMaximum {
	t.Helper()

	// Ref is captured so a referenced parameter can be REFUSED rather
	// than silently skipped. This parser reads inline parameters only,
	// and a $ref'd `maximum:` was proven invisible to it — the gate
	// passed with one live in the spec. No vendored spec uses them
	// today, so the tripwire below costs nothing and stops the day
	// one does from being a silent hole.
	type param struct {
		Name   string `yaml:"name"`
		Ref    string `yaml:"$ref"`
		Schema struct {
			Maximum *int `yaml:"maximum"`
		} `yaml:"schema"`
	}
	var found []declaredMaximum
	var pathsSeen int

	for _, name := range pagingSpecs {
		raw, err := os.ReadFile(specPath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc struct {
			Paths map[string]struct {
				Parameters []param             `yaml:"parameters"`
				Ops        map[string]struct { // get, post, …
					Parameters []param `yaml:"parameters"`
				} `yaml:",inline"`
			} `yaml:"paths"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if len(doc.Paths) == 0 {
			t.Fatalf("no paths parsed from %s; this gate would pass "+
				"vacuously", name)
		}
		for p, item := range doc.Paths {
			pathsSeen++
			collect := func(ps []param) {
				for _, prm := range ps {
					if prm.Ref != "" {
						t.Errorf("%s %s carries a $ref'd parameter "+
							"(%s). This parser reads inline "+
							"parameters only and cannot see a "+
							"maximum behind a reference: inline it, "+
							"or teach declaredPagingMaxima to "+
							"resolve refs before trusting this gate",
							name, p, prm.Ref)
						continue
					}
					if prm.Name != "limit" && prm.Name != "offset" {
						continue
					}
					if prm.Schema.Maximum == nil {
						continue
					}
					found = append(found, declaredMaximum{
						spec: name, path: p, param: prm.Name,
						max: *prm.Schema.Maximum,
					})
				}
			}
			collect(item.Parameters)
			for _, op := range item.Ops {
				collect(op.Parameters)
			}
		}
	}
	if pathsSeen == 0 {
		t.Fatal("walked zero paths across four specs; the parser has " +
			"lost its shape and every assertion below is vacuous")
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].path+found[i].param <
			found[j].path+found[j].param
	})
	return found
}

// TestPagingCeilingsAreSpecDerived is the gate the allowlist's own
// comment promised.
//
// It asserts the allowlist and the contracts describe the SAME set of
// bounds — not merely that each allowlisted number is right, but that
// no allowlist row exists without a declaration behind it. The second
// direction is the one that matters: a row added for an endpoint whose
// spec is silent is how an invented ceiling reaches users wearing a
// message that says the contract declares it.
func TestPagingCeilingsAreSpecDerived(t *testing.T) {
	declared := declaredPagingMaxima(t)

	// Positive control. Two maxima exist today, both on managed's
	// `limit`. Zero means the walk broke rather than that saas removed
	// them — and a broken walk would make an empty allowlist "correct".
	if len(declared) == 0 {
		t.Fatal("no paging maximum found in any vendored spec; " +
			"managed.yaml declares two, so the walk is broken and " +
			"this gate would excuse any allowlist")
	}

	// Compared as (spec path, maximum) PAIRS, never as a bag of
	// numbers. Matching on the value alone let a reviewer point a
	// `controlplane task list` row at a maximum of 100 that byoc had published
	// on /byoc/v1/ingresses: the multiset matched, so cp shipped a
	// fabricated ceiling behind the "the contract declares it"
	// message while byoc's real bound went unenforced, with all five
	// gates green.
	declaredSet := map[string]bool{}
	for _, d := range declared {
		declaredSet[d.path+" "+strconv.Itoa(d.max)] = true
	}
	allowSet := map[string]bool{}
	for verb, c := range pagingCeilings {
		key := c.specPath + " " + strconv.Itoa(c.max)
		allowSet[key] = true
		if !declaredSet[key] {
			t.Errorf("pagingCeilings[%q] claims %s declares a maximum "+
				"of %d, but no vendored spec does. The CLI would "+
				"refuse a value the API accepts, behind a message "+
				"saying the contract declares it.",
				verb, c.specPath, c.max)
		}
	}
	for _, d := range declared {
		if !allowSet[d.path+" "+strconv.Itoa(d.max)] {
			t.Errorf("%s declares a maximum of %d for %s on %s, and "+
				"no verb enforces it — either wire it to the verb "+
				"that reads that endpoint, or say why it is ignored",
				d.spec, d.max, d.param, d.path)
		}
	}
	if len(declared) != len(pagingCeilings) {
		var names []string
		for _, d := range declared {
			names = append(names, d.spec+" "+d.path+" "+d.param+
				"="+strconv.Itoa(d.max))
		}
		sort.Strings(names)
		t.Errorf("the specs declare %d paging maximum/maxima but "+
			"pagingCeilings has %d row(s): %s",
			len(declared), len(pagingCeilings),
			strings.Join(names, ", "))
	}

	// No ceiling on offset, anywhere. On managed backups, where limit
	// maxes at 100, offset is the ONLY way to reach row 101 onward.
	for _, d := range declared {
		if d.param == "offset" {
			t.Errorf("%s %s declares a maximum of %d for offset. "+
				"Pin it deliberately if that is real, and check it "+
				"does not put rows beyond limit's own maximum out of "+
				"reach", d.spec, d.path, d.max)
		}
	}
}
