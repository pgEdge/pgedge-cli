package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

// rcOnlyCase names a shell, where its startup file lives under home,
// and the exact line --rc-only must add to it.
type rcOnlyCase struct {
	shell string
	rc    func(home string) string
	line  string
}

func rcOnlyCases() []rcOnlyCase {
	return []rcOnlyCase{
		{"bash", func(h string) string {
			return filepath.Join(h, ".bashrc")
		}, `eval "$(pgedge completion bash)"`},
		{"zsh", func(h string) string {
			return filepath.Join(h, ".zshrc")
		}, `eval "$(pgedge completion zsh)"`},
		{"fish", func(h string) string {
			return filepath.Join(h, ".config", "fish", "config.fish")
		}, "pgedge completion fish | source"},
		{"powershell", func(h string) string {
			return filepath.Join(h, "psdir",
				"Microsoft.PowerShell_profile.ps1")
		}, "pgedge completion powershell | Out-String | " +
			"Invoke-Expression"},
	}
}

func TestInstallRCOnlyAppendsLoaderAndWritesNoScript(t *testing.T) {
	for _, tc := range rcOnlyCases() {
		t.Run(tc.shell, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			if tc.shell == "powershell" {
				stubPwshProfile(t, tc.rc(home))
			}
			var out, in bytes.Buffer
			cmd := installCmd(t, &out, &in)

			// writeRC=true, rcOnly=true, isTTY=false.
			if err := runCompletionInstallWith(
				cmd, tc.shell, false, true, true, false); err != nil {
				t.Fatalf("install --rc-only: %v", err)
			}
			data, err := os.ReadFile(tc.rc(home))
			if err != nil {
				t.Fatalf("read rc file: %v", err)
			}
			if !strings.Contains(string(data), tc.line) {
				t.Errorf("rc file missing loader line %q: %q",
					tc.line, string(data))
			}
			if _, ok := installedCompletionPath(home, tc.shell); ok {
				t.Error("--rc-only must not write a completion script")
			}
			if !strings.Contains(
				out.String(), "Detected shell: "+tc.shell) {
				t.Errorf("output missing detection line: %q",
					out.String())
			}
		})
	}
}

func TestInstallRCOnlyIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)
	line := `eval "$(pgedge completion zsh)"`

	for i := 0; i < 2; i++ {
		out.Reset()
		if err := runCompletionInstallWith(
			cmd, "zsh", false, true, true, false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if got := strings.Count(string(data), line); got != 1 {
		t.Errorf("loader line appears %d times, want 1", got)
	}
	if !strings.Contains(out.String(), "already") {
		t.Errorf("second run should say the line is already there: %q",
			out.String())
	}
}

func TestInstallRCOnlyTTYPromptAppends(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	in := bytes.NewBufferString("y\n")
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(in)
	cmd, _, _ := root.Find([]string{"completion", "install"})

	// rcOnly with neither --write-rc nor --force: prompt on a TTY.
	if err := runCompletionInstallWith(
		cmd, "zsh", false, false, true, true); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if !strings.Contains(string(data), `eval "$(pgedge completion zsh)"`) {
		t.Errorf("zshrc missing loader line: %q", string(data))
	}
}

func TestInstallRCOnlyTTYDeclineLeavesRCUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	in := bytes.NewBufferString("n\n")
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(in)
	cmd, _, _ := root.Find([]string{"completion", "install"})

	if err := runCompletionInstallWith(
		cmd, "zsh", false, false, true, true); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	if _, err := os.Stat(zshrcPath(home)); err == nil {
		t.Error("~/.zshrc should not have been created on decline")
	}
	if !strings.Contains(out.String(), "add the line above") {
		t.Errorf("expected DIY hint, got %q", out.String())
	}
}

// --force is the second way past the prompt, so a non-interactive run
// that passes it still appends.
func TestInstallRCOnlyForceAppendsWithoutPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "zsh", true, false, true, false); err != nil {
		t.Fatalf("install --rc-only --force: %v", err)
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if !strings.Contains(string(data), `eval "$(pgedge completion zsh)"`) {
		t.Errorf("zshrc missing loader line: %q", string(data))
	}
}

