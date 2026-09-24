package clitest

import (
	"os"
	"strings"
	"testing"

	pgedgecli "github.com/pgEdge/pgedge-cli"
	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
	"github.com/spf13/cobra"
)

// referenceDoc is one reference document and the scope it owns. The
// scope is a command-path prefix under root — "starfleet", "starfleet
// byoc database mcp" — and "" is the index.
type referenceDoc struct {
	path   string
	module string // "" for the index
}

// referenceDocs is the full set: the index, then every page the
// modules embed, read from the modules themselves. The index owns
// every command not under a module; each page owns its subtree minus
// whatever a longer scope claims from it. Nothing here is a list to
// maintain: a page exists because its file does, and this reads the
// same derivation `pgedge llms` serves from.
func referenceDocs() []referenceDoc {
	out := []referenceDoc{{path: "../../llms.txt"}}
	for _, d := range ReferenceDocuments() {
		out = append(out, referenceDoc{
			path: "../../" + d.Path, module: d.Scope})
	}
	return out
}

// referenceScopes is every scope in referenceDocs, which ownedBy needs
// in full to decide which of two nested scopes owns a command.
func referenceScopes() []string {
	docs := referenceDocs()
	scopes := make([]string, 0, len(docs))
	for _, doc := range docs {
		scopes = append(scopes, doc.module)
	}
	return scopes
}

// ownedBy returns the commands the given scope owns. A scope is a
// space-joined command-path prefix under root ("starfleet",
// "starfleet byoc", "controlplane"); "" is the index (root-level commands). A
// command belongs to the LONGEST scope that prefixes its path, so
// "starfleet byoc cluster list" is owned by "starfleet byoc", not "starfleet".
func ownedBy(all []*cobra.Command, scopes []string,
	scope string) []*cobra.Command {
	var out []*cobra.Command
	for _, cmd := range all {
		rel := strings.TrimPrefix(cmd.CommandPath(), "pgedge")
		rel = strings.TrimSpace(rel)
		best := ""
		for _, s := range scopes {
			if s == "" {
				continue
			}
			if (rel == s || strings.HasPrefix(rel, s+" ")) &&
				len(s) > len(best) {
				best = s
			}
		}
		if best == scope {
			out = append(out, cmd)
		}
	}
	return out
}

// TestReferenceDocsConform is the whole reference gate: docgen.Conform,
// run against each document with the commands that document owns.
//
// SPLITTING THE REFERENCE IS WHAT MADE THIS SIMPLE. While there was one
// llms-full.txt, "which module is this text about?" had to be inferred
// from "## Module:" headings, and a doc gate needed a hardcoded path
// special-case to scope a single-topic file correctly. Now the file IS
// the scope, so a block in the wrong document is reported by the same
// Foreign check that reports a block for a deleted command — no new
// mechanism, and no heading-sniffing.
func TestReferenceDocsConform(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	all := docgen.VisibleCommands(root)
	if len(all) == 0 {
		t.Fatal("FullTree produced no visible commands — this gate is " +
			"checking nothing")
	}

	totalBlocks, totalOwned := 0, 0
	scopes := referenceScopes()
	docs := ReferenceDocuments()

	for _, doc := range referenceDocs() {
		raw, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("%s: %v", doc.path, err)
		}
		text := string(raw)

		want := ownedBy(all, scopes, doc.module)
		if _, topic := reference.TopicSummary(raw); topic {
			switch {
			case len(want) > 0:
				t.Errorf("%s is a topic page but owns %d commands — "+
					"a command now has its name; rename the topic",
					doc.path, len(want))
			case docgen.BlockCount(text) > 0:
				t.Errorf("%s is a topic page but carries generated "+
					"blocks", doc.path)
			}
			continue
		}
		if len(want) == 0 {
			t.Errorf("%s owns no commands — either the split is wrong "+
				"or this document should not exist", doc.path)
			continue
		}

		// A conformance run over a document whose markers stopped
		// matching yields zero violations and zero blocks, which is
		// indistinguishable from a clean pass.
		if n := docgen.BlockCount(text); n != len(want) {
			t.Errorf("%s has %d generated blocks but owns %d commands — "+
				"the marker pattern is broken, or a block was "+
				"hand-deleted", doc.path, n, len(want))
		}

		for _, v := range docgen.Conform(text, want) {
			t.Errorf("%s: %s", doc.path, v)
		}

		// Every index page lists the pages below it.
		{
			rr := docgen.ApplyRouting(text, doc.module,
				RoutingFor(all, docs, doc.module))
			switch {
			case rr.Missing:
				t.Errorf("%s has pages below it but no routing table — "+
					"run `make docs` and paste the table it prints",
					doc.path)
			case rr.Stray:
				t.Errorf("%s carries a routing table but nothing is "+
					"below it", doc.path)
			case rr.Foreign != "":
				t.Errorf("%s's routing table declares %q", doc.path,
					rr.Foreign)
			case rr.Duplicate:
				t.Errorf("%s carries more than one routing table",
					doc.path)
			case rr.Changed:
				t.Errorf("%s's routing table is stale — run `make docs`",
					doc.path)
			}
		}

		totalBlocks += docgen.BlockCount(text)
		totalOwned += len(want)
	}

	// Every command must be owned by exactly one document. Without
	// this, a command belonging to no document would simply go
	// undocumented and every per-file check would still pass.
	if totalOwned != len(all) {
		t.Errorf("the reference documents collectively own %d commands "+
			"but the tree has %d — some command belongs to no document",
			totalOwned, len(all))
	}
	if totalBlocks != len(all) {
		t.Errorf("found %d generated blocks across all references for "+
			"%d commands", totalBlocks, len(all))
	}
}

