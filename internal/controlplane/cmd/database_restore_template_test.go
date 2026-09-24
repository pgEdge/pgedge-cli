package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"gopkg.in/yaml.v3"
)

// decodeRestoreSpec mirrors loadSpecFile's YAML->JSON->struct path for
// the standalone restore request. The generated types carry only json
// tags, so decoding this way proves the emitted spec is what
// 'database restore <id> -f' will actually consume.
func decodeRestoreSpec(t *testing.T, got string) api.RestoreDatabaseRequest {
	t.Helper()
	var doc interface{}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("restore spec is not valid YAML: %v\n%s", err, got)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("normalize restore spec: %v\n%s", err, got)
	}
	var req api.RestoreDatabaseRequest
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatalf("decode restore spec: %v\n%s", err, got)
	}
	return req
}

func TestBuildRestoreTemplateDecodes(t *testing.T) {
	got := buildRestoreTemplate()
	// Annotated markers a user edits.
	for _, want := range []string{
		"# restore_config:", "source_database_id",
		"# target_nodes:", "restore <id> -f",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q\n%s", want, got)
		}
	}
	// Valid YAML / consumable shape (all-commented => zero request).
	_ = decodeRestoreSpec(t, got)
}

func TestBuildRestoreSpecPopulated(t *testing.T) {
	rv := &restoreValue{
		repoType: "s3",
		repoFields: map[string]string{
			"s3_bucket": "my-bucket", "s3_region": "us-east-1",
		},
		sourceDBID:     "old-store",
		sourceDBName:   "northwind",
		sourceNodeName: "n1",
		restoreOptions: map[string]string{
			"type": "time", "target": "2026-01-01 00:00:00+00",
		},
	}
	req := decodeRestoreSpec(t, buildRestoreSpec(rv, []string{"n1", "n2"}))
	if req.RestoreConfig.SourceDatabaseId != "old-store" {
		t.Errorf("source id = %q", req.RestoreConfig.SourceDatabaseId)
	}
	if req.RestoreConfig.SourceDatabaseName != "northwind" {
		t.Errorf("source name = %q", req.RestoreConfig.SourceDatabaseName)
	}
	if req.RestoreConfig.SourceNodeName != "n1" {
		t.Errorf("source node = %q", req.RestoreConfig.SourceNodeName)
	}
	if req.RestoreConfig.Repository.S3Bucket == nil ||
		*req.RestoreConfig.Repository.S3Bucket != "my-bucket" {
		t.Errorf("s3_bucket = %v", req.RestoreConfig.Repository.S3Bucket)
	}
	if req.TargetNodes == nil || len(*req.TargetNodes) != 2 ||
		(*req.TargetNodes)[0] != "n1" || (*req.TargetNodes)[1] != "n2" {
		t.Errorf("target_nodes = %v", req.TargetNodes)
	}
	if req.RestoreConfig.RestoreOptions == nil ||
		(*req.RestoreConfig.RestoreOptions)["type"] != "time" {
		t.Errorf("restore_options = %v", req.RestoreConfig.RestoreOptions)
	}
}

func TestBuildRestoreSpecBlankIsChangeMe(t *testing.T) {
	req := decodeRestoreSpec(t, buildRestoreSpec(&restoreValue{}, nil))
	if req.RestoreConfig.SourceDatabaseId != "CHANGE-ME" {
		t.Errorf("blank source id = %q", req.RestoreConfig.SourceDatabaseId)
	}
	if req.TargetNodes != nil {
		t.Errorf("blank target_nodes should be nil, got %v", req.TargetNodes)
	}
}

func TestBuildRestoreSpecNoCredential(t *testing.T) {
	rv := &restoreValue{
		repoType:   "s3",
		repoFields: map[string]string{"s3_bucket": "b", "s3_region": "r"},
	}
	repo := decodeRestoreSpec(t, buildRestoreSpec(rv, nil)).
		RestoreConfig.Repository
	if repo.S3Key != nil || repo.S3KeySecret != nil ||
		repo.GcsKey != nil || repo.AzureKey != nil {
		t.Errorf("generated spec leaked a credential: %+v", repo)
	}
}

func TestDatabaseRestoreTemplateCmd(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"database", "restore", "template"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restore template: %v", err)
	}
	got := rt.Stdout.(interface{ String() string }).String()
	if !strings.Contains(got, "restore_config") {
		t.Errorf("cmd output missing restore_config:\n%s", got)
	}
	_ = decodeRestoreSpec(t, got)
}

