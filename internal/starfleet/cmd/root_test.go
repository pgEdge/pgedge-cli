package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// runStarfleet builds the starfleet tree for rt and executes it with the given
// args, capturing cobra's own output into out.
func runStarfleet(t *testing.T, rt *module.Runtime,
	out *bytes.Buffer, args ...string,
) error {
	t.Helper()
	cmd := NewStarfleetCmd(rt)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

func TestNewStarfleetCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	cmd := NewStarfleetCmd(rt)

	if cmd.Use != "starfleet" {
		t.Errorf("Use = %q, want \"cloud\"", cmd.Use)
	}
	if cmd.Short == "" || len(cmd.Short) >= 60 {
		t.Errorf("Short = %q (%d chars), want non-empty and < 60",
			cmd.Short, len(cmd.Short))
	}
	// Every pgedge command's Long must carry a runnable example; the
	// tree conformance gate only demands a Long on leaves, so lock it
	// here for the group this module roots.
	if !strings.Contains(cmd.Long, "Example:") {
		t.Errorf("Long has no \"Example:\" section:\n%s", cmd.Long)
	}
}

// TestStarfleetCmdMountsEverySubtree pins the merge itself: the
// account-level commands and both infrastructure sub-trees hang off
// this one root. A dropped AddCommand would only show up in the
// generated reference otherwise.
func TestStarfleetCmdMountsEverySubtree(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	got := map[string]bool{}
	for _, sub := range NewStarfleetCmd(rt).Commands() {
		got[sub.Name()] = true
	}
	for _, want := range []string{
		"auth", "doctor", "client", "tenant", "invite", "membership",
		"byoc", "managed",
	} {
		if !got[want] {
			t.Errorf("`cloud %s` is not mounted on the starfleet root", want)
		}
	}
}

// TestStarfleetCmdPersistentFlags pins the three credential/base-URL
// overrides as *persistent* flags on the starfleet root, and only there.
// Registering them as local flags would still pass a help-text check
// but would stop every subcommand — including the byoc and managed
// sub-trees, which no longer declare their own — from inheriting them,
// which is how clientFromCmd reads them off the leaf's inherited flag
// set.
func TestStarfleetCmdPersistentFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	cmd := NewStarfleetCmd(rt)

	for _, name := range []string{"api-url", "client-id", "client-secret"} {
		f := cmd.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not a persistent flag on `starfleet`", name)
			continue
		}
		if f.DefValue != "" {
			t.Errorf("--%s default = %q, want empty so profile "+
				"config wins when the flag is unset", name, f.DefValue)
		}
		if f.Usage == "" {
			t.Errorf("--%s has no usage text", name)
		}
	}
}

// TestSubtreesDeclareNoConnectionFlags is the other half of the
// single-declaration rule: byoc and managed must NOT re-declare the
// connection flags. A duplicate declaration compiles and mostly works,
// but shadows the root's value at the sub-tree boundary and renders a
// second copy of the same flags row in help output.
func TestSubtreesDeclareNoConnectionFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	for _, sub := range NewStarfleetCmd(rt).Commands() {
		if sub.Name() != "byoc" && sub.Name() != "managed" {
			continue
		}
		for _, name := range []string{
			"api-url", "client-id", "client-secret",
		} {
			if f := sub.PersistentFlags().Lookup(name); f != nil {
				t.Errorf("`cloud %s` re-declares --%s; the starfleet root "+
					"owns the connection flags", sub.Name(), name)
			}
		}
	}
}

// TestSubtreeLeavesInheritConnectionFlags proves the inheritance the
// previous two tests only imply: a leaf deep inside byoc resolves
// --api-url against the starfleet root's declaration. This is what
// connFlags reads at run time, so a broken inheritance chain would
// silently strip every per-invocation override.
func TestSubtreeLeavesInheritConnectionFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	root := NewStarfleetCmd(rt)
	leaf, _, err := root.Find([]string{"byoc", "cluster", "list"})
	if err != nil {
		t.Fatalf("find `starfleet byoc cluster list`: %v", err)
	}
	if err := leaf.ParseFlags([]string{"--api-url", "https://x.invalid"}); err != nil {
		t.Fatalf("parse --api-url on a byoc leaf: %v", err)
	}
	got, err := leaf.Flags().GetString("api-url")
	if err != nil {
		t.Fatalf("read --api-url off the leaf: %v", err)
	}
	if got != "https://x.invalid" {
		t.Errorf("--api-url = %q, want the parsed value", got)
	}
}

