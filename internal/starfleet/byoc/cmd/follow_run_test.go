package cmd

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// followFlowHandler simulates a mutation whose task accumulates step
// messages across polls: the first by-id lookup answers a running
// task with one message, later lookups the terminal task with three.
// A --follow that cursors correctly prints each exactly once.
func followFlowHandler(terminalStatus string) http.HandlerFunc {
	var mu sync.Mutex
	mutated := false
	byIDCalls := 0

	const early = `[{"level":"info","progress":5,"status":"running",
		"step":"stop-monitoring","text":"Stop Monitoring",
		"time":"2026-08-05T20:49:50Z"}]`
	const full = `[
		{"level":"info","progress":5,"status":"running",
		 "step":"stop-monitoring","text":"Stop Monitoring",
		 "time":"2026-08-05T20:49:50Z"},
		{"level":"info","progress":15,"status":"succeeded",
		 "step":"stop-monitoring","text":"Stop Monitoring",
		 "time":"2026-08-05T20:49:51Z"},
		{"level":"info","progress":65,"status":"running",
		 "step":"remove-nodes","text":"Removing Nodes",
		 "time":"2026-08-05T20:49:52Z"}]`

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/tasks"):
			if r.URL.Query().Get("id") != "" {
				mu.Lock()
				byIDCalls++
				n := byIDCalls
				mu.Unlock()
				if n == 1 {
					_, _ = w.Write([]byte(`[{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
						`"status":"running","messages":` + early +
						`,"created_at":"2026-06-25T02:00:00Z"}]`))
				} else {
					_, _ = w.Write([]byte(`[{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
						`"status":"` + terminalStatus +
						`","messages":` + full +
						`,"created_at":"2026-06-25T02:00:00Z"}]`))
				}
				return
			}
			mu.Lock()
			done := mutated
			mu.Unlock()
			if done {
				_, _ = w.Write([]byte(`[{"id":"e9562e00-c8f8-438b-860a-b5eb427f88d8",` +
					`"subject_id":"x","subject_kind":"cluster",` +
					`"created_at":"2026-06-25T02:00:00Z"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		case r.Method != http.MethodGet:
			mu.Lock()
			mutated = true
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}
}

// TestClusterDeleteFollowRun pins byoc's --follow contract, mirroring
// the managed test: step messages stream to stderr exactly once each,
// the bare status polls are replaced, and the terminal state keeps
// --wait's exit behaviour.
func TestClusterDeleteFollowRun(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, followFlowHandler("succeeded"))

	if err := runAuthed(t, rt, out, url, "cluster", "delete",
		testClusterID, "--force", "--follow", "--wait-interval", "1",
		"--wait-timeout", "30"); err != nil {
		t.Fatalf("cluster delete --follow: %v", err)
	}

	s := errb.String()
	for _, want := range []string{
		"Stop Monitoring: running (5%)",
		"Stop Monitoring: succeeded (15%)",
		"Removing Nodes: running (65%)",
		"Task e9562e00-c8f8-438b-860a-b5eb427f88d8: succeeded.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr is missing %q:\n%s", want, s)
		}
	}
	if n := strings.Count(s, "Stop Monitoring: running (5%)"); n != 1 {
		t.Errorf("first message printed %d times, want exactly 1:\n%s",
			n, s)
	}
	if strings.Contains(s, "Task e9562e00-c8f8-438b-860a-b5eb427f88d8: running...") {
		t.Errorf("--follow printed --wait's status polls too:\n%s", s)
	}
}
