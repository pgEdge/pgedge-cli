#!/bin/sh
# Tests for scripts/cp-service-keys.sh. Focus: the script must FLAG a
# real divergence between internal/controlplane/svccfg and a Control Plane
# checkout, not just report "match" on every input — a diff harness
# that always says "match" looks exactly like a passing one.
#
# Builds a throwaway git checkout with minimal fixture copies of CP's
# three service-config source files (only the parts the script's
# extractors read: the known-keys map literals for mcp/postgrest, and
# the struct json tags for rag), tagged with the CP_TAG value read out
# of openapi/SOURCE — the same place the script under test reads it, so
# a future tag bump retags the fixture instead of producing a
# misleading "git show <tag> failed" that points at the CP checkout.
# The BASELINE fixture's key sets are
# copied verbatim from the real CP source at v0.10.0 (the plan this
# script implements ships against), so the baseline run doubles as a
# live check that internal/controlplane/svccfg has not drifted — the same
# assertion `make vendor-spec`'s CP re-vendor recipe now runs by hand.
#
# Exit codes are captured with `|| rc=$?`, exempt from errexit. See
# test/coverage_gate_test.sh's header comment for why plain set +e/-e
# around a helper function is unsafe here (errexit is not
# function-scoped).
#
# shellcheck disable=SC2016
# The fixture bodies below are Go source written with single-quoted
# `echo`, deliberately: the backtick-delimited struct tags and literal
# $-free Go syntax must NOT undergo shell expansion.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT="${SCRIPT_DIR}/.."
GATE="${REPO_ROOT}/scripts/cp-service-keys.sh"

if [ ! -x "$GATE" ]; then
    echo "FAIL - ${GATE} is missing or not executable" >&2
    exit 1
fi

# CP_TAG is read from the same openapi/SOURCE line the script under test
# reads, so the fixture is always tagged with whatever tag the script
# will ask git for.
SOURCE_FILE="${REPO_ROOT}/openapi/SOURCE"
CP_TAG=$(grep '^CP_TAG=' "$SOURCE_FILE" | head -n 1 | cut -d= -f2)
if [ -z "$CP_TAG" ]; then
    echo "FAIL - no CP_TAG= line in ${SOURCE_FILE}" >&2
    exit 1
fi

fails=0
check() { # description  actual  want
    if [ "$2" -eq "$3" ]; then
        echo "ok   - $1"
    else
        echo "FAIL - $1 (got $2, want $3)"
        fails=$((fails + 1))
    fi
}

