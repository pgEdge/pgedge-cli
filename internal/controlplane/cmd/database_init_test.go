package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"gopkg.in/yaml.v3"
)

// decodeSpec mirrors loadSpecFile's YAML->JSON->struct path (see
// database_spec.go): the generated spec types carry only json tags,
// so a bare yaml.Unmarshal would silently drop every snake_case
// field. Decoding this way proves the template is actually
// consumable by 'database create', not merely valid YAML.
func decodeSpec(t *testing.T, got string) api.DatabaseSpec2 {
	t.Helper()
	var doc interface{}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v\n%s", err, got)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("normalize spec: %v\n%s", err, got)
	}
	var spec api.DatabaseSpec2
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatalf("decode spec: %v\n%s", err, got)
	}
	return spec
}

// countNodeNames counts uncommented "  - name: nX" node lines.
func countNodeNames(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- name: n") {
			n++
		}
	}
	return n
}

func TestDatabaseInitTemplateHasAllSections(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"database_name", "nodes:", "database_users",
		"# backup_config", "# restore_config", "# services",
		"# postgresql_conf", "# pg_hba_conf",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q", want)
		}
	}
	// Enum hints must appear in the backup stub.
	if !strings.Contains(got, "s3") || !strings.Contains(got, "azure") {
		t.Errorf("backup stub should list repo types")
	}
	// Still create-consumable, still 3 nodes.
	spec := decodeSpec(t, got)
	if spec.DatabaseName == "" || len(spec.Nodes) != 3 {
		t.Errorf("decoded: name=%q nodes=%d", spec.DatabaseName,
			len(spec.Nodes))
	}
}

func TestDatabaseInitDefault(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"database", "init"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := rt.Stdout.(interface{ String() string }).String()

	if !strings.Contains(got, "database_name") {
		t.Errorf("missing database_name: %q", got)
	}
	if countNodeNames(got) != 3 {
		t.Errorf("want 3 nodes by default: %q", got)
	}

	// Prove the template is create-compatible: once decoded the way
	// 'database create' decodes it, DatabaseName is populated and
	// there are exactly 3 nodes.
	spec := decodeSpec(t, got)
	if spec.DatabaseName == "" {
		t.Errorf("decoded spec has empty DatabaseName: %+v", spec)
	}
	if len(spec.Nodes) != 3 {
		t.Errorf("decoded spec has %d nodes, want 3: %+v",
			len(spec.Nodes), spec)
	}
}

func TestDatabaseInitNodes(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"database", "init", "--nodes", "1"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := rt.Stdout.(interface{ String() string }).String()
	if countNodeNames(got) != 1 {
		t.Errorf("want exactly 1 node: %q", got)
	}

	spec := decodeSpec(t, got)
	if spec.DatabaseName == "" {
		t.Errorf("decoded spec has empty DatabaseName: %+v", spec)
	}
	if len(spec.Nodes) != 1 {
		t.Errorf("decoded spec has %d nodes, want 1: %+v",
			len(spec.Nodes), spec)
	}
}

func TestDatabaseInitZeroNodes(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"database", "init", "--nodes", "0"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error for --nodes 0, got nil")
	}
	exitErr, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T: %v", err, err)
	}
	if exitErr.code != ExitUsage {
		t.Errorf("want exit code %d, got %d", ExitUsage, exitErr.code)
	}
	if !strings.Contains(exitErr.msg, "--nodes") {
		t.Errorf("want message referencing --nodes, got %q",
			exitErr.msg)
	}
}

func TestRunInterviewScaffold(t *testing.T) {
	// database_name, node count, per-node name+hosts, admin, port.
	in := "storefront\n2\nn1\nhost-1\nn2\nhost-2\nappuser\n5432\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 3, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.DatabaseName != "storefront" {
		t.Errorf("name=%q", spec.DatabaseName)
	}
	if len(spec.Nodes) != 2 ||
		spec.Nodes[0].HostIds[0] != "host-1" ||
		spec.Nodes[1].HostIds[0] != "host-2" {
		t.Errorf("nodes=%+v", spec.Nodes)
	}
	if spec.DatabaseUsers == nil ||
		(*spec.DatabaseUsers)[0].Username != "appuser" {
		t.Errorf("admin user not set: %+v", spec.DatabaseUsers)
	}
	if spec.Port == nil || *spec.Port != 5432 {
		t.Errorf("port=%v", spec.Port)
	}
}

func TestRunInterviewPortReprompt(t *testing.T) {
	// port: non-numeric "abc" re-prompts, then "5432" is accepted.
	in := "db\n1\nn1\nhost-1\nadmin\nabc\n5432\nn\nn\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), `not a number: "abc"`) {
		t.Errorf("expected port re-prompt: %q", errBuf.String())
	}
	if !strings.Contains(got, "port: 5432") {
		t.Errorf("port not rendered:\n%s", got)
	}
	spec := decodeSpec(t, got)
	if spec.Port == nil || *spec.Port != 5432 {
		t.Errorf("port = %v, want 5432", spec.Port)
	}
}

