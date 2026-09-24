package clitest

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// exemptLong lists command paths excused from the leaf-Long check,
// with reasons.
//
// It is empty, and that is the point: "pgedge help" used to sit here
// excused as a cobra built-in, which was true right up until the tree
// stopped using the built-in. Owning the command (internal/cli.
// NewHelpCmd) removed the only reason any command was exempt, so the
// map stays declared as the short, justified place for the next one
// rather than being deleted and silently reinvented.
var exemptLong = map[string]bool{}

// exemptPureRouter pins the group commands that testsupport.IsPureRouter
// classifies as hybrids rather than routers, and which both pure-router
// checks therefore SKIP: internal/clitest's RunE-presence assertion and
// testsupport.WalkPureRouters' bare-invocation assertion.
//
// That exemption is silent by construction: a group whose Use carries
// any token other than "[flags]" is dropped from both checks with no
// diagnostic, so a decorative token — "database [command]", say — would
// quietly remove a real router from two gates. Nothing in the tree does
// that today, and this map is what keeps it that way: a group that
// becomes exempt without being listed here fails
// TestPureRouterExemptionsArePinned.
//
// Adding an entry is a deliberate act. Read declaresArgument's doc
// first: exempting MORE commands is the quiet direction, not the safe
// one.
var exemptPureRouter = map[string]string{
	"pgedge controlplane database restore": "Use: \"restore <database_id>\" — a " +
		"real action with a required positional argument, not a router",
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, child := range c.Commands() {
		walk(child, fn)
	}
}

// TestCommandTreeConformance enforces the UX conventions every
// pgedge command must follow: a present, sentence-cased Short under
// 60 chars; a Long on leaf commands; lowercase command names;
// singular resource-group names (plurals live in aliases); and
// kebab-case flags.
func TestCommandTreeConformance(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	walk(root, func(c *cobra.Command) {
		path := c.CommandPath()
		t.Run(path, func(t *testing.T) {
			if c.Short == "" {
				t.Error("missing Short")
			} else {
				if len(c.Short) >= 60 {
					t.Errorf("Short is %d chars, want < 60",
						len(c.Short))
				}
				r := []rune(c.Short)[0]
				if !unicode.IsUpper(r) {
					t.Error("Short must start uppercase")
				}
			}
			if !c.HasSubCommands() && c.Long == "" &&
				!exemptLong[path] {
				t.Error("leaf command missing Long")
			}
			// Lowercase is what this checks; no compound verbs (a
			// single word or a kebab-case noun) is the convention
			// it does not.
			name := c.Name()
			if name != strings.ToLower(name) {
				t.Errorf("name %q not lowercase", name)
			}
			// Aliases carry the plurals.
			if c.HasSubCommands() && c != root &&
				strings.HasSuffix(name, "s") &&
				!strings.HasSuffix(name, "ss") &&
				name != "llms" && name != "status" {
				t.Errorf("group %q looks plural; use singular "+
					"with a plural alias", name)
			}
			c.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Name != strings.ToLower(f.Name) ||
					strings.Contains(f.Name, "_") {
					t.Errorf("flag --%s not kebab-case", f.Name)
				}
				// A backtick in a usage string is not styling:
				// pflag's UnquoteUsage lifts the first backticked
				// span out as the flag's TYPE, so --help renders
				// "--region region list" where the generated
				// reference says "--region string". An unpaired
				// backtick is tolerated by pflag but refused here
				// too; quote command references with plain quotes.
				if strings.Contains(f.Usage, "`") {
					t.Errorf("flag --%s usage contains a "+
						"backtick, which pflag renders as the "+
						"flag's type token; use plain quotes",
						f.Name)
				}
			})
			// Group commands: a stray argument must be a usage
			// error (exit 2), not a silent help dump (exit 0); a
			// bare invocation (no args at all) must produce a help
			// rendering, not silently succeed. The rest of the
			// reasoning lives in checkPureRouterBehaviour rather
			// than inline, so a synthetic fixture can drive the
			// exact same logic in a unit test independent of the
			// real command tree.
			if testsupport.IsPureRouter(c) {
				checkPureRouterBehaviour(t, c)
			}
		})
	})
}

// checkPureRouterBehaviour runs the two pure-router checks for one
// command c, reporting failures on t: a bare invocation (no args)
// must produce a help rendering, and a stray argument must be a
// usage error. Extracted from TestCommandTreeConformance's walk so
// TestCheckPureRouterBehaviourHandlesRunOnly can drive it directly
// against a synthetic fixture, independent of the real command tree.
//
// The root command is exempt from ever reaching here: cobra's
// legacyArgs already rejects an unknown subcommand for the ROOT
// specifically (args.go:34-37) even with Args unset, an asymmetry that
// does not extend to any other group. Giving root its own cobra.NoArgs
// would be redundant, not wrong, but is not required here.
// testsupport.IsPureRouter's own HasParent() check is what keeps root
// out (see its doc).
//
// IsPureRouter's declaresArgument check is what excludes a hybrid like
// "pgedge controlplane database restore" (Use: "restore <database_id>"), itself a
// real action with a required positional argument, not a router. cobra's
// Find only recurses into a child when a token exactly matches that
// child's name (command.go:798-817's findNext; prefix matching is opt-in
// and off by default), so a stray token given to a hybrid is real input
// to its own Args, never routing ambiguity — declaring the argument in
// Use is what keeps a command out of scope here, which also forces it
// into --help and the generated reference instead of a hand-maintained
// exemption list.
func checkPureRouterBehaviour(t *testing.T, c *cobra.Command) {
	t.Helper()
	for _, msg := range pureRouterCheckViolations(c) {
		t.Error(msg)
	}
}