func TestInstallRCOnlyLeavesAnInstalledScriptAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, false); err != nil {
		t.Fatalf("file install: %v", err)
	}
	path, ok := installedCompletionPath(home, "bash")
	if !ok {
		t.Fatal("file install wrote no script")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}

	out.Reset()
	if err := runCompletionInstallWith(
		cmd, "bash", false, true, true, false); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("script removed by --rc-only: %v", err)
	}
	if string(after) != string(before) {
		t.Error("--rc-only rewrote the installed completion script")
	}
	if !strings.Contains(out.String(), displayPath(path, home)) {
		t.Errorf("--rc-only did not mention the installed script: %q",
			out.String())
	}
}

func TestUninstallRemovesLoaderLine(t *testing.T) {
	for _, tc := range rcOnlyCases() {
		t.Run(tc.shell, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			if tc.shell == "powershell" {
				stubPwshProfile(t, tc.rc(home))
			}
			var out, in bytes.Buffer
			if err := runCompletionInstallWith(
				installCmd(t, &out, &in), tc.shell,
				false, true, true, false); err != nil {
				t.Fatalf("install --rc-only: %v", err)
			}
			out.Reset()
			if err := runCompletionUninstall(
				uninstallCmd(t, &out, &in), tc.shell,
				true, false); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			data, err := os.ReadFile(tc.rc(home))
			if err != nil {
				t.Fatalf("read rc file: %v", err)
			}
			if strings.Contains(string(data), tc.line) {
				t.Errorf("loader line survived uninstall: %q",
					string(data))
			}
			if strings.Contains(string(data), rcMarker) {
				t.Errorf("marker comment survived uninstall: %q",
					string(data))
			}
			if !strings.Contains(
				out.String(), "Removed the loader line") {
				t.Errorf("uninstall did not report the line: %q",
					out.String())
			}
		})
	}
}

// Only the exact line goes: anything else in the rc file survives.
func TestUninstallLeavesOtherRCLinesAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const mine = "export EDITOR=vim"
	if err := os.WriteFile(zshrcPath(home),
		[]byte(mine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, in bytes.Buffer
	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "zsh",
		false, true, true, false); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "zsh", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if !strings.Contains(string(data), mine) {
		t.Errorf("uninstall removed a line it did not add: %q",
			string(data))
	}
}

// A run with neither a script nor a loader line reports both and
// changes nothing.
func TestUninstallReportsNeitherPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "bash", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("out = %q, want 'nothing to do'", out.String())
	}
	if !strings.Contains(out.String(), "loader line") {
		t.Errorf("out = %q, want the rc line mentioned", out.String())
	}
}

// The loader line alone is enough to make uninstall destructive, so it
// needs --force off a terminal just as a script does.
func TestUninstallLoaderLineNonTTYNeedsForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "zsh",
		false, true, true, false); err != nil {
		t.Fatalf("install --rc-only: %v", err)
	}
	err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "zsh", false, false)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
	data, _ := os.ReadFile(zshrcPath(home))
	if !strings.Contains(string(data), `eval "$(pgedge completion zsh)"`) {
		t.Errorf("loader line removed on refusal: %q", string(data))
	}
}

func TestRCLoaderLineUnsupportedShell(t *testing.T) {
	if got := rcLoaderLine("tcsh"); got != "" {
		t.Errorf("rcLoaderLine(tcsh) = %q, want empty", got)
	}
	if got, _ := shellRCPath(t.TempDir(), "tcsh"); got != "" {
		t.Errorf("shellRCPath(tcsh) = %q, want empty", got)
	}
}

func TestShellRCPathFishHonorsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-alt")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got, _ := shellRCPath(home, "fish")
	want := filepath.Join(xdg, "fish", "config.fish")
	if got != want {
		t.Errorf("shellRCPath(fish) = %q, want %q", got, want)
	}
}
