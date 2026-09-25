package cmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

// Valid UUIDs so the generated parser can decode Task.TaskId, which
// is an openapi_types.UUID — an invalid value fails json.Unmarshal
// and CreateDatabaseWithResponse returns an error before the command
// can report success.
const createTaskID = "11111111-1111-1111-1111-111111111111"
const updateTaskID = "22222222-2222-2222-2222-222222222222"

const createResp = `{"task":{"task_id":"` + createTaskID + `",` +
	`"status":"pending","type":"create","scope":"database",` +
	`"entity_id":"storefront","created_at":"2025-06-18T00:00:00Z"},` +
	`"database":{"id":"storefront","state":"creating","created_at":` +
	`"2025-06-18T00:00:00Z","updated_at":"2025-06-18T00:00:00Z"}}`

const updateResp = `{"task":{"task_id":"` + updateTaskID + `",` +
	`"status":"pending","type":"update","scope":"database",` +
	`"entity_id":"storefront","created_at":"2025-06-18T00:00:00Z"},` +
	`"database":{"id":"storefront","state":"updating","created_at":` +
	`"2025-06-18T00:00:00Z","updated_at":"2025-06-18T00:00:00Z"}}`

const specYAML = `database_name: storefront
nodes:
  - name: n1
    host_ids: [host-1]
`

func TestDatabaseCreateFromFile(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	url := newServer(t, jsonHandler(200, createResp))
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(errb.String(), createTaskID) {
		t.Errorf("want task id in output: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "accepted") {
		t.Errorf("want accepted message: %q", errb.String())
	}
}

// TestDatabaseCreateSendsSnakeCaseBody guards the YAML->JSON->struct
// loader (Override O). The generated spec structs carry only json
// tags, so a plain yaml.Unmarshal would leave database_name empty and
// the server would receive "database_name":"". We capture the request
// body and assert the snake_case field arrived populated.
func TestDatabaseCreateSendsSnakeCaseBody(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	var gotBody string
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(gotBody, `"database_name":"storefront"`) {
		t.Errorf("request body missing database_name: %q", gotBody)
	}
	if !strings.Contains(gotBody, `"host_ids":["host-1"]`) {
		t.Errorf("request body missing host_ids: %q", gotBody)
	}
}

func TestDatabaseCreateMissingFileFlag(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, createResp))
	err := runControlplane(t, rt, out, url,
		"database", "create", "storefront")
	if err == nil {
		t.Fatal("expected error when -f is omitted")
	}
}

func TestDatabaseCreateMissingFile(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, createResp))
	err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", "/nope.yaml")
	if err == nil {
		t.Fatal("expected error for missing spec file")
	}
}

func TestDatabaseCreateStdin(t *testing.T) {
	rt, out, errb := newTestRuntime(t, specYAML, "text")
	url := newServer(t, jsonHandler(200, createResp))
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", "-"); err != nil {
		t.Fatalf("create stdin: %v", err)
	}
	if !strings.Contains(errb.String(), createTaskID) {
		t.Errorf("want task id in output: %q", errb.String())
	}
}

func TestDatabaseUpdateFromFile(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(specYAML), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	url := newServer(t, jsonHandler(200, updateResp))
	if err := runControlplane(t, rt, out, url,
		"database", "update", "storefront", "-f", specPath, "--force"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(errb.String(), updateTaskID) {
		t.Errorf("want task id in output: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "Update task") {
		t.Errorf("want update accepted message: %q", errb.String())
	}
}

func TestDatabaseUpdateMissingFileFlag(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, updateResp))
	err := runControlplane(t, rt, out, url,
		"database", "update", "storefront")
	if err == nil {
		t.Fatal("expected error when -f is omitted")
	}
}

