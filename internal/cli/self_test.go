package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/selfupdate"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"gopkg.in/yaml.v3"
)

// --- fixture Source --------------------------------------------------

// fixtureSource is a hermetic selfupdate.Source: it serves releases
// and assets scripted in advance rather than dialing anything.
type fixtureSource struct {
	releases      []selfupdate.Release
	releasesErr   error
	assets        map[string][]byte
	downloadErr   map[string]error
	brokenPaths   map[string]bool
	downloadCalls []string
	releasesCalls int
}

func (s *fixtureSource) Releases(_ context.Context) ([]selfupdate.Release, error) {
	s.releasesCalls++
	return s.releases, s.releasesErr
}

func (s *fixtureSource) Download(
	_ context.Context, _, assetName, dstDir string,
) (string, error) {
	s.downloadCalls = append(s.downloadCalls, assetName)
	if err, ok := s.downloadErr[assetName]; ok {
		return "", err
	}
	if s.brokenPaths[assetName] {
		// Reports success without writing anything — a Source that
		// lies about where it put the file, driving downloadBytes'
		// own os.ReadFile failure.
		return filepath.Join(dstDir, assetName+".never-written"), nil
	}
	body, ok := s.assets[assetName]
	if !ok {
		return "", fmt.Errorf("fixtureSource: no asset %q scripted", assetName)
	}
	path := filepath.Join(dstDir, assetName)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// --- signed checksums fixture -------------------------------------------

const (
	testIdentity = "https://github.com/pgEdge/pgedge-cli/" +
		".github/workflows/release.yml@refs/tags/v0.6.0"
	testIssuer = "https://token.actions.githubusercontent.com"
)

// signChecksums returns the release's signing asset, a Sigstore bundle
// over checksums from an ephemeral CA, plus the hermetic trust material
// that verifies it. selfupdate.NewTestTrustedMaterial is the only door
// a command-level test has to TrustedMaterial's unexported fields.
func signChecksums(t *testing.T, checksums []byte) (
	sigBundle []byte, trusted *selfupdate.TrustedMaterial,
) {
	t.Helper()

	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}

	return signBundle(t, vs, testIdentity, testIssuer, checksums),
		selfupdate.NewTestTrustedMaterial(vs)
}

// writeArchive builds a tar.gz containing one regular file named
// "pgedge" (or "pgedge.exe" under a windows build) holding body, and
// returns the archive bytes plus the ARCHIVE's own lowercase-hex
// sha256 — checksums.txt records a hash of the .tar.gz file itself,
// not of the binary inside it.
func writeArchive(t *testing.T, body []byte) (archive []byte, sha256hex string) {
	t.Helper()

	binaryName := "pgedge"
	if runtime.GOOS == "windows" {
		binaryName = "pgedge.exe"
	}

	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive temp file: %v", err)
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: binaryName, Typeflag: tar.TypeReg,
		Mode: 0o755, Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}

	archive, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back archive: %v", err)
	}
	sum := sha256.Sum256(archive)
	return archive, hex.EncodeToString(sum[:])
}

// buildFixture assembles a self-consistent, signed release fixture:
// the release list, a fixtureSource serving all four assets under
// archiveBody's own checksum, and the hermetic trust material that
// verifies its signature. archiveOverride, when non-nil, replaces the
// bytes the source actually serves for the archive asset — used to
// simulate a checksum mismatch without touching the signature.
func buildFixture(t *testing.T, tag string, archiveOverride []byte) (
	src *fixtureSource, trusted *selfupdate.TrustedMaterial, assetName string,
) {
	t.Helper()

	assetName, err := releaseAssetName(tag)
	if err != nil {
		t.Fatalf("releaseAssetName(%q): %v", tag, err)
	}

	body := []byte("new-binary-bytes-for-" + tag)
	archive, sum := writeArchive(t, body)

	checksums := []byte(sum + "  " + assetName + "\n")
	sigBundle, trusted := signChecksums(t, checksums)

	served := archive
	if archiveOverride != nil {
		served = archiveOverride
	}

	src = &fixtureSource{
		releases: []selfupdate.Release{{TagName: tag}},
		assets: map[string][]byte{
			assetName:                     served,
			"checksums.txt":               checksums,
			"checksums.txt.sigstore.json": sigBundle,
		},
	}
	return src, trusted, assetName
}

// --- stub Runner: re-execs this test binary as TestSelfHelperProcess,
// following os/exec's own test pattern (see stdlib os/exec/exec_test.go
// and internal/selfupdate/source_test.go's identical stubRunner). No
// test in this file ever runs a real pgedge binary.

// probeRunner returns a Runner that, regardless of the target path it
// is asked to exec, prints stdoutJSON and exits 0 — standing in for
// the newly-swapped binary's `version -o json` probe.
func probeRunner(t *testing.T, stdoutJSON string) selfupdate.Runner {
	t.Helper()
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestSelfHelperOK$", "--", stdoutJSON)
		cmd.Env = append(os.Environ(), "PGEDGE_SELF_TEST_HELPER_OK=1")
		return cmd
	}
}

// failingProbeRunner returns a Runner whose exec always fails, for
// the probe-failure branch.
func failingProbeRunner(t *testing.T) selfupdate.Runner {
	t.Helper()
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(
			ctx, os.Args[0], "-test.run=^TestSelfHelperFail$")
		cmd.Env = append(os.Environ(), "PGEDGE_SELF_TEST_HELPER_FAIL=1")
		return cmd
	}
}

