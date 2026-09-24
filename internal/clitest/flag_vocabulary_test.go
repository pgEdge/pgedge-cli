package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- the flag-vocabulary gate -------------------------------------------
//
// The args-vs-flags contract (ruled 2026-08-23/24) settled one name
// per concept: the wait bound is --wait-timeout, the managed metrics
// lookback is --window while byoc's keeps --interval, the API
// parameter's own name (both are lookbacks, #279), the node
// list is the plural --target-nodes, and --force skips the prompt and
// nothing else. Each ruling retired a spelling, and nothing stopped a
// future flag from quietly reviving one.
//
// This gate holds the LOST spellings as data. It cannot judge
// semantics (a new flag named --window in a future module may be
// right or wrong), so each ban is scoped to where the ruling applies,
// and the failure message names the ruled replacement. The
// human-readable convention lives in CONTRIBUTING.md's "Flag
// vocabulary" section; this list is deliberately the NARROW
// mechanical shadow of it — only rows a name-equality check can hold.

// lostSpellings are the flag names retired by a ruling, the scope the
// ban applies in (a scopeForCommandPath result, or "*" for every
// scope), and the ruled replacement for the failure message.
var lostSpellings = []struct {
	name  string
	scope string
	ruled string
}{
	// Multi-value rule, 2026-08-24 (#384): plural + comma-splittable.
	{"target-node", "*", "--target-nodes (a comma-splittable Slice)"},
	// #358, 2026-08-24 (#383): managed's lookback is --window; byoc's
	// bucket keeps --interval. Each name is banned in the OTHER's
	// scope, so the two meanings can never share a spelling again.
	{"interval", "managed", "--window (the managed lookback)"},
	{"window", "byoc", "--interval (the byoc lookback)"},
}

// lostSpellingViolations returns one violation per flag in the tree
// that revives a lost spelling within its banned scope.
func lostSpellingViolations(root *cobra.Command) []string {
	var violations []string

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		scope := scopeForCommandPath(c.CommandPath())
		seen := map[string]bool{}
		check := func(f *pflag.Flag) {
			if seen[f.Name] {
				return
			}
			seen[f.Name] = true
			for _, row := range lostSpellings {
				if f.Name != row.name {
					continue
				}
				if row.scope != "*" && row.scope != scope {
					continue
				}
				violations = append(violations, c.CommandPath()+
					" accepts --"+f.Name+", a spelling retired by "+
					"ruling — use "+row.ruled)
			}
		}
		// Local, own-persistent AND inherited: a banned name
		// registered persistently ABOVE the banned scope is usable
		// inside it, and LocalFlags alone never visits it — measured
		// on #388's review (an --interval on the starfleet group reached
		// managed leaves with the gate green).
		c.LocalFlags().VisitAll(check)
		c.PersistentFlags().VisitAll(check)
		c.InheritedFlags().VisitAll(check)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return violations
}

func TestNoFlagRevivesALostSpelling(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range lostSpellingViolations(root) {
		t.Error(v)
	}
}

// TestLostSpellingGateCatchesARevival is the positive control: the
// live tree is clean, so without this a walk that visits nothing looks
// identical to a tree that revives nothing. Both dimensions are
// exercised — a global ban, and a scoped ban that must fire in its own
// scope and stay silent outside it.
func TestLostSpellingGateCatchesARevival(t *testing.T) {
	build := func(scopePath []string, flag string) *cobra.Command {
		root := &cobra.Command{Use: "pgedge"}
		parent := root
		for _, use := range scopePath {
			c := &cobra.Command{Use: use}
			parent.AddCommand(c)
			parent = c
		}
		leaf := &cobra.Command{Use: "probe",
			Run: func(*cobra.Command, []string) {}}
		leaf.Flags().String(flag, "", "a revived spelling")
		parent.AddCommand(leaf)
		return root
	}

	t.Run("global ban fires anywhere", func(t *testing.T) {
		got := lostSpellingViolations(
			build([]string{"controlplane"}, "target-node"))
		if len(got) != 1 || !strings.Contains(got[0], "--target-nodes") {
			t.Fatalf("want 1 violation naming the replacement, "+
				"got: %v", got)
		}
	})

	t.Run("scoped ban fires in its scope", func(t *testing.T) {
		got := lostSpellingViolations(
			build([]string{"starfleet", "managed"}, "interval"))
		if len(got) != 1 || !strings.Contains(got[0], "--window") {
			t.Fatalf("want the managed interval revival caught, "+
				"got: %v", got)
		}
	})

	t.Run("inherited persistent flag is caught in the banned scope",
		func(t *testing.T) {
			// The measured escape: a banned name registered
			// persistently ABOVE the banned scope reaches its leaves
			// through inheritance, invisible to LocalFlags.
			root := &cobra.Command{Use: "pgedge"}
			starfleet := &cobra.Command{Use: "starfleet"}
			starfleet.PersistentFlags().String("interval", "",
				"a revived spelling, inherited downward")
			managed := &cobra.Command{Use: "managed"}
			leaf := &cobra.Command{Use: "probe",
				Run: func(*cobra.Command, []string) {}}
			managed.AddCommand(leaf)
			starfleet.AddCommand(managed)
			root.AddCommand(starfleet)

			got := lostSpellingViolations(root)
			if len(got) == 0 {
				t.Fatal("an inherited --interval reached managed " +
					"scope unflagged")
			}
			for _, v := range got {
				if !strings.Contains(v, "--window") {
					t.Errorf("violation should name the replacement: %q", v)
				}
			}
		})

	t.Run("scoped ban stays silent outside its scope", func(t *testing.T) {
		got := lostSpellingViolations(
			build([]string{"starfleet", "byoc"}, "interval"))
		if len(got) != 0 {
			t.Fatalf("byoc's --interval is the ruled byoc spelling "+
				"and must not be flagged: %v", got)
		}
	})
}
