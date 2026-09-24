package module

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// fakeModule carries a ref so tests can cover both halves of the
// Reference contract: a module that ships a reference document and one
// that does not. ProvidesLLMS is derived from ref rather than set
// independently, which is the same rule the real modules follow — the
// two must never be able to disagree.
type fakeModule struct {
	name string
	ref  []byte
}

func (f *fakeModule) Name() string  { return f.name }
func (f *fakeModule) Short() string { return "fake " + f.name }
func (f *fakeModule) Command(rt *Runtime) (*cobra.Command, error) {
	return &cobra.Command{Use: f.name, Short: f.Short()}, nil
}
func (f *fakeModule) Describe() ModuleInfo {
	return ModuleInfo{
		Name:         f.name,
		Short:        f.Short(),
		ProvidesLLMS: len(f.ref) > 0,
	}
}
func (f *fakeModule) Reference() []byte { return f.ref }

// TestReferenceAndProvidesLLMSAgree pins the invariant that makes
// `pgedge llms`'s routing table trustworthy: a module that advertises
// ProvidesLLMS must return a reference, and one that does not must
// return nothing. If those could disagree, the router would either
// offer a module whose reference prints empty, or hide one that has a
// perfectly good document.
func TestReferenceAndProvidesLLMSAgree(t *testing.T) {
	cases := []*fakeModule{
		{name: "with-ref", ref: []byte("# reference\n")},
		{name: "without-ref"},
	}

	for _, m := range cases {
		t.Run(m.name, func(t *testing.T) {
			advertised := m.Describe().ProvidesLLMS
			if got := len(m.Reference()) > 0; got != advertised {
				t.Errorf("Describe().ProvidesLLMS = %v but Reference() "+
					"returns %d bytes — the two must agree",
					advertised, len(m.Reference()))
			}
		})
	}
}

// docModule is a module that ships several reference pages, the
// shape `pgedge llms starfleet byoc database` serves.
type docModule struct {
	fakeModule
	docs []Document
}

func (d *docModule) Documents() []Document { return d.docs }

// TestDocumentedSurvivesTheRegistry pins the one thing the optional
// interface has to do: a module registered as a plain Module must
// still type-assert to Documented on the way out, because that
// assertion is how `pgedge llms <path>` finds the pages. A module
// without pages must NOT assert, so the router can tell "no such
// page" from "no pages at all".
func TestDocumentedSurvivesTheRegistry(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Register(&docModule{
		fakeModule: fakeModule{name: "withdocs", ref: []byte("# ref\n")},
		docs: []Document{
			{Scope: "withdocs", Body: []byte("# ref\n")},
			{Scope: "withdocs one", Body: []byte("# one\n")},
		},
	})
	Register(&fakeModule{name: "plain", ref: []byte("# ref\n")})

	byName := make(map[string]Module)
	for _, m := range Registered() {
		byName[m.Name()] = m
	}

	d, ok := byName["withdocs"].(Documented)
	if !ok {
		t.Fatal("a Documented module did not survive registration as " +
			"one — `pgedge llms <module> <page>` would find nothing")
	}
	docs := d.Documents()
	if len(docs) != 2 || docs[1].Scope != "withdocs one" ||
		string(docs[1].Body) != "# one\n" {
		t.Errorf("Documents() = %+v, want the index and one page", docs)
	}

	if _, ok := byName["plain"].(Documented); ok {
		t.Error("a module with one page must not implement Documented")
	}
}

func TestRegistryBuildsCommands(t *testing.T) {
	reset() // test helper clearing the registry
	Register(&fakeModule{name: "alpha"})
	Register(&fakeModule{name: "beta"})
	cmds, err := BuildCommands(&Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2", len(cmds))
	}
	if cmds[0].Use != "alpha" || cmds[1].Use != "beta" {
		t.Errorf("order not preserved: %s, %s",
			cmds[0].Use, cmds[1].Use)
	}
}

func TestDuplicateRegistrationPanicsAtRegister(t *testing.T) {
	reset()
	Register(&fakeModule{name: "dup"})
	defer func() {
		if recover() == nil {
			t.Error("want panic on duplicate module name")
		}
	}()
	Register(&fakeModule{name: "dup"})
}

func TestRegistered(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Register(&fakeModule{name: "alpha"})
	Register(&fakeModule{name: "beta"})
	got := Registered()
	if len(got) != 2 || got[0].Name() != "alpha" || got[1].Name() != "beta" {
		t.Errorf("Registered() = %v, want [alpha beta] in order", got)
	}
}

type failingModule struct{}

func (failingModule) Name() string  { return "failing" }
func (failingModule) Short() string { return "always fails" }
func (failingModule) Command(rt *Runtime) (*cobra.Command, error) {
	return nil, errors.New("boom")
}
func (failingModule) Reference() []byte { return nil }
func (failingModule) Describe() ModuleInfo {
	return ModuleInfo{Name: "failing", Short: "always fails"}
}

func TestBuildCommandsPropagatesModuleError(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Register(failingModule{})
	if _, err := BuildCommands(&Runtime{}); err == nil {
		t.Fatal("want error when a module fails to build its command")
	}
}

func TestDescribeAll(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Register(&fakeModule{name: "a"})
	Register(&fakeModule{name: "b"})
	infos := DescribeAll()
	if len(infos) != 2 || infos[0].Name != "a" || infos[1].Name != "b" {
		t.Fatalf("DescribeAll = %+v", infos)
	}
}
