// Command gendocs regenerates the AI-agent reference documents from
// the live cobra tree. Run it with `make docs`.
//
// There is one index, one index page per module and product sub-tree,
// and one page per resource below those (database one level further),
// each owning exactly the commands in its scope. A command belongs to
// the longest scope that prefixes its path, so byoc's index holds
// `pgedge starfleet byoc` itself and llms/database/mcp.txt holds the
// mcp sub-tree. The page set is derived from the files each module
// embeds (internal/reference), never listed here:
//
//	llms.txt                                       everything not under a module
//	internal/starfleet/llms.txt                    pgedge starfleet (index)
//	internal/starfleet/llms/client.txt             pgedge starfleet client ...
//	internal/starfleet/byoc/llms/database/mcp.txt  pgedge starfleet byoc database mcp ...
//
// The human reference pages in docs/reference/ each hold one
// generated region rewritten wholesale (internal/docgen/page.go).
//
// Only text between the generated markers is rewritten; see
// internal/docgen for what is deliberately NOT generated.
//
// A command with no block yet is reported, not placed: ordering in
// these documents carries meaning, so the generator prints the block
// to paste and TestReferenceDocsConform fails until it is placed.
//
// The tree comes from internal/clitest.FullTree, which the build gates
// also walk. Importing "clitest" from a tool reads oddly, but the
// alternative is a third copy of "root plus every registered module"
// that can drift from the two that already have to agree.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/clitest"
	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/spf13/cobra"
)

// target is one reference document and the scope whose commands it
// owns: a command-path prefix under root ("starfleet", "starfleet byoc
// database"), or "" for the index.
type target struct {
	path   string
	module string
}

// targets is the index plus every page each module embeds, so the
// generator cannot disagree with `pgedge llms` or
// TestReferenceDocsConform about which pages exist.
func targets() []target {
	out := []target{{path: "llms.txt"}}
	for _, d := range clitest.ReferenceDocuments() {
		out = append(out, target{path: d.Path, module: d.Scope})
	}
	return out
}

// pageTargets are the human reference pages under docs/. Each carries
// ONE generated region holding every owned command, so a new command
// lands on its page with no placement step. Mirrored by
// TestDocsReferencePagesConform.
var pageTargets = []target{
	{path: "docs/reference/pgedge.md"},
	{path: "docs/reference/starfleet.md", module: "starfleet"},
	{path: "docs/reference/starfleet-byoc.md", module: "starfleet byoc"},
	{path: "docs/reference/starfleet-managed.md", module: "starfleet managed"},
	{path: "docs/reference/controlplane.md", module: "controlplane"},
}

func main() {
	check := flag.Bool("check", false,
		"report drift and exit non-zero instead of rewriting")
	adopt := flag.Bool("adopt", false,
		"convert hand-written sections into generated blocks "+
			"(one-time, per section)")
	flag.Parse()

	if err := run(*check, *adopt); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

// ownedBy returns the commands scope owns under the longest-prefix
// rule; "" is the index.
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

func run(check, adopt bool) error {
	root, err := clitest.FullTree()
	if err != nil {
		return err
	}
	all := docgen.VisibleCommands(root)

	tgts := targets()
	scopes := make([]string, 0, len(tgts))
	for _, tgt := range tgts {
		scopes = append(scopes, tgt.module)
	}
	docs := clitest.ReferenceDocuments()

	var drifted []string

	for _, tgt := range tgts {
		raw, err := os.ReadFile(tgt.path) //nolint:gosec // G304: paths come from the modules' own embedded page set
		if err != nil {
			return err
		}
		want := ownedBy(all, scopes, tgt.module)

		doc := string(raw)
		if adopt {
			var adopted []string
			doc, adopted = docgen.Adopt(doc, root)
			fmt.Printf("%s: adopted %d hand-written section(s)\n",
				tgt.path, len(adopted))
		}

		res := docgen.Apply(doc, want)

		for _, v := range docgen.Conform(doc, want) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", tgt.path, v)
		}
		if len(res.Missing) > 0 {
			fmt.Fprintf(os.Stderr,
				"\nPaste each of these into %s, then re-run:\n%s\n",
				tgt.path, docgen.MissingBlocks(want, res.Missing))
		}

		// Every index lists the pages below it in a generated table.
		routingDrift := false
		{
			children := clitest.RoutingFor(all, docs, tgt.module)
			rr := docgen.ApplyRouting(res.Doc, tgt.module, children)
			switch {
			case rr.Missing:
				fmt.Fprintf(os.Stderr, "%s: has pages below it but no "+
					"routing table — paste this where the reader should "+
					"be routed, then re-run:\n%s\n", tgt.path,
					docgen.Routing(tgt.module, children))
			case rr.Stray:
				fmt.Fprintf(os.Stderr, "%s: carries a routing table but "+
					"no page is below it — remove the table\n", tgt.path)
			case rr.Foreign != "":
				fmt.Fprintf(os.Stderr, "%s: its routing table declares "+
					"%q, not this page\n", tgt.path, rr.Foreign)
			case rr.Duplicate:
				fmt.Fprintf(os.Stderr, "%s: carries more than one routing "+
					"table — keep one\n", tgt.path)
			}
			routingDrift = rr.Missing || rr.Stray || rr.Foreign != "" ||
				rr.Duplicate || rr.Changed
			res.Doc = rr.Doc
		}

		if routingDrift || res.Changed > 0 || len(res.Missing) > 0 ||
			len(res.Foreign) > 0 {
			drifted = append(drifted, tgt.path)
		}

		if check {
			continue
		}
		if res.Doc != string(raw) {
			// 0o600 satisfies gosec G306 and costs nothing: git records
			// only the executable bit, and the mode applies only to a
			// new file.
			if err := os.WriteFile(
				tgt.path, []byte(res.Doc), 0o600); err != nil {
				return err
			}
			fmt.Printf("rewrote %s (%d block(s) changed)\n",
				tgt.path, res.Changed)
		}
	}

	// Ownership among the human pages is decided among their own
	// scopes, or the byoc page would lose every database command to an
	// llms scope that has no page here.
	pageScopes := make([]string, 0, len(pageTargets))
	for _, tgt := range pageTargets {
		pageScopes = append(pageScopes, tgt.module)
	}
	for _, tgt := range pageTargets {
		raw, err := os.ReadFile(tgt.path) //nolint:gosec // G304: fixed paths in the pageTargets table above
		if err != nil {
			return err
		}
		want := ownedBy(all, pageScopes, tgt.module)

		out, err := docgen.ApplyPage(string(raw), tgt.module, want)
		if err != nil {
			return fmt.Errorf("%s: %w", tgt.path, err)
		}
		if out == string(raw) {
			continue
		}
		drifted = append(drifted, tgt.path)
		if check {
			continue
		}
		if err := os.WriteFile(tgt.path, []byte(out), 0o600); err != nil {
			return err
		}
		fmt.Printf("rewrote %s\n", tgt.path)
	}

	if len(drifted) > 0 {
		if check {
			return fmt.Errorf(
				"out of date: %s — run `make docs`",
				strings.Join(drifted, ", "))
		}
		// A rewrite fixes Changed but never Missing or Foreign; a
		// re-run reports what is left.
		return nil
	}

	if check {
		fmt.Println("all reference documents are up to date")
	}
	return nil
}
