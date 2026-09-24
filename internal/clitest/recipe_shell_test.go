package clitest

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// Nothing else reads the shell or the procedures we ship to agents:
// the reference gates walk the cobra tree and the GENERATED blocks,
// and the example-output gate compares a line that is WRONG against
// real behaviour. Every #286 idiom was valid shell that ran and
// exited 0, so none of them could see it (#297).
//
// These rules are deliberately narrow, and a rule with false
// positives earns an allowance — which is how a gate rots into the
// blanket amnesty the doc-gate markers were built to replace.

// recipeSkillFloor is asserted so a broken walk cannot pass as a clean
// sweep. Five SKILL.md ship today.
//
// The reference half no longer walks or globs. Twice, two separate
// people globbed `internal/*/llms.txt` and missed the NESTED
// internal/starfleet/{byoc,managed}/llms.txt; a walk keyed on the name
// llms.txt then missed every resource page the split created, which is
// how three pipelineAllowances entries went stale in a single commit.
// ReferenceFiles derives the set from the modules, so neither can
// recur.
const recipeSkillFloor = 5

// recipeReferenceFloor is the same self-check for the derived half: a
// Documents() implementation that returns nothing would hand this an
// empty set. One index plus 47 module documents ship today.
const recipeReferenceFloor = 40

// shippedRecipeDocs returns every document an AI agent reads for
// commands: every reference page and the bundled skills.
func shippedRecipeDocs(t *testing.T) []string {
	t.Helper()

	const root = "../.."
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("walk root is not the repo root: %v", err)
	}

	docs := ReferenceFiles()
	if len(docs) < recipeReferenceFloor {
		t.Fatalf("found %d reference documents, expected at least %d — "+
			"the module derivation is broken, and an empty sweep looks "+
			"exactly like a clean one: %v", len(docs),
			recipeReferenceFloor, docs)
	}

	var skills int
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".superpowers", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "SKILL.md" &&
			strings.Contains(filepath.ToSlash(p), "/skills/") {
			docs = append(docs, p)
			skills++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if skills < recipeSkillFloor {
		t.Fatalf("found %d SKILL.md files, expected at least %d — "+
			"the walk is broken: %v", skills, recipeSkillFloor, docs)
	}
	return docs
}

// fencedBlock is one fenced code block, carrying the line number of
// its opening fence so a failure can be opened in an editor.
type recipeBlock struct {
	doc   string
	info  string
	start int
	lines []string
}

// The info string is matched as ANYTHING, not as a restricted
// character class. A fence the regex cannot match does not merely go
// unclassified — it inverts fence polarity for the rest of the file,
// so the NEXT real block parses as body text and both go unchecked.
// ```bash title="x" was enough to do it. Classification takes the
// first word (see recipeBlock.lang).
var recipeFenceRe = regexp.MustCompile("^( {0,3})(```+|~~~+)[ \t]*(.*?)[ \t]*$")

// fencedBlocksIn extracts every fenced block. It handles ``` and ~~~
// and any info string, because a block that the extractor cannot see
// is a block the gate cannot check, which is the failure this whole
// file exists to prevent.
func recipeBlocksIn(t *testing.T, path string) []recipeBlock {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	lines := strings.Split(string(raw), "\n")

	var blocks []recipeBlock
	for i := 0; i < len(lines); i++ {
		m := recipeFenceRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		open, info := m[2], m[3]
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			c := recipeFenceRe.FindStringSubmatch(lines[j])
			if len(c) == 4 && c[2][0] == open[0] &&
				len(c[2]) >= len(open) && c[3] == "" {
				break
			}
			body = append(body, lines[j])
		}
		blocks = append(blocks, recipeBlock{
			doc: path, info: info, start: i + 1, lines: body,
		})
		i = j
	}
	return blocks
}

