package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// rotateArgs is the minimal valid invocation, confirmation skipped.
func rotateArgs(role string) []string {
	return []string{"database", "rotate-password", testDatabaseID,
		"--role", role, "--force"}
}

// TestDatabaseRotatePasswordRunAcceptsEitherSuccessShape is the whole
// reason this verb needs the empty-body bypass rather than a typed
// call. byoc and managed disagree about what success looks like:
// managed answers 204 with no body, byoc answers 200 carrying the
// JSON literal `null` (RespondOK(ctx, nil) against a spec declaring
// no content).
//
// A test written against managed's 204 alone passes there and would
// have shipped a byoc verb that reports success as failure, so both
// shapes are asserted here on purpose.
func TestDatabaseRotatePasswordRunAcceptsEitherSuccessShape(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"byoc: 200 with the JSON literal null", 200, `null`},
		{"managed: 204 with no body", 204, ``},
		{"200 with an empty body", 200, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(tc.status, tc.body))
			if err := runAuthed(t, rt, out, url,
				rotateArgs("app")...); err != nil {
				t.Fatalf("rotate-password: %v", err)
			}
			if !strings.Contains(errb.String(), "rotated") {
				t.Errorf("missing rotated message: %q", errb.String())
			}
		})
	}
}

func TestDatabaseRotatePasswordRunRequest(t *testing.T) {
	// The role belongs in the path, not a query parameter or body.
	t.Run("role reaches the request path", func(t *testing.T) {
		var gotPath, gotMethod string
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod = r.URL.Path, r.Method
				w.WriteHeader(http.StatusNoContent)
			})
		if err := runAuthed(t, rt, out, url,
			rotateArgs("app_read_only")...); err != nil {
			t.Fatalf("rotate-password: %v", err)
		}
		want := "/byoc/v1/databases/" + testDatabaseID +
			"/roles/app_read_only/rotate-password"
		if gotPath != want {
			t.Errorf("path = %q, want %q", gotPath, want)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
	})

	t.Run("every built-in role is accepted", func(t *testing.T) {
		for _, role := range []string{
			"admin", "app", "app_read_only",
			"application", "application_read_only",
		} {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(204, ``))
			if err := runAuthed(t, rt, out, url,
				rotateArgs(role)...); err != nil {
				t.Errorf("role %q: %v", role, err)
			}
		}
	})
}

func TestDatabaseRotatePasswordRunRejects(t *testing.T) {
	// An unknown role must be caught before spending a round trip.
	t.Run("unknown role never reaches the server", func(t *testing.T) {
		called := false
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
		if err := runAuthed(t, rt, out, url,
			rotateArgs("superuser")...); err == nil {
			t.Fatal("expected error on unknown role")
		}
		if called {
			t.Error("unknown role should not reach the server")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(204, ``))
		if err := runAuthed(t, rt, out, url,
			"database", "rotate-password", testDatabaseID,
			"--role", "app"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})

	t.Run("missing role flag", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(204, ``))
		if err := runAuthed(t, rt, out, url,
			"database", "rotate-password", testDatabaseID,
			"--force"); err == nil {
			t.Fatal("expected error without --role")
		}
	})

	// The API rejects rotation unless both database and cluster are
	// available; that arrives as a 400 and must not read as success.
	t.Run("unavailable database surfaces the 400", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400,
			`{"code":400,"message":"database update cannot be completed.`+
				` database is not in available status"}`))
		err := runAuthed(t, rt, out, url, rotateArgs("app")...)
		if err == nil {
			t.Fatal("expected error on 400")
		}
		if !strings.Contains(err.Error(), "available status") {
			t.Errorf("error should carry the API message: %v", err)
		}
	})
}
