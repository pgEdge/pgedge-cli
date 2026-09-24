package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newDatabaseRestoreTemplateCmd builds `pgedge controlplane database
// restore template`.
func newDatabaseRestoreTemplateCmd(rt *module.Runtime) *cobra.Command {
	var interactive bool
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Generate a starter restore spec",
		Long: `template prints a commented starter restore spec to stdout.
Redirect it to a file, edit it, then apply with
'database restore <id> -f'. Use --interactive to be interviewed for the
values instead of emitting a blank template.

Example:
  pgedge controlplane database restore template > restore.yaml
  pgedge controlplane database restore template -i > restore.yaml
  pgedge controlplane database restore template -i | pgedge controlplane database restore db -f -`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if interactive {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return &ExitError{
						msg:  "interactive mode requires a terminal",
						code: ExitUsage,
					}
				}
				spec, err := runRestoreInterview(rt.Stdin, rt.Stderr)
				if err != nil {
					return err
				}
				fmt.Fprint(rt.Stdout, spec)
				fmt.Fprintf(rt.Stderr,
					"\nWrote restore spec. Restore the database with:\n"+
						"  pgedge controlplane database restore <id> -f <file>\n")
				return nil
			}
			fmt.Fprint(rt.Stdout, buildRestoreTemplate())
			return nil
		},
	}
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false,
		"Interview for restore values instead of a blank template")
	return cmd
}

// runRestoreInterview collects no secret: interviewRestoreConfig never
// asks for repository credentials.
func runRestoreInterview(in io.Reader, errOut io.Writer) (string, error) {
	r := bufio.NewReader(in)
	rv, err := interviewRestoreConfig(r, errOut)
	if err != nil {
		return "", err
	}
	nodes, err := promptString(r, errOut,
		"Nodes to restore, comma-separated (blank = all)", "")
	if err != nil {
		return "", err
	}
	return buildRestoreSpec(rv, splitCSV(nodes)), nil
}
