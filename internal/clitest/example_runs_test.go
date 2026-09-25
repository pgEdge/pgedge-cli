package clitest

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// A shipped example must not be MALFORMED. Copying one is the likeliest
// way anyone meets an error path at all, and 36 of them named the
// truncated id `3fa85f64`, which on a direct-parse command is rejected
// as a bad UUID.
//
// The rule is behavioural rather than textual, which is what makes it
// cover the class: run every example through the real command tree with
// no credentials and assert the exit code is not 2. Exit 2 means "the
// command was malformed" — a bad flag, a missing required flag, an
// unparseable argument — and no documented example should be that.
//
// Exit 5 (no credentials) is the expected outcome for a well-formed
// example, and it is the only non-zero code a passing one reaches:
// auth fails before any resolver runs, and cp is pointed at a dead
// address, so NOTHING exits 4. Exit 4 is NOT why the short-id examples
// survive, and this gate proves nothing about whether prefix resolution
// works.
//
// The SIX lines on `byoc cluster/database/node` and
// `managed database/backup` used to keep a deliberately short id,
// because a prefix was valid syntax there and advertised a real
// feature. Prefixes are withdrawn, so those six now carry full
// UUIDs like every other example and this gate covers them: it caught
// all 49 example lines the withdrawal invalidated, which is the reason
// no separate one had to be written for it.
//
// A consequence worth stating, since it is visible in the source: the
// deepest command paths plus a 36-character UUID exceed 79 columns and
// cannot be wrapped at a flag boundary, so a handful of example lines
// run long. That is deliberate. A `<database_id>` placeholder would
// read better and this gate would SKIP it — see the shell-syntax note
// below — and an example nothing runs is how the 49 got there.
//
// WHAT THIS DOES NOT READ, stated here rather than left to be
// rediscovered:
//
//   - examples carrying shell syntax — a pipe, a redirect, a
//     substitution, a heredoc — because this runs argv, not a shell.
//     Those are covered by the recipe gates instead.
//   - anything outside a command's cobra Example block. The
//     hand-written `**Example:**` prose in each llms.txt is a
//     different population.
//   - whether the example is SENSIBLE. It proves the command parses,
//     not that it does something useful.
func TestShippedExamplesAreNotMalformed(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var checked, skipped int
	walk(root, func(c *cobra.Command) {
		for _, line := range exampleInvocations(c) {
			if hasShellSyntax(line) {
				skipped++
				continue
			}
			args, ok := tokenize(line)
			if !ok || len(args) < 2 || args[0] != "pgedge" ||
				reachesTheNetwork(args) {
				skipped++
				continue
			}
			// Resolved against the tree, so a PROSE sentence beginning
			// "pgedge ..." cannot be run as an invocation. Two ship
			// today ("pgedge manages pgEdge products through a single
			// CLI:" and one about the completion script), and a
			// HasPrefix test cannot tell them from a command.
			if resolved, _, ferr := root.Find(args[1:]); ferr != nil ||
				resolved == root {
				skipped++
				continue
			}
			// A sentence can still resolve: "pgedge completion script
			// it installed. Supports bash, zsh, fish" reaches the
			// completion command and carries the rest as arguments.
			//
			// A SENTENCE break, not any ". ": a bare Contains(". ")
			// also matches an ELLIPSIS followed by a space, which hid
			// `task cancel ... 019783f4-... --force` -- one of the two
			// defect forms this very change fixed -- and dropped three
			// valid examples whose only offence was `sk-...`.
			if isSentenceBreak(line) {
				skipped++
				continue
			}
			checked++
			t.Run(line, func(t *testing.T) {
				err := runForError(t, substitutePlaceholders(args[1:])...)
				if cli.ExitCode(err) != cli.ExitUsage {
					return
				}
				if why := environmental(err); why != "" {
					t.Logf("exit 2 for an environmental reason (%s), "+
						"not because the example is malformed", why)
					return
				}
				t.Errorf("the example shipped in %q help exits 2 "+
					"— it is malformed, and copying it is how a "+
					"user meets this error path:\n  %s\n  %v",
					c.CommandPath(), line, err)
			})
		}
	})

	// Positive control: a walk that examined nothing would pass. The
	// floor is well under the real count so ordinary additions do not
	// trip it, but a broken extractor cannot hide.
	if checked < 150 {
		t.Fatalf("only %d examples were run (%d skipped); the "+
			"extractor is not reaching the tree", checked, skipped)
	}
	// Not all "shell syntax": the skips also cover prose sentences and
	// lines carrying a shell variable.
	t.Logf("ran %d examples, skipped %d", checked, skipped)
}