func TestCheckUnfilledSecrets(t *testing.T) {
	sentinel := map[string]interface{}{"api_key": "CHANGE-ME"}
	filled := map[string]interface{}{"api_key": "sk-real"}
	svc := func(id string, cfg *map[string]interface{}) api.ServiceSpec2 {
		return api.ServiceSpec2{ServiceId: id, Config: cfg}
	}
	tests := []struct {
		name     string
		services *[]api.ServiceSpec2
		wantErr  bool
	}{
		{"nil services", nil, false},
		{"no config", &[]api.ServiceSpec2{svc("s1", nil)}, false},
		{"filled key", &[]api.ServiceSpec2{svc("s1", &filled)}, false},
		{"sentinel key", &[]api.ServiceSpec2{svc("mcp-1", &sentinel)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var services []serviceConfig
			if tc.services != nil {
				for _, sv := range *tc.services {
					services = append(services, serviceConfig{
						id: sv.ServiceId, config: sv.Config})
				}
			}
			err := checkUnfilledPlaceholders(
				nil, nil, nil, nil, services, "create")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				exitErr, ok := err.(*ExitError)
				if !ok || exitErr.code != ExitUsage {
					t.Fatalf("want ExitUsage, got %T %v", err, err)
				}
				if !strings.Contains(exitErr.msg, "mcp-1") {
					t.Errorf("error should name the service: %q",
						exitErr.msg)
				}
			} else if err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}

func TestCheckUnfilledSecretsNested(t *testing.T) {
	// A sentinel nested one level deep inside a config map is caught,
	// and the error names the dotted path plus the service id.
	nested := map[string]interface{}{
		"llm": map[string]interface{}{"api_key": "CHANGE-ME"},
	}
	err := checkUnfilledPlaceholders(nil, nil, nil, nil, []serviceConfig{
		{id: "mcp-1", config: &nested},
	}, "create")
	if err == nil {
		t.Fatal("want error for nested sentinel, got nil")
	}
	exitErr, ok := err.(*ExitError)
	if !ok || exitErr.code != ExitUsage {
		t.Fatalf("want ExitUsage, got %T %v", err, err)
	}
	if !strings.Contains(exitErr.msg, "mcp-1") ||
		!strings.Contains(exitErr.msg, "llm.api_key") {
		t.Errorf("error should name nested path: %q", exitErr.msg)
	}
}

func TestValidatePostgresVersions(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		name    string
		cluster *string
		nodes   []nodeVersion
		wantErr bool
		wantMsg string
	}{
		{"all unset", nil, nil, false, ""},
		{"empty string ignored", ptr(""), nil, false, ""},
		{"valid cluster", ptr("16.14"), nil, false, ""},
		{"valid two-digit major", ptr("17.6"), nil, false, ""},
		{"valid legacy", ptr("9.6"), nil, false, ""},
		{"bare major cluster", ptr("16"), nil, true, "postgres_version"},
		{"three-part cluster", ptr("16.14.1"), nil, true, "16.14.1"},
		{"non-numeric cluster", ptr("latest"), nil, true, "latest"},
		{
			"valid node override", nil,
			[]nodeVersion{{name: "n1", version: ptr("16.14")}},
			false, "",
		},
		{
			"bad node override", nil,
			[]nodeVersion{{name: "n2", version: ptr("17")}},
			true, `node "n2"`,
		},
		{
			"node unset ok", nil,
			[]nodeVersion{{name: "n1", version: nil}},
			false, "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePostgresVersions(tc.cluster, tc.nodes)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			exitErr, ok := err.(*ExitError)
			if !ok || exitErr.code != ExitUsage {
				t.Fatalf("want ExitUsage, got %T %v", err, err)
			}
			if !strings.Contains(exitErr.msg, tc.wantMsg) {
				t.Errorf("error %q should contain %q",
					exitErr.msg, tc.wantMsg)
			}
			if !strings.Contains(exitErr.msg, "major.minor") {
				t.Errorf("error should hint the format: %q", exitErr.msg)
			}
		})
	}
}

func TestCreateRejectsBadPostgresVersion(t *testing.T) {
	// A bare major postgres_version is caught before any API call
	// (127.0.0.1:0 would refuse a connection if one were attempted).
	spec := "database_name: db\npostgres_version: \"16\"\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "create", "storefront", "-f", path)
	requireUsageError(t, err)
}

func TestCreateRejectsBadNodePostgresVersion(t *testing.T) {
	spec := "database_name: db\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n" +
		"    postgres_version: \"17\"\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "create", "storefront", "-f", path)
	requireUsageError(t, err)
}

func TestUpdateRejectsBadPostgresVersion(t *testing.T) {
	spec := "database_name: db\npostgres_version: \"16\"\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "update", "storefront", "-f", path)
	requireUsageError(t, err)
}

func TestCreateRejectsUnfilledSecret(t *testing.T) {
	// A spec carrying the sentinel is rejected before any API call.
	spec := "database_name: db\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n" +
		"services:\n  - service_type: mcp\n    service_id: mcp-1\n" +
		"    connect_as: admin\n    host_ids: [h-1]\n" +
		"    version: latest\n    config:\n" +
		"      api_key: CHANGE-ME\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "create", "storefront", "-f", path)
	requireUsageError(t, err)
}

