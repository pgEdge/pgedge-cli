package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestCompleteFixed(t *testing.T) {
	fn := completeFixed("public", "private")
	got, dir := fn(nil, nil, "")
	if len(got) != 2 || got[0] != "public" || got[1] != "private" {
		t.Errorf("got %v, want [public private]", got)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", dir)
	}
}

func TestCompleteFirewallRuleName(t *testing.T) {
	got, dir := completeFirewallRuleName(nil, nil, "")
	want := map[string]bool{
		"name=http": true, "name=https": true,
		"name=postgres": true, "name=ssh": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected candidate %q", g)
		}
	}
	if dir&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Error("expected NoSpace so more key=value can be typed")
	}

	// Once the user has typed a key=value, offer nothing fixed.
	got2, _ := completeFirewallRuleName(nil, nil, "name=https,")
	if len(got2) != 0 {
		t.Errorf("expected no candidates past '=', got %v", got2)
	}
}

// TestClusterCommandsRegisterEnumCompletion proves the cluster create
// and update commands actually wire their enum-completion functions to
// the flags, not just that the completion functions work in isolation.
// A removed or renamed RegisterFlagCompletionFunc call must fail this
// test.
func TestClusterCommandsRegisterEnumCompletion(t *testing.T) {
	create := newClusterCreateCmd(nil)
	for _, name := range []string{"node-location", "firewall-rule"} {
		if _, ok := create.GetFlagCompletionFunc(name); !ok {
			t.Errorf("create: no completion registered for --%s", name)
		}
	}

	update := newClusterUpdateCmd(nil)
	if _, ok := update.GetFlagCompletionFunc("firewall-rule"); !ok {
		t.Error("update: no completion registered for --firewall-rule")
	}
	if _, ok := update.GetFlagCompletionFunc("node-location"); ok {
		t.Error("update: --node-location does not exist; " +
			"expected no completion registered")
	}
}
