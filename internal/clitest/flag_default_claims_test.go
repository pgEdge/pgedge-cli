package clitest

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- the flag-default check ---------------------------------------------------
//
// flag_claims validates that a documented flag EXISTS and stops there —
// its own comment names "a flag documented with the wrong argument or
// default" as the class it does not catch. That class shipped a proven
// gap: a review set byoc's --wait-timeout default to 917, a
// value in no document, and 2,159 tests plus make docs-check stayed
// green, because the generated flag tables carry no defaults and every
// hand-written "default N seconds" claim was guarded by nothing but the
// author's sweep.
//
// This check binds those claims to the tree: every "default N
// second(s)", "default Ns" or punctuation-closed "(default N)" that
// follows a flag token in the same sentence must match a default the
// flag actually registers within the section's module scope.
//
// WHAT IT DOES NOT CATCH, STATED SO NOBODY ASSUMES OTHERWISE:
//
//   - a unitless number that runs into a word ("default 5 nodes") or
//     carries a non-second unit ("default 2m"). Only "N seconds",
//     "Ns" and a bare number closed by ) , or ; ("(default 5)") are
//     read; minutes/hours get a spelling here when the first one
//     lands rather than a guessed shape now. A period deliberately
//     does NOT close the bare form: "defaults to 0.5" would bind as
//     0, so a period-closed bare number is skipped instead.
//   - a claim written value-first or flag-value style. "the
//     30-second default" and "the shipped defaults (`--timeout 30s`,
//     `--wait-timeout 600`)" — both live in the references today —
//     never match, because the number does not follow the default
//     keyword. controlplane's wait defaults are documented in that second
//     shape, so they sit outside this gate; controlplane's own wait_test pins
//     those values.
//   - anything outside the reference documents. docs/*.md
//     carries the same shapes, but docs/changelog.md quotes old
//     defaults as history by design, so widening the scope needs a
//     carve-out first — deliberate, not an oversight.
//   - a claim in a sentence with no flag token before it. The check
//     needs an anchor; an unanchored claim is skipped, not judged.
//   - a claim anchored to the WRONG flag of the right module. Binding
//     is nearest-preceding within the sentence, which shares the
//     mis-anchoring hazard flag_claims and field_claims already state.

// flagDefaultClaimRe matches a numeric default claim: "default 600
// seconds", "(default 5 seconds)", "defaults to 30s", "default `30s`",
// and the bare punctuation-closed form "(default 5)" managed's
// reference uses for --wait-interval. The mandatory default keyword is
// what keeps example values ("e.g. `45s`, `2m`") from being read as
// claims, and the bare form's closing punctuation is what keeps
// "default 5 nodes" and "default 2m" out (RE2 has no lookahead, so the
// punctuation is consumed rather than asserted). A period is not a
// closer: "defaults to 0.5" would otherwise bind as 0.
var flagDefaultClaimRe = regexp.MustCompile(
	"(?i)\\bdefault(?:s|ing)?(?:\\s+(?:to|is|of))?:?\\s+`?([0-9]+)" +
		"(?:\\s*seconds?\\b|s`?\\b|`?[),;])")

// flagDefaultLookup answers the numeric defaults a flag registers
// within a module scope (integers as they stand, whole-second
// durations as seconds). The indirection exists so the
// kill-shape tests below can drive checkFlagDefaultClaims with a
// hand-built table while the live gate walks the shipped tree.
type flagDefaultLookup func(scope, flag string) ([]int, bool)