// TestCheckUnfilledPlaceholdersCoversTemplateFields is the half the
// original scan missed. `database init` writes CHANGE-ME into a node's
// host_ids and a database user's password — it cannot know either —
// and only service configs were ever checked, so an unedited template
// reached the API and came back with a server error naming nothing
// the user had written.
func TestCheckUnfilledPlaceholdersCoversTemplateFields(t *testing.T) {
	pw := func(s string) *string { return &s }
	sentinel := map[string]interface{}{"api_key": "CHANGE-ME"}

	tests := []struct {
		name     string
		nodes    []nodeHosts
		users    *[]api.DatabaseUserSpec
		repos    []api.BackupRepositorySpec
		services []serviceConfig
		wantErr  string
	}{
		{
			name:  "unedited host id",
			nodes: []nodeHosts{{hostIDs: []string{"CHANGE-ME"}}},
			// The index matters: a three-node template has three of
			// these, and "nodes[1]" is the difference between finding
			// it and re-reading the file.
			wantErr: "nodes[0].host_ids[0]",
		},
		{
			name: "unedited host id on a later node",
			nodes: []nodeHosts{
				{hostIDs: []string{"host-1"}},
				{hostIDs: []string{"host-2", "CHANGE-ME"}},
			},
			wantErr: "nodes[1].host_ids[1]",
		},
		{
			name:  "unedited password",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			users: &[]api.DatabaseUserSpec{
				{Username: "admin", Password: pw("CHANGE-ME")},
			},
			wantErr: "database_users[0].password",
		},
		{
			name:  "password on a later user",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			users: &[]api.DatabaseUserSpec{
				{Username: "admin", Password: pw("real")},
				{Username: "app", Password: pw("CHANGE-ME")},
			},
			wantErr: "database_users[1].password",
		},
		{
			name:     "unedited service secret still caught",
			nodes:    []nodeHosts{{hostIDs: []string{"host-1"}}},
			services: []serviceConfig{{id: "mcp-1", config: &sentinel}},
			wantErr:  "mcp-1",
		},
		{
			name:  "unedited s3 key",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			repos: []api.BackupRepositorySpec{
				{Id: pw("repo1"), S3Key: pw("CHANGE-ME")},
			},
			wantErr: "backup_config.repositories[0].s3_key",
		},
		{
			name:  "unedited s3 key secret on a later repository",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			repos: []api.BackupRepositorySpec{
				{Id: pw("repo1")},
				{Id: pw("repo2"), S3KeySecret: pw("CHANGE-ME")},
			},
			wantErr: "backup_config.repositories[1].s3_key_secret",
		},
		{
			name:  "unedited gcs key",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			repos: []api.BackupRepositorySpec{
				{Id: pw("repo1"), GcsKey: pw("CHANGE-ME")},
			},
			wantErr: "backup_config.repositories[0].gcs_key",
		},
		{
			name:  "unedited azure key",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			repos: []api.BackupRepositorySpec{
				{Id: pw("repo1"), AzureKey: pw("CHANGE-ME")},
			},
			wantErr: "backup_config.repositories[0].azure_key",
		},
		{
			// A repository with no credential at all is legitimate:
			// pgBackRest falls back to the instance credential chain.
			name:  "absent repository credential is not a placeholder",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			repos: []api.BackupRepositorySpec{
				{Id: pw("repo1"), S3Bucket: pw("b")},
			},
		},
		{
			name:  "unedited service host id",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			services: []serviceConfig{
				{id: "mcp-1", hostIDs: []string{"CHANGE-ME"}},
			},
			wantErr: `service "mcp-1" host_ids[0]`,
		},
		{
			// The list step configSentinelHit never took: pipelines
			// is a []interface{}, so this key sat two levels below
			// the single nested map the scan used to reach.
			name:  "unedited api key under a rag pipeline",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			services: []serviceConfig{{
				id: "rag-1",
				config: &map[string]interface{}{
					"pipelines": []interface{}{
						map[string]interface{}{"name": "docs"},
						map[string]interface{}{
							"name": "kb",
							"embedding_llm": map[string]interface{}{
								"api_key": "CHANGE-ME",
							},
						},
					},
				},
			}},
			wantErr: "config.pipelines[1].embedding_llm.api_key",
		},
		{
			// A user with no password at all is legitimate — the
			// field is optional and the CP generates one.
			name:  "absent password is not a placeholder",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			users: &[]api.DatabaseUserSpec{{Username: "admin"}},
		},
		{
			name:  "fully edited spec passes",
			nodes: []nodeHosts{{hostIDs: []string{"host-1"}}},
			users: &[]api.DatabaseUserSpec{
				{Username: "admin", Password: pw("s3cret")},
			},
			services: []serviceConfig{{id: "mcp-1", config: &map[string]interface{}{
				"api_key": "sk-real"}}},
		},
		{
			name: "empty spec passes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUnfilledPlaceholders(
				tc.nodes, tc.users, tc.repos, nil, tc.services, "create")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			exitErr, ok := err.(*ExitError)
			if !ok || exitErr.code != ExitUsage {
				t.Fatalf("want ExitUsage, got %T %v", err, err)
			}
			if !strings.Contains(exitErr.msg, tc.wantErr) {
				t.Errorf("error %q does not name %q",
					exitErr.msg, tc.wantErr)
			}
			// The sentinel is a placeholder, never a credential, so
			// quoting it is what makes the message actionable.
			if !strings.Contains(exitErr.msg, "CHANGE-ME") {
				t.Errorf("error does not quote the sentinel: %q",
					exitErr.msg)
			}
		})
	}
}

