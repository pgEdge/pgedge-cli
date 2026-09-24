package integration_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestBYOCListVerbs exercises every read-only BYOC list verb against
// the configured environment. The CLI is a typed client generated
// from the spec, so a materially changed response fails to decode and
// the verb errors — this suite is the regression test for the API
// path migration in saas PR #1791.
func TestBYOCListVerbs(t *testing.T) {
	requireIntegration(t)

	cases := []struct {
		name string
		args []string
		// keys asserted non-empty on the first element, when the
		// list is non-empty.
		keys []string
	}{
		{
			"cluster", []string{"byoc", "cluster", "list"},
			[]string{"id", "name", "status", "created_at"},
		},
		{
			"database", []string{"byoc", "database", "list"},
			[]string{"id", "name", "status", "created_at"},
		},
		{
			"backup", []string{"byoc", "backup", "list"},
			[]string{"id", "database_id", "name", "status"},
		},
		{
			"backup-store", []string{"byoc", "backup-store", "list"},
			[]string{"id", "name", "status", "created_at"},
		},
		{
			"cloud-account",
			[]string{"byoc", "cloud-account", "list"},
			[]string{"id", "name", "type", "created_at"},
		},
		{
			"ingress", []string{"byoc", "ingress", "list"},
			[]string{"id", "name", "status", "created_at"},
		},
		{
			"ssh-key", []string{"byoc", "ssh-key", "list"},
			[]string{"id", "name", "public_key"},
		},
		{
			"task", []string{"byoc", "task", "list"},
			[]string{"id", "name", "status", "created_at"},
		},
		{
			"invite", []string{"byoc", "invite", "list"},
			[]string{"id", "email", "created_at"},
		},
		{
			"membership", []string{"byoc", "membership", "list"},
			[]string{"id", "user_email", "created_at"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := listOf(t, tc.args...)
			t.Logf("%s: %d item(s)", tc.name, len(items))
			if len(items) == 0 {
				t.Skipf("no %s rows in this environment", tc.name)
			}
			assertNonEmpty(t, tc.name+" list[0]", items[0], tc.keys)
		})
	}
}

// TestBYOCGetVerbs resolves an ID from each list verb and fetches the
// single-resource endpoint, covering the /{id} paths the list sweep
// does not reach.
func TestBYOCGetVerbs(t *testing.T) {
	requireIntegration(t)

	cases := []struct {
		name     string
		listArgs []string
		getArgs  []string
		keys     []string
	}{
		{
			"cluster",
			[]string{"byoc", "cluster", "list"},
			[]string{"byoc", "cluster", "get"},
			[]string{"id", "name", "status"},
		},
		{
			"database",
			[]string{"byoc", "database", "list"},
			[]string{"byoc", "database", "get"},
			[]string{"id", "name", "status"},
		},
		{
			"backup",
			[]string{"byoc", "backup", "list"},
			[]string{"byoc", "backup", "get"},
			[]string{"id", "database_id", "name"},
		},
		{
			"cloud-account",
			[]string{"byoc", "cloud-account", "list"},
			[]string{"byoc", "cloud-account", "get"},
			[]string{"id", "name", "type"},
		},
		{
			"ssh-key",
			[]string{"byoc", "ssh-key", "list"},
			[]string{"byoc", "ssh-key", "get"},
			[]string{"id", "name", "public_key"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := listOf(t, tc.listArgs...)
			if len(items) == 0 {
				t.Skipf("no %s rows in this environment", tc.name)
			}
			id, ok := items[0]["id"].(string)
			if !ok || id == "" {
				t.Fatalf("%s list[0] has no usable id: %v",
					tc.name, items[0])
			}

			out := runJSON(t, append(tc.getArgs, id)...)
			var obj map[string]any
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatalf("%s get %s: %v\nbody: %s",
					tc.name, id, err, truncate(out))
			}
			assertNonEmpty(t,
				fmt.Sprintf("%s get %s", tc.name, id), obj, tc.keys)
		})
	}
}
