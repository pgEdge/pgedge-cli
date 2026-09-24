// Package module defines the contract between the base pgedge CLI
// and its modules. Runtime is the single seam between them.
package module

import (
	"fmt"
	"io"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// Runtime is everything a module receives from the base CLI.
// Modules must not reach into globals.
type Runtime struct {
	Config  *config.Config
	Profile string

	// ProfileExplicit reports that --profile was given a non-empty
	// value on this invocation. cli.GuardProfile's `profile use` repair
	// carve-out and starfleet's env-credential conflict check read it;
	// cli.setupRuntime sets it and says why the value test stays.
	ProfileExplicit bool

	Output  *output.Renderer
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Verbose bool
	Debug   bool

	// DryRun is non-nil when --dry-run is active. One field rather than
	// a bool beside a recorder, so "enabled with nowhere to record"
	// cannot happen.
	DryRun *dryrun.Run

	// Keychain is the OS credential store. nil means unavailable, which
	// is what every test Runtime gets, so no test reaches the real one.
	Keychain keychain.Store
}

// ModuleInfo is a module's uniform self-description, from Describe().
type ModuleInfo struct {
	Name            string
	Short           string
	Version         string
	ContractVersion string // RESERVED — empty in-process, no enforcement

	// ProvidesLLMS reports whether Reference() returns a document, so
	// `pgedge llms` leaves a module without one out of its routing
	// table rather than offering an empty page.
	ProvidesLLMS bool
}

// Module is an in-process pgedge module.
type Module interface {
	Name() string
	Short() string
	Command(rt *Runtime) (*cobra.Command, error)
	Describe() ModuleInfo

	// Reference returns the module's AI-agent index page, which
	// `pgedge llms <name>` prints; per-resource pages come from
	// Documented. It is on the interface rather than in a central map
	// so a module's command tree and its documentation arrive together,
	// and each module's file is its own scope for the doc gates.
	//
	// Return nil if the module ships no reference, and report
	// ProvidesLLMS false from Describe() to match.
	Reference() []byte
}

// Document is one page of a module's reference. Scope is the
// space-joined command path under root it documents ("starfleet byoc",
// "starfleet byoc database mcp"); `pgedge llms <scope words>` prints
// Body. Path is the repository file the bytes were embedded from, so
// the generator and the gates need no second list.
//
// internal/reference derives the set from the embedded llms.txt and
// llms/ directory (llms/database/mcp.txt is scope suffix "database
// mcp"), so every page that exists is served and gated.
type Document struct {
	Scope string
	Path  string
	Body  []byte
}

// Documented is an optional extension of Module for one that ships
// more than a single reference page. Documents includes the module's
// own index (Scope == Name()) so a caller walks one list; Reference()
// returns that same index for callers that want only it.
type Documented interface {
	Documents() []Document
}

var registry []Module

// Register adds a module. Duplicate names are a programming error.
func Register(m Module) {
	for _, existing := range registry {
		if existing.Name() == m.Name() {
			panic(fmt.Sprintf(
				"module: duplicate registration of %q", m.Name()))
		}
	}
	registry = append(registry, m)
}

// Registered returns modules in registration order.
func Registered() []Module { return registry }

// DescribeAll returns each registered module's self-description, in
// registration order.
func DescribeAll() []ModuleInfo {
	infos := make([]ModuleInfo, 0, len(registry))
	for _, m := range registry {
		infos = append(infos, m.Describe())
	}
	return infos
}

// BuildCommands materializes each registered module's command tree.
func BuildCommands(rt *Runtime) ([]*cobra.Command, error) {
	cmds := make([]*cobra.Command, 0, len(registry))
	for _, m := range registry {
		c, err := m.Command(rt)
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", m.Name(), err)
		}
		cmds = append(cmds, c)
	}
	return cmds, nil
}

// Reset clears the registry, for tests in other packages: internal/cli's
// llms tests need a registry they control and cannot import the real
// modules without a cycle. It takes no *testing.T so this package keeps
// no test-only import.
func Reset() { registry = nil }

func reset() { Reset() }
