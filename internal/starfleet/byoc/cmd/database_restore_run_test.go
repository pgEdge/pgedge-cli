package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// restoreArgs is the minimal valid invocation, confirmation skipped.
func restoreArgs(extra ...string) []string {
	base := []string{"database", "restore", testDatabaseID,
		"--node-name", "n1", "--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d", "--force"}
	return append(base, extra...)
}

// captureRestoreBody runs a restore against a stub that records the
// request body, and returns the decoded body.
func captureRestoreBody(
	t *testing.T, args []string,
) map[string]any {
	t.Helper()
	var raw []byte
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`null`))
		})
	if err := runAuthed(t, rt, out, url, args...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	return body
}

// The handler answers RespondOK(ctx, nil) against a spec declaring no
// content, so success arrives as 200 carrying the JSON literal `null`
// — the same shape as rotate-password, and the same reason this verb
// needs the empty-body bypass.
func TestDatabaseRestoreRunAcceptsNullBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"200 with the JSON literal null", 200, `null`},
		{"200 with an empty body", 200, ``},
		{"204 with no body", 204, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(tc.status, tc.body))
			if err := runAuthed(t, rt, out, url,
				restoreArgs()...); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if !strings.Contains(errb.String(), "estore") {
				t.Errorf("missing restore message: %q", errb.String())
			}
		})
	}
}

func TestDatabaseRestoreRunBody(t *testing.T) {
	t.Run("required fields", func(t *testing.T) {
		body := captureRestoreBody(t, restoreArgs())
		if body["provider"] != "pgbackrest" {
			t.Errorf("provider = %v, want pgbackrest", body["provider"])
		}
		cfg, ok := body["restore_config"].(map[string]any)
		if !ok {
			t.Fatalf("restore_config missing: %v", body)
		}
		if cfg["node_name"] != "n1" {
			t.Errorf("node_name = %v, want n1", cfg["node_name"])
		}
		repos, _ := cfg["repositories"].([]any)
		if len(repos) != 1 || repos[0] != "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d" {
			t.Errorf("repositories = %v, want one canonical UUID",
				cfg["repositories"])
		}
	})

	t.Run("repeatable repository and target-nodes", func(t *testing.T) {
		body := captureRestoreBody(t, restoreArgs(
			"--repository", "1f2e3d4c-5b6a-4798-8765-43210fedcba9",
			"--target-nodes", "n2", "--target-nodes", "n3"))
		cfg := body["restore_config"].(map[string]any)
		repos, _ := cfg["repositories"].([]any)
		if len(repos) != 2 {
			t.Errorf("repositories = %v, want 2 entries", repos)
		}
		nodes, _ := body["target_nodes"].([]any)
		if len(nodes) != 2 || nodes[0] != "n2" || nodes[1] != "n3" {
			t.Errorf("target_nodes = %v, want [n2 n3]", body["target_nodes"])
		}
	})

	// The convergence's whole point: a comma splits. Under the old
	// StringArray parser this exact invocation sent ONE element
	// "n2,n3" — reverting the parser while keeping the flag name
	// passes every other test in the tree, so this is the one assertion
	// standing behind the Slice.
	t.Run("comma-separated target-nodes split", func(t *testing.T) {
		body := captureRestoreBody(t, restoreArgs(
			"--target-nodes", "n2,n3"))
		nodes, _ := body["target_nodes"].([]any)
		if len(nodes) != 2 || nodes[0] != "n2" || nodes[1] != "n3" {
			t.Errorf("target_nodes = %v, want [n2 n3] from one "+
				"comma-separated value", body["target_nodes"])
		}
	})

	// restore_command is all-optional: omitting every one of its flags
	// must leave the key out entirely rather than sending an empty
	// object.
	t.Run("restore_command omitted when unset", func(t *testing.T) {
		body := captureRestoreBody(t, restoreArgs())
		if _, present := body["restore_command"]; present {
			t.Errorf("restore_command should be absent: %v", body)
		}
		if _, present := body["target_nodes"]; present {
			t.Errorf("target_nodes should be absent: %v", body)
		}
	})

	// The pgBackRest restore command has its own `force`, which would
	// collide with the repo-wide destructive-confirm --force. It is
	// exposed as --pgbackrest-force, and the two must stay independent:
	// --force alone must NOT set restore_command.force.
	t.Run("confirm force does not set restore_command.force",
		func(t *testing.T) {
			body := captureRestoreBody(t, restoreArgs())
			if _, present := body["restore_command"]; present {
				t.Errorf("--force must not populate restore_command: %v",
					body)
			}
		})

	t.Run("pgbackrest-force sets restore_command.force",
		func(t *testing.T) {
			body := captureRestoreBody(t,
				restoreArgs("--pgbackrest-force"))
			cmd, ok := body["restore_command"].(map[string]any)
			if !ok {
				t.Fatalf("restore_command missing: %v", body)
			}
			if cmd["force"] != true {
				t.Errorf("restore_command.force = %v, want true",
					cmd["force"])
			}
		})

	t.Run("remaining restore_command options", func(t *testing.T) {
		body := captureRestoreBody(t, restoreArgs(
			"--delta", "--set", "20240619-195803F",
			"--target", "2024-06-19T20:00:00Z", "--target-exclusive",
			"--type", "time"))
		cmd, ok := body["restore_command"].(map[string]any)
		if !ok {
			t.Fatalf("restore_command missing: %v", body)
		}
		for k, want := range map[string]any{
			"delta":            true,
			"set":              "20240619-195803F",
			"target":           "2024-06-19T20:00:00Z",
			"target_exclusive": true,
			"type":             "time",
		} {
			if cmd[k] != want {
				t.Errorf("restore_command.%s = %v, want %v", k, cmd[k], want)
			}
		}
	})
}

func TestDatabaseRestoreRunRejects(t *testing.T) {
	// Both restore_config fields are required by the API; catching
	// them here saves a round trip and gives a better message.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing node-name", []string{"database", "restore",
			testDatabaseID, "--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d", "--force"}},
		{"missing repository", []string{"database", "restore",
			testDatabaseID, "--node-name", "n1", "--force"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				})
			if err := runAuthed(t, rt, out, url, tc.args...); err == nil {
				t.Fatal("expected error")
			}
			if called {
				t.Error("incomplete input should not reach the server")
			}
		})
	}

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `null`))
		if err := runAuthed(t, rt, out, url,
			"database", "restore", testDatabaseID,
			"--node-name", "n1", "--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `null`))
		if err := runAuthed(t, rt, out, url,
			"database", "restore", "bad", "--node-name", "n1",
			"--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	// The API refuses a restore unless the database is modifiable.
	t.Run("unmodifiable database surfaces the 400", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400,
			`{"code":400,"message":"database in unmodifiable state: failed"}`))
		err := runAuthed(t, rt, out, url, restoreArgs()...)
		if err == nil {
			t.Fatal("expected error on 400")
		}
		if !strings.Contains(err.Error(), "unmodifiable") {
			t.Errorf("error should carry the API message: %v", err)
		}
	})
}
