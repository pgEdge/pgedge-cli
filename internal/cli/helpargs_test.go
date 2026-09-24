package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestStrayArgsOnHelp covers the predicate directly. The rows are the
// whole decision: only an invocation that asked for help AND handed a
// command that HAS subcommands a positional its own Args validator
// rejects is a usage error.
//
// childless is what distinguishes an unresolvable path element from an
// ordinary argument. A command with no subcommands cannot have been
// handed a subcommand name, so its positionals — present, absent or
// too many — are none of this predicate's business.
func TestStrayArgsOnHelp(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		childless bool
		wantErr   string
	}{
		{
			name: "no help flag, no stray argument",
			args: []string{},
		},
		{
			// Without --help cobra reaches ValidateArgs on its own, so
			// the predicate must stay out of the way: reporting here
			// would double-report the error cobra already returns.
			name: "stray argument without a help flag",
			args: []string{"zzz-no-such-thing"},
		},
		{
			name: "help flag, no stray argument",
			args: []string{"--help"},
		},
		{
			name:    "help flag with a stray argument",
			args:    []string{"zzz-no-such-thing", "--help"},
			wantErr: `unknown command "zzz-no-such-thing"`,
		},
		{
			name:    "short help flag with a stray argument",
			args:    []string{"zzz-no-such-thing", "-h"},
			wantErr: `unknown command "zzz-no-such-thing"`,
		},
		{
			// The row that caught the first attempt at this fix.
			// `pgedge starfleet tenant get --help` is how you find out that
			// get takes an ID, so its ExactArgs(1) must not fire on a
			// help request that has not supplied one.
			name:      "childless command, help flag, no argument",
			args:      []string{"--help"},
			childless: true,
		},
		{
			// This row is what pins the !HasSubCommands gate, and it
			// only does so with TWO positionals: one is what
			// ExactArgs(1) accepts anyway, so a single stray word here
			// passes whether the gate exists or not. Two exceeds the
			// validator, so deleting the gate makes this row fail —
			// which is the whole point of having it.
			//
			// A leaf's arguments are not command names, so an extra one
			// is not an unresolved path element. Out of scope here, and
			// left to the ordinary path, which still rejects it.
			name:      "childless command, help flag, extra argument",
			args:      []string{"its-id", "zzz-no-such-thing", "--help"},
			childless: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := cobra.PositionalArgs(cobra.NoArgs)
			if tt.childless {
				args = cobra.ExactArgs(1)
			}
			group := &cobra.Command{
				Use:  "group",
				Args: args,
				RunE: func(c *cobra.Command, _ []string) error {
					return c.Help()
				},
			}
			if !tt.childless {
				group.AddCommand(&cobra.Command{Use: "child", Run: func(
					*cobra.Command, []string,
				) {
				}})
			}
			group.InitDefaultHelpFlag()
			if err := group.ParseFlags(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			err := StrayArgsOnHelp(group)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("StrayArgsOnHelp = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("StrayArgsOnHelp = nil, want %q", tt.wantErr)
			case tt.wantErr != "" &&
				!strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("StrayArgsOnHelp = %q, want substring %q",
					err, tt.wantErr)
			}
		})
	}
}

// TestStrayArgsOnHelpNilCommand pins the nil guard. main.run passes
// whatever ExecuteC returned, and cobra's contract does not promise a
// non-nil command on every path.
func TestStrayArgsOnHelpNilCommand(t *testing.T) {
	if err := StrayArgsOnHelp(nil); err != nil {
		t.Errorf("StrayArgsOnHelp(nil) = %v, want nil", err)
	}
}

// TestStrayArgsOnHelpWithoutHelpFlag covers a command whose flag set
// carries no help flag at all — reachable before
// InitDefaultHelpFlag runs, so GetBool("help") errors rather than
// returning false, and the predicate must treat that as "help was not
// asked for" rather than reporting a spurious usage error.
// It carries a child so the absent help flag is the only gate that can
// account for the nil.
func TestStrayArgsOnHelpWithoutHelpFlag(t *testing.T) {
	group := &cobra.Command{Use: "group", Args: cobra.NoArgs}
	group.AddCommand(&cobra.Command{Use: "child", Run: func(
		*cobra.Command, []string,
	) {
	}})
	if err := group.ParseFlags([]string{"stray"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := StrayArgsOnHelp(group); err != nil {
		t.Errorf("StrayArgsOnHelp = %v, want nil", err)
	}
}

// TestStrayArgsOnHelpHybrid pins the shape of the one hybrid in the
// tree: `pgedge controlplane database restore <database_id>` has subcommands AND
// declares a required positional of its own. Asking the command's own
// validator is what accepts it; hardcoding cobra.NoArgs for anything
// with subcommands would reject the argument it documents.
func TestStrayArgsOnHelpHybrid(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "its own argument is accepted",
			args: []string{"some-id", "--help"}},
		{name: "one argument too many is not",
			args: []string{"some-id", "extra", "--help"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hybrid := &cobra.Command{
				Use:  "restore <database_id>",
				Args: cobra.ExactArgs(1),
				RunE: func(*cobra.Command, []string) error { return nil },
			}
			hybrid.AddCommand(&cobra.Command{Use: "child", Run: func(
				*cobra.Command, []string,
			) {
			}})
			hybrid.InitDefaultHelpFlag()
			if err := hybrid.ParseFlags(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			err := StrayArgsOnHelp(hybrid)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrayArgsOnHelp = %v, want error: %v",
					err, tt.wantErr)
			}
		})
	}
}

// TestRootHelpSuppressedForStrayArgument is the second half of the fix:
// the diagnostic and the exit code are main.run's, but the help dump
// has to be suppressed here or the error lands underneath a screenful
// of the parent's help and a caller reading stdout still sees a
// successful-looking lookup (#241).
func TestRootHelpSuppressedForStrayArgument(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHelp bool
	}{
		{
			name:     "stray argument suppresses the dump",
			args:     []string{"profile", "zzz-no-such-thing", "--help"},
			wantHelp: false,
		},
		{
			// The negative control that matters most: a real group
			// still prints its help. Suppressing too much would be a
			// worse regression than the bug.
			name:     "real group still prints help",
			args:     []string{"profile", "--help"},
			wantHelp: true,
		},
		{
			name:     "real leaf still prints help",
			args:     []string{"profile", "list", "--help"},
			wantHelp: true,
		},
		{
			name:     "root still prints help",
			args:     []string{"--help"},
			wantHelp: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cmd := NewRootCmd(nil)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			gotHelp := strings.Contains(out.String(), "Usage:")
			if gotHelp != tt.wantHelp {
				t.Errorf("help printed = %v, want %v; output=%q",
					gotHelp, tt.wantHelp, out.String())
			}
		})
	}
}
