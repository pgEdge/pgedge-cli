package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

func pagedTaskID(i int) string {
	return fmt.Sprintf("%08d-2222-2222-2222-222222222222", i)
}

// pagingServer serves n tasks newest first, honoring limit and an
// exclusive after_task_id as the Control Plane does. ignoreCursor
// replays the first page, as a server that drops the parameter would.
func pagingServer(
	t *testing.T, n int, ignoreCursor bool,
) (url string, cursors *[]string) {
	t.Helper()
	var mu sync.Mutex
	cursors = new([]string)
	url = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		*cursors = append(*cursors, q.Get("after_task_id"))
		mu.Unlock()
		limit, _ := strconv.Atoi(q.Get("limit"))
		start := 1
		if after := q.Get("after_task_id"); after != "" && !ignoreCursor {
			for i := 1; i <= n; i++ {
				if pagedTaskID(i) == after {
					start = i + 1
				}
			}
		}
		var rows []string
		for i := start; i <= n && (limit == 0 || len(rows) < limit); i++ {
			rows = append(rows, `{"task_id":"`+pagedTaskID(i)+`",`+
				`"status":"completed","type":"create","scope":"database",`+
				`"entity_id":"db1","created_at":"2025-06-18T00:00:00Z"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tasks":[`+strings.Join(rows, ",")+`]}`)
	})
	return url, cursors
}

func TestTaskListAllFollowsTheCursor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks int
		// wantCursors is the after_task_id of each request, by task.
		wantCursors []int
	}{
		{"short last page", 5, []int{0, 2, 4}},
		{"exact multiple ends on an empty page", 4, []int{0, 2, 4}},
		{"one short page", 1, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "json")
			url, cursors := pagingServer(t, tc.tasks, false)
			if err := runControlplane(t, rt, out, url, "task", "list",
				"--database", "db1", "--all", "--limit", "2"); err != nil {
				t.Fatalf("task list --all: %v", err)
			}
			var doc struct {
				Tasks []struct {
					TaskID string `json:"task_id"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("stdout: %v (%q)", err, out.String())
			}
			if len(doc.Tasks) != tc.tasks {
				t.Errorf("tasks = %d, want %d", len(doc.Tasks), tc.tasks)
			}
			want := make([]string, 0, len(tc.wantCursors))
			for _, c := range tc.wantCursors {
				if c == 0 {
					want = append(want, "")
					continue
				}
				want = append(want, pagedTaskID(c))
			}
			if strings.Join(*cursors, ",") != strings.Join(want, ",") {
				t.Errorf("cursors = %v, want %v", *cursors, want)
			}
		})
	}
}

func TestTaskListAllRefusals(t *testing.T) {
	t.Run("server ignores the cursor", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url, _ := pagingServer(t, 5, true)
		err := runControlplane(t, rt, out, url, "task", "list",
			"--host", "h1", "--all", "--limit", "2")
		if got := cli.ExitCode(err); got != ExitGeneral {
			t.Fatalf("exit = %d (%v), want %d", got, err, ExitGeneral)
		}
	})
	for _, args := range [][]string{
		{"--all"},
		{"--all", "--scope", "database"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url, cursors := pagingServer(t, 5, false)
			err := runControlplane(t, rt, out, url,
				append([]string{"task", "list"}, args...)...)
			requireUsageError(t, err)
			if len(*cursors) != 0 {
				t.Errorf("a refused --all sent %d requests", len(*cursors))
			}
		})
	}
}

func TestTaskListWithoutAllReadsOnePage(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url, cursors := pagingServer(t, 5, false)
	if err := runControlplane(t, rt, out, url, "task", "list",
		"--database", "db1", "--limit", "2"); err != nil {
		t.Fatalf("task list: %v", err)
	}
	if len(*cursors) != 1 {
		t.Errorf("requests = %d, want 1", len(*cursors))
	}
}