func TestRunInterviewNodeOverrideEmptyConfValueSkipped(t *testing.T) {
	// Customize node -> blank port -> blank version -> add conf? y ->
	// key "work_mem" -> empty value (skipped) -> add another? n ->
	// pg_hba? n.
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\nn\n" + // scaffold, no b/s/r
		"y\n" + // customize nodes?
		"y\n" + // override node n1?
		"\n\n" + // port blank, version blank
		"y\nwork_mem\n\n" + // add conf? y, key, empty value
		"n\n" + // add another conf? n
		"n\n" // add pg_hba rule? n
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "work_mem:") {
		t.Errorf("empty conf value should be skipped:\n%s", got)
	}
}

func TestRunInterviewDefaults(t *testing.T) {
	// All empty lines -> defaults; node count defaults to nodes arg (1).
	in := "\n\nn1\n\n\n\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.DatabaseName != "my-database" || len(spec.Nodes) != 1 {
		t.Errorf("defaults wrong: name=%q nodes=%d",
			spec.DatabaseName, len(spec.Nodes))
	}
	if spec.Port != nil {
		t.Errorf("empty port should be omitted, got %v", spec.Port)
	}
}

func TestRunInterviewBackupsS3(t *testing.T) {
	// scaffold (name, 1 node w/ host, admin, port) then:
	// backups? y -> type s3 -> bucket -> region -> endpoint(blank)
	// -> retention(blank) -> schedule? n
	in := "db\n1\nn1\nhost-1\nadmin\n\n" +
		"y\ns3\nmy-bucket\nus-east-1\n\n\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.BackupConfig == nil ||
		len(spec.BackupConfig.Repositories) != 1 {
		t.Fatalf("no backup repo: %+v", spec.BackupConfig)
	}
	repo := spec.BackupConfig.Repositories[0]
	if string(repo.Type) != "s3" ||
		repo.S3Bucket == nil || *repo.S3Bucket != "my-bucket" ||
		repo.S3Region == nil || *repo.S3Region != "us-east-1" {
		t.Errorf("s3 fields wrong: %+v", repo)
	}
	// Credentials must NOT be populated (commented only).
	if repo.S3KeySecret != nil {
		t.Errorf("secret must not be baked in: %+v", repo)
	}
	// gcs/azure fields must be absent for an s3 repo.
	if repo.GcsBucket != nil || repo.AzureAccount != nil {
		t.Errorf("cross-type fields leaked: %+v", repo)
	}
}

func TestRunInterviewBackupsPosixWithSchedule(t *testing.T) {
	in := "db\n1\nn1\nhost-1\nadmin\n\n" +
		"y\nposix\n/var/backups\n\n" + // type, base_path, retention blank
		"y\n0 2 * * *\nfull\n" // schedule? y, cron, type
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	repo := spec.BackupConfig.Repositories[0]
	if string(repo.Type) != "posix" ||
		repo.BasePath == nil || *repo.BasePath != "/var/backups" {
		t.Errorf("posix fields wrong: %+v", repo)
	}
	if spec.BackupConfig.Schedules == nil ||
		(*spec.BackupConfig.Schedules)[0].CronExpression != "0 2 * * *" ||
		string((*spec.BackupConfig.Schedules)[0].Type) != "full" {
		t.Errorf("schedule wrong: %+v", spec.BackupConfig.Schedules)
	}
}

// TestRunInterviewBackupsPosixWithRetention supplies a full-backup
// retention count and asserts it decodes as a numeric RetentionFull
// with RetentionFullType "count". The retention prompt lands between
// the type-specific fields (base_path) and the "Add a backup
// schedule?" prompt, so the retention answer must be inserted there
// or the trailing schedule answers shift onto the wrong prompts.
func TestRunInterviewBackupsPosixWithRetention(t *testing.T) {
	in := "db\n1\nn1\nhost-1\nadmin\n\n" +
		"y\nposix\n/var/backups\n7\n" + // type, base_path, retention=7
		"n\n" // schedule? n
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	repo := spec.BackupConfig.Repositories[0]
	if string(repo.Type) != "posix" ||
		repo.BasePath == nil || *repo.BasePath != "/var/backups" {
		t.Errorf("posix fields wrong: %+v", repo)
	}
	if repo.RetentionFull == nil || *repo.RetentionFull != 7 {
		t.Errorf("retention_full wrong: %+v", repo.RetentionFull)
	}
	if repo.RetentionFullType == nil ||
		string(*repo.RetentionFullType) != "count" {
		t.Errorf("retention_full_type wrong: %+v", repo.RetentionFullType)
	}
}

// TestRunInterviewDatabaseNameWithColon proves a free-text answer
// containing a YAML-significant colon-space still round-trips through
// decodeSpec instead of producing invalid or misparsed YAML.
func TestRunInterviewDatabaseNameWithColon(t *testing.T) {
	in := "prod: east\n1\nn1\nhost-1: a\nadmin\n\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.DatabaseName != "prod: east" {
		t.Errorf("name=%q, want %q", spec.DatabaseName, "prod: east")
	}
	if len(spec.Nodes) != 1 || len(spec.Nodes[0].HostIds) != 1 ||
		spec.Nodes[0].HostIds[0] != "host-1: a" {
		t.Errorf("nodes=%+v", spec.Nodes)
	}
}

