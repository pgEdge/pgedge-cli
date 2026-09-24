package clitest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Example-output blocks in the references are hand-written prose: the
// generator emits a command's Short, flags and usage, never its output.
// So nothing tied them to the code that prints them, and they rotted —
// 36 blocks in one file described output the CLI cannot produce.
// Two of them contradicted the command directly above them.
//
// This file binds the one part of an output block that has a single
// answer in the source: a table's header row. Every table the CLI
// renders takes its headers from a []string in the command's own
// package, so a header row in a doc either matches one of those or the
// doc is describing a table that does not exist.
//
// What this CANNOT see, and why fixing those blocks could not be
// mechanical: a MISSING line. Thirteen of those 36 blocks were wrong by
// omitting the `Monitor with: ...` line that every non-`--wait`
// mutation prints, and a check over what IS written cannot find what is
// absent. Deciding that needs trackMutation's call sites, not string
// matching.

// exampleTableScopes maps a doc to the packages whose column
// declarations it may quote. The scoping is the whole point of the
// check and must stay TIGHT: `[]string{"FIELD", "VALUE"}` exists in
// internal/cli (root `profile show`), and eleven byoc blocks were wrong
// precisely by showing a FIELD/VALUE table. Put internal/cli in byoc's
// scope and all eleven pass.
//
// internal/output earns its place in every scope: the doctor table's
// CHECK/STATUS/DETAILS headers live there and every module has a
// doctor.

// referenceTableScopes is that mapping for the reference side, keyed
// by module directory as ReferenceModuleDir spells it ("" is the
// index). Keying by module rather than by file is what carries the
// scoping across the resource split: a page inherits its module's
// packages, so a page added tomorrow is scoped the day it lands
// instead of the day someone remembers to add a row.
var referenceTableScopes = map[string][]string{
	"":                                 {"../cli", "../output"},
	"../../internal/starfleet":         {"../starfleet/account/cmd", "../output"},
	"../../internal/starfleet/byoc":    {"../starfleet/byoc/cmd", "../output"},
	"../../internal/starfleet/managed": {"../starfleet/managed/cmd", "../output"},
	"../../internal/controlplane":      {"../controlplane/cmd", "../output"},
}

var exampleTableScopes = buildExampleTableScopes()

// buildExampleTableScopes expands referenceTableScopes over every
// reference file and adds the skills, which are one document each and
// so are still named individually.
//
// A module with no referenceTableScopes row contributes no entry at
// all rather than an empty one: knownColumnTuples fails loudly on a
// document it has no scope for, and an empty scope would instead
// report every table in that module's pages.
func buildExampleTableScopes() map[string][]string {
	out := map[string][]string{
		"../../skills/pgedge/SKILL.md":           {"../cli", "../output"},
		"../../skills/pgedge-starfleet/SKILL.md": {"../starfleet/account/cmd", "../output"},
		"../../skills/pgedge-byoc/SKILL.md":      {"../starfleet/byoc/cmd", "../output"},
		"../../skills/pgedge-managed/SKILL.md": {
			"../starfleet/managed/cmd", "../output",
		},
		"../../skills/pgedge-controlplane/SKILL.md": {"../controlplane/cmd", "../output"},
	}
	for _, f := range ReferenceFiles() {
		if dirs, ok := referenceTableScopes[ReferenceModuleDir(f)]; ok {
			out[f] = dirs
		}
	}
	return out
}

// tableHeaderRe matches a rendered table's header row: two or more
// runs of upper-case words separated by two or more spaces. Two spaces
// is the renderer's own column gap, so a single-spaced caption like
// "NOTE THIS" cannot be mistaken for a header, and a lower-case label
// row ("Client ID:  ...") is not a table at all.
var tableHeaderRe = regexp.MustCompile(
	`^[A-Z][A-Z0-9 ()/]*?(  +[A-Z][A-Z0-9 ()/]*?)+$`)

// columnGapRe splits a header row on the renderer's column gap.
var columnGapRe = regexp.MustCompile(`  +`)

