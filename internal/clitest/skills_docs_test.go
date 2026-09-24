package clitest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// namedDocs are agent- and user-facing docs that sit outside the
// skills/*/SKILL.md and docs/*.md globs below, so a glob cannot find
// them and they must be named explicitly. scanDocFiles asserts each
// one is actually folded into the scanned file set — a typo'd path
// here, or a refactor that stops appending this slice into the scan
// loop, must fail loudly rather than silently narrow either gate
// below back down to skills/*/SKILL.md.
// The reference half is no longer hand-maintained. managed's
// reference was missing from this list from the day the module
// shipped, and the resource split would have dropped every page the
// same way; ReferenceFiles derives both the index and every page from
// the modules, so neither omission can be made again.
var namedDocs = append([]string{
	"../../README.md",
	"../../examples/config.yaml",
	// The spec-vendoring and generated-parser notes moved here from
	// docs/index.md; without this entry the move would have taken
	// them out of the scanned universe (proven by mutation in
	// review: a fabricated flag in CONTRIBUTING.md passed while the
	// same sentence in docs/ failed).
	"../../CONTRIBUTING.md",
}, ReferenceFiles()...)

// scanDocFiles returns every agent- and user-facing doc this file's
// guards cover: skills/*/SKILL.md, docs/*.md, and namedDocs. Every
// guard in this file needs the identical file set — a scan that
// checked skills/ docs for dead commands but every doc for false
// claims (or vice versa) would be an inconsistent, hard-to-explain
// gate — so this is the one place that set is assembled, with the
// zero-matches and named-file-coverage self-checks that keep any
// scan from silently covering nothing. examples/config.yaml belongs
// here too: a shipped example rots back to a dead command exactly as
// silently as anything else.
//
// ROADMAP.md used to be in this set and is now gitignored, so there
// is no file to scan and no gate to lose. A planning file that leaves
// the repo also leaves the guards, which is the cost of keeping it
// out of PRs.
func scanDocFiles(t *testing.T) []string {
	t.Helper()

	skillMatches, err := filepath.Glob("../../skills/*/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(skillMatches) == 0 {
		t.Fatal("no skills/*/SKILL.md files matched — the glob is " +
			"broken and this guard is silently checking nothing")
	}

	docMatches := docsTreeFiles(t)

	files := make([]string, 0, len(skillMatches)+len(docMatches)+len(namedDocs))
	files = append(files, skillMatches...)
	files = append(files, docMatches...)
	files = append(files, namedDocs...)

	// Each named file must actually appear in the set returned above.
	// This is not redundant with os.ReadFile failing on a bad path: it
	// catches the case where namedDocs is edited but a refactor of the
	// append above stops folding it in, so a caller quietly scans zero
	// of the files it claims to cover.
	for _, named := range namedDocs {
		found := false
		for _, f := range files {
			if f == named {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s was not included in the scanned file set — "+
				"this guard's named-file coverage is broken", named)
		}
	}

	return files
}

// docsTreeFiles returns every .md file under docs/, at any depth. The
// population was a flat docs/*.md glob until the module directories
// landed, and that flat glob left all 22 workflow pages and the five
// reference preambles outside every claim gate. Two self-checks
// stop the widening being lost again: no files at all, and no file
// below the top level, are both fatal.
func docsTreeFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	nested := false
	err := filepath.WalkDir("../../docs", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		if filepath.Dir(path) != "../../docs" {
			nested = true
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .md files found under docs/ — the walk is broken " +
			"and this guard is silently checking nothing")
	}
	if !nested {
		t.Fatal("no .md file found below docs/ top level — the module " +
			"directories are outside the scanned population again")
	}
	return files
}

// assertAllNamedDocsScanned re-checks, after a scan loop has run,
// that every entry in namedDocs was actually opened — the second half
// of the named-file-coverage check scanDocFiles starts (that half only
// proves the path is *in the slice*; this half proves the loop that
// consumes the slice didn't skip it, e.g. via a broken continue or an
// early return added later).
func assertAllNamedDocsScanned(t *testing.T, scanned map[string]bool) {
	t.Helper()
	for _, named := range namedDocs {
		if !scanned[named] {
			t.Fatalf("%s was never read by the scan loop", named)
		}
	}
}

// TestEveryModuleReferenceIsScanned is the cross-check between the two
// sides that must agree about what a module reference IS: the files on
// disk, and the pages the modules embed and namedDocs is built from.
//
// namedDocs used to be hand-maintained, and managed's reference was
// missing from it from the day the module shipped — so every claim
// gate in this file silently skipped an entire module's documentation
// while reporting green. namedDocs is now derived, which is why the
// filesystem is the side walked here: a derivation compared against
// itself proves nothing, so this reads the disk independently and
// asserts the derived set covers every file it finds.
//
// A file the modules do NOT embed is reported for the same reason a
// missing one is: no gate in this file would ever open it.
//
// The walk is asserted non-empty for the usual reason: a broken
// pattern would make this guard pass by checking nothing.
func TestEveryModuleReferenceIsScanned(t *testing.T) {
	var matches []string
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		slashed := filepath.ToSlash(p)
		// A page lives under a module's llms/ directory; the index is
		// that module's llms.txt. Nothing else in internal/ takes
		// either shape.
		if d.Name() == "llms.txt" || strings.Contains(slashed, "/llms/") {
			matches = append(matches, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no llms.txt or llms/ page found under internal/ — " +
			"the walk is broken and this guard is checking nothing")
	}

	named := make(map[string]bool, len(namedDocs))
	for _, n := range namedDocs {
		abs, aerr := filepath.Abs(n)
		if aerr != nil {
			t.Fatal(aerr)
		}
		named[abs] = true
	}

	for _, m := range matches {
		abs, aerr := filepath.Abs(m)
		if aerr != nil {
			t.Fatal(aerr)
		}
		if !named[abs] {
			t.Errorf("%s is a module reference document that no module "+
				"embeds, so no gate in this file reads it — give it a "+
				"Documents() entry, and give the module a moduleSections "+
				"scope if its vocabulary differs from byoc's", m)
		}
	}
}

// normalizeWhitespace collapses every run of whitespace, including
// newlines, to a single space. Markdown reflow at this project's
// 79-column wrap rule routinely splits a command across two lines
// (e.g. "pgedge starfleet byoc\n  doctor"), and a raw strings.Contains check
// against the unmodified file content passes straight through that
// case — reporting the guard green on a file that still sends an
// agent to a deleted command. Normalising first is what makes the
// wrapped occurrence the normal case it now is, rather than a gap.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// removedCommands are the command spellings the CLI no longer has and
// no doc may name. removedTopLevelSpellings below is the second group.
var removedCommands = []string{
	"pgedge skill install",
	"skill status",
	"skill uninstall",
	// The account-module split moved these; any doc still
	// naming them sends a user or an agent to "unknown command".
	"pgedge byoc auth",
	"pgedge byoc doctor",
	"pgedge byoc invite",
	"pgedge byoc membership",
}

// removedTopLevelSpellings are the top-level module words the
// 2026-08-07 cloud restructure deleted outright. The four
// "pgedge byoc <verb>" entries above only cover the verbs the earlier
// account split moved; the restructure went further and removed the
// module words themselves, so `pgedge byoc cluster list` — a spelling
// no entry above matches — now exits 2 as an unknown command. Every
// surface is `pgedge starfleet ...` now, and "pgedge starfleet byoc" does not
// contain "pgedge byoc", so these needles cannot fire on correct
// current prose.
//
// The `llms` entries are separate needles rather than consequences of
// the first three: `pgedge llms byoc` contains neither "pgedge byoc"
// nor "pgedge llms starfleet byoc", so nothing else catches a doc that
// still sends an agent to the pre-restructure reference selector.
var removedTopLevelSpellings = []string{
	"pgedge account",
	"pgedge byoc",
	"pgedge managed",
	"pgedge llms account",
	"pgedge llms byoc",
	"pgedge llms managed",
	"pgedge cloud",
	"pgedge cp",
	"pgedge llms cloud",
	"pgedge llms cp",
}

// removedCommandViolations reports one violation per removed spelling
// content names. It is split
// out of the test so the gate itself can be positive-controlled against
// fixtures (TestRemovedCommandGateCatchesTopLevelSpellings) rather than
// only against the repo's real docs, which are — correctly — clean, and
// so prove nothing about whether the check still bites.
func removedCommandViolations(path, content string) []string {
	normalized := normalizeWhitespace(content)

	var violations []string
	for _, phrase := range allRemovedCommands() {
		// The phrase must be normalised too, not just the file
		// content: a multi-word entry in this list written with
		// unusual spacing (e.g. a stray double space from a copy-
		// paste edit) would otherwise never match the
		// single-spaced form every real file collapses to.
		needle := normalizeWhitespace(phrase)
		if strings.Contains(normalized, needle) {
			violations = append(violations, fmt.Sprintf(
				"%s still references removed command %q (matched after "+
					"whitespace normalisation) — that command no longer "+
					"exists in the CLI", path, phrase))
		}
	}
	return violations
}

func allRemovedCommands() []string {
	all := make([]string, 0,
		len(removedCommands)+len(removedTopLevelSpellings))
	all = append(all, removedCommands...)
	all = append(all, removedTopLevelSpellings...)
	return all
}

// TestSkillDocsHaveNoRemovedCommands guards every agent- and
// user-facing doc in this repository — skills/*/SKILL.md (the
// external source of truth for AI-agent skills, distributed outside
// the binary via manual copy or the skills CLI, see README.md),
// docs/*.md, README.md, the llms.txt index and every module's
// llms.txt (all //go:embed-ed into the binary itself), ROADMAP.md,
// and examples/config.yaml — from re-referencing a command the CLI
// no longer has. Fix the doc, not this test, when it fails.
//
// This guard is about commands that no longer EXIST. It says nothing
// about a command that exists but is documented with a false claim
// about what it returns — that is TestSkillDocsMatchVerifiedAPIBehaviour
// below, deliberately a separate test: the two failure modes have
// different fixes (repoint the reader at the new command vs. correct
// the described behaviour) and blurring them into one gate would blur
// that distinction for whoever reads the failure.
func TestSkillDocsHaveNoRemovedCommands(t *testing.T) {
	files := scanDocFiles(t)

	scanned := make(map[string]bool, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanned[path] = true

		for _, msg := range removedCommandViolations(path, string(content)) {
			t.Error(msg)
		}
	}

	assertAllNamedDocsScanned(t, scanned)
}

