package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// scenario is a comment-heavy config carrying keys this version does not
// know: a top-level `telemetry:` and a per-profile `byoc:` section (the
// latter is real — PR #51 deleted the shim that used to read it).
//
// Every comment position matters and each one is a different yaml.Node
// field: a document head comment, a head comment above a key, an inline
// comment on a value, and a comment inside a nested mapping.
const scenario = `# pgEdge CLI configuration.
# Copy to ~/.pgedge/cli/config.yaml (mode 0600).

current_profile: default

# Unknown to this version. A later release adds it; this one must not
# eat it.
telemetry:
  enabled: false

profiles:
  default:
    starfleet:
      api_url: https://api.pgedge.com   # optional override
      client_id: stored-id
      client_secret: stored-secret
  staging:
    # Which cluster this profile drives. The comment IS the
    # documentation; there is nowhere else it lives.
    controlplane:
      base_url: https://cp.staging.internal:8443
    byoc:
      client_id: legacy-still-on-disk
output:
  format: text
`

// writeScenario plants the scenario at a temp path and returns it.
func writeScenario(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readBack returns the file's contents as a string.
func readBack(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestWritePreservesComments is the visible half of the bug. Measured on
// the pre-fix code: 8 comment lines before `pgedge profile use`, 0
// after. The repo ships examples/config.yaml, whose comments ARE its
// setup instructions, and tells the user to copy it to
// ~/.pgedge/cli/config.yaml — so the loss lands on documentation that has
// nowhere else to live.
func TestWritePreservesComments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCurrentProfile("staging"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got := readBack(t, path)
	for _, want := range []string{
		"# pgEdge CLI configuration.",            // document head
		"# Copy to ~/.pgedge/cli/config.yaml",    // second head line
		"# Unknown to this version.",             // head above a key
		"# optional override",                    // inline on a value
		"# Which cluster this profile drives.",   // inside a mapping
		"# documentation; there is nowhere else", // continuation line
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comment %q did not survive the write:\n%s",
				want, got)
		}
	}
	// And the write must actually have happened.
	if !strings.Contains(got, "current_profile: staging") {
		t.Errorf("the change itself was not written:\n%s", got)
	}
}

// TestWritePreservesUnknownKeys is the forward-compatibility half. It
// goes live the moment `managed:` ships: a binary that predates the
// section must not silently delete it from a config written by one that
// has it.
func TestWritePreservesUnknownKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCurrentProfile("staging"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got := readBack(t, path)
	for _, want := range []string{
		"telemetry:",           // unknown top-level key
		"enabled: false",       //   and its content
		"byoc:",                // unknown section inside a profile
		"legacy-still-on-disk", //   and its content
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unknown key %q was dropped by the write:\n%s",
				want, got)
		}
	}
}

// TestWriteDeletesOmittedKnownKeys is the one that stops this fix from
// becoming a security bug.
//
// `starfleet auth logout` clears the stored credentials by writing a
// StarfleetProfile carrying only APIURL. client_id and client_secret are
// `omitempty`, so they simply vanish from the marshalled output — and a
// writer that merged into the existing document and never deleted
// anything would leave the old secret on disk. The whole point of that
// code path is that a long-lived secret must not outlive the session.
//
// So the rule is not "never delete". It is: a key this version's structs
// OWN and did not emit was omitted deliberately and must go; a key the
// structs do not know about must stay.
func TestWriteDeletesOmittedKnownKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly what internal/starfleet/account/cmd's logout does.
	kept := cfg.StarfleetProfile("default")
	cfg.SetStarfleetProfile("default", &StarfleetProfile{APIURL: kept.APIURL})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got := readBack(t, path)
	if strings.Contains(got, "stored-secret") {
		t.Errorf("client_secret survived logout — a long-lived secret "+
			"outlived the session:\n%s", got)
	}
	if strings.Contains(got, "stored-id") {
		t.Errorf("client_id survived logout:\n%s", got)
	}
	// The api_url is deliberately kept, so the next login needs no
	// endpoint re-entered.
	if !strings.Contains(got, "https://api.pgedge.com") {
		t.Errorf("api_url should have been kept:\n%s", got)
	}
	// And the unknown key is still not collateral damage.
	if !strings.Contains(got, "telemetry:") {
		t.Errorf("deleting owned keys also ate an unknown one:\n%s", got)
	}
	// Prove it through Load too, not just the bytes.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if ap := reloaded.StarfleetProfile("default"); ap.ClientSecret != "" ||
		ap.ClientID != "" {
		t.Errorf("reloaded credentials = %+v, want both empty", ap)
	}
}

