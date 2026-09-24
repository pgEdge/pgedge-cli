package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testDatabaseID = "b2c3d4e5-1111-2222-3333-444455556666"

const databaseBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z"}`

func TestDatabaseListRun(t *testing.T) {
	body := `[` + databaseBody + `]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "database", "list"); err != nil {
			t.Fatalf("database list: %v", err)
		}
		if !strings.Contains(out.String(), "mydb") {
			t.Errorf("missing db name: %q", out.String())
		}
	})

	t.Run("filter by cluster json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "database", "list",
			"--cluster-id", testClusterID); err != nil {
			t.Fatalf("database list filter: %v", err)
		}
		if !strings.Contains(out.String(), testDatabaseID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("invalid cluster id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "database", "list",
			"--cluster-id", "not-a-uuid"); err == nil {
			t.Fatal("expected error on invalid cluster id")
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url, "database", "list"); err != nil {
			t.Fatalf("database list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No databases found") {
			t.Errorf("want empty message, got %q", errb.String())
		}
	})
}

func TestDatabaseGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url,
			"database", "get", testDatabaseID); err != nil {
			t.Fatalf("database get: %v", err)
		}
		if !strings.Contains(out.String(), "mydb") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"database", "get", testDatabaseID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestDatabaseCreateRun(t *testing.T) {
	args := []string{"database", "create", "--name", "mydb",
		"--cluster-id", testClusterID, "--pg-version", "16"}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("database create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("database create json: %v", err)
		}
		if !strings.Contains(out.String(), testDatabaseID) {
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

// TestDatabaseCreateRejectsAnEmptyPgVersion is byoc's half of the
// empty-value class. byoc publishes no enum for pg_version — its spec
// declares a bare string, and the supported set lives in the
// config-version catalog — so an empty value is the only thing that CAN
// be checked locally here. It is worth checking for the same reason as
// on managed: `database update` has no pg_version, so the version is
// fixed at create, and `--pg-version "$PGV"` with the variable unset
// took the API's default silently.
func TestDatabaseCreateRejectsAnEmptyPgVersion(t *testing.T) {
	called := 0
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			called++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseBody))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--cluster-id", testClusterID,
		"--pg-version", "")
	if err == nil {
		t.Fatal("an empty --pg-version was accepted; a database would " +
			"have been created on a version nobody chose")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("want ExitUsage, got %v", err)
	}
	if called != 0 {
		t.Errorf("an empty --pg-version still reached the API "+
			"(%d calls)", called)
	}
	for _, want := range []string{
		"--pg-version", "empty value", "omit the flag",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
}

// TestDatabaseCreateOmitsPgVersionWhenUnset is the control: an omitted
// flag must still send no pg_version, so the API applies its own
// default. Without it, rejecting "" could be "fixed" by making the flag
// mandatory — and unlike managed, byoc has no local list to fall back
// on if it were.
func TestDatabaseCreateOmitsPgVersionWhenUnset(t *testing.T) {
	var body string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			buf, _ := io.ReadAll(r.Body)
			body = string(buf)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseBody))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--cluster-id", testClusterID); err != nil {
		t.Fatalf("create without --pg-version: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("request body was not JSON: %v (%q)", err, body)
	}
	if _, ok := got["pg_version"]; ok {
		t.Errorf("pg_version %v sent with the flag unset; the API's "+
			"own default is what an omitted flag must get",
			got["pg_version"])
	}
}

func TestDatabaseUpdateRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url, "database", "update",
			testDatabaseID, "--display-name", "My DB",
			"--options", "a,b"); err != nil {
			t.Fatalf("database update: %v", err)
		}
		if !strings.Contains(errb.String(), "updated") {
			t.Errorf("missing updated message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url, "database", "update",
			testDatabaseID, "--display-name", "My DB"); err != nil {
			t.Fatalf("database update json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, "database", "update",
			testDatabaseID, "--display-name", "x"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestDatabaseDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"database", "delete", testDatabaseID, "--force"); err != nil {
			t.Fatalf("database delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"database", "delete", testDatabaseID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}
