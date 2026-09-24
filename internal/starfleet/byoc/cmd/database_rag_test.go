package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewDatabaseRAGCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewDatabaseRAGCmd(rt)

	if cmd.Use != "rag" {
		t.Errorf("Use = %q, want \"rag\"", cmd.Use)
	}

	want := map[string]bool{"deploy": false, "update": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("rag command missing subcommand %q", name)
		}
	}
}

func TestParsePipelineConfig(t *testing.T) {
	bareArray := `[
	  {"name": "p1", "tables": [{"table": "t", "text_column": "c", "vector_column": "v"}]}
	]`
	wrapped := `{"pipelines": [
	  {"name": "p1", "tables": [{"table": "t", "text_column": "c", "vector_column": "v"}]}
	]}`

	tests := []struct {
		name      string
		data      string
		wantLen   int
		wantName  string
		wantError bool
	}{
		{name: "bare array", data: bareArray, wantLen: 1, wantName: "p1"},
		{name: "wrapped object", data: wrapped, wantLen: 1, wantName: "p1"},
		{name: "empty array", data: `[]`, wantLen: 0},
		{name: "explicit empty pipelines", data: `{"pipelines": []}`, wantLen: 0},
		{name: "wrong key typo", data: `{"pipeline": []}`, wantError: true},
		{name: "invalid json", data: `{not json`, wantError: true},
		{name: "wrong shape", data: `"a string"`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePipelineConfig([]byte(tt.data))
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantName != "" && got[0].Name != tt.wantName {
				t.Errorf("name = %q, want %q", got[0].Name, tt.wantName)
			}
		})
	}
}

// validPipelineJSON is the smallest pipeline config the API accepts:
// one named pipeline with one fully specified table.
const validPipelineJSON = `[{"name":"p1","tables":[` +
	`{"table":"public.docs","text_column":"content",` +
	`"vector_column":"embedding"}]}]`

func TestValidatePipelines(t *testing.T) {
	table := api.RAGEmbeddingTable{
		Table:        "public.docs",
		TextColumn:   "content",
		VectorColumn: "embedding",
	}

	tests := []struct {
		name      string
		pipelines []api.RAGPipelineConfig
		wantErr   string
	}{
		{
			name: "one valid pipeline",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1", Tables: []api.RAGEmbeddingTable{table}},
			},
		},
		{
			name:      "empty",
			pipelines: nil,
			wantErr:   "at least one pipeline is required",
		},
		{
			name: "missing name",
			pipelines: []api.RAGPipelineConfig{
				{Tables: []api.RAGEmbeddingTable{table}},
			},
			wantErr: "pipelines[0]: name is required",
		},
		{
			name: "reserved name",
			pipelines: []api.RAGPipelineConfig{
				{Name: "_default", Tables: []api.RAGEmbeddingTable{table}},
			},
			wantErr: `name "_default" is reserved`,
		},
		{
			name: "duplicate name",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1", Tables: []api.RAGEmbeddingTable{table}},
				{Name: "p1", Tables: []api.RAGEmbeddingTable{table}},
			},
			wantErr: `pipelines[1]: duplicate name "p1"`,
		},
		{
			name: "no tables",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1"},
			},
			wantErr: "at least one table is required",
		},
		{
			name: "table missing name",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1", Tables: []api.RAGEmbeddingTable{
					{TextColumn: "c", VectorColumn: "v"},
				}},
			},
			wantErr: "tables[0]: table is required",
		},
		{
			name: "table missing text column",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1", Tables: []api.RAGEmbeddingTable{
					{Table: "t", VectorColumn: "v"},
				}},
			},
			wantErr: "tables[0]: text_column is required",
		},
		{
			name: "table missing vector column",
			pipelines: []api.RAGPipelineConfig{
				{Name: "p1", Tables: []api.RAGEmbeddingTable{
					{Table: "t", TextColumn: "c"},
				}},
			},
			wantErr: "tables[0]: vector_column is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePipelines("cfg.json", tt.pipelines)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q",
					err.Error(), tt.wantErr)
			}
			if !strings.Contains(err.Error(), "cfg.json") {
				t.Errorf("error %q does not name the config file",
					err.Error())
			}
		})
	}
}

// TestRAGUpdateIsPartial is the acceptance test for issue #45. It was
// committed skipped, describing the behaviour we wanted rather than the
// behaviour we had; applyRAGService now does the read-modify-write and
// the skip is gone.
func TestRAGUpdateIsPartial(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rec := &capturedApply{}
	url := testsupport.NewAuthedServer(t, rec.handler(dbWithRAGBody))

	// Only --top-n. Everything else must survive from the deployed
	// service.
	if err := runAuthed(t, rt, out, url, "database", "rag", "update",
		testDatabaseID, "--top-n", "10"); err != nil {
		t.Fatalf("rag update --top-n only: %v", err)
	}

	svc := serviceOfType(t, rec.services(t), "rag")
	cfg, ok := svc["rag_config"].(map[string]any)
	if !ok {
		t.Fatalf("no rag_config in payload: %+v", svc)
	}
	if cfg["top_n"] != float64(10) {
		t.Errorf("top_n = %v, want 10", cfg["top_n"])
	}

	pipelines, _ := cfg["pipelines"].([]any)
	if len(pipelines) == 0 {
		t.Error("pipelines dropped: the deployed pipeline must be " +
			"preserved when --pipeline-config is not passed")
	}
	embedding, _ := cfg["embedding_llm"].(map[string]any)
	if embedding == nil || embedding["model"] == "" {
		t.Errorf("embedding_llm not preserved: %v", embedding)
	}
}
