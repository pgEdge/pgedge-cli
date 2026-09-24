package docgen

import (
	"strings"
	"testing"
)

var kids = []RoutingEntry{
	{Scope: "starfleet byoc cluster", Short: "Manage clusters"},
	{Scope: "starfleet byoc database", Short: "Manage databases | more"},
}

func TestRoutingRendersOneRowPerChild(t *testing.T) {
	got := Routing("starfleet byoc", kids)
	for _, want := range []string{
		RoutingBeginMarker("starfleet byoc"),
		"| `cluster` | `pgedge llms starfleet byoc cluster` | Manage clusters |",
		"| `database` | `pgedge llms starfleet byoc database` | Manage databases \\| more |",
		RoutingEndMarker(),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestApplyRoutingRewritesInPlace(t *testing.T) {
	doc := "# title\n\nprose\n\n" + RoutingBeginMarker("starfleet byoc") +
		"\nstale\n" + RoutingEndMarker() + "\n\ntail\n"
	res := ApplyRouting(doc, "starfleet byoc", kids)
	if !res.Changed || res.Missing || res.Stray || res.Foreign != "" {
		t.Fatalf("res = %+v", res)
	}
	if !strings.HasPrefix(res.Doc, "# title\n\nprose\n\n") ||
		!strings.HasSuffix(res.Doc, "\n\ntail\n") {
		t.Errorf("prose around the table was disturbed:\n%s", res.Doc)
	}
	if strings.Contains(res.Doc, "stale") ||
		!strings.Contains(res.Doc, "`cluster`") {
		t.Errorf("table not rewritten:\n%s", res.Doc)
	}
	again := ApplyRouting(res.Doc, "starfleet byoc", kids)
	if again.Changed {
		t.Error("a second apply changed a conforming table")
	}
}

func TestApplyRoutingReportsRatherThanGuesses(t *testing.T) {
	if res := ApplyRouting("# no table\n", "s", kids); !res.Missing {
		t.Error("children with no table must be reported as Missing")
	}
	withTable := RoutingBeginMarker("s") + "\n" + RoutingEndMarker()
	if res := ApplyRouting(withTable, "s", nil); !res.Stray {
		t.Error("a table with no children must be reported as Stray")
	}
	if res := ApplyRouting("# leaf\n", "s", nil); res.Changed ||
		res.Missing || res.Stray {
		t.Errorf("a leaf with no table is conforming, got %+v", res)
	}
	twice := Routing("s", kids) + "\n\n" + Routing("s", kids)
	if res := ApplyRouting(twice, "s", kids); !res.Duplicate || res.Changed {
		t.Errorf("two identical tables must be reported as Duplicate, "+
			"not rewritten to themselves, got %+v", res)
	}
	if res := ApplyRouting(RoutingBeginMarker("other")+"\n"+
		RoutingEndMarker(), "s", kids); res.Foreign != "pgedge other" {
		t.Errorf("a table declaring another scope must be Foreign, got %+v",
			res)
	}
}

// A routing table must not be counted as a command block.
func TestRoutingIsNotACommandBlock(t *testing.T) {
	if n := BlockCount(Routing("s", kids)); n != 0 {
		t.Errorf("BlockCount counted a routing table as %d block(s)", n)
	}
}

func TestRoutingAtTheRootScope(t *testing.T) {
	if got := RoutingBeginMarker(""); got !=
		"<!-- BEGIN GENERATED ROUTING: pgedge -->" {
		t.Errorf("root marker = %q", got)
	}
	doc := RoutingBeginMarker("") + "\n" + RoutingEndMarker() + "\n"
	res := ApplyRouting(doc, "", kids)
	if res.Foreign != "" || !res.Changed {
		t.Errorf("root table: %+v", res)
	}
}