// TestRunInterviewHostIDWithFlowChars proves a host id containing a
// flow-sequence-significant character (here, brackets) survives the
// host_ids: [a, b] flow rendering intact. host_ids is emitted as a
// YAML flow sequence, where "[]{}," are structurally significant
// anywhere in an element -- not just when leading -- so an unquoted
// "host[1]" would render as host_ids: [host[1]], which fails to
// parse ("did not find expected ',' or ']'").
func TestRunInterviewHostIDWithFlowChars(t *testing.T) {
	in := "db\n1\nn1\nhost[1]\nadmin\n\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if len(spec.Nodes) != 1 || len(spec.Nodes[0].HostIds) != 1 ||
		spec.Nodes[0].HostIds[0] != "host[1]" {
		t.Errorf("nodes=%+v", spec.Nodes)
	}
}

func TestRunInterviewBackupsDeclined(t *testing.T) {
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" // backups? n
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	// Declining leaves backup_config as a commented stub -> nil once
	// decoded, and the stub text present.
	spec := decodeSpec(t, got)
	if spec.BackupConfig != nil {
		t.Errorf("declined backups should not populate: %+v",
			spec.BackupConfig)
	}
	if !strings.Contains(got, "# backup_config") {
		t.Errorf("stub should remain when declined")
	}
}

func TestRunInterviewServicesMCPDeclinedLLM(t *testing.T) {
	// scaffold (name,1 node w/ host, admin, port) -> backups? n ->
	// services? y -> type mcp -> id(default) -> connect_as(default) ->
	// host -> version(default) -> configure LLM? n -> init token? n ->
	// add another? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
		"y\nmcp\n\n\nh-1\n\nn\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "llm_api_key") {
		t.Errorf("must never emit the retired llm_api_key key:\n%s", got)
	}
	if strings.Contains(errBuf.String(), "need to be set") {
		t.Errorf("no secrets configured must not warn")
	}
	spec := decodeSpec(t, got)
	if spec.Services == nil || len(*spec.Services) != 1 {
		t.Fatalf("want 1 service: %+v", spec.Services)
	}
	s := (*spec.Services)[0]
	if string(s.ServiceType) != "mcp" || s.ServiceId != "mcp-1" ||
		s.ConnectAs != "admin" || s.Version != "latest" ||
		len(s.HostIds) != 1 || s.HostIds[0] != "h-1" {
		t.Errorf("service fields wrong: %+v", s)
	}
	// Nothing was configured, so no real config: key was ever emitted
	// (only the commented per-type stub, which decodeSpec drops).
	// Checking the decoded struct rather than a raw substring is what
	// distinguishes "llm_enabled: true" as live YAML from the same
	// text appearing inside serviceConfigGuidance's illustrative
	// comment for a service that configured nothing.
	if s.Config != nil {
		t.Errorf("declining everything should leave config unset: %+v",
			s.Config)
	}
}

// TestRunInterviewServicesMCPProviders is table-driven over the three
// llm_provider choices CP allows, asserting each resolves to the
// PROVIDER-SPECIFIC credential key CP requires
// (mcp_service_config.go:157-178 @ v0.10.0) rather than a generic
// llm_api_key, and that llm_enabled/llm_provider/llm_model all land in
// the decoded config.
func TestRunInterviewServicesMCPProviders(t *testing.T) {
	tests := []struct {
		provider  string
		secretKey string
	}{
		{"anthropic", "anthropic_api_key"},
		{"openai", "openai_api_key"},
		{"ollama", "ollama_url"},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			// services? y -> mcp -> id/connect_as/host/version ->
			// configure LLM? y -> provider -> model -> init token? n
			// -> more? n
			in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
				"y\nmcp\n\n\nh-1\n\ny\n" + tc.provider +
				"\nclaude-sonnet-4-5\nn\nn\n"
			var errBuf bytes.Buffer
			got, err := runInterview(strings.NewReader(in), &errBuf, 1,
				detectionResult{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, tc.secretKey+": CHANGE-ME") {
				t.Errorf("%s must emit %s: CHANGE-ME:\n%s",
					tc.provider, tc.secretKey, got)
			}
			if strings.Contains(got, "llm_api_key") {
				t.Errorf("must never emit the retired llm_api_key key:\n%s",
					got)
			}
			if !strings.Contains(errBuf.String(), "mcp-1") ||
				!strings.Contains(errBuf.String(), "need to be set") {
				t.Errorf("warning should name mcp-1: %q", errBuf.String())
			}
			spec := decodeSpec(t, got)
			s := (*spec.Services)[0]
			if s.Config == nil || (*s.Config)["llm_enabled"] != true ||
				(*s.Config)["llm_provider"] != tc.provider ||
				(*s.Config)["llm_model"] != "claude-sonnet-4-5" ||
				(*s.Config)[tc.secretKey] != "CHANGE-ME" {
				t.Errorf("config wrong: %+v", s.Config)
			}
		})
	}
}

