// Package managed holds the managed sub-tree of the starfleet module: its
// generated API client, its command tree and its reference pages.
package managed

import (
	"embed"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
)

// files holds the managed reference pages. The pattern is `llms*` so
// the llms/ directory is optional.
//
//go:embed llms*
var files embed.FS

// Dir is this package's path under the repository root.
const Dir = "internal/starfleet/managed"

// Documents returns the managed pages, index first, or nil on an
// embedding error that TestEveryModuleShipsItsReference reports.
func Documents() []module.Document {
	docs, err := reference.FromFS(files, Dir, "starfleet managed")
	if err != nil {
		return nil
	}
	return docs
}

// Reference is the managed index page, served by `pgedge llms starfleet
// managed`.
var Reference = indexBody()

func indexBody() []byte {
	for _, d := range Documents() {
		if d.Scope == "starfleet managed" {
			return d.Body
		}
	}
	return nil
}