// tupleKey joins column names into the key both the tuple universe and
// the doc side are compared on. NUL cannot occur in a column name, so a
// two-column {"A B", "C"} can never collide with a three-column
// {"A", "B", "C"}.
func tupleKey(cells ...string) string {
	return strings.Join(cells, "\x00")
}

// collectColumnTuples walks dir and returns every []string composite
// literal whose elements are all string constants, keyed by the tuple
// joined on NUL.
//
// Both spellings are collected on purpose: most tables name their
// columns in a package-level `var xColumns = []string{...}`, but
// `profile show` passes the slice inline to Print. Collecting only the
// named form would leave the root reference's one legitimate
// FIELD/VALUE table unexplainable.
func collectColumnTuples(t *testing.T, dir string) map[string]bool {
	t.Helper()
	if cached, ok := columnTuplesByDir[dir]; ok {
		return cached
	}
	tuples := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Test files are skipped: a fixture in a _test.go file is free
		// to invent a table, and letting one legitimise a doc claim
		// would hollow out this check.
		if info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", path, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || len(lit.Elts) < 2 {
				return true
			}
			arr, ok := lit.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "string" {
				return true
			}

			cells := make([]string, 0, len(lit.Elts))
			for _, el := range lit.Elts {
				bl, ok := el.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				v, uerr := strconv.Unquote(bl.Value)
				if uerr != nil {
					return true
				}
				cells = append(cells, v)
			}
			tuples[tupleKey(cells...)] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("collecting column tuples from %s: %v", dir, err)
	}
	columnTuplesByDir[dir] = tuples
	return tuples
}

// columnTuplesByDir caches the AST walk per source directory. Since
// the split there are 48 reference documents rather than five, and the
// per-doc cache below would otherwise re-parse the same three cmd
// packages once per page.
var columnTuplesByDir = map[string]map[string]bool{}

var knownColumnTuplesCache = map[string]map[string]bool{}

// knownColumnTuples builds and caches the tuple universe for one doc.
func knownColumnTuples(t *testing.T, doc string) map[string]bool {
	t.Helper()
	if cached, ok := knownColumnTuplesCache[doc]; ok {
		return cached
	}

	dirs, ok := exampleTableScopes[doc]
	if !ok {
		t.Fatalf("no source dirs registered for %q — a doc carrying "+
			"example output must declare which packages' column "+
			"declarations it may quote, or this check silently skips it",
			doc)
	}

	tuples := make(map[string]bool)
	for _, dir := range dirs {
		for tuple := range collectColumnTuples(t, dir) {
			tuples[tuple] = true
		}
	}

	// A universe that came back tiny means the walk found nothing and
	// every table in the doc is about to be reported. Fail loudly
	// rather than flag correct docs wholesale.
	if len(tuples) < 3 {
		t.Fatalf("only %d column tuples collected for %q — the source "+
			"walk is broken and this check would flag correct docs "+
			"wholesale", len(tuples), doc)
	}

	knownColumnTuplesCache[doc] = tuples
	return tuples
}

// fencedBlock is one ``` block, with the 1-based line of its opening
// fence so a violation can name where to look.
type fencedBlock struct {
	line int
	body []string
}

// fencedBlocks returns every fenced block in content. Doc-gate markers
// are stripped first, on the same terms as the STATUS/STATE detector:
// a marker sitting between a header and its rows must not take the
// table with it.
func fencedBlocks(content string) []fencedBlock {
	lines := strings.Split(stripDocGateMarkers(content), "\n")
	var out []fencedBlock
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			continue
		}
		j := i + 1
		for j < len(lines) &&
			strings.TrimSpace(lines[j]) != "```" {
			j++
		}
		out = append(out, fencedBlock{line: i + 1, body: lines[i+1 : min(j, len(lines))]})
		i = j
	}
	return out
}