// TestRunInterviewServicesMCPInitToken covers the headline
// gap: init_token is independent of llm_enabled, so
// declining LLM settings but accepting an init token must still emit
// the sentinel and warn about it.
func TestRunInterviewServicesMCPInitToken(t *testing.T) {
	// configure LLM? n -> init token? y -> more? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
		"y\nmcp\n\n\nh-1\n\nn\ny\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "init_token: CHANGE-ME") {
		t.Errorf("init token must emit the sentinel:\n%s", got)
	}
	if strings.Contains(got, "llm_enabled: true") {
		t.Errorf("init token alone must not imply llm_enabled:\n%s", got)
	}
	if !strings.Contains(errBuf.String(), "mcp-1") ||
		!strings.Contains(errBuf.String(), "config.init_token") {
		t.Errorf("warning should name config.init_token: %q",
			errBuf.String())
	}
	spec := decodeSpec(t, got)
	s := (*spec.Services)[0]
	if s.Config == nil || (*s.Config)["init_token"] != "CHANGE-ME" {
		t.Errorf("sentinel not in decoded config: %+v", s.Config)
	}
}

func TestRunInterviewServicesPostgrest(t *testing.T) {
	// services? y -> postgrest -> id -> connect_as -> host -> version
	// -> (no config prompts for non-mcp) -> more? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
		"y\npostgrest\n\n\nh-1\n\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.Services == nil || len(*spec.Services) != 1 {
		t.Fatalf("want 1 service: %+v", spec.Services)
	}
	s := (*spec.Services)[0]
	if string(s.ServiceType) != "postgrest" || s.ServiceId != "postgrest-1" {
		t.Errorf("postgrest fields wrong: %+v", s)
	}
	if s.Config != nil {
		t.Errorf("postgrest must have no populated config: %+v", s.Config)
	}
}

func TestRunInterviewServicesTwo(t *testing.T) {
	// two services: mcp (LLM declined, init token declined) then rag;
	// ids default per type.
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
		"y\nmcp\n\n\nh-1\n\nn\nn\ny\n" + // svc1 mcp, LLM? n, token? n, more? y
		"rag\n\n\nh-2\n\nn\n" // svc2 rag, more? n
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.Services == nil || len(*spec.Services) != 2 {
		t.Fatalf("want 2 services: %+v", spec.Services)
	}
	if (*spec.Services)[0].ServiceId != "mcp-1" ||
		(*spec.Services)[1].ServiceId != "rag-2" {
		t.Errorf("default ids wrong: %+v", spec.Services)
	}
}

func TestRunInterviewServicesDeclined(t *testing.T) {
	// services? n leaves the corrected stub; nothing populated.
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.Services != nil {
		t.Errorf("declined services should not populate: %+v",
			spec.Services)
	}
	if !strings.Contains(got, "# services:") {
		t.Errorf("stub should remain when declined")
	}
}

func TestDatabaseInitInteractiveRequiresTTY(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "init", "-i")
	requireUsageError(t, err)
}

func TestServicesStubCorrected(t *testing.T) {
	// Non-interactive init must emit a corrected services stub:
	// valid enum (mcp), no invalid pgcat, and the required
	// service_id key documented.
	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "# services:") {
		t.Fatalf("services stub missing: %q", got)
	}
	if strings.Contains(got, "pgcat") {
		t.Errorf("stub still lists invalid service_type pgcat")
	}
	if !strings.Contains(got, "service_type: mcp") {
		t.Errorf("stub should show a valid service_type (mcp)")
	}
	if !strings.Contains(got, "service_id:") {
		t.Errorf("stub should document required service_id")
	}
}

func TestBuildSpecPopulatedServices(t *testing.T) {
	// A fully-populated services slice must round-trip through
	// decodeSpec with every required field present, an mcp config
	// block, and the provider-specific secret sentinel emitted
	// uncommented -- never the retired generic llm_api_key.
	v := specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
		services: []serviceValue{
			{
				serviceType: "mcp",
				serviceID:   "mcp-1",
				connectAs:   "admin",
				hostIDs:     []string{"h-1", "h-2"},
				version:     "latest",
				config: map[string]string{
					"llm_enabled":  "true",
					"llm_provider": "openai",
				},
				secretKey: "openai_api_key",
			},
			{
				serviceType: "postgrest",
				serviceID:   "postgrest-1",
				connectAs:   "admin",
				hostIDs:     []string{"h-1"},
				version:     "latest",
			},
		},
	}
	got := buildSpec(v)
	if !strings.Contains(got, "openai_api_key: CHANGE-ME") {
		t.Errorf("mcp needing a key must emit the sentinel uncommented")
	}
	if strings.Contains(got, "llm_api_key") {
		t.Errorf("must never emit the retired llm_api_key key:\n%s", got)
	}
	spec := decodeSpec(t, got)
	if spec.Services == nil || len(*spec.Services) != 2 {
		t.Fatalf("want 2 services, got %+v", spec.Services)
	}
	s0 := (*spec.Services)[0]
	if string(s0.ServiceType) != "mcp" || s0.ServiceId != "mcp-1" ||
		s0.ConnectAs != "admin" || s0.Version != "latest" ||
		len(s0.HostIds) != 2 {
		t.Errorf("mcp service fields wrong: %+v", s0)
	}
	if s0.Config == nil ||
		(*s0.Config)["llm_enabled"] != true ||
		(*s0.Config)["llm_provider"] != "openai" ||
		(*s0.Config)["openai_api_key"] != "CHANGE-ME" {
		t.Errorf("mcp config wrong: %+v", s0.Config)
	}
	s1 := (*spec.Services)[1]
	if string(s1.ServiceType) != "postgrest" || s1.Config != nil {
		t.Errorf("postgrest should have no populated config: %+v", s1)
	}
}