// checkFlagDefaultClaims returns one violation per bound claim that
// contradicts the tree, plus the number of claims it judged. The count
// goes back to the caller so the live gate can pin the population —
// a regex or binding change that silently stops finding claims must
// fail on the tally, not pass on vacuous emptiness.
func checkFlagDefaultClaims(path string, sec docSection,
	lookup flagDefaultLookup) (violations []string, claims int) {

	normalized := normalizeWhitespace(sec.text)

	// sentenceSplitRe treats "e.g. " as a sentence end, which orphans
	// a claim from the flag it follows ("`--timeout` (Go duration,
	// e.g. `45s`; default `30s`)" split just before the values). The
	// dot is rewritten to a comma before splitting — display-only
	// mangling, since only excerpts read this text afterwards.
	for _, abbr := range []string{"e.g.", "i.e."} {
		normalized = strings.ReplaceAll(normalized, abbr+" ",
			strings.TrimSuffix(abbr, ".")+", ")
	}

	for _, sentence := range sentenceSplitRe.Split(normalized, -1) {
		flagLocs := flagClaimRe.FindAllStringIndex(sentence, -1)
		if len(flagLocs) == 0 {
			continue
		}
		for _, m := range flagDefaultClaimRe.
			FindAllStringSubmatchIndex(sentence, -1) {

			// Bind to the nearest flag token that ends before the
			// claim starts. A claim preceding every flag token has no
			// anchor and is skipped (stated limit above).
			flag := ""
			for _, fl := range flagLocs {
				if fl[1] > m[0] {
					break
				}
				flag = sentence[fl[0]:fl[1]]
			}
			if flag == "" {
				continue
			}
			claims++

			n, err := strconv.Atoi(sentence[m[2]:m[3]])
			if err != nil {
				// Reachable only on an out-of-range digit run.
				violations = append(violations, fmt.Sprintf(
					"%s: unparseable default in %q", path,
					claimExcerpt(sentence)))
				continue
			}

			secs, ok := lookup(sec.module, flag)
			if !ok {
				violations = append(violations, fmt.Sprintf(
					"%s: %q claims a default for %s, but no command "+
						"in scope %q registers that flag with a "+
						"numeric default",
					path, claimExcerpt(sentence), flag, sec.module))
				continue
			}
			// The claim must match EVERY registration in scope, not
			// any one. Match-any was measured escaping the exact
			// mutation this gate exists for: byoc registers
			// --wait-timeout at two sites (the shared wait helper and
			// task wait's own), so a 917 mutation on one site left
			// the other vouching for the documented 600. Under
			// match-all a single-site mutation breaks the agreement
			// and fails — and a flag whose defaults genuinely diverge
			// within a module makes any bare "default N" prose claim
			// ambiguous, which deserves the failure.
			for _, s := range secs {
				if s != n {
					violations = append(violations, fmt.Sprintf(
						"%s: %q claims %s defaults to %d; the tree "+
							"registers %v",
						path, claimExcerpt(sentence), flag, n, secs))
					break
				}
			}
		}
	}
	return violations, claims
}

// claimExcerpt keeps violation messages readable when the "sentence"
// is a whole table row. It slices runes, not bytes — the references
// carry em dashes.
func claimExcerpt(s string) string {
	r := []rune(s)
	if len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return s
}

// defaultAsSeconds normalises a pflag DefValue to whole seconds: an
// integer default is taken as seconds (the wait flags), a Go duration
// is converted (the request timeouts). Anything else — bools, strings,
// fractional durations — has no seconds meaning and registers nothing.
func defaultAsSeconds(def string) (int, bool) {
	if n, err := strconv.Atoi(def); err == nil {
		return n, true
	}
	if d, err := time.ParseDuration(def); err == nil &&
		d%time.Second == 0 {
		return int(d / time.Second), true
	}
	return 0, false
}

// scopeForCommandPath maps a command to the docSection scope whose
// reference documents it, mirroring moduleSections' path keys. Word
// boundaries matter: prefix-matching without the trailing space would
// let a future "pgedge cloudx" land in "starfleet".
func scopeForCommandPath(p string) string {
	for _, kv := range []struct{ prefix, scope string }{
		{"pgedge starfleet byoc", "byoc"},
		{"pgedge starfleet managed", "managed"},
		{"pgedge starfleet", "starfleet"},
		{"pgedge controlplane", "controlplane"},
	} {
		if p == kv.prefix || strings.HasPrefix(p, kv.prefix+" ") {
			return kv.scope
		}
	}
	return "root"
}

