package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewMembershipCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewMembershipCmd(rt)

	if cmd.Use != "membership" {
		t.Errorf("Use = %q, want \"membership\"", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "memberships" {
		t.Errorf("Aliases = %v, want first alias \"memberships\"",
			cmd.Aliases)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{"list": false, "delete": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("membership command missing subcommand %q", name)
		}
	}
}

func TestMembershipDeleteRequiresArg(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runAccount(t, rt, &out, "membership", "delete")
	if err == nil {
		t.Fatal("expected error when no arg provided")
	}
}