// TestSelfHelperOK is not a real test: it is the subprocess
// probeRunner re-execs in place of the swapped binary's `version -o
// json`. Guarded by PGEDGE_SELF_TEST_HELPER_OK so a normal `go test`
// run treats it as a no-op.
func TestSelfHelperOK(t *testing.T) {
	if os.Getenv("PGEDGE_SELF_TEST_HELPER_OK") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "probeRunner: missing stdout payload")
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, args[0])
	os.Exit(0)
}

// TestSelfHelperFail is failingProbeRunner's subprocess: it always
// exits nonzero, standing in for a probe that cannot run the new
// binary at all. Guarded by PGEDGE_SELF_TEST_HELPER_FAIL so a normal
// `go test` run treats it as a no-op.
func TestSelfHelperFail(t *testing.T) {
	if os.Getenv("PGEDGE_SELF_TEST_HELPER_FAIL") != "1" {
		return
	}
	os.Exit(1)
}

// --- shared test fixtures -------------------------------------------

func testModules() []module.ModuleInfo {
	return []module.ModuleInfo{
		{Name: "starfleet", Version: "1.0.0"},
		{Name: "controlplane", Version: "2.0.0"},
	}
}

// writeTargetFile creates a placeholder "installed" binary a test can
// assert is untouched or swapped.
func writeTargetFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	return path
}

// harmlessResolve returns a Resolve stub naming a path that never
// gets read or written in the tests that use it — they all fail
// before ever reaching Swap. It exists so every test drives
// runSelfUpdate's now-unconditional, up-front ResolveTarget call
// against a stub rather than the REAL selfupdate.ResolveTarget,
// which would resolve wherever `go test` happens to have built this
// package's test binary and apply the real refusal checks against
// it — not hermetic, and liable to refuse for real on a checkout
// that builds test binaries inside the working tree.
func harmlessResolve(t *testing.T) func() (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pgedge")
	return func() (string, error) { return path, nil }
}

// mustNotBeCalledSource is a selfupdate.Source that fails the test
// the moment either method is invoked. It pins the property this
// fix round exists for: when ResolveTarget refuses, runSelfUpdate
// must return before ever touching the network, so a refusing
// target costs zero release fetch and zero download.
type mustNotBeCalledSource struct{ t *testing.T }

func (s *mustNotBeCalledSource) Releases(_ context.Context) ([]selfupdate.Release, error) {
	s.t.Helper()
	s.t.Fatal("Source.Releases was called after ResolveTarget refused")
	return nil, nil
}

func (s *mustNotBeCalledSource) Download(
	_ context.Context, _, _, _ string,
) (string, error) {
	s.t.Helper()
	s.t.Fatal("Source.Download was called after ResolveTarget refused")
	return "", nil
}

// --- tests -----------------------------------------------------------

func TestSelfUpdateCheckReportsAvailableWithoutDownloading(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)

	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	if len(src.downloadCalls) != 0 {
		t.Errorf("--check downloaded assets: %v", src.downloadCalls)
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.UpdateAvailable == nil || !*report.UpdateAvailable {
		t.Errorf("report.UpdateAvailable = %v, want true", report.UpdateAvailable)
	}
	if report.New != "0.6.0" {
		t.Errorf("report.New = %q, want 0.6.0", report.New)
	}
}