// excusedHeaders returns the header tuples a doc annotates as
// deliberately wrong, keyed the same way as the tuple universe.
//
// The allowance is FILE-scoped, which is narrower than the
// sentence-scoped, count-based allowance the prose checks use. A table
// is not a sentence: its header row is one line and a marker cannot sit
// inside it without breaking the very alignment being checked, so there
// is nothing finer to scope to. The looser scope costs little — a
// marker still has to name the exact tuple — and the alternative is a
// correct doc with no remedy, which is the failure class the marker
// system exists to end.
func excusedHeaders(content string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range docGateMarkerRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "deliberately-wrong" {
			continue
		}
		phrase := strings.TrimSpace(m[2])
		phrase = strings.Trim(phrase, "`")
		out[tupleKey(columnGapRe.Split(phrase, -1)...)] = true
		// A marker naming the tuple with single spaces is accepted too:
		// the author is quoting column names, not reproducing the
		// renderer's gap.
		out[tupleKey(strings.Fields(phrase)...)] = true
	}
	return out
}

// tableHeaderViolations reports every header row in content that
// matches no tuple in the universe and is not excused, and how many
// header rows it examined. The count is returned rather than logged so
// callers can assert the check looked at something: a pass that
// examined nothing is indistinguishable from a clean one.
func tableHeaderViolations(
	doc, content string, tuples, excused map[string]bool,
) (violations []string, checked int) {
	for _, block := range fencedBlocks(content) {
		for k, line := range block.body {
			trimmed := strings.TrimRight(line, " \t")
			if !tableHeaderRe.MatchString(trimmed) {
				continue
			}
			checked++
			headers := columnGapRe.Split(strings.TrimSpace(trimmed), -1)
			key := tupleKey(headers...)
			if tuples[key] || excused[key] {
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s:%d: table headers %v match no column declaration "+
					"in scope.\nThe CLI renders a table's headers from "+
					"a []string in the command's own package, so this "+
					"block describes a table the CLI does not print. "+
					"Capture the real output, or read the column slice. "+
					"If the table is wrong on purpose, annotate it: "+
					"<!-- doc-gate: deliberately-wrong %s — reason -->",
				doc, block.line+k+1, headers,
				strings.Join(headers, " ")))
		}
	}
	return violations, checked
}

// TestExampleOutputHeadersMatchDeclaredColumns fails when a doc shows a
// table whose header row matches no column declaration in scope.
func TestExampleOutputHeadersMatchDeclaredColumns(t *testing.T) {
	var total int
	for doc := range exampleTableScopes {
		content, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		violations, checked := tableHeaderViolations(doc, string(content),
			knownColumnTuples(t, doc), excusedHeaders(string(content)))
		total += checked
		for _, v := range violations {
			t.Error(v)
		}
	}

	// The references carry example tables today; if this drops to zero
	// the block parser or tableHeaderRe has broken, not the docs.
	if total == 0 {
		t.Fatal("no table header rows examined — the fenced-block " +
			"parser or tableHeaderRe has broken, and this check is " +
			"passing without looking at anything")
	}
	t.Logf("examined %d table header row(s)", total)
}

// The checks below cover this gate the way the flag- and field-claim
// gates are covered: prove it fires on a fabricated table, prove it
// stays quiet on a real one, and prove the scoping and the remedy work.
// Without them a refactor can neuter the gate while every doc still
// passes.

func TestExampleTableCheckCatchesFabricatedHeader(t *testing.T) {
	// byoc declares no FIELD/VALUE table; eleven blocks claimed one.
	content := "```\nFIELD               VALUE\n" +
		"ID                  abc\n```\n"
	tuples := map[string]bool{
		tupleKey("ID", "NAME", "STATUS"): true,
	}
	violations, checked := tableHeaderViolations(
		"fake.txt", content, tuples, nil)
	if checked != 1 {
		t.Fatalf("examined %d header rows, want 1 — the parser did not "+
			"reach the fabricated table, so a pass would prove nothing",
			checked)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want 1: %v", len(violations),
			violations)
	}
	if !strings.Contains(violations[0], "FIELD VALUE") {
		t.Errorf("violation does not name the offending headers: %s",
			violations[0])
	}
}

