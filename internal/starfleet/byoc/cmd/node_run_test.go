package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNodeListRun(t *testing.T) {
	body := `[{"id":"aaa-111","name":"n1","region":"us-east-1",` +
		`"instance_type":"r7g.medium","ip_address":"10.0.0.1"}]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url,
			"node", "list", testClusterID); err != nil {
			t.Fatalf("node list: %v", err)
		}
		if !strings.Contains(out.String(), "n1") {
			t.Errorf("missing node name: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url,
			"node", "list", testClusterID); err != nil {
			t.Fatalf("node list json: %v", err)
		}
		if !strings.Contains(out.String(), "aaa-111") {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url,
			"node", "list", testClusterID); err != nil {
			t.Fatalf("node list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No nodes found") {
			t.Errorf("want 'No nodes found', got %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url,
			"node", "list", testClusterID); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}
