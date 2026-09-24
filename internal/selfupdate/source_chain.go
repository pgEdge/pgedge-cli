package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// The deadlines every Source call runs under. The caller sets them,
// not a Source implementation, so both rungs share one budget.
const (
	// ListTimeout bounds a release-list fetch made by `self update`.
	ListTimeout = 30 * time.Second

	// DownloadTimeout bounds one asset download. Generous: it stops a
	// hang, not a slow link.
	DownloadTimeout = 10 * time.Minute

	// DoctorCheckTimeout bounds doctor's whole latest-version check,
	// both rungs together; doctor prints a row and moves on.
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
// Production wires HTTPSource as primary and GHSource as fallback:
// the repository is public, so the unauthenticated HTTP rung serves a
// user without gh, and gh is tried only when it fails.
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

// Rungs reports the chain's sources in the order they are tried, so a
// test can pin the production order: unauthenticated HTTP first, so a
// user without gh never needs it. Reversed, every other test still
// passes.
func (c *Chain) Rungs() []Source { return []Source{c.primary, c.fallback} }

// bothFailed joins both errors, one per line, with errors.Join, so
// errors.Is still reaches ErrGHUnauthenticated through the fallback.
//
// Except when the primary never reached GitHub. gh cannot tell an
// outage from a bad credential (measured 2026-08-27: gh 2.95.0's
// `auth status` says "The token in keyring is invalid" with the
// network down), so offline the command would exit 5 for a network
// failure. The HTTP rung's transport error is the ladder's only
// reachability evidence, so then the auth classification is dropped
// and gh's message kept.
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
