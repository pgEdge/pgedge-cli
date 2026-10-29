package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// TestPreReadFailuresKeepTheirCodeAndWriteNothing covers the reads the
// new flags add before a write or a render: each failure must keep the
// exit code its status maps to, and no write may follow it.
func TestPreReadFailuresKeepTheirCodeAndWriteNothing(t *testing.T) {
	notFound := `{"name":"not_found","message":"no such thing"}`
	for _, tc := range []struct {
		name string
		// failPath is the request path answered with failStatus; every
		// other GET succeeds with okBody.
		failPath   string
		failStatus int
		okBody     string
		args       []string
		want       int
	}{
		{"join --from: member has no token", "/v1/cluster/join-token",
			404, "", []string{"cluster", "join", "--from", "MEMBER"},
			ExitNotFound},
		{"join --wait: host snapshot fails", "/v1/hosts", 500, "",
			[]string{"cluster", "join", "--token", "x",
				"--server-url", "MEMBER", "--wait"},
			ExitGeneral},
		{"instance stop: database missing", "/v1/databases/storefront",
			404, "", []string{"database", "instance", "stop",
				"storefront", "n1", "--force"},
			ExitNotFound},
		{"get --failure: task list fails",
			"/v1/databases/storefront/tasks", 500, databaseBody,
			[]string{"database", "get", "storefront", "--failure"},
			ExitGeneral},
		{"task list --all: host missing", "/v1/hosts/h1/tasks", 404, "",
			[]string{"task", "list", "--host", "h1", "--all"},
			ExitNotFound},
		{"task list --all: scoped entity", "/v1/tasks", 404, "",
			[]string{"task", "list", "--scope", "host",
				"--entity-id", "h1", "--all"},
			ExitNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var writes []string
			url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					mu.Lock()
					writes = append(writes, r.URL.Path)
					mu.Unlock()
					return
				}
				if r.URL.Path == tc.failPath {
					w.WriteHeader(tc.failStatus)
					_, _ = io.WriteString(w, notFound)
					return
				}
				_, _ = io.WriteString(w, tc.okBody)
			})
			args := make([]string, len(tc.args))
			for i, a := range tc.args {
				args[i] = strings.ReplaceAll(a, "MEMBER", url)
			}
			rt, out, _ := newTestRuntime(t, "", "text")
			err := runControlplane(t, rt, out, url, args...)
			if got := cli.ExitCode(err); got != tc.want {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.want)
			}
			if len(writes) != 0 {
				t.Errorf("a write followed the failed read: %v", writes)
			}
		})
	}

	t.Run("join --from: member unreachable", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url, body := joinTarget(t, 200, "")
		err := runControlplane(t, rt, out, url, "cluster", "join",
			"--from", deadServerURL(t))
		if got := cli.ExitCode(err); got != ExitGeneral {
			t.Fatalf("exit = %d (%v), want %d", got, err, ExitGeneral)
		}
		if *body != "" {
			t.Errorf("join sent after the token read failed: %q", *body)
		}
	})
}