// exampleInvocations pulls the `pgedge ...` lines out of a command's
// help text, joining backslash continuations into one line.
//
// It reads LONG, not cobra's Example field. This tree writes its
// examples inside Long under an "Example:" heading, and Example is
// empty everywhere -- the positive control below is what caught a
// first version of this that read Example and examined nothing.
func exampleInvocations(c *cobra.Command) []string {
	text := c.Long
	if text == "" {
		text = c.Example
	}
	if text == "" {
		return nil
	}
	joined := strings.ReplaceAll(text, "\\\n", " ")
	var out []string
	for _, l := range strings.Split(joined, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "pgedge ") {
			out = append(out, strings.Join(strings.Fields(l), " "))
		}
	}
	return out
}

// environmental names the reason an otherwise well-formed example
// exits 2 in a test harness, or "" when there is none.
//
// Three, and all three are properties of the harness rather than of
// the example.
// A DISCRIMINATOR on the message, not a blanket allowance on exit 2:
// the whole point of this gate is that a malformed example also exits
// 2, so anything that waived the code wholesale would waive the gate.
//
//   - The destructive-verb refusal. Every prompting verb ships its
//     interactive form as an example, which is right -- a human gets
//     a prompt. There is no terminal here, so cli.Confirm refuses.
//   - A named input file that does not exist. `-f spec.yaml` is the
//     documented shape and a missing input file is exit 2 in every
//     module; the harness has no spec.yaml.
func environmental(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "this operation is destructive"):
		return "destructive verb, no terminal to prompt on"
	case strings.Contains(msg, "run this in a terminal to choose from a list"):
		return "prompting verb, no terminal to prompt on"
	case strings.Contains(msg, "no such file or directory"):
		return "the example names an input file the harness lacks"
	case strings.Contains(msg, "no .pgedge/link.yaml found") ||
		strings.Contains(msg, "no database ID given and no .pgedge/link.yaml"):
		return "the example relies on a project link the harness's folder lacks"
	case strings.Contains(msg, "unknown module"):
		// `pgedge llms <module>` resolves through module.Registered(),
		// which main.go populates in init and this harness
		// deliberately does not: Register panics on a duplicate name,
		// and every gate in this package runs in one process. So the
		// module lookup cannot succeed here even though it does in the
		// shipped binary -- verified by hand, `pgedge llms starfleet` and
		// `pgedge llms starfleet byoc` both exit 0.
		//
		// The alternative would be registering the real modules once
		// from this package, which trades a documented blind spot for
		// global mutation in a package of build gates. Not worth it
		// for two examples.
		return "llms resolves modules through a registry main.go " +
			"populates and this harness does not"
	}
	return ""
}

// hasShellSyntax reports whether a line needs a shell to mean what it
// says, in which case running it as argv would test something else.
//
// A REDIRECT or a PIPE, not an angle bracket: `<database_id>` is a
// placeholder, and skipping the 105 lines that carry one as "shell
// syntax" is the cap substitutePlaceholders records. Placeholders are
// handled by the resolver check instead: they reach their command, and
// a placeholder in an ID position produces a usage error the gate
// SHOULD report.
func hasShellSyntax(line string) bool {
	// A bare > or < is a redirect; a matched <pair> is a placeholder.
	stripped := anglePlaceholderRe.ReplaceAllString(line, "PLACEHOLDER")
	return strings.ContainsAny(stripped, "|><$`&;")
}

// anglePlaceholderRe matches an angle-bracket placeholder such as
// <database_id>. Non-greedy and space-free so it cannot span a real
// redirect.
var anglePlaceholderRe = regexp.MustCompile(`<[a-zA-Z0-9_.\-]+>`)

