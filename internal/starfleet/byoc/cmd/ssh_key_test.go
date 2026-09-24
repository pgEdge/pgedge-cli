package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewSSHKeyCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewSSHKeyCmd(rt)

	if cmd.Use != "ssh-key" {
		t.Errorf("Use = %q, want \"ssh-key\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "ssh-keys" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"ssh-keys\"", cmd.Aliases)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false, "delete": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("ssh-key command missing subcommand %q", name)
		}
	}
}

func TestSSHKeyCreateRequiresFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "ssh-key", "create")
	if err == nil {
		t.Fatal("expected error when required flags missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}

func TestSSHKeyGetRequiresArg(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "ssh-key", "get")
	if err == nil {
		t.Fatal("expected error when no arg provided")
	}
}

func TestSSHKeyDeleteRequiresArg(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "ssh-key", "delete")
	if err == nil {
		t.Fatal("expected error when no arg provided")
	}
}

func TestSSHKeyHelp(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	if err := runByoc(t, rt, &out, "ssh-key", "--help"); err != nil {
		t.Fatalf("ssh-key help: %v", err)
	}
	if !strings.Contains(out.String(), "ssh-key") {
		t.Errorf("help missing 'ssh-key': %q", out.String())
	}
}
