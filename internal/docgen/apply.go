package docgen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// blockRe matches one complete generated block and captures the command
// path from its opening marker. (?s) so the body may span lines.
var blockRe = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED: ([^>]+?) -->.*?<!-- END GENERATED -->`)

// Result reports what Apply did and what it left for a human. Missing
// and Foreign are never resolved automatically: placing a new block
// would scatter the reference, and deleting one would lose the prose
// beside it.
type Result struct {
	Doc     string
	Changed int      // blocks whose rendered content differed
	Missing []string // commands the document should cover but does not
	Foreign []string // blocks for commands this document should not cover
}

// Apply re-renders every generated block in doc and returns the
// rewritten document. want is the exact set of commands the document
// owns.
//
// It is a pure function of (doc, want) so `make docs` and the gate run
// the same code, and a forgotten regeneration is a red build.
func Apply(doc string, want []*cobra.Command) Result {
	byPath := make(map[string]*cobra.Command, len(want))
	for _, cmd := range want {
		byPath[cmd.CommandPath()] = cmd
	}

	res := Result{}
	found := make(map[string]bool)

	res.Doc = blockRe.ReplaceAllStringFunc(doc, func(block string) string {
		m := blockRe.FindStringSubmatch(block)
		path := strings.TrimSpace(m[1])
		found[path] = true

		cmd, ok := byPath[path]
		if !ok {
			// Left as it is: reported, not acted on.
			res.Foreign = append(res.Foreign, path)
			return block
		}

		rendered := Block(cmd)
		if rendered != block {
			res.Changed++
		}
		return rendered
	})

	for path := range byPath {
		if !found[path] {
			res.Missing = append(res.Missing, path)
		}
	}
	sort.Strings(res.Missing)
	sort.Strings(res.Foreign)

	return res
}

// Conform reports every way doc fails to be a conforming reference for
// want, as human-readable messages. Empty means conforming.
//
// It is the only definition of conforming. A second check, parsing
// `**Usage:**`-anchored sections for each command's flags, would only
// compare generated text with the tree that generated it; a drifted
// block is caught here, byte for byte.
func Conform(doc string, want []*cobra.Command) []string {
	res := Apply(doc, want)

	var out []string
	for _, path := range res.Missing {
		out = append(out, fmt.Sprintf(
			"no generated block for command %q — run `make docs` to "+
				"print the block, then paste it under the right module "+
				"(the generator will not guess a location)", path))
	}
	for _, path := range res.Foreign {
		out = append(out, fmt.Sprintf(
			"has a generated block for %q, which this document is not "+
				"responsible for — either the command no longer exists, "+
				"or the block belongs in another module's reference",
			path))
	}
	if res.Changed > 0 {
		out = append(out, fmt.Sprintf(
			"%d generated block(s) differ from what the command tree "+
				"produces — run `make docs`. Do not hand-edit inside the "+
				"BEGIN/END GENERATED markers; the next regeneration "+
				"overwrites it", res.Changed))
	}
	return out
}

// BlockCount reports how many generated blocks doc contains, so a
// caller can tell a clean pass from a run whose markers stopped
// matching: both report zero violations.
func BlockCount(doc string) int {
	return strings.Count(doc, endMarker)
}

// MissingBlocks renders the blocks for every path in missing, so both
// the generator and a failing gate can show the author exactly what to
// paste.
func MissingBlocks(want []*cobra.Command, missing []string) string {
	byPath := make(map[string]*cobra.Command, len(want))
	for _, cmd := range want {
		byPath[cmd.CommandPath()] = cmd
	}

	var b strings.Builder
	for _, path := range missing {
		cmd, ok := byPath[path]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n", Block(cmd))
	}
	return b.String()
}
