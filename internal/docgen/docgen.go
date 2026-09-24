// Package docgen renders the command reference, the llms pages and
// the docs/reference pages, from the live cobra tree.
//
// Every part of a command's reference entry is already in cobra, and
// the hand-kept copy drifted: three anchor forms, two flag layouts, 28
// leaf/flag pairs documented only in shared prose, and 62 commands
// with no section. Generating makes the gate "regenerate and diff",
// which a merely plausible doc cannot pass.
//
// Only the text between the markers is generated (one row cut here):
//
//	<!-- BEGIN GENERATED: pgedge starfleet byoc cluster list -->
//	##### pgedge starfleet byoc cluster list
//
//	**Usage:** `pgedge starfleet byoc cluster list [flags]`
//
//	List clusters
//
//	**Flags:**
//
//	| Flag | Required | Default | Description |
//	|------|----------|---------|-------------|
//	| `--offset int` | No |  | Offset into the results for pagination |
//	<!-- END GENERATED -->
//
// Everything outside the markers is hand-written and never touched:
// the generator owns what drifts silently, the author owns the prose.
//
// The description is cobra's Short, not Long: Long carries worked
// examples that would duplicate the hand-written ones, and using Short
// means --help and the reference cannot disagree about a command.
package docgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The opening marker names the command path so a block is found by
// path, not position, and a diff shows which command a table belongs
// to.
const (
	beginPrefix  = "<!-- BEGIN GENERATED: "
	markerSuffix = " -->"
	endMarker    = "<!-- END GENERATED -->"
)

// BeginMarker returns the opening marker for a command path.
func BeginMarker(path string) string {
	return beginPrefix + path + markerSuffix
}

// EndMarker returns the closing marker.
func EndMarker() string { return endMarker }

// headingLevel maps a command's depth to a markdown heading level:
// `pgedge` is 1, `pgedge starfleet` is 2, clamped at 6 because
// markdown has no h7.
func headingLevel(path string) int {
	depth := len(strings.Fields(path))
	if depth > 6 {
		depth = 6
	}
	return depth
}

// flagCell renders the Flag column as command-line syntax, so a bool
// prints bare: `--force bool` would be wrong to type.
//
// The type comes from pflag.UnquoteUsage, as in --help, so the two
// cannot disagree: Value.Type() raw said `stringSlice` where --help
// says `strings`, on 15 flags. TestCommandTreeConformance refuses
// backticks in usage strings, so UnquoteUsage always returns the type,
// or "" for a bool.
//
// An optional value renders as --help spells it, `string[="checks"]`.
// A bare `string` would tell an agent `--dry-run` needs a value, when
// `--dry-run` alone is the documented form.
func flagCell(f *pflag.Flag) string {
	var b strings.Builder
	b.WriteString("`")
	if f.Shorthand != "" {
		fmt.Fprintf(&b, "-%s, ", f.Shorthand)
	}
	b.WriteString("--" + f.Name)
	if name, _ := pflag.UnquoteUsage(f); name != "" {
		b.WriteString(" " + name)
		if f.NoOptDefVal != "" {
			// Mirror pflag's FlagUsagesWrapped (v1.0.10): only a
			// string's bracket is quoted, the value raw rather than
			// %q-escaped, and count hides its implicit +1. bool and
			// boolfunc never reach here: UnquoteUsage returned "".
			switch f.Value.Type() {
			case "string":
				fmt.Fprintf(&b, "[=\"%s\"]", f.NoOptDefVal)
			case "count":
				if f.NoOptDefVal != "+1" {
					fmt.Fprintf(&b, "[=%s]", f.NoOptDefVal)
				}
			default:
				fmt.Fprintf(&b, "[=%s]", f.NoOptDefVal)
			}
		}
	}
	b.WriteString("`")
	return b.String()
}

// requiredCell reports whether cobra will refuse to run without this
// flag, read from the annotation MarkFlagRequired sets.
//
// controlplane marks nothing required and validates at runtime, so
// every controlplane row reads No. That is not a bug here.
func requiredCell(f *pflag.Flag) string {
	if vals, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok {
		for _, v := range vals {
			if v == "true" {
				return "Yes"
			}
		}
	}
	return "No"
}

// defaultCell renders the value a flag takes when not passed, blank
// for a zero value: the rule --help uses for "(default ...)", so a
// blank cell always means the type's zero value.
func defaultCell(f *pflag.Flag) string {
	if defaultIsZero(f) {
		return ""
	}
	return "`" + escapePipes(f.DefValue) + "`"
}

// defaultIsZero mirrors pflag's unexported defaultIsZeroValue (v1.0.10)
// for the types this tree declares: string, bool, int, duration,
// stringSlice and stringArray. A type pflag treats differently (its
// stringToString default "[]" is not zero there) has no flag here.
func defaultIsZero(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "bool":
		return f.DefValue == "false"
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8",
		"uint16", "uint32", "uint64", "count", "float32", "float64":
		return f.DefValue == "0"
	case "string":
		return f.DefValue == ""
	case "ip", "ipMask", "ipNet":
		return f.DefValue == "<nil>"
	case "intSlice", "stringSlice", "stringArray":
		return f.DefValue == "[]"
	default:
		switch f.DefValue {
		case "false", "<nil>", "", "0":
			return true
		}
		return false
	}
}

