package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// TestPureRoutersProduceOutput exercises the bare (no-args)
// invocation of every byoc group command's RunE in-package, so
// coverage.out credits this package: `go test ./...` runs with no
// -coverpkg, so the equivalent check in internal/clitest (which
// walks this same tree via FullTree) executes this code at runtime
// but is attributed entirely to internal/clitest's own profile.
//
// The module root is attached under a synthetic parent before
// walking: WalkPureRouters skips any command with no parent (the
// same rule that exempts the real pgedge root), and NewByocCmd on
// its own builds "byoc" with no parent at all, which would silently
// skip checking "byoc" itself — the module root's own bare-invocation
// closure, not just its children's.
func TestPureRoutersProduceOutput(t *testing.T) {
	top := &cobra.Command{Use: "top"}
	top.AddCommand(NewByocCmd(&module.Runtime{}))
	testsupport.WalkPureRouters(t, top)
}
