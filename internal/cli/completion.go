package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// envShell is a named constant, not a literal, because
// TestEnvIsolationCoversProductionReads (internal/testsupport) uses
// this file's read as its positive control for constant resolution:
// it asserts constResolved["SHELL"] names this file. doctor.go reads
// SHELL as a bare literal, so only that assertion notices inlining.
const envShell = "SHELL"

// NewCompletionCmd builds the `pgedge completion` command group. It
// replaces Cobra's auto-generated completion command (disabled in
// NewRootCmd) so the CLI owns the command surface: a documentable,
// gate-tested tree plus the `install` convenience subcommand.
func NewCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate and install shell completion scripts",
		Long: `completion generates shell completion scripts for pgedge
and helps you install them.

Run 'pgedge completion install' to set it up automatically for
bash, zsh, fish or PowerShell, or print a script for any shell
and wire it up yourself.

'install --rc-only' writes no script at all: it adds one line to
your shell's startup file that evaluates this binary's completion
script at each shell start, so completion always matches the
installed binary.

Example:
  pgedge completion install
  pgedge completion install --rc-only
  pgedge completion zsh > ~/.zsh/completions/_pgedge`,
		Annotations: map[string]string{
			AnnotationProfileExempt: "reads $SHELL, never the config",
		},
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		cmd.AddCommand(newCompletionShellCmd(sh))
	}
	cmd.AddCommand(newCompletionInstallCmd())
	cmd.AddCommand(newCompletionUninstallCmd())
	return cmd
}

func newCompletionShellCmd(shell string) *cobra.Command {
	return &cobra.Command{
		Use:   shell,
		Short: "Print the " + shell + " completion script",
		Long: fmt.Sprintf(`Print the %s completion script to stdout.

Pipe it to the location your shell loads completions from, or run
'pgedge completion install' to do this automatically.

Example:
  pgedge completion %s`, shell, shell),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeCompletionScript(
				cmd.Root(), shell, cmd.OutOrStdout())
		},
	}
}

// writeCompletionScript writes the completion script for shell to w,
// generated from the root command tree.
func writeCompletionScript(
	root *cobra.Command, shell string, w io.Writer,
) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(w, true)
	case "zsh":
		return root.GenZshCompletion(w)
	case "fish":
		return root.GenFishCompletion(w, true)
	case "powershell":
		return root.GenPowerShellCompletionWithDesc(w)
	default:
		return fmt.Errorf("unsupported shell %q", shell)
	}
}

func newCompletionInstallCmd() *cobra.Command {
	var shell string
	var force, writeRC, rcOnly bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install shell completion for your shell",
		Long: `install detects your shell, writes the pgedge completion
script to the directory that shell loads completions from, and
prints any remaining step (such as a zsh fpath line or a
PowerShell profile line).

With --rc-only no script is written: install adds a single line to
your shell's startup file that evaluates this binary's completion
script at each shell start. That costs roughly 10–20 ms on a warm
cache and can never fall out of step with the installed binary.

Supports bash, zsh, fish and PowerShell. $SHELL rarely names
PowerShell, so pass --shell powershell explicitly on macOS or
Linux; on Windows it is the default.

Example:
  pgedge completion install
  pgedge completion install --rc-only
  pgedge completion install --shell powershell`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCompletionInstall(cmd, shell, force, writeRC, rcOnly)
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "",
		"Shell to install for: bash, zsh, fish or powershell "+
			"(default: autodetect $SHELL)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the prompt: overwrite a script, or append the "+
			"--rc-only line")
	cmd.Flags().BoolVar(&writeRC, "write-rc", false,
		"Append the zsh fpath, PowerShell profile or --rc-only "+
			"loader line without prompting")
	cmd.Flags().BoolVar(&rcOnly, "rc-only", false,
		"Add the one-line loader to your shell startup file and "+
			"write no script")
	return cmd
}

func runCompletionInstall(
	cmd *cobra.Command, shell string, force, writeRC, rcOnly bool,
) error {
	return runCompletionInstallWith(cmd, shell, force, writeRC, rcOnly,
		term.IsTerminal(int(os.Stdin.Fd())))
}