// tokenize splits an example into argv, honouring double quotes so a
// multi-word value survives.
//
// strings.Fields alone turned `--description "CI runner"` into
// `"CI` and `runner"`, and the command then failed on the stray
// token -- so the gate ran something the example does not say and
// reported it as malformed. Returns false for an unbalanced quote,
// which is a line this cannot faithfully run.
func tokenize(line string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inQuote, started := false, false
	for _, r := range line {
		switch {
		case r == '"' || r == '\'':
			// Single quotes too. No example uses them today, but
			// with the exit-code promotion in place a mis-tokenized
			// line FAILS rather than passing quietly, so the cost
			// of not handling them is a false failure.
			inQuote = !inQuote
			started = true
		case (r == ' ' || r == '\t') && !inQuote:
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	if inQuote {
		return nil, false
	}
	return out, true
}

// runForError runs args and hands back the error, reproducing the two
// things main.go does that a bare Execute does not.
//
// **It points cp at a dead address.** Without a config, cp falls back
// to defaultBaseURL -- http://localhost:3000 -- and a developer
// machine may well have a real Control Plane there. This gate walks
// EVERY example, including `controlplane database delete storefront --force`
// and `cp host remove host-3 --force`, so a bare Execute turns
// `make test` into an unauthenticated destructive client against
// whatever is listening, with live 409/500/400 bodies coming back and
// outbound DNS from `cluster join`. CLAUDE.md puts
// live-API work behind `make test-integration` for exactly this
// reason, and every other command-executing harness in this package
// points at a stub.
//
// **It applies main.go's exit-code promotion.** cobra reports a
// pre-RunE error -- a missing required flag, an unknown flag, an
// unknown command -- as a plain error, and main.go promotes it to
// ExitUsage when no run hook was entered. Without that, every one of
// those arrives here as exit 1 and the gate's whole premise ("exit 2
// means malformed") is false for the majority of malformed shapes:
// measured, 6 of 8 malformed families read 1 in the harness and 2 in
// the real binary.
func runForError(t *testing.T, args ...string) error {
	t.Helper()
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

	ran := false
	markRanForTest(root, &ran)

	args = pointAtDeadAddress(args)
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		return nil
	}
	var se *cli.SetupError
	if !ran && cli.ExitCode(err) == cli.ExitError &&
		!errors.As(err, &se) {
		// Mirrors main.go: a pre-run cobra error not already tagged as
		// a usage error IS one.
		return &cli.UsageError{Msg: err.Error()}
	}
	return err
}

// deadAddress refuses instantly rather than timing out, so no example
// can reach a real service and none of them can hang.
const deadAddress = "http://127.0.0.1:1"

// pointAtDeadAddress rewrites args so no example can reach a real
// service, whatever it names.
//
// It STRIPS the example's own address flag rather than appending after
// it, and the difference matters because the two flags behave
// differently:
//
//   - controlplane's --base-url is a StringArray (HA failover), so a repeated
//     occurrence APPENDS. A first version appended and claimed "the
//     example's own --base-url still wins: cobra takes the last
//     occurrence" -- false for an array flag. Both were tried, the
//     example's FIRST, so `cluster join --base-url http://new-host:3000`
//     still resolved and probed new-host.
//   - starfleet's --api-url is a StringVar, where last-occurrence-wins
//     does hold. Appending would be enough there, but stripping is
//     uniform and cannot be wrong.
//
// The cloud half is not hypothetical. `starfleet auth login --client-id ID
// --client-secret SECRET` ships as an example, and supplying
// credentials inline bypasses the exit-5 that protects every other
// cloud example -- so every `make test` POSTed the literal strings
// ID/SECRET to https://api.pgedge.com and got a 401 back, generating a
// failed-auth event upstream on every run.
func pointAtDeadAddress(args []string) []string {
	if len(args) == 0 {
		return args
	}
	var flag string
	switch args[0] {
	case "controlplane":
		flag = "--base-url"
	case "starfleet":
		flag = "--api-url"
	default:
		// Top-level commands take neither flag. The one that
		// reaches the network anyway, `doctor`, is skipped by name
		// in reachesTheNetwork.
		return args
	}
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++ // also drop its value
			continue
		}
		if strings.HasPrefix(args[i], flag+"=") {
			continue
		}
		out = append(out, args[i])
	}
	return append(out, flag, deadAddress)
}

