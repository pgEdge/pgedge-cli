package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestListEmptyText covers the "No X found" branch each list command
// takes in text mode when the server returns an empty array. Commands
// that take a parent ID argument get one.
func TestListEmptyText(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"database", []string{"database", "list"}, "No databases found"},
		{"backup-store", []string{"backup-store", "list"},
			"No backup stores found"},
		{"cloud-account", []string{"cloud-account", "list"},
			"No cloud accounts found"},
		{"ingress", []string{"ingress", "list"}, "No ingresses found"},
		{"ssh-key", []string{"ssh-key", "list"}, "No SSH keys found"},
		{"task", []string{"task", "list"}, "No tasks found"},
		{"cluster share",
			[]string{"cluster", "share", "list", testClusterID},
			"No shares found"},
		{"ingress service",
			[]string{"ingress", "service", "list", testIngressID},
			"No services found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s list empty: %v", tc.name, err)
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("%s: want %q, got %q",
					tc.name, tc.want, errb.String())
			}
		})
	}
}
