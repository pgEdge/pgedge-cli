package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
)

// twoNodeDatabase is the database the stub serves: nodes n1 and n2.
const twoNodeDatabase = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z","updated_at":"2025-06-18T00:00:00Z",` +
	`"spec":{"database_name":"storefront","nodes":[` +
	`{"name":"n1","host_ids":["host-1"]},` +
	`{"name":"n2","host_ids":["host-2"]}]}}`

const specKeepsBoth = `database_name: storefront
nodes:
  - name: n1
    host_ids: [host-1]
  - name: n2
    host_ids: [host-2]
`

const specDropsN2 = `database_name: storefront
nodes:
  - name: n1
    host_ids: [host-1]
`

// updateStub serves getStatus/getBody on GET of the database and
// counts every POST, which is the update.
func updateStub(t *testing.T, getStatus int, getBody string,
	posts *atomic.Int32,
) string {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			posts.Add(1)
			_, _ = io.WriteString(w, updateResp)
		case r.URL.Path == "/v1/databases/storefront":
			w.WriteHeader(getStatus)
			_, _ = io.WriteString(w, getBody)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	})
}

func TestDatabaseUpdateConfirmsNodeRemoval(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		getStatus int
		getBody   string
		force     bool
		dryRun    bool
		wantCode  int // 0 is success
		wantPosts int32
		wantNote  bool
	}{
		{name: "keeps every node", spec: specKeepsBoth,
			getStatus: 200, getBody: twoNodeDatabase, wantPosts: 1},
		{name: "drops a node off a terminal", spec: specDropsN2,
			getStatus: 200, getBody: twoNodeDatabase,
			wantCode: ExitUsage, wantNote: true},
		{name: "drops a node with --force", spec: specDropsN2,
			getStatus: 200, getBody: twoNodeDatabase, force: true,
			wantPosts: 1, wantNote: true},
		{name: "database not found", spec: specDropsN2,
			getStatus: 404, getBody: `{"name":"not_found","message":"no such database"}`,
			wantCode: ExitNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			url := updateStub(t, tc.getStatus, tc.getBody, &posts)
			rt, out, errb := newTestRuntime(t, "", "text")
			args := []string{"database", "update", "storefront",
				"-f", writeSpec(t, tc.spec)}
			if tc.force {
				args = append(args, "--force")
			}
			err := runControlplane(t, rt, out, url, args...)

			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Fatalf("exit code = %d, want %d: %v", got, tc.wantCode, err)
			}
			if got := posts.Load(); got != tc.wantPosts {
				t.Errorf("update POSTs = %d, want %d", got, tc.wantPosts)
			}
			note := strings.Contains(errb.String(),
				"The spec removes node n2 from database storefront")
			if note != tc.wantNote {
				t.Errorf("removal note printed = %v, want %v; stderr:\n%s",
					note, tc.wantNote, errb.String())
			}
		})
	}
}

// Under --dry-run the read still runs, so the ledger records the
// confirmation the real run would ask for, and the write is stopped.
func TestDatabaseUpdateDryRunRecordsNodeRemoval(t *testing.T) {
	var posts atomic.Int32
	url := updateStub(t, 200, twoNodeDatabase, &posts)
	rt, out, _ := newTestRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	// The write is intercepted, so an error here is the abort.
	_ = runControlplane(t, rt, out, url, "database", "update",
		"storefront", "-f", writeSpec(t, specDropsN2))

	if got := posts.Load(); got != 0 {
		t.Errorf("update POSTs = %d, want 0 under --dry-run", got)
	}
	joined := strings.Join(rt.DryRun.Checks(), "\n")
	if !strings.Contains(joined, "would require confirmation") {
		t.Errorf("ledger missing the confirmation:\n%s", joined)
	}
	if !rt.DryRun.Intercepted() {
		t.Error("the update write was not intercepted")
	}
}

func TestNodeList(t *testing.T) {
	if got := nodeList([]string{"n2"}); got != "node n2" {
		t.Errorf("one = %q", got)
	}
	if got := nodeList([]string{"n2", "n3"}); got != "nodes n2, n3" {
		t.Errorf("two = %q", got)
	}
}