// A file that does not exist yet has no comments to preserve, so the
// write is a plain marshal — and must still land at 0600 inside a 0700
// directory, since it carries credentials.
func TestWriteNewFileIsPlainMarshalAt0600(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".pgedge", "config.yaml")
	cfg := &Config{
		CurrentProfile: "default",
		Profiles: map[string]Profile{
			"default": {Starfleet: &StarfleetProfile{ClientID: "id"}},
		},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); !strings.Contains(got, "client_id: id") {
		t.Errorf("new file = %q, want the marshalled config", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 600", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perms = %o, want 700", perm)
	}
}

// An existing file that cannot be parsed must not make Save fail: the
// caller has already done the work the save records, and refusing to
// write would lose it. The merge is an enhancement over marshalling, so
// its unavailability degrades to the old behaviour.
func TestWriteFallsBackWhenExistingIsUnparseable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(
		path, []byte("not: [valid: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{CurrentProfile: "default"}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("Save must not fail on an unparseable existing "+
			"file: %v", err)
	}
	got := readBack(t, path)
	if !strings.Contains(got, "current_profile: default") {
		t.Errorf("file = %q, want the marshalled config", got)
	}
	if strings.Contains(got, "not: [valid") {
		t.Errorf("file = %q, kept the unparseable content", got)
	}
}

// Indentation is preserved, so a write does not reformat the whole file
// into an unreviewable diff. The scenario is 2-space; yaml.v3's encoder
// defaults to 4 and would silently reindent every line.
func TestWritePreservesIndentation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCurrentProfile("staging"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got := readBack(t, path)
	if !strings.Contains(got, "\n  default:") {
		t.Errorf("2-space indentation was not preserved:\n%s", got)
	}
	if strings.Contains(got, "\n    default:") {
		t.Errorf("the file was reindented to 4 spaces:\n%s", got)
	}
}

// Key order survives, and a key the document does not have yet is
// appended rather than reordering everything around it.
func TestWritePreservesKeyOrderAndAppendsNew(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetControlplaneProfile("default", &ControlplaneProfile{BaseURL: "https://new:8443"})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got := readBack(t, path)

	// Original order: current_profile, telemetry, profiles, output.
	idx := func(s string) int { return strings.Index(got, s) }
	for _, pair := range [][2]string{
		{"current_profile:", "telemetry:"},
		{"telemetry:", "profiles:"},
		{"profiles:", "output:"},
	} {
		if idx(pair[0]) >= idx(pair[1]) {
			t.Errorf("%s should still precede %s:\n%s",
				pair[0], pair[1], got)
		}
	}
	// The new cp section for the default profile must be there, and the
	// cloud section it sits beside must be untouched.
	if !strings.Contains(got, "https://new:8443") {
		t.Errorf("the new cp section was not written:\n%s", got)
	}
	if !strings.Contains(got, "client_id: stored-id") {
		t.Errorf("adding a section disturbed a sibling:\n%s", got)
	}
}

// The bytes are not the only contract: what Load reads back must equal
// what was set. A merge that produced comment-perfect but semantically
// wrong yaml would pass every assertion above.
func TestWriteRoundTripsThroughLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeScenario(t)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCurrentProfile("staging"); err != nil {
		t.Fatal(err)
	}
	cfg.SetStarfleetProfile("staging", &StarfleetProfile{
		APIURL: "https://staging.example", ClientID: "sid",
		ClientSecret: "ssecret",
	})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	if got.CurrentProfile != "staging" {
		t.Errorf("CurrentProfile = %q, want staging", got.CurrentProfile)
	}
	ap := got.StarfleetProfile("staging")
	if ap.APIURL != "https://staging.example" || ap.ClientID != "sid" ||
		ap.ClientSecret != "ssecret" {
		t.Errorf("staging cloud = %+v, want the values just set", ap)
	}
	// The untouched profile is still intact.
	if d := got.StarfleetProfile("default"); d.ClientID != "stored-id" {
		t.Errorf("default profile = %+v, want it untouched", d)
	}
	if got.Output.Format != "text" {
		t.Errorf("output.format = %q, want text", got.Output.Format)
	}
	if cp := got.ControlplaneProfile("staging"); cp.BaseURL !=
		"https://cp.staging.internal:8443" {
		t.Errorf("staging cp = %+v, want it untouched", cp)
	}
}

