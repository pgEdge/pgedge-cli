package byoc

import (
	"strings"
	"testing"
)

// TestReferenceIsShipped pins that the byoc sub-reference is actually
// embedded. The starfleet module serves these bytes as a sub-reference, so
// an empty document would route an agent to nothing at all.
func TestReferenceIsShipped(t *testing.T) {
	if len(Reference) == 0 {
		t.Fatal("byoc ships no sub-reference document")
	}

	// The title is checked, not just the length: two sibling packages
	// embed a same-named llms.txt, so a crossed //go:embed serves the
	// wrong document at full length and every other gate stays green.
	const title = "# pgedge starfleet byoc"
	if !strings.HasPrefix(string(Reference), title) {
		t.Errorf("the embedded sub-reference does not start with %q — "+
			"check the //go:embed directive, it may be serving "+
			"another sub-tree's document", title)
	}
}