// TestRemovedCommandGateCatchesTopLevelSpellings is the positive
// control the gate above cannot supply for itself. Every real doc is
// clean, so the loop reporting nothing is exactly what a loop checking
// nothing looks like. These fixtures assert each deleted spelling fires
// in a normal doc and that current spellings do not.
func TestRemovedCommandGateCatchesTopLevelSpellings(t *testing.T) {
	for _, phrase := range removedTopLevelSpellings {
		t.Run("flagged in a normal doc: "+phrase, func(t *testing.T) {
			doc := "Run `" + phrase + " list` to see them.\n"
			if got := removedCommandViolations(
				"../../README.md", doc); len(got) == 0 {
				t.Fatalf("the gate accepted %q in README.md", phrase)
			}
		})
	}

	t.Run("current spellings are not flagged", func(t *testing.T) {
		doc := "Run `pgedge starfleet byoc cluster list`, " +
			"`pgedge starfleet managed database list`, `pgedge starfleet auth " +
			"login`, `pgedge llms starfleet`, `pgedge llms starfleet byoc` and " +
			"`pgedge llms starfleet managed`.\n"
		if got := removedCommandViolations(
			"../../README.md", doc); len(got) != 0 {
			t.Fatalf("the gate flagged current spellings: %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestSkillDocsMatchVerifiedAPIBehaviour
//
// A phrase-blocklist version of this gate was defeated in one review
// pass by rephrasings and output layouts it had never seen (the
// rephrasings are pinned in
// TestNoEnvelopeCheckCatchesRewordedCaptureInstructions). A blocklist
// of wrong answers is a memo about the past; it says nothing about a
// wording nobody has written yet.
//
// This gate checks two things structurally instead:
//
//  1. task_id: BYOC never wraps a mutation's result in a task/envelope
//     object (verified live 2026-07-30: `byoc cluster list`,
//     `database list`, `task list` and `ingress list` responses on the
//     live tenant contain no `task_id` key at any nesting depth, and
//     `internal/starfleet/byoc/api` + `internal/starfleet/account/api` contain zero
//     `task_id` JSON tags). The default posture for a field that does
//     not exist is that ANY mention of it is wrong — so this scans
//     per-sentence for a sentinel phrase and flags every occurrence
//     unless the author annotated that specific occurrence with a
//     doc-gate marker (see "the deliberately-wrong opt-out" below).
//     That is what makes it survive a rewording: a new way of saying
//     "capture the task_id" still contains the token "task_id" and
//     still trips the check.
//
//  2. status/state values: rather than list wrong values (unbounded —
//     "active", "registered", and whatever gets invented next), this
//     checks every status/state value against a small, source-verified
//     ALLOWlist, across all three layouts the docs actually use (JSON,
//     vertical "Field  Value" text, and a table's STATUS/STATE
//     column). Anything not on the list fails, in any layout, by
//     construction.
//
// Neither check tries to work out whether a sentence naming a wrong
// value is asserting it or warning against it — three rounds of
// polarity inference each had to be corrected by the next (the round
// table is in "the doc-gate marker opt-out" below). The author who
// must quote a wrong value in order to warn against it says so
// explicitly, per occurrence, at the point of use.
//
// Both checks are scoped by which product API the surrounding text
// documents, not by exempting a file. task_id is a real, correct field
// on the unrelated `controlplane` module's Task type (internal/controlplane/api's
// generated TaskId string `json:"task_id"`), and controlplane's status/state
// vocabulary genuinely differs from BYOC's (cp uses "completed" where
// BYOC uses "succeeded"; BYOC has no "canceling" or "restoring" at
// all). Scoping by module — via moduleSections, which tags every file
// (or "## Module: X"-headed region of one) by which single module it
// documents — is what keeps a rule correct for one module from
// becoming a blind spot in the other, or a false alarm against the
// other's genuinely different vocabulary.
// ---------------------------------------------------------------------------

// docSection is one module-scoped region of a doc's text. Every check
// below is run once per section, with that section's module deciding
// which allowlist and which sentinel scope applies.
type docSection struct {
	module string // "byoc" (covers byoc+top-level), "starfleet", "managed" or "controlplane"
	text   string
}

var moduleHeaderRe = regexp.MustCompile(`(?m)^## Module: (\w+)`)

// generatedBlockRe matches one docgen block. It is duplicated here
// rather than imported from internal/docgen because these gates must
// keep working on a doc whose generator has been changed or removed —
// a gate that stops seeing generated text the moment the generator
// changes shape is a gate that silently starts checking the wrong
// thing.
var generatedBlockRe = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED: [^>]+? -->.*?<!-- END GENERATED -->`)

// The reference pages under docs/reference wrap a whole command tree in
// PAGE markers and every llms index ends in a ROUTING table, and
// neither form matches generatedBlockRe. Until docs/reference joined
// the population that cost nothing; the first widened run read 1,580
// generated lines of starfleet-byoc.md as prose and flagged two table
// rows for a task_id nobody wrote.
var generatedPageRe = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED PAGE: [^>]+? -->.*?<!-- END GENERATED PAGE -->`)
var generatedRoutingRe = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED ROUTING: [^>]+? -->.*?<!-- END GENERATED ROUTING -->`)

// stripGeneratedBlocks blanks out every docgen block while PRESERVING
// each newline it spanned, for the same line-count-alignment reason
// stripDocGateMarkers does it — checkStatusStateValues walks stripped
// lines and indexes rawLines by the same i.
//
// WHY THE CLAIM GATES MUST NOT READ GENERATED TEXT. These gates exist
// to catch hand-written prose drifting away from the code. Generated
// text is derived FROM the code, so checking it is checking cobra
// against cobra: it can never find a real drift, and it does find false
// ones. It found one immediately. `pgedge starfleet byoc task get` declares
// `Use: "get <task_id>"`, so the generated usage line puts the literal
// task_id into byoc-scoped text, and checkNoEnvelopeClaims flagged
// three blocks for a claim nobody wrote. The remedy could not be a
// doc-gate marker either — a marker inside a generated block is erased
// by the next `make docs`.
//
// This is now a real boundary in the document and the gates respect it:
// inside the markers, the code is the source of truth and review is the
// control; outside them, an author is asserting something about the
// code and these gates are the control.
//
// The stripper trusts any marker it finds, so it is only safe because
// TestGeneratedMarkersSitOnlyWhereTheGeneratorWrites fails on a marker
// outside the file and form the generator writes.
func stripGeneratedBlocks(text string) string {
	blank := func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	}
	for _, re := range []*regexp.Regexp{
		generatedBlockRe, generatedPageRe, generatedRoutingRe,
	} {
		text = re.ReplaceAllStringFunc(text, blank)
	}
	return text
}

// docsScopes maps a docs/ path fragment to its module. Reference pages
// first: their filenames are more specific than any directory.
var docsScopes = []struct{ key, scope string }{
	{"/docs/reference/starfleet-byoc.md", "byoc"},
	{"/docs/reference/starfleet-managed.md", "managed"},
	{"/docs/reference/controlplane.md", "controlplane"},
	{"/docs/reference/starfleet.md", "starfleet"},
	{"/docs/managed/", "managed"},
	{"/docs/byoc/", "byoc"},
	{"/docs/controlplane/", "controlplane"},
	{"/docs/starfleet/", "starfleet"},
}

// coreDocsDirs are the docs/ directories whose pages are not scoped to
// one module: the flat guides and the generated reference pages, whose
// module members docsScopes names by file.
var coreDocsDirs = map[string]bool{"docs": true, "docs/reference": true}

// moduleSections splits a doc's content into module-scoped regions. A
// module's own llms.txt, and the single-topic cloud, cp and managed
// SKILL.md files (which have no "## Module:" header to split on), are
// scoped by path — longest/most-specific key first, since the cloud
// sub-references (byoc, managed) nest under internal/starfleet/ alongside
// starfleet's own llms.txt. A file with real "## Module: X" headers is
// split on them. A docs/ page is scoped by docsScopes. Everything else
// is one "byoc" section, which is correct because none of those files
// documents cloud, cp or managed status/state or task vocabulary.
//
// There is no account fold any more. Account's docs now live in
// "starfleet" scope, which has its own struct universe
// (scopeSourceDirs' "starfleet" entry) rather than borrowing byoc's —
// account never had a resource lifecycle status of its own, and
// neither does cloud, so starfleet's status/state vocabulary still falls
// through to byoc's (allowedStatusValues) exactly as account's did.
func moduleSections(path, content string) []docSection {
	// Generated blocks are removed before any scoping or claim check
	// runs, so every caller of moduleSections gets the same
	// hand-written-only view. Doing it here rather than in each gate is
	// what stops a future gate being added that forgets to.
	content = stripGeneratedBlocks(content)

	// A reference file IS its scope. The key stops at "/llms" so it
	// matches a module's index (".../llms.txt") and every page under
	// it (".../llms/cluster.txt", ".../llms/database/mcp.txt") alike —
	// a page inherits the scope of the module whose directory it sits
	// in. Most-specific path first: the starfleet sub-references live
	// UNDER internal/starfleet/, so the plain "/starfleet/llms" key
	// must be checked after them.
	for _, kv := range []struct{ key, scope string }{
		{"/starfleet/byoc/llms", "byoc"},
		{"/starfleet/managed/llms", "managed"},
		{"/starfleet/llms", "starfleet"},
		{"/controlplane/llms", "controlplane"},
	} {
		if strings.Contains(path, kv.key) {
			return []docSection{{module: kv.scope, text: content}}
		}
	}

	// Single-topic skill files map to their scope by name. byoc's
	// skill is the default-scope fallback below, like today.
	for _, kv := range []struct{ key, scope string }{
		{"pgedge-starfleet/SKILL.md", "starfleet"},
		{"pgedge-controlplane/SKILL.md", "controlplane"},
		{"pgedge-managed/SKILL.md", "managed"},
	} {
		if strings.Contains(path, kv.key) {
			return []docSection{{module: kv.scope, text: content}}
		}
	}

	// A docs page is scoped by its directory and a module reference
	// page by its filename. Flat docs/*.md and reference/pgedge.md are
	// core: cross-module by nature, and they keep the byoc fallback
	// below. TestEveryDocsDirectoryIsScoped keeps this table and the
	// directories on disk in step, so a new directory cannot inherit
	// byoc's vocabulary by falling through.
	for _, kv := range docsScopes {
		if strings.Contains(path, kv.key) {
			return []docSection{{module: kv.scope, text: content}}
		}
	}

	locs := moduleHeaderRe.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return []docSection{{module: "byoc", text: content}}
	}

	sections := make([]docSection, 0, len(locs)+1)
	sections = append(sections,
		docSection{module: "byoc", text: content[:locs[0][0]]})
	for i, loc := range locs {
		name := content[loc[2]:loc[3]]
		module := "byoc"
		switch name {
		case "controlplane":
			module = "controlplane"
		case "managed":
			module = "managed"
		case "starfleet":
			module = "starfleet"
		}
		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		sections = append(sections,
			docSection{module: module, text: content[loc[0]:end]})
	}
	return sections
}

// --- 1. the task_id / task-envelope check -----------------------------------

// noEnvelopeSentinels are phrases that, within byoc-scoped text, each
// assert BYOC wraps a mutation's result in a task/envelope object.
// Widening this list is safe by construction: a new sentinel goes
// through the same opt-out the existing ones do (see
// TestNoEnvelopeCheckHonoursDeliberatelyWrongMarker), so it does not
// need re-verifying against every file by hand — any correct sentence
// it newly catches fails loudly and is annotated once.
var noEnvelopeSentinels = []string{
	"task_id",
	"task output",
	"task's output",
}

func isNoEnvelopeSentinel(name string) bool {
	for _, sentinel := range noEnvelopeSentinels {
		if sentinel == name {
			return true
		}
	}
	return false
}

// --- the doc-gate marker opt-out --------------------------------------------
//
// Some of this repo's own correct sentences have to quote a wrong value
// in order to warn against it: "There is no `task_id` in any BYOC
// response", "presence in the list plus a populated `url` is the
// signal, not a `"status": "registered"` value". Both checks in this
// file would flag those as claims, because both checks look only at
// whether the wrong phrase is present.
//
// WHY THIS IS AN AUTHOR-WRITTEN MARKER AND NOT AUTOMATIC DETECTION.
// Three rounds tried to work it out from the prose, and each round's
// fix had to be corrected in the next:
//
//	round 1  no negation awareness at all      → flagged every correct
//	                                             warning sentence
//	round 2  negation word anywhere in the      → one unrelated "no"
//	         sentence excuses the sentence        excused a real
//	                                             falsehood later in
//	                                             the same sentence
//	round 3  negation word within 20 chars of   → ordinary hedged prose
//	         the flagged phrase                   puts its negation
//	                                             43-72 chars away and
//	                                             was flagged anyway
//
// Round 3's window could not be fixed by moving it: widening reopens
// round 2's false negative, narrowing makes round 3's false positives
// worse. So the polarity is no longer inferred. An author who must
// quote a wrong value says so, at the point of use, once per
// occurrence:
//
//	<!-- doc-gate: deliberately-wrong task_id — this sentence
//	documents the field's absence -->
//
// THE SECOND VERB, AND WHY THERE ARE EXACTLY TWO.
// deliberately-wrong asserts the named phrase is WRONG and quoted on
// purpose. That covers every case where a doc warns against a value,
// and it covered every case this mechanism was first built for. It does
// not cover the other reason a correct sentence trips these checks: the
// phrase is RIGHT, but right under a different module's scope than the
// section it is written in. ROADMAP.md is a flat planning table with no
// "## Module:" headers, so moduleSections scopes all of it to byoc,
// and its row for controlplane's real `controlplane task cancel <task_id>` command names a
// field that genuinely exists on controlplane's Task type
// (internal/controlplane/api/client.gen.go). Annotating that row
// deliberately-wrong would put a FALSE statement into a shipped doc,
// which is the same defect class these gates exist to catch. So:
//
//	<!-- doc-gate: correct-for controlplane task_id — controlplane's Task type carries a
//	real task_id field -->
//
// The verbs are not interchangeable and neither is a superset of the
// other. deliberately-wrong keeps its exact original meaning; widening
// it to mean "do not check this" is the thing to refuse. correct-for
// additionally names the module the phrase is correct for, and that
// module must be a real gate scope AND must differ from the section's
// own scope — a `correct-for byoc` marker inside a byoc section is
// rejected, because "correct under another module's scope" is the only
// thing this verb is allowed to assert. That rejection is what stops
// correct-for degrading into the blanket amnesty it replaced.
//
// It replaced one, specifically: a prose-derived skip of any sentence
// naming a controlplane command, under which a genuinely false BYOC task_id
// claim passed every gate. That amnesty was essential for exactly
// ONE real site, the ROADMAP.md row above; one annotation bought its
// deletion.
//
// This is deliberately NOT the per-file or per-directory exemption
// these gates ban, in the same way `//nolint:rule // reason` is not
// `//nolint` at the top of a file. The differences are essential and
// every one of them is enforced by a test below:
//
//   - it is per sentence and per named phrase — never per file, never
//     per section (TestDocGateMarkerHasNoWiderScope)
//   - it names the one phrase or value it excuses, so it cannot
//     blanket-excuse a different wrong value in the same sentence
//     (TestDocGateMarkerExcusesOnlyWhatItNames)
//   - it carries a reason; a marker without one is rejected as
//     malformed (TestDocGateMarkerRequiresPhraseAndReason)
//   - it excuses something or it is an error: a marker whose phrase
//     was never flagged in the sentence it annotates is reported as
//     stale, misplaced or redundant (TestDocGateMarkerMustExcuseSomething)
//   - a correct-for marker must name a real gate scope other than the
//     one it sits in, so it can never excuse a claim about its own
//     section's module (TestCorrectForMarkerRejectsOwnAndUnknownScope)
//   - it is greppable in one command:
//     `grep -rn 'doc-gate:' . --include='*.md' --include='*.txt' \
//     --include='*.yaml'`
//
// It costs something, and the cost was accepted knowingly: the llms.txt
// references are //go:embed-ed and printed raw by `pgedge llms`, so
// these markers ship to AI agents as part of the text they read. That
// is why the syntax is an HTML comment that states a true fact about
// the sentence it follows ("deliberately-wrong <phrase>, because
// <reason>") rather than an opaque token like `nocheck` — an agent that
// reads one learns the same thing the gate does. The alternative
// considered was a registry of exempted sentences in this file; it was
// rejected because sentenceSplitRe's coarse split makes several of the
// real sentences 900+ characters long, spanning whole code fences and
// markdown tables, so registry keys would be enormous and would go
// stale on any unrelated edit inside the fence.

// docGateMarkerRe matches one complete marker and captures its verb,
// its operand and its reason. The operand is written bare — no
// backticks and no quotes — because it is compared against the raw
// sentinel ("task_id") or the raw status/state value ("registered") the
// checks below extract. The em dash is the separator: the operand may
// not contain one, the reason may.
//
// The operand's shape depends on the verb, and docGateOperand splits it:
//
//	deliberately-wrong <phrase>
//	correct-for <module> <phrase>
//
// The marker is matched against whitespace-normalised text for the
// sentence pass, so a marker the 79-column wrap rule split across two
// lines still matches. (?s) is there for the line-oriented pass in
// checkStatusStateValues, which sees raw text: without it the reason
// group stops at a newline and a wrapped marker is not recognised at
// all. What a marker must NOT contain is sentence-ending punctuation
// followed by a space: that would split it off the sentence it
// annotates. docGateOpenerRe exists to turn exactly that mistake into a
// clear failure instead of a silently ignored marker — and it is also
// what catches a misspelled verb, since a verb this pattern does not
// list leaves its opener unconsumed.
var docGateMarkerRe = regexp.MustCompile(
	`(?s)<!--\s*doc-gate:\s*(deliberately-wrong|correct-for)\s+` +
		`([^—>]+?)\s*—\s*(.*?)\s*-->`)

// gateScopes are the module scopes correct-for accepts as a target.
//
// The rule, which is what governs whether to add one: correct-for
// asserts the phrase IS a real declared field of the named module, and
// the gate resolves names against json/yaml struct tags. So widen this
// only for a field that module genuinely declares, named from another
// module's section — never to soften a deliberately-wrong. An
// undeclared metrics column is the case that tempts you and is exactly
// the case this must refuse: no module declares it, so correct-for
// would assert something false.
//
// The asymmetry is the feature. Too NARROW fails loudly — the phrase
// still fires and the marker is ignored, which is how the gap in this
// map gets found. Too WIDE fails silently, accepting a false
// assertion. That is why the default is to leave this alone: when a
// correct-for marker for an undeclared column is ignored, widening the
// map would make that marker pass rather than making it honest.
//
// It is deliberately NOT every scope docSection can produce —
// moduleSections also emits "starfleet" and "managed". There is no account
// fold to guard against any more: account is not a module of this CLI
// at all, so naming it here would just be an always-unknown scope like
// any other typo.
var gateScopes = map[string]bool{"byoc": true, "controlplane": true}

// docGateOperand splits a marker's operand into the module it claims
// (empty for deliberately-wrong) and the phrase it names. It reports
// ok=false when the operand does not carry what the verb requires — a
// correct-for with no phrase after its module, or either verb with an
// empty phrase.
func docGateOperand(verb, operand string) (module, phrase string, ok bool) {
	fields := strings.Fields(operand)
	if verb == "correct-for" {
		if len(fields) < 2 {
			return "", "", false
		}
		return strings.ToLower(fields[0]),
			strings.ToLower(strings.Join(fields[1:], " ")), true
	}
	if len(fields) == 0 {
		return "", "", false
	}
	return "", strings.ToLower(strings.Join(fields, " ")), true
}

var docGateOpenerRe = regexp.MustCompile(`<!--\s*doc-gate:`)

// stripDocGateMarkers removes marker text while PRESERVING every newline
// it spanned, so a line-oriented caller sees the same number of lines
// with the marker's characters gone.
//
// THE INVARIANT IS LINE-COUNT IDENTITY WITH THE RAW TEXT, and it is
// essential rather than defensive. checkStatusStateValues's
// table-scanning loop walks the STRIPPED lines and indexes rawLines[i]
// by that same i, so the moment the two disagree in length, every line
// after a wrapped marker is compared against the wrong raw line. A plain
// ReplaceAllString would also stop a marker being read as a table cell,
// so that is NOT what the newline preservation buys — alignment is.
//
// Both failure directions follow from the same one-line shift:
//
//   - a table wrongly EXTENDED past a real paragraph break, when a
//     genuine blank line is compared against a raw line that still holds
//     the marker's second half, so it is misread as marker residue,
//     headerIdx is never cleared, and the following prose is checked as
//     a table row (one false positive on a correct doc);
//   - a table wrongly ENDED early, when a line that is blank in raw but
//     not in stripped clears headerIdx and the rest of that table goes
//     unchecked (a false negative).
//
// Callers must therefore treat a line that was non-blank before
// stripping and is blank after as "skip this line but keep the header"
// rather than as a paragraph break — otherwise a marker written between
// a table's header row and its data rows switches the STATUS/STATE
// column detector off for every remaining row of that table, which is
// the regression this function was introduced to fix.
//
// TestDocGateMarkerDoesNotDisableTableColumnDetector covers the
// headerIdx behaviour but NOT the alignment invariant — all six of its
// cases survive reverting this function to a plain ReplaceAllString.
// TestDocGateMarkerKeepsStrippedAndRawLineCountsAligned is the one that
// binds it, and it is the case to run first if this is ever "simplified".
func stripDocGateMarkers(text string) string {
	return docGateMarkerRe.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
}

// annotatedSentence is one sentence of a doc section with every
// doc-gate marker lifted out of its text and turned into a spendable
// allowance. text is what the checks below scan, so a marker naming
// "task_id" can never itself trip the task_id sentinel.
//
// allowed COUNTS the markers naming each lowercased phrase, and spend
// decrements. Counting rather than presence is essential: a single
// sentence can produce more than one violation for one distinct value,
// because jsonStatusRe matches `"status": "registered"` and `"state":
// "registered"` as separate occurrences. While allowances were deduped
// by name and spent as booleans, such a sentence fired no matter how
// many markers its author added — a correct sentence with no remedy,
// which is the exact failure class this whole design exists to end,
// reintroduced in a narrower form. N markers now clear N occurrences and
// N-1 markers still fire; see
// TestDocGateMarkerCoversRepeatedValueOccurrences.
type annotatedSentence struct {
	text      string
	allowed   map[string]int
	malformed []string
	misscoped []string
}

// spend reports whether this sentence carries an unspent allowance for
// phrase that own claims, consuming one if so.
//
// own is not decoration. The two checks below partition the allowance
// namespace between them — checkNoEnvelopeClaims owns the sentinel
// names, checkStatusStateValues owns everything else — and the SAME
// predicate must govern spending and dead-marker reporting or the
// partition holds in one direction only. It did once: reporting was
// partitioned while spending was not, so `{"status": "task_id"}` beside
// a marker naming task_id was excused by the status detector, breaking
// the "names the one phrase it excuses" property that is the entire
// justification for this opt-out existing.
func (s *annotatedSentence) spend(phrase string, own func(string) bool) bool {
	if !own(phrase) || s.allowed[phrase] <= 0 {
		return false
	}
	s.allowed[phrase]--
	return true
}

// unspent returns the sorted allowance names this sentence declared,
// still has spare markers for, and own claims. One name is reported
// once however many spare markers it has — the author's fix is the same
// either way.
func (s *annotatedSentence) unspent(own func(string) bool) []string {
	var names []string
	for name, spare := range s.allowed {
		if spare > 0 && own(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ownsSentinelNames, ownsValueNames, ownsFieldNames and ownsFlagNames
// are the four arms of that partition, named so a call site cannot
// silently pass the wrong one. They must stay TOTAL (every name owned)
// and DISJOINT (owned once), which is why ownsValueNames is defined by
// subtraction rather than spelled out independently — two
// positively-stated predicates drift, and the drift is invisible until
// a marker is spent by one check and reported as excusing nothing by
// another. TestFieldClaimAllowanceNamespaceIsDisjoint pins it,
// `backing_up` being the name that makes it non-obvious: a real byoc
// status value that also carries an underscore. Flag names keep their
// `--` prefix through extraction, markers and reporting, so the prefix
// is what carves them out of the other three arms.
func ownsSentinelNames(name string) bool { return isNoEnvelopeSentinel(name) }
func ownsValueNames(name string) bool {
	return !isNoEnvelopeSentinel(name) && !ownsFieldNames(name) &&
		!ownsFlagNames(name)
}

// annotateSentences splits whitespace-normalised section text into
// sentences and lifts every doc-gate marker out of the text into the
// annotated sentence it applies to.
//
// A marker written after a sentence's closing punctuation — the natural
// place to put an annotation, and the only placement that leaves the
// prose readable in the raw reference text an agent is handed — lands
// at the START of the next split fragment. So a marker with nothing but
// whitespace before it attaches to the PRECEDING sentence. That is the
// only positional rule in this mechanism, it is arithmetic rather than
// grammar, and it is what lets a marker sit on its own line under the
// sentence it annotates. A marker placed anywhere else inside a
// sentence attaches to that sentence, so both placements work.
//
// module is the enclosing section's scope, and it is needed here rather
// than at the point of use because a correct-for marker naming that
// same scope must not create an allowance at all. Recording the
// allowance and rejecting it later would mean the rejection and the
// spending could drift apart — the exact failure that broke the
// own/spend partition once already (see annotatedSentence.spend).
func annotateSentences(module, normalized string) []annotatedSentence {
	sentences := sentenceSplitRe.Split(normalized, -1)
	out := make([]annotatedSentence, len(sentences))

	for i, raw := range sentences {
		var clean strings.Builder
		prev := 0
		matched := make(map[int]bool)

		for _, loc := range docGateMarkerRe.FindAllStringSubmatchIndex(raw, -1) {
			matched[loc[0]] = true
			clean.WriteString(raw[prev:loc[0]])
			prev = loc[1]

			verb := strings.ToLower(strings.TrimSpace(raw[loc[2]:loc[3]]))
			scope, name, ok := docGateOperand(verb,
				strings.TrimSpace(raw[loc[4]:loc[5]]))
			reason := strings.TrimSpace(raw[loc[6]:loc[7]])
			if !ok || name == "" || reason == "" {
				out[i].malformed = append(out[i].malformed,
					strings.TrimSpace(raw[loc[0]:loc[1]]))
				continue
			}

			// A correct-for marker asserts the phrase is right under
			// ANOTHER module's scope. Naming an unknown scope, or the
			// section's own, asserts nothing — reject it before it can
			// become a spendable allowance.
			if verb == "correct-for" &&
				(!gateScopes[scope] || scope == module) {
				out[i].misscoped = append(out[i].misscoped,
					strings.TrimSpace(raw[loc[0]:loc[1]]))
				continue
			}

			// clean holds the sentence text so far with earlier markers
			// already removed, so a marker preceded only by other
			// markers still counts as leading.
			target := i
			if i > 0 && strings.TrimSpace(clean.String()) == "" {
				target = i - 1
			}
			if out[target].allowed == nil {
				out[target].allowed = make(map[string]int)
			}
			out[target].allowed[name]++
		}

		clean.WriteString(raw[prev:])
		out[i].text = clean.String()

		// Any doc-gate opener the full marker pattern did not consume is
		// a marker nobody will ever honour — most likely one whose
		// reason contained a ". " and got split in half. Report it
		// rather than ignoring it.
		for _, loc := range docGateOpenerRe.FindAllStringIndex(raw, -1) {
			if !matched[loc[0]] {
				out[i].malformed = append(out[i].malformed,
					strings.TrimSpace(raw))
			}
		}
	}

	return out
}

// checkDocGateMarkers reports malformed markers. It is module-agnostic
// and deliberately separate from the two content checks: a marker that
// no check will ever honour is a defect regardless of which check it
// was aimed at, and reporting it from one of the two content checks
// would either miss controlplane-scoped sections (checkNoEnvelopeClaims returns
// early there) or report every marker twice.
func checkDocGateMarkers(path string, sec docSection) []string {
	var violations []string
	for _, sentence := range annotateSentences(sec.module,
		normalizeWhitespace(sec.text)) {
		for _, bad := range sentence.malformed {
			violations = append(violations, fmt.Sprintf(
				"%s: unusable doc-gate marker in %q — the only two forms "+
					"this gate honours are `<!-- doc-gate: "+
					"deliberately-wrong <phrase> — <reason> -->` and "+
					"`<!-- doc-gate: correct-for <module> <phrase> — "+
					"<reason> -->`, each with a non-empty phrase and a "+
					"non-empty reason, and no sentence-ending punctuation "+
					"inside the reason (that splits the marker off the "+
					"sentence it annotates)", path, bad))
		}
		for _, bad := range sentence.misscoped {
			violations = append(violations, fmt.Sprintf(
				"%s: doc-gate correct-for marker names no usable scope in "+
					"%q — it must name a gate scope (%s) OTHER than the "+
					"%q section it sits in, because the only thing this "+
					"verb may assert is that the phrase is correct under "+
					"ANOTHER module's scope. Note that account is not one "+
					"of these scopes at all; if the phrase is simply wrong "+
					"and quoted on purpose, the verb you want is "+
					"deliberately-wrong",
				path, bad, strings.Join(sortedKeys(gateScopes), ", "),
				sec.module))
		}
	}
	return violations
}

// byocCommandMentionRe matches a sentence naming byoc at all — the
// module name on its own is enough, not just "byoc <resource>".
//
// This is the gate's ONLY remaining prose-derived scoping rule, and the
// direction it runs in is why it is allowed to stay. It ADMITS a
// sentence to the check, so a loose pattern only means more is
// verified. Its deleted counterpart ran the other way: a
// `\bcp (task|database|host|cluster|config)\b` match REMOVED a sentence
// from the check entirely, so any false byoc claim sharing a sentence
// with a controlplane command name was waved through. That amnesty is gone, and
// its one real site is annotated `correct-for cp` in ROADMAP.md —
// see the marker vocabulary note above and
// TestNoEnvelopeCheckFlagsCPCommandMentionWithoutMarker, which pins the
// probe that used to return zero violations.
//
// A bare "byoc" counts: a sentence in a cp doc that mentions byoc at
// all is discussing the other module's behaviour, and any envelope
// claim in it is a BYOC claim.
var byocCommandMentionRe = regexp.MustCompile(`\bbyoc\b`)

// sentenceMentionsByocCommand reports whether a sentence makes its
// claim about BYOC. It is what keeps a controlplane-scoped section from being a
// blanket exemption; see checkNoEnvelopeClaims.
func sentenceMentionsByocCommand(sentence string) bool {
	return byocCommandMentionRe.MatchString(strings.ToLower(sentence))
}

// sentenceSplitRe splits normalised (single-spaced) text on sentence
// boundaries. It is intentionally coarse — splitting only on .!? —
// because the sentence is the unit a doc-gate marker attaches to, and a
// finer split would separate a marker from the clause it annotates. It
// is also what keeps a warning like "there is no task_id to capture"
// whole rather than isolating the clause containing the sentinel.
//
// The consequence to know about: a "sentence" spanning a fenced code
// block or a markdown table can run to hundreds of characters, because
// neither contains .!? followed by whitespace. That is why a marker's
// scope is bounded by the phrase it NAMES and not by its sentence alone
// — see TestDocGateMarkerExcusesOnlyWhatItNames.
var sentenceSplitRe = regexp.MustCompile(`[.!?]+\s`)

// checkNoEnvelopeClaims returns one violation message per sentence in
// sec that names a noEnvelopeSentinel without a doc-gate marker
// excusing that specific phrase. It returns messages rather than taking
// a *testing.T so the regression tests below can call it directly
// against a fixture and inspect the result, instead of faking a
// *testing.T to capture t.Errorf calls.
//
// BOTH CHECKS IN THIS FILE MUST HONOUR EXACTLY THE SAME OPT-OUT, AND
// NEITHER MAY GROW A POLARITY HEURISTIC OF ITS OWN — this is the
// sibling note to the one on checkStatusStateValues below, and it must
// keep saying the same thing for the same reason. The two checks were
// built one round apart and went negation-blind → negation-aware →
// proximity-aware in lockstep, and each transition happened because the
// OTHER check's gap was found first: the false positive that forced
// negation-awareness was found in checkStatusStateValues (a correct
// reference warning quoting `"status": "registered"` on purpose,
// flagged as a claim), while the false negative that forced proximity
// bounding was found in checkNoEnvelopeClaims (an unrelated "no"
// earlier in the sentence excusing a real task_id claim later in it) —
// and in both cases the other check had the identical gap, unnoticed.
// So a change here that is not mirrored there just moves which
// detector is broken. In particular: do not "simplify" the marker back
// into automatic detection in one check only, and do not re-derive
// polarity from the prose in either — that is the three-round cycle
// documented above, and it ended by making the author state it.
func checkNoEnvelopeClaims(path string, sec docSection) []string {
	// A cp- or managed-scoped section is out of scope because it is
	// ABOUT a product whose tasks are real. controlplane's Task type genuinely
	// has a task_id JSON field (internal/controlplane/api/client.gen.go:
	// `TaskId string \`json:"task_id"\``), and managed's create,
	// resize and delete each genuinely spawn a task served from
	// `/managed/v1/tasks`. Both legitimately show a bare <task_id>
	// CLI-argument placeholder in their usage examples. This check
	// only applies where the claim is actually false — which is BYOC,
	// where a mutation's response IS the resource.
	//
	// That scoping is per SENTENCE, not per section: a blanket skip of
	// a cp section would make skills/pgedge-controlplane/SKILL.md a whole-FILE
	// exemption, since moduleSections maps all of it to one cp section.
	// A sentence that names a BYOC command is making a BYOC claim
	// wherever it is written, so it is checked like any other.
	// See TestNoEnvelopeCheckCatchesByocClaimInCPScopedFile.
	taskNative := sec.module == "controlplane" || sec.module == "managed"

	var violations []string
	for _, sentence := range annotateSentences(sec.module,
		normalizeWhitespace(sec.text)) {
		if taskNative && !sentenceMentionsByocCommand(sentence.text) {
			continue
		}
		lower := strings.ToLower(sentence.text)
		for _, sentinel := range noEnvelopeSentinels {
			if !strings.Contains(lower, sentinel) {
				continue
			}
			if sentence.spend(sentinel, ownsSentinelNames) {
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s: sentence names %q with no doc-gate marker excusing "+
					"it — BYOC never wraps a mutation's result in a task; "+
					"the create/deploy response is the resource itself. "+
					"If this sentence names the field in order to warn "+
					"against it, annotate it: `<!-- doc-gate: "+
					"deliberately-wrong %s — <reason> -->`. Sentence: %q",
				path, sentinel, sentinel,
				strings.TrimSpace(sentence.text)))
		}
		violations = append(violations,
			unusedMarkerViolations(path, &sentence, ownsSentinelNames)...)
	}
	return violations
}

// unusedMarkerViolations reports every allowance in sentence that own
// claims and nothing spent. A marker that excuses nothing is not
// harmless: it is either stale (the wrong value it named has since been
// edited out), misplaced (attached to the wrong sentence), or redundant
// (naming a value that was never going to be flagged). All three make
// the next reader believe an exception is doing work that it is not,
// which is how a per-occurrence opt-out decays into a per-file one.
func unusedMarkerViolations(path string, sentence *annotatedSentence,
	own func(string) bool) []string {
	var violations []string
	for _, name := range sentence.unspent(own) {
		violations = append(violations, fmt.Sprintf(
			"%s: doc-gate marker excuses %q but nothing in the sentence it "+
				"annotates was flagged for that phrase. Either the phrase "+
				"is written in the wrong form — it must be the bare "+
				"sentinel or value (`registered`), never the layout it "+
				"appears in (`\"status\": \"registered\"`) — or the marker "+
				"is stale, misplaced (it must sit inside that sentence, or "+
				"immediately after its closing punctuation) or redundant "+
				"(the phrase it names was never going to be flagged). "+
				"Sentence: %q", path, name,
			strings.TrimSpace(sentence.text)))
	}
	return violations
}

// --- 2. the status/state value allowlist ------------------------------------

// healthCheckValues covers the doctor family uniformly across every
// module. Verified from internal/cli/doctor.go,
// internal/starfleet/account/cmd/doctor.go, and internal/controlplane/cmd/doctor.go:
// all three build a STATUS column from exactly these three literals,
// regardless of which connection or module they are reporting on, so
// this set is merged into both modules' status-field sets below
// rather than living in only one. It stays out of the state-field
// sets: no doctor output spells its column STATE.
var healthCheckValues = map[string]bool{
	"ok": true, "warning": true, "error": true,
}

// byocStatusFieldValues covers resource `.status`
// (cluster/database/backup/backup-store/ingress) and task `.status` on
// the pgEdge Starfleet API — the byoc, managed and starfleet scopes
// all resolve here, exactly as account's did before the account fold
// was deleted. Service `.state` lives in
// byocStateFieldValues instead: the two vocabularies overlap without
// matching, and a membership check against their union cannot catch a
// value quoted for the WRONG field — the measured escape was
// `state` reading `degraded`, which passed every gate.
// This is one flat set per FIELD, not one per resource, so it cannot
// record "this value is confirmed for resource X but not resource Y"
// as a map entry — the two comment groups below record the
// finer-grained provenance in prose instead, per the standing rule
// that every entry's confidence must stay legible rather than silently
// assumed.
//
// Verified against a live tenant, each against at least one
// real resource (not necessarily every resource that reports the
// value):
//   - "available": `byoc cluster list -o json` (cluster) returned it.
//     UNVERIFIED FOR INGRESS SPECIFICALLY: `byoc ingress list -o json`
//     on a live tenant returned only "failed" and "deleting" (every
//     real ingress there is unhealthy right now), and
//     `openapi/byoc.yaml`'s `Ingress.status` is a bare `type: string`
//     with no enum to fall back on. Kept in this shared allowlist by
//     analogy with every other resource's lifecycle (cluster,
//     database, backup-store all reach "available"), not by direct
//     observation — an ingress doc example using it is accepted on
//     that analogy, not on live proof. Revisit if a healthy ingress
//     is ever observed, or if this turns out wrong.
//   - "failed": cluster, database, AND ingress all returned it live.
//   - "deleting": database and ingress both returned it live.
//   - "succeeded": a live `byoc task list --subject-id` response
//     returned it (task .status), matching
//     internal/starfleet/byoc/cmd/task.go's terminal-state switch.
//
// NOT independently observed live — carried over from
// skills/pgedge-byoc/SKILL.md's own already-verified vocabulary table
// (a read-only session has no live resource sitting in any of these
// transient states to observe):
//   - "queued", "creating", "modifying", "degraded" (resource .status)
//   - "queued" and "running" (task .status) also have code and
//     contract backing even without a live observation:
//     internal/starfleet/byoc/cmd/task.go's --status flag enumerates
//     (queued, running, succeeded, failed), and openapi/byoc.yaml's
//     task-list status parameter declares the same four as its enum.
var byocStatusFieldValues = map[string]bool{
	"available": true, "queued": true, "creating": true,
	"modifying": true, "deleting": true, "failed": true,
	"degraded": true, "running": true, "succeeded": true,
}

// byocStateFieldValues covers service `.state`, the only `state`
// field these docs quote in a form the detectors see. "running" was
// returned live by a database's embedded services on a live tenant;
// "pending" and "failed" are contract-backed rather than
// live-observed: openapi/managed.yaml's service `state` description
// and openapi/byoc.yaml's ServiceConfig.state description each name
// all three (no transient or dead service was available to observe
// read-only).
var byocStateFieldValues = map[string]bool{
	"running": true, "pending": true, "failed": true,
}

// managedStatusFieldValues: managed shares byoc's resource and task
// `.status` vocabulary (same API code paths; "degraded" observed live
// 2026-09-10 in the allowlist apply path's contract).
var managedStatusFieldValues = byocStatusFieldValues

// managedStateFieldValues covers two `state` fields the managed docs
// quote: a service's runtime state (running/pending/failed, as byoc)
// and an allowlist's posture. The allowlist values are contract-backed
// (openapi/managed.yaml IPAllowlist.state enum) and "open" was
// observed live 2026-09-10 on a pre-feature database.
var managedStateFieldValues = map[string]bool{
	"running": true, "pending": true, "failed": true,
	"closed": true, "restricted": true, "open": true,
}

// controlplaneStateFieldValues covers the Control Plane's `state`
// fields: the union of InstanceState, DatabaseSummaryState (its
// per-endpoint Database*State twins declare subsets of the same
// spellings), and ServiceInstanceState — verified directly from the
// generated enum constants in
// internal/controlplane/api/client.gen.go, which are codegen'd from
// the CP openapi spec (the contract, not a live observation).
// Deliberately NOT included, though they are real CP `state` enums in
// the same file: ClusterStatusState ("error") and HostStatusState
// ("healthy", "unreachable"), which no scanned doc quotes today — add
// them here with this same provenance when one needs to.
var controlplaneStateFieldValues = map[string]bool{
	"available": true, "backing_up": true, "creating": true,
	"degraded": true, "deleting": true, "failed": true,
	"modifying": true, "restoring": true, "stopped": true,
	"unknown": true, "running": true,
}

// controlplaneStatusFieldValues covers the Control Plane's TaskStatus
// enum, its one `status` field with a vocabulary the docs quote —
// verified from the same generated constants. Deliberately NOT
// included: HealthCheckResultStatus ("healthy", "unhealthy",
// "unknown"), which no scanned doc quotes today. Note "completed" is real
// here and "succeeded" is not, and "canceling"/"canceled"/"unknown"
// have no BYOC equivalent at all — the mirror image of byoc's list,
// which is exactly why this is a separate map rather than byoc's plus
// a few extras.
var controlplaneStatusFieldValues = map[string]bool{
	"canceled": true, "canceling": true, "completed": true,
	"failed": true, "pending": true, "running": true,
	"unknown": true,
}

// fieldVocabulary holds one allow-set per field name. The split is the
// point: `status` and `state` overlap without matching, so a check
// against their union accepted a value quoted for the wrong field.
type fieldVocabulary struct {
	status map[string]bool
	state  map[string]bool
}

// forField returns the allow-set for a status/state label. The prose
// and vertical detectors lowercase before calling; the horizontal one
// passes exactly STATUS or STATE. Every detector matches only those
// two words, so status is the only other possibility.
func (v fieldVocabulary) forField(label string) map[string]bool {
	if strings.EqualFold(label, "state") {
		return v.state
	}
	return v.status
}

func allowedStatusValues(module string) fieldVocabulary {
	status, state := byocStatusFieldValues, byocStateFieldValues
	if module == "managed" {
		status, state = managedStatusFieldValues, managedStateFieldValues
	}
	if module == "controlplane" {
		status, state = controlplaneStatusFieldValues,
			controlplaneStateFieldValues
	}
	merged := make(map[string]bool, len(status)+len(healthCheckValues))
	for v := range status {
		merged[v] = true
	}
	for v := range healthCheckValues {
		merged[v] = true
	}
	return fieldVocabulary{status: merged, state: state}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsonStatusRe matches a "status"/"state" JSON key and its string
// value, case-insensitively and with \s* between every token — \s
// matches a newline as well as ordinary spaces, so this survives both
// a compact `"status":"available"` and one reflowed across a markdown
// wrap point without any separate normalisation step.
var jsonStatusRe = regexp.MustCompile(`(?i)"(status|state)"\s*:\s*"([a-zA-Z0-9_-]+)"`)

// inlineStatusRe matches a bare status/state label immediately
// followed by a quoted value with no colon — the shape prose uses
// when narrating an expected result inline rather than showing a
// JSON snippet or a table, e.g. `→ state "active"; Instances list
// ...` (found by hand in skills/pgedge-controlplane/SKILL.md, in neither the
// JSON nor the table/vertical-text layout the other two detectors
// cover). Requiring the bare word "status"/"state" right before the
// quote keeps this from matching an unrelated quoted value elsewhere
// in the same sentence.
var inlineStatusRe = regexp.MustCompile(`(?i)\b(status|state)\s+"([a-zA-Z0-9_-]+)"`)

// keyValStatusRe matches the "state=running"/"status=failed"
// key=value shorthand skills/pgedge-byoc/SKILL.md's workflow steps
// use for an expected result (e.g. "Expected: service with
// state=registered", found by hand — the fourth layout alongside
// JSON, inline-quoted, and table/vertical text this doc's examples
// turned out to use).
var keyValStatusRe = regexp.MustCompile(`(?i)\b(status|state)=([a-zA-Z0-9_-]+)`)

// fieldSepRe splits a line of this doc's example output on column
// boundaries. Every table and FIELD/VALUE block in these docs pads
// columns with two or more spaces (a single space is reserved for
// words within one column's value, e.g. "us-east-1,eu-west-1" or
// "prod-cluster"), so splitting on runs of 2+ spaces recovers the
// columns without needing to know each table's fixed width.
var fieldSepRe = regexp.MustCompile(`[ \t]{2,}`)

func firstToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// checkValueAllowed appends a violation message to *violations when
// value is non-empty and not in allowed, ending with remedy — the
// caller's statement of what the author should actually do.
//
// The remedy is a parameter rather than fixed text because it genuinely
// differs by layout, and getting that wrong would be worse than saying
// nothing: the prose detectors can be answered with a marker, the two
// table detectors cannot (see the note on checkStatusStateValues — a
// table row is always read as a literal example value, so it has no
// opt-out by design). Telling a table author to write a marker would
// send them after an exemption that does not exist.
func checkValueAllowed(violations *[]string, path, module, field, label,
	value string, vocab fieldVocabulary, remedy string) {
	// The set is derived HERE from the same field string the message
	// prints, so the two cannot disagree — a call site passing a
	// pre-picked set beside a field name would be two hand-written
	// expressions that must agree.
	allowed := vocab.forField(field)
	value = strings.ToLower(value)
	if value == "" || allowed[value] {
		return
	}
	*violations = append(*violations, fmt.Sprintf(
		"%s: example shows %s %q, which is not in the verified %s `%s` "+
			"vocabulary %v. %s", path, label, value, module,
		strings.ToLower(field), sortedKeys(allowed), remedy))
}

// proseValueRemedy names the exact marker an author must write to keep a
// wrong value they are deliberately quoting. checkNoEnvelopeClaims names
// its marker in its own message the same way; a gate that reports a
// violation without naming its remedy makes the author guess, and the
// most natural guess here (writing the whole `"status": "registered"`
// snippet as the phrase) is wrong.
func proseValueRemedy(value string) string {
	return fmt.Sprintf("If this sentence names the value in order to warn "+
		"against it, annotate that occurrence: `<!-- doc-gate: "+
		"deliberately-wrong %s — <reason> -->`. The phrase must be the "+
		"bare value, not the layout it appears in.", value)
}

// tableValueRemedy is the other half: there is no marker for a table
// row, so it says so rather than leaving the author to look for one.
const tableValueRemedy = "A table or FIELD/VALUE row has no opt-out — it " +
	"is always read as a literal example value — so correct the value, or " +
	"add it to the module's allowlist with its provenance if it is real."

// checkStatusStateValues returns one violation message per
// status/state value found in sec (across the JSON, inline-quoted,
// key=value, vertical-text, and horizontal-table layouts) that is not
// in sec.module's verified allowlist. It returns messages rather than
// taking a *testing.T for the same reason checkNoEnvelopeClaims does.
//
// The three prose-style detectors below (JSON, inline-quoted,
// key=value) honour the SAME doc-gate marker checkNoEnvelopeClaims
// honours, through the same annotateSentences helper, not a copy of it.
// A marker excuses the one VALUE it names, so a sentence quoting two
// wrong values needs two markers.
//
// BOTH CHECKS IN THIS FILE MUST HONOUR EXACTLY THE SAME OPT-OUT, AND
// NEITHER MAY GROW A POLARITY HEURISTIC OF ITS OWN — this is the
// sibling note to the one on checkNoEnvelopeClaims above, and it must
// keep saying the same thing for the same reason: this pairing has
// already been broken twice, in both directions. First, having no
// polarity awareness at all produced a false positive here —
// `... so presence in the list plus a populated
// \`url\` is the signal, not a \`"status": "registered"\` value` in
// the byoc reference, a correct warning that quotes a wrong value on
// purpose, was flagged as if it were a claim; the first "fix" was to
// flatten that concrete example into vaguer prose to dodge the gate,
// which is backwards. Then, a whole-sentence negation check (rather
// than one bounded to the phrase it governs) produced a false negative
// in the opposite direction over in checkNoEnvelopeClaims — an
// unrelated negation word anywhere in the sentence excused a real
// falsehood elsewhere in it. Each time, the OTHER check had the
// identical gap and nobody had noticed, so fixing one and not the other
// just moves which detector is broken. See
// TestStatusValueCheckHonoursDeliberatelyWrongMarker and
// TestStatusValueCheckRejectsUnannotatedClaim, and their
// checkNoEnvelopeClaims twins.
//
// The two table-style detectors below (vertical Field/Value,
// horizontal table column) deliberately do NOT get this treatment: a
// table row in this doc's own style is always read as a literal
// example value, never as a warning, so there is nothing to exempt
// there. They therefore scan the section text with markers stripped
// out, so a stray marker line can never be mistaken for a table cell.
func checkStatusStateValues(path string, sec docSection) []string {
	var violations []string
	vocab := allowedStatusValues(sec.module)

	for _, sentence := range annotateSentences(sec.module,
		normalizeWhitespace(sec.text)) {
		for _, re := range []*regexp.Regexp{jsonStatusRe, inlineStatusRe, keyValStatusRe} {
			for _, loc := range re.FindAllStringSubmatchIndex(sentence.text, -1) {
				label := strings.ToLower(sentence.text[loc[2]:loc[3]])
				value := strings.ToLower(sentence.text[loc[4]:loc[5]])
				allowed := vocab.forField(label)

				// An allowance is spent only on a value that would
				// otherwise be reported, so that a marker naming a value
				// this module allows anyway is still caught as
				// excusing-nothing by unusedMarkerViolations.
				if value == "" || allowed[value] {
					continue
				}
				if sentence.spend(value, ownsValueNames) {
					continue
				}

				switch re {
				case jsonStatusRe:
					checkValueAllowed(&violations, path, sec.module,
						label, `"`+label+`"`, value, vocab,
						proseValueRemedy(value))
				case inlineStatusRe:
					checkValueAllowed(&violations, path, sec.module,
						label, label+` "…"`, value, vocab,
						proseValueRemedy(value))
				case keyValStatusRe:
					checkValueAllowed(&violations, path, sec.module,
						label, label+`=…`, value, vocab,
						proseValueRemedy(value))
				}
			}
		}
		violations = append(violations,
			unusedMarkerViolations(path, &sentence, ownsValueNames)...)
	}

	type headerCol struct {
		idx   int
		label string
	}
	var headerCols []headerCol
	rawLines := strings.Split(sec.text, "\n")
	for i, line := range strings.Split(stripDocGateMarkers(sec.text), "\n") {
		trimmed := strings.TrimRight(line, " \t")
		leading := strings.TrimSpace(trimmed)

		// A line whose only content was marker text is neither a table
		// row nor a paragraph break. Skip it WITHOUT clearing
		// headerCols, or a marker between a table's header and its
		// rows silently switches this detector off for the rest of
		// that table.
		if trimmed == "" && i < len(rawLines) &&
			strings.TrimSpace(rawLines[i]) != "" {
			continue
		}

		if trimmed == "" || strings.HasPrefix(leading, "```") {
			headerCols = nil
			continue
		}
		fields := fieldSepRe.Split(leading, -1)

		// Vertical "Field  Value" row: exactly two columns, the first
		// a bare "Status" or "State" label.
		if len(fields) == 2 {
			label := strings.ToLower(fields[0])
			if label == "status" || label == "state" {
				checkValueAllowed(&violations, path, sec.module, label,
					fields[0], firstToken(fields[1]), vocab,
					tableValueRemedy)
			}
		}

		// Horizontal table: is this line itself the header row? A row
		// can name BOTH a STATUS and a STATE column, so every match is
		// recorded and checked, not only the first.
		// The match stays exact-uppercase on purpose: `Status` is the
		// vertical layout's label style, and a case-folded header
		// match would read a FIELD/VALUE block's Status row as a
		// header and then check the NEXT row's first cell as a value.
		var cols []headerCol
		for i, f := range fields {
			if f == "STATUS" || f == "STATE" {
				cols = append(cols, headerCol{idx: i, label: f})
			}
		}
		switch {
		case len(cols) > 0:
			headerCols = cols
		case len(headerCols) > 0:
			for _, col := range headerCols {
				if len(fields) <= col.idx {
					continue
				}
				checkValueAllowed(&violations, path, sec.module,
					col.label, col.label, firstToken(fields[col.idx]),
					vocab, tableValueRemedy)
			}
		}
	}
	return violations
}

func TestSkillDocsMatchVerifiedAPIBehaviour(t *testing.T) {
	files := scanDocFiles(t)

	scanned := make(map[string]bool, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanned[path] = true

		for _, sec := range moduleSections(path, string(content)) {
			for _, msg := range checkDocGateMarkers(path, sec) {
				t.Error(msg)
			}
			for _, msg := range checkNoEnvelopeClaims(path, sec) {
				t.Error(msg)
			}
			for _, msg := range checkStatusStateValues(path, sec) {
				t.Error(msg)
			}
		}
	}

	assertAllNamedDocsScanned(t, scanned)
}

// ---------------------------------------------------------------------------
// Regression tests
// ---------------------------------------------------------------------------

// TestNormalizeWhitespaceCatchesWrappedCommand is a regression case
// for the exact failure found by hand in skills/pgedge-byoc/SKILL.md:
//
//   - If auth is broken and the cause is unclear, run `pgedge starfleet byoc
//     doctor` for diagnostics.
//
// A raw strings.Contains(content, "pgedge starfleet byoc doctor") check passes
// on that text because the command name is split across two lines by
// the project's 79-column markdown wrap rule — which is the normal
// case for a wrapped command, not an edge case. If normalizeWhitespace
// is ever "simplified" back to an identity function, or the guard
// above stops calling it, this test fails independently of whether
// any real doc happens to be clean at the time.
func TestNormalizeWhitespaceCatchesWrappedCommand(t *testing.T) {
	wrapped := "- If auth is broken, run `pgedge starfleet byoc\n  doctor` for diagnostics."

	if strings.Contains(wrapped, "pgedge starfleet byoc doctor") {
		t.Fatal("test fixture is not actually wrapped across the " +
			"phrase it is meant to test — fix the fixture")
	}

	if !strings.Contains(normalizeWhitespace(wrapped), "pgedge starfleet byoc doctor") {
		t.Fatal("normalizeWhitespace did not collapse the wrapped " +
			"command back into one contiguous phrase")
	}
}

// TestNoEnvelopeCheckCatchesRewordedCaptureInstructions is the
// acceptance test for the structural check: three rephrasings of
// "capture task_id" that defeated the phrase-blocklist approach must
// all fail, run standalone against a byoc-scoped fixture (not the
// real docs — those are meant to stay clean, so this proves the
// mechanism, not today's content).
func TestNoEnvelopeCheckCatchesRewordedCaptureInstructions(t *testing.T) {
	rewordings := []string{
		"Capture the returned `task_id` and poll it.",
		"Note the `task_id` in the response.",
		"Record `task_id` for the wait step.",
	}

	for _, wording := range rewordings {
		t.Run(wording, func(t *testing.T) {
			got := checkNoEnvelopeClaims("fixture.md",
				docSection{module: "byoc", text: wording})
			if len(got) == 0 {
				t.Fatalf("checkNoEnvelopeClaims did not flag %q — the "+
					"structural check must catch a rewording it has "+
					"never seen, not just the exact phrases already "+
					"fixed", wording)
			}
		})
	}
}

// TestNoEnvelopeCheckHonoursDeliberatelyWrongMarker proves negated
// sentences FIRE unannotated and PASS annotated — the author's remedy
// is a one-line marker rather than a window tweak, and there is
// nothing left for the gate to guess.
//
// The last two cases are the hedged phrasings that broke round 3's
// 20-character proximity window (measured negation distances 43 and 72
// characters), in their task_id form. They are the same two shapes
// TestStatusValueCheckHonoursDeliberatelyWrongMarker uses verbatim for
// the other detector.
func TestNoEnvelopeCheckHonoursDeliberatelyWrongMarker(t *testing.T) {
	cases := []struct {
		name      string
		sentence  string
		annotated string
	}{
		{
			name:     "plain absence",
			sentence: "There is no `task_id` in any response.",
			annotated: "There is no `task_id` in any response. " +
				"<!-- doc-gate: deliberately-wrong task_id — " +
				"this sentence documents the field's absence -->",
		},
		{
			name:     "imperative absence",
			sentence: "Do not try to read `task_id`; it does not exist.",
			annotated: "Do not try to read `task_id` " +
				"<!-- doc-gate: deliberately-wrong task_id — names the " +
				"field in order to forbid reading it -->; it does not exist.",
		},
		{
			name: "interjected absence",
			sentence: "The response is the updated database object, not a " +
				"task — there is no `task_id` to capture.",
			annotated: "The response is the updated database object, not a " +
				"task — there is no `task_id` to capture. " +
				"<!-- doc-gate: deliberately-wrong task_id — " +
				"documents the field's absence -->",
		},
		{
			name: "hedged with interjected clauses",
			sentence: "Do not, under any circumstances, ever expect the " +
				"response to include a `task_id` here.",
			annotated: "Do not, under any circumstances, ever expect the " +
				"response to include a `task_id` here. " +
				"<!-- doc-gate: deliberately-wrong task_id — hedged " +
				"warning against a field that does not exist -->",
		},
		{
			name: "hedged with a long parenthetical",
			sentence: "Note that a cluster response will never, under " +
				"normal operation or during any documented failure mode, " +
				"carry a `task_id`.",
			annotated: "Note that a cluster response will never, under " +
				"normal operation or during any documented failure mode, " +
				"carry a `task_id`. <!-- doc-gate: deliberately-wrong " +
				"task_id — hedged warning against a field that does not " +
				"exist -->",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkNoEnvelopeClaims("fixture.md",
				docSection{module: "byoc", text: tc.sentence}); len(got) == 0 {
				t.Fatalf("unannotated sentence %q was not flagged — under "+
					"an explicit opt-out every mention of a nonexistent "+
					"field must fire until its author annotates it",
					tc.sentence)
			}
			if got := checkNoEnvelopeClaims("fixture.md",
				docSection{module: "byoc", text: tc.annotated}); len(got) != 0 {
				t.Fatalf("annotated sentence %q was still flagged "+
					"(violations: %v) — a doc-gate marker naming the "+
					"phrase must excuse it", tc.annotated, got)
			}
		})
	}
}

// TestNoEnvelopeCheckRejectsUnannotatedClaim keeps the fixture that
// exposed round 2's false-negative hole: the "no" governs "doubt", not
// the task_id claim 34 characters later, so a whole-sentence negation
// check waved the claim through. Under an explicit opt-out it is
// simply an unannotated false claim and must fire. Keeping the case
// costs nothing and catches any future attempt to re-derive polarity
// from the prose — exactly the three-round cycle this design ended.
func TestNoEnvelopeCheckRejectsUnannotatedClaim(t *testing.T) {
	stray := "There is no doubt you should capture the `task_id` " +
		"from the create response."
	control := "You should capture the `task_id` from the create response."

	if got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: control}); len(got) == 0 {
		t.Fatal("control sentence (no stray negation) was not flagged " +
			"— fix the fixture, not the assertion, before trusting " +
			"the stray-negation case below")
	}

	if got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: stray}); len(got) == 0 {
		t.Fatal("checkNoEnvelopeClaims let a real task_id claim " +
			"through — a \"no\" earlier in the sentence is not an " +
			"opt-out; only a doc-gate marker naming the phrase is")
	}
}

// TestNoEnvelopeCheckIsScopedToByoc proves the controlplane module's genuinely
// real task_id field is never flagged, and that scoping is by module
// (which API the text documents), not by exempting a file.
func TestNoEnvelopeCheckIsScopedToByoc(t *testing.T) {
	got := checkNoEnvelopeClaims("fixture.md", docSection{
		module: "controlplane",
		text:   "Capture the returned `task_id` and poll it.",
	})
	if len(got) != 0 {
		t.Fatalf("checkNoEnvelopeClaims flagged a controlplane-scoped section "+
			"(violations: %v) — controlplane's Task type genuinely has a "+
			"task_id JSON field", got)
	}
}

// TestNoEnvelopeCheckFlagsCPCommandMentionWithoutMarker pins the
// closure of the deleted cp-command amnesty (see the note on
// byocCommandMentionRe): a genuinely false BYOC claim must not pass
// merely by sharing a sentence with a controlplane command name. The first
// fixture is the exact probe recorded against that hole, which
// returned 0 violations for as long as the amnesty existed and must
// now return 1. The remedy for the one real site the amnesty was
// carrying is the correct-for verb, not a skip.
func TestNoEnvelopeCheckFlagsCPCommandMentionWithoutMarker(t *testing.T) {
	probe := "Capture the `task_id` after `controlplane task cancel <id>` runs."
	got := checkNoEnvelopeClaims("planning.md",
		docSection{module: "byoc", text: probe})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation for the recorded amnesty "+
			"probe, got %d: %v — a false BYOC claim must not be waved "+
			"through just because its sentence names a controlplane command", len(got), got)
	}

	// The honest remedy passes, and it is the shape a planning
	// document with mixed-module rows needs.
	annotated := "Surface it as `controlplane task cancel <task_id>` (gated " +
		"confirm). <!-- doc-gate: correct-for controlplane task_id — controlplane's Task " +
		"type carries a real task_id field -->"
	if got := checkNoEnvelopeClaims("planning.md",
		docSection{module: "byoc", text: annotated}); len(got) != 0 {
		t.Fatalf("the correct-for remedy did not clear the claim it "+
			"names (violations: %v)", got)
	}
}

