package cmd

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// repeatJSONArray builds a JSON array literal holding n copies of
// item (a single JSON object literal, no surrounding brackets).
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

// TestListPaginationFlags exercises the --limit / --offset (and, where
// present, filter) branches of the list commands, which the default
// list tests leave unset.
func TestListPaginationFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"cluster", []string{"cluster", "list", "--limit", "10", "--offset", "5"}},
		{"database", []string{"database", "list", "--limit", "10", "--offset", "5"}},
		{"backup-store",
			[]string{"backup-store", "list", "--limit", "10", "--offset", "5"}},
		{"task", []string{"task", "list", "--limit", "10", "--offset", "5"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s list paginated: %v", tc.name, err)
			}
		})
	}
}

// TestListTruncationHint exercises printTruncationHint through the
// list commands that share it, against each endpoint's own verified
// default/cap (see pagination.go's cli.PageDefaults table and its
// evidence). None of the byoc list endpoints report a total,
// so the hint fires purely on "result count >= min(effective limit,
// server cap)" — this table pins that a full default page, a full
// explicit --limit page, and a page landing on the server's hard cap
// (when one is known) all print the advisory with the right count,
// and a short page of either kind prints nothing.
func TestListTruncationHint(t *testing.T) {
	cases := []struct {
		name string
		args []string
		item string
		pd   cli.PageDefaults
	}{
		{"backup-store", []string{"backup-store", "list"}, storeBody,
			backupStoreDefaults},
		{"cluster", []string{"cluster", "list"}, clusterBody,
			clusterDefaults},
		{"database", []string{"database", "list"}, databaseBody,
			databaseDefaults},
		{"ingress", []string{"ingress", "list"}, ingressBody,
			ingressDefaults},
		{"backup-repository",
			[]string{"backup-repository", "list"}, backupRepoBody,
			backupRepositoryDefaults},
		{"task", []string{"task", "list"}, taskBody("running"),
			taskDefaults},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/default page size, truncated", func(t *testing.T) {
			body := repeatJSONArray(tc.item, tc.pd.Def)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s list: %v", tc.name, err)
			}
			want := "Showing first " + strconv.Itoa(tc.pd.Def) + " results"
			if !strings.Contains(errb.String(), want) {
				t.Errorf("%s: stderr = %q, want it to contain %q",
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
				t.Errorf("%s: unexpected hint in stderr: %q",
					tc.name, errb.String())
			}
		})

		t.Run(tc.name+"/explicit limit, truncated", func(t *testing.T) {
			body := repeatJSONArray(tc.item, 3)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, body))
			args := append(append([]string{}, tc.args...), "--limit", "3")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("%s list: %v", tc.name, err)
			}
			want := "Showing first 3 results"
			if !strings.Contains(errb.String(), want) {
				t.Errorf("%s: stderr = %q, want it to contain %q",
					tc.name, errb.String(), want)
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
				t.Errorf("%s: unexpected hint in stderr: %q",
					tc.name, errb.String())
			}
		})

		if tc.pd.Cap > 0 {
			t.Run(tc.name+"/limit above server cap, hint at the cap",
				func(t *testing.T) {
					// The server clamps --limit to tc.pd.Cap, so asking
					// for more than the cap and getting exactly the cap
					// back (as the API does for task: --limit 200 -> 100
					// rows) must still trip the hint.
					body := repeatJSONArray(tc.item, tc.pd.Cap)
					rt, out, errb := testsupport.NewRuntime(t, "", "text")
					url := testsupport.NewAuthedServer(t,
						testsupport.JSONHandler(200, body))
					args := append(append([]string{}, tc.args...),
						"--limit", strconv.Itoa(tc.pd.Cap*2))
					if err := runAuthed(t, rt, out, url, args...); err != nil {
						t.Fatalf("%s list: %v", tc.name, err)
					}
					want := "Showing first " + strconv.Itoa(tc.pd.Cap) + " results"
					if !strings.Contains(errb.String(), want) {
						t.Errorf("%s: stderr = %q, want it to contain %q",
							tc.name, errb.String(), want)
					}
				})
		}

		t.Run(tc.name+"/json mode unaffected", func(t *testing.T) {
			body := repeatJSONArray(tc.item, tc.pd.Def)
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
			if !strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
				t.Errorf("%s: json stdout not an array: %q",
					tc.name, out.String())
			}
		})
	}
}

