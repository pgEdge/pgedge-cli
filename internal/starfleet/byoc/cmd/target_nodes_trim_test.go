package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// pflag's StringSlice parses its value with csv.Reader and
// TrimLeadingSpace false, so `--target-nodes "n1, n2"` yields
// ["n1", " n2"]. Every --target-nodes site must trim: the
// service verbs would otherwise refuse " n2" as an unknown node, and
// backup/restore would send it to the API verbatim — on restore the
// likely outcome is a node quietly skipped (unprobed).
// These tests cover all five sites so a per-verb regression cannot
// hide behind the shared helper.

// TestBackupCreateTrimsTargetNodes pins the wire body. The input
// carries whitespace on BOTH sides of a comma on purpose: "n1 , n2"
// parses to ["n1 ", " n2"], so a trim narrowed to one side (TrimLeft
// survived every other fixture in the package, measured on review)
// fails here.
func TestBackupCreateTrimsTargetNodes(t *testing.T) {
	var raw []byte
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			testsupport.JSONHandler(200, `{}`)(w, r)
		})
	if err := runAuthed(t, rt, out, url, "backup", "create",
		"--database-id", testDatabaseID, "--provider", "pgbackrest",
		"--target-nodes", "n1 , n2"); err != nil {
		t.Fatalf("backup create: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	nodes, _ := body["target_nodes"].([]any)
	if len(nodes) != 2 || nodes[0] != "n1" || nodes[1] != "n2" {
		t.Errorf("target_nodes = %v, want [n1 n2] with whitespace "+
			"trimmed", body["target_nodes"])
	}
}

// TestDatabaseRestoreTrimsTargetNodes pins the same wire body on
// restore — the verb where an untrimmed name likely means a node
// quietly skipped rather than a loud refusal.
func TestDatabaseRestoreTrimsTargetNodes(t *testing.T) {
	body := captureRestoreBody(t, restoreArgs("--target-nodes", "n1, n2"))
	nodes, _ := body["target_nodes"].([]any)
	if len(nodes) != 2 || nodes[0] != "n1" || nodes[1] != "n2" {
		t.Errorf("target_nodes = %v, want [n1 n2] with whitespace "+
			"trimmed", body["target_nodes"])
	}
}

// TestServiceVerbsTrimTargetNodes drives each service verb with a
// value carrying the whitespace pflag leaves, against a cluster whose
// only node is "n1". Untrimmed, resolveHostIDs refuses " n1" as
// unknown; trimmed, the deploy succeeds and places on host-1.
func TestServiceVerbsTrimTargetNodes(t *testing.T) {
	ragDir := t.TempDir()
	ragCfg := filepath.Join(ragDir, "pipelines.json")
	if err := os.WriteFile(ragCfg, []byte(validPipelineJSON),
		0o600); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}

	for _, tc := range []struct {
		name    string
		svcType string
		args    []string
	}{
		{"mcp deploy", "mcp", []string{
			"database", "mcp", "deploy", testDatabaseID,
			"--target-nodes", " n1"}},
		{"rag deploy", "rag", []string{
			"database", "rag", "deploy", testDatabaseID,
			"--embedding-llm-provider", "openai",
			"--embedding-llm-model", "text-embedding-3-small",
			"--embedding-llm-api-key", "sk-e",
			"--completion-llm-provider", "openai",
			"--completion-llm-model", "gpt-4o",
			"--completion-llm-api-key", "sk-c",
			"--pipeline-config", ragCfg,
			"--target-nodes", " n1"}},
		{"postgrest deploy", "postgrest", []string{
			"database", "postgrest", "deploy", testDatabaseID,
			"--db-schemas", "public", "--db-anon-role", "web_anon",
			"--target-nodes", " n1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(dbNoServiceBody))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s with untrimmed --target-nodes: %v",
					tc.name, err)
			}
			svc := serviceOfType(t, rec.services(t), tc.svcType)
			hostIDs, _ := svc["host_ids"].([]any)
			if len(hostIDs) != 1 || hostIDs[0] != "host-1" {
				t.Errorf("host_ids = %v, want [host-1] resolved from "+
					"the trimmed name", hostIDs)
			}
		})
	}
}
