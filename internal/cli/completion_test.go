package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// execRoot builds a fresh root command, runs it with args, and
// returns combined stdout/stderr. It isolates HOME: root's setup hook
// runs for any real command reached through root.Execute,
// including these, and would otherwise load the developer's actual
// ~/.pgedge/cli/config.yaml.
func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := NewRootCmd(&module.Runtime{})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCompletionShellScripts(t *testing.T) {
	cases := []struct {
		shell  string
		marker string
	}{
		{"bash", "__start_pgedge"},
		{"zsh", "#compdef"},
		{"fish", "complete -c pgedge"},
		{"powershell", "Register-ArgumentCompleter"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			out, err := execRoot(t, "completion", tc.shell)
			if err != nil {
				t.Fatalf("completion %s: %v", tc.shell, err)
			}
			if !strings.Contains(out, tc.marker) {
				t.Errorf("completion %s output missing %q",
					tc.shell, tc.marker)
			}
		})
	}
}

func TestCompletionDefaultDisabled(t *testing.T) {
	root := NewRootCmd(&module.Runtime{})
	if !root.CompletionOptions.DisableDefaultCmd {
		t.Error("Cobra default completion command must be disabled")
	}
	// Exactly one command named "completion" must exist (ours).
	n := 0
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("found %d completion commands, want 1", n)
	}
}

// installCmd returns the `completion install` command from a fresh
// tree with out/in buffers attached to the root.
func installCmd(t *testing.T, out, in *bytes.Buffer) *cobra.Command {
	t.Helper()
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(in)
	c, _, err := root.Find([]string{"completion", "install"})
	if err != nil {
		t.Fatalf("find install cmd: %v", err)
	}
	return c
}

func TestInstallBashWritesScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(data), "__start_pgedge") {
		t.Error("bash script missing marker")
	}
	if !strings.Contains(out.String(), "Detected shell: bash") {
		t.Errorf("output missing detection line: %q", out.String())
	}
}

func TestInstallZshWritesScriptAndPrintsFpath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "zsh", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".zsh", "completions", "_pgedge")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("zsh script not written: %v", err)
	}
	if !strings.Contains(out.String(),
		"fpath=(~/.zsh/completions $fpath)") {
		t.Errorf("output missing fpath instruction: %q", out.String())
	}
}

func TestInstallOverwriteNeedsForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	// First install succeeds.
	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Second, non-interactive without --force, must fail loudly.
	err := runCompletionInstallWith(cmd, "bash", false, false, false, false)
	if err == nil {
		t.Fatal("expected error overwriting without --force")
	}
	// With --force it succeeds.
	if err := runCompletionInstallWith(
		cmd, "bash", true, false, false, false); err != nil {
		t.Fatalf("force overwrite: %v", err)
	}
}

func TestInstallFishWritesScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "fish", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".config", "fish",
		"completions", "pgedge.fish")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(data), "complete -c pgedge") {
		t.Error("fish script missing marker")
	}
	if !strings.Contains(out.String(), "Detected shell: fish") {
		t.Errorf("output missing detection line: %q", out.String())
	}
}

func TestInstallFishHonorsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-alt")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "fish", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(xdg, "fish", "completions", "pgedge.fish")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fish script not under XDG_CONFIG_HOME: %v", err)
	}
}

func TestInstallUnsupportedShell(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)
	err := runCompletionInstallWith(cmd, "tcsh", false, false, false, false)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
}

func TestDetectShellEmptyReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "")
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	err := runCompletionInstallWith(cmd, "", false, false, false, false)
	if err == nil {
		t.Fatal("expected error when shell cannot be detected")
	}
	if !strings.Contains(err.Error(), "could not detect shell") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDetectShellAutodetectsFromEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bash script not written: %v", err)
	}
	if !strings.Contains(out.String(), "Detected shell: bash") {
		t.Errorf("output missing detection line: %q", out.String())
	}
}

func TestInstallOverwriteTTYDeclines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	// First install succeeds.
	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original script: %v", err)
	}

	// Second install on a TTY, declining the overwrite prompt.
	in.WriteString("n\n")
	err = runCompletionInstallWith(cmd, "bash", false, false, false, true)
	if err == nil {
		t.Fatal("expected error when overwrite declined")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script after decline: %v", err)
	}
	if string(after) != string(original) {
		t.Error("script changed despite declined overwrite")
	}
}