// referenceFlagDefaults walks the shipped tree once and answers
// lookups scope-first: a byoc claim about --timeout resolves against
// byoc's own registrations, then starfleet's (where the connection flags
// actually live), then the root's — the same containment the
// references themselves document. The nearest scope that registers
// the flag wins outright, so an ancestor's same-named flag cannot
// vouch for a leaf's different default.
func referenceFlagDefaults(t *testing.T) flagDefaultLookup {
	t.Helper()

	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	perScope := map[string]map[string]map[int]bool{}
	add := func(scope, flag string, secs int) {
		if perScope[scope] == nil {
			perScope[scope] = map[string]map[int]bool{}
		}
		if perScope[scope][flag] == nil {
			perScope[scope][flag] = map[int]bool{}
		}
		perScope[scope][flag][secs] = true
	}

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		scope := scopeForCommandPath(c.CommandPath())
		collect := func(f *pflag.Flag) {
			if secs, ok := defaultAsSeconds(f.DefValue); ok {
				add(scope, "--"+f.Name, secs)
			}
		}
		c.LocalFlags().VisitAll(collect)
		c.PersistentFlags().VisitAll(collect)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	chains := map[string][]string{
		"byoc":         {"byoc", "starfleet", "root"},
		"managed":      {"managed", "starfleet", "root"},
		"starfleet":    {"starfleet", "root"},
		"controlplane": {"controlplane", "root"},
	}

	return func(scope, flag string) ([]int, bool) {
		chain, ok := chains[scope]
		if !ok {
			chain = []string{"root"}
		}
		for _, s := range chain {
			if vals := perScope[s][flag]; len(vals) > 0 {
				out := make([]int, 0, len(vals))
				for v := range vals {
					out = append(out, v)
				}
				return out, true
			}
		}
		return nil, false
	}
}

// referenceFlagDefaultClaims is the number of bound numeric default
// claims the references carry, tallied 2026-08-24 and verified
// claim-by-claim. Like pagingFloorVerbs it catches ONE thing: the
// scan going blind — a regex edit, a binding change or a section split
// that silently stops finding the claims this gate exists to judge. A
// new claim raises it; nothing may lower it without saying why.
//
// It did exactly that job through the resource split: 14 became 6
// while the scan still read only the five files named llms.txt, and it
// is 14 again now that it reads all 48 — the same claims, eight of
// them on pages. The byoc overhaul (2026-09-23) removed eight: the
// index's four workflow error tables and two restated wait defaults
// each in the index and on the database page. The index concision
// pass (2026-09-23) removed five more: the indexes no longer restate
// flag defaults as durations.
const referenceFlagDefaultClaims = 1

// TestReferenceFlagDefaultClaimsMatchTree is the live gate:
// every bound "default N seconds" claim in the references must match a
// default the named flag registers in that section's scope.
func TestReferenceFlagDefaultClaimsMatchTree(t *testing.T) {
	lookup := referenceFlagDefaults(t)

	// Every reference document: the index and every module page. A
	// HasSuffix("llms.txt") filter used to stand here and it survived
	// the resource split reading 5 files of 48, finding 6 of the 14
	// claims — the tally below is what reported it.
	refs := ReferenceFiles()
	scanned := make(map[string]bool)
	for _, f := range scanDocFiles(t) {
		scanned[f] = true
	}
	for _, f := range refs {
		if !scanned[f] {
			t.Fatalf("%s is a reference document that scanDocFiles does "+
				"not cover, so the two doc gates read different "+
				"universes", f)
		}
	}

	total := 0
	for _, path := range refs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, sec := range moduleSections(path, string(raw)) {
			violations, n := checkFlagDefaultClaims(path, sec, lookup)
			for _, v := range violations {
				t.Error(v)
			}
			total += n
		}
	}

	if total != referenceFlagDefaultClaims {
		t.Errorf("found %d bound default-seconds claims across the "+
			"references, expected exactly %d — a new or removed claim "+
			"updates the tally, and lowering it needs a reason",
			total, referenceFlagDefaultClaims)
	}
}

// --- kill shapes, each measured before being pinned ---------------------------

func fixtureDefaults(m map[string][]int) flagDefaultLookup {
	return func(scope, flag string) ([]int, bool) {
		v, ok := m[flag]
		return v, ok
	}
}

// TestFlagDefaultClaimCatchesWrongNumber pins that shape
// itself: the 917 mutation that survived 2,159 tests dies here.
func TestFlagDefaultClaimCatchesWrongNumber(t *testing.T) {
	lookup := fixtureDefaults(map[string][]int{"--wait-timeout": {600}})

	sec := docSection{module: "byoc", text: "Bound the wait with " +
		"`--wait-timeout` (default 917 seconds)."}
	got, n := checkFlagDefaultClaims("fixture.txt", sec, lookup)
	if len(got) != 1 || n != 1 {
		t.Fatalf("want 1 violation from 1 claim, got %d from %d: %v",
			len(got), n, got)
	}
	if !strings.Contains(got[0], "--wait-timeout") ||
		!strings.Contains(got[0], "917") {
		t.Errorf("violation should name the flag and the claimed "+
			"number, got %q", got[0])
	}

	sec.text = "Bound the wait with `--wait-timeout` (default 600 seconds)."
	if got, n := checkFlagDefaultClaims("fixture.txt", sec,
		lookup); len(got) != 0 || n != 1 {
		t.Errorf("the true claim should pass and still be counted, "+
			"got %d violations from %d claims: %v", len(got), n, got)
	}

	// The measured escape: with two registrations disagreeing, the
	// documented value still matches ONE of them, and match-any let
	// that vouch for it. Match-all must fail the claim.
	split := fixtureDefaults(map[string][]int{
		"--wait-timeout": {600, 917}})
	if got, n := checkFlagDefaultClaims("fixture.txt", sec,
		split); len(got) != 1 || n != 1 {
		t.Errorf("divergent registrations must fail even the "+
			"documented value, got %d violations from %d claims: %v",
			len(got), n, got)
	}
}