// TestCorrectForMarkerExcusesOnlyWhatItNames is the correct-for twin of
// TestDocGateMarkerExcusesOnlyWhatItNames. The property that justifies
// this opt-out existing at all is that a marker clears the one phrase it
// names and nothing else, and a second verb that did not have it would
// reintroduce the blanket amnesty in a new spelling.
func TestCorrectForMarkerExcusesOnlyWhatItNames(t *testing.T) {
	fixture := "Read the `task_id` from `controlplane task get` and then read the " +
		"task output. <!-- doc-gate: correct-for controlplane task_id — controlplane's Task " +
		"type carries a real task_id field -->"

	got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: fixture})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation (the unnamed \"task output\"), "+
			"got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "task output") {
		t.Errorf("the surviving violation should be the unnamed sentinel "+
			"\"task output\", got %q", got[0])
	}
}

// TestCorrectForMarkerDoesNotExcuseGenuinelyWrongByocClaim is the
// property the marker-vocabulary decision turned on. correct-for makes a
// TRUE statement about the phrase it names — "right, but under another
// module's scope" — so it must not become a way to launder a claim that
// is simply false. Two directions, both of which a blanket amnesty got
// wrong:
//
//   - a marker reaches only its own sentence, so a real BYOC claim in a
//     neighbouring sentence still fires;
//   - the allowance is COUNTED, so where a detector reports one
//     violation per occurrence, one marker clears one occurrence and the
//     next still fires.
//
// The second case uses the status detector rather than the sentinel
// one, because the two count differently and the difference is easy to
// assert wrongly. checkNoEnvelopeClaims reports at most one violation
// per sentinel per sentence — `strings.Contains` is presence, not a
// match count — so a sentence naming task_id twice is one violation and
// one marker is the correct remedy. checkStatusStateValues reports per
// regexp match, so it is the detector where N occurrences genuinely
// need N markers (the property TestDocGateMarkerCoversRepeatedValue
// Occurrences pins for the other verb).
func TestCorrectForMarkerDoesNotExcuseGenuinelyWrongByocClaim(t *testing.T) {
	t.Run("neighbouring sentence", func(t *testing.T) {
		fixture := "Surface it as `controlplane task cancel <task_id>`. " +
			"<!-- doc-gate: correct-for controlplane task_id — controlplane's Task type " +
			"carries a real task_id field --> Capture the returned " +
			"`task_id` from the byoc deploy response and poll it."

		got := checkNoEnvelopeClaims("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 {
			t.Fatalf("want exactly 1 violation (the following sentence's "+
				"real BYOC claim), got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0], "Capture the returned") {
			t.Errorf("the surviving violation should be the BYOC claim, "+
				"got %q", got[0])
		}
	})

	t.Run("second occurrence needs a second marker", func(t *testing.T) {
		// "active" is in controlplane's spec enum and not in byocStatusValues, so
		// it is exactly the shape correct-for exists for. Two matches,
		// one marker: the second must still fire.
		fixture := "A cp size reports `\"status\": \"active\"` and " +
			"`\"state\": \"active\"`. <!-- doc-gate: correct-for controlplane " +
			"active — controlplane's Size.status enum includes active -->"

		got := checkStatusStateValues("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 {
			t.Fatalf("want exactly 1 violation (the second, unannotated "+
				"occurrence), got %d: %v — one marker must clear exactly "+
				"one occurrence", len(got), got)
		}

		// And two markers clear both, so the remedy is available.
		both := fixture + " <!-- doc-gate: correct-for controlplane active — " +
			"the second occurrence -->"
		if got := checkStatusStateValues("fixture.md",
			docSection{module: "byoc", text: both}); len(got) != 0 {
			t.Fatalf("two markers did not clear two occurrences: %v", got)
		}
	})
}

// TestCorrectForMarkerRejectsOwnAndUnknownScope pins the structural
// constraint that keeps correct-for from decaying into "do not check
// this sentence". The verb may only assert that a phrase is correct
// under ANOTHER module's scope, so naming the section's own scope
// asserts nothing, and naming a scope this gate does not have asserts
// nothing either. In both cases the marker must be reported AND must
// fail to excuse the claim beside it — a rejected marker that silently
// still spent its allowance would be the hole, not the fix.
//
// "account" is the case worth spelling out: it used to be a real
// module of this CLI, folded into "byoc" scope, and is now gone
// entirely — its docs live in "starfleet" scope instead. So it is simply
// an unknown scope like any other typo, no different in kind from
// "unknown scope" below.
func TestCorrectForMarkerRejectsOwnAndUnknownScope(t *testing.T) {
	claim := "Capture the returned `task_id` and poll it."

	cases := map[string]struct {
		module string
		marker string
	}{
		"own scope, byoc section": {
			module: "byoc",
			marker: "<!-- doc-gate: correct-for byoc task_id — " +
				"claiming the section's own scope -->",
		},
		"own scope, cp section": {
			module: "controlplane",
			marker: "<!-- doc-gate: correct-for controlplane task_id — " +
				"claiming the section's own scope -->",
		},
		"account is not a gate scope": {
			module: "byoc",
			marker: "<!-- doc-gate: correct-for account task_id — " +
				"account is not a module of this CLI any more -->",
		},
		"unknown scope": {
			module: "byoc",
			marker: "<!-- doc-gate: correct-for managed task_id — " +
				"no such gate scope -->",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sec := docSection{module: tc.module,
				text: claim + " " + tc.marker}

			markerViolations := checkDocGateMarkers("fixture.md", sec)
			if len(markerViolations) != 1 {
				t.Fatalf("want exactly 1 marker violation, got %d: %v",
					len(markerViolations), markerViolations)
			}
			if !strings.Contains(markerViolations[0], "correct-for") {
				t.Errorf("the violation should name the verb it rejects, "+
					"got %q", markerViolations[0])
			}

			// The claim beside the rejected marker must still fire. A
			// "controlplane" section reaches the sentinel check only via
			// sentenceMentionsByocCommand, so that case is checked with
			// a sentence that names byoc.
			text := claim
			if tc.module == "controlplane" {
				text = "The byoc deploy response: " + claim
			}
			claimViolations := checkNoEnvelopeClaims("fixture.md",
				docSection{module: tc.module, text: text + " " + tc.marker})
			if len(claimViolations) == 0 {
				t.Fatal("a rejected correct-for marker still excused the " +
					"claim beside it — rejection must not create an " +
					"allowance")
			}
		})
	}
}

