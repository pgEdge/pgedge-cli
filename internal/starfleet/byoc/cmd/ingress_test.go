package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewIngressCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewIngressCmd(rt)

	if cmd.Use != "ingress" {
		t.Errorf("Use = %q, want \"ingress\"", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "ingresses" {
		t.Errorf("Aliases = %v, want first alias \"ingresses\"",
			cmd.Aliases)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false,
		"delete": false, "service": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("ingress command missing subcommand %q", name)
		}
	}
}

func TestNewIngressServiceCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewIngressServiceCmd(rt)

	if cmd.Use != "service" {
		t.Errorf("Use = %q, want \"service\"", cmd.Use)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{
		"list": false, "register": false, "deregister": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("ingress service command missing subcommand %q", name)
		}
	}
}

func TestIngressCreateRequiresFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "ingress", "create")
	if err == nil {
		t.Fatal("expected error when required flags missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}
