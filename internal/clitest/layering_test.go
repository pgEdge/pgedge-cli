package clitest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// platformDirs are the packages on the platform side of the layering
// line. They may never import a module package: the launcher must
// stay independent of every module, or one module's generated client
// rides into every other module's closure (the account/conn
// inversion this gate was written to prevent recurring).
//
// errbody and dryrun are here because they are shared TRANSPORTS: both
// sit below the Starfleet and Control Plane clients, so an import of any
// module's generated client from either one is exactly the inversion
// described above, and the worst possible place for it. errbody's own
// doc comment leans on the claim — it declines to re-declare the
// generated Error type because it "deliberately imports no module's
// api" — so the claim needs a gate rather than a promise.
var platformDirs = []string{
	"../apidefaults", "../auth", "../cli", "../config",
	"../dryrun", "../errbody", "../httplog", "../module", "../output",
}

// moduleImportRe matches an import of any module package, under both
// the pre-move (internal/account, internal/byoc, internal/managed)
// and post-move (internal/starfleet) layouts, plus controlplane.
var moduleImportRe = regexp.MustCompile(
	`^github\.com/pgEdge/pgedge-cli/internal/` +
		`(account|byoc|managed|starfleet|controlplane)(/|$)`)

// platformImports parses every non-test .go file under dir and
// returns its import paths keyed by file.
func platformImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil,
			parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			out[path] = append(out[path],
				strings.Trim(imp.Path.Value, `"`))
		}
	}
	return out
}

func TestPlatformImportsNoModule(t *testing.T) {
	filesSeen := 0
	sawKnownImport := false // positive control, see below
	for _, dir := range platformDirs {
		for file, imports := range platformImports(t, dir) {
			filesSeen++
			for _, imp := range imports {
				if imp == "github.com/pgEdge/pgedge-cli/internal/config" {
					sawKnownImport = true
				}
				if moduleImportRe.MatchString(imp) {
					t.Errorf("%s imports module package %s — "+
						"the platform must not depend on any "+
						"module", file, imp)
				}
			}
		}
	}
	// Positive controls: prove the scanner walked real files and
	// actually parses import blocks. internal/cli imports
	// internal/config today; if that ever stops being true, pick
	// another known platform-to-platform import.
	if filesSeen < 10 {
		t.Fatalf("scanned only %d files — the walk is broken and "+
			"this gate is checking nothing", filesSeen)
	}
	if !sawKnownImport {
		t.Fatal("never saw internal/cli's import of internal/config" +
			" — import parsing is broken and this gate is silently" +
			" passing")
	}
}

func TestLayeringGateCatchesModuleImport(t *testing.T) {
	// The regex is the gate's teeth; prove it bites both layouts.
	for _, bad := range []string{
		"github.com/pgEdge/pgedge-cli/internal/account/conn",
		"github.com/pgEdge/pgedge-cli/internal/starfleet/conn",
		"github.com/pgEdge/pgedge-cli/internal/starfleet",
		"github.com/pgEdge/pgedge-cli/internal/controlplane/cmd",
	} {
		if !moduleImportRe.MatchString(bad) {
			t.Errorf("moduleImportRe missed %q", bad)
		}
	}
	for _, good := range []string{
		"github.com/pgEdge/pgedge-cli/internal/config",
		"github.com/pgEdge/pgedge-cli/internal/apidefaults",
		"github.com/pgEdge/pgedge-cli/internal/starfleetx", // no false prefix match
	} {
		if moduleImportRe.MatchString(good) {
			t.Errorf("moduleImportRe wrongly matched %q", good)
		}
	}
}
