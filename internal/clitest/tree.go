// Package clitest assembles the full pgedge command tree for
// build-gate tests. It lives in its own package so it can import
// both internal/cli and the modules (internal/starfleet, internal/controlplane)
// without the import cycle that would arise if these gates lived in
// cli itself.
package clitest

import (
	"strings"

	pgedgecli "github.com/pgEdge/pgedge-cli"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane"
	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
	"github.com/pgEdge/pgedge-cli/internal/starfleet"
	"github.com/spf13/cobra"
)

// Modules is the set of modules the shipped binary carries, in the
// same order main.go registers them. Both FullTree and the version
// gate build from this one slice, so adding a module here (as main.go
// must also do) brings it under every build gate.
func Modules() []module.Module {
	return []module.Module{&starfleet.Module{}, &controlplane.Module{}}
}

// FullTree returns the complete pgedge command tree as shipped:
// the root command plus every registered module, wired the same
// way main.go wires them. The runtime is a zero-value Runtime,
// which NewRootCmd and the module constructors accept for the
// structural checks the gates perform.
func FullTree() (*cobra.Command, error) {
	rt := &module.Runtime{}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			return nil, err
		}
		root.AddCommand(c)
	}
	// Mirrors cmd/pgedge/main.go, which calls this after registering
	// every module. Without it the gates would walk a tree whose
	// group commands suggest nothing while the shipped binary's do, and
	// a behavioural gate over the suggestion would pass or fail for a
	// reason unrelated to the CLI anyone runs.
	cli.AddUnknownCommandSuggestions(root)
	return root, nil
}

// ReferenceDocuments returns every page the shipped modules embed, in
// module order then scope order. It is the one derivation of the page
// set the generator and every gate share: a module that ships a page
// not listed here cannot exist, because this reads the modules.
func ReferenceDocuments() []module.Document {
	out := RootPages()
	for _, m := range Modules() {
		if d, ok := m.(module.Documented); ok {
			out = append(out, d.Documents()...)
			continue
		}
		out = append(out, module.Document{
			Scope: m.Name(), Body: m.Reference()})
	}
	return out
}

// RootPages returns the root's own pages, without the index. An error
// means the embedded page set is malformed;
// TestRootPagesAreLoadedAndScanned fails on it.
func RootPages() []module.Document {
	docs, err := pgedgecli.Pages()
	if err != nil {
		return nil
	}
	return docs
}

// RoutingFor returns the routing rows for scope: its child pages, each
// with the Short of the command it documents, or an empty Short for a
// page that documents no command.
func RoutingFor(all []*cobra.Command, docs []module.Document,
	scope string) []docgen.RoutingEntry {
	short := make(map[string]string, len(all))
	for _, c := range all {
		short[strings.TrimSpace(strings.TrimPrefix(c.CommandPath(),
			"pgedge"))] = c.Short
	}
	var out []docgen.RoutingEntry
	for _, k := range reference.Children(docs, scope) {
		// The root index routes to modules by hand, so its table
		// lists only the root's own pages.
		if scope == "" && !strings.HasPrefix(k.Path, "llms/") {
			continue
		}
		s := short[k.Scope]
		if summary, ok := reference.TopicSummary(k.Body); ok {
			s = summary
		}
		out = append(out, docgen.RoutingEntry{Scope: k.Scope, Short: s})
	}
	return out
}

// ReferenceFiles returns every reference document as a path relative
// to this package: the root index, then every module page. Gates that
// scan prose walk this rather than globbing for llms.txt, which is how
// two sweeps missed the nested byoc and managed files and how any glob
// would now miss the pages under llms/.
func ReferenceFiles() []string {
	out := []string{"../../llms.txt"}
	for _, d := range ReferenceDocuments() {
		out = append(out, "../../"+d.Path)
	}
	return out
}

// ReferenceFilesFor returns the reference files of one module: its
// index and every page under it, keyed by the directory
// ReferenceModuleDir returns. A gate that used to read a module's one
// llms.txt reads this instead, because the split moved prose onto
// pages without moving it out of that module.
func ReferenceFilesFor(moduleDir string) []string {
	var out []string
	for _, f := range ReferenceFiles() {
		if ReferenceModuleDir(f) == moduleDir {
			out = append(out, f)
		}
	}
	return out
}

// ReferenceModuleDir returns the module package directory a reference
// file belongs to, relative to this package
// ("../../internal/starfleet/byoc"), or
// "" for the root index. Gates keyed by module use it so a page
// inherits its module's configuration instead of needing its own row.
func ReferenceModuleDir(file string) string {
	for _, m := range Modules() {
		d, ok := m.(module.Documented)
		if !ok {
			continue
		}
		for _, doc := range d.Documents() {
			if "../../"+doc.Path == file {
				dir := doc.Path
				if i := strings.Index(dir, "/llms"); i >= 0 {
					dir = dir[:i]
				}
				return "../../" + dir
			}
		}
	}
	return ""
}
