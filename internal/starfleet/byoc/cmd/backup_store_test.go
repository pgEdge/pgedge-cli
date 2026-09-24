package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewBackupStoreCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewBackupStoreCmd(rt)

	if cmd.Use != "backup-store" {
		t.Errorf("Use = %q, want \"backup-store\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "backup-stores" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"backup-stores\"", cmd.Aliases)
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
			t.Errorf("backup-store command missing subcommand %q", name)
		}
	}
}

func TestBackupStoreCreateRequiresFlags(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	var out bytes.Buffer
	err := runByoc(t, rt, &out, "backup-store", "create")
	if err == nil {
		t.Fatal("expected error when required flags missing")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want 'required' flag error", err.Error())
	}
}