// TestFlagDefaultClaimBindsToNearestPrecedingFlag proves binding is
// per-claim, not per-sentence: two claims in one sentence each judge
// their own flag, and the violation names the right one.
func TestFlagDefaultClaimBindsToNearestPrecedingFlag(t *testing.T) {
	lookup := fixtureDefaults(map[string][]int{
		"--wait-timeout":  {600},
		"--wait-interval": {10},
	})

	sec := docSection{module: "byoc", text: "Poll with " +
		"`--wait-timeout`, default 600 seconds, and `--wait-interval` " +
		"(default 5)."}
	got, n := checkFlagDefaultClaims("fixture.txt", sec, lookup)
	if n != 2 {
		t.Fatalf("want 2 claims judged, got %d", n)
	}
	if len(got) != 1 || !strings.Contains(got[0], "--wait-interval") {
		t.Fatalf("want exactly the --wait-interval claim to fail, "+
			"got: %v", got)
	}
}

// TestFlagDefaultClaimReadsDurationSpellings covers the Ns and
// backticked forms, and proves example values ("e.g. `45s`") are not
// read as claims — only the number after the default keyword is.
func TestFlagDefaultClaimReadsDurationSpellings(t *testing.T) {
	lookup := fixtureDefaults(map[string][]int{"--timeout": {30}})

	for _, text := range []string{
		"Bound each request (`--timeout`, default 30s).",
		"Set `--timeout` (Go duration, e.g. `45s`, `2m`; default `30s`).",
		"`--timeout` defaults to 30 seconds.",
	} {
		sec := docSection{module: "controlplane", text: text}
		if got, n := checkFlagDefaultClaims("fixture.txt", sec,
			lookup); len(got) != 0 || n != 1 {
			t.Errorf("%q: want 0 violations from 1 claim, got %d "+
				"from %d: %v", text, len(got), n, got)
		}
	}

	// The same e.g. sentence with a wrong registration proves the 30
	// was the value judged, not the 45 the example carries.
	sec := docSection{module: "controlplane", text: "Set `--timeout` (Go " +
		"duration, e.g. `45s`, `2m`; default `30s`)."}
	got, _ := checkFlagDefaultClaims("fixture.txt", sec,
		fixtureDefaults(map[string][]int{"--timeout": {45}}))
	if len(got) != 1 || !strings.Contains(got[0], "30") {
		t.Errorf("want the 30 judged against 45 to fail, got: %v", got)
	}
}

// TestFlagDefaultClaimSkipsUnanchoredSentence: no flag token, no
// judgement — the stated limit, pinned so it stays a decision.
func TestFlagDefaultClaimSkipsUnanchoredSentence(t *testing.T) {
	sec := docSection{module: "byoc",
		text: "The default 30 seconds applies to every request."}
	got, n := checkFlagDefaultClaims("fixture.txt", sec,
		fixtureDefaults(nil))
	if len(got) != 0 || n != 0 {
		t.Errorf("want no claims judged without an anchor, got %d "+
			"violations from %d claims: %v", len(got), n, got)
	}
}

// TestFlagDefaultClaimUnknownFlagIsReported: a claim about a flag
// the scope does not register with a seconds default is a violation,
// not a skip — that is the difference between "unchecked" and
// "checked against nothing".
func TestFlagDefaultClaimUnknownFlagIsReported(t *testing.T) {
	sec := docSection{module: "byoc",
		text: "Tune it with `--warp-speed` (default 10 seconds)."}
	got, n := checkFlagDefaultClaims("fixture.txt", sec,
		fixtureDefaults(nil))
	if len(got) != 1 || n != 1 {
		t.Fatalf("want 1 violation from 1 claim, got %d from %d: %v",
			len(got), n, got)
	}
}
