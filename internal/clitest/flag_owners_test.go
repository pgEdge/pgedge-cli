package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- the flag-tier owners gate -------------------------------------------------
//
// The pushdown definition (consistency evidence, deliverable 1) says a
// flag is declared at exactly one of three tiers: the root's
// persistent set (CLI-level flags), a module root's persistent set
// (connection flags), or a leaf's local set (action flags). The whole
// binary carries exactly three PersistentFlags() owners, and no leaf
// shadows a name an ancestor declared. Until now that held by
// convention alone — cobra tolerates a shadowing leaf flag silently,
// and a fourth persistent owner would register without complaint.
// These two tests convert the definition from "true today" to "cannot
// silently stop being true"; they land with the point-up help fix
// (the sub-roots deferring to `pgedge starfleet --help`), which leans on
// the same guarantee.

// tierRoots is the exact set of commands allowed to declare
// persistent flags, as a written-down decision. Adding a module adds
// its root here deliberately, in review — not by a leaf quietly
// calling PersistentFlags().
var tierRoots = map[string]bool{
	"pgedge":              true,
	"pgedge starfleet":    true,
	"pgedge controlplane": true,
}

// persistentOwnerViolations returns one violation per command outside
// tierRoots that declares persistent flags, plus the owners it saw —
// the caller asserts the owner set exactly, so a walk that went blind
// (or a tier root that stopped declaring) fails as loudly as a rogue
// owner.
func persistentOwnerViolations(root *cobra.Command,
	allowed map[string]bool) (violations, owners []string) {

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.PersistentFlags().HasFlags() {
			owners = append(owners, c.CommandPath())
			if !allowed[c.CommandPath()] {
				violations = append(violations, c.CommandPath()+
					" declares persistent flags but is not a tier root")
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return violations, owners
}

func TestPersistentFlagOwnersAreTheTierRoots(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	violations, owners := persistentOwnerViolations(root, tierRoots)
	for _, v := range violations {
		t.Error(v)
	}

	// The other direction: every tier root must actually own
	// persistent flags. A root that stopped declaring would otherwise
	// leave this gate green while the definition it pins is gone.
	seen := make(map[string]bool, len(owners))
	for _, o := range owners {
		seen[o] = true
	}
	for want := range tierRoots {
		if !seen[want] {
			t.Errorf("%s declares no persistent flags — the tier "+
				"definition says it must, so either the tree or "+
				"tierRoots is wrong", want)
		}
	}
	if len(owners) != len(tierRoots) {
		t.Errorf("expected exactly %d persistent-flag owners, the "+
			"walk found %d: %v", len(tierRoots), len(owners), owners)
	}
}

// shadowViolations returns one violation per flag declared on a
// command when an ancestor already declares the same name
// persistently. cobra tolerates the shadow silently, and a shadowed
// connection or output flag would make `--api-url` mean two things at
// two depths — the exact ambiguity the tier definition exists to
// prevent.
//
// The match is on the LONG name only. A SHORTHAND collision with an
// inherited flag never reaches this gate: pflag panics on the
// duplicate inside mergePersistentFlags, so it fails the suite with
// a stack trace instead of this gate's message (measured) — loud,
// just less legible.
func shadowViolations(root *cobra.Command) []string {
	var violations []string

	var walk func(c *cobra.Command, inherited map[string]string)
	walk = func(c *cobra.Command, inherited map[string]string) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if owner, ok := inherited[f.Name]; ok {
				violations = append(violations,
					c.CommandPath()+" declares --"+f.Name+
						", shadowing the one "+owner+" declares")
			}
		})

		next := inherited
		if c.PersistentFlags().HasFlags() {
			next = make(map[string]string, len(inherited)+8)
			for k, v := range inherited {
				next[k] = v
			}
			c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
				next[f.Name] = c.CommandPath()
			})
		}
		for _, sub := range c.Commands() {
			walk(sub, next)
		}
	}
	walk(root, map[string]string{})
	return violations
}

func TestNoFlagShadowsAnInheritedName(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range shadowViolations(root) {
		t.Error(v)
	}
}

// TestShadowGateCatchesAShadowingLeaf is the positive control: the
// live tree is clean, so without this a walk that visits nothing
// looks identical to a tree that shadows nothing.
func TestShadowGateCatchesAShadowingLeaf(t *testing.T) {
	root := &cobra.Command{Use: "pgedge"}
	root.PersistentFlags().String("output", "", "output format")
	group := &cobra.Command{Use: "starfleet"}
	leaf := &cobra.Command{Use: "list",
		Run: func(*cobra.Command, []string) {}}
	leaf.Flags().String("output", "", "a shadowing local flag")
	group.AddCommand(leaf)
	root.AddCommand(group)

	got := shadowViolations(root)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 shadow violation, got %d: %v",
			len(got), got)
	}
	if !strings.Contains(got[0], "pgedge starfleet list") ||
		!strings.Contains(got[0], "--output") {
		t.Errorf("violation should name the leaf and the flag, "+
			"got %q", got[0])
	}
}

// TestOwnerGateCatchesAFourthOwner is the owner gate's positive
// control, for the same reason.
func TestOwnerGateCatchesAFourthOwner(t *testing.T) {
	root := &cobra.Command{Use: "pgedge"}
	root.PersistentFlags().String("output", "", "output format")
	group := &cobra.Command{Use: "cluster"}
	group.PersistentFlags().String("region", "", "a rogue persistent flag")
	root.AddCommand(group)

	violations, _ := persistentOwnerViolations(root,
		map[string]bool{"pgedge": true})
	if len(violations) != 1 ||
		!strings.Contains(violations[0], "pgedge cluster") {
		t.Fatalf("want the rogue owner reported, got: %v", violations)
	}
}
