package cli_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/spf13/cobra"
)

// hintOf returns the "Did you mean this?" tail of an error message, or
// "" when there is none.
func hintOf(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	i := strings.Index(msg, "\n\nDid you mean this?")
	if i < 0 {
		return ""
	}
	return msg[i:]
}

// TestSuggestionHintMatchesCobrasOwn is the reason internal/cli
// reproduces an unexported cobra function rather than inventing a
// message.
//
// The whole point is that the two depths AGREE. So this
// compares a wrapped depth-2 rejection against what cobra's own
// legacyArgs produces for the identical typo at the root, byte for
// byte, instead of against a string written here. A cobra upgrade that
// rewords the hint, changes the two leading newlines or drops the tab
// then fails this comparison rather than silently splitting the two
// depths apart again.
func TestSuggestionHintMatchesCobrasOwn(t *testing.T) {
	// A root with Args left nil, which is what makes cobra apply
	// legacyArgs -- the path that already suggested.
	// THREE children, two of which match, because a one-child fixture
	// cannot see suggestionHint's loop at all -- review dropped the
	// loop and printed suggestions[0] only, and the whole repository
	// passed. Multi-suggestion output is real: `pgedge starfleet byoc c`
	// returns three names.
	cobraRoot := &cobra.Command{Use: "app", SilenceUsage: true,
		SilenceErrors: true}
	for _, n := range []string{"starfleet", "starflight", "unrelated"} {
		cobraRoot.AddCommand(&cobra.Command{Use: n,
			Run: func(*cobra.Command, []string) {}})
	}
	// Force cobra's lazy child sort before ExecuteC, so both fixtures
	// are in the state the real binary reaches. Without this the two
	// sides differ by insertion order and the comparison above cannot
	// be about the format.
	_ = cobraRoot.Commands()
	cobraRoot.SetArgs([]string{"starfl"})
	cobraRoot.SetOut(&strings.Builder{})
	cobraRoot.SetErr(&strings.Builder{})
	_, rootErr := cobraRoot.ExecuteC()
	if rootErr == nil {
		t.Fatal("cobra accepted an unknown command at the root")
	}
	rootHint := hintOf(rootErr)
	if rootHint == "" {
		t.Fatalf("cobra offered no hint at the root: %v", rootErr)
	}

	// A group command declaring cobra.NoArgs, which is what every
	// command below the root in this tree declares, then wrapped.
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	for _, n := range []string{"starfleet", "starflight", "unrelated"} {
		group.AddCommand(&cobra.Command{Use: n,
			Run: func(*cobra.Command, []string) {}})
	}
	// No Commands() call on the group: AddUnknownCommandSuggestions
	// opens with one, so the walk sorts every node it visits. The
	// root's call above is the one doing the work, because cobra's own
	// legacyArgs path does not sort.
	cli.AddUnknownCommandSuggestions(group)
	groupHint := hintOf(group.Args(group, []string{"starfl"}))
	// Two names, so the comparison covers the loop and not just its
	// first iteration.
	if strings.Count(groupHint, "\n\t") != 2 {
		t.Errorf("expected two suggested names, got %q", groupHint)
	}
	if groupHint == "" {
		t.Fatal("the wrapper offered no hint below the root")
	}

	// BYTE FOR BYTE, ORDER INCLUDED. An earlier version compared the
	// names sorted, on the grounds that cobra's lazy child sort made
	// the two sides disagree -- and that let a reversal through:
	// reversing the suggestions before formatting passed the whole
	// repository while the shipped binary listed `cloud-account,
	// cluster, config-version` at depth 1 and the reverse at depth 3
	// for one typo. Discarding order removed the only thing comparing
	// this wrapper against cobra.
	//
	// The disagreement was real but is the fixture's, not the code's:
	// cobra sorts a command's children on the first Commands() call, in
	// place, and the walk recurses through Commands() while cobra's own
	// legacyArgs path need not have. Calling Commands() on both parents
	// first puts them in the state the real binary is always in by the
	// time either path runs.
	if groupHint != rootHint {
		t.Errorf("hints differ:\n root: %q\ngroup: %q",
			rootHint, groupHint)
	}
	for _, h := range []string{rootHint, groupHint} {
		if !strings.HasPrefix(h, "\n\nDid you mean this?\n\t") {
			t.Errorf("hint does not open exactly as cobra's does: %q", h)
		}
		if !strings.HasSuffix(h, "\n") {
			t.Errorf("hint does not end in a newline: %q", h)
		}
	}
}

