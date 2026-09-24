package starfleet

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// TestModuleContract pins the five Module methods. A module that
// reports the wrong name or an empty Short is registered wrong, and
// `pgedge llms` builds its routing table from Describe.
func TestModuleContract(t *testing.T) {
	m := &Module{}

	if got := m.Name(); got != "starfleet" {
		t.Errorf("Name() = %q, want cloud", got)
	}
	if got := m.Short(); got == "" || len(got) >= 60 {
		t.Errorf("Short() = %q; must be non-empty and under 60 chars", got)
	}

	info := m.Describe()
	if info.Name != m.Name() || info.Short != m.Short() {
		t.Error("Describe disagrees with Name/Short")
	}
	if info.Version != Version {
		t.Errorf("Version = %q, want %q", info.Version, Version)
	}
	if info.ContractVersion != "" {
		t.Errorf("ContractVersion = %q; it is reserved and must be empty",
			info.ContractVersion)
	}
}

// TestReferenceIsShippedAndDeclared pins the two halves that must
// agree: a module reporting ProvidesLLMS must return a reference, and
// one returning a reference must report it. `pgedge llms` offers the
// module based on the flag and prints the bytes, so a mismatch either
// hides the document or offers an empty one.
func TestReferenceIsShippedAndDeclared(t *testing.T) {
	m := &Module{}
	ref := m.Reference()

	if len(ref) == 0 {
		t.Fatal("cloud ships no reference; if that is deliberate, " +
			"Describe must report ProvidesLLMS false")
	}
	if !m.Describe().ProvidesLLMS {
		t.Error("a reference is embedded but ProvidesLLMS is false")
	}
	if !strings.HasPrefix(string(ref), "# pgedge starfleet") {
		t.Error("the reference does not start with its own title")
	}
}

// TestCommandBuilds pins that Command returns a usable tree, rooted at
// "starfleet", for a Runtime the build gates can supply.
func TestCommandBuilds(t *testing.T) {
	rt := &module.Runtime{
		Config:  &config.Config{},
		Profile: "default",
		Output:  &output.Renderer{Format: "table"},
	}
	c, err := (&Module{}).Command(rt)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if c == nil {
		t.Fatal("Command returned nil")
	}
	if c.Name() != "starfleet" {
		t.Errorf("command name = %q, want cloud", c.Name())
	}
	if !c.HasSubCommands() {
		t.Error("the starfleet tree has no subcommands")
	}

	// The three sub-trees the merge exists to unify must all be
	// reachable from the one root; a missing AddCommand would otherwise
	// only surface in the generated reference.
	for _, want := range []string{"auth", "doctor", "tenant", "client",
		"invite", "membership", "byoc", "managed"} {
		found := false
		for _, sub := range c.Commands() {
			if sub.Name() == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("`cloud %s` is not mounted on the starfleet root", want)
		}
	}
}