func TestSelfUpdateAlreadyCurrent(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	Version = "v0.6.0"
	t.Cleanup(func() { Version = "dev" })

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	if len(src.downloadCalls) != 0 {
		t.Errorf("already-current downloaded assets: %v", src.downloadCalls)
	}
	if !strings.Contains(stderr.String(), "already up to date") {
		t.Errorf("stderr missing already-up-to-date sentence:\n%s", stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.Previous != report.New {
		t.Errorf("Previous (%q) != New (%q)", report.Previous, report.New)
	}
}

func TestSelfUpdateGHUnauthenticatedExitsFive(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src := &fixtureSource{
		releasesErr: fmt.Errorf("fetch: %w", selfupdate.ErrGHUnauthenticated),
	}
	deps := &SelfDeps{Source: src, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, selfupdate.ErrGHUnauthenticated) {
		t.Errorf("errors.Is(err, ErrGHUnauthenticated) = false: %v", err)
	}
	if code := ExitCode(err); code != 5 {
		t.Errorf("ExitCode = %d, want 5", code)
	}
}

func TestSelfUpdateVerificationFailureLeavesTargetUntouched(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	// checksums.txt is signed over the ORIGINAL bytes, so the
	// signature still verifies; the source then serves DIFFERENT
	// bytes for the archive, so the checksum comparison is what
	// fails.
	src, trusted, _ := buildFixture(t, "v0.6.0", []byte("tampered bytes"))

	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a verification error")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(got) != "original-binary" {
		t.Errorf("target was modified: %q", got)
	}
}

// TestSelfUpdateRefusalExitsOneVerbatim pins the UPDATE path:
// ResolveTarget runs before any network call, so a refusal costs
// nothing. The Source here fails the test outright if either method
// is ever invoked — there is no fixture to build, which is itself the
// point: a refusing run never gets far enough to need one. (--check
// is the one path that reports through a refusal; see
// TestSelfUpdateCheckReportsOnARefusedInstall.)
func TestSelfUpdateRefusalExitsOneVerbatim(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"

	refusal := &selfupdate.RefusalError{
		Msg: "/opt/pgedge/pgedge was installed via Homebrew; run " +
			"`brew upgrade pgedge` instead of `pgedge self update`",
	}
	deps := &SelfDeps{
		Source:  &mustNotBeCalledSource{t: t},
		Resolve: func() (string, error) { return "", refusal },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a refusal error")
	}
	if err.Error() != refusal.Msg {
		t.Errorf("message = %q, want verbatim %q", err.Error(), refusal.Msg)
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

// TestSelfUpdateCheckReportsOnARefusedInstall pins the split between
// the two paths a refusal reaches. The UPDATE path refuses before any
// network call (TestSelfUpdateRefusalExitsOneVerbatim). --check
// swaps nothing, so it answers the question it was asked — exit 0,
// the report on stdout — and carries the refusal as a stderr note.
// Downloading is still off the table: the report needs the release
// list and nothing else.
func TestSelfUpdateCheckReportsOnARefusedInstall(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)

	refusal := &selfupdate.RefusalError{
		Msg: "/opt/pgedge/pgedge was installed via Homebrew; run " +
			"`brew upgrade pgedge` instead of `pgedge self update`",
	}
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return "", refusal },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--check on a refused install returned %v\nstderr: %s",
			err, stderr.String())
	}

	if len(src.downloadCalls) != 0 {
		t.Errorf("--check downloaded assets: %v", src.downloadCalls)
	}
	if !strings.Contains(stderr.String(), refusal.Msg) {
		t.Errorf("stderr does not carry the refusal note:\n%s", stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.UpdateAvailable == nil || !*report.UpdateAvailable {
		t.Errorf("report.UpdateAvailable = %v, want true", report.UpdateAvailable)
	}
	if report.New != "0.6.0" {
		t.Errorf("report.New = %q, want 0.6.0", report.New)
	}
}

// TestSelfUpdateCheckOnACurrentBinarySaysFalse pins the output
// contract's "update_available": true|false. Omitting the field left
// a parser unable to tell "no update" from "this run never asked".
func TestSelfUpdateCheckOnACurrentBinarySaysFalse(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	Version = "v0.6.0"
	t.Cleanup(func() { Version = "dev" })

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	if !strings.Contains(out.String(), "update_available") {
		t.Errorf("json report omits update_available:\n%s", out.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.UpdateAvailable == nil {
		t.Fatal("report.UpdateAvailable is absent, want an explicit false")
	}
	if *report.UpdateAvailable {
		t.Error("report.UpdateAvailable = true on an already-current binary")
	}
}

// TestSelfUpdateCheckOnACurrentBinarySaysFalseInYAML proves the yaml
// renderer carries the false too — a pointer field with omitempty
// drops a nil, and a nil is what the omission used to be.
func TestSelfUpdateCheckOnACurrentBinarySaysFalseInYAML(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "yaml")
	rt.Profile = "default"
	Version = "v0.6.0"
	t.Cleanup(func() { Version = "dev" })

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	if !strings.Contains(out.String(), "update_available: false") {
		t.Errorf("yaml report missing `update_available: false`:\n%s", out.String())
	}
}

func TestSelfUpdateHappyPathTextTable(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "text")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")

	probeJSON := `{"version":"v0.6.0","commit":"c","build_date":"d",` +
		`"modules":[{"name":"starfleet","version":"9.9.9"}]}`
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  probeRunner(t, probeJSON),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-binary-bytes-for-v0.6.0" {
		t.Errorf("target bytes = %q, want the extracted binary's bytes", got)
	}

	text := out.String()
	for _, want := range []string{
		"COMPONENT", "PREVIOUS", "NEW",
		"pgedge", "dev", "v0.6.0",
		"starfleet", "1.0.0", "9.9.9",
		"controlplane", "2.0.0", "unknown",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stdout missing %q:\n%s", want, text)
		}
	}

	if strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("--force printed the confirmation prompt:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "completion") {
		t.Errorf("no completion is installed, yet stderr mentions one:\n%s", stderr.String())
	}
}

func TestSelfUpdateHappyPathJSON(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")

	probeJSON := `{"version":"v0.6.0","commit":"c","build_date":"d",` +
		`"modules":[{"name":"starfleet","version":"9.9.9"}]}`
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  probeRunner(t, probeJSON),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("stdout did not parse as JSON: %v\n%s", err, out.String())
	}
	if report.New != "v0.6.0" {
		t.Errorf("report.New = %q, want v0.6.0", report.New)
	}

	// Progress went to stderr only: stdout is exactly the JSON report.
	if strings.Contains(out.String(), "downloading") ||
		strings.Contains(out.String(), "verifying") {
		t.Errorf("stdout carries progress text:\n%s", out.String())
	}
	if !strings.Contains(stderr.String(), "downloading") {
		t.Errorf("stderr missing progress text:\n%s", stderr.String())
	}
}

