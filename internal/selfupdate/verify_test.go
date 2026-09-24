package selfupdate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

// writeArchive drops body at dir/name and returns its path plus the
// lowercase hex sha256 a conforming checksums.txt would carry.
func writeArchive(t *testing.T, dir, name, body string) (
	path, sha256hex string,
) {
	t.Helper()

	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	sum := sha256.Sum256([]byte(body))

	return path, hex.EncodeToString(sum[:])
}

func TestVerifyChecksumExactMatchPasses(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_darwin_arm64.tar.gz"
	path, want := writeArchive(t, dir, name, "archive bytes")

	checksums := want + "  " + name + "\n"

	if err := VerifyChecksum(path, name, []byte(checksums)); err != nil {
		t.Fatalf("VerifyChecksum: unexpected error: %v", err)
	}
}

func TestVerifyChecksumMismatchNamesBothHashes(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_linux_amd64.tar.gz"
	path, actual := writeArchive(t, dir, name, "archive bytes")

	stale := strings.Repeat("a", 64)
	checksums := stale + "  " + name + "\n"

	err := VerifyChecksum(path, name, []byte(checksums))
	if err == nil {
		t.Fatal("VerifyChecksum: expected error for a wrong hash")
	}

	if !strings.Contains(err.Error(), stale) {
		t.Errorf("error does not name the expected hash: %v", err)
	}

	if !strings.Contains(err.Error(), actual) {
		t.Errorf("error does not name the actual hash: %v", err)
	}
}

func TestVerifyChecksumMissingEntryFails(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_linux_arm64.tar.gz"
	path, sum := writeArchive(t, dir, name, "archive bytes")

	// The file lists a different platform's archive only.
	checksums := sum + "  pgedge_0.5.0_windows_amd64.zip\n"

	err := VerifyChecksum(path, name, []byte(checksums))
	if err == nil {
		t.Fatal("VerifyChecksum: expected error for a missing entry")
	}

	if !strings.Contains(err.Error(), name) {
		t.Errorf("error does not name the archive: %v", err)
	}
}

// The sidecar collision: checksums.txt lists both the archive and its
// <name>.sbom.json. A substring match would take whichever line came
// first; the exact-field match must take the archive's own line.
func TestVerifyChecksumIgnoresSBOMSidecarLine(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_darwin_arm64.tar.gz"
	path, want := writeArchive(t, dir, name, "archive bytes")

	// Sidecar line FIRST, so a substring scan matches it and fails.
	checksums := strings.Repeat("b", 64) + "  " + name + ".sbom.json\n" +
		want + "  " + name + "\n"

	if err := VerifyChecksum(path, name, []byte(checksums)); err != nil {
		t.Fatalf("VerifyChecksum: sidecar line shadowed the archive: %v", err)
	}
}