// escapePipes keeps a description containing a pipe from breaking the
// markdown table it sits in.
func escapePipes(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "|", `\|`)
}

// declaredFlags returns the flags a command declares, local and
// persistent, sorted by name. Inherited flags are documented on the
// ancestor that declares them: the six global flags on every leaf
// table would bury the ones a reader is looking for.
func declaredFlags(cmd *cobra.Command) []*pflag.Flag {
	seen := make(map[string]*pflag.Flag)
	collect := func(f *pflag.Flag) { seen[f.Name] = f }
	cmd.Flags().VisitAll(collect)
	cmd.PersistentFlags().VisitAll(collect)

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]*pflag.Flag, 0, len(names))
	for _, name := range names {
		out = append(out, seen[name])
	}
	return out
}

// usageLine renders the backticked command line: the path from
// CommandPath, the positional placeholders from Use ("get
// <cluster_id>").
func usageLine(cmd *cobra.Command) string {
	path := cmd.CommandPath()

	args := strings.Fields(cmd.Use)
	if len(args) > 1 {
		path += " " + strings.Join(args[1:], " ")
	}
	if cmd.HasAvailableSubCommands() {
		path += " <command>"
	}
	if declaredFlags(cmd) != nil && len(declaredFlags(cmd)) > 0 {
		path += " [flags]"
	}
	return "**Usage:** `" + path + "`"
}

// Block renders one command's generated section, markers included.
func Block(cmd *cobra.Command) string {
	path := cmd.CommandPath()

	var b strings.Builder
	b.WriteString(BeginMarker(path) + "\n")
	b.WriteString(blockBody(cmd, headingLevel(path)))
	b.WriteString(EndMarker())
	return b.String()
}

// blockBody renders a command's section without markers, with its
// heading at the given level. Block uses the command's tree depth;
// Page re-levels so a page's shallowest command sits under the page's
// own hand-written title.
func blockBody(cmd *cobra.Command, level int) string {
	return renderBody(cmd, level, false)
}

// renderBody renders a command's section. withExample appends the
// Example section from Long after the flags table.
//
// It is true for the reference pages and false for the llms blocks.
// The llms pages carry hand-written examples around their blocks, 150
// across the four module references, so emitting these would print
// each twice. A reference page is one generated region with nowhere
// to put an example, which is why every leaf reached the 2026-08-28
// docs review with none on the page and one in --help.
func renderBody(cmd *cobra.Command, level int, withExample bool) string {
	path := cmd.CommandPath()

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n",
		strings.Repeat("#", level), path)
	b.WriteString(usageLine(cmd) + "\n\n")

	if short := strings.TrimSpace(cmd.Short); short != "" {
		b.WriteString(short + "\n\n")
	}

	flags := declaredFlags(cmd)
	if len(flags) > 0 {
		b.WriteString("**Flags:**\n\n")
		b.WriteString("| Flag | Required | Default | Description |\n")
		b.WriteString("|------|----------|---------|-------------|\n")
		for _, f := range flags {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				flagCell(f), requiredCell(f), defaultCell(f),
				escapePipes(f.Usage))
		}
		b.WriteString("\n")
	}

	if withExample {
		if ex := ExampleSection(cmd.Long); ex != "" {
			b.WriteString("**Example:**\n\n")
			b.WriteString("```\n" + ex + "\n```\n\n")
		}
	}

	return b.String()
}

// ExampleSection returns the commands under a Long string's trailing
// "Example:" heading, dedented, or "" when there is none.
//
// It reads Long, not cobra's Example field, which this tree never
// populates: every example sits in Long under this heading, which
// internal/clitest's example gate depends on too. A silent "" is
// caught by TestEveryLeafCarriesAnExtractableExample.
//
// The section runs to the end of Long: measured 2026-08-29, no Long
// carries unindented text after its Example heading.
func ExampleSection(long string) string {
	const heading = "Example:"

	lines := strings.Split(long, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == heading {
			start = i + 1
		}
	}
	if start < 0 {
		return ""
	}

	body := lines[start:]
	// Dedent by the shallowest indent present rather than a fixed two
	// spaces, so a continuation line keeps its deeper indent and the
	// block still starts at column zero.
	indent := -1
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent < 0 {
		return ""
	}

	out := make([]string, 0, len(body))
	for _, l := range body {
		if len(l) >= indent {
			l = l[indent:]
		}
		out = append(out, strings.TrimRight(l, " \t"))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// VisibleCommands returns every command the reference must document,
// in stable path order: the root, every group and every leaf, minus
// hidden commands and cobra's generated help.
func VisibleCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden || cmd.Name() == "help" {
			return
		}
		out = append(out, cmd)
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CommandPath() < out[j].CommandPath()
	})
	return out
}
