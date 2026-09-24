package main

import (
	"strings"
	"testing"
)

// TestUnknownSubcommandSuggestsANearMiss is what proves main.go calls
// cli.AddUnknownCommandSuggestions (#292).
//
// internal/clitest's gate walks clitest.FullTree, which mirrors this
// wiring — so it would pass whether or not main.go carried the line.
// Only running the real entry point can tell, which is why this lives
// here and goes through runSubprocess rather than building a tree.
//
// Depth 1 is included deliberately. The fix must not change it: the
// root already suggested through cobra's legacyArgs, and the failure
// this row would catch is the wrapper being applied to the root as
// well, which would print the hint twice.
func TestUnknownSubcommandSuggestsANearMiss(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{{
		name: "depth 1 was already suggesting",
		args: []string{"pgedge", "starflet"},
		want: "starfleet",
	}, {
		name: "depth 2",
		args: []string{"pgedge", "starfleet", "tenat"},
		want: "tenant",
	}, {
		name: "depth 3",
		args: []string{"pgedge", "starfleet", "byoc", "databse"},
		want: "database",
	}, {
		// The path #285 made reachable, and the reason this was worth
		// doing now: appending --help used to print the parent's help
		// at exit 0, and now reaches the one diagnostic that offered
		// no near-miss.
		name: "with --help appended",
		args: []string{"pgedge", "starfleet", "tenat", "--help"},
		want: "tenant",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeProfilesConfig(t, home, "alpha", []string{"alpha"})
			stdout, stderr, code := runSubprocess(t, home, tc.args)
			if code != 2 {
				t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
			}
			if !strings.Contains(stderr, "Did you mean this?") {
				t.Errorf("no near-miss offered: %q", stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("hint does not name %q: %q", tc.want, stderr)
			}
			// Exactly once. Applying the wrapper to the root as well
			// would append a second hint to the message legacyArgs
			// already built, and the depth-1 row is the only place that
			// could show up.
			if n := strings.Count(stderr, "Did you mean this?"); n != 1 {
				t.Errorf("hint appears %d times, want 1: %q", n, stderr)
			}
			// A mistyped subcommand must not print a help dump to
			// stdout: that is what made the same input read as a
			// success to anything parsing stdout (#241).
			if strings.Contains(stdout, "Usage:") {
				t.Errorf("printed a help dump to stdout:\n%s", stdout)
			}
		})
	}
}

// TestLeafHelpStillWorks is the inverse, and it is not decoration: the
// suggestion wrapper runs on the same Args validators StrayArgsOnHelp
// consults, so a mistake there would turn every `--help` on a command
// with a required argument into a usage error.
func TestLeafHelpStillWorks(t *testing.T) {
	for _, args := range [][]string{
		{"pgedge", "starfleet", "tenant", "get", "--help"},
		{"pgedge", "starfleet", "byoc", "database", "--help"},
		{"pgedge", "controlplane", "database", "restore", "--help"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			home := t.TempDir()
			writeProfilesConfig(t, home, "alpha", []string{"alpha"})
			stdout, stderr, code := runSubprocess(t, home, args)
			if code != 0 {
				t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("no help printed:\n%s", stdout)
			}
			if strings.Contains(stderr, "Did you mean this?") {
				t.Errorf("offered a near-miss on a valid help "+
					"request: %q", stderr)
			}
		})
	}
}