// TestEveryModuleShipsItsReference closes the loop between the registry
// and the files on disk: a module registered without a reference, or a
// reference that is not the file the module actually embeds, both fail
// here. That is what makes `pgedge llms <module>` trustworthy.
//
// Sub-references are held to the same standard, under the scope name
// `pgedge llms <module> <sub>` accepts: a sub-document that no
// referenceDocs row names is a document nothing checks for
// conformance, and a crossed //go:embed would otherwise serve byoc's
// bytes for `llms starfleet managed` with every gate still green.
func TestEveryModuleShipsItsReference(t *testing.T) {
	for _, m := range Modules() {
		t.Run(m.Name(), func(t *testing.T) {
			if !m.Describe().ProvidesLLMS {
				t.Fatalf("module %q does not advertise a reference — "+
					"every shipped module must document itself", m.Name())
			}
			docs := []module.Document{{Scope: m.Name(),
				Path: "", Body: m.Reference()}}
			if d, ok := m.(module.Documented); ok {
				docs = d.Documents()
			}
			if len(docs) == 0 {
				t.Fatal("Documents() is empty — the embed failed")
			}
			for _, d := range docs {
				t.Run(d.Scope, func(t *testing.T) {
					if len(d.Body) == 0 {
						t.Fatalf("`pgedge llms %s` would print nothing",
							d.Scope)
					}
					if d.Path == "" {
						t.Fatal("a page must record the file it came from")
					}
					raw, err := os.ReadFile("../../" + d.Path)
					if err != nil {
						t.Fatal(err)
					}
					if string(d.Body) != string(raw) {
						t.Errorf("the page %q embeds differs from %s — "+
							"rebuild, and check the //go:embed directive",
							d.Scope, d.Path)
					}
					if d.Scope == m.Name() &&
						string(d.Body) != string(m.Reference()) {
						t.Error("Reference() is not the index page")
					}
				})
			}
		})
	}
}