func TestExampleTableCheckAcceptsDeclaredHeaders(t *testing.T) {
	content := "```\nID    NAME  STATUS\nx     y     z\n```\n"
	tuples := map[string]bool{
		tupleKey("ID", "NAME", "STATUS"): true,
	}
	violations, checked := tableHeaderViolations(
		"fake.txt", content, tuples, nil)
	if checked != 1 {
		t.Fatalf("examined %d header rows, want 1", checked)
	}
	if len(violations) != 0 {
		t.Errorf("declared headers were reported: %v", violations)
	}
}

func TestExampleTableCheckIsScopedToItsOwnModule(t *testing.T) {
	// The scoping is what makes the check work: FIELD/VALUE is real in
	// internal/cli, and byoc's eleven fabrications all passed for years
	// because nothing said WHICH package a doc may quote. A tuple
	// declared elsewhere must still be reported here.
	byoc := knownColumnTuples(t, "../../internal/starfleet/byoc/llms.txt")
	root := knownColumnTuples(t, "../../llms.txt")

	fieldValue := tupleKey("FIELD", "VALUE")
	if !root[fieldValue] {
		t.Fatal("FIELD/VALUE is not in the root scope — this test's " +
			"premise is stale; it lives in internal/cli/profile.go")
	}
	if byoc[fieldValue] {
		t.Error("FIELD/VALUE is in byoc's scope, so the eleven " +
			"fabricated detail tables would pass again")
	}
}

func TestExampleTableCheckHonoursDeliberatelyWrongMarker(t *testing.T) {
	content := "<!-- doc-gate: deliberately-wrong FIELD VALUE — " +
		"quoting the shape the CLI does NOT print -->\n" +
		"```\nFIELD  VALUE\nID     abc\n```\n"
	tuples := map[string]bool{
		tupleKey("ID", "NAME"): true,
	}
	violations, checked := tableHeaderViolations(
		"fake.txt", content, tuples, excusedHeaders(content))
	if checked != 1 {
		t.Fatalf("examined %d header rows, want 1", checked)
	}
	if len(violations) != 0 {
		t.Errorf("marker did not excuse the annotated table: %v",
			violations)
	}
}

func TestExampleTableCheckMarkerExcusesOnlyWhatItNames(t *testing.T) {
	// A marker is not a blanket switch: naming one tuple must not
	// silence a different fabricated table in the same file.
	content := "<!-- doc-gate: deliberately-wrong FIELD VALUE — " +
		"reason -->\n" +
		"```\nFIELD  VALUE\nID     abc\n```\n" +
		"```\nWIDGET  SPROCKET\na       b\n```\n"
	tuples := map[string]bool{
		tupleKey("ID", "NAME"): true,
	}
	violations, checked := tableHeaderViolations(
		"fake.txt", content, tuples, excusedHeaders(content))
	if checked != 2 {
		t.Fatalf("examined %d header rows, want 2", checked)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want 1 (the unannotated table): %v",
			len(violations), violations)
	}
	if !strings.Contains(violations[0], "WIDGET SPROCKET") {
		t.Errorf("wrong table reported: %s", violations[0])
	}
}

func TestExampleTableScopesCoverEveryScannedReference(t *testing.T) {
	// scanDocFiles is the repo's own list of agent-facing docs. Any
	// reference document or SKILL.md in it must have a scope, or this
	// gate silently skips a whole reference — the way namedDocs once
	// missed a module.
	//
	// Reference membership is asked of ReferenceFiles rather than of
	// the file's name: a page is `cluster.txt`, so a base == "llms.txt"
	// test skipped all 43 of them and this gate stopped covering
	// everything the split moved.
	isReference := make(map[string]bool)
	for _, f := range ReferenceFiles() {
		isReference[f] = true
	}
	for _, path := range scanDocFiles(t) {
		if !isReference[path] && filepath.Base(path) != "SKILL.md" {
			continue
		}
		if _, ok := exampleTableScopes[path]; !ok {
			t.Errorf("%s is scanned by scanDocFiles but has no entry in "+
				"exampleTableScopes, so its example tables are checked "+
				"against nothing", path)
		}
	}
}