// TestBuildSpecServiceTypeDefaultsEmitsSentinel guards the latent trap
// where an empty serviceType renders as mcp (via writeService's
// default) but the config gate keys off the raw, still-empty type and
// silently drops the secret sentinel -- defeating create's fail-fast
// guard. writeService must normalize the type before writeServiceConfig
// decides, so a service needing a secret always emits it.
func TestBuildSpecServiceTypeDefaultsEmitsSentinel(t *testing.T) {
	v := specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
		services: []serviceValue{{
			serviceID: "svc-1",
			connectAs: "admin",
			hostIDs:   []string{"h-1"},
			secretKey: "anthropic_api_key",
		}},
	}
	got := buildSpec(v)
	if !strings.Contains(got, "service_type: mcp") {
		t.Errorf("empty serviceType should render as mcp:\n%s", got)
	}
	if !strings.Contains(got, "anthropic_api_key: CHANGE-ME") {
		t.Errorf("needs-key with empty type must still emit sentinel:\n%s",
			got)
	}
	spec := decodeSpec(t, got)
	s := (*spec.Services)[0]
	if s.Config == nil || (*s.Config)["anthropic_api_key"] != "CHANGE-ME" {
		t.Errorf("sentinel missing from decoded config: %+v", s.Config)
	}
}

func TestRestoreStubCorrected(t *testing.T) {
	// Non-interactive init must emit a corrected restore_config stub:
	// nested repository object, the required source_* fields, and NO
	// bogus top-level recovery_target_time field.
	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "# restore_config:") {
		t.Fatalf("restore_config stub missing: %q", got)
	}
	if !strings.Contains(got, "repository:") {
		t.Errorf("stub must show the nested repository object")
	}
	if !strings.Contains(got, "source_database_id:") {
		t.Errorf("stub must document required source_database_id")
	}
	if strings.Contains(got, "recovery_target_time:") {
		t.Errorf("stub still shows the bogus recovery_target_time field")
	}
}

func TestBuildSpecPopulatedRestore(t *testing.T) {
	// A fully-populated restore must round-trip through decodeSpec:
	// nested repository, all three source fields, and restore_options.
	v := specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
		restore: &restoreValue{
			repoType: "s3",
			repoFields: map[string]string{
				"s3_bucket": "my-bucket",
				"s3_region": "us-east-1",
			},
			sourceDBID:     "source-db",
			sourceDBName:   "northwind",
			sourceNodeName: "n1",
			restoreOptions: map[string]string{
				"type":   "time",
				"target": "2026-01-01 00:00:00+00",
			},
		},
	}
	got := buildSpec(v)
	spec := decodeSpec(t, got)
	if spec.RestoreConfig == nil {
		t.Fatalf("restore_config did not decode: %q", got)
	}
	rc := spec.RestoreConfig
	if string(rc.Repository.Type) != "s3" {
		t.Errorf("repo type wrong: %+v", rc.Repository)
	}
	if rc.Repository.S3Bucket == nil ||
		*rc.Repository.S3Bucket != "my-bucket" {
		t.Errorf("s3_bucket wrong: %+v", rc.Repository)
	}
	if rc.SourceDatabaseId != "source-db" ||
		rc.SourceDatabaseName != "northwind" ||
		rc.SourceNodeName != "n1" {
		t.Errorf("source fields wrong: %+v", rc)
	}
	if rc.RestoreOptions == nil ||
		(*rc.RestoreOptions)["type"] != "time" ||
		(*rc.RestoreOptions)["target"] != "2026-01-01 00:00:00+00" {
		t.Errorf("restore_options wrong: %+v", rc.RestoreOptions)
	}
}

func TestBuildSpecRestoreBlankSourcesChangeMe(t *testing.T) {
	// Blank required source_* fields render as CHANGE-ME and round-trip.
	v := specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
		restore: &restoreValue{
			repoType:       "s3",
			repoFields:     map[string]string{"s3_bucket": "b"},
			sourceNodeName: "n1",
		},
	}
	got := buildSpec(v)
	if !strings.Contains(got, "source_database_id: CHANGE-ME") {
		t.Errorf("blank source id should render CHANGE-ME:\n%s", got)
	}
	spec := decodeSpec(t, got)
	if spec.RestoreConfig.SourceDatabaseId != "CHANGE-ME" {
		t.Errorf("CHANGE-ME did not round-trip: %+v", spec.RestoreConfig)
	}
}

