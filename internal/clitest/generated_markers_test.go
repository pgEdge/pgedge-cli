package clitest

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/spf13/cobra"
)

// generatedMarkerRe matches anything the claim gates' stripper could
// take for a block boundary, in every form and in any misspelling the
// stripper could still act on, so a variant the exact patterns would
// not match is still found.
var generatedMarkerRe = regexp.MustCompile(`<!-- (?:BEGIN|END) GENERATED[^>]*-->`)

// generatorMarkers returns, per scanned file, every marker the
// generator would write there. A file absent from the map is one the
// generator never writes, so no marker belongs in it.
//
// The sets are derived from the same lists the two conformance gates
// use — referenceDocs and pageDocs — and from docgen's own marker
// functions, so a new page or a new marker form reaches this gate the
// day it reaches the generator.
func generatorMarkers(t *testing.T, all []*cobra.Command) map[string]map[string]bool {
	t.Helper()
	out := make(map[string]map[string]bool)

	scopes := referenceScopes()
	docs := ReferenceDocuments()
	for _, doc := range referenceDocs() {
		set := map[string]bool{docgen.EndMarker(): true}
		for _, cmd := range ownedBy(all, scopes, doc.module) {
			set[docgen.BeginMarker(cmd.CommandPath())] = true
		}
		if len(RoutingFor(all, docs, doc.module)) > 0 {
			set[docgen.RoutingBeginMarker(doc.module)] = true
			set[docgen.RoutingEndMarker()] = true
		}
		out[doc.path] = set
	}

	pageScopes := make([]string, 0, len(pageDocs))
	for _, doc := range pageDocs {
		pageScopes = append(pageScopes, doc.module)
	}
	for _, doc := range pageDocs {
		if len(ownedBy(all, pageScopes, doc.module)) == 0 {
			t.Fatalf("%s owns no commands", doc.path)
		}
		out[doc.path] = map[string]bool{
			docgen.PageBeginMarker(doc.module): true,
			docgen.PageEndMarker():             true,
		}
	}
	return out
}

// strayMarkers reports every marker in content that allowed does not
// name, as "line N: marker". A nil allowed set means none belongs.
func strayMarkers(content string, allowed map[string]bool) []string {
	var out []string
	for _, loc := range generatedMarkerRe.FindAllStringIndex(content, -1) {
		m := content[loc[0]:loc[1]]
		if allowed[m] {
			continue
		}
		line := strings.Count(content[:loc[0]], "\n") + 1
		out = append(out, "line "+strconv.Itoa(line)+": "+m)
	}
	return out
}

// TestGeneratedMarkersSitOnlyWhereTheGeneratorWrites is what makes it
// safe for the claim gates to trust a marker. stripGeneratedBlocks
// blanks everything between a BEGIN and END pair wherever it finds
// one, so a hand-written envelope on any scanned page would hide a
// fabricated field, a capture instruction or an invented status from
// every gate. This holds every marker in the scanned
// population to the file and the exact form the generator would write:
// a command block or routing table only on the llms page that owns it,
// a PAGE pair only on its docs/reference page, and nothing anywhere
// else. Inside those pairs the conformance gates compare the content
// byte for byte, so with placement pinned here no envelope in the
// scanned population is left that nothing reads.
func TestGeneratedMarkersSitOnlyWhereTheGeneratorWrites(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	all := docgen.VisibleCommands(root)
	if len(all) == 0 {
		t.Fatal("FullTree produced no visible commands")
	}
	generator := generatorMarkers(t, all)

	files := scanDocFiles(t)
	seen := make(map[string]bool, len(files))
	allowedFound, outside := 0, 0
	for _, file := range files {
		seen[file] = true
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		allowed, owned := generator[file]
		if !owned {
			outside++
		}
		for _, stray := range strayMarkers(content, allowed) {
			if owned {
				t.Errorf("%s %s is not a marker the generator writes on "+
					"this page; the claim gates would strip whatever it "+
					"encloses unchecked", file, stray)
				continue
			}
			t.Errorf("%s %s: the generator never writes this file, so "+
				"nothing checks what a marker pair here encloses and "+
				"the claim gates skip it; remove the markers, or to quote "+
				"one in prose break the pair by dropping its <!--",
				file, stray)
		}
		for _, m := range generatedMarkerRe.FindAllString(content, -1) {
			if allowed[m] {
				allowedFound++
			}
		}
	}

	// A generator-owned file outside the scanned population is one
	// whose stray marker this gate would never read.
	var missing []string
	for file := range generator {
		if !seen[file] {
			missing = append(missing, file)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("generator-owned files outside the scanned population: %v",
			missing)
	}
	if allowedFound == 0 {
		t.Fatal("no generator marker matched anywhere — the allowed sets " +
			"are empty or the pattern is broken, and this gate is " +
			"checking nothing")
	}
	if outside == 0 {
		t.Fatal("every scanned file is generator-owned — the population " +
			"no longer holds a hand-written page")
	}
}

// TestGeneratedMarkerGateCatchesHandWrittenEnvelopes runs the
// classifier over the measured shapes, plus two a looser check
// would have missed: a command block on a
// docs/reference page and a PAGE pair on an llms page. Each
// fixture's stray count is exact, so a form the pattern stopped
// matching fails here rather than passing quietly.
func TestGeneratedMarkerGateCatchesHandWrittenEnvelopes(t *testing.T) {
	block := "<!-- BEGIN GENERATED: pgedge x -->\nfrobnicate_id\n" +
		"<!-- END GENERATED -->\n"
	page := "<!-- BEGIN GENERATED PAGE: pgedge x -->\nfrobnicate_id\n" +
		"<!-- END GENERATED PAGE -->\n"
	routing := "<!-- BEGIN GENERATED ROUTING: pgedge x -->\n| a |\n" +
		"<!-- END GENERATED ROUTING -->\n"

	llms := map[string]bool{
		docgen.BeginMarker("pgedge y"): true, docgen.EndMarker(): true,
		docgen.RoutingBeginMarker("y"): true, docgen.RoutingEndMarker(): true,
	}
	reference := map[string]bool{
		docgen.PageBeginMarker("y"): true, docgen.PageEndMarker(): true,
	}

	cases := []struct {
		name    string
		allowed map[string]bool
		content string
		want    int
	}{
		{"command block on a guide", nil, "prose\n" + block, 2},
		{"page envelope on a guide", nil, page, 2},
		{"routing table on a guide", nil, routing, 2},
		{"command block on a reference page", reference, block, 2},
		{"routing table on a reference page", reference, routing, 2},
		{"page envelope for another scope on a reference page", reference,
			"<!-- BEGIN GENERATED PAGE: pgedge y -->\n" +
				"<!-- END GENERATED PAGE -->\n" + page, 1},
		{"page envelope on an llms page", llms, page, 2},
		{"command block for an unowned path on an llms page", llms, block, 1},
		{"misspelled end marker", llms,
			"<!-- BEGIN GENERATED: pgedge y -->\n<!-- END GENERATED PAGES -->\n", 1},
		{"the generator's own output", llms,
			"<!-- BEGIN GENERATED: pgedge y -->\n<!-- END GENERATED -->\n" +
				"<!-- BEGIN GENERATED ROUTING: pgedge y -->\n" +
				"<!-- END GENERATED ROUTING -->\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strayMarkers(tc.content, tc.allowed)
			if len(got) != tc.want {
				t.Fatalf("want %d stray markers, got %d: %v",
					tc.want, len(got), got)
			}
		})
	}
}
