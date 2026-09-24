package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testShareID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

const shareBody = `{"id":"` + testShareID + `","name":"team-a",` +
	`"status":"active","tenancy":"same","capacity":2,` +
	`"allowed_tenants":[],"created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

func TestClusterShareListRun(t *testing.T) {
	body := `[` + shareBody + `]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url,
			"cluster", "share", "list", testClusterID); err != nil {
			t.Fatalf("share list: %v", err)
		}
		if !strings.Contains(out.String(), "team-a") {
			t.Errorf("missing share name: %q", out.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "share", "list", testClusterID); err != nil {
			t.Fatalf("share list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "share", "list", testClusterID); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestClusterShareGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, shareBody))
		if err := runAuthed(t, rt, out, url,
			"cluster", "share", "get", testClusterID, testShareID); err != nil {
			t.Fatalf("share get: %v", err)
		}
		if !strings.Contains(out.String(), "team-a") {
			t.Errorf("missing share name: %q", out.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "share", "get", testClusterID, testShareID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestClusterShareCreateRun(t *testing.T) {
	args := []string{"cluster", "share", "create", testClusterID,
		"--name", "team-a", "--capacity", "2", "--tenancy", "allowlist",
		"--allowed-tenants", "tnt-1,tnt-2"}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, shareBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("share create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, shareBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("share create json: %v", err)
		}
		if !strings.Contains(out.String(), testShareID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestClusterShareDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"delete", testClusterID, testShareID, "--force"); err != nil {
			t.Fatalf("share delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"delete", testClusterID, testShareID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}