// TestSchemaOfConfig pins the ownership map the delete rule depends on.
// If a new section is added to Profile and this schema does not learn
// about it, an omitted value of that section stops being deleted — which
// is the credential-survives-logout failure, one level down.
func TestSchemaOfConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := configSchema

	for _, key := range []string{"current_profile", "profiles", "output"} {
		if !root.owns(key) {
			t.Errorf("root schema does not own %q", key)
		}
	}
	if root.owns("telemetry") {
		t.Error("root schema claims to own the unknown key telemetry")
	}
	// The unexported path field must not become a yaml key.
	if root.owns("path") {
		t.Error("root schema owns the unexported path field")
	}

	// profiles is a map, so every profile name is owned.
	profiles := root.child("profiles")
	if !profiles.owns("anything-at-all") {
		t.Error("profiles should own every key: it is a map")
	}

	prof := profiles.child("whatever")
	for _, key := range []string{"starfleet", "controlplane"} {
		if !prof.owns(key) {
			t.Errorf("profile schema does not own %q", key)
		}
	}
	if prof.owns("byoc") {
		t.Error("profile schema claims to own byoc, deleted in #51")
	}

	cloud := prof.child("starfleet")
	for _, key := range []string{"api_url", "client_id", "client_secret"} {
		if !cloud.owns(key) {
			t.Errorf("cloud schema does not own %q", key)
		}
	}
}

// A config file that is valid yaml but not a mapping has no key
// structure to merge into. Replacing it is the only sane outcome — and
// it must not panic on the way, which a merge assuming MappingNode
// would.
func TestWriteReplacesNonMappingDocument(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name, existing string
	}{
		{name: "bare sequence", existing: "- one\n- two\n"},
		{name: "bare scalar", existing: "just a string\n"},
		// Comments with no keys: parses to an empty document, so there
		// is nothing to merge and nothing to preserve either.
		{name: "comments only", existing: "# nothing but a note\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(
				path, []byte(tc.existing), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{CurrentProfile: "default"}
			if err := cfg.SaveTo(path); err != nil {
				t.Fatalf("SaveTo: %v", err)
			}
			got := readBack(t, path)
			if !strings.Contains(got, "current_profile: default") {
				t.Errorf("file = %q, want the marshalled config", got)
			}
			// And it must still load.
			if _, err := Load(path); err != nil {
				t.Errorf("the written file does not load: %v", err)
			}
		})
	}
}

// The schema helpers' fallbacks: a nil schema is what descending into a
// leaf yields, and it must own nothing rather than panic — owning
// something there would delete document content under a scalar key.
func TestSchemaHelperFallbacks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var nilSchema *schema
	if nilSchema.owns("anything") {
		t.Error("a nil schema must own nothing")
	}
	if nilSchema.child("anything") != nil {
		t.Error("a nil schema must have no children")
	}
	// A leaf reached through a real schema behaves the same way.
	leaf := configSchema.child("current_profile")
	if leaf.owns("anything") {
		t.Error("a scalar's schema must own nothing")
	}
}

// yamlKeyName mirrors yaml.v3's own rules. The untagged and
// empty-name-in-tag paths are unreachable from Config today; they are
// tested because the moment a field is added without a tag, the fallback
// decides whether that field's key can be deleted — and a wrong answer
// there is the credential-survives-logout bug in a new place.
func TestYamlKeyName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	type sample struct {
		Tagged    string `yaml:"tagged_name,omitempty"`
		OnlyOpts  string `yaml:",omitempty"`
		Untagged  string
		Skipped   string `yaml:"-"`
		MixedCase string `yaml:"MixedKey"`
	}
	want := map[string]string{
		"Tagged":    "tagged_name",
		"OnlyOpts":  "onlyopts",
		"Untagged":  "untagged",
		"Skipped":   "-",
		"MixedCase": "MixedKey",
	}
	rt := reflect.TypeOf(sample{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		if got := yamlKeyName(f); got != want[f.Name] {
			t.Errorf("yamlKeyName(%s) = %q, want %q",
				f.Name, got, want[f.Name])
		}
	}
	// And a tag of "-" must not become an owned key.
	s := schemaOf(rt)
	if s.owns("-") || s.owns("Skipped") || s.owns("skipped") {
		t.Error("a yaml:\"-\" field must not be an owned key")
	}
}

func TestDetectIndent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "two space", raw: "a:\n  b: 1\n", want: 2},
		{name: "four space", raw: "a:\n    b: 1\n", want: 4},
		{
			// Comments and blank lines must not be sampled: a comment
			// indented differently from the data is common.
			name: "skips comments and blanks",
			raw:  "a:\n\n   # indented comment\n  b: 1\n",
			want: 2,
		},
		{
			name: "flat document falls back to the default",
			raw:  "a: 1\nb: 2\n",
			want: defaultIndent,
		},
		{name: "empty falls back", raw: "", want: defaultIndent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectIndent([]byte(tt.raw)); got != tt.want {
				t.Errorf("detectIndent(%q) = %d, want %d",
					tt.raw, got, tt.want)
			}
		})
	}
}
