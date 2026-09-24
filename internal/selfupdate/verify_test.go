package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
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
// Most cases run against an ephemeral CA (sigstore-go's
// VirtualSigstore), because only a CA we hold can mint certificates for
// the identities these cases need. The sigprobe fixtures are the other
// half: a bundle cosign v3 really wrote in GitHub Actions, verified
// offline against the public-good roots captured beside it.

const (
	testIdentity = "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.5.0"
	testIssuer = "https://token.actions.githubusercontent.com"

	// sigprobeIdentity anchors the SAN of the certificate in the
	// sigprobe bundle, which was signed from a pull-request run.
	sigprobeIdentity = `^https://github\.com/pgEdge/pgedge-cli/` +
		`\.github/workflows/sigprobe\.yml@refs/pull/`
)

// signed returns a bundle over payload for identity/issuer, and trust
// material naming the ephemeral CA that issued it.
func signed(t *testing.T, identity, issuer string, payload []byte) (
	[]byte, *TrustedMaterial,
) {
	t.Helper()

	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}

	return signBundle(t, vs, identity, issuer, payload),
		&TrustedMaterial{
			roots: vs,
			// Opted out deliberately: the ephemeral CA embeds no SCT.
			// TestVerifySignatureRequiresSCTUnderProductionPosture leaves
			// this at its strict zero value to prove the opt-out is the
			// only thing standing between these tests and a real check.
			skipSCT: true,
		}
}

// sigprobe returns the real bundle, the checksums it signs, and the
// public-good trust material it verifies against, SCT check included.
func sigprobe(t *testing.T) (checksums, bundleJSON []byte, trusted *TrustedMaterial) {
	t.Helper()

	roots, err := root.NewTrustedRootFromJSON(fixture(t, "trusted_root.json"))
	if err != nil {
		t.Fatalf("trusted root: %v", err)
	}

	return fixture(t, "sigprobe-checksums.txt"),
		fixture(t, "sigprobe-checksums.txt.sigstore.json"),
		&TrustedMaterial{roots: roots}
}

func TestVerifySignatureValidPasses(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, trusted); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestVerifyBundleAcceptsARealCosignBundle(t *testing.T) {
	checksums, sig, trusted := sigprobe(t)

	if err := verifyBundle(checksums, sig, trusted, sigprobeIdentity); err != nil {
		t.Fatalf("verifyBundle: %v", err)
	}
}

// The same real bundle through the production entry point: it verifies
// cryptographically, so only the release identity can be refusing it.
func TestVerifySignatureRejectsARealBundleFromAnotherWorkflow(t *testing.T) {
	checksums, sig, trusted := sigprobe(t)

	err := VerifySignature(checksums, sig, trusted)
	if err == nil {
		t.Fatal("VerifySignature accepted a sigprobe.yml signature")
	}

	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("error does not name the identity check: %v", err)
	}
}

func TestVerifyBundleRejectsTamperedChecksumsUnderARealBundle(t *testing.T) {
	_, sig, trusted := sigprobe(t)
	tampered := []byte("cafef00d  pgedge_0.0.0_linux_amd64.tar.gz\n")

	if err := verifyBundle(tampered, sig, trusted, sigprobeIdentity); err == nil {
		t.Fatal("verifyBundle accepted tampered checksums")
	}
}

// The real bundle also carries a timestamp authority's timestamp, so
// with its log entries stripped the certificate is still dated and the
// only check left to fail is the transparency-log requirement.
func TestVerifyBundleRequiresATransparencyLogEntry(t *testing.T) {
	checksums, sig, trusted := sigprobe(t)

	var raw map[string]any
	if err := json.Unmarshal(sig, &raw); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}

	material := raw["verificationMaterial"].(map[string]any)
	delete(material, "tlogEntries")

	unlogged, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encode bundle: %v", err)
	}

	err = verifyBundle(checksums, unlogged, trusted, sigprobeIdentity)
	if err == nil {
		t.Fatal("verifyBundle accepted an unlogged signature")
	}

	if !strings.Contains(err.Error(), "transparency log") {
		t.Errorf("error does not name the transparency log: %v", err)
	}
}

func TestVerifySignatureTamperedChecksumsFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	sig, trusted := signed(t, testIdentity, testIssuer, checksums)

	tampered := []byte("cafef00d  pgedge_0.5.0_linux_amd64.tar.gz\n")

	if err := VerifySignature(tampered, sig, trusted); err == nil {
		t.Fatal("VerifySignature accepted tampered checksums")
	}
}

func TestVerifySignatureWrongIdentityFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	evil := "https://github.com/attacker/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.5.0"
	sig, trusted := signed(t, evil, testIssuer, checksums)

	err := VerifySignature(checksums, sig, trusted)
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
	sig, trusted := signed(t, branch, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, trusted); err == nil {
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
	sig, trusted := signed(t, evil, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, trusted); err == nil {
		t.Fatal("VerifySignature accepted a path-prefixed impostor")
	}
}

// A workflow file other than release.yml in our own repository is a
// different identity, and must not verify.
func TestVerifySignatureWrongWorkflowFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	other := "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/ci.yml@refs/heads/main"
	sig, trusted := signed(t, other, testIssuer, checksums)

	if err := VerifySignature(checksums, sig, trusted); err == nil {
		t.Fatal("VerifySignature accepted a non-release workflow")
	}
}

func TestVerifySignatureWrongIssuerFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	sig, trusted := signed(
		t, testIdentity, "https://accounts.google.com", checksums)

	if err := VerifySignature(checksums, sig, trusted); err == nil {
		t.Fatal("VerifySignature accepted a foreign OIDC issuer")
	}
}

// ProductionTrustedMaterial requires an SCT; the ephemeral CA does not
// embed one, so this asserts the production posture is really enforced
// rather than configured and forgotten.
func TestVerifySignatureRequiresSCTUnderProductionPosture(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	sig, trusted := signed(t, testIdentity, testIssuer, checksums)
	trusted.skipSCT = false

	err := VerifySignature(checksums, sig, trusted)
	if err == nil {
		t.Fatal("VerifySignature accepted a certificate with no SCT")
	}

	if !strings.Contains(err.Error(), "certificate timestamp") {
		t.Errorf("error does not name the SCT check: %v", err)
	}
}

func TestVerifySignatureMalformedBundleFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	_, trusted := signed(t, testIdentity, testIssuer, checksums)

	for name, raw := range map[string]string{
		"not json":      "not a bundle",
		"no media type": `{"verificationMaterial": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := VerifySignature(checksums, []byte(raw), trusted)
			if err == nil {
				t.Fatal("VerifySignature accepted a malformed bundle")
			}

			if !strings.Contains(err.Error(), "parse signature bundle") {
				t.Errorf("error does not name the bundle parse: %v", err)
			}
		})
	}
}

func TestVerifySignatureNilTrustedMaterialFails(t *testing.T) {
	checksums := []byte("deadbeef  pgedge_0.5.0_linux_amd64.tar.gz\n")
	sig, _ := signed(t, testIdentity, testIssuer, checksums)

	for name, tm := range map[string]*TrustedMaterial{
		"nil":      nil,
		"no roots": {skipSCT: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifySignature(checksums, sig, tm); err == nil {
				t.Fatal("VerifySignature accepted empty trust material")
			}
		})
	}
}
