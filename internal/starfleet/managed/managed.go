// Package managed holds the managed sub-tree of the starfleet module: its
// generated API client, its command tree and its reference pages.
package managed

import (
	"embed"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
)

// files holds the managed reference: llms.txt is the index and every
// llms/**/*.txt is one page, scoped by its path (see
// internal/reference). The pattern is `llms*` so that the directory is
// optional: a sub-tree with a single page embeds only its index.
//
//go:embed llms*
var files embed.FS

// Dir is this package's path under the repository root, which every
// page records so a served page can be traced to its file.
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
