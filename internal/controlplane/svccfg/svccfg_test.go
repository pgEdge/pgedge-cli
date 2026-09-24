package svccfg

import (
	"sort"
	"testing"
)

// wantMCPKeys is the literal 27-key inventory from control-plane
// server/internal/database/mcp_service_config.go:66-94 (mcpKnownKeys)
// at v0.10.0. Stated here as an independent expectation — not derived
// from mcpKeys — so this test is a restatement of the inventory, not
// a tautology over the implementation.
var wantMCPKeys = []string{
	"llm_enabled", "llm_provider", "llm_model", "anthropic_api_key",
	"openai_api_key", "ollama_url", "allow_writes", "init_token",
	"init_users", "embedding_provider", "embedding_model",
	"embedding_api_key", "llm_temperature", "llm_max_tokens",
	"pool_max_conns", "disable_query_database", "disable_get_schema_info",
	"disable_similarity_search", "disable_execute_explain",
	"disable_generate_embedding", "disable_search_knowledgebase",
	"disable_count_rows", "kb_enabled", "kb_embedding_provider",
	"kb_embedding_model", "kb_embedding_api_key", "kb_database_host_path",
}

// wantRAGKeys is every field name in the RAG pipeline tree
// (server/internal/database/rag_service_config.go) at v0.10.0,
// flattened, 21 names.
var wantRAGKeys = []string{
	"pipelines", "name", "description", "tables", "table", "text_column",
	"vector_column", "id_column", "embedding_llm", "rag_llm", "provider",
	"model", "api_key", "base_url", "token_budget", "top_n",
	"system_prompt", "search", "hybrid_enabled", "vector_weight",
	"defaults",
}

// wantPostgRESTKeys is the literal 8-key inventory from
// server/internal/database/postgrest_service_config.go:25-34
// (postgrestKnownKeys) at v0.10.0.
var wantPostgRESTKeys = []string{
	"db_schemas", "db_anon_role", "db_pool", "max_rows", "jwt_secret",
	"jwt_aud", "jwt_role_claim_key", "server_cors_allowed_origins",
}

func sortedNames(t *testing.T, serviceType string) []string {
	t.Helper()
	keys := Keys(serviceType)
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = k.Name
	}
	sort.Strings(names)
	return names
}

func assertKeySet(t *testing.T, serviceType string, want []string) {
	t.Helper()
	got := sortedNames(t, serviceType)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	if len(got) != len(wantSorted) {
		t.Fatalf("%s: got %d keys, want %d\ngot:  %v\nwant: %v",
			serviceType, len(got), len(wantSorted), got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Fatalf("%s: key set mismatch at %d: got %q, want %q\n"+
				"got:  %v\nwant: %v",
				serviceType, i, got[i], wantSorted[i], got, wantSorted)
		}
	}
}

func TestMCPKeysMatchInventory(t *testing.T) {
	assertKeySet(t, "mcp", wantMCPKeys)
}

func TestRAGKeysMatchInventory(t *testing.T) {
	assertKeySet(t, "rag", wantRAGKeys)
}

func TestPostgRESTKeysMatchInventory(t *testing.T) {
	assertKeySet(t, "postgrest", wantPostgRESTKeys)
}

func TestKeysUnknownServiceTypeReturnsNil(t *testing.T) {
	if got := Keys("bogus"); got != nil {
		t.Errorf("Keys(bogus) = %v, want nil", got)
	}
}

// TestKeysNoDuplicateNames guards the flattening in ragKeys (and any
// future service): a duplicate name would silently hide a distinct
// CP rule behind one KeyMeta entry.
func TestKeysNoDuplicateNames(t *testing.T) {
	for _, st := range []string{"mcp", "rag", "postgrest"} {
		seen := map[string]bool{}
		for _, k := range Keys(st) {
			if seen[k.Name] {
				t.Errorf("%s: duplicate key name %q", st, k.Name)
			}
			seen[k.Name] = true
		}
	}
}

// TestRequiredKeyNamesSubsetOfKeys pins RequiredKeyNames to Keys so the
// two cannot silently diverge (e.g. a key renamed in Keys but not in
// a cached Required list, if one is ever added).
func TestRequiredKeyNamesSubsetOfKeys(t *testing.T) {
	for _, st := range []string{"mcp", "rag", "postgrest"} {
		known := KnownKeyNames(st)
		for _, name := range RequiredKeyNames(st) {
			if !known[name] {
				t.Errorf("%s: RequiredKeyNames has %q, "+
					"not in KnownKeyNames", st, name)
			}
		}
	}
}

// TestKeysFloorMatchesDocumentedVersion pins the constant's literal
// value so an accidental edit (typo, stray whitespace) is caught here
// rather than only by the cross-package
// TestServiceConfigKeysFloorMatchesSupportFloor gate in internal/clitest.
func TestKeysFloorMatchesDocumentedVersion(t *testing.T) {
	if KeysFloor != "0.10.0" {
		t.Errorf("KeysFloor = %q, want %q", KeysFloor, "0.10.0")
	}
}

// TestPostgRESTOnlyDBAnonRoleRequired pins the one-required-key shape
// that makes config: {} a guaranteed 400 for postgrest but not for
// mcp: exactly one Required key, and it is unconditional.
func TestPostgRESTOnlyDBAnonRoleRequired(t *testing.T) {
	req := RequiredKeyNames("postgrest")
	if len(req) != 1 || req[0] != "db_anon_role" {
		t.Errorf("postgrest RequiredKeyNames = %v, want [db_anon_role]",
			req)
	}
}

// TestMCPHasNoUnconditionalRequiredKey pins config: {} being valid for
// mcp: no key may be Required without a ConditionalOn gate.
func TestMCPHasNoUnconditionalRequiredKey(t *testing.T) {
	for _, k := range Keys("mcp") {
		if k.Required && k.ConditionalOn == "" {
			t.Errorf("mcp key %q is unconditionally required; "+
				"config: {} must stay valid for mcp", k.Name)
		}
	}
}

// TestRAGPipelinesUnconditionallyRequired pins the opposite: rag's
// config: {} is a guaranteed 400 because pipelines is required with no
// condition gating it away.
func TestRAGPipelinesUnconditionallyRequired(t *testing.T) {
	for _, k := range Keys("rag") {
		if k.Name == "pipelines" {
			if !k.Required || k.ConditionalOn != "" {
				t.Errorf("rag pipelines = %+v, want "+
					"unconditionally required", k)
			}
			return
		}
	}
	t.Fatal("rag Keys() has no pipelines entry")
}
