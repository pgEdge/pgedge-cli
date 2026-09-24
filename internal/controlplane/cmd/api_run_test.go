package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
)

func TestControlplaneAPIPassthrough(t *testing.T) {
	var got *http.Request
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"d1"}]`))
	})
	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, url, "api", "GET", "/v1/databases",
		"--query", "limit=1"); err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/v1/databases" || got.URL.Query().Get("limit") != "1" {
		t.Errorf("request: %s", got.URL)
	}
	if !strings.Contains(out.String(), "\"id\": \"d1\"") {
		t.Errorf("stdout = %q", out.String())
	}

	t.Run("status maps to the module's exit code", func(t *testing.T) {
		url := newServer(t, jsonHandler(404, `{"error":"database not found"}`))
		rt, out, _ := newTestRuntime(t, "", "text")
		err := runControlplane(t, rt, out, url, "api", "GET", "/v1/databases/x")
		var ee *ExitError
		if !errors.As(err, &ee) || ee.code != ExitNotFound {
			t.Errorf("want exit %d, got %v", ExitNotFound, err)
		}
	})
	t.Run("dry run intercepts a write", func(t *testing.T) {
		called := false
		url := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(200)
		})
		rt, out, _ := newTestRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		_ = runControlplane(t, rt, out, url, "api", "POST", "/v1/databases",
			"--data", `{"database_name":"x"}`, "--dry-run")
		if called || rt.DryRun == nil || !rt.DryRun.Intercepted() {
			t.Errorf("called=%v intercepted=%v", called, rt.DryRun != nil && rt.DryRun.Intercepted())
		}
	})
}
