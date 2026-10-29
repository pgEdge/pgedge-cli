package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// databaseWithInstances is databaseDetailBody's shape, with node n1 on
// two hosts so a node name can be ambiguous.
const databaseWithInstances = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z","instances":[` +
	`{"id":"storefront-n1-aaaa","host_id":"host-a","node_name":"n1",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"},` +
	`{"id":"storefront-n1-bbbb","host_id":"host-b","node_name":"n1",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"},` +
	`{"id":"storefront-n2-cccc","host_id":"host-c","node_name":"n2",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"}]}`

// instanceServer answers `database get` with databaseWithInstances and
// every write with an accepted task, recording each write's path and
// body.
func instanceServer(t *testing.T) (url string, gets *int, writes *[]capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	gets, writes = new(int), new([]capturedRequest)
	url = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet &&
			r.URL.Path == "/v1/databases/storefront" {
			*gets++
			_, _ = io.WriteString(w, databaseWithInstances)
			return
		}
		*writes = append(*writes, capturedRequest{
			method: r.Method, path: r.URL.Path, body: string(b)})
		_, _ = io.WriteString(w, instTaskResp("op"))
	})
	return url, gets, writes
}

func TestInstanceArgResolvesANodeName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantGets int
		wantCode int
		// wantSent appears in the write's path or body.
		wantSent string
		wantMsg  []string
	}{
		{"instance ID is used as typed",
			[]string{"database", "instance", "stop", "storefront",
				"storefront-n1-aaaa", "--force"},
			0, ExitOK, "/instances/storefront-n1-aaaa/", nil},
		{"unique node resolves",
			[]string{"database", "instance", "restart", "storefront",
				"n2", "--force"},
			1, ExitOK, "/instances/storefront-n2-cccc/", nil},
		{"ambiguous node lists every candidate",
			[]string{"database", "instance", "start", "storefront", "n1"},
			1, ExitUsage, "",
			[]string{"storefront-n1-aaaa", "storefront-n1-bbbb"}},
		{"unknown node is not found",
			[]string{"database", "instance", "stop", "storefront", "n9",
				"--force"},
			1, ExitNotFound, "", []string{"n9"}},
		// The server refuses a candidate from another node, so a node
		// name there is never resolved.
		{"candidate is sent as typed",
			[]string{"database", "node", "switchover", "storefront", "n1",
				"--candidate", "n2", "--force"},
			0, ExitOK, `"candidate_instance_id":"n2"`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url, gets, writes := instanceServer(t)
			err := runControlplane(t, rt, out, url, tc.args...)
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
			if *gets != tc.wantGets {
				t.Errorf("database get calls = %d, want %d",
					*gets, tc.wantGets)
			}
			for _, m := range tc.wantMsg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("error %q does not name %q", err, m)
				}
			}
			if tc.wantCode != ExitOK {
				if len(*writes) != 0 {
					t.Errorf("a refused call wrote: %+v", *writes)
				}
				return
			}
			if len(*writes) != 1 {
				t.Fatalf("writes = %d, want 1", len(*writes))
			}
			sent := (*writes)[0].path + " " + (*writes)[0].body
			if !strings.Contains(sent, tc.wantSent) {
				t.Errorf("sent %q, want it to carry %q", sent, tc.wantSent)
			}
		})
	}
}
