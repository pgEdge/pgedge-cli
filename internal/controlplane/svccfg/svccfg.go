// Package svccfg is a hand-written mirror of the Control Plane's
// per-service-type config key sets (mcp, rag, postgrest) at the CP
// release KeysFloor names.
//
// It cannot be generated. The vendored openapi/control-plane.json
// models every service's `config` as an opaque additionalProperties map
// (ServiceSpec…ServiceSpec8), and CP's key maps live in
// github.com/pgEdge/control-plane/server/internal/database, another
// module's `internal`, which Go will not let this module import. So
// this package GATES the `database init` template against CP's key sets
// rather than generating the template from them (decided 2026-08-08).
// Each struct cites its CP file:line so it can be diffed by eye;
// each KeyMeta states what CP requires or rejects around that key.
//
// KEEPING THIS IN SYNC IS MANUAL: bumping CP_TAG in openapi/SOURCE does
// not re-derive it. Before bumping, read the new tag with `git show`,
// not a checkout's working tree, and:
//
//  1. Re-read server/internal/database/{mcp,rag,postgrest}_service_config.go.
//  2. Run scripts/cp-service-keys.sh <control-plane checkout> and
//     reconcile the drift it reports in this package.
//  3. Update KeysFloor and every struct's provenance comment.
//  4. Re-run internal/clitest's TestInitTemplateNamesOnlyKnownServiceKeys
//     and TestInitTemplateDocumentsEveryRequiredKey, then fix
//     internal/controlplane/cmd/database_spec_template.go for any shape change.
//
// Structs rather than a flat key list or a map[string]bool, for two
// reasons:
//
//   - TestSkillDocsFieldClaimsMatchStructs
//     (internal/clitest/field_claims_test.go) takes the field names a
//     controlplane-scoped doc may backtick from the json and yaml tags
//     of every .go file under internal/controlplane. Map keys carry no
//     tags, so only as structs are these keys in that set, letting
//     llms.txt and the controlplane skill write `init_token`,
//     `kb_enabled` and `db_anon_role` with no doc-gate marker per key.
//   - CP decodes RAG's config tree with
//     json.Decoder.DisallowUnknownFields at every level, not just the
//     two top-level keys. Only a typed mirror says "these fields and
//     no others" at depth; a flat set could gate the top two names and
//     nothing deeper.
package svccfg

// KeysFloor is the Control Plane release this package's key sets were
// read from. It must equal cpcmd.SupportFloor
// (internal/controlplane/cmd/floor.go), not merely move with it:
// TestServiceConfigKeysFloorMatchesSupportFloor (internal/clitest) fails
// when they differ.
const KeysFloor = "0.10.0"

// KeyMeta describes one service config key as CP validates it. Name is
// the exact wire key (the json tag, lowercase, snake_case). Required
// without ConditionalOn is an unconditional requirement; ConditionalOn
// names another key in the same service whose truthy value makes this
// key required, or, for the LLM/KB gate keys, makes setting it valid at
// all. "Conditional" covers a few different CP rules that one bool
// cannot distinguish, so Doc says which applies to this key. Secret
// marks a key the template must render as the CHANGE-ME sentinel, never
// a real value. BootstrapOnly marks a key CP rejects on update. Enum
// lists the allowed values where CP restricts them; nil means free-form.
type KeyMeta struct {
	Name          string
	Required      bool
	ConditionalOn string
	Secret        bool
	BootstrapOnly bool
	Enum          []string
	Doc           string
}

// Keys returns the known config keys for serviceType ("mcp", "rag" or
// "postgrest") in stable, source-cited order, never map order. An
// unrecognised serviceType returns nil, so a caller that must reject an
// unknown type checks len == 0 itself, as the template gates do.
//
// "rag" flattens the whole pipeline tree, so a name loses its nesting:
// "provider" and "model" are valid only under embedding_llm/rag_llm.
// That gates "does the template invent an unknown token" and no more;
// rag.go's RAGConfig tree is the authoritative shape.
func Keys(serviceType string) []KeyMeta {
	switch serviceType {
	case "mcp":
		return append([]KeyMeta(nil), mcpKeys...)
	case "rag":
		return append([]KeyMeta(nil), ragKeys...)
	case "postgrest":
		return append([]KeyMeta(nil), postgrestKeys...)
	default:
		return nil
	}
}

// KnownKeyNames returns the set of valid key names for serviceType, for
// callers that only need membership testing.
func KnownKeyNames(serviceType string) map[string]bool {
	names := make(map[string]bool)
	for _, k := range Keys(serviceType) {
		names[k.Name] = true
	}
	return names
}

// RequiredKeyNames returns, in Keys order, every key with Required set,
// conditional ones included; filter on ConditionalOn == "" for the
// unconditional subset. The template must mark each REQUIRED, a
// conditional one inside the sub-block that states its condition.
func RequiredKeyNames(serviceType string) []string {
	var out []string
	for _, k := range Keys(serviceType) {
		if k.Required {
			out = append(out, k.Name)
		}
	}
	return out
}
