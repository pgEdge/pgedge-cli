package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// TestPureRoutersProduceOutput exercises the bare (no-args)
// invocation of every account group command's RunE in-package, so
// coverage.out credits this package: `go test ./...` runs with no
// -coverpkg, so the equivalent check in internal/clitest (which
// walks this same tree via FullTree) executes this code at runtime
// but is attributed entirely to internal/clitest's own profile.
//
// The commands are walked under newStarfleetRoot's synthetic parent:
// WalkPureRouters skips any command with no parent (the same rule
// that exempts the real pgedge root), so a group built standalone
// would silently skip checking itself — the group's own
// bare-invocation closure, not just its children's.
func TestPureRoutersProduceOutput(t *testing.T) {
	top := &cobra.Command{Use: "top"}
	top.AddCommand(newStarfleetRoot(&module.Runtime{}))
	testsupport.WalkPureRouters(t, top)
}