// runCompletionInstallWith is the testable core: isTTY is passed in
// rather than detected so tests exercise both branches.
func runCompletionInstallWith(
	cmd *cobra.Command, shellFlag string, force, writeRC, rcOnly, isTTY bool,
) error {
	shell := detectShell(shellFlag)
	switch shell {
	case "bash", "zsh", "fish", "powershell":
	case "":
		return &UsageError{Msg: "could not detect shell; pass " +
			"--shell bash, zsh, fish or powershell"}
	default:
		return &UsageError{Msg: fmt.Sprintf(
			"unsupported shell %q; supported: bash, zsh, fish, "+
				"powershell", shell)}
	}
	if rcOnly {
		return installRCOnly(cmd, shell, force, writeRC, isTTY)
	}
	switch shell {
	case "bash":
		return installBashCompletion(cmd, force, isTTY)
	case "zsh":
		return installZshCompletion(cmd, force, writeRC, isTTY)
	case "fish":
		return installFishCompletion(cmd, force, isTTY)
	default:
		return installPowershellCompletion(cmd, force, writeRC, isTTY)
	}
}

// rcLoaderLine is the one line that makes shell load pgedge
// completion by evaluating `pgedge completion <shell>` at startup.
// Cobra's generated scripts self-register when evaluated — bash's
// ends in `complete ... pgedge`, zsh's carries `compdef _pgedge
// pgedge` — so no file on disk is needed. An unsupported shell
// returns "".
func rcLoaderLine(shell string) string {
	switch shell {
	case "bash":
		return `eval "$(pgedge completion bash)"`
	case "zsh":
		return `eval "$(pgedge completion zsh)"`
	case "fish":
		return "pgedge completion fish | source"
	case "powershell":
		return "pgedge completion powershell | Out-String | " +
			"Invoke-Expression"
	default:
		return ""
	}
}

// shellRCPath returns the startup file shell reads on every start.
// viaConvention is true only for PowerShell, when no PowerShell
// binary answered and the conventional profile location was assumed.
// An unsupported shell returns "".
func shellRCPath(home, shell string) (path string, viaConvention bool) {
	switch shell {
	case "bash":
		return filepath.Join(home, ".bashrc"), false
	case "zsh":
		return filepath.Join(home, ".zshrc"), false
	case "fish":
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		return filepath.Join(cfg, "fish", "config.fish"), false
	case "powershell":
		profile, fromPwsh := resolvePwshProfile(home)
		return profile, !fromPwsh
	default:
		return "", false
	}
}

// restartHint tells the user how to pick up a change to shell's
// startup file.
func restartHint(shell string) string {
	if shell == "powershell" {
		return "Restart PowerShell."
	}
	return fmt.Sprintf("Restart your shell (or run: exec %s).", shell)
}

// installRCOnly adds the one-line loader to the shell's startup file
// and writes nothing else. A script an earlier file install left
// behind is reported and left alone. The append follows --write-rc's
// contract: unconditional with --write-rc or --force, otherwise a TTY
// prompt, and off a terminal the line is printed rather than written.
func installRCOnly(
	cmd *cobra.Command, shell string, force, writeRC, isTTY bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	line := rcLoaderLine(shell)
	// One lookup for both paths: each call to pwshScriptPath can
	// spawn pwsh, bounded at 10 seconds.
	var rcPath, script string
	var viaConvention bool
	if shell == "powershell" {
		var fromPwsh bool
		_, script, rcPath, fromPwsh = pwshScriptPath(home)
		viaConvention = !fromPwsh
	} else {
		rcPath, _ = shellRCPath(home, shell)
		_, script = completionScriptPath(home, shell)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Detected shell: %s\n", shell)
	if viaConvention {
		fmt.Fprint(out,
			"No PowerShell binary found to ask for $PROFILE; "+
				"using the default\nprofile location.\n")
	}
	if _, err := os.Stat(script); err == nil {
		fmt.Fprintf(out,
			"A completion script is still installed at %s; "+
				"--rc-only\nleft it alone. Remove it with 'pgedge "+
				"completion uninstall'.\n",
			displayPath(script, home))
	}
	fmt.Fprintln(out)

	already, err := fileContainsLine(rcPath, line)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprintf(out, "%s already loads pgedge completion.\n%s\n",
			displayPath(rcPath, home), restartHint(shell))
		return nil
	}

	fmt.Fprintf(out,
		"This route writes no script. It needs one line in %s:\n\n"+
			"    %s\n\n",
		displayPath(rcPath, home), line)

	doAppend := writeRC || force
	if !doAppend {
		doAppend = confirmPrompt(cmd, fmt.Sprintf(
			"Want me to add that line to %s for you now?",
			displayPath(rcPath, home)), isTTY)
	}
	if !doAppend {
		fmt.Fprintf(out,
			"Left %s unchanged — add the line above when you're "+
				"ready.\n", displayPath(rcPath, home))
		return nil
	}
	if err := appendRCLine(rcPath, line); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added the loader line to %s. %s\n",
		displayPath(rcPath, home), restartHint(shell))
	return nil
}