func TestInstallOverwriteTTYAccepts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	// First install succeeds.
	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, false); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install on a TTY, accepting the overwrite prompt.
	in.WriteString("y\n")
	if err := runCompletionInstallWith(
		cmd, "bash", false, false, false, true); err != nil {
		t.Fatalf("overwrite with confirmation: %v", err)
	}

	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script after accept: %v", err)
	}
	if !strings.Contains(string(data), "__start_pgedge") {
		t.Error("bash script missing marker after overwrite")
	}
}

func zshrcPath(home string) string {
	return filepath.Join(home, ".zshrc")
}

// TestRunCompletionInstallWrapper exercises the thin runCompletionInstall
// wrapper (which detects the TTY) via a non-TTY bash autodetect path.
func TestRunCompletionInstallWrapper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstall(
		cmd, "", false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bash script not written: %v", err)
	}
}

func TestZshWriteRCAppendsLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	// writeRC=true, isTTY=false: append without prompting.
	if err := runCompletionInstallWith(
		cmd, "zsh", false, true, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(zshrcPath(home))
	if err != nil {
		t.Fatalf("read zshrc: %v", err)
	}
	if !strings.Contains(string(data),
		"fpath=(~/.zsh/completions $fpath)") {
		t.Errorf("zshrc missing fpath line: %q", string(data))
	}
}

func TestZshAppendIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	for i := 0; i < 2; i++ {
		// Use --force so the second run doesn't trip the overwrite
		// guard, and --write-rc to append.
		if err := runCompletionInstallWith(
			cmd, "zsh", true, true, false, false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(zshrcPath(home))
	got := strings.Count(string(data),
		"fpath=(~/.zsh/completions $fpath)")
	if got != 1 {
		t.Errorf("fpath line appears %d times, want 1", got)
	}
}

func TestZshInteractiveDeclineLeavesRCUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	in := bytes.NewBufferString("n\n")
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(in)
	cmd, _, _ := root.Find([]string{"completion", "install"})

	// isTTY=true, writeRC=false: prompt shown, user declines.
	if err := runCompletionInstallWith(
		cmd, "zsh", false, false, false, true); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(zshrcPath(home)); err == nil {
		t.Error("~/.zshrc should not have been created on decline")
	}
	if !strings.Contains(out.String(), "add the line above") {
		t.Errorf("expected DIY hint, got %q", out.String())
	}
}

func TestZshAlreadyConfiguredSkipsPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(zshrcPath(home),
		[]byte("fpath=(~/.zsh/completions $fpath)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)
	if err := runCompletionInstallWith(
		cmd, "zsh", false, false, false, true); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out.String(), "already") {
		t.Errorf("expected 'already configured' note, got %q",
			out.String())
	}
}

// uninstallCmd finds the completion uninstall command with the given
// stdin/stdout wired in.
func uninstallCmd(t *testing.T, out, in *bytes.Buffer) *cobra.Command {
	t.Helper()
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(in)
	c, _, err := root.Find([]string{"completion", "uninstall"})
	if err != nil {
		t.Fatalf("find uninstall cmd: %v", err)
	}
	return c
}

func TestUninstallRemovesBashScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer

	// Install then uninstall --force.
	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "bash",
		false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("script not installed: %v", err)
	}
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "bash", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("script still present: %v", err)
	}
	if !strings.Contains(out.String(), "Removed completion script") {
		t.Errorf("out = %q", out.String())
	}
}

func TestUninstallAbsentIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "zsh", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("out = %q, want 'nothing to do'", out.String())
	}
}

func TestUninstallNonTTYNeedsForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, in bytes.Buffer
	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "zsh",
		false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".zsh", "completions", "_pgedge")
	err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "zsh", false, false)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("script should remain on refusal: %v", statErr)
	}
}

func TestUninstallTTYDeclines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	in := bytes.NewBufferString("n\n")
	if err := runCompletionInstallWith(
		installCmd(t, &out, in), "bash",
		false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".local", "share",
		"bash-completion", "completions", "pgedge")
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, in), "bash", false, true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("script should remain after decline: %v", statErr)
	}
	if !strings.Contains(out.String(), "Left the completion script") {
		t.Errorf("out = %q", out.String())
	}
}

func TestUninstallUnsupportedShell(t *testing.T) {
	var out, in bytes.Buffer
	err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "tcsh", true, false)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
}

func TestUninstallEmptyShellErrors(t *testing.T) {
	t.Setenv("SHELL", "")
	var out, in bytes.Buffer
	err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "", true, false)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
}

func TestUninstallRemovesFishScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var out, in bytes.Buffer

	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "fish",
		false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(home, ".config", "fish",
		"completions", "pgedge.fish")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("script not installed: %v", err)
	}
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "fish", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("script still present: %v", err)
	}
}

