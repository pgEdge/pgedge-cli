package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewCloudAccountCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewCloudAccountCmd(rt)

	if cmd.Use != "cloud-account" {
		t.Errorf("Use = %q, want \"cloud-account\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "cloud-accounts" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"cloud-accounts\"",
			cmd.Aliases)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false,
		"delete": false, "cloudformation-template": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("cloud-account command missing subcommand %q", name)
		}
	}
}

func TestCloudAccountCreateRequiresType(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "cloud-account", "create")
	if err == nil {
		t.Fatal("expected error when --type missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}
