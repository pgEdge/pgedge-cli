package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	pgedgecli "github.com/pgEdge/pgedge-cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
	"github.com/spf13/cobra"
)

// NewLLMSCmd builds the `pgedge llms` command.
//
// Bare, it prints the index: what is true across the whole CLI (global
// flags, profiles, environment variables, output formats and exit codes)
// plus routing tables naming every registered module and every top-level
// command group's page (`pgedge llms inspect`). `pgedge llms <module>`
// prints one module's reference, and `pgedge llms <module> <sub>` one
// sub-scope of it: `pgedge llms starfleet byoc` is byoc's document
// without the rest of starfleet.
//
// It prints no single document covering every module, so an agent
// needing one flag does not read every module's reference: the index
// names the one command to run next, and the common path is one small
// read plus one targeted one. Sizes are not stated here, because they
// go stale; pgedgecli.IndexSizeBudget bounds the index and
// TestIndexStaysUnderBudget enforces it. There is deliberately no --all,
// which would only rebuild that monolith.
func NewLLMSCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "llms [module|page] [sub-reference]",
		Short: "Print the AI-agent reference",
		Long: `llms prints the machine-readable reference that ships inside the
binary. AI agents should read it before composing commands instead of
improvising from --help.

With no argument it prints the index: global flags, configuration and
profiles, environment variables, exit codes, and tables routing you to
each module's own reference and each top-level command group's page.

With a module name it prints that module's reference — every command
the module owns, with usage, flags and worked examples.

Each module splits its reference further, one page per resource
and, for database, one per sub-resource, so an agent reads only the
page for the resource it is working on. The path is the command
path: the page for 'pgedge starfleet byoc database mcp' is
'pgedge llms starfleet byoc database mcp'. Every index page ends
with a routing table of the pages below it.

A top-level command group other than version and llms has a page of
its own: 'pgedge llms inspect'.

Example:
  pgedge llms
  pgedge llms inspect
  pgedge llms starfleet
  pgedge llms starfleet byoc
  pgedge llms starfleet byoc database mcp`,
		Annotations: map[string]string{
			AnnotationProfileExempt: "reads only the embedded reference docs",
		},
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeReferencePath,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if len(args) == 0 {
				fmt.Fprint(out, string(pgedgecli.LLMS))
				return nil
			}

			name := args[0]
			for _, m := range module.Registered() {
				if m.Name() != name {
					continue
				}
				return printReferencePage(out, m, args)
			}

			pages, err := pgedgecli.Pages()
			if err != nil {
				return err
			}
			if d, ok := reference.Find(pages, strings.Join(args, " ")); ok {
				fmt.Fprint(out, string(d.Body))
				return nil
			}
			return &UsageError{Msg: fmt.Sprintf(
				"unknown module or page %q — this binary carries the "+
					"modules %s and the pages %s", strings.Join(args, " "),
				strings.Join(referenceModules(), ", "),
				strings.Join(rootPageNames(pages), ", "))}
		},
	}
}

// printReferencePage prints the page at `pgedge llms <args>`. The
// module's own index is args[0] alone; anything longer must name a
// page the module ships, and a miss is a usage error (exit 2) that
// lists the pages one level below the nearest page that does exist,
// rather than a silent fall back to a bigger document — which would
// quietly hand back the 150 KB the split exists to avoid.
func printReferencePage(out io.Writer, m module.Module,
	args []string) error {

	scope := strings.Join(args, " ")
	docs := documentsOf(m)

	if len(args) == 1 {
		ref := m.Reference()
		if len(ref) == 0 {
			return fmt.Errorf(
				"module %q ships no reference document", m.Name())
		}
		fmt.Fprint(out, string(ref))
		return nil
	}

	if d, ok := reference.Find(docs, scope); ok {
		if len(d.Body) == 0 {
			return fmt.Errorf("`pgedge llms %s` is an empty page", scope)
		}
		fmt.Fprint(out, string(d.Body))
		return nil
	}

	nearest, ok := reference.LongestPrefix(docs, scope)
	if !ok {
		return &UsageError{Msg: fmt.Sprintf(
			"module %q has no pages below `pgedge llms %s`",
			m.Name(), m.Name())}
	}
	kids := reference.Children(docs, nearest.Scope)
	if len(kids) == 0 {
		return &UsageError{Msg: fmt.Sprintf(
			"no page at `pgedge llms %s` — `pgedge llms %s` is the "+
				"nearest and has none below it", scope, nearest.Scope)}
	}
	names := make([]string, 0, len(kids))
	for _, k := range kids {
		names = append(names, lastWord(k.Scope))
	}
	return &UsageError{Msg: fmt.Sprintf(
		"no page at `pgedge llms %s` — below `pgedge llms %s` it "+
			"carries: %s", scope, nearest.Scope,
		strings.Join(names, ", "))}
}

// documentsOf returns a module's pages, or just its index when it
// ships no others.
func documentsOf(m module.Module) []module.Document {
	if d, ok := m.(module.Documented); ok {
		return d.Documents()
	}
	return []module.Document{{Scope: m.Name(), Body: m.Reference()}}
}

// rootPageNames returns the scopes of the root's own pages, which are
// the other names `pgedge llms <name>` accepts.
func rootPageNames(pages []module.Document) []string {
	names := make([]string, 0, len(pages))
	for _, d := range pages {
		names = append(names, d.Scope)
	}
	return names
}

func lastWord(scope string) string {
	if i := strings.LastIndex(scope, " "); i >= 0 {
		return scope[i+1:]
	}
	return scope
}

// referenceModules returns the sorted names of registered modules that
// ship a reference, which is exactly the set `pgedge llms <module>`
// accepts.
func referenceModules() []string {
	var names []string
	for _, m := range module.Registered() {
		if m.Describe().ProvidesLLMS {
			names = append(names, m.Name())
		}
	}
	sort.Strings(names)
	return names
}

// completeReferencePath completes module names at the first position
// and, after that, the pages one level below the path typed so far. A
// path with nothing below it completes to nothing rather than to the
// module list again, which would offer names never valid in that slot.
func completeReferencePath(_ *cobra.Command, args []string,
	_ string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		names := referenceModules()
		if pages, err := pgedgecli.Pages(); err == nil {
			names = append(names, rootPageNames(pages)...)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, m := range module.Registered() {
		if m.Name() != args[0] {
			continue
		}
		scope := strings.Join(args, " ")
		for _, k := range reference.Children(documentsOf(m), scope) {
			names = append(names, lastWord(k.Scope))
		}
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}
