package docgen

import (
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Adoption converts a hand-written command section into a generated
// block. `gendocs -adopt` runs it once per section; after that Apply
// keeps the block current.
//
// A section before adoption, and what is replaced:
//
//	##### cluster list                <- heading      REPLACED
//	                                                  REPLACED
//	List all clusters in the tenant.  <- description  REPLACED
//	                                                  REPLACED
//	**Usage:** `pgedge starfleet ...` <- usage        REPLACED
//	                                                  REPLACED
//	**Flags:**                        <- flags        REPLACED
//	| Flag | Required | Description |                 REPLACED
//	| `--limit int` | No | ... |                      REPLACED
//	                                                  <- span ends
//	**Example:**                      <- prose        KEPT
//	```bash                                           KEPT
//	pgedge starfleet byoc cluster list --limit 20     KEPT
//	```                                               KEPT
//
// The span runs from the heading to the last flags-table row, or to
// the usage line when there are no flags.
//
// The one deliberate loss is a one-line description, replaced by
// cobra's Short. Where the one-liner was richer ("List all clusters in
// the current tenant" vs "List clusters"), improve Short, which
// improves --help too, rather than keep two descriptions that can
// disagree.

var (
	anyHeadingRe  = regexp.MustCompile(`^#{1,6} `)
	flagsLabelRe  = regexp.MustCompile(`^\*\*Flags:\*\*`)
	tableRowRe    = regexp.MustCompile(`^\|`)
	usageAnchorRe = regexp.MustCompile("^\\*\\*Usage:\\*\\* `([^`]+)`")
)

// usagePathOnLine returns the command path a Usage line anchors, or ""
// if the line is not a Usage line: the leading bare tokens, up to the
// first placeholder or flag, so "pgedge starfleet byoc cluster get
// <cluster_id> [flags]" yields "pgedge starfleet byoc cluster get".
//
// The call site must compare for equality, never by prefix. A group's
// path followed by a space prefixes every child's usage line, so a
// prefix match adopted the child's section as the group's: 146 spans
// claimed from 122 real anchors, and 406 lines lost to overlapping
// rewrites.
func usagePathOnLine(line string) string {
	m := usageAnchorRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	var path []string
	for _, tok := range strings.Fields(m[1]) {
		if strings.HasPrefix(tok, "<") || strings.HasPrefix(tok, "[") ||
			strings.HasPrefix(tok, "-") {
			break
		}
		path = append(path, tok)
	}
	return strings.Join(path, " ")
}

// Adopt replaces the hand-written section for each command in root that
// has a `**Usage:**` line but no generated block, and returns the
// rewritten document plus the paths it adopted.
//
// Commands with neither a block nor a usage line are left for Apply to
// report as Missing: this function will not guess where a section
// belongs, because a generator that invents placement scatters the
// reference and there is no way to review that automatically.
func Adopt(doc string, root *cobra.Command) (out string, adopted []string) {
	lines := strings.Split(doc, "\n")

	// Paths already owned by a block are skipped outright.
	owned := make(map[string]bool)
	for _, m := range blockRe.FindAllStringSubmatch(doc, -1) {
		owned[strings.TrimSpace(m[1])] = true
	}

	var spans []adoptSpan

	for _, cmd := range VisibleCommands(root) {
		path := cmd.CommandPath()
		if owned[path] {
			continue
		}

		usageIdx := -1
		for i, line := range lines {
			if usagePathOnLine(line) == path {
				usageIdx = i
				break
			}
		}
		if usageIdx < 0 {
			continue // no section at all; Apply reports it as Missing
		}

		// Back to the nearest preceding heading.
		start := usageIdx
		for i := usageIdx - 1; i >= 0; i-- {
			if anyHeadingRe.MatchString(lines[i]) {
				start = i
				break
			}
		}

		// Forward to the end of the flags table, if this section has
		// one before the next heading. flagsIdx marks where the
		// generated part resumes, so prose sitting between the usage
		// line and the table is preserved rather than swallowed.
		end, flagsIdx := usageIdx, -1
		for i := usageIdx + 1; i < len(lines); i++ {
			if anyHeadingRe.MatchString(lines[i]) {
				break
			}
			if flagsLabelRe.MatchString(lines[i]) {
				end, flagsIdx = i, i
				for j := i + 1; j < len(lines); j++ {
					if tableRowRe.MatchString(lines[j]) {
						end = j
						continue
					}
					if strings.TrimSpace(lines[j]) == "" {
						continue
					}
					break
				}
				break
			}
		}

		// Everything in the span that is neither heading, usage line
		// nor flags table is hand-written and must survive. Only a
		// bare one-line description is dropped, because the generated
		// block carries cobra's Short in its place.
		var keep []string
		keep = append(keep, keepProse(lines[start+1:usageIdx])...)
		if flagsIdx > usageIdx {
			// With no flags table flagsIdx is -1, and the slice would
			// run backwards and panic.
			keep = append(keep,
				keepProse(lines[usageIdx+1:flagsIdx])...)
		}

		spans = append(spans, adoptSpan{
			start: start, end: end, cmd: cmd, keep: keep})
	}

	// Rewrite back to front so earlier indices stay valid.
	sortSpansDesc(spans)

	for _, s := range spans {
		block := strings.Split(Block(s.cmd), "\n")

		// A fresh slice, not append(lines[:start], append(block, ...)):
		// that form is safe only while strings.Split leaves no spare
		// capacity, or the inner append overwrites what the outer one
		// is still reading.
		merged := make([]string, 0, len(lines)+len(block)+len(s.keep)+2)
		merged = append(merged, lines[:s.start]...)
		merged = append(merged, block...)
		if len(s.keep) > 0 {
			merged = append(merged, "")
			merged = append(merged, s.keep...)
		}
		merged = append(merged, lines[s.end+1:]...)
		lines = merged

		adopted = append(adopted, s.cmd.CommandPath())
	}

	reverse(adopted)
	return strings.Join(lines, "\n"), adopted
}

// adoptSpan is the line range one hand-written section occupies, plus
// the prose inside it that must be re-emitted after the block.
type adoptSpan struct {
	start, end int // inclusive line indices
	cmd        *cobra.Command
	keep       []string
}

// keepProse decides what to do with the lines a section carries between
// its heading and its usage line, or between its usage line and its
// flags table. It returns them unchanged unless they are a single short
// paragraph, which is the one-line description the generated block
// replaces with cobra's Short.
//
// The default is to keep. `database rag deploy` carried three
// paragraphs and a fenced JSON config in this range, and deleting the
// range outright lost them silently: ```json fences went from 3 to 2
// in a 4,700-line file. Anything with a fence, table, list or quote,
// or more than one line, is kept.
func keepProse(block []string) []string {
	trimmed := trimBlankEdges(block)
	if len(trimmed) == 0 {
		return nil
	}

	for _, line := range trimmed {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") ||
			strings.HasPrefix(t, ">") {
			return trimmed
		}
	}

	// Only a single line counts as the description Short replaces. At
	// a 79-column wrap a second line carries a second clause, and that
	// is the informative half: "Register a new cloud account for AWS,
	// Azure, or GCP. Required flags depend on the cloud provider
	// specified with --type" against Short's "Register a cloud
	// account". Dropping one line is the accepted cost; keeping it
	// would state the same thing twice in 122 sections.
	if len(trimmed) == 1 {
		return nil
	}
	return trimmed
}

// trimBlankEdges drops leading and trailing blank lines.
func trimBlankEdges(block []string) []string {
	start, end := 0, len(block)
	for start < end && strings.TrimSpace(block[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(block[end-1]) == "" {
		end--
	}
	return block[start:end]
}

// sortSpansDesc orders spans last-first so rewriting one does not shift
// the indices of the ones still to be rewritten.
func sortSpansDesc(spans []adoptSpan) {
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].start > spans[j].start
	})
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