func TestSelfUpdateProbeFailureReportsUnknown(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")

	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  failingProbeRunner(t),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.New != "0.6.0" {
		t.Errorf("report.New = %q, want the release tag without its v, 0.6.0", report.New)
	}
	for _, m := range report.Modules {
		if m.New != "unknown" {
			t.Errorf("module %s New = %q, want unknown", m.Name, m.New)
		}
	}
	if !strings.Contains(stderr.String(), "could not verify") {
		t.Errorf("stderr missing probe-failure sentence:\n%s", stderr.String())
	}
}

// --- releaseAssetName --------------------------------------------------

func TestArchiveExtension(t *testing.T) {
	cases := map[string]string{
		"windows": "zip",
		"linux":   "tar.gz",
		"darwin":  "tar.gz",
	}
	for goos, want := range cases {
		if got := archiveExtension(goos); got != want {
			t.Errorf("archiveExtension(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestReleaseAssetNameMatchesInstallSh(t *testing.T) {
	name, err := releaseAssetName("v0.6.0")
	if err != nil {
		t.Fatalf("releaseAssetName: %v", err)
	}
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	want := fmt.Sprintf("pgedge_0.6.0_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
	if name != want {
		t.Errorf("releaseAssetName = %q, want %q", name, want)
	}
}

// TestReleaseAssetNameRejectsPathTraversal drives the deferred T2
// finding assigned to this task: Download never guarded the asset
// name against path traversal, harmless while names came only from
// our own derivation. Here the name derives from a hostile tag (the
// --version flag's ultimate source), so the guard has to live in the
// derivation itself.
func TestReleaseAssetNameRejectsPathTraversal(t *testing.T) {
	hostileTags := []string{
		"v../../../etc/cron.d/evil",
		"v0.6.0/../../etc/passwd",
		"v/..",
	}
	for _, tag := range hostileTags {
		if name, err := releaseAssetName(tag); err == nil {
			t.Errorf("releaseAssetName(%q) = %q, nil; want an error", tag, name)
		}
	}
}

// --- production wiring, resolve/confirm/asset-name/download/verify/
//     extract/swap/probe error branches -------------------------------

// TestProductionSourceBuildsChain exercises the nil-Source default's
// constructor directly. It stops short of calling Releases/Download on
// the result, which would dial the real GitHub API — this only proves
// the ladder is assembled.
func TestProductionSourceBuildsChain(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	src := productionSource(rt)
	if src == nil {
		t.Fatal("productionSource returned nil")
	}
	if _, ok := src.(*selfupdate.Chain); !ok {
		t.Errorf("productionSource returned %T, want *selfupdate.Chain", src)
	}
}

// TestProductionSourceTriesHTTPBeforeGH pins the ladder's ORDER, not
// just its shape. The public flip rests on it: the unauthenticated
// HTTP rung starting to succeed is what makes the gh dependency
// evaporate with no code change, and a chain built the other way
// round would keep execing gh forever while passing every other test
// in this file.
func TestProductionSourceTriesHTTPBeforeGH(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	src := productionSource(rt)
	chain, ok := src.(*selfupdate.Chain)
	if !ok {
		t.Fatalf("productionSource returned %T, want *selfupdate.Chain", src)
	}

	rungs := chain.Rungs()
	if len(rungs) != 2 {
		t.Fatalf("chain has %d rungs, want 2", len(rungs))
	}
	if _, ok := rungs[0].(*selfupdate.HTTPSource); !ok {
		t.Errorf("first rung is %T, want *selfupdate.HTTPSource", rungs[0])
	}
	if _, ok := rungs[1].(*selfupdate.GHSource); !ok {
		t.Errorf("second rung is %T, want *selfupdate.GHSource", rungs[1])
	}
}

func TestSelfUpdateResolveErrorUnknownVersion(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src := &fixtureSource{
		releases: []selfupdate.Release{{TagName: "v0.6.0"}},
	}
	deps := &SelfDeps{Source: src, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--version", "v9.9.9", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an unknown-tag error")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

// TestSelfUpdateConfirmRejectionWithoutForce drives the Confirm error
// branch: go test's own process has no TTY on stdin, so omitting
// --force hits confirmWith's non-interactive UsageError rather than a
// hung prompt.
func TestSelfUpdateConfirmRejectionWithoutForce(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a confirmation error")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("ExitCode = %d, want 2 (usage)", code)
	}
	if len(src.downloadCalls) != 0 {
		t.Errorf("rejected confirmation still downloaded: %v", src.downloadCalls)
	}
}

// TestSelfUpdateHostileReleaseTagRejected drives releaseAssetName's
// guard from inside the real command flow: the release feed itself
// names a tag containing a path separator, Resolve accepts it (an
// exact match against --version), and the asset-name derivation must
// still refuse it before any Download call.
func TestSelfUpdateHostileReleaseTagRejected(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	hostileTag := "v0.6.0/../../etc/passwd"
	src := &fixtureSource{
		releases: []selfupdate.Release{{TagName: hostileTag}},
	}
	deps := &SelfDeps{Source: src, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--version", hostileTag, "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a path-traversal refusal")
	}
	if len(src.downloadCalls) != 0 {
		t.Errorf("hostile tag still reached Download: %v", src.downloadCalls)
	}
}

// TestSelfUpdateDownloadFailureIsPlainError covers every asset's
// generic (non-gh) Download failure, which classifySelfUpdateError
// must pass through unchanged rather than promoting to exit 5.
func TestSelfUpdateDownloadFailureIsPlainError(t *testing.T) {
	assetName, err := releaseAssetName("v0.6.0")
	if err != nil {
		t.Fatalf("releaseAssetName: %v", err)
	}
	for _, failing := range []string{
		assetName, "checksums.txt", "checksums.txt.sigstore.json",
	} {
		t.Run(failing, func(t *testing.T) {
			rt, out, stderr := testsupport.NewRuntime(t, "", "json")
			rt.Profile = "default"
			src := &fixtureSource{
				releases:    []selfupdate.Release{{TagName: "v0.6.0"}},
				downloadErr: map[string]error{failing: errors.New("network unreachable")},
			}
			deps := &SelfDeps{Source: src, Resolve: harmlessResolve(t)}
			cmd := NewSelfCmd(rt, testModules(), deps)
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{"update", "--force"})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected a download error")
			}
			if code := ExitCode(err); code != 1 {
				t.Errorf("ExitCode = %d, want 1", code)
			}
		})
	}
}

// TestSelfUpdateDownloadedFileGoneIsPlainError drives downloadBytes'
// own os.ReadFile failure: the Source reports a successful download
// but the path it names was never actually written.
func TestSelfUpdateDownloadedFileGoneIsPlainError(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	src.brokenPaths = map[string]bool{"checksums.txt": true}
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a read error")
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

// TestSelfUpdateSignatureVerificationFailure builds checksums.txt
// signed over ONE payload but serves a DIFFERENT one under the same
// name, so VerifySignature's own artifact-binding check fails before
// VerifyChecksum ever runs.
func TestSelfUpdateSignatureVerificationFailure(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	assetName, err := releaseAssetName("v0.6.0")
	if err != nil {
		t.Fatalf("releaseAssetName: %v", err)
	}
	archive, sum := writeArchive(t, []byte("some binary bytes"))
	signedChecksums := []byte(sum + "  " + assetName + "\n")
	sigBundle, trusted := signChecksums(t, signedChecksums)

	servedChecksums := []byte(sum + "  " + assetName + "  tampered\n")
	src := &fixtureSource{
		releases: []selfupdate.Release{{TagName: "v0.6.0"}},
		assets: map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               servedChecksums,
			"checksums.txt.sigstore.json": sigBundle,
		},
	}
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected a signature verification error")
	}
	if !strings.Contains(err.Error(), "verify signature") {
		t.Errorf("error = %v, want a verify-signature error", err)
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

// TestSelfUpdateExtractFailure serves an archive whose bytes match
// checksums.txt (so signature and checksum both pass) but which is
// not a valid gzip stream, so ExtractBinary fails.
func TestSelfUpdateExtractFailure(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	assetName, err := releaseAssetName("v0.6.0")
	if err != nil {
		t.Fatalf("releaseAssetName: %v", err)
	}
	notAnArchive := []byte("not a tar.gz file at all")
	sum := sha256.Sum256(notAnArchive)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
	sigBundle, trusted := signChecksums(t, checksums)

	src := &fixtureSource{
		releases: []selfupdate.Release{{TagName: "v0.6.0"}},
		assets: map[string][]byte{
			assetName:                     notAnArchive,
			"checksums.txt":               checksums,
			"checksums.txt.sigstore.json": sigBundle,
		},
	}
	deps := &SelfDeps{Source: src, Trusted: trusted, Resolve: harmlessResolve(t)}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected an extract error")
	}
	if !strings.Contains(err.Error(), "extract archive") {
		t.Errorf("error = %v, want an extract-archive error", err)
	}
}

// TestSelfUpdateSwapFailure points Resolve at a target that does not
// exist, so Swap's own os.Stat(target) fails.
func TestSelfUpdateSwapFailure(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return missing, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a swap error")
	}
	if !strings.Contains(err.Error(), "swap binary") {
		t.Errorf("error = %v, want a swap-binary error", err)
	}
}

// TestSelfUpdateProbeMalformedJSON drives probeNewBinary's decode
// failure: the "new binary" exits 0 but prints something that is not
// valid JSON.
func TestSelfUpdateProbeMalformedJSON(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  probeRunner(t, "not json at all"),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not verify") {
		t.Errorf("stderr missing probe-failure sentence:\n%s", stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.New != "0.6.0" {
		t.Errorf("report.New = %q, want the release tag without its v, 0.6.0", report.New)
	}
}

// TestSelfUpdateDefaultRunnerFailsSafely leaves deps.Runner nil, so
// runSelfUpdate falls back to exec.Command against the swapped target
// itself. The target's bytes are not a real executable, so the OS
// refuses to exec it (ENOEXEC) — proving the default wiring reaches a
// real exec.Command without ever running arbitrary code.
func TestSelfUpdateDefaultRunnerFailsSafely(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not verify") {
		t.Errorf("stderr missing probe-failure sentence:\n%s", stderr.String())
	}
}

// TestSelfUpdateStagesBesideTheTarget pins the placement the swap
// depends on. The staged file must sit in TARGET's directory: the
// swap is a rename, and os.Rename fails with EXDEV across a
// filesystem boundary — which is exactly what staging under $TMPDIR
// hits on the common Linux layout (tmpfs /tmp, binary in
// ~/.local/bin). No hermetic test can conjure a second filesystem, so
// the Swap seam observes where the rename reads from instead.
func TestSelfUpdateStagesBesideTheTarget(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")

	var sawStaged string
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  failingProbeRunner(t),
		Resolve: func() (string, error) { return target, nil },
		Swap: func(tgt, staged string) error {
			sawStaged = staged
			if filepath.Dir(staged) != filepath.Dir(tgt) {
				t.Errorf("staged in %s, want target's own directory %s",
					filepath.Dir(staged), filepath.Dir(tgt))
			}
			if _, err := os.Stat(staged); err != nil {
				t.Errorf("staged file is not on disk at swap time: %v", err)
			}
			return selfupdate.Swap(tgt, staged)
		},
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	if sawStaged == "" {
		t.Fatal("Swap was never called")
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Errorf("stat %s.new after a successful update: %v, want IsNotExist",
			target, err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-binary-bytes-for-v0.6.0" {
		t.Errorf("target content = %q, want the new binary's bytes", got)
	}
}

// TestSelfUpdateRemovesTheStagedFileOnFailure covers the window
// between placing the staged file and the rename consuming it: a
// failure there must not leave a "<binary>.new" sitting beside the
// install forever.
func TestSelfUpdateRemovesTheStagedFileOnFailure(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")

	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return target, nil },
		Swap: func(_, _ string) error {
			return errors.New("rename refused")
		},
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a swap error")
	}

	if _, statErr := os.Stat(target + ".new"); !os.IsNotExist(statErr) {
		t.Errorf("stat %s.new after a failed swap: %v, want IsNotExist",
			target, statErr)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(got) != "original-binary" {
		t.Errorf("target was modified by a failed swap: %q", got)
	}
}

// TestSelfUpdateProbeEmptyJSONKeepsTheCells covers a probe that
// succeeds and says nothing: `{}` decodes without error, and taking
// its values would blank the launcher row and every module cell that
// the swap has already earned.
func TestSelfUpdateProbeEmptyJSONKeepsTheCells(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  probeRunner(t, "{}"),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	var report selfUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.New != "0.6.0" {
		t.Errorf("report.New = %q, want the release tag without its v, 0.6.0", report.New)
	}
	for _, m := range report.Modules {
		if m.New != "unknown" {
			t.Errorf("module %s New = %q, want unknown", m.Name, m.New)
		}
	}
}

// TestLadderDeadlineExitsThree pins the exit code an expired budget
// produces, on BOTH rungs. 3 is the CLI-wide timeout code (408 and
// 504 already map there), and the deadline seam is new enough that
// nothing else observed which code it lands on — the gh rung's
// classification in particular goes out of its way NOT to call it an
// auth failure, and 1 would be just as plausible a mistake as 5.
//
// It lives at the cli layer because ExitCode does: internal/cli
// imports internal/selfupdate, so a selfupdate test cannot ask.
func TestLadderDeadlineExitsThree(t *testing.T) {
	expired, cancel := context.WithDeadline(
		context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	t.Run("gh rung", func(t *testing.T) {
		// A runner that execs this test binary rather than a real gh,
		// so the outcome cannot depend on whether gh is installed on
		// the machine running the suite.
		run := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, os.Args[0],
				"-test.run=^TestSelfHelperOK$", "--", "[]")
			cmd.Env = append(os.Environ(), "PGEDGE_SELF_TEST_HELPER_OK=1")
			return cmd
		}

		_, err := selfupdate.NewGHSource(run).Releases(expired)
		if err == nil {
			t.Fatal("want a deadline error, got nil")
		}
		if errors.Is(err, selfupdate.ErrGHUnauthenticated) {
			t.Fatalf("deadline classified as unauthenticated: %v", err)
		}
		if code := ExitCode(err); code != ExitTimeout {
			t.Errorf("ExitCode = %d, want %d (timed out)", code, ExitTimeout)
		}
	})

	t.Run("http rung", func(t *testing.T) {
		// A closed port: if the expired deadline somehow failed to
		// stop the request, this fails as "connection refused", which
		// is NOT a timeout and would report 1 here rather than pass.
		src := selfupdate.NewHTTPSource("http://127.0.0.1:1", "http://127.0.0.1:1")

		_, err := src.Releases(expired)
		if err == nil {
			t.Fatal("want a deadline error, got nil")
		}
		if code := ExitCode(err); code != ExitTimeout {
			t.Errorf("ExitCode = %d, want %d (timed out); err = %v",
				code, ExitTimeout, err)
		}
	})

	t.Run("download", func(t *testing.T) {
		src := selfupdate.NewHTTPSource("http://127.0.0.1:1", "http://127.0.0.1:1")

		_, err := src.Download(expired, "v0.6.0", "asset.tar.gz", t.TempDir())
		if err == nil {
			t.Fatal("want a deadline error, got nil")
		}
		if code := ExitCode(err); code != ExitTimeout {
			t.Errorf("ExitCode = %d, want %d (timed out); err = %v",
				code, ExitTimeout, err)
		}
	})
}

