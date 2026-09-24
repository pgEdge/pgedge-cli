package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The machine-readable output contract.
//
// Under -o json every read verb writes its payload to stdout, and so
// does a service write — printUpdatedDatabase exists precisely so a
// deploy is not silent under json, since its human confirmation goes to
// stderr. A caller that cannot read back the service id or assigned
// port it just created has no way to continue.

func TestJSONOutputIsWrittenForReads(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
	}{
		{"database list", "[" + databaseJSON(testDatabaseID, "") + "]",
			[]string{"database", "list"}},
		{"database get", databaseJSON(testDatabaseID, ""),
			[]string{"database", "get", testDatabaseID}},
		{"database connection-string", databaseWithConnectionJSON(testDatabaseID),
			[]string{"database", "connection-string", testDatabaseID}},
		{"database metrics", managedMetricsBody,
			[]string{"database", "metrics", testDatabaseID}},
		{"database logs", managedLogsBody,
			[]string{"database", "logs", testDatabaseID}},
		{"service list", databaseJSON(testDatabaseID, mcpServiceJSON),
			[]string{"database", "service", "list", testDatabaseID}},
		{"service get", databaseJSON(testDatabaseID, mcpServiceJSON),
			[]string{"database", "service", "get", testDatabaseID, "mcp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, tc.body))

			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if strings.TrimSpace(out.String()) == "" {
				t.Fatal("nothing was written to stdout under -o json")
			}
			var any1 any
			if err := json.Unmarshal(out.Bytes(), &any1); err != nil {
				t.Errorf("stdout was not valid JSON: %v\n%s", err, out.String())
			}
		})
	}
}

// TestJSONOutputIsWrittenForServiceWrites is the one that matters most:
// without printUpdatedDatabase a deploy writes nothing at all to stdout
// under -o json.
func TestJSONOutputIsWrittenForServiceWrites(t *testing.T) {
	for _, args := range [][]string{
		{"database", "mcp", "update", testDatabaseID, "--allow-writes"},
		{"database", "service", "remove", testDatabaseID, "mcp", "--force"},
	} {
		t.Run(strings.Join(args[:3], " "), func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, threeServicesJSON),
				http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("write: %v", err)
			}
			if strings.TrimSpace(out.String()) == "" {
				t.Fatal("a service write wrote nothing under -o json")
			}
		})
	}
}

// TestTextOutputDoesNotDumpJSON is the negative control: under text the
// payload must NOT be printed, or printUpdatedDatabase's format guard
// is doing nothing.
func TestTextOutputDoesNotDumpJSON(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes")
	if err != nil {
		t.Fatalf("mcp update: %v", err)
	}
	if strings.Contains(out.String(), `"service_id"`) {
		t.Errorf("text mode dumped the raw payload:\n%s", out.String())
	}
}

// --- full flag sets, so every overlay branch is exercised ---