// TestSuggestionWrapperLeavesTheRootAlone pins that a nil Args stays
// nil. Wrapping it would give depth 1 the hint twice -- once from the
// wrapper and once from legacyArgs, which cobra still reaches because
// the wrapper cannot replace what it never set.
func TestSuggestionWrapperLeavesTheRootAlone(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	root.AddCommand(&cobra.Command{Use: "starfleet",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(root)
	if root.Args != nil {
		t.Error("the root's nil Args was replaced, so legacyArgs no " +
			"longer decides depth 1")
	}
}

// TestSuggestionWrapperPreservesTheUnderlyingError covers the two
// shapes the wrapper must not touch, which is what makes it safe to
// apply to a command that legitimately takes a positional.
func TestSuggestionWrapperPreservesTheUnderlyingError(t *testing.T) {
	t.Run("an accepted positional stays accepted", func(t *testing.T) {
		// The hybrid's shape: subcommands AND a required positional.
		hybrid := &cobra.Command{Use: "restore <database_id>",
			Args: cobra.ExactArgs(1),
			RunE: func(*cobra.Command, []string) error { return nil }}
		hybrid.AddCommand(&cobra.Command{Use: "template",
			Run: func(*cobra.Command, []string) {}})
		cli.AddUnknownCommandSuggestions(hybrid)
		if err := hybrid.Args(hybrid,
			[]string{"bfe1f23d-ed55-4a54-b95f-c9cafbe123f8"}); err != nil {
			t.Errorf("the wrapper rejected the argument this command "+
				"exists to take: %v", err)
		}
	})

	t.Run("a non-suggestion Args error is unchanged", func(t *testing.T) {
		hybrid := &cobra.Command{Use: "restore <database_id>",
			Args: cobra.ExactArgs(1),
			RunE: func(*cobra.Command, []string) error { return nil }}
		hybrid.AddCommand(&cobra.Command{Use: "template",
			Run: func(*cobra.Command, []string) {}})
		// "t", NOT "a". SuggestionsFor matches by PREFIX as well as by
		// distance, so only a value that prefixes a real child reaches
		// the branch this subtest names. With "a" the test passed for
		// its fixture value rather than for the code, and it is the one
		// test that would have caught the wrapper mislabelling an
		// arity error as a mistyped subcommand.
		before := hybrid.Args(hybrid, []string{"t", "b"})
		if before == nil {
			t.Fatal("two positionals were accepted by ExactArgs(1)")
		}
		cli.AddUnknownCommandSuggestions(hybrid)
		after := hybrid.Args(hybrid, []string{"t", "b"})
		if after == nil {
			t.Fatal("two positionals were accepted after wrapping")
		}
		if after.Error() != before.Error() {
			t.Errorf("the wrapper altered an unrelated Args error:\n"+
				"before: %q\n after: %q", before, after)
		}
	})

	t.Run("no positionals still passes", func(t *testing.T) {
		group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return nil }}
		group.AddCommand(&cobra.Command{Use: "child",
			Run: func(*cobra.Command, []string) {}})
		cli.AddUnknownCommandSuggestions(group)
		if err := group.Args(group, nil); err != nil {
			t.Errorf("NoArgs rejected zero positionals: %v", err)
		}
	})
}

// TestSuggestionWrapperRespectsDisableSuggestions pins the one cobra
// switch a caller could reasonably set. Nothing in this tree sets it,
// which is exactly why the behaviour has to be written down.
func TestSuggestionWrapperRespectsDisableSuggestions(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		DisableSuggestions: true,
		RunE:               func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "starfleet",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(group)
	err := group.Args(group, []string{"starfl"})
	if err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
	if hintOf(err) != "" {
		t.Errorf("offered a hint despite DisableSuggestions: %v", err)
	}
}