// TestReferencePagesStayUnderBudget is the point of the split: a page
// below a module index must be small enough that reading only it is a
// saving. The pages named in oversized are known to exceed it until
// their topic prose moves out (spec 2026-08-30); each names its reason,
// and a page that shrinks back under budget must leave the list.
func TestReferencePagesStayUnderBudget(t *testing.T) {
	oversized := map[string]string{}
	for _, d := range ReferenceDocuments() {
		if strings.HasSuffix(d.Path, "/llms.txt") {
			continue // an index page; its budget is a separate question
		}
		reason, excused := oversized[d.Scope]
		over := len(d.Body) > reference.PageSizeBudget
		switch {
		case over && !excused:
			t.Errorf("`pgedge llms %s` is %d bytes, over the %d-byte "+
				"page budget — move prose to a topic page or split the "+
				"resource further", d.Scope, len(d.Body),
				reference.PageSizeBudget)
		case !over && excused:
			t.Errorf("`pgedge llms %s` is under budget; remove it from "+
				"oversized (was excused: %s)", d.Scope, reason)
		}
	}
}

// TestIndexEmbedMatchesFile is the index's half of the same check.
func TestIndexEmbedMatchesFile(t *testing.T) {
	raw, err := os.ReadFile("../../llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(pgedgecli.LLMS) != string(raw) {
		t.Fatal("the embedded llms.txt differs from the file on disk — " +
			"rebuild, and check embed.go's //go:embed directive")
	}
}

// TestReferenceGateCatchesDeletedFlagRow is the mutation proof, and the
// reason this gate is trusted at all.
//
// The hole it pins is the one that started this work: the original
// check asked strings.Contains(doc, "--"+flag) against the WHOLE file,
// so one --force anywhere satisfied every command that has a --force,
// and deleting a flag's row from a single command's table passed. The
// mutation is applied to a copy of the real document, so this tracks
// the doc as it changes, and it is asserted to have landed before the
// result is believed — a mutation that silently failed to apply
// produces a pass indistinguishable from a working gate.
func TestReferenceGateCatchesDeletedFlagRow(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	// The byoc cluster PAGE, because that is where the row now lives:
	// the split moved every command block off the byoc index onto a
	// page per resource, and the index the mutation used to read now
	// carries no flag table at all.
	const scope = "starfleet byoc cluster"
	want := ownedBy(
		docgen.VisibleCommands(root), referenceScopes(), scope)

	raw, err := os.ReadFile("../starfleet/byoc/llms/cluster.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	if got := docgen.Conform(doc, want); len(got) != 0 {
		t.Fatalf("the unmutated document must be clean before a "+
			"mutation result means anything — %d violation(s): %v",
			len(got), got)
	}

	const row = "| `--force` | No |  | Skip the confirmation prompt |\n"
	if strings.Count(doc, row) == 0 {
		t.Fatal("no --force row found in the expected form — this " +
			"test's mutation no longer applies; re-point it at a row " +
			"that does exist rather than deleting the test")
	}
	mutated := strings.Replace(doc, row, "", 1)

	if strings.Count(mutated, row) != strings.Count(doc, row)-1 {
		t.Fatal("the mutation did not land — exactly one --force row " +
			"must have been removed")
	}
	if !strings.Contains(mutated, "--force") {
		t.Fatal("the mutation removed every --force mention, which " +
			"would make this pass for the wrong reason: it must prove " +
			"the check is scoped to the owning command, not that the " +
			"flag is absent from the file")
	}

	if got := docgen.Conform(mutated, want); len(got) == 0 {
		t.Fatal("deleting a --force row from one command's table did " +
			"not redden the gate")
	}
}

// TestReferenceGateCatchesMisfiledBlock is the check the split made
// possible: a block that is perfectly valid, but sitting in the wrong
// module's document. Under the old monolith this could not even be
// expressed.
func TestReferenceGateCatchesMisfiledBlock(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	all := docgen.VisibleCommands(root)

	controlplaneDoc, err := os.ReadFile("../controlplane/llms.txt")
	if err != nil {
		t.Fatal(err)
	}

	// Give controlplane's document a byoc block and check it against controlplane's
	// commands.
	var byocBlock string
	for _, cmd := range all {
		if cmd.CommandPath() == "pgedge starfleet byoc cluster list" {
			byocBlock = docgen.Block(cmd)
			break
		}
	}
	if byocBlock == "" {
		t.Fatal("could not render a byoc block to misfile")
	}

	mutated := string(controlplaneDoc) + "\n\n" + byocBlock + "\n"
	got := docgen.Conform(mutated, ownedBy(all, referenceScopes(), "controlplane"))
	for _, v := range got {
		if strings.Contains(v, "pgedge starfleet byoc cluster list") {
			return
		}
	}
	t.Errorf("a byoc block filed in controlplane's reference was not reported: %v",
		got)
}