// completionScriptPath returns the directory and full path where the
// completion script for shell lives. It is the single source of truth
// shared by install and uninstall, except for PowerShell: that shell's
// path depends on a live profile lookup, so it goes through
// pwshScriptPath instead. An unsupported shell returns empty strings.
func completionScriptPath(home, shell string) (dir, path string) {
	switch shell {
	case "bash":
		dir = filepath.Join(home, ".local", "share",
			"bash-completion", "completions")
		return dir, filepath.Join(dir, "pgedge")
	case "zsh":
		dir = filepath.Join(home, ".zsh", "completions")
		return dir, filepath.Join(dir, "_pgedge")
	case "fish":
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		dir = filepath.Join(cfg, "fish", "completions")
		return dir, filepath.Join(dir, "pgedge.fish")
	default:
		return "", ""
	}
}

func newCompletionUninstallCmd() *cobra.Command {
	var shell string
	var force bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove an installed shell completion script",
		Long: `uninstall detects your shell (or use --shell) and removes the pgedge
completion script it installed, together with the --rc-only loader
line if present. Supports bash, zsh, fish and PowerShell. Safe to
re-run; anything not present is reported and nothing is removed. A
zsh fpath line or a PowerShell dot-source line remains in place;
remove it by hand.

Example:
  pgedge completion uninstall
  pgedge completion uninstall --shell zsh --force`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCompletionUninstall(cmd, shell, force,
				term.IsTerminal(int(os.Stdin.Fd())))
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "",
		"Shell to uninstall for: bash, zsh, fish or powershell "+
			"(default: autodetect $SHELL)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Remove without prompting for confirmation")
	return cmd
}

// runCompletionUninstall removes whatever `completion install` put in
// place for the resolved shell: the completion script, the --rc-only
// loader line, or both. isTTY is passed in (not detected) so tests
// exercise both the prompt and non-prompt paths.
func runCompletionUninstall(
	cmd *cobra.Command, shellFlag string, force, isTTY bool,
) error {
	shell := detectShell(shellFlag)
	switch shell {
	case "bash", "zsh", "fish", "powershell":
	case "":
		return &UsageError{Msg: "could not detect shell; pass " +
			"--shell bash, zsh, fish or powershell"}
	default:
		return &UsageError{Msg: fmt.Sprintf(
			"unsupported shell %q; supported: bash, zsh, fish, "+
				"powershell", shell)}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	out := cmd.OutOrStdout()
	// PowerShell's script and profile come out of one lookup: each
	// call to pwshScriptPath can spawn pwsh, bounded at 10 seconds.
	var path, rcPath string
	if shell == "powershell" {
		var fromPwsh bool
		_, path, rcPath, fromPwsh = pwshScriptPath(home)
		if !fromPwsh {
			fmt.Fprint(out,
				"No PowerShell binary found to ask for $PROFILE; "+
					"using the default\nprofile location.\n\n")
		}
	} else {
		_, path = completionScriptPath(home, shell)
		rcPath, _ = shellRCPath(home, shell)
	}

	scriptPresent := true
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat completion script: %w", err)
		}
		scriptPresent = false
	}

	loader := rcLoaderLine(shell)
	rcPresent, err := fileContainsLine(rcPath, loader)
	if err != nil {
		return err
	}

	if !scriptPresent && !rcPresent {
		fmt.Fprintf(out,
			"No %s completion script at %s and no loader line in "+
				"%s;\nnothing to do.\n",
			shell, displayPath(path, home), displayPath(rcPath, home))
		return nil
	}

	var targets []string
	if scriptPresent {
		targets = append(targets,
			"the completion script at "+displayPath(path, home))
	}
	if rcPresent {
		targets = append(targets,
			"the loader line in "+displayPath(rcPath, home))
	}
	joined := strings.Join(targets, " and ")

	if !force {
		if !isTTY {
			return &UsageError{Msg: "removing " + joined + " is " +
				"destructive; re-run with --force to confirm, or run " +
				"interactively"}
		}
		if !confirmPrompt(cmd, fmt.Sprintf(
			"Remove %s for %s?", joined, shell), isTTY) {
			fmt.Fprintf(out, "Left %s in place.\n", joined)
			return nil
		}
	}

	fmt.Fprintf(out, "Detected shell: %s\n", shell)
	if scriptPresent {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove completion script: %w", err)
		}
		fmt.Fprintf(out, "Removed completion script %s\n",
			displayPath(path, home))
		switch shell {
		case "zsh":
			fmt.Fprintln(out,
				"If you added the fpath line to ~/.zshrc, remove it "+
					"by hand.")
		case "powershell":
			fmt.Fprintln(out,
				"If you added the dot-source line to your PowerShell "+
					"profile, remove it by hand.")
		}
	}
	if rcPresent {
		if err := removeRCLine(rcPath, loader); err != nil {
			return err
		}
		fmt.Fprintf(out, "Removed the loader line from %s\n",
			displayPath(rcPath, home))
	}
	return nil
}

