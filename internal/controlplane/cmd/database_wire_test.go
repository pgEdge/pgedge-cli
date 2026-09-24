package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOperationalVerbsSendFlagsOnWire asserts that each operational
// verb's distinguishing flags actually reach the HTTP request — as a
// query parameter (--force-unmodifiable, --force-lost) or in the JSON
// body (--type, --candidate, --scheduled-at, --skip-validation,
// restore_config, --image) — rather than only that the command exits
// without error. It also locks --force to its prompt-skip-only meaning
// on every verb that sends a wire force: --force alone must NOT put
// force=true on the wire, and on upgrade, failover and the restarts it
// cannot (those endpoints have no API force parameter).
func TestOperationalVerbsSendFlagsOnWire(t *testing.T) {
	dir := t.TempDir()
	restoreSpec := filepath.Join(dir, "restore.yaml")
	if err := os.WriteFile(
		restoreSpec, []byte(restoreSpecYAML), 0o600); err != nil {
		t.Fatalf("write restore spec: %v", err)
	}

	cases := []struct {
		name      string
		args      []string
		resp      string
		wantQuery string   // substring required in the request query
		wantBody  []string // substrings required in the request body
		absent    []string // substrings that must appear in NEITHER
	}{
		{
			name: "node backup --type incr in body",
			args: []string{"database", "node", "backup",
				"storefront", "n1", "--type", "incr"},
			resp:     nodeTaskResp("backup"),
			wantBody: []string{`"type":"incr"`},
		},
		{
			name: "node backup --force-unmodifiable in query",
			args: []string{"database", "node", "backup",
				"storefront", "n1", "--force-unmodifiable"},
			resp:      nodeTaskResp("backup"),
			wantQuery: "force=true",
			wantBody:  []string{`"type":"full"`},
		},
		{
			name: "node switchover body, force not on wire",
			args: []string{"database", "node", "switchover",
				"storefront", "n1", "--candidate", "inst-2",
				"--scheduled-at", "2026-07-17T22:00:00Z", "--force"},
			resp: nodeTaskResp("switchover"),
			wantBody: []string{
				`"candidate_instance_id":"inst-2"`, `"scheduled_at"`},
			// Bare "force", not "force=true": this endpoint takes a
			// body, so a leak could read "force":true and slip past a
			// query-shaped assertion. Proven -- with a Force field
			// added to the request body, the "force=true" form passed
			// and this form fails.
			absent: []string{"force"},
		},
		{
			name: "node failover flags in body, force not on wire",
			args: []string{"database", "node", "failover",
				"storefront", "n1", "--candidate", "inst-2",
				"--skip-validation", "--force"},
			resp: nodeTaskResp("failover"),
			wantBody: []string{
				`"candidate_instance_id":"inst-2"`,
				`"skip_validation":true`},
			// Bare, for the reason switchover's arm above is.
			absent: []string{"force"},
		},
		{
			name: "instance start --force-unmodifiable in query",
			args: []string{"database", "instance", "start",
				"storefront", "inst-1", "--force-unmodifiable"},
			resp:      instTaskResp("start"),
			wantQuery: "force=true",
		},
		{
			name: "instance stop --force-unmodifiable in query",
			args: []string{"database", "instance", "stop",
				"storefront", "inst-1", "--force",
				"--force-unmodifiable"},
			resp:      instTaskResp("stop"),
			wantQuery: "force=true",
		},
		{
			// The --force split: it skips the prompt and ONLY that.
			// Before 2026-08-24 it also carried the server's
			// unmodifiable-state override; a script relying on that
			// now needs --force-unmodifiable.
			name: "instance stop --force alone sends no force",
			args: []string{"database", "instance", "stop",
				"storefront", "inst-1", "--force"},
			resp:   instTaskResp("stop"),
			absent: []string{"force=true"},
		},
		{
			name: "instance restart --scheduled-at in body, " +
				"force not on wire",
			args: []string{"database", "instance", "restart",
				"storefront", "inst-1", "--force",
				"--scheduled-at", "2026-07-17T22:00:00Z"},
			resp:     instTaskResp("restart"),
			wantBody: []string{`"scheduled_at"`},
			// Bare "force", not "force=true": this endpoint takes a
			// body, so a leak could read `"force":true` and slip past
			// a query-shaped assertion.
			absent: []string{"force"},
		},
		{
			name: "database upgrade --image in body, force not on wire",
			args: []string{"database", "upgrade", "storefront",
				"--image", "pgedge/pg:16.4", "--force"},
			resp:     upgradeResp,
			wantBody: []string{`"image":"pgedge/pg:16.4"`},
			absent:   []string{"force=true"},
		},
		{
			name: "database restore --force-unmodifiable in query, " +
				"config in body",
			args: []string{"database", "restore", "storefront",
				"-f", restoreSpec, "--force", "--force-unmodifiable"},
			resp:      restoreResp,
			wantQuery: "force=true",
			wantBody: []string{
				`"restore_config"`, `"source_database_id":"old-store"`},
		},
		{
			// The --force split, restore's arm.
			name: "database restore --force alone sends no force",
			args: []string{"database", "restore", "storefront",
				"-f", restoreSpec, "--force"},
			resp:   restoreResp,
			absent: []string{"force=true"},
		},
		{
			name: "database delete --force-unmodifiable in query",
			args: []string{"database", "delete", "storefront",
				"--force", "--force-unmodifiable"},
			resp:      deleteMatrixResp,
			wantQuery: "force=true",
		},
		{
			// The --force split, delete's arm: --force alone must not
			// carry the unmodifiable override onto the wire.
			name: "database delete --force alone sends no force",
			args: []string{"database", "delete", "storefront",
				"--force"},
			resp:   deleteMatrixResp,
			absent: []string{"force=true"},
		},
		{
			name: "host remove --force-lost in query",
			args: []string{"host", "remove", "host-3",
				"--force", "--force-lost"},
			resp:      hostRemoveMatrixResp,
			wantQuery: "force=true",
		},
		{
			// The --force split, host remove's arm — the override here
			// waives QUORUM, so an accidental send is the worst of
			// the six.
			name:   "host remove --force alone sends no force",
			args:   []string{"host", "remove", "host-3", "--force"},
			resp:   hostRemoveMatrixResp,
			absent: []string{"force=true"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			var rec capturedRequest
			url := captureServer(t, 200, tc.resp, &rec)
			if err := runControlplane(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("run: %v", err)
			}
			if tc.wantQuery != "" &&
				!strings.Contains(rec.query, tc.wantQuery) {
				t.Errorf("query = %q, want substring %q",
					rec.query, tc.wantQuery)
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(rec.body, want) {
					t.Errorf("body = %q, want substring %q",
						rec.body, want)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(rec.query, no) ||
					strings.Contains(rec.body, no) {
					t.Errorf("wire unexpectedly contains %q "+
						"(query=%q body=%q)", no, rec.query, rec.body)
				}
			}
		})
	}
}
