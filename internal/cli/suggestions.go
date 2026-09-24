package cli

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
)

// noArgsPointer identifies cobra.NoArgs so a wrapped validator can be
// told from the one this walk is willing to wrap. Taken once: the
// address is stable within a build, and comparing it per command is
// what keeps the walk idempotent.
var noArgsPointer = reflect.ValueOf(cobra.NoArgs).Pointer()

// AddUnknownCommandSuggestions makes an unknown subcommand offer the
// same "Did you mean this?" near-miss at every depth that the root
// already offers at depth 1.
//
// Two cobra paths produce the same sentence and only one suggests: the
// root goes through legacyArgs, which ends `unknown command %q for
// %q%s` with findSuggestions, while every command below it declares
// cobra.NoArgs, whose message has the same first two verbs and no
// third. findSuggestions is unexported, so this rebuilds its output
// byte for byte from the exported SuggestionsFor, and the depths agree.
//
// It wraps only a validator that is exactly cobra.NoArgs, by function
// identity, and that is the safety argument. NoArgs fails one way, a
// positional given to a command that takes none, so a near-miss cannot
// mislabel a different kind of mistake, as it did when this wrapped an
// arity check. `controlplane database restore <database_id>` has
// subcommands and a required positional, and SuggestionsFor matches by
// prefix as well as by edit distance, so on the shipped binary
// `controlplane database restore t b` answered
//
//	Error: accepts 1 arg(s), received 2
//	Did you mean this?
//	        template
//
// though the caller mistyped nothing and `template` does not fix an
// arity error.
//
// That failure is usually a subcommand that did not resolve, but not
// always: `pgedge starfleet -- tenant` reaches it with a positional the
// caller said was not a command. cobra's Find strips everything after
// `--` before legacyArgs, so the root never sees it, while below the
// root Flags().Args() keeps it; suggestionHint drops the one suggestion
// that case produces.
//
// The identity test costs no coverage: measured before the walk runs,
// the 46 group commands are the root with a nil Args, 44 declaring
// cobra.NoArgs, and that one hybrid.
//
// It is also what makes the walk idempotent. After wrapping, Args is
// this closure, not cobra.NoArgs, so a second walk skips it; without
// the test, walking twice appended the hint twice (measured 1 hint
// before, 2 after), and this has two call sites.
//
// StrayArgsOnHelp asks the command's own validator, now the wrapped
// one, so an unknown subcommand at depth 2 or more with --help appended
// gets the same suggestion as the ordinary form.
//
// The root is left alone. Its Args is nil, so cobra's ValidateArgs
// falls through to legacyArgs and depth 1 keeps its own hint; wrapping
// it would append a second copy. The nil check is redundant with the
// identity test and kept because it says why.
//
// Call it after every module has registered, because a command added
// later is not walked. cmd/pgedge/main.go and clitest.FullTree both do,
// and the gate walks the population the tree declares rather than a
// list, so a new group command is covered the day it lands.
func AddUnknownCommandSuggestions(root *cobra.Command) {
	if root == nil {
		return
	}
	for _, c := range root.Commands() {
		AddUnknownCommandSuggestions(c)
	}
	if !root.HasSubCommands() || root.Args == nil {
		return
	}
	// Function identity, not a behavioural probe: calling the validator
	// to see what it does would mean inventing an argument for it, and
	// a group command that is also an action would run its own checks.
	if reflect.ValueOf(root.Args).Pointer() != noArgsPointer {
		return
	}
	// cobra's findSuggestions defaults this lazily as it formats. Set
	// here so a depth-2 suggestion uses the root's distance: left at
	// zero, SuggestionsFor matches only a prefix or an exact hit, so
	// `tenat` would suggest nothing while the root suggested
	// `starfleet` for `strafleet`.
	if root.SuggestionsMinimumDistance <= 0 {
		root.SuggestionsMinimumDistance = 2
	}
	inner := root.Args
	root.Args = func(cmd *cobra.Command, args []string) error {
		err := inner(cmd, args)
		if err == nil || len(args) == 0 {
			return err
		}
		hint := suggestionHint(cmd, args[0])
		if hint == "" {
			return err
		}
		// args[0], not the last positional: NoArgs reports the first
		// unexpected word, the one that failed to resolve as a
		// subcommand. %w keeps a typed Args error reachable through
		// errors.As; nothing returns one today, and a wrapper is where
		// that changing would go unseen.
		return fmt.Errorf("%w%s", err, hint)
	}
}

// suggestionHint reproduces cobra's unexported findSuggestions for arg.
//
// Kept byte-identical to the original, including the two leading
// newlines, the tab before each name and their order.
// TestSuggestionHintMatchesCobrasOwn compares a depth-2 miss against a
// depth-1 miss rather than a string written here, so a cobra upgrade
// that changes the wording fails it instead of splitting the depths. It
// needs more than one matching child to see the loop, and compares the
// names in order: a sorted comparison let a reversal through, which put
// `cloud-account, cluster, config-version` backwards at depth 3 while
// depth 1 kept its order.
func suggestionHint(cmd *cobra.Command, arg string) string {
	if cmd.DisableSuggestions {
		return ""
	}
	suggestions := cmd.SuggestionsFor(arg)
	// Never suggest the word the caller typed. SuggestionsFor counts an
	// exact hit as a match (HasPrefix, and a string prefixes itself).
	// cobra normally descends into that child instead, so this surfaces
	// only after a `--`: `pgedge starfleet -- tenant` answered
	// "unknown command \"tenant\"" then "Did you mean this? tenant".
	// Filter rather than drop the hint, because a typo can match both
	// an exact name and a near one.
	//
	// A fresh slice, not suggestions[:0]: filtering in place aliases
	// what SuggestionsFor returned, safe in cobra v1.10.2, which
	// allocates per call, but quiet corruption if a later version
	// caches or pools that slice.
	kept := make([]string, 0, len(suggestions))
	for _, sg := range suggestions {
		if sg != arg {
			kept = append(kept, sg)
		}
	}
	suggestions = kept
	if len(suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nDid you mean this?\n")
	for _, s := range suggestions {
		fmt.Fprintf(&b, "\t%v\n", s)
	}
	return b.String()
}