// TestBackupStoreListJSONByteIdentical pins that JSON-mode output for
// a full page is byte-for-byte what the renderer would produce from
// the decoded response alone — the truncation hint's code path must
// be structurally unreachable from -o json, not merely quiet in this
// one case.
func TestBackupStoreListJSONByteIdentical(t *testing.T) {
	body := repeatJSONArray(storeBody, backupStoreDefaults.Def)

	var stores []api.BackupStore
	if err := json.Unmarshal([]byte(body), &stores); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(&stores); err != nil {
		t.Fatalf("encode expected: %v", err)
	}

	rt, out, errb := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
	if err := runAuthed(t, rt, out, url, "backup-store", "list"); err != nil {
		t.Fatalf("backup-store list json: %v", err)
	}

	if out.String() != want.String() {
		t.Errorf("json stdout mismatch:\ngot:  %q\nwant: %q",
			out.String(), want.String())
	}
	if errb.String() != "" {
		t.Errorf("json mode wrote to stderr: %q", errb.String())
	}
}

// TestBackupRepositoryGetTruncationHint covers get separately: its
// backups come nested inside the response body, not as a bare array,
// so it does not fit TestListTruncationHint's table shape. get's
// backupInfoDefaults has no known server cap (see pagination.go), so
// there is no cap sub-case here.
func TestBackupRepositoryGetTruncationHint(t *testing.T) {
	backup := `{"label":"20240619-195803F","type":"full",` +
		`"backup_size":1048576,"database_size":2097152,` +
		`"created_at":"2024-06-19T19:58:03Z",` +
		`"finished_at":"2024-06-19T20:01:12Z"}`
	infoWith := func(n int) string {
		return `{"backup_repository_id":"` + testBackupRepoID + `",` +
			`"database_id":"27937dd4-e554-4d38-80f8-56f83559f60a",` +
			`"node_name":"n1","status":"ok","pg_version":"16",` +
			`"backups":` + repeatJSONArray(backup, n) + `}`
	}

	t.Run("default page size, truncated", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, infoWith(backupInfoDefaults.Def)))
		if err := runAuthed(t, rt, out, url, "backup-repository", "get",
			testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get: %v", err)
		}
		want := "Showing first " + strconv.Itoa(backupInfoDefaults.Def) + " backups"
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q",
				errb.String(), want)
		}
	})

	t.Run("default page size, short", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, infoWith(backupInfoDefaults.Def-1)))
		if err := runAuthed(t, rt, out, url, "backup-repository", "get",
			testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get: %v", err)
		}
		if strings.Contains(errb.String(), "Showing first") {
			t.Errorf("unexpected hint in stderr: %q", errb.String())
		}
	})

	t.Run("explicit limit, truncated", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, infoWith(3)))
		if err := runAuthed(t, rt, out, url, "backup-repository", "get",
			testBackupRepoID, "n1", "--limit", "3"); err != nil {
			t.Fatalf("backup-repository get: %v", err)
		}
		want := "Showing first 3 backups"
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q",
				errb.String(), want)
		}
	})

	t.Run("explicit limit, short", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, infoWith(2)))
		if err := runAuthed(t, rt, out, url, "backup-repository", "get",
			testBackupRepoID, "n1", "--limit", "3"); err != nil {
			t.Fatalf("backup-repository get: %v", err)
		}
		if strings.Contains(errb.String(), "Showing first") {
			t.Errorf("unexpected hint in stderr: %q", errb.String())
		}
	})

	t.Run("json mode unaffected", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, infoWith(backupInfoDefaults.Def)))
		if err := runAuthed(t, rt, out, url, "backup-repository", "get",
			testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get json: %v", err)
		}
		if errb.String() != "" {
			t.Errorf("json mode wrote to stderr: %q", errb.String())
		}
	})
}
