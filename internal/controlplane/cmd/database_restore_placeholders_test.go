package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

// restoreSentinelCases is every restore_config field a sentinel can
// sit in: the three required source_* identifiers `database init`
// writes CHANGE-ME into when an interview leaves them blank, and the
// four repository credentials the template documents.
var restoreSentinelCases = []struct {
	name  string
	field string
	yaml  string
}{
	{"source database id", "restore_config.source_database_id",
		"  source_database_id: CHANGE-ME\n" +
			"  source_database_name: store\n  source_node_name: n1\n"},
	{"source database name", "restore_config.source_database_name",
		"  source_database_id: old\n" +
			"  source_database_name: CHANGE-ME\n  source_node_name: n1\n"},
	{"source node name", "restore_config.source_node_name",
		"  source_database_id: old\n" +
			"  source_database_name: store\n  source_node_name: CHANGE-ME\n"},
	{"repository s3 key", "restore_config.repository.s3_key",
		"  source_database_id: old\n" +
			"  source_database_name: store\n  source_node_name: n1\n" +
			"  repository:\n    type: s3\n    s3_key: CHANGE-ME\n"},
	{"repository s3 key secret", "restore_config.repository.s3_key_secret",
		"  source_database_id: old\n" +
			"  source_database_name: store\n  source_node_name: n1\n" +
			"  repository:\n    type: s3\n    s3_key_secret: CHANGE-ME\n"},
	{"repository gcs key", "restore_config.repository.gcs_key",
		"  source_database_id: old\n" +
			"  source_database_name: store\n  source_node_name: n1\n" +
			"  repository:\n    type: gcs\n    gcs_key: CHANGE-ME\n"},
	{"repository azure key", "restore_config.repository.azure_key",
		"  source_database_id: old\n" +
			"  source_database_name: store\n  source_node_name: n1\n" +
			"  repository:\n    type: azure\n    azure_key: CHANGE-ME\n"},
}

// TestRestoreRejectsUnfilledPlaceholder: `restore -f` runs the
// placeholder scan, so a restore_config still carrying CHANGE-ME never
// reaches the server. The port cannot be dialled, so a spec that gets past
// the scan fails as a network error, not a usage error.
func TestRestoreRejectsUnfilledPlaceholder(t *testing.T) {
	for _, tc := range restoreSentinelCases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSpecFile(t, "restore_config:\n"+tc.yaml)
			rt, out, _ := newTestRuntime(t, "", "text")
			err := runControlplane(t, rt, out, "http://127.0.0.1:0",
				"database", "restore", "storefront", "-f", path, "--force")
			requireUsageError(t, err)
			for _, want := range []string{"CHANGE-ME", tc.field,
				"before restore"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
		})
	}
}

// TestCreateAndUpdateRejectUnfilledRestorePlaceholder covers the
// other way the sentinel reaches a restore_config: `database init`
// renders blank source_* fields as CHANGE-ME inside a DatabaseSpec,
// and neither create's nor update's scan read restore_config.
func TestCreateAndUpdateRejectUnfilledRestorePlaceholder(t *testing.T) {
	const head = "database_name: db\n" +
		"nodes:\n  - name: n1\n    host_ids: [h-1]\n"
	for _, verb := range []string{"create", "update"} {
		for _, tc := range restoreSentinelCases {
			t.Run(verb+" "+tc.name, func(t *testing.T) {
				path := writeSpecFile(t, head+"restore_config:\n"+tc.yaml)
				rt, out, _ := newTestRuntime(t, "", "text")
				err := runControlplane(t, rt, out, "http://127.0.0.1:0",
					"database", verb, "storefront", "-f", path)
				requireUsageError(t, err)
				for _, want := range []string{"CHANGE-ME", tc.field,
					"before " + verb} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error lacks %q: %v", want, err)
					}
				}
			})
		}
	}
}

// TestRestoreRefusesThePlaceholderBeforeThePrompt pins the placement:
// moving the check below cli.Confirm passes every other test, because
// they all pass --force. Without --force, stdin
// is no terminal under go test, so cli.Confirm refuses with its own
// usage error; the error naming CHANGE-ME is therefore the proof the
// scan ran first.
func TestRestoreRefusesThePlaceholderBeforeThePrompt(t *testing.T) {
	path := writeSpecFile(t, "restore_config:\n"+restoreSentinelCases[0].yaml)
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:0",
		"database", "restore", "storefront", "-f", path)
	requireUsageError(t, err)
	if !strings.Contains(err.Error(), "CHANGE-ME") {
		t.Errorf("the confirm prompt ran before the scan: %v", err)
	}
}

// TestPlaceholderScanReportsRestoreConfigInTemplateOrder pins where
// restore_config sits in the scan: below the nodes and above the
// services, as the template writes them, so a spec with a sentinel on
// either side names the one a reader meets first.
func TestPlaceholderScanReportsRestoreConfigInTemplateOrder(t *testing.T) {
	sentinel := map[string]interface{}{"api_key": "CHANGE-ME"}
	restore := &api.RestoreConfigSpec{SourceDatabaseId: "CHANGE-ME"}
	services := []serviceConfig{{id: "mcp-1", config: &sentinel}}
	nodes := []nodeHosts{{hostIDs: []string{"CHANGE-ME"}}}

	err := checkUnfilledPlaceholders(nil, nil, nil, restore, services,
		"create")
	if err == nil || !strings.Contains(err.Error(), "restore_config") {
		t.Errorf("want restore_config named above services, got %v", err)
	}
	err = checkUnfilledPlaceholders(nodes, nil, nil, restore, nil, "create")
	if err == nil || !strings.Contains(err.Error(), "nodes[0]") {
		t.Errorf("want nodes[0] named above restore_config, got %v", err)
	}
}

// TestCheckUnfilledRestorePlaceholdersAcceptsFilled is the positive
// control: a filled restore_config, and no restore_config at all,
// both pass, or the tests above would be satisfied by a scan that
// refuses everything.
func TestCheckUnfilledRestorePlaceholdersAcceptsFilled(t *testing.T) {
	key := "k"
	filled := &api.RestoreConfigSpec{
		SourceDatabaseId:   "old",
		SourceDatabaseName: "store",
		SourceNodeName:     "n1",
		Repository:         api.RestoreRepositorySpec{S3Key: &key},
	}
	if err := checkUnfilledRestorePlaceholders(filled, "restore"); err != nil {
		t.Errorf("filled restore_config refused: %v", err)
	}
	if err := checkUnfilledRestorePlaceholders(nil, "create"); err != nil {
		t.Errorf("absent restore_config refused: %v", err)
	}
}
