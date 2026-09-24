package cli

import (
	"bytes"
	"strings"
	"testing"

	pgedgecli "github.com/pgEdge/pgedge-cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// refModule is a registered module that ships a reference, so these
// tests can exercise `pgedge llms <module>` without importing the real
// modules (which would be an import cycle: they import internal/cli's
// siblings, and internal/cli must stay importable by all of them).
type refModule struct {
	name string
	ref  []byte
}

func (m refModule) Name() string  { return m.name }
func (m refModule) Short() string { return "fake " + m.name }
func (m refModule) Command(*module.Runtime) (*cobra.Command, error) {
	return &cobra.Command{Use: m.name}, nil
}
func (m refModule) Describe() module.ModuleInfo {
	return module.ModuleInfo{
		Name: m.name, Short: m.Short(), ProvidesLLMS: len(m.ref) > 0,
	}
}
func (m refModule) Reference() []byte { return m.ref }

// docModule is a refModule that ships several pages, so these tests
// can exercise `pgedge llms <module> <page...>` without importing the
// real starfleet module.
type docModule struct {
	refModule
	docs []module.Document
}

func (m docModule) Documents() []module.Document {
	return append([]module.Document{{Scope: m.name, Body: m.ref}},
		m.docs...)
}

func runLLMS(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewLLMSCmd()
	// Run standalone, llms is a root, and cobra would add its default
	// completion command and shadow the root page of that name.
	cmd.CompletionOptions.DisableDefaultCmd = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// TestLLMSPrintsIndexByDefault pins the routing behaviour: bare `llms`
// prints the index, not every module's reference concatenated. The
// index is ~16 KB against the old monolith's 163 KB, and that
// reduction is the entire reason the reference was split.
func TestLLMSPrintsIndexByDefault(t *testing.T) {
	out, err := runLLMS(t)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if out != string(pgedgecli.LLMS) {
		t.Error("llms output does not match the embedded llms.txt index")
	}

	// It must route rather than dead-end, at both levels: to a module's
	// reference and to a sub-scope of it.
	for _, route := range []string{
		"pgedge llms starfleet", "pgedge llms starfleet byoc",
	} {
		if !strings.Contains(out, route) {
			t.Errorf("the index does not tell an agent how to reach %q "+
				"— routing is the index's whole job", route)
		}
	}

	// And it must NOT have quietly become the monolith again. A module
	// command that only appears in that module's own reference is the
	// cheapest probe for that.
	if strings.Contains(out, "cluster create") {
		t.Error("the index contains a module's command reference — it " +
			"is supposed to carry only what is true CLI-wide")
	}
}

func TestLLMSPrintsOneModulesReference(t *testing.T) {
	module.Reset()
	t.Cleanup(module.Reset)
	module.Register(refModule{name: "alpha", ref: []byte("# alpha ref\n")})
	module.Register(refModule{name: "beta", ref: []byte("# beta ref\n")})

	out, err := runLLMS(t, "alpha")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "# alpha ref\n" {
		t.Errorf("want alpha's reference, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Error("printing one module's reference leaked another's")
	}
}

// TestLLMSRejectsUnknownModule checks both halves of the failure: it is
// a usage error (exit 2, not 1), and the message names what the binary
// actually carries rather than leaving the caller to guess.
func TestLLMSRejectsUnknownModule(t *testing.T) {
	module.Reset()
	t.Cleanup(module.Reset)
	module.Register(refModule{name: "alpha", ref: []byte("# alpha ref\n")})

	_, err := runLLMS(t, "nosuch")
	if err == nil {
		t.Fatal("an unknown module name must be an error")
	}
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, ExitUsage)
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("the error should name the modules this binary "+
			"carries, got %q", err)
	}
}

// TestLLMSSkipsModulesWithoutReference pins the ProvidesLLMS contract
// from the caller's side: a module that ships no reference is not
// offered in the routing hint and is not printable.
func TestLLMSSkipsModulesWithoutReference(t *testing.T) {
	module.Reset()
	t.Cleanup(module.Reset)
	module.Register(refModule{name: "alpha", ref: []byte("# alpha ref\n")})
	module.Register(refModule{name: "bare"})

	if got := referenceModules(); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("referenceModules() = %v, want [alpha] — a module with "+
			"no reference must not be advertised", got)
	}

	if _, err := runLLMS(t, "bare"); err == nil {
		t.Error("a module with no reference must not print as empty " +
			"success")
	}
}

