package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestDatabaseRowMatchesItsColumnSet is the gate for the PG VERSION split.
//
// A row carrying one more cell than its header set does not fail: the
// tabwriter renders an unlabelled extra column, which is how a
// mismatch would reach a user rather than a build. So the counts are
// asserted directly, for BOTH readers, because the whole point
// is that one declaration serves two readers whose data differs.
func TestDatabaseRowMatchesItsColumnSet(t *testing.T) {
	d := api.Database{
		Id:        "f6a7b8c9-d0e1-2345-fabc-456789012345",
		Name:      "mydb",
		Status:    "available",
		ClusterId: "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
	}
	cases := []struct {
		name    string
		row     databaseRow
		columns []string
	}{
		{"get", databaseRowFrom(d), databaseGetColumns},
		{"list", databaseListRowFrom(d), databaseListColumns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := len(tc.row.Columns()),
				len(tc.columns); got != want {
				t.Errorf("%s row has %d cells, header set has %d",
					tc.name, got, want)
			}
		})
	}
	// The two sets must differ by exactly the one column, asserted in
	// both directions. Checking only that list omits PG VERSION would
	// pass against a list set that had lost something else as well.
	if len(databaseGetColumns) != len(databaseListColumns)+1 {
		t.Fatalf("get has %d columns, list %d; want exactly one more",
			len(databaseGetColumns), len(databaseListColumns))
	}
	for _, c := range databaseListColumns {
		if c == "PG VERSION" {
			t.Error("list still declares PG VERSION, which the list " +
				"endpoint does not send")
		}
	}
	found := false
	for _, c := range databaseGetColumns {
		if c == "PG VERSION" {
			found = true
		}
	}
	if !found {
		t.Error("get lost PG VERSION, which it is the only reader of")
	}
}

// TestDatabaseListRowOmitsThePGVersionCell pins the CELL rather than
// the header, because the two are separate decisions and only the pair
// renders correctly. A row that kept the cell while the header set
// dropped it would shift CLUSTER and CREATED one column left under a
// correct-looking header row.
func TestDatabaseListRowOmitsThePGVersionCell(t *testing.T) {
	version := "18"
	d := api.Database{
		Id:        "f6a7b8c9-d0e1-2345-fabc-456789012345",
		Name:      "mydb",
		Status:    "available",
		PgVersion: &version,
		ClusterId: "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
	}
	// A version is supplied deliberately. The list endpoint does not
	// send one, so a test relying on it being absent would pass for
	// the wrong reason -- it would be asserting the API's behaviour
	// rather than this command's.
	list := databaseListRowFrom(d).Columns()
	for i, c := range list {
		if c == version {
			t.Errorf("list row cell %d carries the PG version %q: %v",
				i, version, list)
		}
	}
	get := databaseRowFrom(d).Columns()
	if get[3] != version {
		t.Errorf("get row cell 3 = %q, want the PG version %q: %v",
			get[3], version, get)
	}
	// The columns either side of the dropped one must keep their
	// meaning, which is the failure a count check alone cannot see.
	if list[3] != d.ClusterId {
		t.Errorf("list cell 3 = %q, want the cluster id", list[3])
	}
	if get[4] != d.ClusterId {
		t.Errorf("get cell 4 = %q, want the cluster id", get[4])
	}
}

// secondDatabaseID is the second fixture row's id, so a two-row render
// can assert that row 2 is a DIFFERENT database rather than row 1
// printed twice.
const secondDatabaseID = "c3d4e5f6-2222-3333-4444-555566667777"

