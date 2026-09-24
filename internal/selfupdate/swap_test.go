package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ResolveTarget / refusals ------------------------------------------------

func TestResolveTargetSucceedsForTestBinary(t *testing.T) {
	// The compiled test binary lives under a go-build temp dir outside
	// this repo (and outside Homebrew's Cellar), so it should resolve
	// cleanly with no refusal — the same "unknown" install method
	// doctor's own TestInstallMethodFrom pins for a path shaped like
	// this one.
	got, err := ResolveTarget()
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got == "" {
		t.Error("ResolveTarget returned an empty path")
	}
}

func TestCheckRefusalsHomebrew(t *testing.T) {
	path := "/opt/homebrew/Cellar/pgedge/0.5.0/bin/pgedge"

	_, err := checkRefusals(path)

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("checkRefusals(%q) = %v (%T), want *RefusalError", path, err, err)
	}
	if !strings.Contains(refusal.Msg, "brew upgrade") {
		t.Errorf("refusal message %q does not name `brew upgrade`", refusal.Msg)
	}
}

func TestCheckRefusalsGitWorktreeDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("seed .git directory: %v", err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("seed bin directory: %v", err)
	}
	target := filepath.Join(binDir, "pgedge")

	_, err := checkRefusals(target)

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("checkRefusals(%q) = %v (%T), want *RefusalError", target, err, err)
	}
	if !strings.Contains(refusal.Msg, "make build") {
		t.Errorf("refusal message %q does not name `make build`", refusal.Msg)
	}
}

func TestCheckRefusalsGitWorktreeFile(t *testing.T) {
	dir := t.TempDir()
	// A worktree's .git is a FILE holding a `gitdir: ...` pointer, not
	// a directory — the case that makes os.Lstat the right check
	// instead of one that only recognizes a directory.
	gitFile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitFile,
		[]byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatalf("seed .git file: %v", err)
	}
	target := filepath.Join(dir, "pgedge")

	_, err := checkRefusals(target)

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("checkRefusals(%q) = %v (%T), want *RefusalError", target, err, err)
	}
	if !strings.Contains(refusal.Msg, "make build") {
		t.Errorf("refusal message %q does not name `make build`", refusal.Msg)
	}
}

func TestCheckRefusalsPlainTempDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")

	got, err := checkRefusals(target)
	if err != nil {
		t.Fatalf("checkRefusals(%q): unexpected refusal: %v", target, err)
	}
	if got != target {
		t.Errorf("checkRefusals(%q) = %q, want %q", target, got, target)
	}
}

// --- Swap ---------------------------------------------------------------------

func TestSwapReplacesBytesPreservesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")
	if err := os.WriteFile(target, []byte("old-bytes"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	srcDir := t.TempDir()
	newBinary := filepath.Join(srcDir, "pgedge.new")
	// Deliberately a different starting mode from target's 0755, so a
	// pass here proves Swap took target's mode rather than merely
	// leaving newBinary's own mode in place by coincidence.
	if err := os.WriteFile(newBinary, []byte("new-bytes"), 0o600); err != nil {
		t.Fatalf("seed new binary: %v", err)
	}

	if err := Swap(target, newBinary); err != nil {
		t.Fatalf("Swap: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-bytes" {
		t.Errorf("target content = %q, want %q", got, "new-bytes")
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("target mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestSwapReadOnlyDirNamesDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	srcDir := t.TempDir()
	newBinary := filepath.Join(srcDir, "pgedge.new")
	if err := os.WriteFile(newBinary, []byte("new"), 0o755); err != nil {
		t.Fatalf("seed new binary: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := Swap(target, newBinary)
	if err == nil {
		t.Fatal("expected an error swapping into a read-only directory")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %v does not name directory %s", err, dir)
	}
	// Naming the directory is only half an answer; the spec wants the
	// remedy with it, because no retry of the same command fixes a
	// directory this user does not own.
	for _, want := range []string{"install script", "sudo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not suggest %q", err, want)
		}
	}
}

// --- ExtractBinary --------------------------------------------------------

// tarFixtureEntry describes one entry for writeTarGzFixture. typeflag
// defaults to a regular file (tar.TypeReg) when left zero.
type tarFixtureEntry struct {
	name     string
	content  []byte
	typeflag byte
	linkname string
}

func writeTarGzFixture(t *testing.T, entries []tarFixtureEntry) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.tar.gz")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture archive: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: typeflag,
			Mode:     0o755,
			Linkname: e.linkname,
		}
		if typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.content))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("write content %s: %v", e.name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture file: %v", err)
	}

	return path
}

func TestExtractBinaryHappyPath(t *testing.T) {
	archive := writeTarGzFixture(t, []tarFixtureEntry{
		{name: "README.txt", content: []byte("decoy")},
		{name: "pgedge", content: []byte("binary-bytes")},
	})
	dstDir := t.TempDir()

	path, err := ExtractBinary(archive, dstDir)
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if filepath.Dir(path) != dstDir {
		t.Errorf("extracted into %s, want %s", filepath.Dir(path), dstDir)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(got) != "binary-bytes" {
		t.Errorf("extracted content = %q, want %q", got, "binary-bytes")
	}
}

func TestExtractBinaryDecoyOnly(t *testing.T) {
	archive := writeTarGzFixture(t, []tarFixtureEntry{
		{name: "README.txt", content: []byte("decoy")},
	})

	_, err := ExtractBinary(archive, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an archive without the pgedge binary")
	}
	if !strings.Contains(err.Error(), "does not contain the pgedge binary") {
		t.Errorf("error = %v, want it to say the binary is missing", err)
	}
}

func TestExtractBinaryTraversalEntriesNeverExtracted(t *testing.T) {
	archive := writeTarGzFixture(t, []tarFixtureEntry{
		{name: "../evil", content: []byte("escape")},
		{name: "nested/pgedge", content: []byte("nested")},
		{name: "pgedge", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	dstDir := t.TempDir()

	_, err := ExtractBinary(archive, dstDir)
	if err == nil {
		t.Fatal("expected an error: no entry is an exact, regular `pgedge` member")
	}
	if !strings.Contains(err.Error(), "does not contain the pgedge binary") {
		t.Errorf("error = %v, want it to say the binary is missing", err)
	}

	left, err := os.ReadDir(dstDir)
	if err != nil {
		t.Fatalf("read dstDir: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("dstDir should be empty, found %v", left)
	}

	// Confirm ../evil never escaped dstDir upward either.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dstDir), "evil")); err == nil {
		t.Error("traversal entry ../evil escaped dstDir")
	}
}

func TestExtractZipHappyPath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "fixture.zip")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create fixture zip: %v", err)
	}
	zw := zip.NewWriter(f)

	w, err := zw.Create("README.txt")
	if err != nil {
		t.Fatalf("create decoy zip entry: %v", err)
	}
	if _, err := w.Write([]byte("decoy")); err != nil {
		t.Fatalf("write decoy zip entry: %v", err)
	}

	w, err = zw.Create("pgedge.exe")
	if err != nil {
		t.Fatalf("create pgedge.exe zip entry: %v", err)
	}
	if _, err := w.Write([]byte("windows-binary")); err != nil {
		t.Fatalf("write pgedge.exe zip entry: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture file: %v", err)
	}

	dstDir := t.TempDir()
	path, err := ExtractBinary(archive, dstDir)
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if filepath.Base(path) != "pgedge.exe" {
		t.Errorf("extracted path = %s, want basename pgedge.exe", path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(got) != "windows-binary" {
		t.Errorf("extracted content = %q, want %q", got, "windows-binary")
	}
}

// --- StageBeside -----------------------------------------------------

// TestStageBesideStagesInTargetDirectory pins the property the
// download directory cannot satisfy: the staged file has to be in
// target's OWN directory, because the swap that follows is a rename
// and a rename cannot cross a filesystem boundary.
func TestStageBesideStagesInTargetDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")
	if err := os.WriteFile(target, []byte("old-bytes"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	src := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(src, []byte("new-bytes"), 0o600); err != nil {
		t.Fatalf("seed extracted binary: %v", err)
	}

	staged, err := StageBeside(target, src)
	if err != nil {
		t.Fatalf("StageBeside: %v", err)
	}

	if filepath.Dir(staged) != dir {
		t.Errorf("staged in %s, want %s", filepath.Dir(staged), dir)
	}
	if staged != target+".new" {
		t.Errorf("staged = %s, want %s", staged, target+".new")
	}

	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != "new-bytes" {
		t.Errorf("staged content = %q, want %q", got, "new-bytes")
	}

	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("stat staged: %v", err)
	}
	// Target's 0755, not src's 0600 — and not 0755 masked by the
	// process umask either.
	if info.Mode().Perm() != 0o755 {
		t.Errorf("staged mode = %v, want 0755", info.Mode().Perm())
	}
}

// TestStageBesideThenSwapReplacesTarget walks the two halves in the
// order runSelfUpdate does, and asserts the staged file is gone
// afterwards — consumed by the rename, never left beside the binary.
func TestStageBesideThenSwapReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")
	if err := os.WriteFile(target, []byte("old-bytes"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	src := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(src, []byte("new-bytes"), 0o600); err != nil {
		t.Fatalf("seed extracted binary: %v", err)
	}

	staged, err := StageBeside(target, src)
	if err != nil {
		t.Fatalf("StageBeside: %v", err)
	}
	if err := Swap(target, staged); err != nil {
		t.Fatalf("Swap: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-bytes" {
		t.Errorf("target content = %q, want %q", got, "new-bytes")
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("stat %s after Swap: %v, want IsNotExist", staged, err)
	}
}

func TestStageBesideMissingTargetFails(t *testing.T) {
	src := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatalf("seed extracted binary: %v", err)
	}

	if _, err := StageBeside(
		filepath.Join(t.TempDir(), "does-not-exist"), src); err == nil {
		t.Fatal("expected an error staging beside a missing target")
	}
}

func TestStageBesideMissingSourceFails(t *testing.T) {
	target := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	staged, err := StageBeside(target, filepath.Join(t.TempDir(), "gone"))
	if err == nil {
		t.Fatal("expected an error staging from a missing source")
	}
	if staged != "" {
		t.Errorf("staged = %q on failure, want empty", staged)
	}
	if _, statErr := os.Stat(target + ".new"); !os.IsNotExist(statErr) {
		t.Errorf("stat %s.new after a failure: %v, want IsNotExist",
			target, statErr)
	}
}

// TestStageBesideReadOnlyDirNamesDirectoryAndRemedy covers the case
// that reaches a user most often — an install directory they do not
// own — which now fails at staging rather than at the rename.
func TestStageBesideReadOnlyDirNamesDirectoryAndRemedy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	src := filepath.Join(t.TempDir(), "pgedge")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatalf("seed extracted binary: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := StageBeside(target, src)
	if err == nil {
		t.Fatal("expected an error staging into a read-only directory")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %v does not name directory %s", err, dir)
	}
	for _, want := range []string{"install script", "sudo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not suggest %q", err, want)
		}
	}
}