// pureRouterCheckViolations is the *testing.T-free core of
// checkPureRouterBehaviour, split out for the same reason
// testsupport.pureRouterViolations is split from WalkPureRouters: a
// failing subtest created via t.Run always propagates failure to every
// ancestor test up to the whole `go test` result, so
// TestCheckPureRouterBehaviourHandlesRunOnly cannot assert "this must
// fail" through the live reporting path without failing this package's
// entire test run. Returning data instead of reporting sidesteps that.
func pureRouterCheckViolations(c *cobra.Command) []string {
	var out []string
	if c.RunE == nil && c.Run == nil {
		out = append(out, "group command has no RunE or Run: cobra "+
			"returns flag.ErrHelp for a non-runnable command before "+
			"Args is ever consulted (command.go:954-956), so a "+
			"stray argument prints help and exits 0")
	} else {
		// testsupport.RunBareCapture handles both RunE and Run:
		// calling c.RunE(c, nil) unconditionally here (once EITHER
		// hook was known non-nil) is exactly the bug that let a
		// Run-only pure router reach a nil c.RunE and panic, taking
		// this whole package's test run down instead of reporting a
		// clean failure. Nothing in the shipped tree currently uses
		// bare Run, so this did not fire today — but the guard
		// above already tolerated the shape, and the invocation
		// must too.
		//
		// LooksLikeHelp, not a bare non-empty check: a no-op RunE
		// (return nil, print nothing) satisfies every other check
		// here while reproducing the exact bug this task exists to
		// close, just triggered by zero args instead of a stray
		// one, and a router that printed one stray character would
		// satisfy "non-empty" while still being silently broken.
		output, err := testsupport.RunBareCapture(c)
		switch {
		case err != nil:
			out = append(out, fmt.Sprintf(
				"bare invocation returned an error: %v", err))
		case !testsupport.LooksLikeHelp(output):
			out = append(out,
				"bare invocation did not print a help rendering")
		}
	}
	if c.Args == nil {
		out = append(out, "group command missing Args: a "+
			"stray argument is accepted silently")
	} else if err := c.Args(
		c, []string{"zzz-no-such-thing"},
	); err == nil {
		out = append(out, "group command Args accepts a stray argument")
	}
	return out
}

// TestCheckPureRouterBehaviourHandlesRunOnly is Important 1's mutation
// proof at the clitest level: a pure router written with Run instead of
// RunE must reach a clean, reported failure — not a nil-pointer panic
// from an unconditional c.RunE(c, nil) call, which is exactly what the
// pre-fix guard (`if c.RunE == nil && c.Run == nil { fail } else {
// c.RunE(c, nil) }`) produced once EITHER hook was non-nil. Completing
// without a panic is itself part of what this proves: that version
// would have crashed the whole package's test binary here. Driven
// against a synthetic fixture via pureRouterCheckViolations directly,
// independent of the real command tree, since nothing in the shipped
// tree currently uses bare Run.
//
// Args is cobra.NoArgs (not left nil) so the fixture triggers exactly
// one violation — the Run-only no-op detection — rather than two,
// keeping the assertion precise about which check caught it.
func TestCheckPureRouterBehaviourHandlesRunOnly(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	bad := &cobra.Command{
		Use:  "bad",
		Args: cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			// No-op: prints nothing, matching the no-op-RunE mutant
			// this task's gate already catches for the RunE shape.
		},
	}
	bad.AddCommand(&cobra.Command{
		Use: "leaf", Run: func(*cobra.Command, []string) {},
	})
	root.AddCommand(bad)

	got := pureRouterCheckViolations(bad)
	if len(got) != 1 {
		t.Fatalf("violations = %v, want exactly 1", got)
	}
	const want = "bare invocation did not print a help rendering"
	if got[0] != want {
		t.Errorf("violation = %q, want %q", got[0], want)
	}
}

// TestPureRouterExemptionsArePinned asserts that the set of group
// commands exempted from the pure-router checks is exactly
// exemptPureRouter. Without it the exemption is unobservable: a group
// silently classified as a hybrid disappears from two gates and nothing
// says so.
func TestPureRouterExemptionsArePinned(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var groups, routers int
	exempt := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		path := c.CommandPath()
		// The same shape IsPureRouter tests, minus its
		// declaresArgument clause: every command that is a candidate
		// router at all.
		if !c.HasParent() || !c.HasSubCommands() {
			return
		}
		groups++
		if testsupport.IsPureRouter(c) {
			routers++
			return
		}
		exempt[path] = true
	})

	for path := range exempt {
		if reason, ok := exemptPureRouter[path]; !ok {
			t.Errorf("%s is exempt from both pure-router checks but is "+
				"not in exemptPureRouter — a token in its Use made it a "+
				"hybrid. Either drop that token or list it with a "+
				"reason", path)
		} else if reason == "" {
			t.Errorf("%s: exemption must carry a reason", path)
		}
	}
	for path := range exemptPureRouter {
		if !exempt[path] {
			t.Errorf("%s is listed in exemptPureRouter but is no longer "+
				"exempt — delete the entry", path)
		}
	}

	// Positive controls. Without these a walk that matched nothing, or
	// an IsPureRouter that returned false for everything, would pass:
	// the first loop would find no unexpected exemptions and the second
	// would be vacuous.
	if groups < 20 {
		t.Errorf("found only %d group commands; expected 20+ — has the "+
			"walk stopped finding them?", groups)
	}
	if routers < groups/2 {
		t.Errorf("only %d of %d groups classified as pure routers — "+
			"IsPureRouter looks broken", routers, groups)
	}
}
