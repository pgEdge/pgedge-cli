package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"gopkg.in/yaml.v3"
)

// paramBounds is one path parameter's declared range. Pointers, not
// ints: a missing `maximum` and a maximum of 0 are different facts,
// and only the pointer form can tell them apart.
type paramBounds struct {
	minimum *int
	maximum *int
	found   bool
}

// pagingParam reads one GET parameter's bounds out of the vendored
// managed spec.
//
// A parameter this test cannot find is a hard failure rather than a
// zero value. The whole purpose here is to notice a re-vendor that
// moved a bound, and a lookup that silently returned "no bounds" for a
// renamed path would pass against anything — the exact failure
// schemaPropertyEnum documents above it.
func pagingParam(t *testing.T, path, name string) paramBounds {
	t.Helper()

	specFile := filepath.Join(
		"..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("read %s: %v", specFile, err)
	}
	var doc struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name   string `yaml:"name"`
					Schema struct {
						Minimum *int `yaml:"minimum"`
						Maximum *int `yaml:"maximum"`
					} `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", specFile, err)
	}
	p, ok := doc.Paths[path]
	if !ok {
		t.Fatalf("no %s path in %s; this test has lost its truth "+
			"source and would pass against anything", path, specFile)
	}
	for _, prm := range p.Get.Parameters {
		if prm.Name != name {
			continue
		}
		return paramBounds{
			minimum: prm.Schema.Minimum,
			maximum: prm.Schema.Maximum,
			found:   true,
		}
	}
	t.Fatalf("no %s parameter on GET %s in %s; this test has lost "+
		"its truth source", name, path, specFile)
	return paramBounds{}
}

// TestPagingBoundsMatchTheSpec pins each paging constant to the
// endpoint's own parameter.
//
// The defect this closes is not a wrong number, it is a SHARED one.
// `limit` is bounded {1,100} on backups and {1,1000} on databases, so
// any constant covering both is wrong for one of them — and neither
// help text said which applied. A re-vendor that moves either bound
// must redden here rather than leave the CLI refusing values the API
// now accepts.
func TestPagingBoundsMatchTheSpec(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"backups", "/managed/v1/backups", backupLimitMax},
		{"databases", "/managed/v1/databases", databaseLimitMax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := pagingParam(t, tc.path, "limit")
			if b.maximum == nil {
				t.Fatalf("GET %s no longer declares a maximum for "+
					"limit; either drop the constant and pass "+
					"cli.NoUpperBound, or restore the declaration",
					tc.path)
			}
			if *b.maximum != tc.want {
				t.Errorf("limit maximum on %s = %d, constant = %d",
					tc.path, *b.maximum, tc.want)
			}
			if b.minimum == nil || *b.minimum != cli.LimitLowest {
				t.Errorf("limit minimum on %s = %v, cli.LimitLowest = %d",
					tc.path, b.minimum, cli.LimitLowest)
			}
		})
	}

	// offset, on both endpoints that declare BOUNDS for it. A third
	// (/managed/v1/tasks) declares the parameter with no bounds and is
	// covered by TestTaskPagingDeclaresNoBounds.
	//
	// The maximum half is the one that was missing, and a reviewer
	// found the hole by exploiting it: giving backups' --offset a
	// ceiling of 100 passed every paging test with the same === RUN
	// count, while making rows 101 onward unreachable — offset is the
	// only way past a limit capped at 100 — behind a message asserting
	// a clamp no spec declares. Nothing said offset was unbounded, so
	// nothing objected.
	//
	// Zero must stay ACCEPTED on the minimum side: it is the first
	// page, not an empty value, which is why OffsetLowest is 0 and not
	// LimitLowest.
	for _, path := range []string{
		"/managed/v1/backups", "/managed/v1/databases"} {
		off := pagingParam(t, path, "offset")
		if off.minimum == nil || *off.minimum != cli.OffsetLowest {
			t.Errorf("offset minimum on %s = %v, cli.OffsetLowest = %d",
				path, off.minimum, cli.OffsetLowest)
		}
		if off.maximum != nil {
			t.Errorf("GET %s now declares a maximum of %d for offset. "+
				"No offset ceiling was ever enforced because none was "+
				"published; if that has changed, pin it — but do NOT "+
				"invent one, and check it does not put rows beyond "+
				"limit's own maximum out of reach",
				path, *off.maximum)
		}
	}
}

// TestTaskPagingDeclaresNoBounds is the negative half, and it is the
// one that stops a guessed ceiling creeping back in.
//
// /managed/v1/tasks was measured clamping --limit 500 to 100 rows.
// The temptation is to pin 100 here too. The spec publishes nothing,
// so such a constant would refuse a value the API would accept the
// day the cap moves — the direction of error this repo treats as the
// serious one. If saas ever DOES declare bounds on tasks, this test
// fails and the fix is to add the constant, not to delete the test.
func TestTaskPagingDeclaresNoBounds(t *testing.T) {
	for _, name := range []string{"limit", "offset"} {
		b := pagingParam(t, "/managed/v1/tasks", name)
		if b.maximum != nil {
			t.Errorf("GET /managed/v1/tasks now declares a maximum "+
				"of %d for %s — pin it in paging.go and pass it to "+
				"cli.OptionalIntFlagInRange instead of NoUpperBound",
				*b.maximum, name)
		}
	}
}