func TestBuildSpecFullyDefaulted(t *testing.T) {
	// A fully-defaulted specValues with an all-empty service and an
	// all-empty restore must still produce valid, create-consumable
	// YAML, exercising every default branch.
	v := specValues{
		services: []serviceValue{{}},
		restore:  &restoreValue{},
	}
	got := buildSpec(v)
	spec := decodeSpec(t, got)
	if spec.Services == nil || len(*spec.Services) != 1 {
		t.Fatalf("want 1 defaulted service: %+v", spec.Services)
	}
	s := (*spec.Services)[0]
	if string(s.ServiceType) != "mcp" || s.Version != "latest" {
		t.Errorf("service defaults wrong: %+v", s)
	}
	if spec.RestoreConfig == nil {
		t.Fatalf("restore did not decode: %q", got)
	}
	rc := spec.RestoreConfig
	if string(rc.Repository.Type) != "s3" {
		t.Errorf("repo type default wrong: %+v", rc.Repository)
	}
	if rc.SourceDatabaseId != "CHANGE-ME" ||
		rc.SourceDatabaseName != "CHANGE-ME" ||
		rc.SourceNodeName != "CHANGE-ME" {
		t.Errorf("blank source_* should default to CHANGE-ME: %+v", rc)
	}
}

func TestRunInterviewRestoreS3NoPITR(t *testing.T) {
	// scaffold (name,1 node w/ host, admin, port) -> backups? n ->
	// services? n -> restore? y -> s3 -> bucket -> region ->
	// endpoint(blank) -> source id -> source name -> source node ->
	// PITR? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\n" +
		"y\ns3\nmy-bucket\nus-east-1\n\n" +
		"source-db\nnorthwind\nn1\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.RestoreConfig == nil {
		t.Fatalf("restore did not populate: %q", got)
	}
	rc := spec.RestoreConfig
	if string(rc.Repository.Type) != "s3" ||
		rc.Repository.S3Bucket == nil ||
		*rc.Repository.S3Bucket != "my-bucket" {
		t.Errorf("repository wrong: %+v", rc.Repository)
	}
	if rc.SourceDatabaseId != "source-db" ||
		rc.SourceDatabaseName != "northwind" ||
		rc.SourceNodeName != "n1" {
		t.Errorf("source fields wrong: %+v", rc)
	}
	if rc.RestoreOptions != nil {
		t.Errorf("no PITR expected: %+v", rc.RestoreOptions)
	}
}

func TestRunInterviewRestorePITR(t *testing.T) {
	// ... restore? y -> s3 -> bucket -> region -> endpoint -> source
	// id/name/node -> PITR? y -> kind time -> target
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\n" +
		"y\ns3\nmy-bucket\nus-east-1\n\n" +
		"source-db\nnorthwind\nn1\n" +
		"y\ntime\n2026-01-01 00:00:00+00\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	rc := spec.RestoreConfig
	if rc == nil || rc.RestoreOptions == nil {
		t.Fatalf("PITR did not populate: %q", got)
	}
	if (*rc.RestoreOptions)["type"] != "time" ||
		(*rc.RestoreOptions)["target"] != "2026-01-01 00:00:00+00" {
		t.Errorf("restore_options wrong: %+v", rc.RestoreOptions)
	}
}

func TestRunInterviewRestoreDeclined(t *testing.T) {
	// restore? n leaves the corrected stub; nothing populated.
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if spec.RestoreConfig != nil {
		t.Errorf("declined restore should not populate: %+v",
			spec.RestoreConfig)
	}
	if !strings.Contains(got, "# restore_config:") {
		t.Errorf("stub should remain when declined")
	}
}

func TestRunInterviewRestoreReprompsBlankSources(t *testing.T) {
	// restore? y -> posix -> base path -> source id(blank->value) ->
	// source name(blank->value) -> source node -> PITR? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\n" +
		"y\nposix\n/var/lib/pgbackrest\n" +
		"\nsource-db\n" + // id: blank re-prompts, then value
		"\nnorthwind\n" + // name: blank re-prompts, then value
		"n1\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(errBuf.String(), "a value is required") < 2 {
		t.Errorf("expected two required re-prompts: %q", errBuf.String())
	}
	// The commented credential/host scaffold always carries CHANGE-ME
	// notes; only the interactively collected source fields must not.
	if strings.Contains(got, "source_database_id: CHANGE-ME") ||
		strings.Contains(got, "source_database_name: CHANGE-ME") {
		t.Errorf("interactive restore must not emit CHANGE-ME sources:\n%s", got)
	}
	spec := decodeSpec(t, got)
	rc := spec.RestoreConfig
	if rc == nil || rc.SourceDatabaseId != "source-db" ||
		rc.SourceDatabaseName != "northwind" {
		t.Errorf("source fields wrong: %+v", rc)
	}
	if string(rc.Repository.Type) != "posix" ||
		rc.Repository.BasePath == nil ||
		*rc.Repository.BasePath != "/var/lib/pgbackrest" {
		t.Errorf("posix repo wrong: %+v", rc.Repository)
	}
}

