package cmd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// serviceApplyHandlerFor stands in for the read-modify-write flow shared
// by mcp/rag deploy and update: GET a database, list its cluster's
// nodes, then accept the PUT that applies the new service list. dbBody
// is the database the GET returns, letting a caller choose one with or
// without a pre-existing service of the type under test — the
// deploy/update guard (#117) refuses `deploy` outright when one is
// already present, so a genuine deploy test needs a fixture without it.
func serviceApplyHandlerFor(dbBody string) http.HandlerFunc {
	nodes := `[{"id":"host-1","name":"n1","region":"us-east-1",` +
		`"instance_type":"r7g.medium","ip_address":"10.0.0.1"}]`
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			testsupport.JSONHandler(200, nodes)(w, r)
		case r.Method == http.MethodGet:
			testsupport.JSONHandler(200, dbBody)(w, r)
		default:
			testsupport.JSONHandler(200, `{}`)(w, r)
		}
	}
}

// serviceApplyHandler is serviceApplyHandlerFor(dbWithServiceBody): a
// database that already carries an MCP service, for tests exercising an
// update against a deployed service (or a first deploy of a type other
// than MCP).
func serviceApplyHandler() http.HandlerFunc {
	return serviceApplyHandlerFor(dbWithServiceBody)
}

func TestDatabaseMCPDeployRun(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			serviceApplyHandlerFor(dbNoServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID, "--allow-writes"); err != nil {
			t.Fatalf("mcp deploy: %v", err)
		}
		if !strings.Contains(errb.String(), "MCP service applied") {
			t.Errorf("missing applied message: %q", errb.String())
		}
	})

	t.Run("with embedding provider", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			serviceApplyHandlerFor(dbNoServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID, "--embedding-provider", "openai",
			"--embedding-model", "text-embedding-3-small",
			"--embedding-api-key", "sk-x"); err != nil {
			t.Fatalf("mcp deploy embedding: %v", err)
		}
	})

	t.Run("deploy on an existing service is refused", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, serviceApplyHandler())
		err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID)
		if err == nil {
			t.Fatal("deploy against an already-deployed MCP service " +
				"was accepted")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
			t.Errorf("want exit %d, got %v", ExitGeneral, err)
		}
		if !strings.Contains(err.Error(), "mcp update") {
			t.Errorf("error does not point at mcp update: %v", err)
		}
	})

	t.Run("database not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url, "database", "mcp",
			"deploy", testDatabaseID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestDatabaseMCPUpdateRun(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, serviceApplyHandler())
	if err := runAuthed(t, rt, out, url, "database", "mcp",
		"update", testDatabaseID, "--allow-writes"); err != nil {
		t.Fatalf("mcp update: %v", err)
	}
}

func TestDatabaseRAGDeployRun(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pipelines.json")
	// A structurally valid single pipeline: an empty array is rejected
	// both client-side and by the API, so the old `[]` fixture was
	// asserting a request the server never accepts.
	if err := os.WriteFile(cfgPath, []byte(validPipelineJSON),
		0o600); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}

	deployArgs := []string{"database", "rag", "deploy", testDatabaseID,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "text-embedding-3-small",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "openai",
		"--completion-llm-model", "gpt-4o",
		"--completion-llm-api-key", "sk-c",
		"--pipeline-config", cfgPath}

	t.Run("success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, serviceApplyHandler())
		if err := runAuthed(t, rt, out, url, deployArgs...); err != nil {
			t.Fatalf("rag deploy: %v", err)
		}
		if !strings.Contains(errb.String(), "RAG service applied") {
			t.Errorf("missing applied message: %q", errb.String())
		}
	})

	t.Run("missing pipeline file", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, serviceApplyHandler())
		args := append([]string{}, deployArgs...)
		args[len(args)-1] = filepath.Join(dir, "does-not-exist.json")
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error for missing pipeline file")
		}
	})
}