// registerDocs sets up a controlled registry holding one module with
// pages two levels deep and one plain module; the tests below all
// drive `pgedge llms` against it.
func registerDocs(t *testing.T) {
	t.Helper()
	module.Reset()
	t.Cleanup(module.Reset)
	module.Register(docModule{
		refModule: refModule{name: "fake", ref: []byte("# fake ref\n")},
		// Named "gamma", not "page": a failure message below contains
		// the word "page", so a fixture named "page" would satisfy a
		// substring check whether or not the message listed anything.
		docs: []module.Document{
			{Scope: "fake gamma", Body: []byte("# fake gamma\n")},
			{Scope: "fake gamma delta", Body: []byte("# fake gamma delta\n")},
			{Scope: "fake gamma epsilon", Body: []byte("# fake gamma epsilon\n")},
		},
	})
	module.Register(refModule{name: "plain", ref: []byte("# plain ref\n")})
}

// TestLLMSModuleStillPrintsWholeReference pins that a longer path did
// not change what one argument means: `llms <module>` prints the
// module's own index, not a page.
func TestLLMSModuleStillPrintsWholeReference(t *testing.T) {
	registerDocs(t)

	out, err := runLLMS(t, "fake")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "# fake ref\n" {
		t.Errorf("want the module reference, got %q", out)
	}
}

// TestLLMSPrintsPagesAtEveryDepth is the routing the split exists
// for: `pgedge llms starfleet byoc database mcp` must print that page
// alone, so an agent working on one resource reads only that resource.
func TestLLMSPrintsPagesAtEveryDepth(t *testing.T) {
	registerDocs(t)

	for path, want := range map[string]string{
		"fake gamma":         "# fake gamma\n",
		"fake gamma delta":   "# fake gamma delta\n",
		"fake gamma epsilon": "# fake gamma epsilon\n",
	} {
		out, err := runLLMS(t, strings.Fields(path)...)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if out != want {
			t.Errorf("%s printed %q, want %q", path, out, want)
		}
	}
}

// TestLLMSRejectsUnknownPage checks both halves of the failure: it is
// a usage error (exit 2), and the message lists the pages below the
// nearest page that exists rather than leaving the caller guessing.
func TestLLMSRejectsUnknownPage(t *testing.T) {
	registerDocs(t)

	_, err := runLLMS(t, "fake", "nope")
	if err == nil {
		t.Fatal("an unknown page name must be an error")
	}
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, ExitUsage)
	}
	// The list segment, not the bare name: deleting the joined names
	// from the message must redden this.
	if !strings.Contains(err.Error(), "it carries: gamma") {
		t.Errorf("the error should name the pages below the module, "+
			"got %q", err)
	}

	// Two levels down, the nearest page is gamma and the list is its
	// children, not the module's.
	_, err = runLLMS(t, "fake", "gamma", "zeta")
	if err == nil {
		t.Fatal("an unknown page below a page must be an error")
	}
	if !strings.Contains(err.Error(), "`pgedge llms fake gamma` it "+
		"carries: delta, epsilon") {
		t.Errorf("the error should route from the nearest page, got %q",
			err)
	}

	// A leaf has nothing below it, and says so instead of listing
	// its siblings as if they were children.
	_, err = runLLMS(t, "fake", "gamma", "delta", "deeper")
	if err == nil || !strings.Contains(err.Error(), "has none below it") {
		t.Errorf("a path below a leaf should say the leaf has no "+
			"pages, got %v", err)
	}
}

