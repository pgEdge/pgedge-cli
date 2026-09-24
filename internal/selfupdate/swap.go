package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RefusalError marks a target selfupdate must not touch. `self
// update` only ever manages a binary it fully owns in place; a
// Homebrew install and a copy inside a git working tree are each
// owned by something else instead, so ResolveTarget refuses rather
// than swapping over either.
type RefusalError struct{ Msg string }

func (e *RefusalError) Error() string { return e.Msg }

// ResolveTarget locates the running pgedge binary — os.Executable,
// resolved through any symlink — and applies the refusal checks
// before handing it back as a swap target.
func ResolveTarget() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate running executable: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// A symlink that cannot be resolved is not fatal to the
		// refusal checks: fall back to the unresolved path so they
		// still have something to inspect, rather than failing the
		// whole resolve on a problem they do not care about.
		resolved = exe
	}

	return checkRefusals(resolved)
}

// checkRefusals is ResolveTarget's decision split out so tests can
// drive it with a fabricated path, without depending on where the
// test binary itself happens to live.
func checkRefusals(path string) (string, error) {
	if InstallMethodFrom(path) == "homebrew" {
		return "", &RefusalError{Msg: fmt.Sprintf(
			"%s was installed via Homebrew; run `brew upgrade pgedge` "+
				"instead of `pgedge self update`", path)}
	}

	if root, ok := gitWorkingTreeRoot(path); ok {
		return "", &RefusalError{Msg: fmt.Sprintf(
			"%s is inside a git working tree (%s); run `make build` "+
				"instead of `pgedge self update`", path, root)}
	}

	return path, nil
}

// gitWorkingTreeRoot walks up from the directory holding path looking
// for a `.git` entry, and reports the directory that holds it. `.git`
// is a directory in a normal clone but a FILE in a worktree (holding
// a `gitdir: ...` pointer back to the real one), so this checks for
// either with Lstat rather than assuming a directory.
func gitWorkingTreeRoot(path string) (string, bool) {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// StageBeside copies src to "<target>.new" and returns that path,
// with target's own permission bits.
//
// Same directory, deliberately: that is what makes the following
// rename atomic, and on Linux it is what makes it POSSIBLE. Staging
// under $TMPDIR and renaming from there fails with EXDEV whenever
// /tmp is a different filesystem from the install directory — a tmpfs
// /tmp with the binary in ~/.local/bin is the modal non-root install,
// so this is the common case rather than an exotic one.
//
// A failure leaves nothing behind: whatever was written is removed
// before the error returns, so a caller that got an error has no path
// to clean up.
func StageBeside(target, src string) (string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", target, err)
	}

	in, err := os.Open(src) //nolint:gosec // G304: src is the binary this package just extracted.
	if err != nil {
		return "", fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	staged := target + ".new"
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // G304: staged is derived from the resolved target path.
	if err != nil {
		return "", swapError(target, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(staged)
		return "", fmt.Errorf("write %s: %w", staged, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(staged)
		return "", fmt.Errorf("close %s: %w", staged, err)
	}

	// Explicit rather than relying on OpenFile's mode argument, which
	// the process umask masks: a 0755 target would otherwise stage as
	// 0755 &^ umask and land unexecutable for group and other.
	if err := os.Chmod(staged, info.Mode().Perm()); err != nil {
		_ = os.Remove(staged)
		return "", fmt.Errorf("chmod %s: %w", staged, err)
	}

	return staged, nil
}

// swapError reports a swap failure naming the directory it could not
// write, with the remedy: the usual cause is an install directory
// this user does not own, which no retry of the same command fixes.
func swapError(target string, err error) error {
	return fmt.Errorf(
		"swap into %s: %w — re-run the install script, or use sudo, "+
			"if that directory is not yours to write", filepath.Dir(target), err)
}

// The binary name inside a release archive: "pgedge" in the tar.gz
// archives every non-Windows GOOS ships, "pgedge.exe" in the zip
// Windows ships. ExtractBinary picks between them by the archive's
// own extension rather than the host GOOS, so both extraction paths
// are exercised on any platform's test suite.
const (
	unixBinaryName    = "pgedge"
	windowsBinaryName = "pgedge.exe"
)

// errBinaryNotFound is returned when an archive has no member exactly
// matching the expected binary name.
var errBinaryNotFound = errors.New("archive does not contain the pgedge binary")

// ExtractBinary pulls the pgedge binary out of the archive at
// archivePath into dstDir and returns the extracted file's path.
//
// Only the EXACT top-level member name ("pgedge", or "pgedge.exe" in
// a zip) is ever extracted; every other entry is skipped. That is the
// path-traversal guard: a nested path ("foo/pgedge") and a name
// containing ".." ("../evil") both fail the exact-match test. A
// symlink member, even one literally named "pgedge", is rejected
// separately in each format — tar by its Typeflag, zip by the mode
// bit its entries carry instead. No entry's Linkname is ever followed
// or written.
func ExtractBinary(archivePath, dstDir string) (string, error) {
	if strings.EqualFold(filepath.Ext(archivePath), ".zip") {
		return extractZip(archivePath, dstDir)
	}
	return extractTarGz(archivePath, dstDir)
}

func extractTarGz(archivePath, dstDir string) (string, error) {
	f, err := os.Open(archivePath) //nolint:gosec // G304: caller's own downloaded archive path.
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar entry: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg || hdr.Name != unixBinaryName {
			continue
		}

		return writeExtracted(dstDir, unixBinaryName, tr)
	}

	return "", errBinaryNotFound
}

func extractZip(archivePath, dstDir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, entry := range zr.File {
		if entry.Name != windowsBinaryName {
			continue
		}
		// zip has no first-class symlink typeflag the way tar does;
		// a symlink member instead sets this bit in the stored mode.
		// Guard the same as the tar path: skip rather than follow it.
		if entry.Mode()&os.ModeSymlink != 0 || entry.FileInfo().IsDir() {
			continue
		}

		rc, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("open zip entry %s: %w", entry.Name, err)
		}
		path, err := writeExtracted(dstDir, windowsBinaryName, rc)
		_ = rc.Close()
		return path, err
	}

	return "", errBinaryNotFound
}

// writeExtracted copies r into dstDir/name, always as an executable
// file (0755): the mode an archived entry carries is not trusted, and
// every extracted file is meant to run regardless.
func writeExtracted(dstDir, name string, r io.Reader) (string, error) {
	dst := filepath.Join(dstDir, name)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // G304: name is one of two hardcoded constants, never archive-supplied.
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}

	if _, err := io.Copy(f, r); err != nil { //nolint:gosec // G110: release archives are checksum+signature verified before extraction.
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", dst, err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", dst, err)
	}

	return dst, nil
}
