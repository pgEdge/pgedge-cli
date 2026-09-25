package clitest

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// linkedReads is every verb whose <database_id> may be left out in a
// linked folder. Adding one is a decision, so the list is pinned here.
var linkedReads = []string{
	"pgedge starfleet managed database allowlist get",
	"pgedge starfleet managed database branch list",
	"pgedge starfleet managed database connection-string",
	"pgedge starfleet managed database env pull",
	"pgedge starfleet managed database get",
	"pgedge starfleet managed database inspect",
	"pgedge starfleet managed database logs",
	"pgedge starfleet managed database metrics",
}

// promptedWrites leave the database ID out only to ask a person for it
// on a terminal. Off one they exit 2, and none of them reads the link.
var promptedWrites = map[string]bool{
	"pgedge starfleet managed database link": true,
}

// TestNoWriteReadsTheProjectLink holds the rule that a verb which
// changes anything names its database: an unset variable in a script
// run inside a linked folder must fail, not reach the linked database.
// The writes are derived from the tree (the mutating annotation, or a
// --force confirmation), never listed.
func TestNoWriteReadsTheProjectLink(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	var optional, writes []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, k := range c.Commands() {
			walk(k)
		}
		if !strings.Contains(c.Use, "<database_id>") || promptedWrites[c.CommandPath()] {
			return
		}
		if strings.Contains(c.Use, "[<database_id>]") {
			optional = append(optional, c.CommandPath())
		}
		if c.Annotations[cli.MutatesAnnotation] == "true" || c.Flags().Lookup("force") != nil {
			writes = append(writes, c.CommandPath())
			if strings.Contains(c.Use, "[<database_id>]") {
				t.Errorf("%s is a write but its database ID is optional", c.CommandPath())
			}
			// cobra accepts any arguments when Args is nil, so a write
			// without a validator could be run with its ID left out.
			if c.Args == nil || c.Args(c, nil) == nil {
				t.Errorf("%s is a write but runs with no arguments", c.CommandPath())
			}
		}
	}
	walk(root)

	if len(writes) < 20 {
		t.Fatalf("found only %d writes taking <database_id>; has the walk broken?", len(writes))
	}
	sort.Strings(optional)
	if strings.Join(optional, "\n") != strings.Join(linkedReads, "\n") {
		t.Errorf("verbs with an optional database ID:\n%s\nwant:\n%s",
			strings.Join(optional, "\n"), strings.Join(linkedReads, "\n"))
	}
}
