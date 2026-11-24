package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// TestEveryLeafUsageHintCarriesAnExample holds every leaf to an example
// of itself under a usage error. A leaf whose Example section never
// invokes it gets a usage line alone, and the caller a --help call.
func TestEveryLeafUsageHintCarriesAnExample(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	// invite accept always refuses: its only example is the verb to run
	// instead, which the refusal names.
	excused := map[string]bool{"pgedge starfleet invite accept": true}
	var leaves int
	walk(root, func(c *cobra.Command) {
		if excused[c.CommandPath()] {
			delete(excused, c.CommandPath())
			return
		}
		if c.HasSubCommands() || c.Hidden || c.Name() == "help" {
			return
		}
		leaves++
		hint := cli.UsageHint(c)
		want := "\nExample: " + c.CommandPath()
		if !strings.Contains(hint, want) {
			t.Errorf("%s: usage hint has no example of itself:\n%s",
				c.CommandPath(), hint)
		}
	})
	for path := range excused {
		t.Errorf("%s is excused but not in the tree", path)
	}
	if leaves < 100 {
		t.Fatalf("walked only %d leaves; the walk is broken", leaves)
	}
}