// The monitor-line check below closes the hole that let the one defect
// this gate's first check could not see through review: a `Monitor
// with:` line added to `database update`, a verb that calls no
// trackMutation and registers no --wait. The header check cannot help —
// the line is a fabricated string, but there is no table to compare.
//
// The invariant a reviewer found, and it is mechanical: trackMutation is
// the only caller that prints that line, and every command reaching it
// gets its wait flags from addWaitFlags. So a section showing the line
// MUST be a section whose GENERATED flag table declares --wait. The
// generated half is regenerated from the cobra tree by `make docs`,
// which makes it a truth source the hand-written half can be checked
// against — the two halves of every command's entry, cross-checked.
//
// One direction only. A section declaring --wait need NOT show the
// line: rotate-password, restore and postgrest deploy pass --wait in
// their examples and show the wait sequence instead, which is correct
// and must not be reported.

// monitorLinePrefix is what trackMutation prints in text mode when no
// --wait was passed. Kept as a prefix rather than a full format so a
// change to the command spelling after it still trips this check.
const monitorLinePrefix = "Monitor with:"

// referencePreamble is the key referenceSections gives the span BEFORE
// the first generated command heading. It is not a command path and so
// cannot collide with one.
//
// It exists because that span used to be discarded, and it is not a
// scrap: byoc's preamble is 234 of its 3844 lines and managed's is 653
// of 2270 — every hand-written section on what waiting proves, in the
// document this gate reads to check claims about waiting. A Monitor
// line pasted there was invisible. Review found it.
const referencePreamble = "(preamble, before the first ##### heading)"

// referenceSections splits a module reference on its generated command
// headings, returning each command's name and full text. The heading is
// the five-hash generated one, one level deeper than the hand-written
// prose heading of the same name — anchoring to the shallower one would
// merge each command with its neighbour.
//
// The span before the first such heading comes back under
// referencePreamble, so the map covers the whole document rather than
// only the part of it that carries generated blocks.
func referenceSections(content string) map[string]string {
	out := map[string]string{}
	name := referencePreamble
	var buf []string
	for _, line := range strings.Split(content, "\n") {
		if rest, ok := strings.CutPrefix(line, "##### "); ok {
			out[name] = strings.Join(buf, "\n")
			name, buf = strings.TrimSpace(rest), nil
			continue
		}
		buf = append(buf, line)
	}
	out[name] = strings.Join(buf, "\n")
	return out
}

// monitorLineDocs is every module this check reads, with the floor for
// its section count and whether it is EXPECTED to paste Monitor lines
// at all.
//
// TWO OF THE FOUR MODULES, and the scope is exact rather than
// convenient: `Monitor with:` is printed from exactly two places in
// the tree, internal/starfleet/{byoc,managed}/cmd/wait.go, so these
// are the only two modules whose commands can print it. What that
// leaves unread is a line pasted into a document belonging to a module
// that cannot print it — the index, starfleet's and controlplane's
// pages, and all five SKILL.md files. None contains the string today.
// Adding them needs a per-module floor and expectLines judgement, and
// a positive control for each, so it is stated here rather than half
// done.
//
// managed expects none, and that is a ruling rather than an omission:
// example output stays prose-first in managed and cp, so those
// references describe what a verb prints instead of pasting it. So the
// forward direction — a pasted line must belong to a --wait verb — has
// nothing to match there today, and the arm is the NEGATIVE direction:
// it fires if a line is pasted later.
//
// Two reasons that arm is weak, both named rather than left to be
// found. First, it can only catch an addition, so it pins nothing
// about the wait flags themselves — TestManagedWaitFlagsFollowTheCode
// in internal/clitest is what does that, from the call graph. Second,
// referenceSections keys on the command NAME, and a prose-first
// reference gives the same name to its generated heading and its
// hand-written one, so the two collapse and the later wins. The
// `| `--wait` |` table lookup would therefore be unreliable on managed
// even if a line were pasted, which is the other reason this arm
// asserts only the absence.
//
// The check runs per MODULE rather than per file. The split gave each
// resource its own page, so byoc's sections are spread across 16 files
// and managed's across 11; a single-file read found ONE section in
// each index and said so, which is the self-check below doing its job.
//
// The floors are the module's section count summed over its index and
// every page, each file's preamble included. Measured today: byoc 67
// across 16 files, managed 37 across 11 — both ROSE (from 31 and 21),
// because every page brings a preamble the single file did not have.
// The floors sit about a tenth under those, so deleting a resource
// does not trip them while the collapse this guards against — a read
// that reaches one file, or none — is nowhere near.
var monitorLineDocs = []struct {
	moduleDir    string
	sectionFloor int
	expectLines  bool
}{
	{"../../internal/starfleet/byoc", 60, true},
	{"../../internal/starfleet/managed", 33, false},
}

