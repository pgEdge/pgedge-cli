package selfupdate

import (
	"fmt"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

// ctLogThreshold requires one verified SignedCertificate Timestamp,
// matching `cosign verify-blob`'s default.
const ctLogThreshold = 1

// TrustedMaterial is the Sigstore roots a release's certificate must
// chain to, and whose logs and timestamp authorities must have dated
// and recorded its signature.
type TrustedMaterial struct {
	roots root.TrustedMaterial

	// skipSCT opts OUT of certificate-transparency verification, and
	// exists only for tests: the ephemeral CA they sign with embeds no
	// SCT. Strict is the ZERO VALUE, so a TrustedMaterial built
	// anywhere in this package verifies SCTs unless someone wrote the
	// opt-out down.
	skipSCT bool
}

// SigstoreCacheDir is where the refreshed TUF trust root is cached.
// sigstore-go's default, ~/.sigstore/root, is outside ~/.pgedge/cli,
// the only directory this CLI owns.
func SigstoreCacheDir() (string, error) {
	dir, err := config.DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sigstore"), nil
}

// sigstoreTUFOptions is sigstore-go's public-good TUF configuration
// with only the cache location changed.
func sigstoreTUFOptions() (*tuf.Options, error) {
	dir, err := SigstoreCacheDir()
	if err != nil {
		return nil, err
	}
	return tuf.DefaultOptions().WithCachePath(dir), nil
}

// fetchTrustedRoot is root.FetchTrustedRootWithOptions, indirected so
// a test can prove the options reach sigstore-go: a call site using
// root.FetchTrustedRoot() would cache under ~/.sigstore again with
// every path-helper test still passing.
var fetchTrustedRoot = root.FetchTrustedRootWithOptions

// ProductionTrustedMaterial loads the public-good Sigstore trust root:
// sigstore-go's embedded TUF root, refreshed from the CDN and cached
// under SigstoreCacheDir.
func ProductionTrustedMaterial() (*TrustedMaterial, error) {
	opts, err := sigstoreTUFOptions()
	if err != nil {
		return nil, fmt.Errorf("load sigstore trust root: %w", err)
	}

	roots, err := fetchTrustedRoot(opts)
	if err != nil {
		return nil, fmt.Errorf("load sigstore trust root: %w", err)
	}

	return &TrustedMaterial{roots: roots}, nil
}

// NewTestTrustedMaterial builds a TrustedMaterial from a caller-minted
// Sigstore root (e.g. sigstore-go's ca.NewVirtualSigstore), for tests
// outside this package that drive VerifySignature end to end without
// dialing public Sigstore. SCT verification is skipped: an ephemeral
// CA embeds no SCT.
func NewTestTrustedMaterial(roots root.TrustedMaterial) *TrustedMaterial {
	return &TrustedMaterial{roots: roots, skipSCT: true}
}