// TestCorrectForMarkerRequiresModulePhraseAndReason is the correct-for
// twin of TestDocGateMarkerRequiresPhraseAndReason, plus the operand
// arity this verb adds: correct-for takes a module AND a phrase, so a
// marker carrying only one bare word is not a correct-for marker with a
// defaulted module — it is malformed. Getting that wrong in the lenient
// direction would make `correct-for task_id` parse as scope "task_id"
// with no phrase, or worse, as a phrase with an empty scope that then
// compares unequal to every section module and is honoured everywhere.
func TestCorrectForMarkerRequiresModulePhraseAndReason(t *testing.T) {
	claim := "Capture the returned `task_id` and poll it."

	cases := map[string]string{
		"module but no phrase": claim +
			" <!-- doc-gate: correct-for controlplane — cp has tasks -->",
		"phrase but no module": claim +
			" <!-- doc-gate: correct-for task_id — no module named -->",
		"no reason": claim +
			" <!-- doc-gate: correct-for controlplane task_id -->",
		"empty reason": claim +
			" <!-- doc-gate: correct-for controlplane task_id — -->",
		"misspelled verb": claim +
			" <!-- doc-gate: correct_for cp task_id — underscore, not " +
			"a hyphen -->",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			sec := docSection{module: "byoc", text: fixture}

			if got := checkDocGateMarkers("fixture.md", sec); len(got) == 0 {
				t.Error("checkDocGateMarkers accepted an unusable marker — " +
					"a marker nobody will honour must be reported, not " +
					"ignored")
			}
			if got := checkNoEnvelopeClaims("fixture.md", sec); len(got) == 0 {
				t.Error("the claim beside an unusable marker was excused " +
					"anyway — an unusable marker must not create an " +
					"allowance")
			}
		})
	}
}