// TestSelfUpdateEmptyTargetIsRefusedNotSwapped drives the guard that
// keeps an unresolved target out of the swap path. Nothing in
// production returns an empty path with a nil error today; the guard
// is there so that if something ever does, the run stops instead of
// swapping into "".
func TestSelfUpdateEmptyTargetIsRefusedNotSwapped(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "json")
	rt.Profile = "default"
	src, trusted, _ := buildFixture(t, "v0.6.0", nil)

	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Resolve: func() (string, error) { return "", nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an unresolved target")
	}
	if !strings.Contains(err.Error(), "no swap target") {
		t.Errorf("error = %v, want an unresolved-target error", err)
	}
	if len(src.downloadCalls) != 0 {
		t.Errorf("downloaded assets before the guard: %v", src.downloadCalls)
	}
}

// TestReleaseAssetNameMatchesGoreleaser binds releaseAssetName's
// derivation to the config that names the release archives. The
// install.sh parity test above proves the two DOWNLOADERS agree; this
// proves they agree with the PRODUCER, so an archives.name_template
// or format edit cannot break `pgedge self update` for every user
// with no CI signal. goreleaser's default name_template is
// `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}` plus
// variant suffixes this build never sets, so an unset template is the
// only one the derivation matches.
func TestReleaseAssetNameMatchesGoreleaser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg struct {
		ProjectName string `yaml:"project_name"`
		Archives    []struct {
			NameTemplate    string   `yaml:"name_template"`
			Formats         []string `yaml:"formats"`
			FormatOverrides []struct {
				GOOS    string   `yaml:"goos"`
				Formats []string `yaml:"formats"`
			} `yaml:"format_overrides"`
		} `yaml:"archives"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode .goreleaser.yaml: %v", err)
	}
	if len(cfg.Archives) != 1 {
		t.Fatalf("%d archives configured, want 1", len(cfg.Archives))
	}
	archive := cfg.Archives[0]
	if archive.NameTemplate != "" {
		t.Errorf("archives[0].name_template = %q; releaseAssetName "+
			"derives goreleaser's DEFAULT template", archive.NameTemplate)
	}

	for _, goos := range []string{"linux", "darwin", "windows"} {
		formats := archive.Formats
		for _, o := range archive.FormatOverrides {
			if o.GOOS == goos {
				formats = o.Formats
			}
		}
		if len(formats) != 1 {
			t.Fatalf("%s: %d archive formats, want 1", goos, len(formats))
		}
		if got, want := archiveExtension(goos), formats[0]; got != want {
			t.Errorf("archiveExtension(%q) = %q, .goreleaser.yaml ships %q",
				goos, got, want)
		}
	}

	name, err := releaseAssetName("v0.6.0")
	if err != nil {
		t.Fatalf("releaseAssetName: %v", err)
	}
	want := fmt.Sprintf("%s_0.6.0_%s_%s.%s", cfg.ProjectName,
		runtime.GOOS, runtime.GOARCH, archiveExtension(runtime.GOOS))
	if name != want {
		t.Errorf("releaseAssetName = %q, want %q from .goreleaser.yaml", name, want)
	}
}

// TestLadderAnswersVerboseOnStderr proves the production wiring, not
// just the rungs: --verbose on the runtime reaches both rungs'
// diagnostics and they land on rt.Stderr, where every other client's
// do. The HTTP rung 404s so the gh rung runs too.
func TestLadderAnswersVerboseOnStderr(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	rt, stdout, stderr := testsupport.NewRuntime(t, "", "text")
	rt.Verbose = true
	src := newLadder(rt, srv.URL, srv.URL, failingProbeRunner(t))

	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("want error from a 404 primary and a failing gh, got nil")
	}
	out := stderr.String()
	if !strings.Contains(out, "> GET "+srv.URL+"/repos/pgEdge/pgedge-cli/releases") {
		t.Errorf("stderr missing the HTTP rung's request line:\n%s", out)
	}
	if !strings.Contains(out, "< 404 Not Found") {
		t.Errorf("stderr missing the HTTP rung's response line:\n%s", out)
	}
	if !strings.Contains(out, "> gh api repos/pgEdge/pgedge-cli/releases") {
		t.Errorf("stderr missing the gh rung's command line:\n%s", out)
	}
	if stdout.Len() != 0 {
		t.Errorf("diagnostics leaked to stdout:\n%s", stdout.String())
	}
}

// TestLadderIsSilentWithoutTheFlags is the guard on the wiring above:
// a quiet run adds no stderr line of its own.
func TestLadderIsSilentWithoutTheFlags(t *testing.T) {
	srv := releasesListServerCLI(t, `[{"tag_name":"v9.9.9","assets":[]}]`)
	defer srv.Close()

	rt, _, stderr := testsupport.NewRuntime(t, "", "text")
	src := newLadder(rt, srv.URL, srv.URL, failingProbeRunner(t))
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("quiet run wrote to stderr:\n%s", stderr.String())
	}
}

func releasesListServerCLI(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// --- completion refresh after a swap ----------------------------------

// dispatchRunner stands in for the NEW binary across both calls self
// update makes of it: `version -o json` answers probeJSON, and
// `completion <shell>` answers a script naming its shell, so a test
// can tell a refreshed file from the old one and from another shell's.
func dispatchRunner(t *testing.T, probeJSON string) selfupdate.Runner {
	t.Helper()
	return func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		payload := probeJSON
		if len(args) == 2 && args[0] == "completion" {
			payload = generatedCompletion(args[1])
		}
		cmd := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestSelfHelperOK$", "--", payload)
		cmd.Env = append(os.Environ(), "PGEDGE_SELF_TEST_HELPER_OK=1")
		return cmd
	}
}

// completionFailingRunner answers the version probe and fails every
// `completion` exec, for the refresh-failure branch.
func completionFailingRunner(t *testing.T, probeJSON string) selfupdate.Runner {
	t.Helper()
	ok := probeRunner(t, probeJSON)
	fail := failingProbeRunner(t)
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "completion" {
			return fail(ctx, name, args...)
		}
		return ok(ctx, name, args...)
	}
}

func generatedCompletion(shell string) string {
	return "# generated " + shell + " completion\n"
}

// installOldCompletion writes a stale script where `completion
// install` would have put shell's, and returns its path.
func installOldCompletion(t *testing.T, home, shell string) string {
	t.Helper()
	var dir, path string
	if shell == "powershell" {
		dir, path, _, _ = pwshScriptPath(home)
	} else {
		dir, path = completionScriptPath(home, shell)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte("# OLD "+shell+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// noPwsh makes resolvePwshProfile fall back to the convention path
// under HOME, so the PowerShell branch is exercised without a pwsh.
func noPwsh(t *testing.T) {
	t.Helper()
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) { return "", errors.New("no pwsh") }
	t.Cleanup(func() { pwshProfileQuery = orig })
}

func TestSelfUpdateRefreshesInstalledCompletions(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "text")
	noPwsh(t)
	home, _ := os.UserHomeDir()
	installed := map[string]string{}
	for _, shell := range []string{"bash", "zsh", "powershell"} {
		installed[shell] = installOldCompletion(t, home, shell)
	}
	_, fishPath := completionScriptPath(home, "fish")

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  dispatchRunner(t, `{"version":"v0.6.0","modules":[]}`),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr: %s", err, stderr.String())
	}

	for shell, path := range installed {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != generatedCompletion(shell) {
			t.Errorf("%s script = %q, want the new binary's %q", shell, got, generatedCompletion(shell))
		}
		if _, err := os.Stat(path + ".new"); err == nil {
			t.Errorf("%s: staging file %s.new left behind", shell, path)
		}
		if !strings.Contains(stderr.String(), "refreshed "+shell+" completion") {
			t.Errorf("stderr missing the %s refresh line:\n%s", shell, stderr.String())
		}
	}
	if _, err := os.Stat(fishPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fish script %s was created (%v); only installed scripts are refreshed", fishPath, err)
	}
	if strings.Contains(stderr.String(), "fish") {
		t.Errorf("stderr mentions fish, which was never installed:\n%s", stderr.String())
	}
	if strings.Contains(out.String(), "refreshed") {
		t.Errorf("refresh lines leaked to stdout:\n%s", out.String())
	}
}

func TestSelfUpdateCompletionRefreshFailureIsAWarning(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "text")
	noPwsh(t)
	home, _ := os.UserHomeDir()
	bashPath := installOldCompletion(t, home, "bash")

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  completionFailingRunner(t, `{"version":"v0.6.0","modules":[]}`),
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("a completion refresh failure must not fail the update: %v", err)
	}

	got, _ := os.ReadFile(bashPath)
	if string(got) != "# OLD bash\n" {
		t.Errorf("bash script = %q; a failed refresh must leave the old script intact", got)
	}
	if _, err := os.Stat(bashPath + ".new"); err == nil {
		t.Errorf("staging file %s.new left behind", bashPath)
	}
	if !strings.Contains(stderr.String(), "warning: could not refresh bash completion") {
		t.Errorf("stderr missing the warning:\n%s", stderr.String())
	}
	if !strings.Contains(out.String(), "v0.6.0") {
		t.Errorf("report not printed after the warning:\n%s", out.String())
	}
}