// markRanForTest is main.go's markRan. It is duplicated rather than
// exported because main.go is package main and cannot be imported;
// the two are small and the comment above records the dependency.
func markRanForTest(c *cobra.Command, ran *bool) {
	if orig := c.RunE; orig != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			*ran = true
			return orig(cmd, args)
		}
	}
	if orig := c.Run; orig != nil {
		c.Run = func(cmd *cobra.Command, args []string) {
			*ran = true
			orig(cmd, args)
		}
	}
	for _, child := range c.Commands() {
		markRanForTest(child, ran)
	}
}

// placeholderValue is what an angle-bracket placeholder stands in for.
// A UUID, because most placeholders are ids and every other position
// accepts an arbitrary string.
const placeholderValue = "b0c1d2e3-f4a5-6789-bcde-890123456789"

// substitutePlaceholders replaces `<database_id>` and friends with a
// usable value, leaving every other token exactly as written.
//
// This is the difference between a template and a defect.
// `backup-store delete <backup_store_id>` is how documentation says
// "put your id here"; nobody copies the angle brackets. Refusing it
// would be a false positive on 29 examples, and SKIPPING all 105
// placeholder lines as shell syntax would hide 70 examples that run
// perfectly well, a malformed `rag deploy` among them.
//
// It substitutes ONLY the bracket form. A bare truncated id like
// 3fa85f64 is left alone, so the truncated-id defect is still detected --
// TestTheExampleGateStillCatchesATruncatedID proves that, because a
// substitution broad enough to normalise short ids would quietly
// retire this whole gate.
func substitutePlaceholders(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if anglePlaceholderRe.MatchString(a) &&
			strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") {
			out[i] = placeholderValue
			continue
		}
		out[i] = a
	}
	return out
}

// TestTheExampleGateStillCatchesATruncatedID is the guard on the guard.
//
// substitutePlaceholders is one broadened regex away from normalising
// every id in every example, at which point the gate above passes
// whatever the tree says and truncated ids come back unnoticed. With
// ids normalised, a planted defect goes green and the example count
// does not move, so the population floor cannot see it.
func TestTheExampleGateStillCatchesATruncatedID(t *testing.T) {
	// The truncated-id shape, on a direct-parse command.
	args := substitutePlaceholders(
		[]string{"starfleet", "client", "get", "3fa85f64"})
	if got := cli.ExitCode(runForError(t, args...)); got != cli.ExitUsage {
		t.Errorf("exit = %d, want %d: a truncated id must still be "+
			"detectable, or the example gate has stopped covering "+
			"the defect it exists for", got, cli.ExitUsage)
	}
	// And the placeholder form must NOT be, or the gate false-positives
	// on every templated example in the tree.
	tmpl := substitutePlaceholders(
		[]string{"starfleet", "client", "get", "<client_id>"})
	if got := cli.ExitCode(runForError(t, tmpl...)); got == cli.ExitUsage {
		t.Errorf("a documentation placeholder was reported as " +
			"malformed; substitution is not reaching it")
	}
}

// isSentenceBreak reports whether line contains a period that ends a
// sentence, as opposed to one inside an ellipsis or a version.
func isSentenceBreak(line string) bool {
	for i := 1; i < len(line)-1; i++ {
		if line[i] != '.' || line[i+1] != ' ' {
			continue
		}
		// "...", "sk-..." and "019783f4-..." all have a period before
		// the period. A sentence does not.
		if line[i-1] == '.' {
			continue
		}
		return true
	}
	return false
}

// reachesTheNetwork reports whether an example contacts a host this
// harness cannot point at a dead address.
//
// Only `doctor`, and only because its latest-version check GETs the
// GitHub releases API from an unexported var in internal/cli this
// package cannot reach. That call is unauthenticated, idempotent,
// public, 5s-bounded and fails soft — so it is not the hazard cp and
// cloud were — but `make test` should not depend on GitHub being up.
// Every other command is covered by pointAtDeadAddress. Named here
// rather than left to leak, because "make test does not touch the
// network" is a property worth keeping true after it took three review
// rounds to get there.
func reachesTheNetwork(args []string) bool {
	return len(args) > 1 && args[1] == "doctor"
}