// osName is runtime.GOOS behind a seam so tests can exercise the
// Windows branches on any platform. This package has no parallel
// tests, so mutating this seam mid-test is safe.
var osName = runtime.GOOS

func detectShell(explicit string) string {
	sh := strings.ToLower(explicit)
	if sh == "" {
		env := os.Getenv(envShell)
		if env == "" {
			if osName == "windows" {
				// Windows sets no $SHELL; the native shell is
				// PowerShell.
				return "powershell"
			}
			return ""
		}
		sh = strings.ToLower(filepath.Base(env))
	}
	if sh == "pwsh" {
		return "powershell"
	}
	return sh
}

// pwshProfileQuery asks PowerShell itself where $PROFILE lives.
// It is a package var so tests never spawn a real shell. Asking
// beats convention because OneDrive commonly relocates Documents
// on Windows, moving the real profile.
var pwshProfileQuery = queryPwshProfile

func queryPwshProfile() (string, error) {
	names := []string{"pwsh"}
	if osName == "windows" {
		names = append(names, "powershell")
	}
	for _, name := range names {
		exe, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		// install.sh runs this under curl|sh, so a hung PowerShell
		// process must never hang the install; bound it.
		ctx, cancel := context.WithTimeout(
			context.Background(), 10*time.Second)
		//nolint:gosec // G204: binary is a fixed name resolved via LookPath
		out, err := exec.CommandContext(ctx, exe,
			"-NoProfile", "-NonInteractive",
			"-Command", "Write-Output $PROFILE").Output()
		cancel()
		if err != nil {
			continue
		}
		if p := strings.TrimSpace(string(out)); p != "" {
			return p, nil
		}
	}
	return "", errors.New("no PowerShell binary answered")
}

// resolvePwshProfile returns the PowerShell profile path,
// preferring PowerShell's own answer. fromPwsh is false when the
// convention fallback was used, so callers can say so.
func resolvePwshProfile(home string) (profile string, fromPwsh bool) {
	if p, err := pwshProfileQuery(); err == nil {
		return p, true
	}
	if osName == "windows" {
		// Assumes PowerShell 7's Documents\PowerShell layout, and is
		// near-unreachable in practice: powershell.exe, tried after
		// pwsh on Windows, almost always answers.
		return filepath.Join(home, "Documents", "PowerShell",
			"Microsoft.PowerShell_profile.ps1"), false
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "powershell",
		"Microsoft.PowerShell_profile.ps1"), false
}

// pwshScriptPath resolves where the PowerShell completion script
// lives: next to the profile PowerShell itself reports, or next to
// the convention profile when no PowerShell binary answers. It also
// returns the profile path itself, since install needs it as the
// dot-source target.
func pwshScriptPath(home string) (dir, path, profile string, fromPwsh bool) {
	profile, fromPwsh = resolvePwshProfile(home)
	dir = filepath.Dir(profile)
	path = filepath.Join(dir, pwshScriptName)
	return dir, path, profile, fromPwsh
}

func installBashCompletion(
	cmd *cobra.Command, force, isTTY bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir, path := completionScriptPath(home, "bash")
	if err := writeScriptFile(
		cmd, "bash", dir, path, force, isTTY); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"Detected shell: bash\n"+
			"Wrote completion script to %s\n\n"+
			"bash-completion loads this automatically. Restart your "+
			"shell\n(or run: exec bash) to enable it.\n",
		displayPath(path, home))
	return nil
}