func TestDetectShellNormalizesPwsh(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/pwsh")
	if got := detectShell(""); got != "powershell" {
		t.Errorf("detectShell($SHELL=pwsh) = %q, want powershell", got)
	}
	if got := detectShell("pwsh"); got != "powershell" {
		t.Errorf("detectShell(pwsh) = %q, want powershell", got)
	}
}

func TestDetectShellNormalizesCase(t *testing.T) {
	if got := detectShell("PowerShell"); got != "powershell" {
		t.Errorf("detectShell(PowerShell) = %q, want powershell", got)
	}
}

func TestDetectShellWindowsDefaultsToPowershell(t *testing.T) {
	t.Setenv("SHELL", "")
	orig := osName
	osName = "windows"
	t.Cleanup(func() { osName = orig })
	if got := detectShell(""); got != "powershell" {
		t.Errorf("detectShell on windows = %q, want powershell", got)
	}
}

func TestResolvePwshProfilePrefersPwshAnswer(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "somewhere", "profile.ps1")
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) { return want, nil }
	t.Cleanup(func() { pwshProfileQuery = orig })

	got, fromPwsh := resolvePwshProfile(home)
	if got != want || !fromPwsh {
		t.Errorf("resolvePwshProfile = (%q, %v), want (%q, true)",
			got, fromPwsh, want)
	}
}

func TestResolvePwshProfileFallsBackToConvention(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) {
		return "", errors.New("no pwsh")
	}
	t.Cleanup(func() { pwshProfileQuery = orig })

	got, fromPwsh := resolvePwshProfile(home)
	want := filepath.Join(home, ".config", "powershell",
		"Microsoft.PowerShell_profile.ps1")
	if got != want || fromPwsh {
		t.Errorf("resolvePwshProfile = (%q, %v), want (%q, false)",
			got, fromPwsh, want)
	}
}

func TestResolvePwshProfileWindowsConvention(t *testing.T) {
	home := t.TempDir()
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) {
		return "", errors.New("no pwsh")
	}
	t.Cleanup(func() { pwshProfileQuery = orig })
	origOS := osName
	osName = "windows"
	t.Cleanup(func() { osName = origOS })

	got, _ := resolvePwshProfile(home)
	want := filepath.Join(home, "Documents", "PowerShell",
		"Microsoft.PowerShell_profile.ps1")
	if got != want {
		t.Errorf("windows convention = %q, want %q", got, want)
	}
}

// TestQueryPwshProfileRealBinary is a REAL test: when pwsh is
// installed (it is on this machine and on GitHub ubuntu runners),
// actually ask it for $PROFILE.
//
// pwsh is warmed up first, because queryPwshProfile's 10-second
// bound is production behaviour (install.sh must never hang on a
// wedged PowerShell) and a COLD pwsh on a starved CI runner can
// take longer than that just to JIT itself — three CI failures at
// exactly 10.0s on 2026-08-06, on post-outage runners, all in this
// test. Warming keeps the assertion about "pwsh answers within the
// production bound", not about runner cold-start latency. A runner
// where even the warm-up cannot finish is skipped as unusable, not
// failed: that is the environment broken, not this code.
func TestQueryPwshProfileRealBinary(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(),
		90*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "pwsh", "-NoProfile",
		"-NonInteractive", "-Command", "exit").Run(); err != nil {
		t.Skipf("pwsh present but did not finish a warm-up run "+
			"(%v) — runner unusable for this test", err)
	}
	got, err := queryPwshProfile()
	if err != nil {
		t.Fatalf("queryPwshProfile: %v", err)
	}
	if filepath.Base(got) != "Microsoft.PowerShell_profile.ps1" {
		t.Errorf("profile = %q, want a Microsoft.PowerShell_profile.ps1 path", got)
	}
}

// stubPwshProfile points pwshProfileQuery at a fixed profile path
// under home and restores it afterwards.
func stubPwshProfile(t *testing.T, profile string) {
	t.Helper()
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) { return profile, nil }
	t.Cleanup(func() { pwshProfileQuery = orig })
}

func TestInstallPowershellWritesScriptAndPrintsLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "psdir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "powershell", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	script := filepath.Join(home, "psdir", "pgedge.complete.ps1")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(data), "Register-ArgumentCompleter") {
		t.Error("powershell script missing marker")
	}
	// Hardcode the expected literal (not derived from the same
	// formatting call the production code uses) so a regression back
	// to Go's %q quoting is actually caught.
	dotLine := ". '" + script + "'"
	if !strings.Contains(out.String(), dotLine) {
		t.Errorf("output missing dot-source line %q: %q",
			dotLine, out.String())
	}
	if _, err := os.Stat(profile); err == nil {
		t.Error("profile should not exist without --write-rc")
	}
}