func TestRunRestoreInterviewPopulated(t *testing.T) {
	// Answers, in prompt order:
	// repo type, s3 bucket, s3 region, s3 endpoint,
	// source id, source name, source node,
	// PITR? kind, target, target nodes.
	in := strings.NewReader(strings.Join([]string{
		"s3", "my-bucket", "us-east-1", "",
		"old-store", "northwind", "n1",
		"y", "time", "2026-01-01 00:00:00+00",
		"n1, n2",
	}, "\n") + "\n")
	var errOut strings.Builder
	got, err := runRestoreInterview(in, &errOut)
	if err != nil {
		t.Fatalf("interview: %v", err)
	}
	req := decodeRestoreSpec(t, got)
	if req.RestoreConfig.SourceDatabaseId != "old-store" {
		t.Errorf("source id = %q", req.RestoreConfig.SourceDatabaseId)
	}
	if req.RestoreConfig.SourceNodeName != "n1" {
		t.Errorf("source node = %q", req.RestoreConfig.SourceNodeName)
	}
	if req.RestoreConfig.Repository.S3Bucket == nil ||
		*req.RestoreConfig.Repository.S3Bucket != "my-bucket" {
		t.Errorf("s3_bucket = %v", req.RestoreConfig.Repository.S3Bucket)
	}
	if req.TargetNodes == nil || len(*req.TargetNodes) != 2 {
		t.Errorf("target_nodes = %v", req.TargetNodes)
	}
	if req.RestoreConfig.RestoreOptions == nil ||
		(*req.RestoreConfig.RestoreOptions)["target"] !=
			"2026-01-01 00:00:00+00" {
		t.Errorf("restore_options = %v", req.RestoreConfig.RestoreOptions)
	}
	// No secret prompted or written.
	if req.RestoreConfig.Repository.S3Key != nil ||
		req.RestoreConfig.Repository.S3KeySecret != nil {
		t.Errorf("interview leaked a credential")
	}
}

func TestRunRestoreInterviewBlankTargetAndNoPITR(t *testing.T) {
	// s3 (bucket+region required) -> blank endpoint ->
	// source id/name required -> node default -> no PITR ->
	// blank target nodes.
	in := strings.NewReader(strings.Join([]string{
		"s3", "my-bucket", "us-east-1", "",
		"old-store", "northwind", "n1",
		"n",
		"",
	}, "\n") + "\n")
	var errOut strings.Builder
	got, err := runRestoreInterview(in, &errOut)
	if err != nil {
		t.Fatalf("interview: %v", err)
	}
	req := decodeRestoreSpec(t, got)
	if req.TargetNodes != nil {
		t.Errorf("blank target nodes should omit target_nodes: %v",
			req.TargetNodes)
	}
	if req.RestoreConfig.RestoreOptions != nil {
		t.Errorf("declined PITR should omit restore_options: %v",
			req.RestoreConfig.RestoreOptions)
	}
	if req.RestoreConfig.SourceDatabaseId != "old-store" {
		t.Errorf("source id = %q", req.RestoreConfig.SourceDatabaseId)
	}
}

func TestRunRestoreInterviewReservedLiteralNodes(t *testing.T) {
	// target_nodes and source name equal to reserved literals must
	// round-trip as strings.
	in := strings.NewReader(strings.Join([]string{
		"s3", "my-bucket", "us-east-1", "",
		"src", "true", "n1",
		"n",
		"null, n2",
	}, "\n") + "\n")
	var errOut strings.Builder
	got, err := runRestoreInterview(in, &errOut)
	if err != nil {
		t.Fatalf("interview: %v", err)
	}
	req := decodeRestoreSpec(t, got)
	if req.RestoreConfig.SourceDatabaseName != "true" {
		t.Errorf("source name = %q, want \"true\"",
			req.RestoreConfig.SourceDatabaseName)
	}
	if req.TargetNodes == nil || len(*req.TargetNodes) != 2 ||
		(*req.TargetNodes)[0] != "null" {
		t.Errorf("target_nodes = %v, want [null n2]", req.TargetNodes)
	}
}

func TestDatabaseRestoreTemplateInteractiveRequiresTTY(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"database", "restore", "template", "-i"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	requireUsageError(t, err)
}
