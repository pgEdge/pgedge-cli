package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/spf13/cobra"
)

// NoUsageHint marks an exit-2 error caused by state rather than by how
// the command was typed: a profile, a file's contents, a prompt answer.
// A usage hint under it would say the command line was wrong. The exit
// code and message stay the wrapped error's.
func NoUsageHint(err error) error {
	if err == nil {
		return nil
	}
	return &noHintError{err}
}

type noHintError struct{ error }

func (e *noHintError) Unwrap() error { return e.error }

// HintSuppressed reports whether err was marked by NoUsageHint.
func HintSuppressed(err error) bool {
	var n *noHintError
	return errors.As(err, &n)
}

// UsageHint returns the lines printed under a usage error: the
// command's usage line, then its first example of itself or, for a
// group, the subcommands it takes. Each costs an agent a --help call
// otherwise. It returns "" for a nil command.
func UsageHint(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	var subs []string
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			subs = append(subs, c.Name())
		}
	}
	var b strings.Builder
	// A group's UseLine is "<path> [flags]", which hides that it wants
	// a subcommand. A group that also takes a positional declares it in
	// Use, and then UseLine is the accurate form.
	if len(subs) > 0 && !strings.Contains(cmd.Use, " ") {
		fmt.Fprintf(&b, "Usage: %s <command>\n", cmd.CommandPath())
	} else {
		fmt.Fprintf(&b, "Usage: %s\n", cmd.UseLine())
	}
	if len(subs) > 0 {
		fmt.Fprintf(&b, "Commands: %s\n", strings.Join(subs, ", "))
	} else if ex := firstExample(cmd); ex != "" {
		fmt.Fprintf(&b, "Example: %s\n", ex)
	}
	return b.String()
}

// firstExample returns the first example in cmd's Long that invokes
// cmd itself, continuation lines joined, or "". An Example section can
// open with a prerequisite command, which would mislead here.
func firstExample(cmd *cobra.Command) string {
	path := cmd.CommandPath()
	var cur []string
	for _, line := range strings.Split(docgen.ExampleSection(cmd.Long), "\n") {
		line = strings.TrimSpace(line)
		if len(cur) == 0 && line != path &&
			!strings.HasPrefix(line, path+" ") {
			continue
		}
		cont := strings.HasSuffix(line, `\`)
		cur = append(cur, strings.TrimSpace(strings.TrimSuffix(line, `\`)))
		if !cont {
			break
		}
	}
	return strings.Join(cur, " ")
}
