package clitest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryDocsDirectoryIsScoped keeps docsScopes and the directories
// under docs/ in step. A directory that holds pages but is in neither
// docsScopes nor coreDocsDirs would fall through moduleSections to the
// byoc fallback, and its controlplane or managed pages would then be
// checked against byoc's field and status vocabulary, which is what the
// flat glob did to every workflow page before the module directories
// existed. The reverse failure is caught too: a module directory in
// docsScopes with no page on disk is a dead allowance left by a rename.
func TestEveryDocsDirectoryIsScoped(t *testing.T) {
	dirs := make(map[string]bool)
	for _, f := range docsTreeFiles(t) {
		rel, err := filepath.Rel("../..", f)
		if err != nil {
			t.Fatal(err)
		}
		dirs[filepath.Dir(rel)] = true
	}

	scoped := make(map[string]bool)
	for _, kv := range docsScopes {
		if strings.HasSuffix(kv.key, "/") {
			scoped[strings.Trim(kv.key, "/")] = true
		}
	}

	for dir := range dirs {
		if coreDocsDirs[dir] || scoped[dir] {
			continue
		}
		t.Errorf("%s holds pages but is not in moduleSections' docsScopes "+
			"table, so its pages would be checked against byoc's "+
			"vocabulary; add the directory with its module, or move the "+
			"pages", dir)
	}
	for dir := range scoped {
		if !dirs[dir] {
			t.Errorf("docsScopes names %s but no .md file exists there; "+
				"remove the dead entry or restore the pages", dir)
		}
	}
	if len(scoped) == 0 {
		t.Fatal("docsScopes names no directory at all")
	}
}

// TestStripGeneratedBlocksStripsPageAndRoutingForms is the positive
// control for the two marker forms generatedBlockRe never matched. Each
// fixture carries a task_id, which checkNoEnvelopeClaims flags in byoc
// scope, so a form the stripper misses produces a violation. The last
// case misspells the PAGE end marker and must still be flagged, which
// proves the pass depends on the stripper and not on the fixture.
func TestStripGeneratedBlocksStripsPageAndRoutingForms(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    int
	}{
		{"command block", "<!-- BEGIN GENERATED: pgedge x -->\n" +
			"**Usage:** `pgedge x <task_id>`\n<!-- END GENERATED -->\n", 0},
		{"page block", "<!-- BEGIN GENERATED PAGE: pgedge x -->\n" +
			"**Usage:** `pgedge x <task_id>`\n<!-- END GENERATED PAGE -->\n", 0},
		{"routing block", "<!-- BEGIN GENERATED ROUTING: pgedge x -->\n" +
			"| `pgedge x <task_id>` | reads a task |\n" +
			"<!-- END GENERATED ROUTING -->\n", 0},
		{"page block with a broken end marker",
			"<!-- BEGIN GENERATED PAGE: pgedge x -->\n" +
				"**Usage:** `pgedge x <task_id>`\n<!-- END GENERATED PAGES -->\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secs := moduleSections("fixture.md", tc.fixture)
			var got []string
			for _, sec := range secs {
				got = append(got, checkNoEnvelopeClaims("fixture.md", sec)...)
			}
			if len(got) != tc.want {
				t.Fatalf("want %d violations, got %d: %v", tc.want, len(got), got)
			}
		})
	}
}