// TestCorrectForMarkerMustExcuseSomething is the correct-for twin of
// TestDocGateMarkerMustExcuseSomething. A well-formed marker naming a
// phrase that was never flagged is stale, misplaced or redundant, and
// all three teach the next reader that an exception is doing work it is
// not.
func TestCorrectForMarkerMustExcuseSomething(t *testing.T) {
	fixture := "The `controlplane task list` verb enumerates tasks. " +
		"<!-- doc-gate: correct-for controlplane task_id — nothing here names " +
		"task_id -->"

	got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: fixture})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation (the marker that excused "+
			"nothing), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "excuses") {
		t.Errorf("the violation should be the excuses-nothing report, "+
			"got %q", got[0])
	}
}

// TestCorrectForMarkerIsStrippedFromScannedText proves the marker's own
// text cannot trip the detectors it exempts. A correct-for marker names
// its phrase bare — "task_id" — inside a sentence whose whole purpose is
// that task_id must not be flagged there, so a stripping bug would make
// the marker self-defeating: it would clear one occurrence and introduce
// another. The deliberately-wrong verb has the same property through the
// same code path; this pins it for the new verb, and for the
// line-oriented stripDocGateMarkers pass that checkStatusStateValues
// runs.
func TestCorrectForMarkerIsStrippedFromScannedText(t *testing.T) {
	marker := "<!-- doc-gate: correct-for controlplane task_id — controlplane's Task type " +
		"carries a real task_id field -->"

	if got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: "Nothing to see here. " + marker}); len(got) != 1 {
		// One violation, and it must be "excused nothing" — NOT a
		// task_id claim sourced from the marker's own words.
		t.Fatalf("want exactly 1 violation, got %d: %v", len(got), got)
	} else if !strings.Contains(got[0], "excuses") {
		t.Errorf("the marker's own text was read as a claim: %q", got[0])
	}

	if stripped := stripDocGateMarkers(marker); strings.Contains(
		stripped, "task_id") {
		t.Errorf("stripDocGateMarkers left marker text behind: %q", stripped)
	}
}