// cellStarts returns the RUNE offset at which each column begins on a
// rendered header line.
//
// A column starts where a non-space run follows two or more spaces,
// which is the tabwriter's padding (internal/output uses padding 2), so
// a column whose NAME contains one space stays a single column.
//
// RUNES, NOT BYTES, and that is the third defect in this parser rather
// than a precaution. tabwriter aligns by counting runes --
// output/sanitize.go says so where it explains that U+3000 is
// double-width while tabwriter counts runes -- so a byte-indexed
// version split a cell mid-rune the moment a value carried any
// non-ASCII printable. Review measured it on the real render: a
// database named "cafééé" produced cells that were not valid UTF-8,
// and "日本語db" the same. Sanitize escapes non-printables and
// non-ASCII SPACES but admits ordinary printable non-ASCII, and the
// value is whatever the server returns. Nothing
// user-visible, this being test-only, but it made the gate's
// diagnostics wrong and silently so for exactly the values a bug
// report is likeliest to carry.
func cellStarts(header string) []int {
	var starts []int
	runs := 0
	for i, r := range []rune(header) {
		if r == ' ' {
			runs++
			continue
		}
		if i == 0 || runs >= 2 {
			starts = append(starts, i)
		}
		runs = 0
	}
	return starts
}

// sliceCells cuts a rendered line at the offsets the HEADER line
// defines, so a column is read by position rather than by looking for
// content.
//
// Splitting on padding cannot see an EMPTY cell -- three columns whose
// middle one is blank collapse to two, and every index after it shifts.
// That is not hypothetical here: the byoc reference records two
// `failed` databases returning `"pg_version": ""`, and against that
// body a padding split reported `under CLUSTER the row carries
// "2024-03-15"` -- accusing a column that was fine, which is the same
// misleading diagnostic the earlier strings.Fields off-by-one produced,
// arriving from the other direction. Slicing by offset keeps an empty
// cell empty, and it keeps a VALUE containing two spaces from splitting
// as well.
func sliceCells(line string, starts []int) []string {
	rs := []rune(line)
	out := make([]string, 0, len(starts))
	for i, at := range starts {
		if at > len(rs) {
			out = append(out, "")
			continue
		}
		end := len(rs)
		if i+1 < len(starts) && starts[i+1] < end {
			end = starts[i+1]
		}
		out = append(out, strings.TrimSpace(string(rs[at:end])))
	}
	return out
}

