package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testMembershipID = "66666666-7777-8888-9999-0000aaaabbbb"

const membershipBody = `{"id":"` + testMembershipID + `",` +
	`"user_name":"Alice","user_email":"alice@example.com",` +
	`"user_id":"u-1","is_owner":true,` +
	`"created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

// membershipBodyNotOwner is the other side of is_owner, so the OWNER
// column is exercised in both directions rather than assumed.
const membershipBodyNotOwner = `{"id":"77777777-8888-9999-aaaa-bbbbccccdddd",` +
	`"user_name":"Bob","user_email":"bob@example.com",` +
	`"user_id":"u-2","is_owner":false,` +
	`"created_at":"2024-03-16T10:30:00Z",` +
	`"updated_at":"2024-03-16T10:30:00Z"}`

func TestMembershipListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotPath string
		url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testsupport.JSONHandler(200, `[`+membershipBody+`]`)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list: %v", err)
		}
		if !strings.Contains(out.String(), "alice@example.com") {
			t.Errorf("missing email: %q", out.String())
		}
		if gotPath != "/account/v1/memberships" {
			t.Errorf("request path = %q, want /account/v1/memberships", gotPath)
		}
	})

	// is_owner renders as an OWNER column, yes/no like every other
	// boolean column;
	// -o json carries the raw boolean through from the generated type.
	t.Run("owner column", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			200, `[`+membershipBody+`,`+membershipBodyNotOwner+`]`))
		if err := runAuthedAccount(
			t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list: %v", err)
		}
		if !strings.Contains(out.String(), "OWNER") {
			t.Errorf("no OWNER column: %q", out.String())
		}
		for _, want := range []string{"Alice", "yes", "Bob", "no"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q: %q", want, out.String())
			}
		}
	})

	// The CELL POSITIONS, not just the presence of the values. A
	// swap of owner and created in
	// Columns() passes a presence check: both cells are still
	// somewhere in the output. This walks the rendered row and pins
	// each cell to its header's index in membershipColumns, so a row
	// adapter that drifts out of step with the column list reddens.
	t.Run("cell order matches membershipColumns", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			200, `[`+membershipBody+`]`))
		if err := runAuthedAccount(
			t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list: %v", err)
		}

		lines := strings.Split(
			strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("want a header and one row, got %d lines: %q",
				len(lines), out.String())
		}

		// The header is matched against membershipColumns rather than
		// against a literal, so renaming a column cannot silently
		// leave this test asserting the old name. "USER NAME" carries
		// a space, so the header cannot be split on whitespace --
		// each header's byte offset is located instead, and the row's
		// cell is read from the same offset.
		header, row := lines[0], lines[1]
		offsets := make([]int, len(membershipColumns))
		at := 0
		for i, col := range membershipColumns {
			j := strings.Index(header[at:], col)
			if j < 0 {
				t.Fatalf("header %q does not carry %q at or after "+
					"column %d", header, col, at)
			}
			offsets[i] = at + j
			at = offsets[i] + len(col)
		}

		wantCells := map[string]string{
			"ID":         testMembershipID,
			"USER NAME":  "Alice",
			"USER EMAIL": "alice@example.com",
			"OWNER":      "yes",
			"CREATED":    "2024-03-15",
		}
		for i, col := range membershipColumns {
			want, ok := wantCells[col]
			if !ok {
				t.Fatalf("membershipColumns carries %q, which this "+
					"test has no expected cell for; add one rather "+
					"than deleting the column from the check", col)
			}
			if offsets[i] >= len(row) {
				t.Errorf("row %q is too short to carry %q at %d",
					row, col, offsets[i])
				continue
			}
			if got := strings.Fields(row[offsets[i]:]); len(got) == 0 ||
				got[0] != want {
				t.Errorf("column %q (offset %d) holds %q, want %q; "+
					"row=%q", col, offsets[i], got, want, row)
			}
		}
	})

	t.Run("owner in json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			200, `[`+membershipBody+`,`+membershipBodyNotOwner+`]`))
		if err := runAuthedAccount(
			t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list json: %v", err)
		}
		for _, want := range []string{
			`"is_owner":true`, `"is_owner":false`,
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("json missing %s: %q", want, out.String())
			}
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list json: %v", err)
		}
	})

	// text empty covers the "No memberships found" branch in text mode,
	// distinct from "empty json" above.
	t.Run("text empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url, "membership", "list"); err != nil {
			t.Fatalf("membership list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No memberships found") {
			t.Errorf("missing empty-list message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url, "membership", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestMembershipDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"membership", "delete", testMembershipID, "--force"); err != nil {
			t.Fatalf("membership delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"membership", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"membership", "delete", testMembershipID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}
