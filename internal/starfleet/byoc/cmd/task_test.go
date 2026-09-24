package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewTaskCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewTaskCmd(rt)

	if cmd.Use != "task" {
		t.Errorf("Use = %q, want \"task\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "tasks" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"tasks\"", cmd.Aliases)
	}

	want := map[string]bool{"list": false, "get": false, "wait": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("task command missing subcommand %q", name)
		}
	}
}
