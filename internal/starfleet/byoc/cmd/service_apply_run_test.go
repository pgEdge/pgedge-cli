package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestServiceApplyDeploysOrUpdates: apply deploys when the read finds
// no service of its type, updates the deployed one in place when it
// finds one, starts a new service read-only, and never writes after a
// read that failed.
func TestServiceApplyDeploysOrUpdates(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		getStatus int
		getBody   string
		wantCode  int
		wantIn    string
		wantOut   string
	}{
		{"mcp absent deploys read-only", []string{"mcp", "apply"},
			200, dbNoServiceBody, 0, `"service_type":"mcp"`,
			`"allow_writes"`},
		{"mcp present updates in place", []string{"mcp", "apply",
			"--init-tokens", "tok"},
			200, dbWithMCPConfig, 0, `"embedding_provider":"openai"`,
			`tok-deployed`},
		{"rag absent without deploy flags", []string{"rag", "apply",
			"--top-n", "5"},
			200, dbNoServiceBody, ExitUsage, "", ""},
		{"postgrest absent without schemas", []string{"postgrest",
			"apply", "--max-rows", "5"},
			200, dbNoServiceBody, ExitUsage, "", ""},
		{"postgrest absent deploys", []string{"postgrest", "apply",
			"--db-schemas", "public", "--db-anon-role", "web_anon"},
			200, dbNoServiceBody, 0, `"service_type":"postgrest"`, ""},
		{"failed read deploys nothing", []string{"mcp", "apply"},
			500, `{"code":500,"message":"boom"}`, ExitGeneral, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &capturedApply{}
			ok := rec.handler(tc.getBody)
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && tc.getStatus != 200 {
						testsupport.JSONHandler(tc.getStatus,
							tc.getBody)(w, r)
						return
					}
					ok(w, r)
				})
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := append([]string{"database"}, tc.args[0], tc.args[1],
				testDatabaseID)
			args = append(args, tc.args[2:]...)
			err := runAuthed(t, rt, out, url, args...)
			if tc.wantCode != 0 {
				var ee *ExitError
				if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
				if rec.callCount() != 0 {
					t.Fatalf("refused, yet %d write(s) went out",
						rec.callCount())
				}
				return
			}
			if err != nil {
				t.Fatalf("want exit 0, got %v", err)
			}
			body := string(rec.body)
			if rec.callCount() != 1 || !strings.Contains(body, tc.wantIn) {
				t.Fatalf("writes = %d, want one carrying %s:\n%s",
					rec.callCount(), tc.wantIn, body)
			}
			if tc.wantOut != "" && strings.Contains(body, tc.wantOut) {
				t.Errorf("write carries %s:\n%s", tc.wantOut, body)
			}
		})
	}
}