func TestDatabaseRAGUpdateRun(t *testing.T) {
	// Against a database that HAS a RAG service, a flags-only update
	// works and inherits the rest.
	t.Run("partial update against a deployed service", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithRAGBody))
		if err := runAuthed(t, rt, out, url, "database", "rag",
			"update", testDatabaseID, "--top-n", "10",
			"--token-budget", "1000"); err != nil {
			t.Fatalf("rag update: %v", err)
		}
	})

	// Against one that does not, the CLI now says so instead of sending
	// a request that can only 400. This used to "succeed" against a stub
	// while failing against the real API with "rag_config must have at
	// least one pipeline" — the exact report that opened issue #45.
	//
	// Since #117, this is the deploy/update guard firing, not the
	// required-flags check: the guard runs before flag validation, so
	// the message names `rag deploy` rather than the flags a first
	// deploy needs.
	t.Run("no deployed service points at rag deploy", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, serviceApplyHandler())
		err := runAuthed(t, rt, out, url, "database", "rag",
			"update", testDatabaseID, "--top-n", "10")
		if err == nil {
			t.Fatal("expected an error updating a database with no " +
				"RAG service deployed")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
			t.Errorf("want exit %d, got %v", ExitGeneral, err)
		}
		for _, want := range []string{`"rag"`, "rag deploy"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

// dbWithMCPConfig is a database whose MCP service has write access, an
// embedding provider, and its server-side secrets set.
//
// The secrets belong in this fixture: the CLI reads services through
// GET /databases/{id}, which saas converts with includeSecrets=true, so
// embedding_api_key and init_tokens really do come back. Verified live
// against --profile dev. (ListDatabases converts with
// includeSecrets=false and omits them — so a fixture modelled on a LIST
// response would understate what the merge has to preserve.)
const dbWithMCPConfig = `{"id":"` + testDatabaseID + `",` +
	`"name":"mydb","status":"available","pg_version":"16",` +
	`"cluster_id":"` + testClusterID + `",` +
	`"created_at":"2024-03-15T10:30:00Z","services":[` +
	`{"service_id":"svc-1","service_type":"mcp","state":"running",` +
	`"host_id":"host-1","mcp_config":{"allow_writes":true,` +
	`"embedding_provider":"openai",` +
	`"embedding_model":"text-embedding-3-small",` +
	`"embedding_api_key":"sk-deployed","init_tokens":"tok-deployed",` +
	`"init_users":"alice:pw"}}]}`

// TestMCPUpdatePreservesConfig covers the half of issue #45 that the
// issue itself got wrong. It recorded `mcp update` as faring better
// than `rag update` "because its config fields are all optional
// pointers" — but AllowWrites was assigned unconditionally from a bool
// flag defaulting to false, so any unrelated change silently sent
// allow_writes:false and revoked the service's write access.
//
// That is a privilege change, not a dropped setting, and nothing was
// asserting it either way.
func TestMCPUpdatePreservesConfig(t *testing.T) {
	apply := func(t *testing.T, args ...string) map[string]any {
		t.Helper()
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithMCPConfig))
		full := append([]string{"database", "mcp", "update",
			testDatabaseID}, args...)
		if err := runAuthed(t, rt, out, url, full...); err != nil {
			t.Fatalf("mcp update %v: %v", args, err)
		}
		svc := serviceOfType(t, rec.services(t), "mcp")
		cfg, ok := svc["mcp_config"].(map[string]any)
		if !ok {
			t.Fatalf("no mcp_config in payload: %+v", svc)
		}
		return cfg
	}

	t.Run("an unrelated change does not revoke write access",
		func(t *testing.T) {
			cfg := apply(t, "--embedding-model", "text-embedding-3-large")
			if cfg["allow_writes"] != true {
				t.Errorf("allow_writes = %v, want true preserved — an "+
					"update that never mentioned --allow-writes must not "+
					"revoke it", cfg["allow_writes"])
			}
			if cfg["embedding_model"] != "text-embedding-3-large" {
				t.Errorf("embedding_model = %v, want the new value",
					cfg["embedding_model"])
			}
		})

	t.Run("the embedding configuration survives", func(t *testing.T) {
		cfg := apply(t, "--allow-writes=false")
		for key, want := range map[string]any{
			"embedding_provider": "openai",
			"embedding_model":    "text-embedding-3-small",
		} {
			if cfg[key] != want {
				t.Errorf("%s = %v, want %v preserved", key, cfg[key], want)
			}
		}
	})

	t.Run("server-side secrets survive", func(t *testing.T) {
		// These come back on GET /databases/{id}, so the merge must
		// carry them. Dropping them would blank working credentials
		// server-side on any unrelated config change.
		cfg := apply(t, "--allow-writes=false")
		for key, want := range map[string]any{
			"embedding_api_key": "sk-deployed",
			"init_tokens":       "tok-deployed",
			"init_users":        "alice:pw",
		} {
			if cfg[key] != want {
				t.Errorf("%s = %v, want %v preserved", key, cfg[key], want)
			}
		}
	})

	t.Run("allow-writes=false still turns it off", func(t *testing.T) {
		// The flag must remain usable in both directions: preserving an
		// omitted bool is only correct if an explicit false still lands.
		cfg := apply(t, "--allow-writes=false")
		if cfg["allow_writes"] != false {
			t.Errorf("allow_writes = %v, want false", cfg["allow_writes"])
		}
	})
}

