package clitest

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// The gate on shipped examples reads a command's cobra Long text and
// says of everything else: "a different population". Part of that
// population is the command a document names mid-sentence, in
// backticks, and #251 lived exactly there — the controlplane reference told a
// user whose create had just failed to run
// `pgedge controlplane task get <task_id>`, which exits 2 for want of a scope
// flag. The working form sat 880 lines further down the same file, and
// nothing compared either against the command tree.
//
// So this is the same rule (exit 2 means the command was malformed,
// and no documented command may be) over the inline population: the
// documents the recipe gates already walk, which is 53 today — 48
// reference documents since the resource split, plus five SKILL.md.
// Eight of them carry a runnable mention and the rest carry none, so
// this covers a population rather than every document — the count it
// logs is the honest statement of reach.
//
// WHAT IT DOES NOT READ, so nobody reads more into a pass:
//
//   - a fragment. Only a mention carrying an <angle_placeholder> is
//     run, because that is what makes it a command a reader
//     substitutes into rather than a name being discussed
//     (`pgedge controlplane doctor`, `pgedge controlplane task get`).
//   - a mention needing a shell to mean what it says — a pipe, a
//     redirect, `&&`. This runs argv, and the recipe gates cover
//     those.
//   - a FENCED block. Those are a third population, and running them
//     as argv needs an elision filter this does not have: the byoc and
//     managed documents deliberately write `cluster create ... --wait`
//     and column-aligned verb lists, which are not invocations.
//   - whether the command is SENSIBLE, or whether the sentence around
//     it is true. It proves the command parses.
func TestInlineDocumentedCommandsAreNotMalformed(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	spent := map[string]bool{}
	roots := rootCommandNames(root)
	contributing := map[string]bool{}
	var checked, skipped int
	for _, doc := range shippedRecipeDocs(t) {
		for _, m := range inlineCommandsIn(t, doc, roots) {
			if hasShellSyntax(m) || !anglePlaceholderRe.MatchString(m) {
				skipped++
				continue
			}
			args, ok := tokenize(m)
			if !ok || len(args) == 0 ||
				reachesTheNetworkFromRoot(args) {
				skipped++
				continue
			}
			resolved, rest, ferr := root.Find(args)
			if ferr != nil || resolved == root {
				skipped++
				continue
			}
			// A placeholder where cobra wants a SUBCOMMAND stands for
			// one of an enumerated set of words, not for an id, and
			// substitutePlaceholders has only a UUID to offer: it
			// turns `completion <shell>` into an unknown command. That
			// is the harness, not the document. Leaves #251's shape
			// checked, because `task get <task_id>` resolves to a leaf.
			if resolved.HasAvailableSubCommands() && len(rest) > 0 &&
				anglePlaceholderRe.MatchString(rest[0]) {
				skipped++
				continue
			}
			if isSentenceBreak(m) {
				skipped++
				continue
			}
			if _, ok := exitTwoOnPurpose[m]; ok {
				spent[m] = true
				skipped++
				continue
			}
			checked++
			contributing[doc] = true
			t.Run(m, func(t *testing.T) {
				err := runForError(t, substitutePlaceholders(args)...)
				if cli.ExitCode(err) != cli.ExitUsage {
					return
				}
				if why := environmental(err); why != "" {
					t.Logf("exit 2 for an environmental reason (%s), "+
						"not because the command is malformed", why)
					return
				}
				t.Errorf("%s documents this command inline and it "+
					"exits 2 — it is malformed, and a reader meets it "+
					"at the moment they needed it to work:\n  %s\n  %v",
					doc, m, err)
			})
		}
	}

	// Positive control: a walk that examined nothing would pass. 20
	// mentions run today, from eight of the 53 documents; the floor is
	// set just under that, because the failure this guards against is
	// an extractor that stops matching, and that shows up as a
	// collapse rather than a small drift. It was 18 from six of ten
	// before the split, and the rise is the pages coming into reach —
	// the walk that missed them reported 11. The index concision pass
	// (2026-09-23) cut prose carrying one, leaving 14.
	if checked < 12 {
		t.Fatalf("only %d inline commands were run (%d skipped); the "+
			"extractor is not reaching the documents", checked, skipped)
	}
	t.Logf("ran %d inline commands from %d of %d documents, skipped %d",
		checked, len(contributing), len(shippedRecipeDocs(t)), skipped)

	for m := range exitTwoOnPurpose {
		if !spent[m] {
			t.Errorf("exitTwoOnPurpose excuses %q, which no document "+
				"mentions inline any more; delete the entry", m)
		}
	}
}

