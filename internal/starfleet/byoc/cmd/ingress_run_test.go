package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testIngressID = "44444444-5555-6666-7777-888899990000"

const ingressBody = `{"id":"` + testIngressID + `","name":"web",` +
	`"status":"active","cluster_id":"` + testClusterID + `",` +
	`"cloud_account_id":"` + testAccountID + `","region":"us-east-1",` +
	`"created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

func TestIngressListRun(t *testing.T) {
	body := `[` + ingressBody + `]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "ingress", "list"); err != nil {
			t.Fatalf("ingress list: %v", err)
		}
		if !strings.Contains(out.String(), "web") {
			t.Errorf("missing ingress name: %q", out.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url, "ingress", "list"); err != nil {
			t.Fatalf("ingress list json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, "ingress", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestIngressGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, ingressBody))
		if err := runAuthed(t, rt, out, url,
			"ingress", "get", testIngressID); err != nil {
			t.Fatalf("ingress get: %v", err)
		}
		if !strings.Contains(out.String(), "web") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "get", testIngressID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestIngressCreateRun(t *testing.T) {
	args := []string{"ingress", "create", "--name", "web",
		"--cluster-id", testClusterID, "--region", "us-east-1"}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, ingressBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("ingress create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, ingressBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("ingress create json: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestIngressDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "delete", testIngressID, "--force"); err != nil {
			t.Fatalf("ingress delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "delete", testIngressID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

func TestIngressServiceRun(t *testing.T) {
	svcRegBody := `{"service_id":"svc-1","url":"https://web.example.com",` +
		`"database_id":"` + testDatabaseID + `","hosts":[]}`

	t.Run("list text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[`+svcRegBody+`]`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "service", "list", testIngressID); err != nil {
			t.Fatalf("ingress service list: %v", err)
		}
		if !strings.Contains(out.String(), "svc-1") {
			t.Errorf("missing service id: %q", out.String())
		}
	})

	t.Run("list empty", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"ingress", "service", "list", testIngressID); err != nil {
			t.Fatalf("ingress service list empty: %v", err)
		}
	})

	t.Run("register text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, svcRegBody))
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"register", testIngressID, "--database-id", testDatabaseID,
			"--service-id", "svc-1"); err != nil {
			t.Fatalf("ingress service register: %v", err)
		}
		if !strings.Contains(errb.String(), "registered") {
			t.Errorf("missing registered message: %q", errb.String())
		}
	})

	t.Run("register invalid database id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, svcRegBody))
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"register", testIngressID, "--database-id", "bad",
			"--service-id", "svc-1"); err == nil {
			t.Fatal("expected error on invalid database id")
		}
	})

	t.Run("deregister force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"deregister", testIngressID, "svc-1", "--force"); err != nil {
			t.Fatalf("ingress service deregister: %v", err)
		}
		if !strings.Contains(errb.String(), "deregistered") {
			t.Errorf("missing deregistered message: %q", errb.String())
		}
	})

	t.Run("deregister no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "ingress", "service",
			"deregister", testIngressID, "svc-1"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}
