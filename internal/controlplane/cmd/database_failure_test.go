package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const failedTaskID = "55555555-5555-5555-5555-555555555555"

// failureServer answers `database get` with a failed database and the
// task list with tasksBody, recording the task list's query.
func failureServer(
	t *testing.T, tasksBody string,
) (url string, query *string) {
	t.Helper()
	query = new(string)
	url = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/tasks") {
			*query = r.URL.RawQuery
			_, _ = io.WriteString(w, tasksBody)
			return
		}
		_, _ = io.WriteString(w, `{"id":"storefront","state":"failed",`+
			`"created_at":"2025-06-18T00:00:00Z",`+
			`"updated_at":"2025-06-18T00:00:00Z"}`)
	})
	return url, query
}

func oneTask(status, errMsg string) string {
	e := ""
	if errMsg != "" {
		e = `"error":"` + errMsg + `",`
	}
	return `{"tasks":[{"task_id":"` + failedTaskID + `",` + e +
		`"status":"` + status + `","type":"create","scope":"database",` +
		`"entity_id":"storefront","created_at":"2025-06-18T00:00:00Z"}]}`
}

func TestDatabaseGetFailure(t *testing.T) {
	t.Run("text shows the latest task's error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url, query := failureServer(t,
			oneTask("failed", "port 5432 already allocated"))
		if err := runControlplane(t, rt, out, url, "database", "get",
			"storefront", "--failure"); err != nil {
			t.Fatalf("get --failure: %v", err)
		}
		if *query != "limit=1" {
			t.Errorf("task list query = %q, want limit=1", *query)
		}
		for _, want := range []string{"failed", failedTaskID,
			"Error\nport 5432 already allocated"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("stdout lacks %q: %q", want, out.String())
			}
		}
	})

	t.Run("json is one object", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url, _ := failureServer(t, oneTask("failed", "boom"))
		if err := runControlplane(t, rt, out, url, "database", "get",
			"storefront", "--failure"); err != nil {
			t.Fatalf("get --failure: %v", err)
		}
		var doc struct {
			DatabaseID string `json:"database_id"`
			State      string `json:"state"`
			Task       struct {
				TaskID string `json:"task_id"`
				Error  string `json:"error"`
			} `json:"task"`
		}
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatalf("stdout: %v (%q)", err, out.String())
		}
		if doc.DatabaseID != "storefront" || doc.State != "failed" ||
			doc.Task.TaskID != failedTaskID || doc.Task.Error != "boom" {
			t.Errorf("doc = %+v", doc)
		}
	})

	for _, tc := range []struct{ name, body, want string }{
		{"latest task did not fail", oneTask("completed", ""),
			"latest task " + failedTaskID + " is completed"},
		{"no tasks", `{"tasks":[]}`, "has no tasks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "json")
			url, _ := failureServer(t, tc.body)
			if err := runControlplane(t, rt, out, url, "database", "get",
				"storefront", "--failure"); err != nil {
				t.Fatalf("get --failure: %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty", out.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("stderr %q lacks %q", errb.String(), tc.want)
			}
		})
	}
}