// TestStatusValueCheckCatchesEveryLayout is the other half of the
// review's acceptance test: an invented status/state value ("active")
// must fail in every layout the real docs use — JSON, vertical text,
// and a table column — not just the one layout a previous blocklist
// entry happened to be phrased for.
func TestStatusValueCheckCatchesEveryLayout(t *testing.T) {
	cases := map[string]string{
		"JSON":             `Expected: {"status": "active"} in the response.`,
		"JSON (state key)": `Expected: {"state": "active"} in the response.`,
		"vertical text":    "FIELD   VALUE\nID      abc\nStatus              active\n",
		"horizontal table": "ID    NAME     STATUS    REGIONS\nabc   test     active    us-east-1\n",
		"inline quoted": "Verify: → state \"active\"; Instances list " +
			"NODE/HOST/STATE (found by hand in skills/pgedge-controlplane/SKILL.md).",
		"key=value shorthand": "Expected: service with state=active " +
			"(found by hand in skills/pgedge-byoc/SKILL.md).",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkStatusStateValues("fixture.md",
				docSection{module: "byoc", text: fixture})
			if len(got) == 0 {
				t.Fatalf("checkStatusStateValues did not catch the "+
					"invented value \"active\" in the %s layout", name)
			}
		})
	}
}

// TestStatusValueCheckAllowsVerifiedVocabulary proves the allowlist
// does not misfire on real, verified values across both module scopes
// and both fields — the mirror image of the catches-everything test
// above, and the exact trap a single shared list would fall into
// ("completed" is real for cp and wrong for byoc; "degraded" is real
// for byoc `status` and wrong for byoc `state`; a merged list would
// either miss the wrong occurrence or reject the correct one).
func TestStatusValueCheckAllowsVerifiedVocabulary(t *testing.T) {
	cases := []struct {
		module string
		field  string
		value  string
	}{
		{"byoc", "status", "available"}, {"byoc", "status", "failed"},
		{"byoc", "status", "running"}, {"byoc", "status", "succeeded"},
		{"byoc", "status", "ok"},
		{"byoc", "state", "running"}, {"byoc", "state", "pending"},
		{"byoc", "state", "failed"},
		{"controlplane", "status", "completed"},
		{"controlplane", "status", "canceling"},
		{"controlplane", "status", "ok"},
		{"controlplane", "state", "restoring"},
		{"controlplane", "state", "available"},
		{"controlplane", "state", "backing_up"},
	}
	for _, tc := range cases {
		t.Run(tc.module+"/"+tc.field+"/"+tc.value, func(t *testing.T) {
			fixture := `Expected: {"` + tc.field + `": "` + tc.value + `"}.`
			got := checkStatusStateValues("fixture.md",
				docSection{module: tc.module, text: fixture})
			if len(got) != 0 {
				t.Fatalf("checkStatusStateValues rejected %q under "+
					"module %q field %q (violations: %v), but it is a "+
					"verified value for that field", tc.value, tc.module,
					tc.field, got)
			}
		})
	}
}

