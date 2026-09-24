package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestMCPEmbeddingProviderRequiresAPIKey covers #551: setting
// --embedding-provider used to write successfully with no key
// reachable anywhere, and only the server's later tools/call failed,
// with "API key is required" reaching the operator through the MCP
// client rather than this CLI. The check added for #551 asserts a key
// is available — passed on this invocation or already stored on the
// deployed service — before the write is sent.
//
// mcpServiceJSON (deploy/stored) carries an openai provider AND a
// stored embedding_api_key; mcpWithAllowlistJSON's mcp_config carries
// neither, so it stands in for a deployed service with no embedding
// configuration at all.
func TestMCPEmbeddingProviderRequiresAPIKey(t *testing.T) {
	noService := databaseJSON(testDatabaseID, "")
	storedKey := databaseJSON(testDatabaseID, mcpServiceJSON)
	noKey := databaseJSON(testDatabaseID, mcpWithAllowlistJSON)

	tests := []struct {
		name    string
		verb    string
		db      string
		args    []string
		wantErr bool
	}{
		{
			name: "deploy: provider set, key passed this call",
			verb: "deploy",
			db:   noService,
			args: []string{"--embedding-provider", "openai",
				"--embedding-model", "text-embedding-3-small",
				"--embedding-api-key", "sk-new"},
		},
		{
			name: "update: provider set, key already stored",
			verb: "update",
			db:   storedKey,
			args: []string{"--embedding-provider", "openai",
				"--embedding-model", "text-embedding-3-small"},
		},
		{
			name: "deploy: provider set, no key anywhere",
			verb: "deploy",
			db:   noService,
			args: []string{"--embedding-provider", "openai",
				"--embedding-model", "text-embedding-3-small"},
			wantErr: true,
		},
		{
			name: "update: provider set, no key anywhere",
			verb: "update",
			db:   noKey,
			args: []string{"--embedding-provider", "openai",
				"--embedding-model", "text-embedding-3-small"},
			wantErr: true,
		},
		{
			name: "update: provider not touched, no check applies",
			verb: "update",
			db:   noKey,
			args: []string{"--allow-writes"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, tc.db))

			args := append([]string{"database", "mcp", tc.verb,
				testDatabaseID}, tc.args...)
			err := runAuthed(t, rt, out, url, args...)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			requireExitUsage(t, err)
			if !strings.Contains(err.Error(), "--embedding-api-key") {
				t.Errorf("error does not name the flag it demands: %v",
					err)
			}
		})
	}
}