func TestMonitorLineOnlyWhereWaitIsDeclared(t *testing.T) {
	for _, d := range monitorLineDocs {
		t.Run(d.moduleDir, func(t *testing.T) {
			checkMonitorLines(t, d.moduleDir, d.sectionFloor,
				d.expectLines)
		})
	}
}

// monitorLineShown reports whether a section PASTES the Monitor line.
// Fenced blocks only: a section may legitimately discuss the line in
// prose — `database update`'s says it prints none — and scanning the
// whole section reported exactly the sentence documenting the correct
// behaviour. Found by this check firing on its own fix.
func monitorLineShown(text string) bool {
	for _, block := range fencedBlocks(text) {
		for _, line := range block.body {
			if strings.HasPrefix(strings.TrimSpace(line),
				monitorLinePrefix) {
				return true
			}
		}
	}
	return false
}

// monitorLineBacked reports whether a section carries the evidence that
// would justify pasting the line: a --wait row in its generated flag
// table. `make docs` writes that row from the cobra tree, which is what
// makes it evidence rather than another hand-written claim. The prose
// may mention --wait too, which is why the backticked table spelling is
// what gets matched.
//
// The preamble has no generated block at all, so nothing there can back
// a pasted line and a row appearing there would excuse itself.
func monitorLineBacked(name, text string) bool {
	return name != referencePreamble &&
		strings.Contains(text, "| `--wait` |")
}

// monitorLineSectionCounts returns each file's section count for a
// module, and the total. Keeping the sections per FILE is not
// bookkeeping: every page has its own preamble, so merging the maps
// would collapse them into one key and lose all but one page's
// unbacked-preamble check.
func monitorLineSectionCounts(
	t *testing.T, moduleDir string,
) (sections map[string]map[string]string, total int) {
	t.Helper()

	files := ReferenceFilesFor(moduleDir)
	if len(files) == 0 {
		t.Fatalf("no reference files for %s — the module derivation "+
			"is broken and this check has nothing to read", moduleDir)
	}

	out := make(map[string]map[string]string, len(files))
	total = 0
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		sections := referenceSections(string(content))
		out[f] = sections
		total += len(sections)
	}
	return out, total
}

func checkMonitorLines(
	t *testing.T, moduleDir string, sectionFloor int, expectLines bool,
) {
	t.Helper()

	perFile, total := monitorLineSectionCounts(t, moduleDir)
	if total < sectionFloor {
		t.Fatalf("only %d command sections found across %s — the "+
			"heading split has broken and this check would pass "+
			"without examining anything", total, moduleDir)
	}

	var withLine int
	for doc, sections := range perFile {
		for name, text := range sections {
			if !monitorLineShown(text) {
				continue
			}
			withLine++
			if monitorLineBacked(name, text) {
				continue
			}
			where := fmt.Sprintf("section %q shows", name)
			if name == referencePreamble {
				where = "the span before the first ##### heading shows"
			}
			reportUnbackedMonitorLine(t, doc, where)
		}
	}

	if expectLines && withLine == 0 {
		t.Fatalf("no section of %s shows a Monitor line — every "+
			"asynchronous byoc verb prints one, so the section split "+
			"or the prefix has broken", moduleDir)
	}
	if !expectLines && withLine > 0 {
		t.Errorf("%s pastes %d Monitor line(s), but this reference is "+
			"prose-first and describes output rather than "+
			"pasting it. Either the ruling changed — in which case set "+
			"expectLines and give this module a real forward check — "+
			"or the lines should be prose.", moduleDir, withLine)
	}
	t.Logf("checked %d section(s) showing a Monitor line, out of %d "+
		"sections across %d file(s)", withLine, total, len(perFile))
}

