package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testStoreID = "22222222-3333-4444-5555-666677778888"

const storeBody = `{"id":"` + testStoreID + `","name":"store1",` +
	`"status":"active","cloud_account_id":"` + testClusterID + `",` +
	`"cloud_account_type":"aws","created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

func TestBackupStoreListRun(t *testing.T) {
	body := `[` + storeBody + `]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "list"); err != nil {
			t.Fatalf("store list: %v", err)
		}
		if !strings.Contains(out.String(), "store1") {
			t.Errorf("missing store name: %q", out.String())
		}
	})

	t.Run("filters json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "backup-store", "list",
			"--limit", "20", "--offset", "5",
			"--created-after", "2024-01-01T00:00:00Z",
			"--created-before", "2025-01-01T00:00:00Z"); err != nil {
			t.Fatalf("store list filters: %v", err)
		}
	})

	t.Run("invalid created-before", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "backup-store", "list",
			"--created-before", "nope"); err == nil {
			t.Fatal("expected error on invalid timestamp")
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "list"); err != nil {
			t.Fatalf("store list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No backup stores found") {
			t.Errorf("want empty message, got %q", errb.String())
		}
	})
}

func TestBackupStoreGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, storeBody))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "get", testStoreID); err != nil {
			t.Fatalf("store get: %v", err)
		}
		if !strings.Contains(out.String(), "store1") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, storeBody))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "get", "bad"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})
}

func TestBackupStoreCreateRun(t *testing.T) {
	args := []string{"backup-store", "create", "--name", "store1",
		"--cloud-account-id", testClusterID, "--region", "us-east-1"}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, storeBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("store create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, storeBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("store create json: %v", err)
		}
	})

	t.Run("invalid cloud account id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, storeBody))
		if err := runAuthed(t, rt, out, url, "backup-store", "create",
			"--name", "s", "--cloud-account-id", "bad"); err == nil {
			t.Fatal("expected error on invalid cloud account id")
		}
	})
}

func TestBackupStoreDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "delete", testStoreID, "--force"); err != nil {
			t.Fatalf("store delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"backup-store", "delete", testStoreID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}
