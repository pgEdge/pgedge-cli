package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestRenderedConfigVersionTablesMatchTheirColumns binds each of
// config-version's three column NAMES to the value rendered beneath
// it.
//
// configVersionColumns and configVersionRow.Columns() are two
// hand-written lists nothing forces to agree, and both readers print
// the same pair, so a swap moves every row of both list and get at
// once. TestConfigVersionListRun cannot see it: it asserts with
// strings.Contains, and "15.6.0", "16, 17, 18" and "postgis, vector"
// are all still present in a table that puts each under the wrong
// header.
//
// cellStarts and sliceCells come from databasecolumns_test.go, which
// records why they slice by the header's offsets rather than splitting
// on padding. Here a padding split would fail on the VALUES: every
// PG VERSIONS cell contains ", ", which strings.Fields turns into two
// tokens for "16, 17" and three for "16, 17, 18".
//
// get is exercised on BOTH fixtures, and that is the whole point of
// the second one. Against 15.6.0 alone the MANAGED EXTENSIONS binding
// asserts "" == "" -- managed_extensions is null on the current 15.x
// line -- so it cannot fail whatever the row adapter does with that
// field. Review landed the escape: inlining configVersionRowFrom at
// the get call site with the extensions field left out compiled,
// passed, and rendered a blank MANAGED EXTENSIONS for a version whose
// server response carried postgis and vector.
func TestRenderedConfigVersionTablesMatchTheirColumns(t *testing.T) {
	// Row values are distinct across the whole table, so a value under
	// the wrong header is unambiguous rather than merely suspicious.
	firstRow := map[string]string{
		"NAME":               "15.6.0",
		"PG VERSIONS":        "16, 17, 18",
		"MANAGED EXTENSIONS": "",
	}
	secondRow := map[string]string{
		"NAME":               "14.1.8",
		"PG VERSIONS":        "16, 17",
		"MANAGED EXTENSIONS": "postgis, vector",
	}
	cases := []struct {
		name    string
		args    []string
		handler http.HandlerFunc
		// want is one map per rendered row, in render order.
		want []map[string]string
	}{
		{"list", []string{"config-version", "list"},
			testsupport.JSONHandler(200, `[`+configVersionBody+`,`+
				configVersionWithExtBody+`]`),
			[]map[string]string{firstRow, secondRow}},
		// The null-extensions render, which nothing else pins.
		{"get", []string{"config-version", "get", "15.6.0"},
			testsupport.JSONHandler(200, configVersionBody),
			[]map[string]string{firstRow}},
		// The populated one, which is what makes get's third binding
		// capable of failing at all.
		{"get with extensions",
			[]string{"config-version", "get", "14.1.8"},
			testsupport.JSONHandler(200, configVersionWithExtBody),
			[]map[string]string{secondRow}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, tc.handler)
			if err := runAuthed(t, rt, out, url,
				tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			lines := strings.Split(
				strings.TrimRight(out.String(), "\n"), "\n")
			if len(lines) != len(tc.want)+1 {
				t.Fatalf("expected %d rows and a header, got %d "+
					"lines:\n%s", len(tc.want), len(lines), out.String())
			}
			starts := cellStarts(lines[0])
			header := sliceCells(lines[0], starts)
			// This compares the render against the same slice the
			// command printed, so it catches a call site passing the
			// wrong column set, and render-layer corruption, but NOT a
			// reorder or a rename of the declaration itself -- both
			// sides move together. The hand-typed want maps below are
			// what catch that.
			if got, want := strings.Join(header, "|"),
				strings.Join(configVersionColumns, "|"); got != want {
				t.Errorf("header = %q, want %q", got, want)
			}
			if len(starts) != len(configVersionColumns) {
				t.Errorf("header has %d columns against %d declared: %q",
					len(starts), len(configVersionColumns), lines[0])
			}
			// EVERY column of EVERY row, because a map covering two of
			// three would pass against a set that had lost the third,
			// and a check reading row 0 only passes when row 1 is row 0
			// printed twice. Both escapes are recorded on the database
			// tables in databasecolumns_test.go.
			for r, want := range tc.want {
				line := lines[r+1]
				cells := sliceCells(line, starts)
				if len(want) != len(configVersionColumns) {
					t.Fatalf("row %d covers %d columns, table has %d",
						r, len(want), len(configVersionColumns))
				}
				for name, value := range want {
					at := -1
					for i, h := range header {
						if h == name {
							at = i
							break
						}
					}
					if at < 0 {
						t.Errorf("no %s column in %q", name, lines[0])
						continue
					}
					if at >= len(cells) {
						t.Errorf("%s column at %d, row %d has %d cells",
							name, at, r, len(cells))
						continue
					}
					if cells[at] != value {
						t.Errorf("row %d under %s carries %q, want %q",
							r, name, cells[at], value)
					}
				}
			}
		})
	}
}

// TestConfigVersionRowMatchesItsColumnSet asserts the two counts
// directly, so the failure names the mismatch rather than reporting it
// as a wrong value in the last column, which is how the render test
// above sees a dropped or extra cell.
func TestConfigVersionRowMatchesItsColumnSet(t *testing.T) {
	if got, want := len(configVersionRow{}.Columns()),
		len(configVersionColumns); got != want {
		t.Errorf("row has %d cells, header set has %d", got, want)
	}
}