// TestSelfUpdateEmptyCompletionOutputLeavesTheScript pins the guard a
// reviewer mutation escaped: a `completion <shell>` that exits 0 but
// prints nothing must not truncate the installed script to 0 bytes
// and call it refreshed.
func TestSelfUpdateEmptyCompletionOutputLeavesTheScript(t *testing.T) {
	rt, out, stderr := testsupport.NewRuntime(t, "", "text")
	noPwsh(t)
	home, _ := os.UserHomeDir()
	bashPath := installOldCompletion(t, home, "bash")

	src, trusted, _ := buildFixture(t, "v0.6.0", nil)
	target := writeTargetFile(t, "original-binary")
	probeJSON := `{"version":"v0.6.0","modules":[]}`
	emptyCompletion := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "completion" {
			return probeRunner(t, "")(ctx, name, args...)
		}
		return probeRunner(t, probeJSON)(ctx, name, args...)
	}
	deps := &SelfDeps{
		Source:  src,
		Trusted: trusted,
		Runner:  emptyCompletion,
		Resolve: func() (string, error) { return target, nil },
	}
	cmd := NewSelfCmd(rt, testModules(), deps)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"update", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(bashPath)
	if string(got) != "# OLD bash\n" {
		t.Errorf("bash script = %q; an empty script must not replace the old one", got)
	}
	if !strings.Contains(stderr.String(), "warning: could not refresh bash completion") ||
		strings.Contains(stderr.String(), "refreshed bash") {
		t.Errorf("stderr should warn, not claim a refresh:\n%s", stderr.String())
	}
}
