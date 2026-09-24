package docgen

// The docs/reference pages carry no prose between commands, unlike
// the llms pages, so the contract is the opposite of Apply's: one
// marker pair per page, the generator owns everything between them,
// and a new command appears with no placement step.

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	pageBeginPrefix = "<!-- BEGIN GENERATED PAGE: "
	pageEndMarker   = "<!-- END GENERATED PAGE -->"
)

// PageBeginMarker returns the opening marker for a page's generated
// region. The scope is a command-path prefix under root ("starfleet
// byoc", "controlplane"), or "" for the index page; the marker names
// the full path.
func PageBeginMarker(scope string) string {
	path := "pgedge"
	if scope != "" {
		path += " " + scope
	}
	return pageBeginPrefix + path + markerSuffix
}

// PageEndMarker returns the closing marker for a page's generated
// region.
func PageEndMarker() string { return pageEndMarker }

// Page renders a reference page's generated region: every command's
// section in order, no per-command markers, headings re-levelled so
// the shallowest command sits at h2 under the page's hand-written h1.
func Page(cmds []*cobra.Command) string {
	shallowest := 7
	for _, cmd := range cmds {
		if d := headingLevel(cmd.CommandPath()); d < shallowest {
			shallowest = d
		}
	}

	parts := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		// Not dead despite headingLevel's clamp: on a page whose
		// shallowest command is the root, depth 6 re-levels to 7.
		level := headingLevel(cmd.CommandPath()) - shallowest + 2
		if level > 6 {
			level = 6
		}
		parts = append(parts,
			strings.TrimRight(renderBody(cmd, level, true), "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// ApplyPage replaces the text between a page's markers with the
// rendered region for its commands. Absent, duplicated or misordered
// markers are an error, not a skip: the pages are a fixed table, so a
// broken one is drift.
func ApplyPage(doc, scope string, cmds []*cobra.Command) (string, error) {
	begin, end := PageBeginMarker(scope), PageEndMarker()

	i := strings.Index(doc, begin)
	if i < 0 {
		return "", fmt.Errorf("missing %q", begin)
	}
	j := strings.Index(doc, end)
	if j < i {
		return "", fmt.Errorf("missing or misplaced %q", end)
	}
	if strings.Contains(doc[i+len(begin):], begin) {
		return "", fmt.Errorf("more than one %q", begin)
	}
	if strings.Contains(doc[j+len(end):], end) {
		return "", fmt.Errorf("more than one %q", end)
	}

	return doc[:i+len(begin)] + "\n\n" + Page(cmds) + "\n\n" +
		doc[j:], nil
}
