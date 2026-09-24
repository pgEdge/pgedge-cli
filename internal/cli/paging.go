package cli

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// PageDefaults records what a list endpoint measurably does: the page
// size it applies when --limit is omitted (Def), and the size it clamps
// a larger --limit to (Cap).
//
// Neither value may gate a refusal. Refusals come from the vendored
// spec, through OptionalIntFlagInRange; refusing from this table would
// invent a client-side ceiling that outlives any change to the server's
// cap. These values feed the truncation hint and nothing else.
//
// Def of 0 means the endpoint applies no default page, so an unbounded
// list cannot have been truncated and PrintTruncationHint stays silent.
// Cap of 0 means no clamp is known, so only --limit bounds the page.
//
// Each module keeps its own table with its own provenance, because what
// an endpoint such as /managed/v1/tasks does is a module fact.
type PageDefaults struct {
	Def int
	Cap int
}

// LimitFlagHelp is the --limit help text for a list verb, with the
// default page size taken from pd rather than typed as a literal. `make
// docs` copies the help into the generated reference, and building it
// from pd keeps it from disagreeing with the table the truncation hint
// reads. Def 0 means no default page, so it gets no parenthetical.
func LimitFlagHelp(pd PageDefaults) string {
	if pd.Def <= 0 {
		return "Maximum number of results to return"
	}
	return fmt.Sprintf(
		"Maximum number of results to return (API default %d)", pd.Def)
}

// PrintTruncationHint writes an advisory line to rt.Stderr, in text and
// table mode only, when a paginated result may have been capped by the
// server rather than complete.
//
// limit is the command's --limit value, or 0 if unset, in which case
// pd.Def is the effective limit. The hint fires when resultCount >=
// min(effective, pd.Cap), or effective alone when pd.Cap is 0. That
// cannot tell a capped page from a result landing exactly on the
// threshold, so the hint says "may be more" and never states a total;
// none of these endpoints reports one.
//
// noun names the collection in the message ("results", "backups", …).
//
// Callers reach this only from the text/table branch, after the JSON/YAML
// early return, and after their own "no results" early return, so
// resultCount is always > 0 here.
func PrintTruncationHint(
	rt *module.Runtime, resultCount, limit int, pd PageDefaults, noun string,
) {
	// Enforces the doc comment's rule, so a new caller cannot hint in
	// json mode. Structured is true for any format but text or table,
	// an empty one included. A Runtime with no renderer, which only
	// tests build, hints in any mode.
	if rt.Output != nil && rt.Output.Structured() {
		return
	}

	effective := limit
	if effective <= 0 {
		if pd.Def <= 0 {
			// No default page: an unbounded read is the whole result.
			return
		}
		effective = pd.Def
	}
	threshold := effective
	if pd.Cap > 0 && pd.Cap < threshold {
		threshold = pd.Cap
	}
	if resultCount < threshold {
		return
	}
	fmt.Fprintf(rt.Stderr,
		"Showing first %d %s; there may be more — pass --limit/--offset to page.\n",
		resultCount, output.Sanitize(noun))
}