// TestStarfleetCmdIsAvailable pins the first of the two reasons `starfleet`
// carries a RunE. Cobra lists a child under the parent's "Available
// Commands" only when IsAvailableCommand() holds — when the command is
// runnable or has available subcommands. A group with neither is
// registered, versioned and reachable but absent from `pgedge --help`,
// so users cannot find it.
func TestStarfleetCmdIsAvailable(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	if !NewStarfleetCmd(rt).IsAvailableCommand() {
		t.Error("`starfleet` is not an available command, so cobra will " +
			"omit it from `pgedge --help`")
	}
}

// TestStarfleetCmdPrintsHelp exercises the group with no arguments: it
// prints its own help, including the usage/flags block, and returns
// nil. The Long alone would satisfy the `--api-url` substrings, so the
// "Usage:"/"Flags:" anchors are asserted too — those come only from
// cobra's UsageString, which a non-runnable childless command omits.
func TestStarfleetCmdPrintsHelp(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	out := &bytes.Buffer{}

	if err := runStarfleet(t, rt, out); err != nil {
		t.Fatalf("runStarfleet() error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"starfleet manages pgEdge Starfleet",
		"pgedge starfleet auth login",
		"Usage:",
		"Flags:",
		"--api-url",
		"--client-id",
		"--client-secret",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q:\n%s", want, got)
		}
	}
}

// TestStarfleetCmdRejectsStrayArg pins the second reason for the RunE: it
// is what makes Args: cobra.NoArgs reachable. Cobra returns
// flag.ErrHelp for a non-runnable command before it ever calls
// ValidateArgs, so without RunE a stray argument prints help and exits
// 0 instead of failing. The tree-wide form of this gate lives in
// internal/clitest and testsupport.WalkPureRouters.
func TestStarfleetCmdRejectsStrayArg(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	out := &bytes.Buffer{}

	if err := runStarfleet(t, rt, out, "bogus"); err == nil {
		t.Fatalf("expected an error for a stray argument, got nil:\n%s",
			out.String())
	}
}

// TestStarfleetCmdAcceptsCredentialFlags proves the persistent flags parse
// on the root itself. A missing or local-only registration surfaces as
// cobra's "unknown flag" parse error, so a nil error is the assertion —
// TestStarfleetCmdUnknownFlag is the negative control that proves an
// unparseable flag really does fail.
func TestStarfleetCmdAcceptsCredentialFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	out := &bytes.Buffer{}

	// No stub server is contacted: with no subcommand, cobra parses the
	// flags and prints help without making a request.
	if err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out,
		"https://api.invalid"); err != nil {
		t.Fatalf("RunAuthed() error: %v", err)
	}
	if !strings.Contains(out.String(), "starfleet manages pgEdge Starfleet") {
		t.Errorf("expected help output, got:\n%s", out.String())
	}
}

func TestStarfleetCmdUnknownFlag(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	out := &bytes.Buffer{}

	if err := runStarfleet(t, rt, out, "--nope"); err == nil {
		t.Fatal("expected an error for an unknown flag, got nil")
	}
}

// TestPureRoutersProduceOutput exercises the bare (no-args) invocation
// of the starfleet root's own RunE in-package, so coverage.out credits
// this package: `go test ./...` runs with no -coverpkg, so the
// equivalent check in internal/clitest executes this code at runtime
// but is attributed entirely to internal/clitest's own profile.
//
// The root is attached under a synthetic parent before walking:
// WalkPureRouters skips any command with no parent (the same rule that
// exempts the real pgedge root), and NewStarfleetCmd on its own has none.
func TestPureRoutersProduceOutput(t *testing.T) {
	top := &cobra.Command{Use: "top"}
	top.AddCommand(NewStarfleetCmd(&module.Runtime{}))
	testsupport.WalkPureRouters(t, top)
}