// The reverse of the sidecar case: asking for the sidecar itself must
// not be answered by the archive's line.
func TestVerifyChecksumSidecarResolvesToItsOwnLine(t *testing.T) {
	dir := t.TempDir()
	archive := "pgedge_0.5.0_darwin_arm64.tar.gz"
	sidecar := archive + ".sbom.json"
	path, want := writeArchive(t, dir, sidecar, "sbom bytes")

	checksums := strings.Repeat("c", 64) + "  " + archive + "\n" +
		want + "  " + sidecar + "\n"

	if err := VerifyChecksum(path, sidecar, []byte(checksums)); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

func TestVerifyChecksumUnreadableArchiveFails(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_linux_amd64.tar.gz"
	missing := filepath.Join(dir, name)

	checksums := strings.Repeat("d", 64) + "  " + name + "\n"

	if err := VerifyChecksum(missing, name, []byte(checksums)); err == nil {
		t.Fatal("VerifyChecksum: expected error for a missing archive")
	}
}

// Blank lines and a malformed one-field line must not derail the scan
// of a file that does carry the entry.
func TestVerifyChecksumToleratesBlankAndShortLines(t *testing.T) {
	dir := t.TempDir()
	name := "pgedge_0.5.0_linux_amd64.tar.gz"
	path, want := writeArchive(t, dir, name, "archive bytes")

	checksums := "\n   \ngarbage\n" + want + "  " + name + "\n"

	if err := VerifyChecksum(path, name, []byte(checksums)); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

// --- signature -----------------------------------------------------
//
// Every case below runs against an ephemeral CA (sigstore-go's
// VirtualSigstore): its own Fulcio root, its own Rekor key, and a leaf
// minted per call. Nothing here reaches public-good Sigstore — a real
// Fulcio certificate lives ten minutes, so a fixture captured from a
// release would start failing the same day it was written.

const (
	testIdentity = "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.5.0"
	testIssuer = "https://token.actions.githubusercontent.com"
)

// stubTlog stands in for the Rekor lookup, returning entries the test
// minted rather than any the public log holds.
type stubTlog struct {
	entries []*tlog.Entry
	err     error
}

func (s stubTlog) Entries(_, _ []byte) ([]*tlog.Entry, error) {
	return s.entries, s.err
}

// signed mints a leaf for identity/issuer, signs payload with it, and
// returns the three files a release ships: the base64-wrapped PEM
// certificate, the base64 signature, and the trust material naming the
// ephemeral CA plus the tlog entry recording that signature.
func signed(t *testing.T, identity, issuer string, payload []byte) (
	certB64, sigB64 []byte, trusted *TrustedMaterial) {
	t.Helper()

	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}

	certB64, sigB64, entries := signWith(t, vs, identity, issuer, payload)

	return certB64, sigB64, &TrustedMaterial{
		roots: vs,
		tlog:  stubTlog{entries: entries},
		// Opted out deliberately: the ephemeral CA embeds no SCT.
		// TestVerifySignatureRequiresSCTUnderProductionPosture leaves
		// this at its strict zero value to prove the opt-out is the
		// only thing standing between these tests and a real check.
		skipSCT: true,
	}
}

// signWith is signed's body for callers that need a second signature
// from the SAME certificate authority and transparency log.
func signWith(t *testing.T, vs *ca.VirtualSigstore,
	identity, issuer string, payload []byte) (
	certB64, sigB64 []byte, entries []*tlog.Entry) {
	t.Helper()

	entity, err := vs.Sign(identity, issuer, payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	vc, err := entity.VerificationContent()
	if err != nil {
		t.Fatalf("verification content: %v", err)
	}

	sc, err := entity.SignatureContent()
	if err != nil {
		t.Fatalf("signature content: %v", err)
	}

	entries, err = entity.TlogEntries()
	if err != nil {
		t.Fatalf("tlog entries: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: vc.Certificate().Raw,
	})

	certB64 = []byte(base64.StdEncoding.EncodeToString(certPEM))
	sigB64 = []byte(base64.StdEncoding.EncodeToString(sc.Signature()))

	return certB64, sigB64, entries
}

func TestVerifySignatureValidPasses(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, cert, trusted); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestVerifySignatureAcceptsBarePEM(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	bare, err := base64.StdEncoding.DecodeString(string(cert))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if err := VerifySignature(checksums, sig, bare, trusted); err != nil {
		t.Fatalf("VerifySignature on bare PEM: %v", err)
	}
}

func TestVerifySignatureTamperedChecksumsFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	tampered := []byte("cafef00d  pgedge_0.5.0_linux_amd64.tar.gz\n")

	if err := VerifySignature(tampered, sig, cert, trusted); err == nil {
		t.Fatal("VerifySignature accepted tampered checksums")
	}
}

func TestVerifySignatureWrongIdentityFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	evil := "https://github.com/attacker/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.5.0"
	cert, sig, trusted := signed(t, evil, testIssuer, checksums)

	err := VerifySignature(checksums, sig, cert, trusted)
	if err == nil {
		t.Fatal("VerifySignature accepted a foreign workflow identity")
	}

	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("error does not name the identity check: %v", err)
	}
}

// The release workflow only runs on tags today, so a signature from a
// run on any other ref can only mean the trigger was widened or the
// run was forged. The identity pins the ref to refs/tags/ so neither
// verifies.
func TestVerifySignatureBranchRunFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	branch := "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/release.yml@refs/heads/main"
	cert, sig, trusted := signed(t, branch, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, cert, trusted); err == nil {
		t.Fatal("VerifySignature accepted a signature from a branch run")
	}
}

// install.sh and the README's manual recipe must accept exactly the
// signatures VerifySignature accepts, so all three carry one regexp.
// Only REPO is substituted in the script; the two files are read
// from the repo root the way flag_claims_test reads the README.
func TestReleaseIdentityRegexpMatchesInstallRecipes(t *testing.T) {
	for _, tc := range []struct{ path, quoted string }{
		{"../../install.sh", `"` + strings.Replace(releaseIdentityRegexp,
			"pgEdge/pgedge-cli", "${REPO}", 1) + `"`},
		{"../../README.md", "'" + releaseIdentityRegexp + "'"},
	} {
		raw, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), tc.quoted) {
			t.Errorf("%s does not carry the identity regexp %s",
				tc.path, tc.quoted)
		}
		// One recipe per file: a second, laxer regexp beside the
		// right one would otherwise pass this test.
		if n := strings.Count(string(raw),
			"--certificate-identity-regexp"); n != 1 {
			t.Errorf("%s passes --certificate-identity-regexp %d times, "+
				"want 1", tc.path, n)
		}
	}
}

