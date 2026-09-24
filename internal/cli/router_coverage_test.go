package cli

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestPureRoutersProduceOutput exercises the bare (no-args)
// invocation of every root-level group command's RunE in-package
// (currently just "completion"; "profile" and root itself were
// already covered before this task), so coverage.out credits this
// package: `go test ./...` runs with no -coverpkg, so the equivalent
// check in internal/clitest (which walks this same tree via
// FullTree) executes this code at runtime but is attributed entirely
// to internal/clitest's own profile.
func TestPureRoutersProduceOutput(t *testing.T) {
	root := NewRootCmd(&module.Runtime{})
	testsupport.WalkPureRouters(t, root)
}
