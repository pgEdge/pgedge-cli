package clitest

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestRootOwnsTheOnlySetupHook is the fragility gate for the
// setup-hook design: root's PersistentPreRunE (internal/cli.setupRuntime, wired
// in NewRootCmd) is the ONLY place in the tree that populates the
// Runtime from cobra's parsed flags. The failure mode this guards
// against: a future command defines its own PersistentPreRun(E) or
// PreRun(E), a maintainer assumes that replaces root's setup rather
// than running alongside it, drifts the Runtime's population logic
// out of internal/cli/setup.go, and the split silently invites the
// two to disagree. cobra.EnableTraverseRunHooks (set next to the hook
// in internal/cli.NewRootCmd) is what keeps root's own hook from ever
// being skipped even if that happens — but this gate is what keeps it
// from happening at all, by holding the tree to "root, and only root,
// owns a run hook".
func TestRootOwnsTheOnlySetupHook(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	if root.PersistentPreRunE == nil {
		t.Fatal("root has no PersistentPreRunE: the setup hook " +
			"is missing from internal/cli.NewRootCmd")
	}

	// The other half of the pairing internal/cli.NewRootCmd presents,
	// and the half nothing asserted before: cobra runs only the NEAREST
	// ancestor's PersistentPreRun(E) unless this global is set. With it
	// false, the first module command to define its own hook would
	// shadow root's, setupRuntime would never run, rt.Config would stay
	// nil, and the next dereference would panic — a nil-deref at
	// runtime, not a build failure, and not something the walk below
	// can see. The walk keeps that command from being written; this
	// keeps the belt-and-braces that survives it being written anyway.
	// FullTree() above is what runs NewRootCmd and so sets this, which
	// is why the assertion has to come after it.
	if !cobra.EnableTraverseRunHooks {
		t.Error("cobra.EnableTraverseRunHooks is false — internal/cli." +
			"NewRootCmd sets it true next to the setup hook, and " +
			"without it any command defining its own run hook silently " +
			"skips root's, leaving the Runtime unpopulated for a nil " +
			"dereference at run time")
	}

	visited := 0
	var violations []string
	walk(root, func(c *cobra.Command) {
		visited++
		if c == root {
			return
		}
		if c.PersistentPreRun != nil || c.PersistentPreRunE != nil ||
			c.PreRun != nil || c.PreRunE != nil {
			violations = append(violations, c.CommandPath())
		}
	})
	// A walk that silently visits nothing (a broken FullTree, an
	// AddCommand that stopped wiring modules in) would make the
	// violations check above vacuously pass. The shipped tree carries
	// well over 100 commands across cloud and cp; asserting the count
	// is what keeps this gate honest.
	if visited <= 100 {
		t.Fatalf("walk visited only %d commands — the walk is broken "+
			"and this gate is checking nothing", visited)
	}
	if len(violations) > 0 {
		t.Errorf("these commands define their own run hook, which "+
			"must live in internal/cli/setup.go's single hook instead: "+
			"%v", violations)
	}
}