// mcpConfigFrom pulls the single service's mcp_config out of a captured
// PATCH body.
func mcpConfigFrom(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Services []struct {
			ServiceType string         `json:"service_type"`
			MCPConfig   map[string]any `json:"mcp_config"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("body was not JSON: %v", err)
	}
	for _, s := range payload.Services {
		if s.ServiceType == "mcp" {
			return s.MCPConfig
		}
	}
	t.Fatal("no mcp service in the request body")
	return nil
}

func TestMCPDeployAppliesEveryFlag(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
		testDatabaseID,
		"--allow-writes",
		"--embedding-provider", "voyage",
		"--embedding-model", "nomic",
		"--embedding-api-key", "sk-x",
		"--init-tokens", "tok",
		"--init-users", "u:p")
	if err != nil {
		t.Fatalf("mcp deploy: %v", err)
	}

	cfg := mcpConfigFrom(t, rec.Body)
	for k, want := range map[string]any{
		"allow_writes":       true,
		"embedding_provider": "voyage",
		"embedding_model":    "nomic",
		"embedding_api_key":  "sk-x",
		"init_tokens":        "tok",
		"init_users":         "u:p",
	} {
		if cfg[k] != want {
			t.Errorf("mcp_config[%q] = %v, want %v", k, cfg[k], want)
		}
	}
	if _, ok := cfg["ollama_url"]; ok {
		t.Errorf("ollama_url reached the wire; saas rejects the field "+
			"on managed and the whole body 400s: %v", cfg)
	}
}

// TestMCPManagedRefusesOllama pins the managed/byoc asymmetry from the
// managed side. saas dropped ollama from the managed contract,
// so the provider is refused client-side for exit 2
// rather than spending a round trip on a 400.
//
// The flag is gone as well as the provider, and cobra answers an
// unknown flag with exit 2 of its own — but only the provider check
// is this package's to make, so that is what is asserted here.
func TestMCPManagedRefusesOllama(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
		testDatabaseID, "--embedding-provider", "ollama")
	if err == nil {
		t.Fatal("ollama was accepted as a managed embedding provider")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("want ExitUsage, got %v", err)
	}
	if rec.Body != "" {
		t.Errorf("a refused provider still reached the API: %q",
			rec.Body)
	}
}

func TestRAGDeployAppliesEveryFlag(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "rag", "deploy",
		testDatabaseID,
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "em",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "anthropic",
		"--completion-llm-model", "cm",
		"--completion-llm-api-key", "sk-c",
		"--token-budget", "0",
		"--top-n", "0",
		"--pipeline-config", writePipelineConfig(t))
	if err != nil {
		t.Fatalf("rag deploy: %v", err)
	}

	// --top-n 0 and --token-budget 0 are explicit zeros, not "unset".
	// A `> 0` guard would drop them silently.
	for _, want := range []string{
		`"top_n":0`, `"token_budget":0`, `"provider":"anthropic"`,
		`"name":"docs"`,
	} {
		if !strings.Contains(rec.Body, want) {
			t.Errorf("body missing %s:\n%s", want, rec.Body)
		}
	}
}

func TestPostgRESTDeployAppliesEveryFlag(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "postgrest", "deploy",
		testDatabaseID,
		"--db-schemas", "public,api",
		"--db-anon-role", "anon",
		"--db-pool", "20",
		"--max-rows", "500",
		"--cors-origins", "https://example.com",
		"--jwt-secret", strings.Repeat("s", 32),
		"--jwt-audience", "aud",
		"--jwt-role-claim-key", ".role")
	if err != nil {
		t.Fatalf("postgrest deploy: %v", err)
	}
	for _, want := range []string{
		`"db_schemas":"public,api"`, `"db_anon_role":"anon"`,
		`"db_pool":20`, `"max_rows":500`,
		`"cors_origins":"https://example.com"`,
		`"jwt_audience":"aud"`, `"jwt_role_claim_key":".role"`,
	} {
		if !strings.Contains(rec.Body, want) {
			t.Errorf("body missing %s:\n%s", want, rec.Body)
		}
	}
}

// TestPostgRESTDeployAcceptsBoundaryValues pins the inclusive edges, so
// a `<` that should be `<=` is caught.
func TestPostgRESTDeployAcceptsBoundaryValues(t *testing.T) {
	for _, extra := range [][]string{
		{"--db-pool", "1"}, {"--db-pool", "30"},
		{"--max-rows", "1"}, {"--max-rows", "10000"},
	} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, threeServicesJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := append([]string{"database", "postgrest", "update",
				testDatabaseID}, extra...)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("boundary value rejected: %v", err)
			}
		})
	}
}

// --- error paths on the read-modify-write helpers ---

func TestServiceWriteFailsWhenTheGetFails(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusNotFound, `{"code":404,"message":"nope"}`))

	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes")
	if err == nil {
		t.Fatal("a failed GET was reported as a successful write")
	}
}

func TestServiceWriteFailsWhenThePatchFails(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusInternalServerError, `{"code":500,"message":"boom"}`))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes")
	if err == nil {
		t.Fatal("a failed PATCH was reported as success")
	}
}

// TestResolveByPrefixFailsWhenTheListFails covers the resolve path's
// error branch: a prefix needs a list call, and that call can fail.
func TestResolveByPrefixFailsWhenTheListFails(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusInternalServerError, `{"code":500,"message":"boom"}`))

	err := runAuthed(t, rt, out, url, "database", "get", "abc")
	if err == nil {
		t.Fatal("a failed list during prefix resolution was ignored")
	}
}

// TestGetHandlesAnEmptyBody covers the nil-payload branch: a 200 with a
// JSON null is not a crash.
func TestGetHandlesAnEmptyBody(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "null"))

	if err := runAuthed(t, rt, out, url,
		"database", "get", testDatabaseID); err != nil {
		t.Fatalf("a null body crashed get: %v", err)
	}
}

// TestCreateHandlesAnEmptyBody covers create's no-details branch.
func TestCreateHandlesAnEmptyBody(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "null"))

	err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "d", "--region", "r", "--size", "s")
	if err != nil {
		t.Fatalf("a null body crashed create: %v", err)
	}
}

// TestUpdateHandlesAnEmptyBody covers update's no-details branch.
func TestUpdateHandlesAnEmptyBody(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "null"))

	err := runAuthed(t, rt, out, url, "database", "update",
		testDatabaseID, "--display-name", "x")
	if err != nil {
		t.Fatalf("a null body crashed update: %v", err)
	}
}

// The two `rag update --pipeline-config` branches that used to be
// covered here — the file read and the JSON parse — moved to
// database_rag_exit_run_test.go, which asserts the exit CODE rather
// than only that an error came back. Both branches still run; nothing
// asserted here was dropped.