func reportUnbackedMonitorLine(t *testing.T, doc, where string) {
	t.Helper()
	t.Errorf("%s: %s a %q line, with no generated flag table "+
		"declaring --wait to back it.\n"+
		"Only trackMutation prints that line, and every command "+
		"reaching it registers addWaitFlags, so name the verb and "+
		"read its RunE: if it returns without calling "+
		"trackMutation, drop the line.",
		doc, where, monitorLinePrefix)
}

func TestMonitorLineCheckIgnoresProseMentions(t *testing.T) {
	// The regression this scoping fixed: `database update`'s prose says
	// the verb prints no Monitor line, and an unscoped search reported
	// the very sentence documenting the correct behaviour.
	if monitorLineShown(
		"Some prose that says: prints no Monitor with: line.\n") {
		t.Error("a prose mention of the Monitor line was read as " +
			"example output, so a section correctly documenting that a " +
			"verb prints none would be reported")
	}
	if !monitorLineShown("```\nMonitor with: pgedge x\n```\n") {
		t.Error("a pasted line in a fenced block was not seen, so the " +
			"scoping has gone too far and this gate sees nothing")
	}
}

func TestReferenceSectionsSplitsOnGeneratedHeadings(t *testing.T) {
	// Every command appears twice, and the generated heading is one
	// level deeper than the hand-written one. Splitting on the shallower
	// heading would fold each command into its neighbour and let a
	// sibling's --wait row excuse it.
	content := "#### pgedge starfleet byoc database\n" +
		"prose about the group\n" +
		"##### pgedge starfleet byoc database update\n" +
		"| `--display-name string` | No | x |\n" +
		"##### pgedge starfleet byoc database delete\n" +
		"| `--wait` | No | y |\n"
	got := referenceSections(content)
	if len(got) != 3 {
		t.Fatalf("split into %d sections, want 3 (two commands and the "+
			"preamble): %v", len(got), got)
	}
	if strings.Contains(got["pgedge starfleet byoc database update"], "--wait") {
		t.Error("update's section absorbed delete's --wait row, so a " +
			"fabricated Monitor line in update would be excused by its " +
			"neighbour")
	}
	// The preamble is the span this gate used to throw away, which is
	// 234 lines in byoc and 653 in managed.
	if !strings.Contains(got[referencePreamble], "prose about the group") {
		t.Errorf("the span before the first ##### heading was dropped: "+
			"%q", got[referencePreamble])
	}
	if strings.Contains(got[referencePreamble], "database update") {
		t.Error("the preamble absorbed the first command section")
	}
}

func TestMonitorLineCheckReadsThePreamble(t *testing.T) {
	// The defect the span fix closes: a Monitor line pasted before the
	// first generated heading. Nothing there can back it, so it must be
	// reported rather than dropped with the span.
	content := "# Reference\n\n```\nMonitor with: pgedge starfleet byoc " +
		"task list --subject-id <id>\n```\n\n" +
		"##### pgedge starfleet byoc database delete\n\n" +
		"| `--wait` | No | y |\n"
	sections := referenceSections(content)
	pre, ok := sections[referencePreamble]
	if !ok {
		t.Fatal("no preamble section, so this test proves nothing")
	}
	if !monitorLineShown(pre) {
		t.Fatal("the pasted line is not in the preamble span, so the " +
			"split is wrong and a pass would prove nothing")
	}
	if monitorLineBacked(referencePreamble, pre) {
		t.Error("the preamble was treated as backed, so a line pasted " +
			"before the first ##### heading is still invisible")
	}
	// The command section below it is the control: the same line there
	// IS backed, and must stay excused.
	cmd := sections["pgedge starfleet byoc database delete"]
	if !monitorLineBacked("pgedge starfleet byoc database delete", cmd) {
		t.Error("a command section with a generated --wait row was not " +
			"treated as backed, which would report every correct byoc " +
			"section")
	}
}