// isShell reports whether a block is shell an agent would run.
//
// An EMPTY info string counts. Restricting this to `bash` looked
// reasonable and was the gate's worst hole: 31 of the blocks
// containing `pgedge` carry no info string, every numbered-step
// workflow in the five SKILL.md files is one of them, and so is
// Workflow 6 of the controlplane skill — which is one of #286's own eight
// sites. The gate could be handed that exact defect back and stay
// green. A document's fences are labelled by whoever wrote them, so
// the label cannot decide what gets checked.
//
// json/yaml/text are excluded because they are data, and a `pgedge`
// line inside them is being quoted rather than run.
// lang is the info string's first word: ```bash title="x" is bash.
func (b recipeBlock) lang() string {
	f := strings.Fields(b.info)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

func (b recipeBlock) isShell() bool {
	switch b.lang() {
	case "", "bash", "sh", "shell", "console":
		return true
	}
	return false
}

// logicalLines joins backslash continuations and drops comments, so a
// rule sees the command an agent runs rather than how it was wrapped
// to fit the page. A `#` inside quotes is not a comment.
func logicalLines(lines []string) []string {
	var out []string
	var cur string
	for _, l := range lines {
		cur += stripComment(l)
		if strings.HasSuffix(strings.TrimRight(l, " \t"), "\\") {
			cur = strings.TrimRight(strings.TrimRight(cur, " \t"), "\\")
			cur += " "
			continue
		}
		if s := strings.TrimSpace(cur); s != "" {
			out = append(out, s)
		}
		cur = ""
	}
	if s := strings.TrimSpace(cur); s != "" {
		out = append(out, s)
	}
	return out
}

func stripComment(l string) string {
	var sq, dq bool
	for i, r := range l {
		switch r {
		case '\'':
			if !dq {
				sq = !sq
			}
		case '"':
			if !sq {
				dq = !dq
			}
		case '#':
			if !sq && !dq && (i == 0 || l[i-1] == ' ' || l[i-1] == '\t') {
				return l[:i]
			}
		}
	}
	return l
}

// placeholderRe matches a doc placeholder like `<public|private>`.
// The `|` inside one is an OR between the values a reader may
// substitute, not a pipe, and reading it as a pipe reported
// `cluster create --node-location <public|private>` as a recipe that
// discards a status. Only spanless placeholders qualify, so a real
// `< file` redirect cannot be swallowed by this.
var placeholderRe = regexp.MustCompile(`<[^<>\s]*\|[^<>\s]*>`)

// alternationRe matches a bare `start|stop|restart`. These documents
// write a set of sibling verbs that way, and `pgedge controlplane database
// instance start|stop|restart storefront` is not three pipeline
// stages.
//
// A first attempt keyed this on there being no whitespace around the
// `|`, which was an observation about today's formatting dressed up
// as a property — and it silently regressed the primary rule, because
// `pgedge … -o json|jq -r .id` is #286's own shape with two spaces
// deleted. Spacing cannot tell the two apart. What can is whether the
// thing after the pipe is a COMMAND, so the mask is skipped whenever
// a filter follows.
var alternationRe = regexp.MustCompile(
	`[A-Za-z0-9_.-]+(?:\|[A-Za-z0-9_.-]+)+`)

// filterCmdRe names what a pipeline in these documents actually pipes
// into. A `|` followed by one of these is a pipeline whatever the
// spacing; a `|` followed by anything else, inside a single
// unspaced token, is an alternation.
var filterCmdRe = regexp.MustCompile(
	`\|\s*(jq|grep|sed|awk|head|tail|tee|sort|uniq|cut|tr|wc|xargs|` +
		`column|less|python3?|sh|bash|while|read|pgedge)\b`)

const pipeSentinel = "\x00"

// maskPlaceholders hides the pipes inside doc placeholders so the
// pipeline splitter cannot see them.
func maskPlaceholders(l string) string {
	mask := func(m string) string {
		return strings.ReplaceAll(m, "|", pipeSentinel)
	}
	masked := placeholderRe.ReplaceAllStringFunc(l, mask)
	return alternationRe.ReplaceAllStringFunc(masked, func(m string) string {
		if filterCmdRe.MatchString(m) {
			return m
		}
		return mask(m)
	})
}

// splitPipeline splits on `|` outside quotes, leaving `||` alone.
// tableRowRe matches a markdown table row. Unlabelled fences hold
// prose as well as commands, and a row's `|` separators are not
// pipes. maskPlaceholders cannot reach these — it only masks an
// unspaced `a|b`, and a table row is spaced — so the row has to be
// recognised for what it is.
var tableRowRe = regexp.MustCompile(`^\s*\|.*\|\s*$`)

func splitPipeline(l string) []string {
	if tableRowRe.MatchString(l) {
		return []string{l}
	}
	l = maskPlaceholders(l)
	var sq, dq bool
	var stages []string
	var cur strings.Builder
	for i := 0; i < len(l); i++ {
		c := l[i]
		// A backslash escapes the next byte inside double quotes, so
		// `get "a\" b" | jq .` does not leave the splitter believing
		// it is still inside a string — which used to swallow the
		// pipe and miss the finding.
		if c == '\\' && dq && i+1 < len(l) {
			cur.WriteByte(c)
			i++
			cur.WriteByte(l[i])
			continue
		}
		switch c {
		case '\'':
			if !dq {
				sq = !sq
			}
		case '"':
			if !sq {
				dq = !dq
			}
		case '|':
			if !sq && !dq {
				if i+1 < len(l) && l[i+1] == '|' {
					cur.WriteString("||")
					i++
					continue
				}
				stages = append(stages, cur.String())
				cur.Reset()
				continue
			}
		}
		cur.WriteByte(c)
	}
	stages = append(stages, cur.String())
	for i := range stages {
		stages[i] = strings.ReplaceAll(stages[i], pipeSentinel, "|")
	}
	return stages
}

var pgedgeWordRe = regexp.MustCompile(`(^|[^\w./-])pgedge\s`)

func mentionsPgedge(s string) bool { return pgedgeWordRe.MatchString(s) }

// pipelineAllowances excuses one shipped recipe each, by the exact
// logical line. A pipeline DOES discard the upstream status, so these
// are not false alarms in the abstract — each is safe only because
// the CONSUMER refuses the empty stdin a failed producer leaves, and
// that is a property of the consumer, not of the pipeline.
//
// Measured 2026-08-19 with an unreachable Control Plane: empty stdin
// into `controlplane database create db -f -` exits 2, "the spec read from
// stdin does not contain a database spec", client-side and before any
// request; `controlplane database restore db -f -` exits 2 the same way. So a
// failed producer cannot make either of these write.
//
// Anything added here needs that measurement, not an argument.
var pipelineAllowances = map[string]string{
	"pgedge controlplane database init --nodes 3 | pgedge controlplane database create db -f -":       "empty stdin makes `create -f -` exit 2 client-side",
	"pgedge controlplane database init -i -o json | pgedge controlplane database create db -f -":      "empty stdin makes `create -f -` exit 2 client-side",
	"pgedge controlplane database restore template -i | pgedge controlplane database restore db -f -": "empty stdin makes `restore -f -` exit 2 client-side",
}

// TestShippedRecipesDoNotDiscardAPgedgeStatus is the #286 class as a
// rule: in shell we ship, a pgedge call's exit status must reach the
// reader rather than being swallowed by something whose own status
// says nothing about whether the CLI succeeded.
func TestShippedRecipesDoNotDiscardAPgedgeStatus(t *testing.T) {
	spent := map[string]bool{}

	for _, doc := range shippedRecipeDocs(t) {
		for _, b := range recipeBlocksIn(t, doc) {
			if !b.isShell() {
				continue
			}
			ll := logicalLines(b.lines)
			for _, l := range ll {
				stages := splitPipeline(l)
				for i, s := range stages[:max(0, len(stages)-1)] {
					if !mentionsPgedge(s) {
						continue
					}
					if _, ok := pipelineAllowances[l]; ok {
						spent[l] = true
						continue
					}
					t.Errorf("%s:%d: stage %d of a pipeline is a "+
						"pgedge call, so the pipeline reports the "+
						"LAST command's status and not the CLI's:\n"+
						"  %s\nRedirect to a file, test the status, "+
						"then read the file — or add a "+
						"pipelineAllowances entry with the "+
						"measurement that shows the consumer refuses "+
						"a failed producer's empty output.",
						b.doc, b.start, i+1, l)
				}

				if capturesPgedgeUnchecked(l, ll) {
					t.Errorf("%s:%d: a pgedge call is captured in a "+
						"command substitution and its value used "+
						"with no status or non-empty test:\n  %s\n"+
						"A failed read prints nothing on stdout, so "+
						"the substitution yields the empty string "+
						"and the line runs anyway.", b.doc, b.start, l)
				}

				if branchesOnPgedgeWithoutStopping(l, ll) {
					t.Errorf("%s:%d: a pgedge call is the condition "+
						"of %q and no branch in the block stops:\n"+
						"  %s\nA failed read then selects a branch "+
						"on its own, which is how #286's recipes "+
						"deployed a second service off a read that "+
						"404'd. Report the failure and exit, or "+
						"`case $?` on the one code that means "+
						"\"absent\".", b.doc, b.start,
						strings.Fields(l)[0], l)
				}

				if listAndConnective(l) {
					t.Errorf("%s:%d: a pgedge call sits on the left "+
						"of && or ||, so its status selects what "+
						"runs next without being reported:\n  %s",
						b.doc, b.start, l)
				}
			}
		}
	}

	for l := range pipelineAllowances {
		if !spent[l] {
			t.Errorf("pipelineAllowances excuses a line that no "+
				"longer appears in any shipped recipe; delete it:\n"+
				"  %s", l)
		}
	}
}

// `$( )` anywhere, and a backtick capture only in ASSIGNMENT context.
//
// Matching bare backticks reported ordinary markdown inline code —
// these documents write `pgedge starfleet auth login` as a command NAME
// 367 times. Abandoning backticks entirely was the first answer and
// it was too crude: it let a real `ID=` + backtick capture through.
// The discriminator is the assignment, not the backtick. Measured:
// 367 backticked `pgedge` spans in the corpus, ZERO of them preceded
// by `NAME=`, so this costs no false positives at all.
var captureRe = regexp.MustCompile(
	`\$\(\s*pgedge\s` + "|^[A-Za-z_][A-Za-z0-9_]*=\\s*`\\s*pgedge\\s")

// Both clears scan the block's LOGICAL lines, so a comment cannot
// grant them — `# return here later` used to clear every `if pgedge`
// finding in its block.
//
// `test -z` counts alongside `[ -z`, and `&& exit` alongside
// `|| exit`, with commands allowed to intervene, because
// `test -z "$ID" && exit 1` and `[ "$ID" ] || { echo …; exit 1; }`
// are both ordinary guards that an earlier draft reported as defects.
// A first attempt excluded `&` between the connective and the exit,
// which the `>&2` in the second one trips.
//
// The two connective alternatives narrow the hole rather than closing
// it, and it is worth being exact about which. The first cannot cross
// a quote, so `… || echo "cannot list; you may exit and retry"` no
// longer clears a finding on the strength of the WORD exit inside a
// message. The second does cross quotes and only wants the exit at a
// command position, so a message reading `…; exit …` still clears —
// narrowed from "exit anywhere within 60 characters" to "exit after a
// `;` or `{` within 80". Excluding quotes there too would break the
// brace guard, whose `;` sits inside its braces.
//
// `-z` and `-n` require test-bracket context. Bare `-z\s`/`-n\s`
// matched `echo -n "using $ID"` and even `jq -r 'select(.a -z b)'`,
// either of which cleared every capture finding in the block. Bare
// `exit 1` is gone for the same reason: an unrelated recipe's exit in
// the same fence is not this capture's guard.
var guardRe = regexp.MustCompile(
	`\$\?|\[\[?\s+-[zn]\s|\btest\s+-[zn]\s|\bif\s+!\s|` +
		`\bset\s+-[a-z]*e|[|&]{2}[^"']{0,60}?\b(exit|return)\b|` +
		`[|&]{2}.{0,80}?[;{]\s*(exit|return)\b`)

// capturesPgedgeUnchecked flags `$(pgedge …)` used inline, and an
// assignment whose block never tests the captured value. The inline
// form is always wrong: there is no variable left to test.
func capturesPgedgeUnchecked(l string, block []string) bool {
	if !captureRe.MatchString(l) {
		return false
	}
	assign := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`).MatchString(l)
	if !assign {
		return true
	}
	for _, other := range block {
		if guardRe.MatchString(other) {
			return false
		}
	}
	return true
}

// constructRe matches the rest of a shell conditional or loop. A cap
// on the shapes it accepts silently narrows the rule: matching only a
// bare `then`/`fi` line lost `while`/`until` entirely, because a loop
// has `do`/`done` and never a `then`, and lost every one-line
// conditional too. `conditionRe` advertises while and until, so that
// was two thirds of the rule's declared scope made unreachable.
var constructRe = regexp.MustCompile(
	`(?:^|;\s*)(?:then|do)\b|(?:^|;\s*)(?:fi|done)\s*$`)

func blockHasThen(block []string) bool {
	for _, l := range block {
		if constructRe.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

var conditionRe = regexp.MustCompile(`^(if|while|until)\s`)

// stopRe matches a branch that hands the failure back to the caller
// rather than deciding what to do about it.
var stopRe = regexp.MustCompile(`\bexit\s|\breturn\b|\bcase\s+\$\?`)

// branchesOnPgedgeWithoutStopping flags `if pgedge …` where nothing in
// the block stops on the failure, so the else branch runs on a read
// that never happened. This is #286's third shape, the one with no
// pipeline and no `jq` in it, which is why the first sweep's grep
// could not see it.
//
// The scan is block-scoped rather than scoped to the matching `fi`.
// These blocks are a handful of lines and self-contained, and the
// looser scope only ever excuses — it never invents a violation.
func branchesOnPgedgeWithoutStopping(l string, block []string) bool {
	if !conditionRe.MatchString(l) {
		return false
	}
	// A shell `if` has a `then`. Unlabelled fences in this corpus hold
	// prose as well as commands, and an English sentence opening with
	// a lowercase "if" and naming a `pgedge` read is not a
	// conditional. Requiring the block to contain the rest of the
	// construct separates the two without guessing.
	if !blockHasThen(block) {
		return false
	}
	head := l
	if i := strings.Index(l, ";"); i >= 0 {
		head = l[:i]
	}
	if !mentionsPgedge(head) {
		return false
	}
	for _, other := range block {
		if stopRe.MatchString(other) {
			return false
		}
	}
	return true
}

var connectiveRe = regexp.MustCompile(`&&|\|\|`)

func listAndConnective(l string) bool {
	if !connectiveRe.MatchString(l) {
		return false
	}
	loc := connectiveRe.FindStringIndex(l)
	return mentionsPgedge(l[:loc[0]])
}

// emptinessRe matches a branch keyed on there being no results, which
// is the condition a failed read is indistinguishable from.
// The vocabulary is drawn from what the documents actually say. Every
// term here was written by someone describing "the read came back
// with nothing" — and the list has already been short once: it lacked
// `none` on its own, so "If none matches, create one:" in the byoc
// reference escaped a rule built to catch exactly that sentence.
var emptinessRe = regexp.MustCompile(
	`(?i)empty|\bnone\b|not found|no results|nothing|does not exist|absent|no \w+ (yet|found|exists|match)`)

// mutatingVerbRe names the pgedge verbs that make or destroy
// something. A branch that only reports is not the hazard.
// The list is drawn from the shipped command tree, not from what the
// current defects happened to use: it was eleven verbs and missed
// `controlplane cluster init`, which is the controlplane skill's own Workflow 1 shape and
// creates a cluster. `backup` is deliberately absent — `backup list`
// and `backup get` are reads whose NAME carries a write verb, and the
// write is `backup create`, which `create` already covers.
var mutatingVerbRe = regexp.MustCompile(
	`pgedge\s+[\w\s-]*\b(create|delete|deploy|update|restart|register|` +
		`rotate-password|restore|resize|revoke|remove|accept|` +
		`failover|switchover|deregister|upgrade|install|uninstall)\b` +
		`|pgedge\s+[\w\s-]*\b(init|join|cancel|start|stop|set)($|[^\w-])`)

// The half that is not shell. The references write their procedures
// as free prose under `**Step N:**` headings rather than as steps in
// a fence, so a structural check cannot see them — which is how two
// defects sat in internal/starfleet/byoc/llms.txt through four sweeps,
// including the one that fixed their twins in the skill.
//
// So this ignores structure and scans the raw document: a read, then
// a branch keyed on the result being empty, then a write on that
// branch, with no branch keyed on the read's own status.

// recipeProximityWindow is how far after a read a write still counts
// as the same procedure. The two sites in byoc/llms.txt were 9 and 6
// lines apart. 40 is wide enough to span a fenced command block and
// the prose either side of it, and narrow enough that two unrelated
// procedures on one page are not welded together.
//
// This is the rule's one soft edge: a read and a write separated by
// more than this, with the branch between them, would pass.
const recipeProximityWindow = 40

// branchLineRe matches a line that poses a condition to the reader.
// The references write these as prose ("If none matches, create
// one:") and the skills as arrows ("→ none found:").
// The marker does not have to open the line. Anchoring it at ^\s*
// meant "Create one when the result comes back with nothing:" — a
// perfectly ordinary way to write the same defect — walked through.
var branchLineRe = regexp.MustCompile(
	`(?i)^\s*(→|->)|\b(if|when|unless|otherwise|once)\b`)

// sectionBreakRe matches the end of a procedure: a horizontal rule, a
// markdown heading, or the start of a generator-owned block.
var sectionBreakRe = regexp.MustCompile(`^(---+\s*|#{1,6} .*|<!-- BEGIN GENERATED.*)$`)

var readVerbRe = regexp.MustCompile(`pgedge\s+[\w\s-]*\b(list|get|status)\b`)

// statusSignalRe matches a branch keyed on the read's own result. The
// stderr clause is here because an agent in a harness that hides exit
// codes still sees the CLI's own `Error:` line.
// `error:` was here and is deliberately gone. It matched ANYWHERE in
// the span before the branch, and these documents quote `Error:`
// lines constantly — one unrelated sentence quoting a CLI error
// cleared three of four real findings. It was also dead: every real
// guard in the tree says "non-zero exit" and "on stderr" as well, so
// removing it changes no verdict on the shipped corpus.
var statusSignalRe = regexp.MustCompile(
	`(?i)non-zero|nonzero|exit code|exits? \d|\$\?|check the exit|on stderr`)

func TestShippedProceduresDoNotWriteOffAnUncheckedRead(t *testing.T) {
	for _, doc := range shippedRecipeDocs(t) {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		lines := strings.Split(string(raw), "\n")

		for i, l := range lines {
			if !readVerbRe.MatchString(l) {
				continue
			}
			end := min(i+recipeProximityWindow, len(lines))
			window := lines[i+1 : end]
			// A procedure does not run past a section break. Without
			// this the window welds the tail of one command's example
			// onto the head of the next command's GENERATED block,
			// which is how the first draft reported a `client get`
			// example as writing off `client create`.
			for k, w := range window {
				if sectionBreakRe.MatchString(w) {
					window = window[:k]
					break
				}
			}
			// The emptiness must be a BRANCH on this read, not any
			// mention of the word. Without that the rule fired on
			// "an empty array is rejected" — prose about what the
			// API validates in a later step — and on a `service
			// list` that captures an id under a workflow which had
			// already decided to create. The defect shape is
			// specifically: read, then a branch keyed on the result
			// being empty, then a write ON that branch.
			branch := -1
			for k, w := range window {
				// Stop at the next READ rather than at a fixed
				// distance. A distance cap was calibrated against the
				// pre-fix corpus, where guards are absent and the
				// prose is short; on the tree that actually ships,
				// eight of ten sites have their branch at or beyond
				// eight lines, because #300's own guard paragraph sits
				// between the read and the branch. So the cap, not the
				// guard, decided whether the rule could see a site —
				// and it hid a degraded Workflow 5 completely. The
				// next read is the real boundary: a branch after it
				// tests THAT read, not this one.
				if k > 0 && readVerbRe.MatchString(w) {
					break
				}
				if branchLineRe.MatchString(w) &&
					emptinessRe.MatchString(w) {
					branch = k
					break
				}
			}
			if branch < 0 {
				continue
			}
			if !mutatingVerbRe.MatchString(
				strings.Join(window[branch:], "\n")) {
				continue
			}
			if statusSignalRe.MatchString(
				strings.Join(window[:branch+1], "\n")) {
				continue
			}
			t.Errorf("%s:%d: this read is followed within %d lines by "+
				"a write, with a branch keyed on the result being "+
				"empty and none keyed on the read's own status:\n"+
				"  read:  %s\nA failed read prints nothing on "+
				"stdout, so the emptiness branch is also what "+
				"failure looks like, and the write runs off a read "+
				"that never happened.",
				doc, i+1, recipeProximityWindow, strings.TrimSpace(l))
		}
	}
}

// TestPipelineAllowancesStillEarnThemselves makes the allowances
// defend themselves.
//
// Each entry in pipelineAllowances excuses a real hazard — a pipeline
// DOES discard the producer's status — on the grounds that the
// consumer refuses the empty stdin a failed producer leaves. That
// reason lived only in a comment, so relaxing `-f -` validation would
// have made all three allowances wrong with nothing failing.
//
// The commands run in process against a zero-value Runtime, so they
// reach the spec check and stop there. No server is involved, and
// exit 2 is what proves the check fired BEFORE any request.
func TestPipelineAllowancesStillEarnThemselves(t *testing.T) {
	// The MESSAGE, not just the code. cli.ExitCode maps every
	// UsageError to 2, so a required-flag check added ahead of the
	// spec read would satisfy an exit-code-only assertion while the
	// property this allowance rests on — that the SPEC check refuses
	// empty stdin — had quietly stopped being tested. Demonstrated:
	// that mutation left an exit-2-only version of this test green.
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"database create", []string{
			"controlplane", "database", "create", "db", "-f", "-",
		}, "does not contain a database spec"},
		{"database restore", []string{
			"controlplane", "database", "restore", "db", "-f", "-",
		}, "does not contain a restore spec"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			rt := &module.Runtime{Stdin: strings.NewReader("")}
			root := cli.NewRootCmd(rt)
			for _, m := range Modules() {
				c, err := m.Command(rt)
				if err != nil {
					t.Fatal(err)
				}
				root.AddCommand(c)
			}
			root.SetArgs(tc.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)

			err := root.Execute()
			got := cli.ExitCode(err)
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if got != 2 || !strings.Contains(msg, tc.want) {
				t.Errorf("empty stdin into `pgedge %s` exited %d with "+
					"%q; want exit 2 and a message containing %q.\n"+
					"The pipelineAllowances entries are excused ONLY "+
					"because the SPEC check refuses empty stdin "+
					"client-side, before any request. If this verb "+
					"now accepts an empty spec, or refuses it for "+
					"some other reason, a failed producer in "+
					"`pgedge … | pgedge … -f -` can make it write and "+
					"those allowances must go.",
					strings.Join(tc.args, " "), got, msg, tc.want)
			}
		})
	}
}