nonzero() { if [ "$1" -ne 0 ]; then echo 1; else echo 0; fi; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

CP_FIXTURE="${WORK}/control-plane"
FIXTURE_DIR="server/internal/database"
MCP_REL="${FIXTURE_DIR}/mcp_service_config.go"
RAG_REL="${FIXTURE_DIR}/rag_service_config.go"
PG_REL="${FIXTURE_DIR}/postgrest_service_config.go"
mkdir -p "${CP_FIXTURE}/${FIXTURE_DIR}"
git -C "$CP_FIXTURE" init -q
git -C "$CP_FIXTURE" config user.email test@example.com
git -C "$CP_FIXTURE" config user.name "cp-service-keys test"

# MCP_KEYS is CP's mcpKnownKeys set at v0.10.0, copied verbatim (27
# keys). write_fixture renders it back out as a Go map literal so a
# single key can be added or removed per test case.
MCP_KEYS="llm_enabled llm_provider llm_model anthropic_api_key
openai_api_key ollama_url allow_writes init_token init_users
embedding_provider embedding_model embedding_api_key llm_temperature
llm_max_tokens pool_max_conns disable_query_database
disable_get_schema_info disable_similarity_search
disable_execute_explain disable_generate_embedding
disable_search_knowledgebase disable_count_rows kb_enabled
kb_embedding_provider kb_embedding_model kb_embedding_api_key
kb_database_host_path"

# write_fixture(extra_mcp_key, dropped_mcp_key) writes the three source
# files, then commits and force-tags $CP_TAG (read above from the same
# openapi/SOURCE line the script under test reads). Both arguments are
# optional and drive the two directions of the script's report():
#
#   extra_mcp_key   — a key CP has that svccfg does not mirror
#   dropped_mcp_key — a key svccfg mirrors that CP no longer has
write_fixture() {
    extra_key=${1:-}
    drop_key=${2:-}
    mcp_file="${CP_FIXTURE}/${MCP_REL}"
    {
        echo 'package database'
        echo 'var mcpKnownKeys = map[string]bool{'
        # Deliberate word splitting over MCP_KEYS' whitespace.
        # shellcheck disable=SC2086
        for k in $MCP_KEYS; do
            if [ "$k" = "$drop_key" ]; then
                continue
            fi
            printf '\t"%s": true,\n' "$k"
        done
        if [ -n "$extra_key" ]; then
            printf '\t"%s": true,\n' "$extra_key"
        fi
        echo '}'
    } >"$mcp_file"

    rag_file="${CP_FIXTURE}/${RAG_REL}"
    {
        echo 'package database'
        echo 'type RAGPipelineLLMConfig struct {'
        echo '	Provider string  `json:"provider"`'
        echo '	Model    string  `json:"model"`'
        echo '	APIKey   *string `json:"api_key,omitempty"`'
        echo '	BaseURL  *string `json:"base_url,omitempty"`'
        echo '}'
        echo 'type RAGPipelineTable struct {'
        echo '	Table        string  `json:"table"`'
        echo '	TextColumn   string  `json:"text_column"`'
        echo '	VectorColumn string  `json:"vector_column"`'
        echo '	IDColumn     *string `json:"id_column,omitempty"`'
        echo '}'
        echo 'type RAGPipelineSearch struct {'
        echo '	HybridEnabled *bool    `json:"hybrid_enabled,omitempty"`'
        echo '	VectorWeight  *float64 `json:"vector_weight,omitempty"`'
        echo '}'
        echo 'type RAGPipeline struct {'
        echo '	Name         string               `json:"name"`'
        echo '	Description  *string              `json:"description,omitempty"`'
        echo '	Tables       []RAGPipelineTable   `json:"tables"`'
        echo '	EmbeddingLLM RAGPipelineLLMConfig `json:"embedding_llm"`'
        echo '	RAGLLM       RAGPipelineLLMConfig `json:"rag_llm"`'
        echo '	TokenBudget  *int                 `json:"token_budget,omitempty"`'
        echo '	TopN         *int                 `json:"top_n,omitempty"`'
        echo '	SystemPrompt *string              `json:"system_prompt,omitempty"`'
        echo '	Search       *RAGPipelineSearch   `json:"search,omitempty"`'
        echo '}'
        echo 'type RAGDefaults struct {'
        echo '	TokenBudget *int `json:"token_budget,omitempty"`'
        echo '	TopN        *int `json:"top_n,omitempty"`'
        echo '}'
        echo 'type RAGServiceConfig struct {'
        echo '	Pipelines []RAGPipeline `json:"pipelines"`'
        echo '	Defaults  *RAGDefaults  `json:"defaults,omitempty"`'
        echo '}'
    } >"$rag_file"

    pg_file="${CP_FIXTURE}/${PG_REL}"
    {
        echo 'package database'
        echo 'var postgrestKnownKeys = map[string]bool{'
        echo '	"db_schemas":                  true,'
        echo '	"db_anon_role":                true,'
        echo '	"db_pool":                     true,'
        echo '	"max_rows":                    true,'
        echo '	"jwt_secret":                  true,'
        echo '	"jwt_aud":                     true,'
        echo '	"jwt_role_claim_key":          true,'
        echo '	"server_cors_allowed_origins": true,'
        echo '}'
    } >"$pg_file"

    # Explicit paths, never `add -A`: the repo convention, and here it
    # also keeps a stray file under $WORK out of the fixture commit.
    git -C "$CP_FIXTURE" add "$MCP_REL" "$RAG_REL" "$PG_REL"
    git -C "$CP_FIXTURE" commit -q -m "fixture" --allow-empty
    git -C "$CP_FIXTURE" tag -f "$CP_TAG" >/dev/null
}

# T1 (positive control, the whole point of this script): a fixture
# whose CP-side mcp key set exactly matches internal/controlplane/svccfg (the
# real mirror this repo ships) must PASS.
write_fixture ""
rc=0
out=$("$GATE" "$CP_FIXTURE" 2>&1) || rc=$?
check "matching fixture passes" "$rc" 0
case "$out" in
    *"matches CP"*) echo "ok   - match message printed" ;;
    *)
        echo "FAIL - match message missing: $out" >&2
        fails=$((fails + 1))
        ;;
