// Package pgedgecli embeds the AI-facing index shipped in this
// repository so the binary can serve it anywhere it is installed,
// without a checkout of the repo.
//
// Only the INDEX lives here. Each module embeds its own reference
// document alongside its code (internal/<module>/llms.txt), reached
// through module.Module's Reference method and printed by
// `pgedge llms <module>`. That split is deliberate: a single
// llms-full.txt covering every module cost an agent roughly 40k tokens
// to look up one flag, and it made module scope something the doc
// gates had to infer rather than read off the file it was in.
//
// Agent skills live in the repo's skills/ tree and are distributed via
// external, format-native installers (see the README) — they are NOT
// embedded in the binary.
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
// The index exists because printing every module's reference cost the
// whole of that reference to look up one flag -- 314 KB across the
// four module documents as of this writing, and it only grows. Every
// section added to the index makes it a
// little more like the monolith it replaced, and before this budget
// there was no signal that a writer was spending against anything --
// the design comment above cited a figure that had drifted to more
// than twice its stated value (#306).
//
// It is set at the current size plus headroom, not at some ideal: the
// point is to make growth deliberate and visible, not to force a
// rewrite. Raising it is a fine thing to do on purpose, in a commit
// that says why.
//
// The MODULE references are deliberately exempt. They are the
// targeted second read -- an agent reaches one only after the index
// has told it which -- so their size is paid by a caller who already
// knows they want that module, and byoc's is 131 KB precisely because
// it can be.
const IndexSizeBudget = 28 * 1024
