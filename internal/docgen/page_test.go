package docgen

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// pageTree builds pgedge -> starfleet -> byoc -> cluster -> list and
// returns the three commands a byoc-scoped page would own, in
// VisibleCommands order.
func pageTree() []*cobra.Command {
	root := newGroup("pgedge", "")
	starfleet := newGroup("starfleet", "")
	byoc := newGroup("byoc", "Manage BYOC resources.")
	cluster := newGroup("cluster", "Manage clusters.")
	list := newLeaf("list", "List clusters.")
	list.Flags().Int("limit", 0, "Maximum number of results to return")
	root.AddCommand(starfleet)
	starfleet.AddCommand(byoc)
	byoc.AddCommand(cluster)
	cluster.AddCommand(list)
	return []*cobra.Command{byoc, cluster, list}
}

func TestPageRelevelsToH2(t *testing.T) {
	got := Page(pageTree())

	// The scope root is depth 3 (pgedge starfleet byoc); on its own page
	// it must sit at h2, under the page's hand-written h1.
	if !strings.Contains(got, "\n## pgedge starfleet byoc\n") &&
		!strings.HasPrefix(got, "## pgedge starfleet byoc\n") {
		t.Errorf("Page() scope root is not an h2:\n%s", got)
	}
	// The leaf is two levels deeper, so h4 — the shift preserves
	// relative depth.
	if !strings.Contains(got, "\n#### pgedge starfleet byoc cluster list\n") {
		t.Errorf("Page() leaf is not an h4:\n%s", got)
	}
	// The flag table travels with the block body.
	if !strings.Contains(got,
		"| `--limit int` | No |  | Maximum number of results to return |") {
		t.Errorf("Page() missing flag row:\n%s", got)
	}
}

func TestPageCarriesNoCommandMarkers(t *testing.T) {
	got := Page(pageTree())
	if strings.Contains(got, "BEGIN GENERATED:") ||
		strings.Contains(got, EndMarker()) {
		t.Errorf("Page() leaked per-command markers:\n%s", got)
	}
}

func TestApplyPageReplacesRegion(t *testing.T) {
	cmds := pageTree()
	doc := "# byoc reference\n\nhand-written intro.\n\n" +
		PageBeginMarker("starfleet byoc") + "\nstale content\n" +
		PageEndMarker() + "\n\nhand-written postamble.\n"

	out, err := ApplyPage(doc, "starfleet byoc", cmds)
	if err != nil {
		t.Fatalf("ApplyPage() error: %v", err)
	}
	if !strings.HasPrefix(out, "# byoc reference\n\nhand-written intro.\n\n") {
		t.Errorf("ApplyPage() disturbed the preamble:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n\nhand-written postamble.\n") {
		t.Errorf("ApplyPage() disturbed the postamble:\n%s", out)
	}
	if strings.Contains(out, "stale content") {
		t.Errorf("ApplyPage() kept stale region content:\n%s", out)
	}
	if !strings.Contains(out, "## pgedge starfleet byoc") {
		t.Errorf("ApplyPage() did not write the rendered page:\n%s", out)
	}

	// Idempotent: applying again changes nothing.
	again, err := ApplyPage(out, "starfleet byoc", cmds)
	if err != nil {
		t.Fatalf("ApplyPage() second pass error: %v", err)
	}
	if again != out {
		t.Errorf("ApplyPage() is not idempotent:\nfirst:\n%s\nsecond:\n%s",
			out, again)
	}
}

func TestApplyPageMissingMarkerIsError(t *testing.T) {
	cmds := pageTree()

	for name, doc := range map[string]string{
		"no markers": "# page\n\nprose only\n",
		"no end":     "# page\n\n" + PageBeginMarker("starfleet byoc") + "\n",
		"no begin":   "# page\n\n" + PageEndMarker() + "\n",
		"end before begin": "# page\n\n" + PageEndMarker() + "\n" +
			PageBeginMarker("starfleet byoc") + "\n",
		"wrong scope": "# page\n\n" + PageBeginMarker("controlplane") + "\n" +
			PageEndMarker() + "\n",
		"duplicate begin": "# page\n\n" +
			PageBeginMarker("starfleet byoc") + "\n" +
			PageBeginMarker("starfleet byoc") + "\n" + PageEndMarker() + "\n",
		"stray second end": "# page\n\n" +
			PageBeginMarker("starfleet byoc") + "\n" + PageEndMarker() +
			"\n\nprose\n\n" + PageEndMarker() + "\n",
	} {
		if _, err := ApplyPage(doc, "starfleet byoc", cmds); err == nil {
			t.Errorf("ApplyPage(%s) = nil error, want one", name)
		}
	}
}

// TestPageClampsAtH6 pins the re-level ceiling. headingLevel's own
// clamp at 6 is not enough: on a page whose shallowest command is the
// root (shallowest 1), a depth-6 command re-levels to 7, and markdown
// has no h7.
func TestPageClampsAtH6(t *testing.T) {
	root := newGroup("pgedge", "")
	parent := root
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		child := newGroup(name, "")
		parent.AddCommand(child)
		parent = child
	}
	cmds := VisibleCommands(root)

	got := Page(cmds)
	if strings.Contains(got, "#######") {
		t.Errorf("Page() rendered an h7:\n%s", got)
	}
	if !strings.Contains(got, "\n###### pgedge a b c d e\n") {
		t.Errorf("Page() did not clamp the deepest command at h6:\n%s",
			got)
	}
}

func TestPageBeginMarkerNamesTheScope(t *testing.T) {
	if got, want := PageBeginMarker(""),
		"<!-- BEGIN GENERATED PAGE: pgedge -->"; got != want {
		t.Errorf("PageBeginMarker(\"\") = %q, want %q", got, want)
	}
	if got, want := PageBeginMarker("starfleet byoc"),
		"<!-- BEGIN GENERATED PAGE: pgedge starfleet byoc -->"; got != want {
		t.Errorf("PageBeginMarker(scope) = %q, want %q", got, want)
	}
}
