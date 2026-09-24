package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewBackupCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewBackupCmd(rt)

	if cmd.Use != "backup" {
		t.Errorf("Use = %q, want \"backup\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "backups" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"backups\"", cmd.Aliases)
	}

	// create is the only verb: the read, delete and download verbs sat
	// on bare /v1 paths with no namespaced equivalent and were removed
	// with the scrub. Asserting their *absence* keeps them from being
	// re-added against a retired surface.
	want := map[string]bool{"create": false}
	gone := map[string]bool{
		"list": false, "get": false, "delete": false, "url": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
		if _, ok := gone[name]; ok {
			t.Errorf("backup subcommand %q is on the retired bare /v1 "+
				"surface and must not exist", name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("backup command missing subcommand %q", name)
		}
	}
}

func TestBackupCreateRequiresFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "backup", "create")
	if err == nil {
		t.Fatal("expected error when required flags missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}