// TestRenderedDatabaseTablesMatchTheirColumns is the gate for the
// CALL SITE, which the two tests above cannot see.
//
// They build their own header/row pairs, so both were "gated" only in
// isolation: swapping databaseListColumns for databaseGetColumns at the
// Print call in `database list` — leaving databaseListRowFrom
// untouched — compiled cleanly and passed the entire suite, while
// rendering a table whose CLUSTER id sat under a PG VERSION heading and
// whose CREATED column was empty. Review landed exactly that.
//
// So this renders the real commands through the real Print path and
// reads the header line the user would see. It asserts the cell count
// against the header count on the DATA row too, because a header line
// alone cannot show a misalignment.
func TestRenderedDatabaseTablesMatchTheirColumns(t *testing.T) {
	// TWO rows, and the second carries a NON-ASCII name. Both halves
	// matter, and both were found by mutation.
	//
	// Non-ASCII, because every value in databaseBody is ASCII, so byte
	// and rune offsets coincide and the fixture could not tell the two
	// apart -- reverting the parser to byte indexing passed the whole
	// suite. That parser has now produced a defect on three separate
	// readings, so what holds it in place should be a fixture rather
	// than a comment.
	//
	// Two rows, because the render read lines[1] only: rendering the
	// SAME row twice passed, dropping the second database entirely.
	// That is the mirror of the escape this repo already records --
	// sanitising only cell 0, or only multi-row tables, passed 1225
	// subtests -- on the other axis.
	const secondName = "cafééé"
	second := `{"id":"` + secondDatabaseID + `","name":"` +
		secondName + `","status":"available","pg_version":"17",` +
		`"cluster_id":"` + testClusterID + `",` +
		`"created_at":"2024-04-16T10:30:00Z"}`
	body := `[` + databaseBody + `,` + second + `]`
	cases := []struct {
		name    string
		args    []string
		handler http.HandlerFunc
		columns []string
	}{
		{"list", []string{"database", "list"},
			testsupport.JSONHandler(200, body), databaseListColumns},
		{"get", []string{"database", "get", testDatabaseID},
			testsupport.JSONHandler(200, databaseBody),
			databaseGetColumns},
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
			if len(lines) < 2 {
				t.Fatalf("expected a header and a row:\n%s",
					out.String())
			}
			// list renders both fixtures; get renders one.
			wantRows := 1
			if tc.name == "list" {
				wantRows = 2
			}
			if len(lines) != wantRows+1 {
				t.Fatalf("expected %d rows and a header, got %d "+
					"lines:\n%s", wantRows, len(lines), out.String())
			}
			// Split on the tabwriter's padding, not on whitespace:
			// "PG VERSION" is one column whose name contains a space,
			// and strings.Fields turns it into two tokens that then
			// shift every index after it by one. That off-by-one is
			// the bug a first version of this test had.
			starts := cellStarts(lines[0])
			header := sliceCells(lines[0], starts)
			wantHeader := strings.Join(tc.columns, "|")
			if strings.Join(header, "|") != wantHeader {
				t.Errorf("header = %q, want %q",
					strings.Join(header, "|"), wantHeader)
			}
			// This counts the HEADER's columns, not the row's, and
			// saying so matters: sliceCells always returns one cell
			// per header offset, padding with "" -- so dropping a cell
			// from the row adapter does NOT fire this check, only the
			// value bindings below do. What it still guards is the
			// header having the number of columns the command
			// declares, which is what catches a swapped column set.
			cells := sliceCells(lines[1], starts)
			if len(starts) != len(tc.columns) {
				t.Errorf("header has %d columns against %d declared: %q",
					len(starts), len(tc.columns), lines[0])
			}
			// And the PG VERSION column must be present in exactly one
			// of the two renders, carrying the version the fixture
			// declares.
			hasVersion := strings.Contains(lines[0], "PG VERSION")
			if hasVersion != (tc.name == "get") {
				t.Errorf("%s: PG VERSION present=%v, want %v: %q",
					tc.name, hasVersion, tc.name == "get", lines[0])
			}
			if hasVersion && !strings.Contains(lines[1], "16") {
				t.Errorf("get did not render the version: %q",
					lines[1])
			}
			// The SECOND row must be the second database, read at the
			// same offsets. This is what a one-row fixture could not
			// see: rendering row 0 twice passed everything.
			if tc.name == "list" {
				secondCells := sliceCells(lines[2], starts)
				if len(secondCells) != len(starts) {
					t.Fatalf("second row has %d cells against %d "+
						"columns: %q", len(secondCells), len(starts),
						lines[2])
				}
				for i, h := range header {
					switch h {
					case "ID":
						if secondCells[i] != secondDatabaseID {
							t.Errorf("row 2 under ID: %q",
								secondCells[i])
						}
					case "NAME":
						// The non-ASCII assertion. Byte indexing
						// splits this mid-rune and yields invalid
						// UTF-8.
						if secondCells[i] != secondName {
							t.Errorf("row 2 under NAME: %q, want %q",
								secondCells[i], secondName)
						}
					case "CREATED":
						if secondCells[i] != "2024-04-16" {
							t.Errorf("row 2 under CREATED: %q",
								secondCells[i])
						}
					}
				}
			}
			// Bind each column NAME to the VALUE beneath it. Comparing
			// the header against the same slice the command printed
			// cannot catch a reorder -- both sides move together -- so
			// the header is parsed and specific cells are read by the
			// position their name occupies. Column index and row count
			// is the dimension this repo has been bypassed on before.
			// EVERY column, including the last. A first version bound
			// four of five and review swapped CREATED for STATUS at
			// the row adapter -- rendering "available" under CREATED
			// on every row, on prod -- with the whole suite green. The
			// claim was "binds each column name to the value beneath
			// it", which was true of the columns present in this map
			// and false of the set.
			want := map[string]string{
				"ID":      testDatabaseID,
				"NAME":    "mydb",
				"STATUS":  "available",
				"CLUSTER": testClusterID,
				"CREATED": "2024-03-15",
			}
			if hasVersion {
				want["PG VERSION"] = "16"
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
					t.Errorf("%s column at %d, row has %d cells",
						name, at, len(cells))
					continue
				}
				if cells[at] != value {
					t.Errorf("under %s the row carries %q, want %q",
						name, cells[at], value)
				}
			}
		})
	}
}
