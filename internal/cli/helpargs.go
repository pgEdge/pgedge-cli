package cli

import "github.com/spf13/cobra"

// StrayArgsOnHelp asks a command's own Args validator the question
// cobra skipped: with --help present, does this command carry a
// positional it cannot accept, a word meant to name a subcommand that
// does not?
//
// cobra's execute() returns flag.ErrHelp on the help flag
// (command.go:935 in the pinned v1.10.2) before reaching ValidateArgs
// at 968. Every group command pairs Args: cobra.NoArgs with RunE so
// `pgedge controlplane backup` is a usage error, and appending --help
// printed help and exited 0 instead, at every depth below the root. The
// root is safe because cobra's Find rejects an unresolvable first word
// before execute() runs, and legacyArgs applies that rule only to a
// command with no parent.
//
// Asking the command's own validator, rather than re-deriving whether
// the word names a subcommand, keeps one definition of a stray argument
// for the help path and the ordinary path. It also accepts the one
// hybrid, `controlplane database restore <database_id>`, a group
// command with a required positional; hardcoding cobra.NoArgs would
// reject it.
//
// The two gates below narrow this to an unknown subcommand, and each
// decides a different shape, so do not collapse them:
//
//   - len(positional) == 0 keeps `<leaf> --help` working when the leaf
//     declares a required argument. `pgedge starfleet tenant get --help`
//     is how an operator learns get needs an ID, and asking ExactArgs(1)
//     about zero positionals answers "accepts 1 arg(s), received 0".
//     Nothing else prevents that.
//
//   - !HasSubCommands keeps a childless command's surplus positionals
//     out of scope: `starfleet tenant get id1 id2 --help`, `profile use
//     a b --help` and `completion bash extra --help` exit 0, and 2
//     without this gate. A positional given to a command with
//     subcommands is a path element that failed to resolve; one given to
//     a childless command is an argument, not part of the path a help
//     request names. The "childless command, help flag, extra argument"
//     test row pins it, and needs two positionals to do so.
//
// cmd.Flags().Args() is exactly what execute() would have validated,
// since ParseFlags ran before ErrHelp was returned. Deriving the
// positionals again would mean copying cobra's unexported stripFlags,
// and re-parsing would double-append every slice flag, such as
// controlplane's stringArray --base-url.
//
// The help-flag check matters because two routes through execute()
// reach HelpFunc with a nil error, the help flag (935) and a
// non-runnable command (956), and only the first is ours;
// internal/clitest's pure-router gate already forbids a non-runnable
// group. Where ValidateArgs did run, the validator has already agreed,
// so re-asking could only disagree by accident, and would for a
// DisableFlagParsing command, whose raw args Flags().Args() never
// holds. Nothing sets that field today.
//
// A nil command returns nil: main.run passes whatever ExecuteC returned,
// which is non-nil on the paths that matter but not by its signature.
func StrayArgsOnHelp(cmd *cobra.Command) error {
	if cmd == nil {
		return nil
	}
	// An error here means the command has no help flag at all, which is
	// reachable only before InitDefaultHelpFlag has run — help was not
	// asked for, so there is nothing to report.
	helpRequested, err := cmd.Flags().GetBool("help")
	if err != nil || !helpRequested {
		return nil
	}
	positional := cmd.Flags().Args()
	if len(positional) == 0 || !cmd.HasSubCommands() {
		return nil
	}
	return cmd.ValidateArgs(positional)
}
