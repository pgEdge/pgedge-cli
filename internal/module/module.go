// Package module defines the contract between the base pgedge CLI
// and its modules. The Runtime struct is deliberately the single
// seam: when external binary dispatch is added, this same data
// becomes the env/flag contract handed to external binaries, and
// the registry grows a fallback path — the interface does not
// change.
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

	// ProfileExplicit reports that the operator named a profile on
	// this invocation — cobra parsed a --profile token AND it carried
	// a value. internal/cli.setupRuntime sets it from the executing
	// command's own flag set; the pre-cobra argv peek it once used is
	// long gone (#120, #153).
	//
	// An empty --profile never reaches this field: setupRuntime rejects
	// it as a usage error first (#246). The value test in that
	// assignment is therefore unreachable today, and is kept only so
	// this field stays correct if the rejection is ever narrowed —
	// exempting the repair commands is the obvious candidate. An empty
	// value names no profile, so Profile would come from
	// current_profile, and calling that explicit would shut the #150
	// repair carve-out in cli.GuardProfile, this field's only reader:
	// `profile use` repairs a broken current_profile, and it would have
	// been refused in the name of a value the operator never typed.
	ProfileExplicit bool

	Output  *output.Renderer
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Verbose bool
	Debug   bool

	// DryRun is non-nil when --dry-run is active, and is where a
	// stopped write and the checks that passed before it are recorded.
	//
	// One field rather than a bool beside a recorder, so there is no
	// "enabled with nowhere to record" state to get wrong. Every method
	// on it tolerates a nil receiver, which is what lets check sites
	// call rt.DryRun.Pass unconditionally instead of each guarding on a
	// mode flag.
	DryRun *dryrun.Run

	// Keychain is the OS credential store. nil means unavailable, which
	// is what every test Runtime gets, so no test reaches the real one.
	Keychain keychain.Store
}

// ModuleInfo is a module's uniform self-description. Built-in modules
// return it from Describe(); a future external module binary would
// emit the same shape as JSON from a __describe subcommand.
type ModuleInfo struct {
	Name            string
	Short           string
	Version         string
	ContractVersion string // RESERVED — empty in-process, no enforcement

	// ProvidesLLMS reports whether Reference() returns a reference
	// document. `pgedge llms` uses it to build its routing table, so a
	// module that ships no reference is simply not offered rather than
	// offered and empty.
	ProvidesLLMS bool
}

// Module is an in-process pgedge module.
type Module interface {
	Name() string
	Short() string
	Command(rt *Runtime) (*cobra.Command, error)
	Describe() ModuleInfo

	// Reference returns the module's AI-agent reference document —
	// every command it owns, with usage, flags and worked examples.
	// `pgedge llms <name>` prints it.
	//
	// It is on the interface rather than in a map somewhere because the
	// reference used to be one 163 KB llms-full.txt covering every
	// module, which cost an agent roughly 40k tokens to look up a
	// single flag and made module scope something the doc gates had to
	// INFER, from "## Module:" headings and a hardcoded path
	// special-case. Splitting it per module makes the file the scope,
	// and putting it here makes a module self-contained: its command
	// tree and its documentation arrive together, and a new module
	// cannot be registered while quietly forgetting to document
	// itself. A central map would work today and would recreate the
	// monolith by degrees.
	//
	// Return nil if the module ships no reference, and report
	// ProvidesLLMS false from Describe() to match.
	Reference() []byte
}

// Document is one page of a module's reference. Scope is the
// space-joined command path under root it documents ("starfleet byoc",
// "starfleet byoc database mcp"); `pgedge llms <scope words>` prints
// Body. Path is the file under the repository root the bytes were
// embedded from, so the generator and the gates can find the file a
// served page came from without a second list.
//
// The set is derived from files, not declared: internal/reference
// walks a package's embedded llms.txt and llms/ directory and turns
// llms/database/mcp.txt into the scope suffix "database mcp". A page
// is served because it exists, and a page that exists is gated
// because it is served.
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

// Reset clears the registry. It exists so tests in OTHER packages can
// establish a known set of modules — internal/cli's llms tests need a
// registry they control, and cannot build one by importing the real
// modules without an import cycle. Production code never calls it, and
// it takes no *testing.T so this package keeps no test-only import.
func Reset() { registry = nil }

func reset() { Reset() }