// TestSuggestionWrapperSetsTheSameDistanceAsCobra covers the field
// cobra defaults lazily while formatting.
//
// Left at zero, SuggestionsFor reads it directly and matches only an
// exact hit or a prefix -- so "tenat" would suggest nothing below the
// root while the root suggested "starfleet" for "starfl", which is the split
// this change exists to close rather than move.
func TestSuggestionWrapperSetsTheSameDistanceAsCobra(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "tenant",
		Run: func(*cobra.Command, []string) {}})
	if group.SuggestionsMinimumDistance != 0 {
		t.Fatalf("premise gone: cobra now defaults the distance to %d "+
			"before formatting", group.SuggestionsMinimumDistance)
	}
	cli.AddUnknownCommandSuggestions(group)
	if group.SuggestionsMinimumDistance != 2 {
		t.Errorf("distance = %d, want 2 to match cobra's own default",
			group.SuggestionsMinimumDistance)
	}
	// "tenat" is distance 2 from "tenant" and not a prefix of it, so it
	// is exactly the input a zero distance would have missed.
	if hintOf(group.Args(group, []string{"tenat"})) == "" {
		t.Error("no hint for a distance-2 typo, which is what the " +
			"default exists to catch")
	}
}

// TestSuggestionWrapperOnlyWrapsNoArgs is the structural half of the
// safety argument, and it exists because the earlier behavioural
// argument was measured false.
//
// SuggestionsFor matches by PREFIX as well as by edit distance, so a
// group command that is also an action — one required positional and
// subcommands — had its arity error decorated with a near miss:
// `controlplane database restore t b` answered "accepts 1 arg(s), received 2"
// followed by "Did you mean this? template". cobra.NoArgs has exactly
// one failure mode, so restricting the wrapper to it means the hint can
// never mislabel a different kind of mistake.
func TestSuggestionWrapperOnlyWrapsNoArgs(t *testing.T) {
	newHybrid := func() *cobra.Command {
		c := &cobra.Command{Use: "restore <id>",
			Args: cobra.ExactArgs(1),
			RunE: func(*cobra.Command, []string) error { return nil }}
		c.AddCommand(&cobra.Command{Use: "template",
			Run: func(*cobra.Command, []string) {}})
		return c
	}
	// The exact input that produced the wrong hint.
	hybrid := newHybrid()
	cli.AddUnknownCommandSuggestions(hybrid)
	if got := hintOf(hybrid.Args(hybrid, []string{"t", "b"})); got != "" {
		t.Errorf("decorated an arity error with a near miss: %q", got)
	}
	// And a validator this walk does not recognise is left as it was,
	// asserted by identity on the SAME command before and after.
	// Comparing two separately built commands cannot work:
	// cobra.ExactArgs returns a fresh closure per call, so their
	// pointers differ with no wrapping involved -- which is how a
	// first version of this assertion failed for its own reason
	// rather than the code's.
	same := newHybrid()
	beforePtr := reflect.ValueOf(same.Args).Pointer()
	cli.AddUnknownCommandSuggestions(same)
	if reflect.ValueOf(same.Args).Pointer() != beforePtr {
		t.Error("a non-NoArgs validator was replaced")
	}
}

// TestSuggestionWalkIsIdempotent pins that walking a tree twice does
// not append the hint twice.
//
// It is not hypothetical: the walk is exported and already has two call
// sites (cmd/pgedge/main.go and clitest.FullTree). Before the identity
// test, a second pass wrapped the wrapper — measured one hint before,
// two after.
func TestSuggestionWalkIsIdempotent(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "starfleet",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(group)
	once := group.Args(group, []string{"starfl"}).Error()
	cli.AddUnknownCommandSuggestions(group)
	twice := group.Args(group, []string{"starfl"}).Error()
	if once != twice {
		t.Errorf("a second walk changed the message:\n once: %q\ntwice: %q",
			once, twice)
	}
	if n := strings.Count(twice, "Did you mean this?"); n != 1 {
		t.Errorf("hint appears %d times after two walks, want 1", n)
	}
}

