// Package cloud is the pgEdge Starfleet module: one product, one
// connection, three faces (account-level resources, byoc, managed).
package starfleet

import (
	"embed"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/cmd"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed"
	"github.com/spf13/cobra"
)

// Version is stamped at build time from versions.env (STARFLEET_VERSION)
// via -ldflags; see the Makefile and .goreleaser.yaml. The default is
// what a plain `go build` reports.
var Version = "dev"

// files holds the starfleet module's own reference: llms.txt is the
// index and every llms/**/*.txt is one page (see internal/reference).
// byoc and managed embed theirs in their own packages.
//
//go:embed llms*
var files embed.FS

// Dir is this package's path under the repository root.
const Dir = "internal/starfleet"

// Module implements module.Module for the starfleet tree.
type Module struct{}

// Name returns the module's command name.
func (m *Module) Name() string { return "starfleet" }

// Short returns the module's one-line description.
func (m *Module) Short() string { return "Manage pgEdge Starfleet" }

// Command builds the cloud command tree for the given runtime.
func (m *Module) Command(rt *module.Runtime) (*cobra.Command, error) {
	return cmd.NewStarfleetCmd(rt), nil
}

// Describe returns the module's self-description. ContractVersion is
// intentionally empty: it is reserved for the future external-dispatch
// contract and carries no meaning while cloud ships in-process.
func (m *Module) Describe() module.ModuleInfo {
	return module.ModuleInfo{
		Name:         m.Name(),
		Short:        m.Short(),
		Version:      Version,
		ProvidesLLMS: len(m.Reference()) > 0,
	}
}

// Reference returns the starfleet module's index page.
func (m *Module) Reference() []byte {
	for _, d := range m.Documents() {
		if d.Scope == m.Name() {
			return d.Body
		}
	}
	return nil
}

// Documents returns every page the starfleet tree ships: the module's
// own, then byoc's and managed's, so `pgedge llms starfleet byoc
// database` resolves through one list. There is no "account" page:
// account dissolved into the starfleet level, so its commands are
// documented in the module's own pages.
func (m *Module) Documents() []module.Document {
	own, err := reference.FromFS(files, Dir, m.Name())
	if err != nil {
		return nil
	}
	own = append(own, byoc.Documents()...)
	return append(own, managed.Documents()...)
}
