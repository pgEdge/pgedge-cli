package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

func TestLoadSpecBytesDecodesSnakeCase(t *testing.T) {
	var spec api.DatabaseSpec2
	raw := []byte("database_name: testdb\nnodes: []\n")
	if err := loadSpecBytes(raw, &spec); err != nil {
		t.Fatalf("loadSpecBytes: %v", err)
	}
	if spec.DatabaseName != "testdb" {
		t.Errorf("database_name = %q, want testdb", spec.DatabaseName)
	}
}

// wrappedGetDocument is what `database get -o yaml` emits: the whole
// Database, with the spec nested under a spec: key. Written as literal
// YAML rather than built from a generated type so the test still
// describes the real wire shape if those types are renumbered.
const wrappedGetDocument = `id: storefront
state: available
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
tenant_id: t-1
spec:
    database_name: storefront
    nodes:
        - name: n1
          host_ids: [host-1]
`

// TestLoadSpecUnwrapsGetDocument is the round-trip the help text
// promises. update -f must accept the output of get -o yaml unchanged.
func TestLoadSpecUnwrapsGetDocument(t *testing.T) {
	var spec api.DatabaseSpec5
	if err := decodeSpecInto(
		[]byte(wrappedGetDocument), &spec, true); err != nil {
		t.Fatalf("decode wrapped document: %v", err)
	}
	if spec.DatabaseName != "storefront" {
		t.Errorf("database_name = %q, want storefront",
			spec.DatabaseName)
	}
	if len(spec.Nodes) != 1 || spec.Nodes[0].Name != "n1" {
		t.Errorf("nodes = %+v, want one node n1", spec.Nodes)
	}
}

// TestLoadSpecStillAcceptsBareSpec pins that adding the unwrap did not
// break the plain case. Without this, unwrapping could regress every
// hand-written spec file and the test above would still pass.
func TestLoadSpecStillAcceptsBareSpec(t *testing.T) {
	bare := "database_name: storefront\n" +
		"nodes:\n  - name: n1\n    host_ids: [host-1]\n"
	var spec api.DatabaseSpec5
	if err := decodeSpecInto([]byte(bare), &spec, true); err != nil {
		t.Fatalf("decode bare spec: %v", err)
	}
	if spec.DatabaseName != "storefront" || len(spec.Nodes) != 1 {
		t.Errorf("bare spec did not decode: %+v", spec)
	}
}

// TestRestoreDoesNotUnwrap pins the scoping decision. restore -f
// decodes a RestoreDatabaseRequest, which has no spec field, so the
// unwrap must not reach it. If unwrap ever moves into the shared
// loader this fails.
func TestRestoreDoesNotUnwrap(t *testing.T) {
	// A document with a spec: key and a sibling restore_config. With
	// unwrapping the restore_config would be discarded.
	raw := []byte("spec:\n    database_name: decoy\n" +
		"restore_config:\n" +
		"    source_database_id: src\n" +
		"    source_database_name: northwind\n" +
		"    source_node_name: n1\n" +
		"    repository:\n        type: s3\n        id: repo1\n")
	var body api.RestoreDatabaseRequest
	err := loadSpecBytes(raw, &body)
	// The spec: key is unknown to RestoreDatabaseRequest, so strict
	// decoding rejects it. What must NOT happen is a silent unwrap
	// that throws restore_config away.
	if err == nil {
		t.Fatal("expected strict decode to reject the unknown spec: key")
	}
	if !strings.Contains(err.Error(), "spec") {
		t.Errorf("error should name the unknown field, got: %v", err)
	}
}

// TestStrictDecodeRejectsUnknownFields covers the typo case: a key
// that matches nothing must fail locally rather than decode to an
// all-zero spec that gets sent as an empty update.
func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	var spec api.DatabaseSpec2
	raw := []byte("database_name: testdb\nnodes: []\nnodez: oops\n")
	err := loadSpecBytes(raw, &spec)
	if err == nil {
		t.Fatal("expected an error for the unknown field nodez")
	}
	if !strings.Contains(err.Error(), "nodez") {
		t.Errorf("error should name nodez, got: %v", err)
	}
}

// TestRequireSpecContent covers the guard that catches a file which
// parsed fine but carried nothing, including the comments-only case.
func TestRequireSpecContent(t *testing.T) {
	tests := []struct {
		name    string
		dbName  string
		nodes   int
		wantErr bool
	}{
		{name: "populated", dbName: "storefront", nodes: 1},
		{name: "no database_name", dbName: "", nodes: 1, wantErr: true},
		{name: "no nodes", dbName: "storefront", wantErr: true},
		{name: "empty", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireSpecContent("spec.yaml", tt.dbName, tt.nodes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), "spec.yaml") {
				t.Errorf("error should name the file, got: %v", err)
			}
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code() != ExitUsage {
				t.Errorf("want ExitUsage, got %v", err)
			}
		})
	}
}