// exitTwoOnPurpose excuses a mention that is documented BECAUSE it
// exits 2. The index's exit-code section teaches the codes by naming
// the shape that produces each one, so there the usage error is the
// subject rather than a defect. Keyed on the exact text, and an entry
// that stops matching is reported by the test above — the same staleness rule
// pipelineAllowances lives under.
var exitTwoOnPurpose = map[string]string{
	"help <group> <typo>": "the sentence around it documents " +
		"exit 2 as what a typo'd help path returns",
}

// inlineCommandRe matches any backticked span. The `pgedge ` prefix is
// NOT part of the pattern, and that is the whole point: these
// documents write `controlplane task get --database my-db <task_id>` as readily
// as they write it out in full, so keying on the prefix left 26
// placeholder-carrying mentions across six documents invisible —
// including controlplane's own `task cancel` line and, in the very file #251
// was filed against, `database create <id> -f spec.yaml`. #251's
// defect written without the prefix would have passed.
//
// What replaces the prefix as the filter is rootCommandNames below: a
// mention has to start at a command the tree actually declares.
var inlineCommandRe = regexp.MustCompile("`([^`]+)`")

// rootCommandNames returns the name and every alias of each command
// directly under the root, which is the set a runnable mention can
// start with. Derived from the tree rather than listed, so a module
// added tomorrow is covered without an edit here.
//
// `help` is NOT excluded, unlike in the walks that enumerate commands
// to document: the index documents `pgedge help <group> <typo>` as a
// command with a stated exit code, so it is text a reader runs.
// Excluding it would have hidden that mention instead of excusing it,
// and exitTwoOnPurpose is where the reason belongs.
func rootCommandNames(root *cobra.Command) map[string]bool {
	names := map[string]bool{"help": true}
	for _, c := range root.Commands() {
		if c.Hidden {
			continue
		}
		names[c.Name()] = true
		for _, a := range c.Aliases {
			names[a] = true
		}
	}
	return names
}

// inlineCommandsIn returns the backticked mentions in doc that a
// reader is meant to run, whitespace-collapsed and with any `pgedge`
// prefix stripped, so every mention arrives as argv.
//
// GENERATED blocks are excluded: their `pgedge controlplane task get <task_id>
// [flags]` is a usage SIGNATURE rendered from cobra, so running it
// would report the tree against itself and every group's
// `pgedge controlplane <command>` line as a defect.
func inlineCommandsIn(
	t *testing.T, doc string, roots map[string]bool,
) []string {
	t.Helper()

	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("reading %s: %v", doc, err)
	}
	var out []string
	generated := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.Contains(line, "BEGIN GENERATED"):
			generated = true
			continue
		case strings.Contains(line, "END GENERATED"):
			generated = false
			continue
		case generated:
			continue
		}
		for _, m := range inlineCommandRe.FindAllStringSubmatch(line, -1) {
			fields := strings.Fields(m[1])
			if len(fields) > 0 && fields[0] == "pgedge" {
				fields = fields[1:]
			}
			if len(fields) == 0 || !roots[fields[0]] {
				continue
			}
			cmd := strings.Join(fields, " ")
			if strings.Contains(cmd, "[flags]") ||
				strings.Contains(cmd, "<command>") {
				continue
			}
			out = append(out, cmd)
		}
	}
	return out
}

// reachesTheNetworkFromRoot is reachesTheNetwork for argv that has
// already had its `pgedge` stripped: `doctor` is the whole hazard, and
// it is the first token here rather than the second.
func reachesTheNetworkFromRoot(args []string) bool {
	return len(args) > 0 && args[0] == "doctor"
}

// TestTheInlineGateCatchesAMissingScopeFlag is the guard on the guard.
// The check above passes if nothing reaches its run step, and #251's
// own shape is the cheapest proof that something does.
func TestTheInlineGateCatchesAMissingScopeFlag(t *testing.T) {
	broken := substitutePlaceholders(
		[]string{"controlplane", "task", "get", "<task_id>"})
	if got := cli.ExitCode(runForError(t, broken...)); got != cli.ExitUsage {
		t.Errorf("exit = %d, want %d: #251's form must still be "+
			"detectable, or this gate has stopped covering the defect "+
			"it exists for", got, cli.ExitUsage)
	}
	fixed := substitutePlaceholders(
		[]string{"controlplane", "task", "get", "--database", "<database_id>",
			"<task_id>"})
	if got := cli.ExitCode(runForError(t, fixed...)); got == cli.ExitUsage {
		t.Errorf("the documented form was reported as malformed; the " +
			"gate would false-positive on the fix")
	}
}