// TestCreateRejectsUnfilledPlaceholderAnywhereInTheSpec drives the
// scan through the real spec loader, which is where three gaps
// once lived: a spec carrying a CHANGE-ME in a backup
// repository credential, a service's host_ids, or a rag pipeline's
// nested api_key was sent to the API instead of refused locally.
//
// It runs against a port that cannot be dialled, so a spec that gets
// past the scan fails as a network error rather than a usage error --
// the two are distinguishable, which is what makes the assertion real.
func TestCreateRejectsUnfilledPlaceholderAnywhereInTheSpec(t *testing.T) {
	const head = "database_name: db\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n"

	tests := []struct {
		name string
		spec string
	}{
		{
			// The positive control: the case the scan already caught,
			// re-run here so a widening that broke it is visible.
			name: "node host id",
			spec: "database_name: db\n" +
				"nodes:\n  - name: n1\n    host_ids: [CHANGE-ME]\n",
		},
		{
			name: "backup repository s3 key",
			spec: head +
				"backup_config:\n  repositories:\n    - type: s3\n" +
				"      id: repo1\n      s3_bucket: b\n" +
				"      s3_key: CHANGE-ME\n",
		},
		{
			name: "backup repository s3 key secret",
			spec: head +
				"backup_config:\n  repositories:\n    - type: s3\n" +
				"      id: repo1\n      s3_key_secret: CHANGE-ME\n",
		},
		{
			name: "backup repository gcs key",
			spec: head +
				"backup_config:\n  repositories:\n    - type: gcs\n" +
				"      id: repo1\n      gcs_key: CHANGE-ME\n",
		},
		{
			name: "backup repository azure key",
			spec: head +
				"backup_config:\n  repositories:\n    - type: azure\n" +
				"      id: repo1\n      azure_key: CHANGE-ME\n",
		},
		{
			name: "service host ids",
			spec: head +
				"services:\n  - service_type: mcp\n" +
				"    service_id: mcp-1\n    connect_as: admin\n" +
				"    host_ids: [CHANGE-ME]\n    version: latest\n",
		},
		{
			// The list step is the one configSentinelHit never took:
			// pipelines is a []interface{}, so a key nested under it
			// was two levels deeper than the scan reached.
			name: "rag pipeline api key",
			spec: head +
				"services:\n  - service_type: rag\n" +
				"    service_id: rag-1\n    connect_as: admin\n" +
				"    host_ids: [h-1]\n    version: latest\n" +
				"    config:\n      pipelines:\n        - name: docs\n" +
				"          embedding_llm:\n            provider: openai\n" +
				"            api_key: CHANGE-ME\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSpecFile(t, tc.spec)
			rt, out, _ := newTestRuntime(t, "", "text")
			err := runControlplane(t, rt, out, "http://127.0.0.1:0",
				"database", "create", "storefront", "-f", path)
			requireUsageError(t, err)
			if !strings.Contains(err.Error(), "CHANGE-ME") {
				t.Errorf("error does not quote the sentinel: %v", err)
			}
		})
	}
}

// TestCheckUnfilledPlaceholdersNamesTheVerb pins the wording, because
// the same scan now runs for two commands and telling a user to fix
// something "before create" during an update is worse than saying
// nothing.
func TestCheckUnfilledPlaceholdersNamesTheVerb(t *testing.T) {
	nodes := []nodeHosts{{hostIDs: []string{"CHANGE-ME"}}}
	for _, verb := range []string{"create", "update"} {
		err := checkUnfilledPlaceholders(nodes, nil, nil, nil, nil, verb)
		if err == nil {
			t.Fatalf("%s: want an error", verb)
		}
		if !strings.Contains(err.Error(), "before "+verb) {
			t.Errorf("%s: error does not name the verb: %v", verb, err)
		}
	}
}
