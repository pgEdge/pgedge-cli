package clitest

import (
	"os"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/docgen"
)

// pageDocs is the human reference-page set under docs/reference, one
// page per scope. It mirrors pageTargets in cmd/gendocs the same way
// referenceDocs mirrors that command's targets, and the same
// completeness check applies: every command in the tree must land on
// exactly one page.
var pageDocs = []referenceDoc{
	{path: "../../docs/reference/pgedge.md"},
	{path: "../../docs/reference/starfleet.md", module: "starfleet"},
	{path: "../../docs/reference/starfleet-byoc.md", module: "starfleet byoc"},
	{path: "../../docs/reference/starfleet-managed.md", module: "starfleet managed"},
	{path: "../../docs/reference/controlplane.md", module: "controlplane"},
}

// TestDocsReferencePagesConform is the whole gate for the docs pages:
// a page conforms when rewriting its generated region is a no-op, so
// the check IS the generator — a page that is merely plausible cannot
// pass, and a page whose markers broke fails on ApplyPage's error
// rather than passing vacuously.
func TestDocsReferencePagesConform(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	all := docgen.VisibleCommands(root)
	if len(all) == 0 {
		t.Fatal("FullTree produced no visible commands — this gate is " +
			"checking nothing")
	}

	totalOwned := 0
	// Ownership among the human pages is decided among their own five
	// scopes, not the finer llms pages, or the byoc page would lose its
	// database commands to a scope with no page here.
	scopes := make([]string, 0, len(pageDocs))
	for _, doc := range pageDocs {
		scopes = append(scopes, doc.module)
	}

	for _, doc := range pageDocs {
		raw, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("%s: %v", doc.path, err)
		}
		text := string(raw)

		want := ownedBy(all, scopes, doc.module)
		if len(want) == 0 {
			t.Errorf("%s owns no commands — either the split is wrong "+
				"or this page should not exist", doc.path)
			continue
		}
		totalOwned += len(want)

		out, err := docgen.ApplyPage(text, doc.module, want)
		if err != nil {
			t.Errorf("%s: %v", doc.path, err)
			continue
		}
		if out != text {
			t.Errorf("%s is out of date — run `make docs`", doc.path)
		}
	}

	if totalOwned != len(all) {
		t.Errorf("the reference pages collectively own %d commands but "+
			"the tree has %d — some command lands on no page",
			totalOwned, len(all))
	}
}

// TestDocsReferencePagesGateCatchesDeletedFlagRow proves the gate is
// not vacuous: a hand-deleted flag row must make ApplyPage report a
// different document, exactly as it would after a flag change in the
// tree.
func TestDocsReferencePagesGateCatchesDeletedFlagRow(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	all := docgen.VisibleCommands(root)
	scopes := referenceScopes()

	raw, err := os.ReadFile("../../docs/reference/controlplane.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	mutated := make([]string, 0, len(lines))
	dropped := false
	for _, line := range lines {
		if !dropped && strings.HasPrefix(line, "| `--") {
			dropped = true
			continue
		}
		mutated = append(mutated, line)
	}
	if !dropped {
		t.Fatal("no flag row found to delete — the fixture no longer " +
			"exercises the gate")
	}

	doc := strings.Join(mutated, "\n")
	want := ownedBy(all, scopes, "controlplane")
	out, err := docgen.ApplyPage(doc, "controlplane", want)
	if err != nil {
		t.Fatal(err)
	}
	if out == doc {
		t.Error("a deleted flag row survived ApplyPage — the gate " +
			"would not catch this mutation")
	}
}
