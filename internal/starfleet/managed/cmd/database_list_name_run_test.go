package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// pagedDatabaseServer serves total databases named db0..db<total-1>,
// honouring limit and offset as /managed/v1/databases does: an omitted
// limit returns every remaining row.
func pagedDatabaseServer(t *testing.T, total int) (
	srvURL string, queries func() []url.Values,
) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []url.Values
	)
	srvURL = testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			mu.Lock()
			seen = append(seen, q)
			mu.Unlock()
			offset, _ := strconv.Atoi(q.Get("offset"))
			end := total
			if limit, err := strconv.Atoi(q.Get("limit")); err == nil &&
				offset+limit < total {
				end = offset + limit
			}
			rows := []string{}
			for i := offset; i < end; i++ {
				rows = append(rows, namedDatabaseJSON(i))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
		})
	return srvURL, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), seen...)
	}
}

func namedDatabaseID(i int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
}

func namedDatabaseJSON(i int) string {
	return fmt.Sprintf(`{"id":%q,"name":"db%d","status":"available",`+
		`"region":"us-east-1","size":"small",`+
		`"created_at":"2026-07-31T10:00:00Z",`+
		`"updated_at":"2026-07-31T10:00:00Z"}`, namedDatabaseID(i), i)
}

func TestDatabaseListByName(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total       int
		args        []string
		format      string
		wantOffsets []string
		wantID      string // "" when nothing should match
		wantStderr  string
	}{
		{
			// db1 is a prefix of db10..db19, which must not match.
			name: "match on the first page", total: 20,
			args: []string{"--name", "db1"}, format: "text",
			wantOffsets: []string{"0"}, wantID: namedDatabaseID(1),
			wantStderr: "1 database\n",
		},
		{
			name: "match on a later page", total: 2500,
			args: []string{"--name", "db2100"}, format: "text",
			wantOffsets: []string{"0", "1000", "2000"},
			wantID:      namedDatabaseID(2100),
			wantStderr:  "1 database\n",
		},
		{
			// A full last page is read past: only a short page ends
			// the walk.
			name: "full last page", total: 2000,
			args: []string{"--name", "db1999"}, format: "text",
			wantOffsets: []string{"0", "1000", "2000"},
			wantID:      namedDatabaseID(1999),
			wantStderr:  "1 database\n",
		},
		{
			name: "no match", total: 5,
			args: []string{"--name", "nope"}, format: "text",
			wantOffsets: []string{"0"},
			wantStderr:  "No databases found.\n",
		},
		{
			name: "no match json", total: 5,
			args: []string{"--name", "nope"}, format: "json",
			wantOffsets: []string{"0"},
		},
		{
			name: "match json", total: 1200,
			args: []string{"--name", "db1100"}, format: "json",
			wantOffsets: []string{"0", "1000"},
			wantID:      namedDatabaseID(1100),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errOut := testsupport.NewRuntime(t, "", tc.format)
			srvURL, queries := pagedDatabaseServer(t, tc.total)

			args := append([]string{"database", "list"}, tc.args...)
			if err := runAuthed(t, rt, out, srvURL, args...); err != nil {
				t.Fatalf("list: %v", err)
			}

			var offsets []string
			for _, q := range queries() {
				if got := q.Get("limit"); got != "1000" {
					t.Errorf("limit = %q, want 1000", got)
				}
				offsets = append(offsets, q.Get("offset"))
			}
			if strings.Join(offsets, ",") !=
				strings.Join(tc.wantOffsets, ",") {
				t.Errorf("offsets = %v, want %v", offsets, tc.wantOffsets)
			}

			if tc.format == "json" {
				var got []map[string]any
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("stdout is not a JSON array: %v\n%s",
						err, out.String())
				}
				wantLen := 0
				if tc.wantID != "" {
					wantLen = 1
				} else if s := strings.TrimSpace(out.String()); s != "[]" {
					// null would also unmarshal to an empty slice.
					t.Errorf("stdout = %q, want []", s)
				}
				if len(got) != wantLen {
					t.Fatalf("%d rows, want %d", len(got), wantLen)
				}
				if wantLen == 1 && got[0]["id"] != tc.wantID {
					t.Errorf("id = %v, want %s", got[0]["id"], tc.wantID)
				}
				if errOut.Len() != 0 {
					t.Errorf("stderr = %q, want empty", errOut.String())
				}
				return
			}

			if errOut.String() != tc.wantStderr {
				t.Errorf("stderr = %q, want %q",
					errOut.String(), tc.wantStderr)
			}
			if tc.wantID == "" {
				if out.Len() != 0 {
					t.Errorf("stdout = %q, want empty", out.String())
				}
				return
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 2 || !strings.Contains(lines[1], tc.wantID) {
				t.Errorf("want a header and the one row for %s; got:\n%s",
					tc.wantID, out.String())
			}
		})
	}
}

func TestDatabaseListByNameSendsRegionOnEveryPage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srvURL, queries := pagedDatabaseServer(t, 1500)

	if err := runAuthed(t, rt, out, srvURL, "database", "list",
		"--name", "db1", "--region", "eu-west-1"); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := queries()
	if len(got) != 2 {
		t.Fatalf("%d requests, want 2", len(got))
	}
	for i, q := range got {
		if q.Get("region") != "eu-west-1" {
			t.Errorf("request %d region = %q", i, q.Get("region"))
		}
	}
}

func TestDatabaseListByNameRefusesConflictingFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"with limit", []string{"--name", "db1", "--limit", "5"}},
		{"with offset", []string{"--name", "db1", "--offset", "0"}},
		{"empty name", []string{"--name", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			srvURL, queries := recordingServer(t)

			args := append([]string{"database", "list"}, tc.args...)
			err := runAuthedSilent(t, rt, out, srvURL, args...)
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want %d (%v)", got, ExitUsage, err)
			}
			if n := len(queries()); n != 0 {
				t.Errorf("%d request(s) reached the server", n)
			}
		})
	}
}

// The count line goes to stderr so stdout stays table rows only.
func TestDatabaseListPrintsCountOnStderr(t *testing.T) {
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	srvURL, _ := pagedDatabaseServer(t, 3)

	if err := runAuthed(t, rt, out, srvURL, "database", "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if errOut.String() != "3 databases\n" {
		t.Errorf("stderr = %q, want %q", errOut.String(), "3 databases\n")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Errorf("stdout has %d lines, want a header and 3 rows:\n%s",
			len(lines), out.String())
	}
	if strings.Contains(out.String(), "databases") {
		t.Errorf("count line leaked to stdout:\n%s", out.String())
	}
}
