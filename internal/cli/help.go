package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NewHelpCmd builds the `pgedge help` command, replacing cobra's
// built-in one through the SetHelpCommand extension point.
//
// The built-in cannot report a failure, for two structural reasons:
//
//   - It is declared with Run, not RunE (command.go's
//     InitDefaultHelpCmd), so `pgedge help zzz` prints "Unknown help
//     topic" and exits 0, a diagnostic with a success code.
//   - It discards Find's second return, the args Find could not
//     consume, so `pgedge help starfleet zzz` drops `zzz` and prints
//     starfleet's help with no diagnostic at all.
//
// Both are usage errors (exit 2) here, as a malformed command is
// everywhere. A path that resolves renders the help cobra would have,
// via the built-in's three calls; that branch is the only part copied
// from cobra and the only part that can drift against it.
func NewHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Show help for any command",
		Long: `help prints the help for any command in the tree.

Give it a command path to describe, or run it bare for the top-level
command list. A path that does not resolve to a real command is a
usage error (exit 2) rather than a silent fall back to the root help,
so a typo cannot look like a successful lookup.

Example:
  pgedge help
  pgedge help starfleet auth login`,
		Annotations: map[string]string{
			AnnotationProfileExempt: "must match --help, which cobra " +
				"short-circuits before any hook",
		},
		ValidArgsFunction: helpTopicCompletions,
		RunE: func(c *cobra.Command, args []string) error {
			// rest is what Find could not consume. err alone is not
			// enough: Find returns one only when it lands on the root
			// with arguments left over.
			target, rest, err := c.Root().Find(args)
			if err != nil || target == nil || len(rest) > 0 {
				return &UsageError{Msg: fmt.Sprintf(
					"unknown help topic %q — run 'pgedge help' for the "+
						"command list", strings.Join(args, " "))}
			}

			// The built-in's rendering branch: flow the context down so
			// help text can read it (the built-in does so only when the
			// target has none), and initialise the two auto-flags so
			// they appear in the rendering.
			target.SetContext(c.Context())
			target.InitDefaultHelpFlag()
			target.InitDefaultVersionFlag()
			return target.Help()
		},
	}
}

// helpTopicCompletions completes command paths for `pgedge help <TAB>`
// as cobra's built-in does, so replacing that command loses nothing.
func helpTopicCompletions(c *cobra.Command, args []string, toComplete string) (
	[]cobra.Completion, cobra.ShellCompDirective,
) {
	target, _, err := c.Root().Find(args)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if target == nil {
		target = c.Root()
	}
	var out []cobra.Completion
	for _, sub := range target.Commands() {
		if !sub.IsAvailableCommand() && sub.Name() != "help" {
			continue
		}
		if strings.HasPrefix(sub.Name(), toComplete) {
			out = append(out,
				cobra.CompletionWithDesc(sub.Name(), sub.Short))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