func TestInstallPowershellWriteRCAppendsDotSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "psdir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "powershell", false, true, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	script := filepath.Join(home, "psdir", "pgedge.complete.ps1")
	if !strings.Contains(string(data), pwshDotLine(script)) {
		t.Errorf("profile missing dot-source line: %q", string(data))
	}
}

func TestInstallPowershellAppendIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "psdir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	for i := 0; i < 2; i++ {
		if err := runCompletionInstallWith(
			cmd, "powershell", true, true, false, false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	data, _ := os.ReadFile(profile)
	script := filepath.Join(home, "psdir", "pgedge.complete.ps1")
	got := strings.Count(string(data), pwshDotLine(script))
	if got != 1 {
		t.Errorf("dot-source line appears %d times, want 1", got)
	}
}

// pwshDotLine mirrors the production PowerShell single-quoted literal
// so tests can assert against it without repeating the literal
// everywhere; TestInstallPowershellWritesScriptAndPrintsLine
// hardcodes the literal directly so a quoting regression is still
// caught even if this helper drifted with production.
func pwshDotLine(path string) string {
	return ". '" + strings.ReplaceAll(path, "'", "''") + "'"
}

func TestInstallPowershellQuotesSingleQuoteInPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "ps'dir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "powershell", false, true, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	script := filepath.Join(home, "ps'dir", "pgedge.complete.ps1")
	want := ". '" + strings.ReplaceAll(script, "'", "''") + "'"
	if !strings.Contains(string(data), want) {
		t.Errorf("profile missing escaped dot-source line %q: %q",
			want, string(data))
	}
}

func TestInstallPowershellFallbackNoted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) {
		return "", errors.New("no pwsh")
	}
	t.Cleanup(func() { pwshProfileQuery = orig })
	var out, in bytes.Buffer
	cmd := installCmd(t, &out, &in)

	if err := runCompletionInstallWith(
		cmd, "powershell", false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	script := filepath.Join(home, ".config", "powershell",
		"pgedge.complete.ps1")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("script not at convention path: %v", err)
	}
	if !strings.Contains(out.String(), "default\nprofile location") {
		t.Errorf("output missing fallback note: %q", out.String())
	}
}

// TestUninstallPowershellFallbackNoted covers finding 2: when the
// pwshProfileQuery seam errors, uninstall must resolve the same
// convention path install would have used AND print the same
// fallback note, rather than silently trusting a possibly-wrong path.
func TestUninstallPowershellFallbackNoted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	orig := pwshProfileQuery
	pwshProfileQuery = func() (string, error) {
		return "", errors.New("no pwsh")
	}
	t.Cleanup(func() { pwshProfileQuery = orig })
	var out, in bytes.Buffer

	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "powershell", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !strings.Contains(out.String(), "default\nprofile location") {
		t.Errorf("output missing fallback note: %q", out.String())
	}
	script := filepath.Join(home, ".config", "powershell",
		"pgedge.complete.ps1")
	if !strings.Contains(out.String(), displayPath(script, home)) {
		t.Errorf("uninstall did not resolve the convention path: %q",
			out.String())
	}
}

func TestInstallPowershellTTYDeclineLeavesProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "psdir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out bytes.Buffer
	in := bytes.NewBufferString("n\n")
	root := NewRootCmd(&module.Runtime{})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(in)
	cmd, _, _ := root.Find([]string{"completion", "install"})

	if err := runCompletionInstallWith(
		cmd, "powershell", false, false, false, true); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(profile); err == nil {
		t.Error("profile should not have been created on decline")
	}
	if !strings.Contains(out.String(), "add the line above") {
		t.Errorf("expected DIY hint, got %q", out.String())
	}
}

func TestUninstallRemovesPowershellScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, "psdir",
		"Microsoft.PowerShell_profile.ps1")
	stubPwshProfile(t, profile)
	var out, in bytes.Buffer

	if err := runCompletionInstallWith(
		installCmd(t, &out, &in), "powershell",
		false, false, false, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	script := filepath.Join(home, "psdir", "pgedge.complete.ps1")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("script not installed: %v", err)
	}
	if err := runCompletionUninstall(
		uninstallCmd(t, &out, &in), "powershell", true, false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("script still present: %v", err)
	}
	if !strings.Contains(out.String(), "dot-source line") {
		t.Errorf("expected profile-line reminder, got %q", out.String())
	}
}