// installFishCompletion writes the script into fish's completions
// directory. fish auto-loads from there, so unlike zsh there is no
// rc file to touch.
func installFishCompletion(
	cmd *cobra.Command, force, isTTY bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir, path := completionScriptPath(home, "fish")
	if err := writeScriptFile(
		cmd, "fish", dir, path, force, isTTY); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"Detected shell: fish\n"+
			"Wrote completion script to %s\n\n"+
			"fish loads this automatically. Restart your shell\n"+
			"(or run: exec fish) to enable it.\n",
		displayPath(path, home))
	return nil
}

// installZshCompletion writes the script, always prints the fpath
// instruction, and optionally appends the fpath line to ~/.zshrc (on
// a TTY when confirmed, or unconditionally with --write-rc). The
// append is idempotent.
func installZshCompletion(
	cmd *cobra.Command, force, writeRC, isTTY bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir, path := completionScriptPath(home, "zsh")
	if err := writeScriptFile(
		cmd, "zsh", dir, path, force, isTTY); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out,
		"Detected shell: zsh\n"+
			"Wrote completion script to %s\n\n",
		displayPath(path, home))

	rcPath := filepath.Join(home, ".zshrc")
	const fpathLine = "fpath=(~/.zsh/completions $fpath)"

	already, err := fileContainsLine(rcPath, fpathLine)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprint(out,
			"~/.zshrc already sources ~/.zsh/completions.\n"+
				"Restart your shell (or run: exec zsh) to enable "+
				"completion.\n")
		return nil
	}

	// Always print the self-contained instruction.
	fmt.Fprintf(out,
		"zsh only loads completions from directories listed in "+
			"$fpath,\nand ~/.zsh/completions is not one of them yet. "+
			"To enable\ncompletion, add this line to ~/.zshrc:\n\n"+
			"    %s\n\nThen restart your shell (or run: exec zsh).\n",
		fpathLine)

	doAppend := writeRC
	if !writeRC {
		doAppend = confirmPrompt(cmd,
			"Want me to add that line to ~/.zshrc for you now?", isTTY)
	}
	if !doAppend {
		fmt.Fprint(out,
			"Left ~/.zshrc unchanged — add the line above when "+
				"you're ready.\n")
		return nil
	}
	if err := appendRCLine(rcPath, fpathLine); err != nil {
		return err
	}
	fmt.Fprint(out,
		"Added the fpath line to ~/.zshrc. Restart your shell "+
			"(or run: exec zsh).\n")
	return nil
}

// pwshScriptName is the completion script written next to the
// PowerShell profile; the profile dot-sources it by absolute path.
const pwshScriptName = "pgedge.complete.ps1"

// installPowershellCompletion writes the script next to the
// PowerShell profile and follows the zsh flow: always print the
// profile line, offer to append it (TTY prompt or --write-rc),
// append idempotently.
func installPowershellCompletion(
	cmd *cobra.Command, force, writeRC, isTTY bool,
) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir, path, profile, fromPwsh := pwshScriptPath(home)
	if err := writeScriptFile(
		cmd, "powershell", dir, path, force, isTTY); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out,
		"Detected shell: powershell\n"+
			"Wrote completion script to %s\n\n",
		displayPath(path, home))
	if !fromPwsh {
		fmt.Fprint(out,
			"No PowerShell binary found to ask for $PROFILE; "+
				"using the default\nprofile location.\n\n")
	}

	dotLine := ". '" + strings.ReplaceAll(path, "'", "''") + "'"
	already, err := fileContainsLine(profile, dotLine)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprint(out,
			"Your PowerShell profile already loads the script.\n"+
				"Restart PowerShell to enable completion.\n")
		return nil
	}

	fmt.Fprintf(out,
		"PowerShell loads completions from your profile. To "+
			"enable\ncompletion, add this line to %s:\n\n"+
			"    %s\n\nThen restart PowerShell.\n",
		displayPath(profile, home), dotLine)

	doAppend := writeRC
	if !writeRC {
		doAppend = confirmPrompt(cmd,
			"Want me to add that line to your profile for you now?",
			isTTY)
	}
	if !doAppend {
		fmt.Fprint(out,
			"Left your profile unchanged — add the line above "+
				"when you're ready.\n")
		return nil
	}
	if err := appendRCLine(profile, dotLine); err != nil {
		return err
	}
	fmt.Fprint(out,
		"Added the line to your PowerShell profile. Restart "+
			"PowerShell.\n")
	return nil
}

