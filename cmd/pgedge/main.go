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

	// cli.NewRootCmd wires a PersistentPreRunE on root that populates
	// the rest of rt (Config, Profile, Output, Verbose, Debug) from
	// cobra's own parsed flags once Execute reaches it (#120). Nothing
	// below this point may assume rt is fully populated before
	// Execute runs; every command tree built here is wired for that,
	// same as internal/clitest.FullTree() proves for the gates.
	root := cli.NewRootCmd(rt)
	cmds, err := module.BuildCommands(rt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return cli.ExitError
	}
	root.AddCommand(cmds...)

	// ranRunE flips true only once cobra has entered a command's own run
	// hook. A non-nil error while it is still false therefore came from
	// cobra's parse/validation phase — an unknown command or flag, a bad
	// argument count, a missing required flag — which is the user
	// mistyping the command, so exit 2 and not 1.
	//
	// Wrapping the run hooks is what makes that uniform, and it replaced
	// a root PersistentPreRun that set the same sentinel. cobra validates
	// arguments BEFORE its pre-run hooks and required flags AFTER them —
	// command.go's execute() runs ValidateArgs, then PersistentPreRun,
	// then ValidateRequiredFlags, then RunE. A sentinel set in the
	// pre-run hook is therefore already true by the time a required flag
	// is checked, so `starfleet tenant get` (missing argument) exited 2
	// while `starfleet client create` (missing required flag) exited 1 —
	// the same class of mistake, split across two codes, at 31
	// MarkFlagRequired sites. Setting it inside the run hook puts both
	// validations on the same side of the line.
	// wrapProfileGuard MUST run before markRan: markRan's wrapper has
	// to be the OUTER one so ranRunE is already true by the time the
	// guard's wrapper returns an error. If the order were reversed,
	// a rejected --profile would still be reported before ranRunE
	// flips, and the exit-1-to-2 promotion just below would turn a
	// parity-with-`profile use` error (exit 1) into a usage error
	// (exit 2) — exactly the outcome cli.CheckProfile exists to avoid.
	//
	// cobra's __complete command is added lazily inside Execute,
	// after this walk runs, so it is never wrapped: shell completion
	// for a half-typed "--profile pr…" cannot hard-fail. See
	// TestCompleteIgnoresUnknownProfile.
	// After every module has registered, because a command added later
	// is not walked. clitest.FullTree mirrors this call so the build
	// gates walk the same shape the binary does; the subprocess row in
	// TestUnknownSubcommandSuggestsANearMiss is what proves this line
	// is here, since a unit test over FullTree could not tell.
	cli.AddUnknownCommandSuggestions(root)

	wrapProfileGuard(root, rt)
	ranRunE := false
	markRan(root, &ranRunE)

	executed, err := root.ExecuteC()

	// cobra prints help and returns nil whenever it sees --help, before
	// it ever validates arguments, so a mistyped subcommand exited 0
	// with the parent's help at every depth below the root (#241).
	// ExecuteC is called for its first return value alone: it is the
	// command cobra actually resolved, and hence the one whose Args
	// validator cli.StrayArgsOnHelp has to ask. root.HelpFunc already
	// swallowed the help dump for these; the diagnostic and the exit
	// code are here because a HelpFunc has no channel for either.
	//
	// Tagged UsageError rather than left to the ranRunE promotion
	// below: the promotion fires only for ExitError, and a stray
	// argument is a usage error on its own terms whether or not a run
	// hook happened to have been entered.
	if err == nil {
		if serr := cli.StrayArgsOnHelp(executed); serr != nil {
			err = &cli.UsageError{Msg: serr.Error()}
		}
	}

	// EVERY dry run reports, not only one that intercepted something.
	//
	// Gating on Intercepted() alone made two documented behaviours
	// unreachable: a run that stopped at a failed check said nothing
	// about how far it got, and a verb that turns out to send nothing
	// printed nothing at all — writeDryRunText's "nothing would be
	// sent" branch existed but production could never reach it.
	//
	// A recorded interception IS the outcome, whatever error came back:
	// the transport stopped the write on purpose, and the error only
	// unwound the stack, so that case exits 0. With nothing intercepted
	// the command's own error still decides the exit code — a dry run
	// whose checks failed must exit exactly what the real run would —
	// and the partial report goes to STDERR, so it can never be mistaken
	// for a successful result on stdout.
	//
	// The interception cannot be recognised with errors.As.
	// internal/controlplane/cmd.networkError formats its cause with %v into an
	// ExitError that carries no Unwrap, so by the time any cp write site
	// returns, the chain is gone. Asking the Run instead means no write
	// site has to be sentinel-transparent, and none has to stay that way
	// as the tree grows.
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
		// A setup failure (cli.SetupError, from root's PersistentPreRunE
		// building the Runtime — a bad --config file, say) also reaches
		// this branch with ranRunE still false, but it is a runtime
		// failure and not a cobra parse error, so it must not be
		// promoted to exit 2 the way an actual parse error is.
		var se *cli.SetupError
		if !ranRunE && code == cli.ExitError && !errors.As(err, &se) {
			// Pre-run cobra error not already tagged as a usage error.
			code = cli.ExitUsage
		}
		return code
	}
	return cli.ExitOK
}

// removeStaleSwapBackup deletes the ".old" file a Windows self-update
// swap leaves beside the running binary (internal/selfupdate's
// swap_windows.go renames the previous binary aside rather than over
// itself, since Windows will not let a running executable be
// replaced directly). By the time this process starts, nothing still
// has that file open.
//
// Windows only, because only the Windows swap ever creates one:
// elsewhere a "<binary>.old" beside the executable is a file the USER
// put there, and deleting someone's own backup is not this command's
// business. Best-effort and silent otherwise: an os.Executable
// failure or an undeletable file is never worth failing the whole
// command over.
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

// markRan wraps every run hook in c's tree so that entering one records
// the fact through ran. See the note at the call site for why this is a
// wrap rather than a PersistentPreRun hook.
//
// It wraps Run as well as RunE. Nothing in the shipped tree uses bare
// Run, so that arm does not fire today — but a command written with Run
// would otherwise never set the sentinel, and every runtime error it
// returned would be reported as a usage error. Same reasoning
// testsupport.RunBareCapture uses for handling a hook nothing currently
// sets.
//
// cobra's built-in `help` command is added lazily during Execute and so
// is not wrapped. That is not a gap this creates: `help` returns nil, so
// there is no error for the promotion to act on either way.
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
// one first runs cli.GuardProfile. It must be called before markRan —
// see the comment at the call site — so markRan's wrapper ends up
// outermost and a rejected --profile still reports exit 1 rather than
// being promoted to exit 2.
//
// It wraps Run as well as RunE, mirroring markRan, for the same
// reason: nothing in the shipped tree uses bare Run today, but a
// command written with it must not silently skip the guard.
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
