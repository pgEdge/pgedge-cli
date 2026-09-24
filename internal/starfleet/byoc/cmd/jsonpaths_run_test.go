package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestGetJSONPaths covers the machine-output branch of each get
// command: `if format != table/text { return Print(JSON200) }`, which
// the text-mode get tests skip.
func TestGetJSONPaths(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
		want string
	}{
		{"database", databaseBody,
			[]string{"database", "get", testDatabaseID}, testDatabaseID},
		{"database connection-string", byocDatabaseWithNodesJSON(publicNode),
			[]string{"database", "connection-string", testDatabaseID},
			testPublicHost},
		{"backup-store", storeBody,
			[]string{"backup-store", "get", testStoreID}, testStoreID},
		{"cloud-account", accountBody,
			[]string{"cloud-account", "get", testAccountID}, testAccountID},
		{"ingress", ingressBody,
			[]string{"ingress", "get", testIngressID}, testIngressID},
		{"ssh-key", sshKeyBody,
			[]string{"ssh-key", "get", testSSHKeyID}, testSSHKeyID},
		{"cluster share", shareBody,
			[]string{"cluster", "share", "get", testClusterID, testShareID},
			testShareID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, tc.body))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s get json: %v", tc.name, err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("%s: missing %q in %q",
					tc.name, tc.want, out.String())
			}
		})
	}
}

// TestListJSONDataPaths covers list commands in json mode with a
// non-empty body, plus the parent-scoped list commands.
func TestListJSONDataPaths(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
	}{
		{"ingress", `[` + ingressBody + `]`, []string{"ingress", "list"}},
		{"cloud-account", `[` + accountBody + `]`,
			[]string{"cloud-account", "list"}},
		{"ssh-key", `[` + sshKeyBody + `]`, []string{"ssh-key", "list"}},
		{"database service", dbWithServiceBody,
			[]string{"database", "service", "list", testDatabaseID}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, tc.body))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s list json: %v", tc.name, err)
			}
		})
	}
}