esac

# T2 (the mutation this script exists to catch, direction 1 of 2): plant
# a key on the CP side that svccfg does not mirror. The script must FAIL
# and name the planted key, proving the diff is real and not a silent
# no-op.
write_fixture "totally_new_cp_key"
rc=0
out=$("$GATE" "$CP_FIXTURE" 2>&1) || rc=$?
check "planted CP-only key fails closed" "$(nonzero "$rc")" 1
case "$out" in
    *"svccfg is MISSING keys CP has"*)
        echo "ok   - reported as the svccfg-is-missing direction"
        ;;
    *)
        echo "FAIL - wrong direction reported: $out" >&2
        fails=$((fails + 1))
        ;;
esac
case "$out" in
    *"totally_new_cp_key"*)
        echo "ok   - planted key named in output"
        ;;
    *)
        echo "FAIL - planted key not named in output: $out" >&2
        fails=$((fails + 1))
        ;;
esac

# T2b (direction 2 of 2, the inverse of T2): remove a key from the CP
# side that svccfg still mirrors — the shape of a real CP release that
# retires a key. report() must fire its OTHER branch: a stale svccfg,
# not a missing one. Asserting the direction, not just the exit code,
# is what stops both branches collapsing into one message.
write_fixture "" "kb_database_host_path"
rc=0
out=$("$GATE" "$CP_FIXTURE" 2>&1) || rc=$?
check "svccfg-only key fails closed" "$(nonzero "$rc")" 1
case "$out" in
    *"svccfg has keys CP does not have"*)
        echo "ok   - reported as the svccfg-is-stale direction"
        ;;
    *)
        echo "FAIL - wrong direction reported: $out" >&2
        fails=$((fails + 1))
        ;;
esac
case "$out" in
    *"kb_database_host_path"*)
        echo "ok   - retired key named in output"
        ;;
    *)
        echo "FAIL - retired key not named in output: $out" >&2
        fails=$((fails + 1))
        ;;
esac
case "$out" in
    *"svccfg is MISSING keys CP has"*)
        echo "FAIL - reported the missing direction too: $out" >&2
        fails=$((fails + 1))
        ;;
    *) echo "ok   - did not also report the missing direction" ;;
esac

# T3: a checkout path that is not a git repo fails closed with a clear
# message, rather than a confusing git error.
rc=0
out=$("$GATE" "${WORK}/not-a-repo" 2>&1) || rc=$?
check "non-git checkout path fails closed" "$(nonzero "$rc")" 1
case "$out" in
    *"not a git checkout"*) echo "ok   - clear error for non-git path" ;;
    *)
        echo "FAIL - unclear error for non-git path: $out" >&2
        fails=$((fails + 1))
        ;;
esac

# T4: wrong argument count fails closed with a usage message.
rc=0
out=$("$GATE" 2>&1) || rc=$?
check "missing argument fails closed" "$(nonzero "$rc")" 1
case "$out" in
    *"usage:"*) echo "ok   - usage message printed" ;;
    *)
        echo "FAIL - no usage message: $out" >&2
        fails=$((fails + 1))
        ;;
esac

if [ "$fails" -ne 0 ]; then
    echo "${fails} test(s) failed" >&2
    exit 1
fi
echo "all cp-service-keys tests passed"
