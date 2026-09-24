// Package controlplane implements the pgEdge Control Plane module of
// the unified pgedge CLI.
package controlplane

import (
	"embed"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/cmd"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
	"github.com/spf13/cobra"
)

// Version is the controlplane module's version, injected via ldflags. The
// default is what a plain `go build` reports.
var Version = "dev"

// files holds the controlplane reference pages, embedded here so the
// module carries its own documentation.
//
//go:embed llms*
var files embed.FS

// Dir is this package's path under the repository root.
const Dir = "internal/controlplane"

// Module is the controlplane module singleton.
type Module struct{}

func (m *Module) Name() string  { return "controlplane" }
func (m *Module) Short() string { return "Manage a pgEdge Control Plane" }

func (m *Module) Command(rt *module.Runtime) (*cobra.Command, error) {
	return cmd.NewControlplaneCmd(rt), nil
}

// Describe returns the module's self-description. ContractVersion is
// reserved.
func (m *Module) Describe() module.ModuleInfo {
	return module.ModuleInfo{
		Name:         m.Name(),
		Short:        m.Short(),
		Version:      Version,
		ProvidesLLMS: len(m.Reference()) > 0,
	}
}

// Reference returns the controlplane module's index page.
func (m *Module) Reference() []byte {
	for _, d := range m.Documents() {
		if d.Scope == m.Name() {
			return d.Body
		}
	}
	return nil
}

// Documents returns every page the controlplane module ships.
func (m *Module) Documents() []module.Document {
	docs, err := reference.FromFS(files, Dir, m.Name())
	if err != nil {
		return nil
	}
	return docs
}
