package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const (
	testMCPURI   = "https://mydb.example.pgedge.cloud/mcp/v1"
	testMCPToken = "mcp-bearer-secret"
	// The database GET carries these too; none may reach the output.
	testDBPassword = "db-password-secret"
	testEmbedKey   = "embedding-key-secret"
)

// mcpDatabaseJSON is a database GET carrying a connection password and
// an MCP service with an embedding key, whose uri and init_tokens are
// raw JSON so null and "" can be passed.
func mcpDatabaseJSON(uri, token string) string {
	return fmt.Sprintf(`{
		"id":%q,"name":"mydb","status":"available",
		"region":"us-east-1","size":"small","pg_version":"16",
		"created_at":"2026-07-31T10:00:00Z",
		"updated_at":"2026-07-31T10:00:00Z",
		"connection":{"host":"h","port":5432,"database":"mydb",
			"username":"app","password":%q},
		"services":[{"service_type":"mcp","uri":%s,
			"mcp_config":{"embedding_api_key":%q,"init_tokens":%s}}]}`,
		testDatabaseID, testDBPassword, uri, testEmbedKey, token)
}

func TestMCPConnectionPrintsOnlyTheEndpointAndToken(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		for _, noToken := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/no-token=%v", format, noToken),
				func(t *testing.T) {
					rt, out, _ := testsupport.NewRuntime(t, "", format)
					url := testsupport.NewAuthedServer(t,
						testsupport.JSONHandler(http.StatusOK,
							mcpDatabaseJSON(fmt.Sprintf("%q", testMCPURI),
								fmt.Sprintf("%q", testMCPToken))))
					args := []string{"database", "mcp", "connection",
						testDatabaseID}
					if noToken {
						args = append(args, "--no-token")
					}
					if err := runAuthed(t, rt, out, url,
						args...); err != nil {
						t.Fatalf("mcp connection: %v", err)
					}
					got := out.String()
					if !strings.Contains(got, testMCPURI) {
						t.Errorf("uri missing:\n%s", got)
					}
					if strings.Contains(got, testMCPToken) == noToken {
						t.Errorf("token present = %v under "+
							"--no-token=%v:\n%s", !noToken, noToken, got)
					}
					if noToken && strings.Contains(got, "token") {
						t.Errorf("token field present under "+
							"--no-token:\n%s", got)
					}
					for _, secret := range []string{testDBPassword,
						testEmbedKey} {
						if strings.Contains(got, secret) {
							t.Errorf("output carries %q:\n%s", secret, got)
						}
					}
				})
		}
	}
}

func TestMCPConnectionRefusesWhatAClientCannotUse(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"null token", mcpDatabaseJSON(fmt.Sprintf("%q", testMCPURI),
			"null"), "no bearer token"},
		{"empty token", mcpDatabaseJSON(fmt.Sprintf("%q", testMCPURI),
			`""`), "no bearer token"},
		{"no uri", mcpDatabaseJSON("null", fmt.Sprintf("%q",
			testMCPToken)), "no URL yet"},
		{"no mcp service", databaseJSON(testDatabaseID, "[]"),
			"has no MCP service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
				http.StatusOK, tc.body))
			err := runAuthed(t, rt, out, url, "database", "mcp",
				"connection", testDatabaseID)
			wantExit(t, err, ExitGeneral, tc.want)
			// out also holds cobra's usage text in this harness, so
			// the check is for the value lines, not for emptiness.
			if strings.Contains(out.String(), "URI:") ||
				strings.Contains(out.String(), "Token:") {
				t.Errorf("value printed on refusal: %q", out.String())
			}
		})
	}
}

// A branch's MCP server has its own token, so --branch must read the
// branch GET rather than the source database's.
func TestMCPConnectionBranchReadsTheBranch(t *testing.T) {
	const branchToken = "branch-bearer-secret"
	branch := strings.TrimSuffix(branchJSON(testDatabaseID, testBranchID),
		"}") + fmt.Sprintf(`,"services":[{"service_type":"mcp",`+
		`"uri":%q,"mcp_config":{"init_tokens":%q}}]}`,
		testMCPURI, branchToken)
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.PathHandler(t,
		"/managed/v1/databases/"+testDatabaseID+"/branches/"+testBranchID,
		http.StatusOK, branch))

	if err := runAuthed(t, rt, out, url, "database", "mcp", "connection",
		testDatabaseID, "--branch", testBranchID); err != nil {
		t.Fatalf("mcp connection --branch: %v", err)
	}
	if !strings.Contains(out.String(), branchToken) {
		t.Errorf("branch token missing:\n%s", out.String())
	}
}