// fileContainsLine reports whether path has a line equal to line
// (after trimming surrounding whitespace). A missing file is not an
// error and reports false.
func fileContainsLine(path, line string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: fixed, home-relative rc path
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == line {
			return true, nil
		}
	}
	return false, nil
}

// rcMarker labels the lines install adds, so uninstall can take its
// own comment away with the line it introduces.
const rcMarker = "# Added by 'pgedge completion install'"

// appendRCLine appends line to path (creating it, and its directory,
// if needed) with a marker comment.
func appendRCLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile( //nolint:gosec // G304: fixed, home-relative rc path
		path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "\n%s\n%s\n",
		rcMarker, line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// removeRCLine deletes every line equal to line (after trimming) from
// path, along with the two lines appendRCLine wrote above it: the
// marker comment, and the blank line above that. Taking the blank line
// too is what makes an install/uninstall cycle leave the file
// byte-identical rather than growing one blank line each time.
//
// Both are conditional on the marker being there. A user's own line
// sitting directly above a hand-written loader line is not install's
// marker and must not be swept up with it.
//
// A missing file, or a file without the line, is not an error.
func removeRCLine(path, line string) error {
	// A ~/.zshrc symlinked into a dotfiles checkout is common, and a
	// rename onto the link would leave a regular file where the link
	// was. Resolve first so the rewrite lands on the real file.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", target, err)
	}
	data, err := os.ReadFile(target) //nolint:gosec // G304: fixed, home-relative rc path
	if err != nil {
		return fmt.Errorf("read %s: %w", target, err)
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, l := range lines {
		if strings.TrimSpace(l) != line {
			kept = append(kept, l)
			continue
		}
		removed = true
		if n := len(kept); n > 0 &&
			strings.TrimSpace(kept[n-1]) == rcMarker {
			kept = kept[:n-1]
			if n := len(kept); n > 0 &&
				strings.TrimSpace(kept[n-1]) == "" {
				kept = kept[:n-1]
			}
		}
	}
	if !removed {
		return nil
	}
	return replaceFile(
		target, []byte(strings.Join(kept, "\n")), info.Mode())
}

// replaceFile writes data to a temp file in path's own directory and
// renames it over path, so a shell sourcing the rc file mid-uninstall
// reads the old bytes or the new, never a truncated file. mode is the
// original file's, carried over because the rename otherwise leaves
// whatever the temp file was created with.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".pgedge-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	//nolint:gosec // G302: the rc file's own mode is carried over deliberately
	if err := os.Chmod(tmp, mode.Perm()); err != nil {
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeScriptFile(
	cmd *cobra.Command, shell, dir, path string, force, isTTY bool,
) error {
	if _, err := os.Stat(path); err == nil && !force {
		if !isTTY {
			return &UsageError{Msg: fmt.Sprintf(
				"%s already exists; re-run with --force to overwrite",
				path)}
		}
		if !confirmPrompt(cmd,
			fmt.Sprintf("%s exists. Overwrite?", path), isTTY) {
			return &UsageError{Msg: "aborted: not confirmed"}
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create completion directory: %w", err)
	}
	var buf strings.Builder
	if err := writeCompletionScript(
		cmd.Root(), shell, &buf); err != nil {
		return fmt.Errorf("generate %s completion: %w", shell, err)
	}
	if err := os.WriteFile(
		path, []byte(buf.String()), 0o600); err != nil {
		return fmt.Errorf("write completion script: %w", err)
	}
	return nil
}

// confirmPrompt asks a yes/no question and reads the answer from the
// command's input. It returns false on anything but y/Y and false on
// a non-terminal (so piped/automated runs never block).
func confirmPrompt(cmd *cobra.Command, question string, isTTY bool) bool {
	if !isTTY {
		return false
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	reader := bufio.NewReader(cmd.InOrStdin())
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	return line == "y" || line == "Y"
}

// displayPath renders path relative to home with a ~ prefix when
// possible, for friendly output.
func displayPath(path, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil &&
		!strings.HasPrefix(rel, "..") {
		return "~/" + rel
	}
	return path
}
