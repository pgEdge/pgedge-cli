package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The refusal off a terminal must name what it would have removed. It
// said "the completion script" even with no script on disk.
func TestUninstallNonTTYRefusalNamesItsTarget(t *testing.T) {
	cases := []struct {
		name      string
		rcOnly    bool
		wantIn    string
		wantNotIn string
	}{
		{"script only", false,
			"the completion script at ~/.zsh/completions/_pgedge",
			"loader line"},
		{"loader line only", true,
			"the loader line in ~/.zshrc",
			"completion script"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			var out, in bytes.Buffer
			if err := runCompletionInstallWith(
				installCmd(t, &out, &in), "zsh",
				false, true, tc.rcOnly, false); err != nil {
				t.Fatalf("install: %v", err)
			}
			err := runCompletionUninstall(
				uninstallCmd(t, &out, &in), "zsh", false, false)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want *UsageError", err)
			}
			if !strings.Contains(ue.Msg, tc.wantIn) {
				t.Errorf("refusal %q does not name %q",
					ue.Msg, tc.wantIn)
			}
			if strings.Contains(ue.Msg, tc.wantNotIn) {
				t.Errorf("refusal %q names %q, which is not there",
					ue.Msg, tc.wantNotIn)
			}
		})
	}
}

// removeRCLine drops the marker comment above the loader line. The
// guard that the line above IS the marker is what keeps a hand-written
// rc intact: without it, uninstall deletes whatever the user put
// directly above their own loader line.
func TestUninstallKeepsTheLineAboveAHandWrittenLoader(t *testing.T) {
	loader := `eval "$(pgedge completion zsh)"`
	cases := []struct {
		name  string
		above string
	}{
		{"export", "export MINE=1"},
		{"comment", "# my own note"},
		{"another command", "source ~/.aliases"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			body := tc.above + "\n" + loader + "\n"
			if err := os.WriteFile(zshrcPath(home),
				[]byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, in bytes.Buffer
			if err := runCompletionUninstall(
				uninstallCmd(t, &out, &in), "zsh",
				true, false); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			data, err := os.ReadFile(zshrcPath(home))
			if err != nil {
				t.Fatalf("read zshrc: %v", err)
			}
			if !strings.Contains(string(data), tc.above) {
				t.Errorf("uninstall took the line above the loader "+
					"with it: %q", string(data))
			}
			if strings.Contains(string(data), loader) {
				t.Errorf("loader line survived: %q", string(data))
			}
		})
	}
}

// appendRCLine opens with a newline so the marker cannot glue itself
// to a last line that has none.
func TestInstallRCOnlyStartsTheMarkerOnItsOwnLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const mine = "export EDITOR=vim" // deliberately no trailing newline
	if err := os.WriteFile(zshrcPath(home),
		[]byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, in bytes.Buffer
	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "zsh",
		false, true, true, false); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	var sawMine, sawMarker bool
	for _, l := range strings.Split(string(data), "\n") {
		switch l {
		case mine:
			sawMine = true
		case rcMarker:
			sawMarker = true
		}
	}
	if !sawMine || !sawMarker {
		t.Errorf("%q and %q must each be a whole line; got %q",
			mine, rcMarker, string(data))
	}
}

// A ~/.zshrc symlinked into a dotfiles checkout must stay a symlink,
// and the file it points at must keep its own mode.
func TestUninstallKeepsASymlinkedRCFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	rcFile := filepath.Join(dir, "zshrc")
	loader := `eval "$(pgedge completion zsh)"`
	body := "export MINE=1\n\n" + rcMarker + "\n" + loader + "\n"
	if err := os.WriteFile(rcFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is subject to umask; set it explicitly.
	if err := os.Chmod(rcFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rcFile, zshrcPath(home)); err != nil {
		t.Fatal(err)
	}

	var out, in bytes.Buffer
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "zsh", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	li, err := os.Lstat(zshrcPath(home))
	if err != nil {
		t.Fatalf("lstat ~/.zshrc: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("~/.zshrc is no longer a symlink")
	}
	fi, err := os.Stat(rcFile)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("target mode = %v, want -rw-r--r--", fi.Mode().Perm())
	}
	data, err := os.ReadFile(rcFile)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if strings.Contains(string(data), loader) {
		t.Errorf("loader line survived in the target: %q", string(data))
	}
	if !strings.Contains(string(data), "export MINE=1") {
		t.Errorf("target lost the user's line: %q", string(data))
	}
}

// install then uninstall must leave the rc file exactly as it was. The
// blank line appendRCLine writes above the marker has to come back out
// with it, or repeated cycles accumulate blank lines.
func TestRCOnlyRoundTripLeavesTheRCFileByteIdentical(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const original = "export EDITOR=vim\n"
	if err := os.WriteFile(zshrcPath(home),
		[]byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, in bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := runCompletionInstallWith(
			installCmd(t, &out, &in), "zsh",
			false, true, true, false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
		if err := runCompletionUninstall(
			uninstallCmd(t, &out, &in), "zsh", true, false); err != nil {
			t.Fatalf("uninstall %d: %v", i, err)
		}
		data, err := os.ReadFile(zshrcPath(home))
		if err != nil {
			t.Fatalf("read zshrc: %v", err)
		}
		if string(data) != original {
			t.Fatalf("cycle %d left %q, want %q",
				i, string(data), original)
		}
	}
}
