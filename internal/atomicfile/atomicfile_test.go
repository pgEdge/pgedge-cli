package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWriteReplacesContentAndPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
	assertNoStaging(t, dir)
}

// The staging file must be created in the target's own directory, not
// in the system temp directory, or the rename stops being atomic the
// moment the two are on different filesystems. t.TempDir, MkdirTemp
// and macOS all put both on one mount, so the cross-device failure is
// invisible to a direct test. This pins the property structurally
// instead: TMPDIR is made unwritable and the target's directory left
// writable, so a writer staging in TMPDIR fails at CreateTemp and a
// writer staging beside the target succeeds. (The inverse setup, a
// read-only target directory, does not discriminate: the rename INTO
// it fails for either writer.) The positive control proves TMPDIR is
// really unwritable.
func TestWriteStagesInTargetDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmp, 0o700) })
	if _, err := os.CreateTemp("", "control"); err == nil {
		t.Fatal("positive control: TMPDIR accepted a file; the test " +
			"cannot tell where staging happened")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := Write(p, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write with an unwritable TMPDIR failed, so the staging "+
			"file was created there rather than beside the target: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q", got)
	}
	assertNoStaging(t, dir)
}

// perm is the one argument-dependent behaviour, and os.CreateTemp's
// own 0600 hides a skipped Chmod from every 0600 caller.
func TestWriteHonoursPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := Write(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 644", perm)
	}
}

// A symlinked target keeps its link and the linked file gets the new
// content. Renaming over the link would replace the link with a
// regular file and leave the target file stale.
func TestWriteFollowsASymlinkedTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("linked file holds %q, want %q", got, "new")
	}
	assertNoStaging(t, dir)
}

func TestWriteFailureLeavesTargetAndNoStaging(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory at the target makes the rename fail after the
	// staging file has been fully written.
	blocker := filepath.Join(dir, "g")
	if err := os.MkdirAll(filepath.Join(blocker, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(blocker, []byte("x"), 0o600); err == nil {
		t.Fatal("Write over a non-empty directory succeeded")
	}
	assertNoStaging(t, dir)
	got, _ := os.ReadFile(p)
	if string(got) != "old" {
		t.Errorf("sibling target changed: %q", got)
	}
}

func TestWriteMissingDirectoryIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing", "f.txt")
	if err := Write(p, []byte("x"), 0o600); err == nil {
		t.Fatal("Write into a missing directory succeeded")
	}
}

func TestStagingPrefixMatchesWhatWriteCreates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	// Make the rename fail so the staging file's name is observable
	// through the error, which names the temp path.
	if err := os.MkdirAll(filepath.Join(p, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := Write(p, []byte("x"), 0o600)
	if err == nil {
		t.Fatal("want rename failure")
	}
	want := filepath.Join(dir, StagingPrefix("cfg.yaml"))
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name a staging file under %q", err, want)
	}
}

func TestConcurrentReaderNeverSeesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	body := func(i int) []byte {
		return []byte(fmt.Sprintf("%08d:%s\n", i, strings.Repeat("x", 4096)))
	}
	if err := Write(p, body(0), 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		torn  atomic.Int64
		reads atomic.Int64
		done  = make(chan struct{})
	)
	wg.Go(func() {
		defer close(done)
		for i := 1; i <= 300; i++ {
			if err := Write(p, body(i), 0o600); err != nil {
				t.Errorf("Write %d: %v", i, err)
				return
			}
		}
	})
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			got, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			reads.Add(1)
			if len(got) != len(body(0)) {
				torn.Add(1)
			}
		}
	})
	wg.Wait()
	if torn.Load() != 0 {
		t.Errorf("%d of %d reads saw a partial file", torn.Load(), reads.Load())
	}
	if reads.Load() == 0 {
		t.Fatal("reader never read: the test observed nothing")
	}
	assertNoStaging(t, dir)
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("staging residue left behind: %s", e.Name())
		}
	}
}
