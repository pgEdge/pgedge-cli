package output_test

import (
	"slices"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/output"
)

// TestCheckRowColumns pins the two properties the three doctors relied
// on their own copies of this type for: field order matching
// CheckHeaders, and Status — and only Status — going through
// ColorStatus. Colour on the Check or Details column would corrupt an
// otherwise plain table, and colour missing from Status would silently
// undo every doctor's verdict highlighting at once.
func TestCheckRowColumns(t *testing.T) {
	output.ColorEnabled = true
	t.Cleanup(func() { output.ColorEnabled = false })

	got := output.CheckRow{
		Check:   "Auth",
		Status:  "ok",
		Details: "authenticated via flags",
	}.Columns()

	want := []string{
		"Auth",
		output.ColorStatus("ok"),
		"authenticated via flags",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Columns() = %q, want %q", got, want)
	}
	if got[1] == "ok" {
		t.Error("Status was not coloured; ColorStatus was skipped")
	}
	if got[0] != "Auth" || got[2] != "authenticated via flags" {
		t.Errorf("Check/Details must pass through uncoloured: %q", got)
	}
}

// TestCheckRowColumnsNoColor is the other half: with colour disabled the
// row must be plain strings, so piping a doctor into a file or a test
// buffer yields comparable text.
func TestCheckRowColumnsNoColor(t *testing.T) {
	output.ColorEnabled = false

	got := output.CheckRow{
		Check: "API connectivity", Status: "error", Details: "unreachable",
	}.Columns()
	want := []string{"API connectivity", "error", "unreachable"}
	if !slices.Equal(got, want) {
		t.Errorf("Columns() = %q, want %q", got, want)
	}
}

// TestCheckHeadersIsNotShared proves CheckHeaders hands out a fresh
// slice each call. It is a function rather than a package-level var so
// one doctor cannot reorder or rename the headers every other doctor
// prints; that guarantee is worth an assertion, since turning it back
// into a var would compile and pass every other test in the tree.
func TestCheckHeadersIsNotShared(t *testing.T) {
	first := output.CheckHeaders()
	if want := []string{"CHECK", "STATUS", "DETAILS"}; !slices.Equal(
		first, want) {
		t.Fatalf("CheckHeaders() = %q, want %q", first, want)
	}
	first[0] = "TAMPERED"
	if second := output.CheckHeaders(); second[0] != "CHECK" {
		t.Errorf("CheckHeaders() = %q after a caller wrote to an "+
			"earlier result; the headers are shared state", second)
	}
}

// TestCheckRowSatisfiesRow keeps the interface conformance a compile
// error rather than a runtime surprise if Columns' signature drifts.
func TestCheckRowSatisfiesRow(t *testing.T) {
	var r output.Row = output.CheckRow{Check: "x"}
	if len(r.Columns()) != 3 {
		t.Errorf("Columns() length = %d, want 3", len(r.Columns()))
	}
}
