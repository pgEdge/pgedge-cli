// Package reference derives a module's reference pages from the files
// it embeds, and answers the routing questions `pgedge llms` and the
// generator ask of them.
//
// The layout is the contract. A package's llms.txt is its index page,
// and every llms/**/*.txt beneath it is one more page whose scope is
// the file path with the directory separators and the extension
// removed: llms/database/mcp.txt under "starfleet byoc" is the page for
// `pgedge starfleet byoc database mcp`. Nothing declares the set, so
// there is no list to fall behind the files, and every gate that needs
// the set reads it from here rather than keeping its own.
package reference

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

// PageSizeBudget bounds one reference page below the module index.
//
// The split exists so an agent working on one resource reads that
// resource's page and nothing else; a page has to be small enough for
// that to be a saving. 25 KB is about 6k tokens. The module index is
// not bound by it: an index carries the cross-cutting prose every
// resource shares once, and its own budget is a separate question.
const PageSizeBudget = 25 * 1024

const (
	indexFile = "llms.txt"
	pagesDir  = "llms"
	pageExt   = ".txt"
)

// FromFS returns the pages a package embeds, sorted by scope. dir is
// the package's path under the repository root and scope its command
// path under `pgedge`, "" for the root; both are recorded on every
// page so a caller can go from a served page back to its file.
func FromFS(fsys fs.FS, dir, scope string) ([]module.Document, error) {
	index, err := fs.ReadFile(fsys, indexFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	docs := []module.Document{{
		Scope: scope, Path: path.Join(dir, indexFile), Body: index,
	}}

	if _, err := fs.Stat(fsys, pagesDir); err != nil {
		return docs, nil
	}
	err = fs.WalkDir(fsys, pagesDir, func(p string, d fs.DirEntry,
		err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, pageExt) {
			return fmt.Errorf("%s/%s: a reference page must end in %s",
				dir, p, pageExt)
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, pagesDir+"/"),
			pageExt)
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		docs = append(docs, module.Document{
			Scope: strings.TrimSpace(
				scope + " " + strings.ReplaceAll(rel, "/", " ")),
			Path: path.Join(dir, p),
			Body: body,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Scope < docs[j].Scope
	})
	return docs, nil
}

const (
	topicOpen  = "<!-- topic: "
	topicClose = " -->"
)

// TopicSummary reports whether a page is a topic page, one that
// documents a task spanning several resources rather than a command,
// and returns the summary its routing-table row shows. A topic page
// says so on its first line, `<!-- topic: <summary> -->`, because
// nothing else tells it apart from a resource page whose command was
// deleted.
func TopicSummary(body []byte) (string, bool) {
	first, _, _ := strings.Cut(string(body), "\n")
	inner, ok := strings.CutPrefix(first, topicOpen)
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, topicClose)
	inner = strings.TrimSpace(inner)
	if !ok || inner == "" {
		return "", false
	}
	return inner, true
}

// Find returns the page for exactly this scope.
func Find(docs []module.Document, scope string) (module.Document, bool) {
	for _, d := range docs {
		if d.Scope == scope {
			return d, true
		}
	}
	return module.Document{}, false
}

// Children returns the pages one word below scope, in scope order.
// They are what a routing table lists and what Tab completion offers.
func Children(docs []module.Document, scope string) []module.Document {
	var out []module.Document
	for _, d := range docs {
		rest, ok := strings.CutPrefix(d.Scope, scope+" ")
		if scope == "" {
			rest, ok = d.Scope, d.Scope != ""
		}
		if ok && !strings.Contains(rest, " ") {
			out = append(out, d)
		}
	}
	return out
}

// LongestPrefix returns the page whose scope is the longest prefix of
// scope, word-wise, so a miss can be reported against the nearest
// page that does exist.
func LongestPrefix(docs []module.Document, scope string) (
	module.Document, bool,
) {
	best, found := module.Document{}, false
	for _, d := range docs {
		if (scope == d.Scope || strings.HasPrefix(scope, d.Scope+" ")) &&
			len(d.Scope) > len(best.Scope) {
			best, found = d, true
		}
	}
	return best, found
}

// Scopes returns every page's scope, in order, which is the set of
// ownership scopes the generator and the conformance gate share.
func Scopes(docs []module.Document) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Scope)
	}
	return out
}
