package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func taskBody(status string) string {
	errField := ""
	if status == "failed" {
		errField = `"error":"node unreachable",`
	}
	return `{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"deploy-cluster","status":"` + status +
		`",` + errField + `"subject_id":"` + testClusterID +
		`","subject_kind":"cluster","messages":[],` +
		`"created_at":"2024-03-15T10:30:00Z",` +
		`"updated_at":"2024-03-15T10:30:00Z"}`
}

func TestTaskListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[`+taskBody("running")+`]`))
		if err := runAuthed(t, rt, out, url, "task", "list",
			"--subject-id", testClusterID, "--subject-kind", "cluster",
			"--status", "running"); err != nil {
			t.Fatalf("task list: %v", err)
		}
		if !strings.Contains(out.String(), "7db8f401-0d8d-4daf-889b-f721e395df61") {
			t.Errorf("missing task id: %q", out.String())
		}
	})

	// byoc had NO query assertion at all, so a flag could ship
	// declared but never reaching the wire — which is how `--name`
	// would have arrived here. The value carries a hyphen, so a
	// plain Contains works: only the separators get percent-encoded.
	t.Run("filters reach the query", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotQuery string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tasks") {
					gotQuery = r.URL.RawQuery
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[` + taskBody("running") + `]`))
			})
		if err := runAuthed(t, rt, out, url, "task", "list",
			"--subject-id", testClusterID, "--subject-kind", "cluster",
			"--status", "running", "--name", "deploy-cluster",
			"--limit", "20", "--offset", "5"); err != nil {
			t.Fatalf("task list filters: %v", err)
		}
		for _, want := range []string{
			"subject_id=" + testClusterID, "subject_kind=cluster",
			"status=running", "name=deploy-cluster",
			"limit=20", "offset=5",
		} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q missing %q", gotQuery, want)
			}
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url, "task", "list"); err != nil {
			t.Fatalf("task list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, "task", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestTaskGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("succeeded")+`]`))
		if err := runAuthed(t, rt, out, url,
			"task", "get", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task get: %v", err)
		}
		if !strings.Contains(out.String(), "7db8f401-0d8d-4daf-889b-f721e395df61") {
			t.Errorf("missing task id: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("succeeded")+`]`))
		if err := runAuthed(t, rt, out, url,
			"task", "get", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task get json: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"task", "get", "7db8f401-0d8d-4daf-889b-f721e395df61"); err == nil {
			t.Fatal("expected not-found error")
		}
	})
}

func TestTaskWaitRun(t *testing.T) {
	t.Run("succeeded", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("succeeded")+`]`))
		if err := runAuthed(t, rt, out, url,
			"task", "wait", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task wait: %v", err)
		}
		if !strings.Contains(errb.String(), "succeeded") {
			t.Errorf("missing succeeded message: %q", errb.String())
		}
	})

	t.Run("succeeded json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("succeeded")+`]`))
		if err := runAuthed(t, rt, out, url,
			"task", "wait", "7db8f401-0d8d-4daf-889b-f721e395df61"); err != nil {
			t.Fatalf("task wait json: %v", err)
		}
	})

	t.Run("failed", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+taskBody("failed")+`]`))
		err := runAuthed(t, rt, out, url, "task", "wait", "7db8f401-0d8d-4daf-889b-f721e395df61")
		if err == nil {
			t.Fatal("expected error for failed task")
		}
		if !strings.Contains(err.Error(), "node unreachable") {
			t.Errorf("missing error detail: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"task", "wait", "7db8f401-0d8d-4daf-889b-f721e395df61"); err == nil {
			t.Fatal("expected not-found error")
		}
	})
}