// The identity regexp is anchored, so a repository whose path merely
// CONTAINS ours must not satisfy it either.
func TestVerifySignaturePrefixImpostorFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	evil := "https://github.com/evil/pgEdge/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.5.0"
	cert, sig, trusted := signed(t, evil, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, cert, trusted); err == nil {
		t.Fatal("VerifySignature accepted a path-prefixed impostor")
	}
}

// A workflow file other than release.yml in our own repository is a
// different identity, and must not verify.
func TestVerifySignatureWrongWorkflowFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	other := "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/ci.yml@refs/heads/main"
	cert, sig, trusted := signed(t, other, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, cert, trusted); err == nil {
		t.Fatal("VerifySignature accepted a non-release workflow")
	}
}

func TestVerifySignatureWrongIssuerFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(
		t, testIdentity, "https://accounts.google.com", checksums)

	if err := VerifySignature(checksums, sig, cert, trusted); err == nil {
		t.Fatal("VerifySignature accepted a foreign OIDC issuer")
	}
}

func TestVerifySignatureNoTlogEntryFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)
	trusted.tlog = stubTlog{}

	err := VerifySignature(checksums, sig, cert, trusted)
	if err == nil {
		t.Fatal("VerifySignature accepted an unlogged signature")
	}

	if !strings.Contains(err.Error(), "transparency log") {
		t.Errorf("error does not name the transparency log: %v", err)
	}
}

func TestVerifySignatureTlogLookupErrorFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)
	trusted.tlog = stubTlog{err: errors.New("rekor unreachable")}

	err := VerifySignature(checksums, sig, cert, trusted)
	if err == nil {
		t.Fatal("VerifySignature ignored a failed transparency-log lookup")
	}

	if !strings.Contains(err.Error(), "rekor unreachable") {
		t.Errorf("error does not carry the lookup failure: %v", err)
	}
}

// Rekor indexes by artifact digest, so a lookup can return entries for
// signatures other than ours over the same bytes. The unrelated ones
// must be dropped: they are logged by the SAME Rekor, so the verifier
// reaches them and rejects the whole verification rather than skipping
// them. Verified by mutation: deleting the filter fails this test —
// though it fails on "duplicate tlog entries", not production's
// "signature does not match", because the virtual CA stamps log index
// 1000 on every entry it mints and exports no way to vary it.
func TestVerifySignatureIgnoresUnrelatedTlogEntries(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")

	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}

	// A stranger signs the identical bytes and logs it first.
	_, _, strangers := signWith(
		t, vs, "someone@example.com", testIssuer, checksums)
	cert, sig, ours := signWith(t, vs, testIdentity, testIssuer, checksums)

	trusted := &TrustedMaterial{
		roots:   vs,
		tlog:    stubTlog{entries: append(strangers, ours...)},
		skipSCT: true,
	}

	if err := VerifySignature(checksums, sig, cert, trusted); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

// ProductionTrustedMaterial requires an SCT; the ephemeral CA does not
// embed one, so this asserts the production posture is really enforced
// rather than configured and forgotten.
func TestVerifySignatureRequiresSCTUnderProductionPosture(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)
	trusted.skipSCT = false

	err := VerifySignature(checksums, sig, cert, trusted)
	if err == nil {
		t.Fatal("VerifySignature accepted a certificate with no SCT")
	}

	if !strings.Contains(err.Error(), "certificate timestamp") {
		t.Errorf("error does not name the SCT check: %v", err)
	}
}

func TestVerifySignatureMalformedCertificateFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	_, sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	if err := VerifySignature(
		checksums, sig, []byte("not a certificate"), trusted,
	); err == nil {
		t.Fatal("VerifySignature accepted a malformed certificate")
	}
}

func TestVerifySignatureMalformedSignatureFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, _, trusted := signed(t, testIdentity, testIssuer, checksums)

	if err := VerifySignature(
		checksums, []byte("!!not base64!!"), cert, trusted,
	); err == nil {
		t.Fatal("VerifySignature accepted a malformed signature")
	}
}

func TestVerifySignatureNilTrustedMaterialFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	cert, sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	cases := map[string]*TrustedMaterial{
		"nil":         nil,
		"no roots":    {tlog: trusted.tlog, skipSCT: true},
		"no log":      {roots: trusted.roots, skipSCT: true},
		"neither set": {},
	}

	for name, tm := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifySignature(checksums, sig, cert, tm); err == nil {
				t.Fatal("VerifySignature accepted empty trust material")
			}
		})
	}
}
