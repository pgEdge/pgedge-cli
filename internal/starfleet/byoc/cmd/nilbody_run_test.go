package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestNilBodyBranches covers the "accepted but no details returned"
// branches: a 2xx response that carries no parseable JSON200 body, so
// each command's `resp.JSON200 == nil` path runs. A 202 keeps
// checkResponse happy while leaving JSON200 unset (the parser only
// fills it for 200).
func TestNilBodyBranches(t *testing.T) {
	getCases := []struct {
		name string
		args []string
	}{
		{"cluster get", []string{"cluster", "get", testClusterID}},
		{"database get", []string{"database", "get", testDatabaseID}},
		{"backup-store get", []string{"backup-store", "get", testStoreID}},
		{"cloud-account get", []string{"cloud-account", "get", testAccountID}},
		{"ingress get", []string{"ingress", "get", testIngressID}},
		{"ssh-key get", []string{"ssh-key", "get", testSSHKeyID}},
		{"cluster share get",
			[]string{"cluster", "share", "get", testClusterID, testShareID}},
	}
	for _, tc := range getCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(202, `{}`))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}

	createCases := []struct {
		name string
		args []string
	}{
		{"cluster create", []string{"cluster", "create", "--name", "c",
			"--cloud-account-id", testClusterID, "--regions", "us-east-1",
			"--node-location", "public"}},
		{"database create", []string{"database", "create", "--name", "d",
			"--cluster-id", testClusterID}},
		{"backup-store create", []string{"backup-store", "create",
			"--name", "s", "--cloud-account-id", testClusterID}},
		{"cloud-account create", []string{"cloud-account", "create",
			"--type", "aws", "--role-arn", "arn:x"}},
		{"ingress create", []string{"ingress", "create", "--name", "web",
			"--cluster-id", testClusterID, "--region", "us-east-1"}},
		{"ssh-key create", []string{"ssh-key", "create", "--name", "k",
			"--public-key", fixturePublicKey}},
		{"cluster share create", []string{"cluster", "share", "create",
			testClusterID, "--name", "sh"}},
		{"ingress service register", []string{"ingress", "service",
			"register", testIngressID, "--database-id", testDatabaseID,
			"--service-id", "svc-1"}},
	}
	for _, tc := range createCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(202, `{}`))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}
}