// TestSuggestionWrapperPreservesErrorIdentity pins the %w.
//
// Nothing in the tree returns a typed Args error today, so the verb is
// inert — which is exactly why it had no coverage and why review could
// swap it for %v with the whole repository green. The day a group
// command's validator returns something carrying its own exit code,
// %v would silently flatten it.
func TestSuggestionWrapperPreservesErrorIdentity(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "starfleet",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(group)

	// Through the PRODUCTION wrapper. A first version of this test
	// hand-wrote its own wrapper around a sentinel error, which tested
	// a copy of the code rather than the code -- so %v passed it.
	// errors.Unwrap is the assertion that works on the real path:
	// cobra.NoArgs returns a plain fmt.Errorf with nothing to be
	// identical to, but %w leaves it unwrappable and %v does not.
	got := group.Args(group, []string{"starfl"})
	if got == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
	inner := errors.Unwrap(got)
	if inner == nil {
		t.Error("the wrapper flattened the error it wrapped, so a " +
			"validator returning a typed error would lose its type " +
			"and any exit code with it")
	}
	if inner != nil && !strings.Contains(inner.Error(),
		"unknown command") {
		t.Errorf("unwrapped to something unexpected: %v", inner)
	}
	if !strings.Contains(got.Error(), "Did you mean this?") {
		t.Errorf("the hint was lost: %v", got)
	}
}

// TestSuggestionHintReadsTheFirstPositional pins WHICH word the hint
// is about.
//
// cobra.NoArgs reports the FIRST unexpected word, and that is the one
// that failed to resolve as a subcommand -- so the hint has to be about
// args[0]. Every other test here passes exactly one positional, where
// args[0] and the last argument are the same string, so nothing
// distinguished them: review swapped in args[len(args)-1] and the whole
// repository passed.
func TestSuggestionHintReadsTheFirstPositional(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "tenant",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(group)
	// "tenat" nearly matches; "somethingelse" matches nothing. The
	// hint must be about the first.
	err := group.Args(group, []string{"tenat", "somethingelse"})
	if err == nil {
		t.Fatal("two positionals were accepted by NoArgs")
	}
	if hintOf(err) == "" {
		t.Errorf("no hint for the first positional: %v", err)
	}
	// And the reverse order must NOT suggest, which is what makes the
	// pair a two-directional assertion rather than one lucky input.
	err = group.Args(group, []string{"somethingelse", "tenat"})
	if err == nil {
		t.Fatal("two positionals were accepted by NoArgs")
	}
	if h := hintOf(err); h != "" {
		t.Errorf("suggested for a later positional: %q", h)
	}
}

// TestSuggestionHintDropsTheWordJustTyped covers the one way NoArgs'
// failure mode is reached WITHOUT a mistyped subcommand.
//
// SuggestionsFor counts an exact hit as a match, since it tests
// HasPrefix and a string prefixes itself. That normally cannot surface,
// because cobra descends into a child of that name — but a `--`
// terminator gets past it: cobra's Find strips everything after the
// dashes before applying legacyArgs, so the root never sees it, while
// below the root Flags().Args() keeps it. `pgedge starfleet -- tenant`
// therefore answered "unknown command \"tenant\"" followed by
// "Did you mean this? tenant".
func TestSuggestionHintDropsTheWordJustTyped(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(&cobra.Command{Use: "tenant",
		Run: func(*cobra.Command, []string) {}})
	cli.AddUnknownCommandSuggestions(group)

	// The exact word: no hint at all, because the only suggestion was
	// the word itself.
	if h := hintOf(group.Args(group, []string{"tenant"})); h != "" {
		t.Errorf("suggested the word just typed: %q", h)
	}
	// A near miss is unaffected.
	if h := hintOf(group.Args(group, []string{"tenat"})); h == "" {
		t.Error("dropped a legitimate near miss")
	}
	// And a typo matching an exact name AND a near one keeps the near
	// one, which is why this filters rather than skipping the hint.
	group.AddCommand(&cobra.Command{Use: "tenants",
		Run: func(*cobra.Command, []string) {}})
	h := hintOf(group.Args(group, []string{"tenant"}))
	if !strings.Contains(h, "tenants") {
		t.Errorf("dropped the whole hint instead of the exact hit: %q", h)
	}
	if strings.Contains(h, "\n\ttenant\n") {
		t.Errorf("kept the exact hit alongside the near one: %q", h)
	}
}