func TestRunInterviewNodeOverridesDeclined(t *testing.T) {
	// scaffold 1 node -> backups n -> services n -> restore n ->
	// customize n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if len(spec.Nodes) != 1 {
		t.Fatalf("want 1 node: %+v", spec.Nodes)
	}
	if spec.Nodes[0].Port != nil ||
		spec.Nodes[0].PostgresqlConf != nil {
		t.Errorf("declined customization should leave no overrides: %+v",
			spec.Nodes[0])
	}
}

func TestRunInterviewNodeOverridesOne(t *testing.T) {
	// ... customize? y -> node n1 override? y -> port -> version ->
	// pgconf add? y key val, add? n -> hba add? y rule, add? n
	in := "db\n1\nn1\nhost-1\nadmin\n\nn\nn\nn\n" +
		"y\n" +
		"y\n5433\n16.4\n" +
		"y\nshared_buffers\n8GB\nn\n" +
		"y\nhost all all 10.0.0.0/8 scram-sha-256\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	n := spec.Nodes[0]
	if n.Port == nil || *n.Port != 5433 {
		t.Errorf("port wrong: %+v", n.Port)
	}
	if n.PostgresVersion == nil || *n.PostgresVersion != "16.4" {
		t.Errorf("version wrong: %+v", n.PostgresVersion)
	}
	if n.PostgresqlConf == nil ||
		(*n.PostgresqlConf)["shared_buffers"] != "8GB" {
		t.Errorf("postgresql_conf wrong: %+v", n.PostgresqlConf)
	}
	if n.PgHbaConf == nil || len(*n.PgHbaConf) != 1 {
		t.Errorf("pg_hba_conf wrong: %+v", n.PgHbaConf)
	}
}

func TestRunInterviewNodeOverridesSecondOnly(t *testing.T) {
	// 2 nodes; customize? y; node n1 override? n; node n2 override? y
	// -> port -> version(blank) -> pgconf add? n -> hba add? n
	in := "db\n2\nn1\nhost-1\nn2\nhost-2\nadmin\n\nn\nn\nn\n" +
		"y\n" +
		"n\n" +
		"y\n5433\n\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 2, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	spec := decodeSpec(t, got)
	if len(spec.Nodes) != 2 {
		t.Fatalf("want 2 nodes: %+v", spec.Nodes)
	}
	if spec.Nodes[0].Port != nil {
		t.Errorf("node 1 should have no override: %+v", spec.Nodes[0])
	}
	if spec.Nodes[1].Port == nil || *spec.Nodes[1].Port != 5433 {
		t.Errorf("node 2 port override wrong: %+v", spec.Nodes[1])
	}
}

func TestNodeOverridesStubCorrected(t *testing.T) {
	// Non-interactive init must emit a corrected per-node override doc
	// note: no non-existent "scripts" field, and the real advanced
	// fields (e.g. patroni_port) documented.
	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "scripts") {
		t.Errorf("node-override note still lists non-existent scripts")
	}
	if !strings.Contains(got, "Per-node overrides:") {
		t.Fatalf("corrected per-node override note missing: %q", got)
	}
	if !strings.Contains(got, "patroni_port") {
		t.Errorf("corrected note should list real advanced fields")
	}
}

func TestBuildSpecNodeOverrides(t *testing.T) {
	// A node with all four override fields populated must round-trip
	// through decodeSpec with each field decoded.
	v := specValues{
		databaseName: "db",
		nodes: []nodeValue{{
			name:            "n1",
			hostIDs:         []string{"h-1"},
			port:            "5433",
			postgresVersion: "16.4",
			postgresqlConf:  map[string]string{"shared_buffers": "8GB"},
			pgHbaConf: []string{
				"host all all 10.0.0.0/8 scram-sha-256"},
		}},
	}
	got := buildSpec(v)
	spec := decodeSpec(t, got)
	if len(spec.Nodes) != 1 {
		t.Fatalf("want 1 node: %+v", spec.Nodes)
	}
	n := spec.Nodes[0]
	if n.Port == nil || *n.Port != 5433 {
		t.Errorf("port override wrong: %+v", n.Port)
	}
	if n.PostgresVersion == nil || *n.PostgresVersion != "16.4" {
		t.Errorf("postgres_version override wrong: %+v", n.PostgresVersion)
	}
	if n.PostgresqlConf == nil ||
		(*n.PostgresqlConf)["shared_buffers"] != "8GB" {
		t.Errorf("postgresql_conf override wrong: %+v", n.PostgresqlConf)
	}
	if n.PgHbaConf == nil || len(*n.PgHbaConf) != 1 ||
		(*n.PgHbaConf)[0] != "host all all 10.0.0.0/8 scram-sha-256" {
		t.Errorf("pg_hba_conf override wrong: %+v", n.PgHbaConf)
	}
}

