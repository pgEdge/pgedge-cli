package conn

import (
	"io"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// TestErrBodyTransportIsAlwaysInstalled guards the regression that
// would make the error-body repair invisible on a normal run.
// HTTPClientFor used to short-circuit to http.DefaultClient whenever
// logging was off and no dry run was active — which is every ordinary
// command, precisely the case issue #140 was reported against.
//
// It lives here rather than beside the transport because it is a claim
// about THIS module's client layering, not about the repair itself.
// internal/errbody now serves the controlplane module too, and cp has its own
// copy of this guard over its own constructor; a shared transport
// cannot assert where any particular caller installed it.
func TestErrBodyTransportIsAlwaysInstalled(t *testing.T) {
	// Quiet on purpose: no --verbose, no --debug, no --dry-run.
	rt := &module.Runtime{Stderr: io.Discard}
	c := HTTPClientFor(rt, RequestTimeout)
	if c.Transport == nil {
		t.Fatal("quiet client has no transport; the error-body repair " +
			"cannot run")
	}
	if _, ok := c.Transport.(*errbody.Transport); !ok {
		t.Fatalf("quiet client's outermost transport is %T, want "+
			"*errbody.Transport", c.Transport)
	}
}
