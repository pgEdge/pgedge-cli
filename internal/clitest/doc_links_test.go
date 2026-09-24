package clitest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// --- 4. the relative-link check ----------------------------------------------
//
// llms.txt shipped 20 links into a `docs/commands/` and
// `docs/workflows/` tree that has never existed in this repository —
// out of 24 links total, so five sixths of the file's navigation was
// dead. It is //go:embed-ed and printed raw by `pgedge llms`, so those
// were dead links handed directly to agents, in the one file whose
// entire job is to tell an agent where to look next.
//
// Nothing caught it because every existing doc gate asks about
// COMMANDS and FIELDS. A link is neither. This check is deliberately
// dumb and complete: every relative markdown link target in every
// scanned doc must exist on disk.
//
// SCOPE, AND WHY IT STOPS WHERE IT DOES. Only relative targets are
// checked. An http(s) URL needs the network to verify, and a gate that
// reaches the network is a gate that fails on a train; anchor-only
// targets (`#section`) are a separate problem needing a heading index.
// Both are skipped explicitly rather than by accident, because a
// silently-skipped category is how a check ends up covering less than
// its name promises.

// markdownLinkRe matches `[text](target)`. The target group stops at
// the first closing paren, which is correct for every link in this
// repo's docs and would need revisiting only for a target containing
// a literal paren.
var markdownLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// checkRelativeLinks returns one violation per relative link target in
// content that does not resolve to a file on disk, resolved relative to
// the directory holding path.
func checkRelativeLinks(path, content string) []string {
	dir := filepath.Dir(path)

	var violations []string
	seen := make(map[string]bool)

	for _, m := range markdownLinkRe.FindAllStringSubmatch(content, -1) {
		target := strings.TrimSpace(m[1])

		// Strip a trailing anchor: docs/index.md#limitations resolves
		// against the file, and the anchor half is out of scope.
		if i := strings.Index(target, "#"); i >= 0 {
			target = target[:i]
		}
		if target == "" {
			continue // anchor-only link; see the scope note above
		}
		if strings.Contains(target, "://") ||
			strings.HasPrefix(target, "mailto:") {
			continue // absolute; needs the network
		}
		if seen[target] {
			continue
		}
		seen[target] = true

		if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
			violations = append(violations, fmt.Sprintf(
				"%s: links to %q, which does not exist. Point it at a "+
					"real path, or drop the link and name the thing in "+
					"plain text — a dead link in a //go:embed-ed file is "+
					"handed straight to an agent as navigation",
				path, target))
		}
	}
	return violations
}

// TestDocLinksResolve runs the relative-link check over the same doc
// set as the other doc gates.
func TestDocLinksResolve(t *testing.T) {
	files := scanDocFiles(t)

	scanned := make(map[string]bool, len(files))
	checked := 0
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanned[path] = true
		checked += len(markdownLinkRe.FindAllString(string(content), -1))

		for _, msg := range checkRelativeLinks(path, string(content)) {
			t.Error(msg)
		}
	}

	// A link check that found no links is a link check that proves
	// nothing, and it looks exactly like a clean run.
	if checked == 0 {
		t.Fatal("no markdown links found in any scanned doc — the link " +
			"pattern is broken and this gate is checking nothing")
	}

	assertAllNamedDocsScanned(t, scanned)
}

// TestRelativeLinkCheckCatchesDeadTarget is the positive control, using
// the exact shape that shipped: a link into the docs/commands/ tree
// that has never existed.
func TestRelativeLinkCheckCatchesDeadTarget(t *testing.T) {
	got := checkRelativeLinks("../../llms.txt",
		"- [Cluster](docs/commands/cluster.md): list, get, create")
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation for a dead relative link, "+
			"got %d: %v", len(got), got)
	}
}

// TestRelativeLinkCheckAcceptsLiveTargets is the negative control. A
// gate that flags everything and a gate that flags nothing are
// indistinguishable on a doc that happens to be clean, so both
// directions are pinned. README.md and the vendored specs are real
// files that llms.txt really links to.
func TestRelativeLinkCheckAcceptsLiveTargets(t *testing.T) {
	fixture := "- [README](README.md): start here\n" +
		"- [BYOC API Spec](openapi/byoc.yaml): the spec\n" +
		"- [Upstream](https://github.com/pgEdge/pgedge-cli): remote\n" +
		"- [Anchor](#known-limitations): same page\n" +
		"- [With anchor](README.md#install): a real file plus anchor"

	if got := checkRelativeLinks("../../llms.txt", fixture); len(got) != 0 {
		t.Fatalf("flagged a live target, an absolute URL, or an anchor: "+
			"%v", got)
	}
}
