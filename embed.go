// Package pgedgecli embeds the AI-facing index and the root's own
// pages, so the binary serves them without a checkout.
//
// Each module embeds its own reference alongside its code
// (internal/<module>/llms.txt), printed by `pgedge llms <module>`. A
// single llms-full.txt covering every module cost an agent roughly 40k
// tokens to look up one flag, and left module scope for the doc gates
// to infer rather than read off the file.
//
// Agent skills in skills/ are distributed by external installers (see
// the README), not embedded.
package pgedgecli

import (
	"embed"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/reference"
)

// LLMS is the AI-agent index (llms.txt), printed by `pgedge llms`.
//
//go:embed llms.txt
var LLMS []byte

//go:embed llms.txt llms
var files embed.FS

// Pages returns the root's own pages, one per top-level command group
// (`pgedge llms inspect`), without the index. They keep the index
// small: a task pays for a command's page only when it runs it.
func Pages() ([]module.Document, error) {
	docs, err := reference.FromFS(files, "", "")
	if err != nil {
		return nil, err
	}
	return docs[1:], nil
}

// IndexSizeBudget bounds the size of the top-level llms.txt index.
//
// Every section added to the index makes it more like the monolith it
// replaced (the module references totalled 406 KB on 2026-09-24). The
// budget is the size at the time plus headroom, to make growth
// deliberate rather than force a rewrite; raise it on purpose, in a
// commit that says why.
//
// The module references are exempt: an agent reaches one only after
// the index has told it which, so its size is paid by a caller who
// already wanted that module.
const IndexSizeBudget = 28 * 1024
