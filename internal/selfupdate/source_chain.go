package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// The deadlines every Source call runs under. They live here, beside
// the interface, because a Source implementation must not invent its
// own budget: the caller decides how long the whole ladder may take,
// and both rungs share it.
//
// A gh rung execs a child process, which has no timeout of its own —
// an unauthenticated `gh` waiting on a prompt would otherwise hang
// the command forever.
const (
	// ListTimeout bounds a release-list fetch made by `self update`.
	ListTimeout = 30 * time.Second

	// DownloadTimeout bounds one asset download. Generous on purpose:
	// a release archive on a slow link is legitimately slow, and the
	// deadline is here to stop a hang, not to police throughput.
	DownloadTimeout = 10 * time.Minute

	// DoctorCheckTimeout bounds doctor's whole latest-version check —
	// both rungs together. doctor prints a row and moves on, so it
	// gives up far sooner than an update would.
	DoctorCheckTimeout = 5 * time.Second

	// ProbeTimeout bounds the post-swap `pgedge version -o json` exec.
	ProbeTimeout = 30 * time.Second
)

// Source fetches the release list and downloads a chosen asset.
// HTTPSource, GHSource and Chain all implement it.
type Source interface {
	Releases(ctx context.Context) ([]Release, error)
	Download(ctx context.Context, tag, assetName, dstDir string) (string, error)
}

// Chain tries primary, falling back to fallback when primary fails.
// Production wires HTTPSource as primary and GHSource as fallback
// (the design's "ladder"): the http rung 404s while
// pgEdge/pgedge-cli stays private, so every fetch rides gh, and the
// day the repo goes public the http rung starts succeeding with no
// code change on either side.
type Chain struct {
	primary  Source
	fallback Source
}

// NewChain builds a Chain trying primary first, then fallback.
func NewChain(primary, fallback Source) *Chain {
	return &Chain{primary: primary, fallback: fallback}
}

func (c *Chain) Releases(ctx context.Context) ([]Release, error) {
	releases, err := c.primary.Releases(ctx)
	if err == nil {
		return releases, nil
	}

	releases, fbErr := c.fallback.Releases(ctx)
	if fbErr == nil {
		return releases, nil
	}

	return nil, bothFailed(err, fbErr)
}

func (c *Chain) Download(
	ctx context.Context, tag, assetName, dstDir string,
) (string, error) {
	path, err := c.primary.Download(ctx, tag, assetName, dstDir)
	if err == nil {
		return path, nil
	}

	path, fbErr := c.fallback.Download(ctx, tag, assetName, dstDir)
	if fbErr == nil {
		return path, nil
	}

	return "", bothFailed(err, fbErr)
}

// Rungs reports the chain's sources in the order they are tried.
// It exists so a test can assert the PRODUCTION ladder's order —
// unauthenticated HTTP first, gh second — which is the property the
// public flip rests on: the first rung starting to succeed is what
// makes the gh dependency evaporate with no code change. Reversed,
// every other test still passes.
func (c *Chain) Rungs() []Source { return []Source{c.primary, c.fallback} }

// bothFailed joins the primary and fallback errors, one per line, via
// errors.Join so errors.Is still walks both wrapped chains (needed
// for ErrGHUnauthenticated to answer through the fallback's cause).
//
// Except when the primary never reached GitHub. gh cannot tell an
// outage from a bad credential — measured 2026-08-27, gh 2.95.0's
// `auth status` says "The token in keyring is invalid" with the
// network down — so offline, the gh rung classifies the outage as
// unauthenticated and the command exits 5 for a network failure
// (#399). The HTTP rung's transport error is the only reachability
// evidence the ladder has, and when it says unreachable the
// fallback's auth classification is dropped, keeping gh's message.
func bothFailed(primary, fallback error) error {
	var auth *ghAuthError
	if unreachable(primary) && errors.As(fallback, &auth) {
		fallback = auth.unclassified()
	}
	return errors.Join(
		fmt.Errorf("primary: %w", primary),
		fmt.Errorf("fallback: %w", fallback),
	)
}

// unreachable reports whether err is a transport-level failure — the
// request never got an HTTP status back. A 404 is a reached server
// saying no; a refused connection, a DNS failure or a dial timeout is
// not, and each arrives as a net.Error through *url.Error.
func unreachable(err error) bool {
	var ne net.Error
	return errors.As(err, &ne)
}
