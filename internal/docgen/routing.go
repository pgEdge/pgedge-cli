package docgen

import (
	"fmt"
	"regexp"
	"strings"
)

// A routing table is the generated block at the foot of an index page
// that lists the pages one level below it. It is generated because it
// is the one thing that must never lag the files: a page nobody is
// routed to is a page no agent reads. Its markers differ from a
// command block's so BlockCount, which counts command blocks against
// owned commands, does not count it.
const (
	routingBeginPrefix = "<!-- BEGIN GENERATED ROUTING: "
	routingEndMarker   = "<!-- END GENERATED ROUTING -->"
)

var routingRe = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED ROUTING: ([^>]+?) -->.*?` +
		`<!-- END GENERATED ROUTING -->`)

// RoutingEntry is one row: the page's scope and the Short of the
// command it documents, so the table says what each page is for in
// the command tree's own words.
type RoutingEntry struct {
	Scope string
	Short string
}

// RoutingBeginMarker returns the opening marker for a scope's table.
func RoutingBeginMarker(scope string) string {
	return routingBeginPrefix + strings.TrimSpace("pgedge "+scope) +
		markerSuffix
}

// RoutingEndMarker returns the closing marker.
func RoutingEndMarker() string { return routingEndMarker }

// Routing renders the table for scope's children. The `Read` column
// is the exact command to run, because the table exists to be
// followed, not admired.
func Routing(scope string, children []RoutingEntry) string {
	var b strings.Builder
	b.WriteString(RoutingBeginMarker(scope) + "\n")
	b.WriteString("| Page | Read | Covers |\n|------|------|--------|\n")
	for _, c := range children {
		word := c.Scope[strings.LastIndex(c.Scope, " ")+1:]
		fmt.Fprintf(&b, "| `%s` | `pgedge llms %s` | %s |\n",
			word, c.Scope, escapePipes(c.Short))
	}
	b.WriteString(routingEndMarker)
	return b.String()
}

// RoutingResult reports what ApplyRouting found.
type RoutingResult struct {
	Doc     string
	Changed bool
	// Missing is set when the page has children but no table to list
	// them in. The generator will not guess where the table belongs;
	// it reports the block to paste, as it does for a command block.
	Missing bool
	// Stray is set when the page has a table but no children, which
	// is a table that lists nothing and should be removed.
	Stray bool
	// Foreign names the scope a table declares when it is not this
	// page's own — a table pasted from another index.
	Foreign string
	// Duplicate is set when the page carries more than one table. Two
	// identical tables would otherwise rewrite to themselves and pass.
	Duplicate bool
}

// ApplyRouting rewrites the routing table in doc for scope's children,
// or reports what stops it.
func ApplyRouting(doc, scope string, children []RoutingEntry,
) RoutingResult {
	res := RoutingResult{Doc: doc}
	matches := routingRe.FindAllStringSubmatch(doc, -1)
	switch {
	case len(matches) == 0 && len(children) == 0:
		return res
	case len(matches) == 0:
		res.Missing = true
		return res
	case len(children) == 0:
		res.Stray = true
		return res
	case len(matches) > 1:
		res.Duplicate = true
		return res
	}
	if declared := strings.TrimSpace(matches[0][1]); declared !=
		strings.TrimSpace("pgedge "+scope) {
		res.Foreign = declared
		return res
	}
	want := Routing(scope, children)
	res.Doc = routingRe.ReplaceAllLiteralString(doc, want)
	res.Changed = res.Doc != doc
	return res
}

// HasRouting reports whether doc carries a routing table.
func HasRouting(doc string) bool { return routingRe.MatchString(doc) }
