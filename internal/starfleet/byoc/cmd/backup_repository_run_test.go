package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testBackupRepoID = "11111111-2222-4333-8444-555555555503"

const backupRepoBody = `{"id":"` + testBackupRepoID + `",` +
	`"database_id":"27937dd4-e554-4d38-80f8-56f83559f60a",` +
	`"type":"s3","s3_bucket":"derick-backups","s3_region":"eu-central-1",` +
	`"retention_full":30,"retention_full_type":"time",` +
	`"created_at":"2024-06-19T19:58:03Z"}`

const backupRepoInfoBody = `{"backup_repository_id":"` +
	testBackupRepoID + `",` +
	`"database_id":"27937dd4-e554-4d38-80f8-56f83559f60a",` +
	`"node_name":"n1","status":"ok","pg_version":"16",` +
	`"backups":[{"label":"20240619-195803F","type":"full",` +
	`"backup_size":1048576,"database_size":2097152,` +
	`"created_at":"2024-06-19T19:58:03Z",` +
	`"finished_at":"2024-06-19T20:01:12Z"}]}`

func TestBackupRepositoryListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+backupRepoBody+`]`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "list"); err != nil {
			t.Fatalf("backup-repository list: %v", err)
		}
		if !strings.Contains(out.String(), "derick-backups") {
			t.Errorf("missing bucket: %q", out.String())
		}
		if !strings.Contains(out.String(), "s3") {
			t.Errorf("missing type: %q", out.String())
		}
	})

	// backupRepositoryDefaults records this endpoint's page size and cap,
	// with the API evidence. Assert the flags actually reach the query
	// string rather than being parsed and dropped.
	t.Run("filter flags reach the query string", func(t *testing.T) {
		var got string
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[` + backupRepoBody + `]`))
			})
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "list",
			"--limit", "100", "--offset", "10",
			"--type", "s3",
			"--database-id", "27937dd4-e554-4d38-80f8-56f83559f60a",
			"--descending"); err != nil {
			t.Fatalf("backup-repository list filtered: %v", err)
		}
		for _, want := range []string{
			"limit=100", "offset=10", "type=s3",
			"database_id=27937dd4-e554-4d38-80f8-56f83559f60a",
			"descending=true",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("query %q missing %q", got, want)
			}
		}
	})

	t.Run("empty text reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "list"); err != nil {
			t.Fatalf("backup-repository list empty: %v", err)
		}
		if !strings.Contains(errb.String(),
			"No backup repositories found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "list"); err != nil {
			t.Fatalf("backup-repository list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestBackupRepositoryGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, backupRepoInfoBody))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get: %v", err)
		}
		if !strings.Contains(out.String(), "20240619-195803F") {
			t.Errorf("missing backup label: %q", out.String())
		}
		if !strings.Contains(out.String(), "1.0 MiB") {
			t.Errorf("missing humanised backup size: %q", out.String())
		}
		if !strings.Contains(errb.String(), "n1") {
			t.Errorf("missing node summary: %q", errb.String())
		}
	})

	t.Run("no backups reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200,
			`{"backup_repository_id":"`+testBackupRepoID+`",`+
				`"database_id":"27937dd4-e554-4d38-80f8-56f83559f60a",`+
				`"node_name":"n1","backups":[]}`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No backups found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, backupRepoInfoBody))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1"); err != nil {
			t.Fatalf("backup-repository get json: %v", err)
		}
		if !strings.Contains(out.String(), "20240619-195803F") {
			t.Errorf("json output should carry backups: %q", out.String())
		}
	})

	t.Run("backup filter flags reach the query string", func(t *testing.T) {
		var got string
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(backupRepoInfoBody))
			})
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1",
			"--limit", "50", "--offset", "5",
			"--type", "full", "--descending"); err != nil {
			t.Fatalf("backup-repository get filtered: %v", err)
		}
		for _, want := range []string{
			"backup_limit=50", "backup_offset=5",
			"backup_type=full", "descending=true",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("query %q missing %q", got, want)
			}
		}
	})

	t.Run("rejects an unknown backup type", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, backupRepoInfoBody))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1",
			"--type", "sideways"); err == nil {
			t.Fatal("expected error on unknown backup type")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, backupRepoInfoBody))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", "bad", "n1"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	// The endpoint contacts pgBackRest live, so an unreachable
	// repository answers 500 rather than an empty 200. Confirmed on
	// a BYOC dev tenant 2026-08-03.
	t.Run("unreachable repository", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500,
			`{"code":500,"message":"failed to connect to backup repository"}`))
		if err := runAuthed(t, rt, out, url,
			"backup-repository", "get", testBackupRepoID, "n1"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}
