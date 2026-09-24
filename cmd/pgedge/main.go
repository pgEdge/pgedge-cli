package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet"
	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run())
}

// newKeychain opens the OS credential store. The subprocess tests
// replace it: they run run() for real under a temp HOME, where macOS's
// security tool finds no login keychain and opens a dialog.
var newKeychain = keychain.OS

func run() int {
	removeStaleSwapBackup()

	rt := &module.Runtime{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,

		Keychain: newKeychain(),
	}

	module.Register(&starfleet.Module{})
	module.Register(&controlplane.Module{})

	// Root's PersistentPreRunE fills the rest of rt from the parsed
	// flags once Execute reaches it, so nothing built here may read
	// those fields before then.
	root := cli.NewRootCmd(rt)
	cmds, err := module.BuildCommands(rt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return cli.ExitError
	}
	root.AddCommand(cmds...)

	// After every module has registered, because a command added later
	// is not walked. clitest.FullTree mirrors this call; only the
	// subprocess row in TestUnknownSubcommandSuggestsANearMiss proves
	// the binary makes it.
	cli.AddUnknownCommandSuggestions(root)

	// wrapProfileGuard MUST run before markRan, so markRan's wrapper is
	// outermost and ranRunE is already true when the guard rejects a
	// --profile; otherwise the promotion below turns that exit 1 into a
	// usage error, exit 2.
	//
	// cobra adds __complete lazily inside Execute, after this walk, so
	// completion of a half-typed "--profile pr…" cannot hard-fail
	// (TestCompleteIgnoresUnknownProfile).
	wrapProfileGuard(root, rt)

	// ranRunE flips true once cobra enters a command's run hook, so an
	// error while it is false came from parsing or validation — the
	// user mistyped the command — and exits 2, not 1.
	//
	// It is set inside the run hook, not in a PersistentPreRun, because
	// cobra's execute() runs ValidateArgs, then the pre-run hooks, then
	// ValidateRequiredFlags: a pre-run sentinel would split a missing
	// argument (exit 2) from a missing required flag (exit 1).
	ranRunE := false
	markRan(root, &ranRunE)

	executed, err := root.ExecuteC()

	// cobra returns nil on --help before validating arguments, so a
	// mistyped subcommand would exit 0 with its parent's help.
	// ExecuteC's first return is the command cobra resolved, whose Args
	// validator cli.StrayArgsOnHelp asks. root.HelpFunc already
	// suppressed the help dump; the diagnostic and exit code are here
	// because a HelpFunc has no channel for either.
	//
	// Tagged UsageError because the ranRunE promotion fires only for
	// ExitError, and a stray argument is a usage error either way.
	if err == nil {
		if serr := cli.StrayArgsOnHelp(executed); serr != nil {
			err = &cli.UsageError{Msg: serr.Error()}
		}
	}

	// Every dry run reports, not only one that intercepted a write, so
	// a run that stopped at a failed check, or a verb that sends
	// nothing, still says how far it got.
	//
	// An interception is the outcome whatever error came back, so it
	// exits 0; the Run is asked rather than errors.As, which the dryrun
	// package doc explains. With nothing intercepted the command's own
	// error decides the exit code, exactly as a real run would, and the
	// partial report goes to stderr so it never reads as a result.
	if rt.DryRun != nil {
		if rt.DryRun.Intercepted() {
			if rerr := cli.RenderDryRun(rt); rerr != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", rerr)
				return cli.ExitError
			}
			return cli.ExitOK
		}
		cli.RenderDryRunPartial(rt)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		code := cli.ExitCode(err)
		// A cli.SetupError (a bad --config file, say) also arrives with
		// ranRunE false, but it is not a parse error, so it keeps exit 1.
		var se *cli.SetupError
		if !ranRunE && code == cli.ExitError && !errors.As(err, &se) {
			code = cli.ExitUsage
		}
		return code
	}
	return cli.ExitOK
}

// removeStaleSwapBackup deletes the ".old" file the Windows self-update
// swap (internal/selfupdate/swap_windows.go) leaves beside the binary,
// which nothing holds open once a new process starts. Windows only:
// elsewhere a "<binary>.old" is the user's own file. Best-effort and
// silent, since it is never worth failing a command over.
func removeStaleSwapBackup() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

// markRan wraps every run hook in c's tree so that entering one sets
// *ran; the call site says why.
//
// Nothing in the tree uses bare Run today. Its arm is there because a
// Run command would otherwise never set the sentinel, and an error from
// its post-run hooks would be promoted to a usage error.
//
// cobra's `help` command is added during Execute and is not wrapped;
// its hook returns no error for the promotion to act on.
func markRan(c *cobra.Command, ran *bool) {
	if orig := c.RunE; orig != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			*ran = true
			return orig(cmd, args)
		}
	}
	if orig := c.Run; orig != nil {
		c.Run = func(cmd *cobra.Command, args []string) {
			*ran = true
			orig(cmd, args)
		}
	}
	for _, child := range c.Commands() {
		markRan(child, ran)
	}
}

// wrapProfileGuard wraps every run hook in c's tree so that entering
// one first runs cli.GuardProfile. It must be called before markRan;
// the call site says why. It wraps Run too, so a command written with
// it cannot skip the guard.
func wrapProfileGuard(c *cobra.Command, rt *module.Runtime) {
	if orig := c.RunE; orig != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if err := cli.GuardProfile(cmd, rt); err != nil {
				return err
			}
			return orig(cmd, args)
		}
	}
	if orig := c.Run; orig != nil {
		c.Run = func(cmd *cobra.Command, args []string) {
			if err := cli.GuardProfile(cmd, rt); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				return
			}
			orig(cmd, args)
		}
	}
	for _, child := range c.Commands() {
		wrapProfileGuard(child, rt)
	}
}