// TestCPReferenceNamesRouteMissPhrase pins a deliberate duplication:
// internal/controlplane/llms.txt's hand-written prose quotes the exact route-miss
// message internal/controlplane/cmd's checkResponse (client.go) returns for a
// plain-mux 404. The phrase is duplicated on purpose rather than
// generated — if either side is reworded without the other, this test
// fails, and that is the signal that the two must move together.
//
// normalizeWhitespace (shared with skills_docs_test.go) is required,
// not decoration: the 79-column wrap rule splits the phrase across two
// lines in the file on disk, so a raw strings.Contains against the
// unmodified content would never find it.
func TestCPReferenceNamesRouteMissPhrase(t *testing.T) {
	raw, err := os.ReadFile("../controlplane/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	const phrase = "does not serve an endpoint this command needs"
	if !strings.Contains(normalizeWhitespace(string(raw)), phrase) {
		t.Fatalf("internal/controlplane/llms.txt no longer contains %q — this "+
			"phrase is duplicated from internal/controlplane/cmd's route-miss "+
			"message (client.go's checkResponse) on purpose; if either "+
			"side rewords the message, update both together", phrase)
	}
}

// TestIndexReferenceNamesStarfleetRouteMiss pins the Starfleet twin of the
// duplication above: the index llms.txt's Exit Codes section quotes
// both the message internal/starfleet/conn's CheckResponse returns for a
// route miss and the canonical example body its isRouteMiss doc
// comment names — the API's router-level 404, `{"message":"Not Found"}`.
// That body is the worked example, not the string
// the discriminator compares against: isRouteMiss classifies
// structurally (any 404 body lacking a "code" field is a route miss),
// so this example is one instance among many, not the only one. If
// either side is reworded without the other, this still fails — the
// signal that the two must move together.
func TestIndexReferenceNamesStarfleetRouteMiss(t *testing.T) {
	raw, err := os.ReadFile("../../llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := normalizeWhitespace(string(raw))
	for _, quote := range []string{
		`{"message":"Not Found"}`,
		"does not serve an endpoint this command needs",
	} {
		if !strings.Contains(doc, quote) {
			t.Errorf("llms.txt (index) no longer contains %q — this "+
				"is duplicated from internal/starfleet/conn's route-miss "+
				"discriminator (conn.go's CheckResponse) on purpose; if "+
				"either side rewords it, update both together", quote)
		}
	}
}

// TestIndexReferenceNamesPlanEntitlementPhrase pins the plan-denial
// twin of the duplication above: the index llms.txt's Exit Codes
// section quotes both the substring internal/starfleet/conn's
// CheckResponse matches on to recognise a plan-entitlement denial
// (planDenialPhrase in conn.go) and its own sentence naming the exit
// code that shape gets. If either side is reworded
// without the other, this fails — the signal that the two must move
// together.
func TestIndexReferenceNamesPlanEntitlementPhrase(t *testing.T) {
	raw, err := os.ReadFile("../../llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := normalizeWhitespace(string(raw))
	for _, quote := range []string{
		"plan does not allow",
		"plan-entitlement 400 is 5, not 1",
	} {
		if !strings.Contains(doc, quote) {
			t.Errorf("llms.txt (index) no longer contains %q — this "+
				"is duplicated from internal/starfleet/conn's plan-denial "+
				"discriminator (conn.go's CheckResponse, planDenialPhrase) "+
				"on purpose; if either side rewords it, update both "+
				"together", quote)
		}
	}
}