// TestStatusValueCheckHonoursDeliberatelyWrongMarker's first fixture
// is the real false positive that forced the opt-out: the byoc
// reference's Expose Service workflow correctly warned that an
// ingress registration has no status field, quoting `"status":
// "registered"` on purpose, and the check flagged it as a claim. The
// first "fix" for that was to flatten the concrete example into vaguer
// prose to dodge the gate, which is backwards; the sentence is correct
// and the gate was wrong to guess. It now says so out loud instead.
//
// The last two cases are the exact hedged sentences that broke round
// 3's 20-character proximity window — their negations sit 43 and 72
// characters from the value — and they are why this design replaced the
// window rather than moving it.
func TestStatusValueCheckHonoursDeliberatelyWrongMarker(t *testing.T) {
	cases := []struct {
		name      string
		sentence  string
		annotated string
	}{
		{
			name: "ingress registration warning",
			sentence: "There is no status field on an ingress " +
				"registration at all, so presence in the list plus a " +
				"populated `url` is the signal, not a `\"status\": " +
				"\"registered\"` value.",
			annotated: "There is no status field on an ingress " +
				"registration at all, so presence in the list plus a " +
				"populated `url` is the signal, not a `\"status\": " +
				"\"registered\"` value. <!-- doc-gate: " +
				"deliberately-wrong registered — quoted to name the " +
				"value a reader might wrongly expect -->",
		},
		{
			name: "contrast with the real value",
			sentence: "A resource is ready at `available`, not " +
				"`\"state\": \"active\"`.",
			annotated: "A resource is ready at `available`, not " +
				"`\"state\": \"active\"`. <!-- doc-gate: " +
				"deliberately-wrong active — contrasted against the " +
				"real value -->",
		},
		{
			name: "cross-module warning",
			sentence: "Do not expect `state=completed` under byoc; that " +
				"is not a real value here.",
			annotated: "Do not expect `state=completed` " +
				"<!-- doc-gate: deliberately-wrong completed — controlplane's " +
				"value, named here to warn it is not byoc's --> under " +
				"byoc; that is not a real value here.",
		},
		{
			name: "hedged with interjected clauses",
			sentence: "Do not, under any circumstances, ever expect the " +
				"response to include `\"status\": \"registered\"` here.",
			annotated: "Do not, under any circumstances, ever expect the " +
				"response to include `\"status\": \"registered\"` here. " +
				"<!-- doc-gate: deliberately-wrong registered — hedged " +
				"warning against a value the API never reports -->",
		},
		{
			name: "hedged with a long parenthetical",
			sentence: "Note that a cluster response will never, under " +
				"normal operation or during any documented failure mode, " +
				"report `\"status\": \"active\"`.",
			annotated: "Note that a cluster response will never, under " +
				"normal operation or during any documented failure mode, " +
				"report `\"status\": \"active\"`. <!-- doc-gate: " +
				"deliberately-wrong active — hedged warning against a " +
				"value the API never reports -->",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkStatusStateValues("fixture.md",
				docSection{module: "byoc", text: tc.sentence}); len(got) == 0 {
				t.Fatalf("unannotated sentence %q was not flagged — under "+
					"an explicit opt-out every unverified value must fire "+
					"until its author annotates it", tc.sentence)
			}
			if got := checkStatusStateValues("fixture.md",
				docSection{module: "byoc", text: tc.annotated}); len(got) != 0 {
				t.Fatalf("annotated sentence %q was still flagged "+
					"(violations: %v) — a doc-gate marker naming the "+
					"value must excuse it", tc.annotated, got)
			}
		})
	}

	// The opt-out must not become a general softening: a plain claim
	// with no marker anywhere still fires.
	stillWrong := `Expected: {"status": "active"} in the response.`
	if got := checkStatusStateValues("fixture.md",
		docSection{module: "byoc", text: stillWrong}); len(got) == 0 {
		t.Fatal("checkStatusStateValues missed a genuine unannotated claim")
	}
}

// TestStatusValueCheckRejectsUnannotatedClaim is
// TestNoEnvelopeCheckRejectsUnannotatedClaim's sibling for the other
// detector, and it keeps the same fixture for the same reason: "There
// is no doubt the response then reports `"status": "active"`
// immediately." has a real negation word governing "doubt", 37
// characters from the false "active" claim it does nothing to excuse.
// The old whole-sentence check let it through. Under an explicit opt-out
// it is simply an unannotated false claim, and keeping the case guards
// against any future attempt to re-derive polarity from the prose.
func TestStatusValueCheckRejectsUnannotatedClaim(t *testing.T) {
	stray := "There is no doubt the response then reports " +
		"`\"status\": \"active\"` immediately."
	control := "The response then reports `\"status\": \"active\"` immediately."

	if got := checkStatusStateValues("fixture.md",
		docSection{module: "byoc", text: control}); len(got) == 0 {
		t.Fatal("control sentence (no stray negation) was not flagged " +
			"— fix the fixture, not the assertion, before trusting " +
			"the stray-negation case below")
	}

	if got := checkStatusStateValues("fixture.md",
		docSection{module: "byoc", text: stray}); len(got) == 0 {
		t.Fatal("checkStatusStateValues let a real \"active\" claim " +
			"through — a \"no\" earlier in the sentence is not an " +
			"opt-out; only a doc-gate marker naming the value is")
	}
}

// TestStatusValueCheckIsModuleScoped proves "completed" — real for cp,
// invented for byoc — is accepted under cp and rejected under byoc
// from the exact same input text, which only a module-scoped
// allowlist (as opposed to one shared list, or a file-level exemption)
// can do correctly.
func TestStatusValueCheckIsModuleScoped(t *testing.T) {
	fixture := `Expected: {"status": "completed"}.`

	if got := checkStatusStateValues("fixture.md",
		docSection{module: "controlplane", text: fixture}); len(got) != 0 {
		t.Fatalf(`"completed" is a real cp TaskStatus value and must `+
			"not be rejected under controlplane scope (violations: %v)", got)
	}

	if got := checkStatusStateValues("fixture.md",
		docSection{module: "byoc", text: fixture}); len(got) == 0 {
		t.Fatal(`"completed" is not a byoc value (byoc uses ` +
			`"succeeded") and must be rejected under byoc scope`)
	}
}

// TestModuleSectionsSplitsOnModuleHeaders is a regression case for the
// scoping mechanism itself: llms.txt and llms-full.txt both mix
// modules under "## Module: X" headers, and status/state and task_id
// rules must follow whichever module's heading a given line sits
// under, not the file as a whole.
func TestModuleSectionsSplitsOnModuleHeaders(t *testing.T) {
	content := "preamble\n\n## Module: byoc\n\nbyoc text\n\n" +
		"## Module: controlplane\n\ncontrolplane text\n"

	sections := moduleSections("llms-full.txt", content)
	if len(sections) != 3 {
		t.Fatalf("got %d sections, want 3 (preamble, byoc, cp): %+v",
			len(sections), sections)
	}
	if sections[0].module != "byoc" || !strings.Contains(sections[0].text, "preamble") {
		t.Errorf("section 0 = %+v, want byoc-scoped preamble", sections[0])
	}
	if sections[1].module != "byoc" || !strings.Contains(sections[1].text, "byoc text") {
		t.Errorf("section 1 = %+v, want byoc-scoped byoc text", sections[1])
	}
	if sections[2].module != "controlplane" || !strings.Contains(sections[2].text, "controlplane text") {
		t.Errorf("section 2 = %+v, want controlplane-scoped text", sections[2])
	}
}

// ---------------------------------------------------------------------------
// The doc-gate opt-out's own containment properties
//
// These are the tests that make the marker a `//nolint:rule // reason`
// rather than a `//nolint` at the top of a file. Every bullet in the
// "the deliberately-wrong opt-out" note above has one of them.
// ---------------------------------------------------------------------------

// TestDocGateMarkerExcusesOnlyWhatItNames proves a marker cannot
// blanket-excuse its sentence. Both detectors get a sentence carrying
// two flagged phrases and a marker naming only one: the named one is
// excused, the other still fires. This is the property a per-sentence
// "trust me" flag would not have, and the reason the marker names a
// phrase at all.
func TestDocGateMarkerExcusesOnlyWhatItNames(t *testing.T) {
	t.Run("status value", func(t *testing.T) {
		fixture := "Presence in the list is the signal, not a " +
			"`\"status\": \"registered\"` value and never a " +
			"`\"state\": \"active\"` one. <!-- doc-gate: " +
			"deliberately-wrong registered — quoted to warn against it -->"

		got := checkStatusStateValues("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 {
			t.Fatalf("want exactly 1 violation (the unnamed \"active\"), "+
				"got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0], `"active"`) {
			t.Errorf("the surviving violation should be the unnamed "+
				"value \"active\", got %q", got[0])
		}
	})

	t.Run("envelope sentinel", func(t *testing.T) {
		fixture := "There is no `task_id` here and you should read the " +
			"task output instead. <!-- doc-gate: deliberately-wrong " +
			"task_id — documents the field's absence -->"

		got := checkNoEnvelopeClaims("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 {
			t.Fatalf("want exactly 1 violation (the unnamed \"task "+
				"output\"), got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0], "task output") {
			t.Errorf("the surviving violation should be the unnamed "+
				"sentinel \"task output\", got %q", got[0])
		}
	})
}

// TestDocGateMarkerHasNoWiderScope proves there is no file-scoped or
// section-scoped form of the opt-out, which is the distinction the
// ban on per-file exemptions turns on. A marker reaches the
// sentence it sits in (or, when it trails a sentence's closing
// punctuation, that sentence) and nothing else: not the next sentence,
// not the rest of the section, and not because of which file it is in.
func TestDocGateMarkerHasNoWiderScope(t *testing.T) {
	// A marker attached to one sentence does not reach the next.
	twoSentences := "There is no `task_id` in any response. " +
		"<!-- doc-gate: deliberately-wrong task_id — documents the " +
		"field's absence --> Capture the returned `task_id` and poll it."
	got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: twoSentences})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation (the second sentence's real "+
			"claim), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Capture the returned") {
		t.Errorf("the surviving violation should be the following "+
			"sentence's claim, got %q", got[0])
	}

	// A marker written at the head of a section — the closest thing to
	// a file-level pragma this syntax allows — covers only its own
	// sentence, and is itself reported for excusing nothing.
	sectionHead := "<!-- doc-gate: deliberately-wrong task_id — an " +
		"attempt at a section-wide exemption --> Overview of the byoc " +
		"module. Capture the returned `task_id` and poll it."
	got = checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: sectionHead})
	if len(got) != 2 {
		t.Fatalf("want 2 violations (the unexcused claim, plus the "+
			"marker that excused nothing), got %d: %v", len(got), got)
	}

	// Nothing is keyed on the path: the identical section produces the
	// identical result under any file name, so no file can be
	// privileged by adding it to a list somewhere.
	claim := docSection{module: "byoc",
		text: "Capture the returned `task_id` and poll it."}
	for _, path := range []string{"llms-full.txt", "README.md",
		"skills/pgedge-byoc/SKILL.md", "docs/index.md"} {
		if got := checkNoEnvelopeClaims(path, claim); len(got) != 1 {
			t.Errorf("path %q changed the outcome (%d violations: %v) — "+
				"the opt-out must not be file-scoped in any form",
				path, len(got), got)
		}
	}
}

// TestDocGateMarkerRequiresPhraseAndReason proves a marker without a
// reason is not a marker. An exception whose justification nobody wrote
// down is the thing that rots into a blanket exemption, so the reason is
// enforced rather than merely conventional — and the claim beside a
// reasonless marker still fires, so the failure cannot be mistaken for
// a pass.
func TestDocGateMarkerRequiresPhraseAndReason(t *testing.T) {
	claim := "Capture the returned `task_id` and poll it."

	cases := map[string]string{
		"no separator and no reason": claim +
			" <!-- doc-gate: deliberately-wrong task_id -->",
		"separator but empty reason": claim +
			" <!-- doc-gate: deliberately-wrong task_id — -->",
		"no phrase": claim +
			" <!-- doc-gate: deliberately-wrong — some reason -->",
		// A reason containing sentence-ending punctuation splits the
		// marker in half at sentenceSplitRe, so no complete marker
		// survives. Reporting it is what keeps this from being a silent
		// no-op — the exact class of self-inflicted false pass this
		// file's harness notes warn about.
		"reason split by a full stop": claim +
			" <!-- doc-gate: deliberately-wrong task_id — it is absent. " +
			"Truly -->",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			sec := docSection{module: "byoc", text: fixture}

			if got := checkDocGateMarkers("fixture.md", sec); len(got) == 0 {
				t.Errorf("malformed marker was not reported: %q", fixture)
			}
			if got := checkNoEnvelopeClaims("fixture.md", sec); len(got) == 0 {
				t.Errorf("a malformed marker excused the claim beside it "+
					"(%q) — an unusable marker must excuse nothing", fixture)
			}
		})
	}

	// The positive control: the same claim with a well-formed marker
	// reports no marker defect and no violation, so the assertions
	// above are reacting to the malformation and not to the fixture
	// shape.
	good := docSection{module: "byoc", text: claim +
		" <!-- doc-gate: deliberately-wrong task_id — control case -->"}
	if got := checkDocGateMarkers("fixture.md", good); len(got) != 0 {
		t.Fatalf("well-formed marker reported as malformed: %v", got)
	}
	if got := checkNoEnvelopeClaims("fixture.md", good); len(got) != 0 {
		t.Fatalf("well-formed marker did not excuse its phrase: %v", got)
	}
}

// TestDocGateMarkerMustExcuseSomething proves a marker that excuses
// nothing is an error rather than dead weight. That is what keeps the
// annotations honest as the docs are edited: delete or reword the
// warning sentence and its marker fails loudly instead of lingering as
// a licence someone later widens.
func TestDocGateMarkerMustExcuseSomething(t *testing.T) {
	t.Run("stale status marker", func(t *testing.T) {
		// The wrong value this marker was written for is gone.
		fixture := "There is no status field on an ingress registration " +
			"at all. <!-- doc-gate: deliberately-wrong registered — " +
			"left behind after the example was removed -->"
		got := checkStatusStateValues("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 || !strings.Contains(got[0], "registered") {
			t.Fatalf("want 1 stale-marker violation naming "+
				"\"registered\", got %d: %v", len(got), got)
		}
	})

	t.Run("redundant status marker", func(t *testing.T) {
		// "available" is verified byoc vocabulary, so it was never
		// going to be flagged and the marker excuses nothing.
		fixture := "Expected: {\"status\": \"available\"} in the " +
			"response. <!-- doc-gate: deliberately-wrong available — " +
			"unnecessary, this value is real -->"
		got := checkStatusStateValues("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 || !strings.Contains(got[0], "available") {
			t.Fatalf("want 1 redundant-marker violation naming "+
				"\"available\", got %d: %v", len(got), got)
		}
	})

	t.Run("stale sentinel marker", func(t *testing.T) {
		fixture := "Poll the resource until it reports `available`. " +
			"<!-- doc-gate: deliberately-wrong task_id — left behind " +
			"after the sentence was reworded -->"
		got := checkNoEnvelopeClaims("fixture.md",
			docSection{module: "byoc", text: fixture})
		if len(got) != 1 || !strings.Contains(got[0], "task_id") {
			t.Fatalf("want 1 stale-marker violation naming \"task_id\", "+
				"got %d: %v", len(got), got)
		}
	})

	t.Run("a marker cannot be spent across the namespace split",
		func(t *testing.T) {
			// Contrived on purpose: a sentinel name appearing as a status
			// VALUE. Dead-marker reporting was partitioned by owner from
			// the start, but spending was not, so the status detector
			// would consume a sentinel marker and wave this through —
			// breaking the "names the one phrase it excuses" property in
			// letter, which is the whole justification for the opt-out.
			fixture := "Expected: {\"status\": \"task_id\"} in the " +
				"response. <!-- doc-gate: deliberately-wrong task_id — " +
				"a sentinel marker, not a value marker -->"

			got := checkStatusStateValues("fixture.md",
				docSection{module: "byoc", text: fixture})
			if len(got) == 0 {
				t.Fatal("the status detector spent a sentinel-owned " +
					"marker and excused an invalid status value")
			}
			found := false
			for _, m := range got {
				if strings.Contains(m, `example shows "status" "task_id"`) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected the invalid value itself to be "+
					"reported, got %v", got)
			}
		})

	t.Run("each check reports only its own dead markers", func(t *testing.T) {
		// The two checks partition the allowance namespace between
		// them; neither may report the other's markers, or every doc
		// carrying either kind would fail twice over.
		sec := docSection{module: "byoc", text: "Nothing wrong here. " +
			"<!-- doc-gate: deliberately-wrong task_id — sentinel " +
			"marker --> <!-- doc-gate: deliberately-wrong registered — " +
			"status marker -->"}

		envelope := checkNoEnvelopeClaims("fixture.md", sec)
		if len(envelope) != 1 || !strings.Contains(envelope[0], "task_id") {
			t.Errorf("checkNoEnvelopeClaims should report exactly the "+
				"sentinel marker, got %v", envelope)
		}
		status := checkStatusStateValues("fixture.md", sec)
		if len(status) != 1 || !strings.Contains(status[0], "registered") {
			t.Errorf("checkStatusStateValues should report exactly the "+
				"non-sentinel marker, got %v", status)
		}
	})
}

