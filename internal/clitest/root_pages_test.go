package clitest

import (
	"testing"

	pgedgecli "github.com/pgEdge/pgedge-cli"
)

// TestRootPagesAreLoadedAndScanned guards RootPages, which drops an
// embed error: a malformed root page set must fail here rather than
// quietly leaving every prose gate reading fewer files.
func TestRootPagesAreLoadedAndScanned(t *testing.T) {
	pages, err := pgedgecli.Pages()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 || len(RootPages()) != len(pages) {
		t.Fatalf("root pages: embed has %d, RootPages has %d",
			len(pages), len(RootPages()))
	}
	files := make(map[string]bool)
	for _, f := range ReferenceFiles() {
		files[f] = true
	}
	for _, p := range pages {
		if !files["../../"+p.Path] {
			t.Errorf("%s is a root page the prose gates do not read",
				p.Path)
		}
	}
}

// TestNoModuleShadowsARootPage pins the two name sets apart: `pgedge
// llms <name>` tries modules first, so a module named like a root page
// would hide that page without an error.
func TestNoModuleShadowsARootPage(t *testing.T) {
	pages := make(map[string]bool)
	for _, p := range RootPages() {
		pages[p.Scope] = true
	}
	for _, m := range Modules() {
		if pages[m.Name()] {
			t.Errorf("module %q shadows the root page of that name",
				m.Name())
		}
	}
}
