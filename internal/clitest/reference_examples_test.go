package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/docgen"
)

// TestEveryLeafCarriesAnExtractableExample is the positive control on
// docgen.ExampleSection.
//
// The extractor returns "" for a Long with no "Example:" heading, and
// a silent "" is indistinguishable from a leaf that genuinely has no
// example: an extractor broken in any way that stops it matching the
// heading would empty every reference page and nothing else would
// notice, because `make docs-check` compares the pages against
// whatever the generator currently produces. It is the same shape that
// let a first version of the example-runs gate read cmd.Example,
// examine nothing and pass.
//
// So this asserts the population from the tree rather than from a
// list, and counts what it found.
func TestEveryLeafCarriesAnExtractableExample(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var leaves, withExample int
	walk(root, func(c *cobra.Command) {
		if c.HasSubCommands() {
			return
		}
		leaves++
		path := c.CommandPath()
		if docgen.ExampleSection(c.Long) == "" {
			t.Errorf("%s: no example extractable from Long", path)
			return
		}
		withExample++
	})

	if leaves < 100 {
		t.Fatalf("walked only %d leaves; the walk is not reaching "+
			"the tree", leaves)
	}
	t.Logf("%d of %d leaves carry an extractable example",
		withExample, leaves)
}

// exemptSelfInvocation names the leaves whose example deliberately
// shows a DIFFERENT command, because the documented one cannot
// succeed with the credentials the CLI supports and always exits 5.
// Both call errUserSessionRequired (internal/starfleet/account/cmd/
// invite.go): accepting an invite has to record which user joined,
// and a machine credential names no user. An example demonstrating
// either would be an example of a guaranteed failure, so each shows
// `invite list` instead.
//
// The map is checked for staleness below: an entry naming a path that
// no longer exists, or one whose example has since started invoking
// its own command, fails rather than lingering.
var exemptSelfInvocation = map[string]string{
	"pgedge starfleet invite create": "needs a signed-in user",
	"pgedge starfleet invite accept": "needs a signed-in user",
}

// TestExtractedExamplesInvokeTheCommandTheyDocument catches an
// extractor that matches the heading but returns the prose above it,
// which the count in the test above would pass.
//
// It asserts that at least ONE line invokes the command itself, not
// that every line does. Several examples open with a related command
// that sets the scene, and those are the better examples for it:
// `controlplane database update` shows the `database get` that writes
// the spec file first, and both `invite` writes show the `invite
// list` that finds the id.
func TestExtractedExamplesInvokeTheCommandTheyDocument(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var checked int
	seenExempt := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		if c.HasSubCommands() {
			return
		}
		ex := docgen.ExampleSection(c.Long)
		if ex == "" {
			return
		}
		checked++
		path := c.CommandPath()
		// Token boundary, not a bare prefix: `controlplane cluster
		// join` is a string prefix of `... join-token`, so a plain
		// HasPrefix would let join pass on an example that only ever
		// shows join-token. Nothing exploits that today; it is one
		// authoring edit from mattering.
		invokes := false
		for _, line := range strings.Split(ex, "\n") {
			line = strings.TrimSpace(line)
			if line == path || strings.HasPrefix(line, path+" ") {
				invokes = true
				break
			}
		}
		_, exempt := exemptSelfInvocation[path]
		switch {
		case invokes && exempt:
			// Marked seen either way, so one fault reports once
			// rather than tripping the staleness loop below as well.
			seenExempt[path] = true
			t.Errorf("%s: exempt from self-invocation but its example "+
				"now invokes it; drop the exemption", path)
		case !invokes && !exempt:
			t.Errorf("%s: no example line invokes it:\n%s", path, ex)
		default:
			seenExempt[path] = true
		}
	})
	if checked < 100 {
		t.Fatalf("checked only %d examples; the walk is not reaching "+
			"the tree", checked)
	}
	for path := range exemptSelfInvocation {
		if !seenExempt[path] {
			t.Errorf("%s: exempted but not found in the tree", path)
		}
	}
}