// TestRAGUpdateOverlayDetails covers the parts of the merge that the
// acceptance test does not: an explicit zero, the write-only API keys,
// and placement.
func TestRAGUpdateOverlayDetails(t *testing.T) {
	ragCfg := func(t *testing.T, twoNode bool, args ...string) (
		map[string]any, map[string]any,
	) {
		t.Helper()
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		h := rec.handler(dbWithRAGBody)
		if twoNode {
			h = rec.twoNodeHandler(dbWithRAGBody)
		}
		url := testsupport.NewAuthedServer(t, h)
		full := append([]string{"database", "rag", "update",
			testDatabaseID}, args...)
		if err := runAuthed(t, rt, out, url, full...); err != nil {
			t.Fatalf("rag update %v: %v", args, err)
		}
		svc := serviceOfType(t, rec.services(t), "rag")
		cfg, ok := svc["rag_config"].(map[string]any)
		if !ok {
			t.Fatalf("no rag_config in payload: %+v", svc)
		}
		return cfg, svc
	}

	t.Run("an explicit zero is not an omitted flag", func(t *testing.T) {
		// The previous implementation gated on `opts.topN > 0`, which
		// cannot distinguish `--top-n 0` from not passing it at all.
		// Flags().Changed can, and this is the only assertion that says
		// so.
		cfg, _ := ragCfg(t, false, "--top-n", "0")
		got, present := cfg["top_n"]
		if !present {
			t.Fatal("top_n absent: an explicit --top-n 0 was swallowed")
		}
		if got != float64(0) {
			t.Errorf("top_n = %v, want 0", got)
		}
	})

	t.Run("write-only API keys are not invented", func(t *testing.T) {
		// RAGLLMConfig.ApiKey is write-only and never returned, so there
		// is nothing to merge. The merge must leave it absent rather
		// than sending an empty string, which would blank a working
		// credential server-side.
		cfg, _ := ragCfg(t, false, "--top-n", "7")
		embedding, _ := cfg["embedding_llm"].(map[string]any)
		if embedding == nil {
			t.Fatal("no embedding_llm in payload")
		}
		if v, present := embedding["api_key"]; present {
			t.Errorf("api_key = %v, want absent — it is write-only, so "+
				"the merge has nothing to preserve and must not send a "+
				"blank", v)
		}
		if embedding["model"] != "text-embedding-3-small" {
			t.Errorf("model = %v, want the deployed value preserved",
				embedding["model"])
		}
	})

	t.Run("placement is preserved, and explicit nodes still win",
		func(t *testing.T) {
			_, svc := ragCfg(t, true, "--top-n", "7")
			hosts, _ := svc["host_ids"].([]any)
			if len(hosts) != 1 || hosts[0] != "host-1" {
				t.Errorf("host_ids = %v, want the deployed [host-1]", hosts)
			}

			_, svc = ragCfg(t, true, "--top-n", "7", "--target-nodes", "n2")
			hosts, _ = svc["host_ids"].([]any)
			if len(hosts) != 1 || hosts[0] != "host-2" {
				t.Errorf("host_ids = %v, want the explicit [host-2]", hosts)
			}
		})
}

// TestMCPEmbeddingProviderRequiresAPIKey covers #563, the byoc port of
// managed's #551 check: openai or voyage with no key passed or stored
// is refused before the write, while ollama, which takes --ollama-url,
// is not. dbWithMCPConfig stores a key; dbWithServiceBody's MCP service
// carries no config at all.
func TestMCPEmbeddingProviderRequiresAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		verb    string
		db      string
		args    []string
		wantErr bool
	}{
		{
			name: "deploy: key passed this call",
			verb: "deploy", db: dbNoServiceBody,
			args: []string{"--embedding-provider", "openai",
				"--embedding-api-key", "sk-new"},
		},
		{
			name: "update: key already stored",
			verb: "update", db: dbWithMCPConfig,
			args: []string{"--embedding-provider", "voyage"},
		},
		{
			name: "deploy: ollama needs no key",
			verb: "deploy", db: dbNoServiceBody,
			args: []string{"--embedding-provider", "ollama",
				"--ollama-url", "http://ollama:11434"},
		},
		{
			name: "update: provider not touched",
			verb: "update", db: dbWithServiceBody,
			args: []string{"--allow-writes"},
		},
		{
			name: "deploy: openai, no key anywhere",
			verb: "deploy", db: dbNoServiceBody,
			args:    []string{"--embedding-provider", "openai"},
			wantErr: true,
		},
		{
			name: "update: voyage, no key anywhere",
			verb: "update", db: dbWithServiceBody,
			args:    []string{"--embedding-provider", "voyage"},
			wantErr: true,
		},
		{
			name: "deploy: an empty key is no key",
			verb: "deploy", db: dbNoServiceBody,
			args: []string{"--embedding-provider", "openai",
				"--embedding-api-key", ""},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			writes := 0
			apply := serviceApplyHandlerFor(tc.db)
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes++
					}
					apply(w, r)
				})
			args := append([]string{"database", "mcp", tc.verb,
				testDatabaseID}, tc.args...)
			err := runAuthed(t, rt, out, url, args...)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if writes != 1 {
					t.Errorf("writes = %d, want 1", writes)
				}
				return
			}
			requireExitUsage(t, err)
			if !strings.Contains(err.Error(), "--embedding-api-key") {
				t.Errorf("error does not name the flag it demands: %v",
					err)
			}
			if writes != 0 {
				t.Errorf("writes = %d, want 0: the refusal must come "+
					"before the write", writes)
			}
		})
	}
}