// TestDocGateMarkerCoversRepeatedValueOccurrences is a regression case
// for a narrower rerun of the exact failure this whole design exists to
// end: a correct sentence firing with no remedy available to its author.
// jsonStatusRe matches `"status": "registered"` and `"state":
// "registered"` separately, so one sentence quoting the same wrong value
// twice produces two violations — and while allowances were deduped by
// name and spent as booleans, no number of markers could clear the
// second one. Allowances are counted, so N markers clear N occurrences
// and N-1 markers still fire.
func TestDocGateMarkerCoversRepeatedValueOccurrences(t *testing.T) {
	const twice = "Presence in the list is the signal, not a " +
		"`\"status\": \"registered\"` value and not a " +
		"`\"state\": \"registered\"` one."
	marker := " <!-- doc-gate: deliberately-wrong registered — quoted " +
		"to warn against it -->"

	cases := []struct {
		name    string
		text    string
		want    int
		wantWhy string
	}{
		{"no marker (control)", twice, 2,
			"both occurrences must fire, or this fixture proves nothing"},
		{"one marker", twice + marker, 1,
			"one marker must excuse exactly one occurrence"},
		{"two markers", twice + marker + marker, 0,
			"two markers must excuse both occurrences — the author needs " +
				"a remedy that works"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkStatusStateValues("fixture.md",
				docSection{module: "byoc", text: tc.text})
			if len(got) != tc.want {
				t.Fatalf("want %d violations, got %d (%s): %v",
					tc.want, len(got), tc.wantWhy, got)
			}
		})
	}
}

// TestDocGateMarkerDoesNotDisableTableColumnDetector is a regression
// case for a marker line switching the horizontal STATUS/STATE column
// detector off for the rest of its table. The line pass strips marker
// text so a marker can never be misread as a table cell; if stripping
// leaves a blank line behind, the blank-line rule clears headerIdx and
// every remaining row of that table goes unchecked. The table detectors
// have no opt-out by design, so nothing may weaken them.
//
// Both marker shapes are covered, because they failed differently: a
// single-line marker vanished entirely and blanked its line, while one
// wrapped at the 79-column rule was not stripped at all (the reason
// group does not cross a newline without (?s)) and so could be misread
// as a table cell instead.
//
// The marker in these fixtures is deliberately unrelated to the table —
// the realistic case is a marker in prose that happens to sit near one —
// so it also draws a dead-marker report. That report is not what this
// test measures, so the assertions count STATUS-column violations only.
func TestDocGateMarkerDoesNotDisableTableColumnDetector(t *testing.T) {
	const header = "NAME    STATUS\n"
	const rows = "alpha   provisioned\nbeta    provisioned\n" +
		"gamma   provisioned\n"

	countColumnViolations := func(msgs []string) int {
		n := 0
		for _, m := range msgs {
			if strings.Contains(m, `example shows STATUS`) {
				n++
			}
		}
		return n
	}

	cases := []struct {
		name    string
		between string
		want    int
	}{
		{"nothing (control)", "", 3},
		{"an ordinary HTML comment (control)",
			"<!-- an ordinary comment -->\n", 3},
		{"arbitrary prose (control)", "some prose about the table\n", 3},
		{"a single-line doc-gate marker",
			"<!-- doc-gate: deliberately-wrong active — unrelated to " +
				"this table -->\n", 3},
		{"a wrapped doc-gate marker",
			"<!-- doc-gate: deliberately-wrong active — unrelated to\n" +
				"this table -->\n", 3},
		// Pre-existing and deliberate: a genuinely blank line ends the
		// table, so the rows after it are a different block. Kept as a
		// control so the cases above cannot pass by accident of the
		// blank-line rule having been removed.
		{"a genuinely blank line (pre-existing behaviour)", "\n", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkStatusStateValues("fixture.md", docSection{
				module: "byoc", text: header + tc.between + rows})
			if n := countColumnViolations(got); n != tc.want {
				t.Fatalf("want %d STATUS-column violations, got %d — a "+
					"marker between a header and its rows must not stop "+
					"the STATUS column being checked: %v", tc.want, n, got)
			}
		})
	}
}

// TestDocGateMarkerKeepsStrippedAndRawLineCountsAligned binds the
// line-count-alignment invariant stripDocGateMarkers documents — both
// failure directions and all of the reasoning are there — which is
// narrower and more specific than "a marker must not look like a table
// cell", and which TestDocGateMarkerDoesNotDisableTableColumnDetector
// does not cover.
//
// The shape below is the case where losing alignment changes a VERDICT
// rather than merely shifting an index: a wrapped marker, then a
// GENUINE blank line, then a row that must sit outside the table. The
// shift makes the genuine blank line compare against the marker's
// second raw line, found non-blank and misread as marker residue — the
// "skip but keep the header" branch fires, headerIdx is never cleared,
// and the trailing row is wrongly checked as a table row. One false
// positive, on a doc that is correct.
func TestDocGateMarkerKeepsStrippedAndRawLineCountsAligned(t *testing.T) {
	const header = "NAME    STATUS\n"
	const goodRow = "alpha   available\n"
	// Wrapped at the 79-column rule, exactly as the real markers in
	// the llms.txt references are.
	const wrappedMarker = "<!-- doc-gate: deliberately-wrong active — " +
		"unrelated to\nthis table -->\n"
	// A genuine paragraph break, then prose that happens to split into
	// two columns. It is NOT a table row and must never be read as one.
	const afterBreak = "\ntrailing   provisioned\n"

	countColumnViolations := func(msgs []string) int {
		n := 0
		for _, m := range msgs {
			if strings.Contains(m, `example shows STATUS`) {
				n++
			}
		}
		return n
	}

	t.Run("wrapped marker then blank line then content", func(t *testing.T) {
		got := checkStatusStateValues("fixture.md", docSection{module: "byoc",
			text: header + goodRow + wrappedMarker + afterBreak})
		if n := countColumnViolations(got); n != 0 {
			t.Fatalf("want 0 STATUS-column violations, got %d — the "+
				"genuine blank line after the wrapped marker must end the "+
				"table, so the trailing line is prose and not a row. "+
				"Stripped and raw text have almost certainly fallen out "+
				"of line-count alignment: %v", n, got)
		}
	})

	// Control 1: the same expectation with no marker at all, so raw and
	// stripped are trivially aligned. If this ever disagrees with the
	// case above, the marker is doing something to the table boundary
	// that it must not.
	t.Run("control: no marker, same boundary", func(t *testing.T) {
		got := checkStatusStateValues("fixture.md", docSection{module: "byoc",
			text: header + goodRow + afterBreak})
		if n := countColumnViolations(got); n != 0 {
			t.Fatalf("want 0 STATUS-column violations without any marker, "+
				"got %d — the fixture's own expectation is wrong, so fix "+
				"the fixture before trusting the case above: %v", n, got)
		}
	})

	// Control 2: the fixture shape must still be capable of reporting a
	// violation, or "0 violations" above proves only that the detector
	// was switched off. A bad value INSIDE the table, before the marker,
	// must fire.
	t.Run("control: a bad row inside the table still fires", func(t *testing.T) {
		got := checkStatusStateValues("fixture.md", docSection{module: "byoc",
			text: header + "alpha   provisioned\n" + wrappedMarker + afterBreak})
		if n := countColumnViolations(got); n != 1 {
			t.Fatalf("want exactly 1 STATUS-column violation for the bad "+
				"row inside the table, got %d — if this is 0 the detector "+
				"is off and the assertions above are vacuous: %v", n, got)
		}
	})
}

// TestDocGateMarkerSurvivesLineWrap is the counterpart to
// TestNormalizeWhitespaceCatchesWrappedCommand, for the irony this file
// has already produced once: the 79-column wrap rule that these docs
// follow will split a marker across two lines, and a marker the gate
// then failed to see would silently stop excusing anything. The fixture
// asserts it really is wrapped before asserting the marker still works.
func TestDocGateMarkerSurvivesLineWrap(t *testing.T) {
	wrapped := "The response is the updated database object, not a task —\n" +
		"there is no `task_id` to capture.\n" +
		"<!-- doc-gate: deliberately-wrong task_id — this sentence\n" +
		"documents the field's absence -->\n"

	if strings.Contains(wrapped, "deliberately-wrong task_id — this "+
		"sentence documents") {
		t.Fatal("test fixture is not actually wrapped across the marker " +
			"— fix the fixture")
	}

	if got := checkNoEnvelopeClaims("fixture.md",
		docSection{module: "byoc", text: wrapped}); len(got) != 0 {
		t.Fatalf("a line-wrapped marker stopped excusing its phrase "+
			"(violations: %v)", got)
	}
}

// TestNoEnvelopeCheckCatchesByocClaimInCPScopedFile pins the
// per-sentence scoping checkNoEnvelopeClaims explains: a blanket skip
// of cp sections would be a whole-FILE exemption — the file-scoped
// sibling of the sentence-level hole documented on
// byocCommandMentionRe, with a wider blast radius.
func TestNoEnvelopeCheckCatchesByocClaimInCPScopedFile(t *testing.T) {
	got := checkNoEnvelopeClaims("skills/pgedge-controlplane/SKILL.md",
		docSection{
			module: "controlplane",
			text: "After `pgedge starfleet byoc database mcp deploy` returns, " +
				"capture the `task_id` and poll it.",
		})
	if len(got) == 0 {
		t.Fatal("a false BYOC task_id claim in a controlplane-scoped section " +
			"was not flagged — the whole-file exemption is still open")
	}
}

// TestNoEnvelopeCheckStillExemptsGenuineCPSentences guards the other
// direction: narrowing the cp exemption must not start flagging controlplane's
// own legitimate task_id, which is a real field on controlplane's Task type.
func TestNoEnvelopeCheckStillExemptsGenuineCPSentences(t *testing.T) {
	for _, text := range []string{
		"Capture the returned `task_id` and poll it.",
		"Run `pgedge controlplane task cancel <task_id>` to stop it.",
	} {
		got := checkNoEnvelopeClaims("skills/pgedge-controlplane/SKILL.md",
			docSection{module: "controlplane", text: text})
		if len(got) != 0 {
			t.Errorf("flagged a genuine cp sentence %q: %v", text, got)
		}
	}
}

// TestStatusValueCheckRejectsValueFromTheOtherFieldsVocabulary pins
// the per-field split: a flat union of both vocabularies cannot catch a value
// quoted for the WRONG field. The first case is the measured escape —
// `state` reading `degraded` (a legal resource .status, an illegal
// service .state) planted in docs/workflows/byoc-services.md passed
// every gate.
func TestStatusValueCheckRejectsValueFromTheOtherFieldsVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		module  string
		fixture string
	}{
		{"the measured escape: inline state degraded", "byoc",
			`A deploy can leave the service with state "degraded".`},
		{"key=value state degraded", "byoc",
			"Expected: service with state=degraded."},
		{"JSON state degraded", "byoc",
			`Expected: {"state": "degraded"} in the response.`},
		{"JSON status pending", "byoc",
			`Expected: {"status": "pending"} in the response.`},
		{"controlplane status available", "controlplane",
			`Expected: {"status": "available"} in the response.`},
		{"controlplane state completed", "controlplane",
			`Expected: {"state": "completed"} in the response.`},
		{"horizontal table STATE degraded", "byoc",
			"SERVICE ID    TYPE    STATE\nabc           mcp     degraded\n"},
		{"vertical text State degraded", "byoc",
			"FIELD   VALUE\nID      abc\nState              degraded\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkStatusStateValues("fixture.md",
				docSection{module: tc.module, text: tc.fixture})
			if len(got) == 0 {
				t.Fatalf("checkStatusStateValues accepted a value from "+
					"the wrong field's vocabulary in %q — the union-set "+
					"escape", tc.fixture)
			}
		})
	}
}

// TestStatusValueCheckReadsEveryStatusStateColumn pins a review
// finding: a header row naming BOTH a STATUS and a STATE column got
// only its first match checked, so `degraded` under STATE — the exact
// measured escape — survived in the horizontal layout whenever a STATUS
// column sat to its left. Three columns, because a two-field header
// would be read by the vertical detector instead.
func TestStatusValueCheckReadsEveryStatusStateColumn(t *testing.T) {
	fixture := "NAME    STATUS       STATE\n" +
		"db1     available    degraded\n"
	got := checkStatusStateValues("fixture.md",
		docSection{module: "byoc", text: fixture})
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation (STATE degraded; STATUS "+
			"available is legal), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "degraded") ||
		!strings.Contains(got[0], "`state`") {
		t.Fatalf("the violation should name degraded against the "+
			"state vocabulary, got %q", got[0])
	}
}