// TestLLMSRejectsPageOnModuleWithoutAny pins the other no-match shape:
// a module that ships one page must fail on a longer path, not print
// its whole reference and pretend the path was honoured.
func TestLLMSRejectsPageOnModuleWithoutAny(t *testing.T) {
	registerDocs(t)

	out, err := runLLMS(t, "plain", "gamma")
	if err == nil {
		t.Fatal("a module with no pages must reject a second argument")
	}
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, ExitUsage)
	}
	if strings.Contains(out, "# plain ref") {
		t.Error("the module's whole reference was printed for an " +
			"unmatched page name")
	}
}

// TestLLMSRejectsUnknownModuleWithPage keeps the module check first: a
// bad module name must fail as a bad module name, whatever follows it.
func TestLLMSRejectsUnknownModuleWithPage(t *testing.T) {
	registerDocs(t)

	_, err := runLLMS(t, "nosuch", "gamma")
	if err == nil {
		t.Fatal("an unknown module name must be an error even with a " +
			"second argument")
	}
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, ExitUsage)
	}
	if !strings.Contains(err.Error(), "fake") {
		t.Errorf("the error should name the modules this binary "+
			"carries, got %q", err)
	}
}

// TestCompleteReferencePathOffersChildren pins Tab completion: at each
// position it offers the pages one level below the path typed so far,
// and nothing below a leaf or a module with no pages.
func TestCompleteReferencePathOffersChildren(t *testing.T) {
	registerDocs(t)

	got, _ := completeReferencePath(nil, []string{"fake"}, "")
	if len(got) != 1 || got[0] != "gamma" {
		t.Errorf("completion after `llms fake` = %v, want [gamma]", got)
	}
	got, _ = completeReferencePath(nil, []string{"fake", "gamma"}, "")
	if strings.Join(got, ",") != "delta,epsilon" {
		t.Errorf("completion after `llms fake gamma` = %v", got)
	}
	if got, _ := completeReferencePath(
		nil, []string{"plain"}, ""); len(got) != 0 {
		t.Errorf("completion after `llms plain` = %v, want none", got)
	}
	if got, _ := completeReferencePath(
		nil, []string{"fake", "gamma", "delta"}, ""); len(got) != 0 {
		t.Errorf("completion below a leaf = %v, want none", got)
	}
}

// TestLLMSPrintsARootPage pins the root's own pages: a top-level
// command group that is not a module prints its page, and a miss
// names the pages as well as the modules.
func TestLLMSPrintsARootPage(t *testing.T) {
	module.Reset()
	t.Cleanup(module.Reset)
	module.Register(refModule{name: "alpha", ref: []byte("# alpha ref\n")})

	pages, err := pgedgecli.Pages()
	if err != nil || len(pages) == 0 {
		t.Fatalf("root pages = %d, %v", len(pages), err)
	}
	for _, p := range pages {
		out, err := runLLMS(t, strings.Fields(p.Scope)...)
		if err != nil {
			t.Fatalf("llms %s: %v", p.Scope, err)
		}
		if out != string(p.Body) {
			t.Errorf("llms %s printed something other than its page",
				p.Scope)
		}
	}

	_, err = runLLMS(t, pages[0].Scope, "nosuch")
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("a path below a root page: exit %d, want %d",
			got, ExitUsage)
	}
	if err == nil || !strings.Contains(err.Error(), pages[0].Scope) {
		t.Errorf("the error should name the root pages, got %v", err)
	}

	got, _ := completeReferencePath(nil, nil, "")
	if !strings.Contains(strings.Join(got, ","), pages[0].Scope) {
		t.Errorf("completion at the first position = %v, want the "+
			"root pages among the modules", got)
	}
}
