package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// VerifyChecksum guarantees the bytes on disk at archivePath are the
// bytes checksums.txt recorded for archiveName — so an archive swapped
// in transit, or a truncated download, cannot reach the extractor.
// It is the second half of the trust chain: VerifySignature proves
// checksums.txt is ours, this proves the archive matches it.
//
// The match is on the FILENAME FIELD, never a substring: checksums.txt
// lists each archive twice over, once as `<name>` and once as
// `<name>.sbom.json`, and a substring scan takes whichever came first.
func VerifyChecksum(archivePath, archiveName string, checksums []byte) error {
	want, ok := checksumFor(checksums, archiveName)
	if !ok {
		return fmt.Errorf(
			"no checksum entry for %s in checksums.txt", archiveName)
	}

	got, err := sha256File(archivePath)
	if err != nil {
		return err
	}

	if !strings.EqualFold(got, want) {
		return fmt.Errorf(
			"checksum mismatch for %s: expected %s, got %s",
			archiveName, want, got)
	}

	return nil
}

// checksumFor returns the hash whose line's second field equals name
// exactly — `awk '$2 == name { print $1 }'` semantics.
func checksumFor(checksums []byte, name string) (string, bool) {
	for line := range strings.Lines(string(checksums)) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return fields[0], true
		}
	}

	return "", false
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the caller's own download path.
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read archive: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// The install.sh recipe, character for character: `self update` must
// accept exactly the signatures the install script accepts, so any
// drift between the two is a supply-chain difference, not a detail.
const (
	releaseIdentityRegexp = `^https://github\.com/pgEdge/pgedge-cli/` +
		`\.github/workflows/release\.yml@refs/tags/`
	releaseOIDCIssuer = "https://token.actions.githubusercontent.com"
)

// VerifySignature proves checksums.txt came from our release workflow,
// which is what makes the checksums worth comparing against.
// bundleJSON is the release's `checksums.txt.sigstore.json`, the
// Sigstore bundle cosign's --bundle writes: certificate, signature,
// log entry and timestamps in one file, so nothing is fetched here.
//
// Failure is final: there is no checksum-only fallback, because a
// `self update` running unattended has no operator to read a warning.
func VerifySignature(
	checksums, bundleJSON []byte, trusted *TrustedMaterial,
) error {
	return verifyBundle(checksums, bundleJSON, trusted, releaseIdentityRegexp)
}

// verifyBundle is VerifySignature with the identity as a parameter, so
// a bundle captured from a workflow other than release.yml can prove
// the parse and the cryptography against the real public-good roots.
func verifyBundle(
	checksums, bundleJSON []byte, trusted *TrustedMaterial,
	identityRegexp string,
) error {
	if trusted == nil || trusted.roots == nil {
		return errors.New("no sigstore trust material")
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("parse signature bundle: %w", err)
	}

	options := []verify.VerifierOption{
		// Requires the certificate to be dated by a log's signed entry
		// timestamp or a timestamp authority: a Fulcio certificate lives
		// ten minutes, so without one every release would look expired
		// the day after it shipped.
		verify.WithObserverTimestamps(1),
		// Requires the signature to be publicly recorded, so a private
		// signature from a stolen identity cannot pass unnoticed.
		verify.WithTransparencyLog(1),
	}

	if !trusted.skipSCT {
		// Requires the certificate itself to have been published to a
		// certificate-transparency log, so a Fulcio issuing off the
		// record cannot mint one for our identity unseen.
		options = append(options,
			verify.WithSignedCertificateTimestamps(ctLogThreshold))
	}

	verifier, err := verify.NewVerifier(trusted.roots, options...)
	if err != nil {
		return fmt.Errorf("configure sigstore verifier: %w", err)
	}

	// Ties the certificate to OUR release workflow: the SAN must match
	// the anchored identity regexp AND the OIDC issuer must be GitHub
	// Actions, so a valid Fulcio certificate for any other repository,
	// workflow or identity provider is rejected.
	identity, err := verify.NewShortCertificateIdentity(
		releaseOIDCIssuer, "", "", identityRegexp)
	if err != nil {
		return fmt.Errorf("configure certificate identity: %w", err)
	}

	// Binds the signature to these exact checksums.txt bytes.
	_, err = verifier.Verify(&b, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(checksums)),
		verify.WithCertificateIdentity(identity),
	))
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}
