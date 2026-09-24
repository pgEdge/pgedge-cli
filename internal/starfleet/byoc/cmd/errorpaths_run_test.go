package cmd

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// firstThenErr returns 200+okBody for the first request and errStatus
// for every subsequent one, so tests can fail the second API call a
// multi-step command makes.
func firstThenErr(okBody string, errStatus int) http.HandlerFunc {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(okBody))
			return
		}
		w.WriteHeader(errStatus)
		_, _ = w.Write([]byte(`err`))
	}
}

func TestClusterUpdateErrorPaths(t *testing.T) {
	t.Run("bad firewall rule", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--firewall-rule", "port=notint"); err == nil {
			t.Fatal("expected firewall parse error")
		}
	})

	t.Run("update call fails", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, firstThenErr(clusterBody, 500))
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "eu-west-1"); err == nil {
			t.Fatal("expected update error")
		}
	})

	t.Run("cluster not found body", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		// 202 -> checkResponse passes but JSON200 is nil.
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(202, `{}`))
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "eu-west-1"); err == nil {
			t.Fatal("expected not-found error")
		}
	})
}

func TestServiceApplyErrorPaths(t *testing.T) {
	nodes := `[{"id":"host-1","name":"n1","region":"us-east-1",` +
		`"instance_type":"r7g.medium","ip_address":"10.0.0.1"}]`

	t.Run("mcp update call fails", func(t *testing.T) {
		var mu sync.Mutex
		n := 0
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/nodes"):
				_, _ = w.Write([]byte(nodes))
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(dbWithServiceBody))
			default:
				mu.Lock()
				n++
				mu.Unlock()
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`err`))
			}
		}
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, handler)
		if err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID); err == nil {
			t.Fatal("expected mcp update error")
		}
	})

	t.Run("rag node lookup fails", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/nodes") {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`err`))
				return
			}
			_, _ = w.Write([]byte(dbWithServiceBody))
		}
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, handler)
		err := runAuthed(t, rt, out, url, "database", "rag", "update",
			testDatabaseID, "--top-n", "5")
		if err == nil {
			t.Fatal("expected node lookup error")
		}
	})

	t.Run("mcp bad cluster id on database", func(t *testing.T) {
		badDB := `{"id":"` + testDatabaseID + `","name":"mydb",` +
			`"status":"available","cluster_id":"not-a-uuid",` +
			`"created_at":"2024-03-15T10:30:00Z"}`
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, badDB))
		if err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID); err == nil {
			t.Fatal("expected bad cluster id error")
		}
	})
}

func TestDatabaseServiceRemoveErrorPath(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, firstThenErr(dbWithServiceBody, 500))
	if err := runAuthed(t, rt, out, url, "database", "service",
		"remove", testDatabaseID, "mcp", "--force"); err == nil {
		t.Fatal("expected remove update error")
	}
}

func TestClusterShareArgErrors(t *testing.T) {
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, shareBody))

	t.Run("get bad cluster id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"get", "bad", testShareID); err == nil {
			t.Fatal("expected bad cluster id error")
		}
	})

	t.Run("get bad share id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"get", testClusterID, "bad"); err == nil {
			t.Fatal("expected bad share id error")
		}
	})

	t.Run("delete bad share id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"delete", testClusterID, "bad", "--force"); err == nil {
			t.Fatal("expected bad share id error")
		}
	})

	t.Run("list bad cluster id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "cluster", "share",
			"list", "bad"); err == nil {
			t.Fatal("expected bad cluster id error")
		}
	})
}

func TestIngressServiceArgErrors(t *testing.T) {
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))

	t.Run("list bad ingress id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"list", "bad"); err == nil {
			t.Fatal("expected bad ingress id error")
		}
	})

	t.Run("register bad ingress id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"register", "bad", "--database-id", testDatabaseID,
			"--service-id", "svc-1"); err == nil {
			t.Fatal("expected bad ingress id error")
		}
	})

	t.Run("deregister bad ingress id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"deregister", "bad", "svc-1", "--force"); err == nil {
			t.Fatal("expected bad ingress id error")
		}
	})
}

func TestTaskWaitProgressThenSucceed(t *testing.T) {
	var mu sync.Mutex
	n := 0
	handler := func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		status := "queued"
		if n > 1 {
			status = "succeeded"
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","status":"` + status +
			`","messages":[],"subject_id":"x","subject_kind":"cluster",` +
			`"created_at":"2026-06-25T02:00:00Z"}]`))
	}
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, handler)
	if err := runAuthed(t, rt, out, url, "task", "wait", "7db8f401-0d8d-4daf-889b-f721e395df61",
		"--wait-interval", "1", "--wait-timeout", "30"); err != nil {
		t.Fatalf("task wait: %v", err)
	}
	if !strings.Contains(errb.String(), "queued") {
		t.Errorf("expected queued progress line: %q", errb.String())
	}
}
