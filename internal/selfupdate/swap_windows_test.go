//go:build windows

package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwapWindowsRenamesAsideThenIn(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge.exe")
	if err := os.WriteFile(target, []byte("old-bytes"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	newBinary := filepath.Join(dir, "pgedge.exe.new")
	if err := os.WriteFile(newBinary, []byte("new-bytes"), 0o755); err != nil {
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

	if _, err := os.Stat(target + ".old"); err != nil {
		t.Errorf("expected %s.old to exist, stat: %v", target, err)
	}
}

func TestSwapWindowsMissingNewBinaryRestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pgedge.exe")
	if err := os.WriteFile(target, []byte("old-bytes"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	err := Swap(target, filepath.Join(dir, "does-not-exist.exe"))
	if err == nil {
		t.Fatal("expected an error for a missing new binary")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %v does not name directory %s", err, dir)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("original target should be restored: %v", err)
	}
	if string(got) != "old-bytes" {
		t.Errorf("restored target content = %q, want %q", got, "old-bytes")
	}
}
