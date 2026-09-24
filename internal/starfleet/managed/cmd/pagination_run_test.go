package cmd

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// repeatJSONArray builds a JSON array literal holding n copies of
// item (a single JSON object literal, no surrounding brackets).
//
// byoc has its own copy. Sharing one would mean a third package that
// exists to hold six lines, and testsupport is for the runtime stubs
// every module needs rather than for fixture arithmetic.
func repeatJSONArray(item string, n int) string {
	if n <= 0 {
		return "[]"
	}
	items := make([]string, n)
	for i := range items {
		items[i] = item
	}
	return "[" + strings.Join(items, ",") + "]"
}

const truncationTaskItem = `{"id":"task-1","name":"create-managed",
	"status":"succeeded","subject_id":"db-1","subject_kind":"database",
	"messages":[],"created_at":"2026-08-03T18:22:38Z",
	"updated_at":"2026-08-03T18:24:00Z"}`

// managed's list verbs used to print no truncation hint, so `task list
// --limit 500` returned 100 rows and said nothing — a clamp
// deliberately not pre-empted with a local ceiling. This is the table
// that pins the hint per endpoint, against the default and cap each one
// MEASURABLY applies (see paging.go for the evidence).
func TestManagedListTruncationHint(t *testing.T) {
	cases := []struct {
		name string
		args []string
		item string
		pd   cli.PageDefaults
	}{
		{"task", []string{"task", "list"}, truncationTaskItem,
			taskPageDefaults},
		{"backup", []string{"backup", "list"},
			backupJSON(testBackupID, testDatabaseID), backupPageDefaults},
		{"database", []string{"database", "list"},
			databaseJSON(testDatabaseID, ""), databasePageDefaults},
	}

	for _, tc := range cases {
		// Only for an endpoint that HAS a default page. `database list`
		// has none, and its own sub-test below is the opposite
		// assertion rather than a skip.
		if tc.pd.Def > 0 {
			t.Run(tc.name+"/default page size, truncated", func(t *testing.T) {
				body := repeatJSONArray(tc.item, tc.pd.Def)
				rt, out, errb := testsupport.NewRuntime(t, "", "text")
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(200, body))
				if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s list: %v", tc.name, err)
				}
				want := "Showing first " + strconv.Itoa(tc.pd.Def) +
					" results"
				if !strings.Contains(errb.String(), want) {
					t.Errorf("%s: stderr = %q, want %q",
						tc.name, errb.String(), want)
				}
			})

			t.Run(tc.name+"/default page size, short", func(t *testing.T) {
				body := repeatJSONArray(tc.item, tc.pd.Def-1)
				rt, out, errb := testsupport.NewRuntime(t, "", "text")
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(200, body))
				if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
					t.Fatalf("%s list: %v", tc.name, err)
				}
				if strings.Contains(errb.String(), "Showing first") {
					t.Errorf("%s: unexpected hint: %q",
						tc.name, errb.String())
				}
			})
		}

		t.Run(tc.name+"/explicit limit, truncated", func(t *testing.T) {
			body := repeatJSONArray(tc.item, 3)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			args := append(append([]string{}, tc.args...), "--limit", "3")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s list: %v", tc.name, err)
			}
			if !strings.Contains(errb.String(), "Showing first 3 results") {
				t.Errorf("%s: stderr = %q, want a hint at 3",
					tc.name, errb.String())
			}
		})

		t.Run(tc.name+"/explicit limit, short", func(t *testing.T) {
			body := repeatJSONArray(tc.item, 2)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			args := append(append([]string{}, tc.args...), "--limit", "3")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s list: %v", tc.name, err)
			}
			if strings.Contains(errb.String(), "Showing first") {
				t.Errorf("%s: unexpected hint: %q",
					tc.name, errb.String())
			}
		})

		if tc.pd.Cap > 0 {
			t.Run(tc.name+"/limit above the cap, hint at the cap",
				func(t *testing.T) {
					// The silent clamp, which is the whole reason this
					// hint exists: `task list --limit 500` comes back
					// as 100 rows with nothing else to say so.
					body := repeatJSONArray(tc.item, tc.pd.Cap)
					rt, out, errb := testsupport.NewRuntime(t, "", "text")
					url := testsupport.NewAuthedServer(t,
						testsupport.JSONHandler(200, body))
					// backup list refuses a --limit above its declared
					// maximum locally, so ask for exactly the cap
					// there and for twice it where none is declared.
					ask := tc.pd.Cap * 2
					if tc.name == "backup" {
						ask = tc.pd.Cap
					}
					args := append(append([]string{}, tc.args...),
						"--limit", strconv.Itoa(ask))
					if err := runAuthed(
						t, rt, out, url, args...); err != nil {
						t.Fatalf("%s list: %v", tc.name, err)
					}
					want := "Showing first " +
						strconv.Itoa(tc.pd.Cap) + " results"
					if !strings.Contains(errb.String(), want) {
						t.Errorf("%s: stderr = %q, want %q",
							tc.name, errb.String(), want)
					}
				})
		}

		t.Run(tc.name+"/json mode unaffected", func(t *testing.T) {
			body := repeatJSONArray(tc.item, 25)
			rt, out, errb := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s list json: %v", tc.name, err)
			}
			if errb.String() != "" {
				t.Errorf("%s: json mode wrote to stderr: %q",
					tc.name, errb.String())
			}
		})
	}
}

// `managed database list` applies NO default page — the API passes
// `limit` through unchanged and applies it only when positive, so an
// omitted --limit reads every row. A hint there would claim a complete
// result might be truncated.
//
// This is not a vacuous test and it is not a skip: it is the assertion
// the Def == 0 guard exists for, and it runs a page far larger than
// any other endpoint's cap so that "copy databaseLimitMax across"
// -- the obvious wrong fix -- reddens it.
func TestManagedDatabaseListNeverHintsWithoutALimit(t *testing.T) {
	for _, n := range []int{1, 100, 1000} {
		t.Run(strconv.Itoa(n)+" rows", func(t *testing.T) {
			body := repeatJSONArray(databaseJSON(testDatabaseID, ""), n)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			if err := runAuthed(
				t, rt, out, url, "database", "list"); err != nil {
				t.Fatalf("database list: %v", err)
			}
			if strings.Contains(errb.String(), "Showing first") {
				t.Errorf("hinted at truncation on an unbounded read of "+
					"%d rows: %q\n/managed/v1/databases applies no "+
					"default page, so an unbounded list is the whole "+
					"result.", n, errb.String())
			}
		})
	}
}
