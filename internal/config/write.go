package config

import (
	"bytes"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultIndent is what a document with nothing to copy is written with.
// It is 4 to match gopkg.in/yaml.v3's own Marshal, so such a document
// is indented as a new file is.
const defaultIndent = 4

// render returns the bytes to write for c, given whatever is currently
// on disk at the destination (empty for a new file).
//
// A plain yaml.Marshal of the struct is destructive in two ways:
//
//   - Comments cannot survive a struct round trip, and
//     examples/config.yaml, whose comments are the setup instructions,
//     tells the user to copy it to ~/.pgedge/cli/config.yaml. Measured
//     with a plain marshal: 8 comment lines in, 0 out, after one
//     `pgedge profile use`.
//   - Config has no field for a key it does not know, so yaml.Unmarshal
//     drops it and an older binary would silently delete a newer
//     binary's section.
//
// So the existing document is parsed into a yaml.Node and only the parts
// the struct describes are applied to it. Comments, key order,
// indentation and unknown keys are left as the user wrote them.
//
// Two cosmetic things are NOT preserved:
//
//   - A blank line not attached to a comment. yaml.v3 keeps a blank line
//     only inside a node's HeadComment, so the encoder drops a bare
//     separator between two keys. Fixing that means not using yaml.v3's
//     emitter.
//   - Runs of spaces before an inline comment: `a: b   # note` re-emits
//     as `a: b # note`.
//
// Measured on the scenario in write_test.go: 8 comment lines in, 8 out,
// both unknown keys kept, 655 bytes to 636 — the 19 lost bytes are those
// blank separators.
func (c *Config) render(existing []byte) ([]byte, error) {
	if len(bytes.TrimSpace(existing)) == 0 {
		return yaml.Marshal(c)
	}

	var have yaml.Node
	if err := yaml.Unmarshal(existing, &have); err != nil {
		// An unparseable file has no structure to preserve. Refusing to
		// save would lose the work this write records (a completed login,
		// a token cleared) to protect bytes that are already broken.
		return yaml.Marshal(c)
	}

	raw, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var want yaml.Node
	if err := yaml.Unmarshal(raw, &want); err != nil {
		return nil, err
	}

	haveRoot, wantRoot := documentRoot(&have), documentRoot(&want)
	if haveRoot == nil || wantRoot == nil ||
		haveRoot.Kind != yaml.MappingNode ||
		wantRoot.Kind != yaml.MappingNode {
		// The file holds something that is valid yaml but not a mapping
		// (a bare list, a scalar). There is no key structure to merge
		// into, so replace it.
		return raw, nil
	}
	mergeMapping(haveRoot, wantRoot, configSchema)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndent(existing))
	if err := enc.Encode(&have); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// documentRoot unwraps a document node to the value it contains.
// yaml.Unmarshal into a *yaml.Node yields a DocumentNode; a node built
// by hand may already be the mapping.
func documentRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

// mergeMapping applies want onto have in place, guided by sc.
//
//   - A key in both: recurse, so deeper comments survive too.
//   - A key only in want: append it at the end, not in marshal order,
//     so a one-line change stays a one-line diff.
//   - A key only in have: if sc says the struct OWNS the key, its
//     absence is deliberate and it is dropped. `auth logout` writes a
//     StarfleetProfile carrying only APIURL, and client_secret is
//     `omitempty`, so omission is how logout deletes a stored secret;
//     keeping it would leave a long-lived credential on disk. A key the
//     struct does not know stays.
//
// Getting that backwards either way is a bug: delete everything and
// unknown keys vanish; delete nothing and logout stops removing the
// secret.
func mergeMapping(have, want *yaml.Node, sc *schema) {
	wantValues := make(map[string]*yaml.Node, len(want.Content)/2)
	wantOrder := make([]string, 0, len(want.Content)/2)
	for i := 0; i+1 < len(want.Content); i += 2 {
		key := want.Content[i].Value
		wantValues[key] = want.Content[i+1]
		wantOrder = append(wantOrder, key)
	}

	merged := make([]*yaml.Node, 0, len(have.Content))
	seen := make(map[string]bool, len(have.Content)/2)
	for i := 0; i+1 < len(have.Content); i += 2 {
		keyNode, valNode := have.Content[i], have.Content[i+1]
		key := keyNode.Value
		seen[key] = true

		wantVal, inWant := wantValues[key]
		if !inWant {
			if sc.owns(key) {
				continue // deliberately omitted: drop it
			}
			merged = append(merged, keyNode, valNode) // not ours: keep
			continue
		}
		mergeNode(valNode, wantVal, sc.child(key))
		merged = append(merged, keyNode, valNode)
	}

	for _, key := range wantOrder {
		if seen[key] {
			continue
		}
		merged = append(merged,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			wantValues[key])
	}
	have.Content = merged
}

// mergeNode applies want onto have for one value position.
//
// Two mappings recurse. Anything else takes want's content but keeps
// have's comments, which document the position rather than the value:
// `api_url: https://... # optional override` keeps its comment when the
// URL changes.
func mergeNode(have, want *yaml.Node, sc *schema) {
	if have.Kind == yaml.MappingNode && want.Kind == yaml.MappingNode {
		mergeMapping(have, want, sc)
		return
	}
	head, line, foot := have.HeadComment, have.LineComment, have.FootComment
	*have = *want
	have.HeadComment, have.LineComment, have.FootComment = head, line, foot
}

// schema describes which yaml keys this version's structs own at each
// level of the document. It is the delete rule's authority: owns(key)
// answers "would a marshal of our own types ever produce this key?", and
// only a key we would produce may be deleted for being absent.
type schema struct {
	// keys maps an owned key to the schema of its value. A nil value
	// means a leaf (scalar or something with no key structure).
	keys map[string]*schema
	// anyKey is non-nil for a mapping with user-defined keys — a Go
	// map[string]T, such as Config.Profiles, where every key is ours.
	anyKey *schema
}

// owns reports whether a marshal of this version's types could produce
// key at this level.
func (s *schema) owns(key string) bool {
	if s == nil {
		return false
	}
	if s.anyKey != nil {
		return true
	}
	_, ok := s.keys[key]
	return ok
}

// child returns the schema for key's value, or nil when there is none to
// descend into.
func (s *schema) child(key string) *schema {
	if s == nil {
		return nil
	}
	if s.anyKey != nil {
		return s.anyKey
	}
	return s.keys[key]
}

// configSchema is the ownership map for the whole document, derived by
// reflection so a new section added to Profile is owned automatically; a
// hand list that forgot it would stop deleting its omitted values.
var configSchema = schemaOf(reflect.TypeOf(Config{}))

// schemaOf builds the ownership map for a Go type. Structs contribute
// their yaml key names, maps contribute a wildcard, everything else is a
// leaf. It assumes a non-recursive type, which Config is.
func schemaOf(t reflect.Type) *schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		s := &schema{keys: map[string]*schema{}}
		for i := range t.NumField() {
			f := t.Field(i)
			// Unexported fields are invisible to yaml, so they are not
			// document keys. Config.path is the live example.
			if f.PkgPath != "" {
				continue
			}
			name := yamlKeyName(f)
			if name == "" || name == "-" {
				continue
			}
			s.keys[name] = schemaOf(f.Type)
		}
		return s
	case reflect.Map:
		return &schema{anyKey: schemaOf(t.Elem())}
	default:
		return nil
	}
}

// yamlKeyName returns the document key a struct field marshals to,
// applying the same rules gopkg.in/yaml.v3 does: the name from the yaml
// tag when present, otherwise the field name lowercased.
func yamlKeyName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

// detectIndent returns the indentation width the existing document uses.
// yaml.v3's encoder defaults to 4 and the repo's own example is 2, so
// without this a one-key change reindents the whole file. It samples the
// first indented data line; a comment's indentation is often not the
// document's.
func detectIndent(raw []byte) int {
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if n := len(line) - len(trimmed); n > 0 {
			return n
		}
	}
	return defaultIndent
}
