package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runHelp executes `pgedge help <args...>` against a fresh root and
// returns everything it printed plus the error it returned.
func runHelp(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"help"}, args...))
	// Execute first, THEN read the buffer. Returning
	// `out.String(), root.Execute()` reads it before Execute has run —
	// Go evaluates a return statement's operands left to right — so
	// every rendering assertion saw an empty string and this helper
	// reported the command as broken when it was fine.
	err := root.Execute()
	return out.String(), err
}

// TestHelpRejectsUnknownTopics is the gate on the rank-9 defect. Both
// rows failed differently before the command was replaced, and only one
// of them was ever reported:
//
//   - "unknown topic" printed a diagnostic and exited 0.
//   - "stray after a valid topic" printed the GROUP's help and exited 0
//     with no diagnostic at all, because cobra's built-in discards the
//     args Find could not consume. That is the quieter and worse of the
//     two, and it is why checking Find's error alone is not enough.
//
// The error type is asserted, not the exit number: ExitCode owns the
// mapping and TestExitCodeContract proves the end-to-end number, so
// duplicating the literal 2 here would just be a second place to
// update.
func TestHelpRejectsUnknownTopics(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// want is the topic the message must quote back, so the
		// operator can see what was actually parsed.
		want string
	}{
		{
			name: "unknown top-level topic",
			args: []string{"zzz-no-such-thing"},
			want: "zzz-no-such-thing",
		},
		{
			name: "stray argument after a valid topic",
			args: []string{"profile", "zzz-no-such-thing"},
			want: "profile zzz-no-such-thing",
		},
		{
			name: "stray argument after a valid leaf",
			args: []string{"version", "zzz-no-such-thing"},
			want: "version zzz-no-such-thing",
		},
		{
			name: "deep stray argument",
			args: []string{"profile", "list", "zzz-no-such-thing"},
			want: "profile list zzz-no-such-thing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runHelp(t, tc.args...)
			if err == nil {
				t.Fatal("want an error for an unresolvable help topic")
			}
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v (%T), want *UsageError", err, err)
			}
			if ExitCode(err) != ExitUsage {
				t.Errorf("ExitCode = %d, want ExitUsage (%d)",
					ExitCode(err), ExitUsage)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not quote the topic %q",
					err, tc.want)
			}
		})
	}
}

// TestHelpResolvesRealTopics is the other half, and the one that would
// catch overcorrecting: tightening `help` must not cost it the ability
// to describe any real command. LooksLikeHelp-style content checks
// rather than a bare non-empty check, since a command that printed one
// stray character would satisfy "non-empty" while being broken.
func TestHelpResolvesRealTopics(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "bare", args: nil, want: "Usage:"},
		{name: "group", args: []string{"profile"}, want: "profile"},
		{name: "leaf", args: []string{"version"}, want: "version"},
		{name: "help itself", args: []string{"help"}, want: "help"},
		{
			name: "nested leaf",
			args: []string{"profile", "list"},
			want: "Usage:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runHelp(t, tc.args...)
			if err != nil {
				t.Fatalf("help %v returned an error: %v", tc.args, err)
			}
			if !strings.Contains(out, "Usage:") {
				t.Errorf("no help rendering for %v:\n%s", tc.args, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("help %v output missing %q:\n%s",
					tc.args, tc.want, out)
			}
		})
	}
}

// TestHelpCommandIsInTheTreeEagerly pins the registration detail that
// makes the fix reachable at all. cobra adds its help command lazily
// during Execute; ours is added in NewRootCmd. Two gates depend on
// that and neither would say so if it regressed — main.go's markRan
// only wraps hooks present when it walks the tree, and
// TestCommandTreeConformance only sees commands that exist before
// Execute. A lazily-added help command is invisible to both, which is
// how it stayed exempt from the stray-argument standard in the first
// place.
func TestHelpCommandIsInTheTreeEagerly(t *testing.T) {
	root := NewRootCmd(nil)

	var found *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("help is not in root.Commands() before Execute")
	}
	if found.RunE == nil {
		t.Error("help has no RunE: cobra's built-in uses Run, which " +
			"cannot report a bad topic at all")
	}
	if found.Long == "" || found.Short == "" {
		t.Error("help must carry Short and Long like every other leaf")
	}
}

// TestHelpTopicCompletions guards the part of replacing a built-in that
// no exit-code test would ever notice: cobra's help command ships a
// ValidArgsFunction, and dropping it would silently cost every user
// their `pgedge help <TAB>` completions.
func TestHelpTopicCompletions(t *testing.T) {
	root := NewRootCmd(nil)
	help, _, err := root.Find([]string{"help"})
	if err != nil {
		t.Fatalf("find help: %v", err)
	}
	if help.ValidArgsFunction == nil {
		t.Fatal("help lost its ValidArgsFunction")
	}

	names := func(cs []cobra.Completion) []string {
		var out []string
		for _, c := range cs {
			out = append(out, strings.SplitN(string(c), "\t", 2)[0])
		}
		return out
	}

	// Top level, unfiltered: the real subcommands must be offered.
	got, directive := help.ValidArgsFunction(help, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if !slicesContains(names(got), "version") {
		t.Errorf("completions %v missing version", names(got))
	}

	// Filtered by a prefix: positive and negative in one assertion, so
	// a function that ignored toComplete entirely cannot pass.
	got, _ = help.ValidArgsFunction(help, nil, "vers")
	n := names(got)
	if !slicesContains(n, "version") {
		t.Errorf("completions %v missing version for prefix 'vers'", n)
	}
	if slicesContains(n, "doctor") {
		t.Errorf("completions %v ignored the prefix filter", n)
	}

	// One level down, so a function that only ever completed the root's
	// children would be caught.
	got, _ = help.ValidArgsFunction(help, []string{"profile"}, "")
	if !slicesContains(names(got), "list") {
		t.Errorf("completions %v missing profile's children",
			names(got))
	}
}

func slicesContains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