func TestBuildSpecNodeNoOverridesUnchanged(t *testing.T) {
	// A node with no overrides renders exactly name + host_ids (no
	// stray override keys).
	v := specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
	}
	got := buildSpec(v)
	if strings.Contains(got, "    port:") ||
		strings.Contains(got, "    postgres_version:") {
		t.Errorf("node with no overrides emitted override keys:\n%s", got)
	}
	spec := decodeSpec(t, got)
	if spec.Nodes[0].Port != nil {
		t.Errorf("no-override node should have nil Port: %+v", spec.Nodes[0])
	}
}

func TestRunInterviewDatabaseNameReservedLiteral(t *testing.T) {
	// A database name equal to a YAML reserved literal ("null") must
	// round-trip as the string "null", not decode to empty.
	in := "null\n1\nn1\nhost-1\nadmin\n\nn\nn\nn\nn\n"
	var errBuf bytes.Buffer
	got, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `database_name: "null"`) {
		t.Errorf("reserved literal should be quoted:\n%s", got)
	}
	spec := decodeSpec(t, got)
	if spec.DatabaseName != "null" {
		t.Errorf("database_name = %q, want \"null\"", spec.DatabaseName)
	}
}

func TestEmitSpecJSONRoundTrips(t *testing.T) {
	// Minimal scripted interview: name, 1 node, its host, admin,
	// blank port, decline backups/services/restore/node-overrides.
	in := "testdb\n1\nn1\nhost-a\nadmin\n\nn\nn\nn\nn\n"
	var errBuf bytes.Buffer
	specYAML, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}

	var out bytes.Buffer
	if err := emitSpecJSON(&out, specYAML); err != nil {
		t.Fatalf("emitSpecJSON: %v", err)
	}
	got := out.String()

	// Pure JSON: no annotated-stub comment lines.
	if strings.Contains(got, "#") {
		t.Errorf("json output contains a comment line:\n%s", got)
	}
	// Round-trips back through the shared loader into the create type.
	var spec api.DatabaseSpec2
	if err := loadSpecBytes([]byte(got), &spec); err != nil {
		t.Fatalf("emitted JSON did not decode: %v\n%s", err, got)
	}
	if spec.DatabaseName != "testdb" {
		t.Errorf("database_name = %q, want testdb", spec.DatabaseName)
	}
	if len(spec.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(spec.Nodes))
	}
	if !strings.Contains(got, "host-a") {
		t.Errorf("host id host-a missing from JSON:\n%s", got)
	}
}

func TestInitJSONWithoutInteractiveErrors(t *testing.T) {
	// The -o/--output flag lives on the top-level pgedge root and is
	// resolved into rt.Output.Format before the cp tree is built (see
	// cmd/pgedge/main.go). newTestRuntime seeds that json format here,
	// mirroring production; driving `database init` non-interactively
	// under a json runtime must be rejected before any TTY check.
	rt, out, _ := newTestRuntime(t, "", "json")
	cmd := NewControlplaneCmd(rt)
	// The production pgedge root silences cobra's usage/error printing
	// (it maps the error to an exit code centrally); mirror that here so
	// the buffer captures only what the RunE writes, not usage noise.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"database", "init"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	requireUsageError(t, err)
	if out.Len() != 0 {
		t.Errorf("nothing should be written to stdout, got: %q",
			out.String())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "yaml") {
		t.Errorf("error should point the user to YAML, got: %v", err)
	}
}

func TestEmitSpecJSONKeepsSecretSentinel(t *testing.T) {
	// name,1 node,host,admin,blank port, decline backups,
	// services=yes -> mcp service needing a key, decline the rest.
	// Ordering verified against TestRunInterviewServicesMCPProviders.
	in := "keydb\n1\nn1\nhost-a\nadmin\n\nn\n" + // scaffold + no backups
		"y\n" + // configure services
		"mcp\nmcp-1\nadmin\nhost-a\nlatest\n" + // service fields
		"y\nopenai\ngpt-4o\nn\n" + // mcp config: LLM on, init token? no
		"n\n" + // add another service? no
		"n\nn\n" // restore? no; node overrides? no
	var errBuf bytes.Buffer
	specYAML, err := runInterview(strings.NewReader(in), &errBuf, 1, detectionResult{})
	if err != nil {
		t.Fatalf("runInterview: %v", err)
	}
	if !strings.Contains(errBuf.String(), "need to be set") ||
		!strings.Contains(errBuf.String(), "config.openai_api_key") {
		t.Errorf("expected the unfilled-secret warning on stderr, got: %q",
			errBuf.String())
	}
	var out bytes.Buffer
	if err := emitSpecJSON(&out, specYAML); err != nil {
		t.Fatalf("emitSpecJSON: %v", err)
	}
	if !strings.Contains(out.String(), secretSentinel) {
		t.Errorf("JSON dropped the %q sentinel; create's fail-fast "+
			"would not protect the piped spec:\n%s",
			secretSentinel, out.String())
	}
}