// TestRequireRestoreContentRejectsCommentsOnlyTemplate feeds the real
// `database restore template` output through the guard. That template
// is entirely comments, so it parses to nothing — the exact input a
// user gets by redirecting the template and forgetting to edit it.
func TestRequireRestoreContentRejectsCommentsOnlyTemplate(t *testing.T) {
	var body api.RestoreDatabaseRequest
	if err := loadSpecBytes(
		[]byte(buildRestoreTemplate()), &body); err != nil {
		t.Fatalf("template should still parse: %v", err)
	}
	err := requireRestoreContent("restore.yaml", body.RestoreConfig)
	if err == nil {
		t.Fatal("unedited restore template was accepted")
	}
	if !strings.Contains(err.Error(), "restore.yaml") {
		t.Errorf("error should name the file, got: %v", err)
	}
}

// TestRequireRestoreContentAcceptsFilledSpec is the positive half: a
// real restore_config must pass, or the guard above would be satisfied
// by rejecting everything.
func TestRequireRestoreContentAcceptsFilledSpec(t *testing.T) {
	cfg := api.RestoreConfigSpec{
		SourceDatabaseId:   "src",
		SourceDatabaseName: "northwind",
		SourceNodeName:     "n1",
	}
	if err := requireRestoreContent("restore.yaml", cfg); err != nil {
		t.Errorf("filled restore config rejected: %v", err)
	}
}

// TestTemplateUsesOneSentinelSpelling pins the "one spelling per value"
// standard. Before this, buildSpec emitted `password: change-me` while
// everything else used the CHANGE-ME constant — two spellings of one
// placeholder, so a check for unfilled values could only ever catch one
// of them.
//
// The absent-half is the half that bites: asserting only that
// CHANGE-ME appears passes just as happily on a template that still
// says `change-me` somewhere else.
//
// buildRestoreTemplate is checked for the lowercase spelling but not
// for the sentinel's presence: it is entirely comments and illustrates
// values with realistic examples ("source_database_id: source-db")
// rather than placeholders. Only the interviewed path puts a sentinel
// in a restore spec, via orChangeMe.
func TestTemplateUsesOneSentinelSpelling(t *testing.T) {
	lower := strings.ToLower(secretSentinel)
	if lower == secretSentinel {
		t.Fatal("sentinel has no case to distinguish; test is vacuous")
	}

	specTemplate := buildSpec(specValues{})
	if !strings.Contains(specTemplate, secretSentinel) {
		t.Errorf("spec template never uses %q:\n%s",
			secretSentinel, specTemplate)
	}
	// The password line specifically — the one that used to differ.
	if !strings.Contains(specTemplate,
		"password: "+secretSentinel) {
		t.Errorf("password placeholder is not %q:\n%s",
			secretSentinel, specTemplate)
	}

	for name, got := range map[string]string{
		"spec template":    specTemplate,
		"restore template": buildRestoreTemplate(),
	} {
		if strings.Contains(got, lower) {
			t.Errorf("%s uses the lowercase spelling %q:\n%s",
				name, lower, got)
		}
	}
}

// TestSpecDecodeErrorRewording covers the message polish: encoding/json
// says `json: unknown field "x"` for a file the user wrote as YAML,
// naming a format they never used. The field name must survive the
// rewrite, since it is the only actionable part.
func TestSpecDecodeErrorRewording(t *testing.T) {
	tests := []struct {
		name       string
		in         error
		wantHas    []string
		wantHasNot []string
	}{
		{
			name:       "unknown field keeps the field name",
			in:         errors.New(`json: unknown field "backupconfig"`),
			wantHas:    []string{`"backupconfig"`, "unrecognized field"},
			wantHasNot: []string{"json:"},
		},
		{
			name:       "other json errors lose only the prefix",
			in:         errors.New("json: cannot unmarshal string into int"),
			wantHas:    []string{"cannot unmarshal string into int"},
			wantHasNot: []string{"json:"},
		},
		{
			name:       "unrelated errors pass through",
			in:         errors.New("yaml: line 3: bad indent"),
			wantHas:    []string{"yaml: line 3: bad indent"},
			wantHasNot: []string{"unrecognized field"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := specDecodeError(tt.in)
			for _, w := range tt.wantHas {
				if !strings.Contains(got, w) {
					t.Errorf("%q missing %q", got, w)
				}
			}
			for _, w := range tt.wantHasNot {
				if strings.Contains(got, w) {
					t.Errorf("%q should not contain %q", got, w)
				}
			}
		})
	}
}
