package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// suggestionHintMarker is cobra's own wording. It is asserted rather
// than reproduced: internal/cli's TestSuggestionHintMatchesCobrasOwn
// compares the two formats directly, so this only has to recognise it.
const suggestionHintMarker = "Did you mean this?"

// groupCommandsWithoutAnArgsValidator are the group commands whose Args
// is nil, so cobra's legacyArgs decides for them.
//
// It is the ROOT and nothing else, and that is the whole design: cobra
// only applies legacyArgs' unknown-subcommand rule to a command with no
// parent, which is why depth 1 already suggested and nothing below it
// did (#292). Wrapping the root as well would append a second copy of
// the same hint.
var groupCommandsWithoutAnArgsValidator = map[string]bool{
	"pgedge": true,
}

// groupCommandsThatAcceptAPositional are group commands that are also
// actions, so a positional is an ARGUMENT rather than a failed path
// element and their validator accepts it.
//
// There is exactly one, and internal/cli's StrayArgsOnHelp documents
// the same command as the reason it asks a command's own validator
// instead of hardcoding cobra.NoArgs. A suggestion wrapper that
// replaced the validator rather than wrapping it would reject the
// argument this command exists to take.
var groupCommandsThatAcceptAPositional = map[string]bool{
	"pgedge controlplane database restore": true,
}

// TestEveryGroupCommandSuggestsANearMiss is the gate for #292.
//
// The population is DERIVED from what the tree declares — every command
// with subcommands — rather than listed, because a hand list has been
// bypassed here repeatedly and because the point of the fix is that a
// group command added later is covered without anyone remembering. The
// two exception sets above are named individually and their sizes are
// pinned below, so a new exception has to be declared rather than
// quietly joining a count.
//
// It types a real child's name plus "zz": a Levenshtein distance of 2,
// which is exactly cobra's default threshold, and a spelling no command
// in the tree uses. Dropping a character instead would risk landing on
// a real sibling, which resolves and produces no error at all.
func TestEveryGroupCommandSuggestsANearMiss(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatalf("FullTree: %v", err)
	}

	var groups, suggested int
	var sawNilArgs, sawAccepting []string

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range c.Commands() {
			walk(child)
		}
		if !c.HasSubCommands() {
			return
		}
		groups++
		path := c.CommandPath()

		if c.Args == nil {
			sawNilArgs = append(sawNilArgs, path)
			if !groupCommandsWithoutAnArgsValidator[path] {
				t.Errorf("%s declares no Args validator, so nothing "+
					"below the root rejects an unknown subcommand "+
					"there; add one or declare the exception", path)
			}
			return
		}

		// The first AVAILABLE child, not simply the first. cobra
		// declines to suggest a Hidden or Deprecated command
		// (IsAvailableCommand), so typoing one would report "no
		// near-miss offered" for behaviour that is correct. None is
		// hidden today -- checked, the set is empty -- which is
		// exactly why the guard has to be written down rather than
		// discovered later.
		var child string
		for _, ch := range c.Commands() {
			if ch.IsAvailableCommand() {
				child = ch.Name()
				break
			}
		}
		if child == "" {
			t.Errorf("%s has subcommands but none available", path)
			return
		}
		typo := child + "zz"
		// The typo must not itself name something real, or the
		// validator would have nothing to reject and this row would
		// pass without testing anything.
		for _, sib := range c.Commands() {
			if sib.Name() == typo || sib.HasAlias(typo) {
				t.Fatalf("%s: the synthesized typo %q names a real "+
					"command; pick another suffix", path, typo)
			}
		}

		err := c.Args(c, []string{typo})
		if err == nil {
			sawAccepting = append(sawAccepting, path)
			if !groupCommandsThatAcceptAPositional[path] {
				t.Errorf("%s accepted the positional %q, so an "+
					"unknown subcommand there is not rejected at all",
					path, typo)
			}
			return
		}
		suggested++
		if !strings.Contains(err.Error(), suggestionHintMarker) {
			t.Errorf("%s: no near-miss offered for %q: %v",
				path, typo, err)
		}
		if !strings.Contains(err.Error(), child) {
			t.Errorf("%s: hint for %q does not name %q: %v",
				path, typo, child, err)
		}
	}
	walk(root)

	// Exact counts, not floors. A floor with slack is how a command
	// leaving a gate's population has gone unnoticed here before: the
	// number clears the bar and nothing says which one left.
	// 47 -> 48 and 45 -> 46 when `managed pg-version` landed: one new
	// group command, which offers a near-miss like every other.
	// 48 -> 49 and 46 -> 47 when `managed database allowlist` landed:
	// another new group command, offering a near-miss the same way.
	// 49 -> 50 and 47 -> 48 when `managed database branch` landed:
	// another new group command, offering a near-miss the same way.
	if groups != 50 {
		t.Errorf("walked %d group commands, expected 50; update this "+
			"number deliberately and say why", groups)
	}
	if suggested != 48 {
		t.Errorf("%d group commands offered a near-miss, expected 48",
			suggested)
	}
	if len(sawNilArgs) != len(groupCommandsWithoutAnArgsValidator) {
		t.Errorf("group commands with no Args validator = %v, "+
			"expected exactly %v",
			sawNilArgs, groupCommandsWithoutAnArgsValidator)
	}
	if len(sawAccepting) != len(groupCommandsThatAcceptAPositional) {
		t.Errorf("group commands accepting a positional = %v, "+
			"expected exactly %v",
			sawAccepting, groupCommandsThatAcceptAPositional)
	}
	// 47 = the root + the one hybrid + the 45 that suggest. Asserted
	// as an identity so the four numbers above cannot drift apart
	// while each one individually still looks right.
	if groups != suggested+len(sawNilArgs)+len(sawAccepting) {
		t.Errorf("%d groups != %d suggesting + %d nil-Args + %d "+
			"accepting; a group command is in none of the three "+
			"categories", groups, suggested, len(sawNilArgs),
			len(sawAccepting))
	}
}

// TestSuggestionIsNotOfferedForSomethingUnlikeACommand pins the
// inverse, which is what stops the wrapper appending noise to every
// Args error it sees.
//
// A UUID is the case that matters rather than an arbitrary string: the
// one group command that takes a positional takes a database ID, and
// the wrapper is only safe for it because a UUID never nearly matches
// a child name. If cobra's matching ever became loose enough to
// suggest for one, that command would start telling callers their
// database ID was a misspelled subcommand.
func TestSuggestionIsNotOfferedForSomethingUnlikeACommand(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatalf("FullTree: %v", err)
	}
	group, _, err := root.Find([]string{"starfleet", "byoc", "database"})
	if err != nil {
		t.Fatalf("find starfleet byoc database: %v", err)
	}
	for _, arg := range []string{
		"bfe1f23d-ed55-4a54-b95f-c9cafbe123f8",
		"totally-unrelated-word",
	} {
		argsErr := group.Args(group, []string{arg})
		if argsErr == nil {
			t.Fatalf("%q was accepted as a positional", arg)
		}
		if strings.Contains(argsErr.Error(), suggestionHintMarker) {
			t.Errorf("offered a near-miss for %q: %v", arg, argsErr)
		}
	}
}
