package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/tlog"
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
// which is what makes the checksums worth comparing against. sig and
// certPEM are the release's `checksums.txt.sig` and `checksums.txt.pem`
// as cosign wrote them (base64 over the DER signature and over the PEM
// certificate respectively).
//
// Failure is final: there is no checksum-only fallback, because a
// `self update` running unattended has no operator to read a warning.
func VerifySignature(
	checksums, sig, certPEM []byte, trusted *TrustedMaterial,
) error {
	if trusted == nil || trusted.roots == nil || trusted.tlog == nil {
		return errors.New("no sigstore trust material")
	}

	cert, err := parseCertificate(certPEM)
	if err != nil {
		return err
	}

	sigDER, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(string(sig)))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	digest := sha256.Sum256(checksums)

	// tlogFinder.Entries already names the transparency log in its own
	// error (a Rekor outage, a malformed index, a query failure), so
	// this returns it unwrapped rather than doubling that prefix.
	found, err := trusted.tlog.Entries(digest[:], sigDER)
	if err != nil {
		return err
	}

	// Rekor indexes by artifact digest, so the answer may hold entries
	// for other people's signatures over the same bytes. Keeping one
	// would fail the whole verification, so keep only ours. The finder
	// filters too; tlogFinder is an interface, and this is the side of
	// it that must not depend on an implementation being careful.
	entries := make([]*tlog.Entry, 0, len(found))

	for _, entry := range found {
		if bytes.Equal(entry.Signature(), sigDER) {
			entries = append(entries, entry)
		}
	}

	options := []verify.VerifierOption{
		// Requires the certificate to be dated by a log's signed entry
		// timestamp: a Fulcio certificate lives ten minutes, so without
		// one every release would look expired the day after it shipped.
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
		releaseOIDCIssuer, "", "", releaseIdentityRegexp)
	if err != nil {
		return fmt.Errorf("configure certificate identity: %w", err)
	}

	entity := &blobEntity{
		cert:    cert,
		entries: entries,
		signature: bundle.NewMessageSignature(
			digest[:], "SHA2_256", sigDER),
	}

	// Binds the signature to these exact checksums.txt bytes.
	_, err = verifier.Verify(entity, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(checksums)),
		verify.WithCertificateIdentity(identity),
	))
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// parseCertificate reads the release's `.pem` asset. cosign writes it
// base64-encoded over the PEM, so accept both that and a bare PEM.
func parseCertificate(raw []byte) (*x509.Certificate, error) {
	trimmed := bytes.TrimSpace(raw)

	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN")) {
		decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
		if err != nil {
			return nil, fmt.Errorf("decode certificate: %w", err)
		}

		trimmed = decoded
	}

	block, _ := pem.Decode(trimmed)
	if block == nil {
		return nil, errors.New("certificate is not PEM-encoded")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	return cert, nil
}

// blobEntity presents a detached cosign signature to sigstore-go's
// verifier. A release ships the certificate, the signature and (via
// Rekor) the log entry as three separate things rather than as one
// Sigstore bundle, so the SignedEntity is assembled here instead of
// being unmarshalled.
type blobEntity struct {
	cert      *x509.Certificate
	signature *bundle.MessageSignature
	entries   []*tlog.Entry
}

func (e *blobEntity) VerificationContent() (verify.VerificationContent, error) {
	return bundle.NewCertificate(e.cert), nil
}

func (e *blobEntity) SignatureContent() (verify.SignatureContent, error) {
	return e.signature, nil
}

func (e *blobEntity) TlogEntries() ([]*tlog.Entry, error) {
	return e.entries, nil
}

// No RFC3161 timestamp: cosign sign-blob does not request one, and the
// log's integrated time is the observer timestamp instead.
func (e *blobEntity) Timestamps() ([][]byte, error) {
	return nil, nil
}

func (e *blobEntity) HasInclusionPromise() bool {
	for _, entry := range e.entries {
		if entry.HasInclusionPromise() {
			return true
		}
	}

	return false
}

func (e *blobEntity) HasInclusionProof() bool {
	for _, entry := range e.entries {
		if entry.HasInclusionProof() {
			return true
		}
	}

	return false
}

// Version reports the bundle version whose semantics this entity
// follows. v0.3 is accurate for our shape: one certificate, a message
// signature, and a log entry carrying a promise and/or a proof.
//
// The trap is that nothing on this path validates the string. The
// v1.1.4 verifier reads it in exactly one place — whether to add a
// compatibility verifier for ECDSA P-384/P-521 keys signed with
// SHA-256 — so declaring the unreleased v0.4 would silently drop that
// leniency rather than buy strictness, while claiming a format we do
// not use. The leniency is inert for the P-256 keys Fulcio issues us
// anyway.
func (e *blobEntity) Version() (string, error) {
	return "v0.3", nil
}
