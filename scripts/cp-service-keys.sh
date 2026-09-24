#!/bin/sh
# Diffs internal/controlplane/svccfg's hand-mirrored service-config key sets
# against Control Plane's own key literals, read straight out of
# `git show` at the tag pinned in openapi/SOURCE (CP_TAG) — never from
# a checkout's working tree, since the checkout's HEAD can be a
# divergent line rather than simply "ahead" of the tag (see
# openapi/SOURCE's CONTROL PLANE section).
#
#   usage: cp-service-keys.sh <control-plane-checkout>
#
# Exit non-zero on ANY difference in either direction:
#   - CP has a key svccfg does not mirror yet (svccfg is missing it)
#   - svccfg claims a key CP no longer has (svccfg is stale)
#
# Wire this in as step 0 of the CP-tag re-vendor recipe (openapi/SOURCE):
# run it and reconcile internal/controlplane/svccfg BEFORE bumping CP_TAG.
#
# mcp and postgrest are each gated by CP's own flat known-keys map
# literal (mcpKnownKeys / postgrestKnownKeys) — the exact set CP's
# parser accepts, with no risk of picking up an unrelated nested
# struct's fields (mcp_service_config.go also defines MCPServiceUser,
# whose username/password fields are not top-level mcp keys).
#
# rag has no such flat map for its full shape — only the two top-level
# keys are listed that way (ragKnownTopLevelKeys) — so it is gated by
# every `json:"..."` tag in rag_service_config.go instead. That file
# defines only the RAG pipeline tree (no unrelated struct), so a
# blanket tag scan is exactly the flattened key set svccfg.Keys("rag")
# mirrors.
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT="${SCRIPT_DIR}/.."
SVCCFG_DIR="${REPO_ROOT}/internal/controlplane/svccfg"
SOURCE_FILE="${REPO_ROOT}/openapi/SOURCE"

die() {
    echo "cp-service-keys: $1" >&2
    exit 1
}

[ "$#" -eq 1 ] || die "usage: $0 <control-plane-checkout>"
CP_CHECKOUT=$1
[ -d "${CP_CHECKOUT}/.git" ] || die "${CP_CHECKOUT} is not a git checkout"
[ -f "$SOURCE_FILE" ] || die "${SOURCE_FILE} not found"

CP_TAG=$(grep '^CP_TAG=' "$SOURCE_FILE" | head -n 1 | cut -d= -f2)
[ -n "$CP_TAG" ] || die "no CP_TAG= line in ${SOURCE_FILE}"

MCP_PATH="server/internal/database/mcp_service_config.go"
RAG_PATH="server/internal/database/rag_service_config.go"
PG_PATH="server/internal/database/postgrest_service_config.go"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fetch() { # cp_relative_path  ->  stdout
    git -C "$CP_CHECKOUT" show "${CP_TAG}:$1" 2>/dev/null ||
        die "git show ${CP_TAG}:$1 failed (checkout: ${CP_CHECKOUT})"
}

# extract_map_keys prints the quoted string keys of a Go map literal
# named varname (e.g. `var mcpKnownKeys = map[string]bool{ "k": true`),
# one per line, reading the source from stdin.
extract_map_keys() { # varname
    awk -v v="$1" '
        index($0, "var " v " = map") { found = 1; next }
        found && $0 ~ /^}/ { found = 0 }
        found && match($0, /"[a-zA-Z0-9_]+"/) {
            print substr($0, RSTART + 1, RLENGTH - 2)
        }
    '
}

# extract_json_tags prints every `json:"name"` tag's name, one per
# line, reading Go source from stdin. Used only for rag, whose full
# shape has no flat known-keys map (see header comment).
extract_json_tags() {
    grep -o 'json:"[a-zA-Z0-9_]*' | sed 's/^json:"//'
}

# extract_svccfg_names prints every KeyMeta{Name: "..."} literal's name
# out of the given svccfg source file(s), one per line.
extract_svccfg_names() {
    grep -oh 'Name: *"[a-zA-Z0-9_]*"' "$@" |
        sed -E 's/Name: *"([a-zA-Z0-9_]*)"/\1/'
}

fetch "$MCP_PATH" >"${WORK}/cp_mcp.go"
fetch "$RAG_PATH" >"${WORK}/cp_rag.go"
fetch "$PG_PATH" >"${WORK}/cp_postgrest.go"

extract_map_keys mcpKnownKeys <"${WORK}/cp_mcp.go" |
    sort -u >"${WORK}/cp_mcp.keys"
extract_json_tags <"${WORK}/cp_rag.go" |
    sort -u >"${WORK}/cp_rag.keys"
extract_map_keys postgrestKnownKeys <"${WORK}/cp_postgrest.go" |
    sort -u >"${WORK}/cp_postgrest.keys"

extract_svccfg_names "${SVCCFG_DIR}/mcp.go" |
    sort -u >"${WORK}/svccfg_mcp.keys"
extract_svccfg_names "${SVCCFG_DIR}/rag.go" |
    sort -u >"${WORK}/svccfg_rag.keys"
extract_svccfg_names "${SVCCFG_DIR}/postgrest.go" |
    sort -u >"${WORK}/svccfg_postgrest.keys"

STATUS=0

report() { # label  cp_keys_file  svccfg_keys_file
    label=$1
    missing=$(comm -23 "$2" "$3")
    extra=$(comm -13 "$2" "$3")
    if [ -n "$missing" ]; then
        echo "${label}: svccfg is MISSING keys CP has at ${CP_TAG}:" >&2
        echo "$missing" | sed 's/^/  /' >&2
        STATUS=1
    fi
    if [ -n "$extra" ]; then
        echo "${label}: svccfg has keys CP does not have at ${CP_TAG}:" >&2
        echo "$extra" | sed 's/^/  /' >&2
        STATUS=1
    fi
}

report mcp "${WORK}/cp_mcp.keys" "${WORK}/svccfg_mcp.keys"
report rag "${WORK}/cp_rag.keys" "${WORK}/svccfg_rag.keys"
report postgrest "${WORK}/cp_postgrest.keys" "${WORK}/svccfg_postgrest.keys"

if [ "$STATUS" -eq 0 ]; then
    echo "cp-service-keys: internal/controlplane/svccfg matches CP at ${CP_TAG}"
fi
exit "$STATUS"
