package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestAPIPassthroughSendsTheAuthenticatedRequest(t *testing.T) {
	var got *http.Request
	var gotBody string
	url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out, url,
		"api", "post", "/byoc/v1/clusters", "--query", "limit=2",
		"--data", `{"name":"x"}`, "-H", "X-Trace: 1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL.Path != "/byoc/v1/clusters" ||
		got.URL.Query().Get("limit") != "2" || gotBody != `{"name":"x"}` {
		t.Errorf("request: %s %s body=%q", got.Method, got.URL, gotBody)
	}
	if !strings.HasPrefix(got.Header.Get("Authorization"), "Bearer ") ||
		got.Header.Get("X-Trace") != "1" {
		t.Errorf("headers: %v", got.Header)
	}
	if strings.TrimSpace(out.String()) != `{"ok":true}` {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestAPIPassthroughKeepsTheExitContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   int
	}{
		{"not found", 404, `{"code":"not_found","message":"cluster not found"}`, conn.ExitNotFound},
		{"unauthorized", 401, `{"code":"unauthorized","message":"bad token"}`, conn.ExitAuth},
		{"server error", 500, `{"code":"internal","message":"boom"}`, conn.ExitGeneral},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(tc.status, tc.body))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out, url,
				"api", "GET", "/byoc/v1/clusters/x")
			var ee *conn.ExitError
			if err == nil {
				t.Fatal("no error")
			}
			if c, ok := err.(interface{ Code() int }); !ok || c.Code() != tc.code {
				t.Errorf("want exit %d, got %v (%T %v)", tc.code, err, err, ee)
			}
		})
	}
}

func TestAPIPassthroughRefusesBeforeConnecting(t *testing.T) {
	called := false
	url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	for _, args := range [][]string{
		{"api", "FETCH", "/x"},
		{"api", "GET", "https://evil.example/x"},
		{"api", "GET", "/x", "-H", "Authorization: Bearer stolen"},
		{"api", "GET", "/x", "--data", "{}"},
	} {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out, url, args...)
		if c, ok := err.(interface{ Code() int }); !ok || c.Code() != conn.ExitUsage {
			t.Errorf("%v: want exit 2, got %v", args, err)
		}
	}
	if called {
		t.Error("a refused invocation reached the server")
	}
}

func TestAPIPassthroughDryRunInterceptsAWrite(t *testing.T) {
	called := false
	url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out, url,
		"api", "DELETE", "/byoc/v1/clusters/x", "--dry-run")
	if called {
		t.Fatal("dry run sent the request")
	}
	if rt.DryRun == nil || !rt.DryRun.Intercepted() {
		t.Fatalf("dry run did not intercept: err=%v", err)
	}
	if req := rt.DryRun.Request(); req == nil || req.Method != "DELETE" {
		t.Errorf("recorded request = %+v", req)
	}
}
