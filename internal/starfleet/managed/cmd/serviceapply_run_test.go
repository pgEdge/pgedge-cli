package cmd

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestServiceApplyDeploysOrUpdates: apply deploys when the read finds
// no service, updates when it finds one, and never writes after a read
// that failed.
func TestServiceApplyDeploysOrUpdates(t *testing.T) {
	ragFlags := []string{
		"--embedding-llm-provider", "openai",
		"--embedding-llm-model", "em",
		"--embedding-llm-api-key", "sk-e",
		"--completion-llm-provider", "anthropic",
		"--completion-llm-model", "cm",
		"--completion-llm-api-key", "sk-c",
	}
	cases := []struct {
		name      string
		args      []string
		status    int
		getBody   string
		wantCode  int
		wantWrite bool
		wantBody  string
	}{
		{"mcp absent deploys", []string{"mcp", "apply", testDatabaseID},
			200, databaseJSON(testDatabaseID, ""), 0, true,
			`"service_type":"mcp"`},
		{"mcp present updates", []string{"mcp", "apply", testDatabaseID,
			"--allow-writes"},
			200, databaseJSON(testDatabaseID, mcpServiceJSON), 0, true,
			`"service_id":"abc12345"`},
		{"rag absent deploys", append([]string{"rag", "apply",
			testDatabaseID, "--pipeline-config", "PIPELINE_CONFIG"},
			ragFlags...),
			200, databaseJSON(testDatabaseID, ""), 0, true,
			`"service_type":"rag"`},
		{"rag absent without deploy flags", []string{"rag", "apply",
			testDatabaseID, "--top-n", "5"},
			200, databaseJSON(testDatabaseID, ""), ExitUsage, false, ""},
		{"failed read deploys nothing", []string{"mcp", "apply",
			testDatabaseID},
			500, `{"code":500,"message":"boom"}`, ExitGeneral, false, ""},
		{"absent database deploys nothing", []string{"mcp", "apply",
			testDatabaseID},
			404, `{"code":404,"message":"managed database not found"}`,
			ExitNotFound, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writePipelineConfig(t)
			args := append([]string{"database"}, tc.args...)
			for i, a := range args {
				if a == "PIPELINE_CONFIG" {
					args[i] = path
				}
			}
			rec := &captureRequest{}
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			get := stubGetThenWrite(rec, tc.getBody, 200,
				databaseJSON(testDatabaseID, ""))
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && tc.status != 200 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.getBody))
						return
					}
					get(w, r)
				})
			err := runAuthed(t, rt, out, url, args...)
			if tc.wantCode == 0 && err != nil {
				t.Fatalf("want exit 0, got %v", err)
			}
			if tc.wantCode != 0 {
				var ee *ExitError
				if !asExitError(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
			}
			if (rec.Calls > 0) != tc.wantWrite {
				t.Fatalf("writes = %d, want a write: %v",
					rec.Calls, tc.wantWrite)
			}
			if !strings.Contains(rec.Body, tc.wantBody) {
				t.Errorf("write body lacks %s:\n%s", tc.wantBody, rec.Body)
			}
		})
	}
}

// TestServiceAllowlistFlags: --allow, --my-ip and --open become a new
// service's ip_allowlist in the deploy request, and are refused on a
// service that already exists.
func TestServiceAllowlistFlags(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		getBody   string
		wantCode  int
		wantRules string
	}{
		{"deploy with allow and my-ip", []string{"mcp", "deploy",
			testDatabaseID, "--allow", "198.51.100.0/24", "--my-ip"},
			databaseJSON(testDatabaseID, ""), 0,
			`"ip_allowlist":{"rules":[{"cidr":"198.51.100.0/24"},` +
				`{"cidr":"203.0.113.7/32"}]}`},
		{"apply new with open", []string{"mcp", "apply", testDatabaseID,
			"--open"},
			databaseJSON(testDatabaseID, ""), 0,
			`"ip_allowlist":{"rules":[{"cidr":"0.0.0.0/0"}]}`},
		{"apply existing refuses my-ip", []string{"mcp", "apply",
			testDatabaseID, "--my-ip"},
			databaseJSON(testDatabaseID, mcpServiceJSON), ExitUsage, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &captureRequest{}
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, stubAllowlist(rec,
				tc.getBody, "203.0.113.7", 200,
				databaseJSON(testDatabaseID, "")))
			err := runAuthed(t, rt, out, url,
				append([]string{"database"}, tc.args...)...)
			if tc.wantCode != 0 {
				var ee *ExitError
				if !asExitError(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
				if rec.Calls != 0 {
					t.Errorf("refused, yet %d write(s) went out", rec.Calls)
				}
				if !strings.Contains(err.Error(),
					"allowlist add "+testDatabaseID+" --service mcp") {
					t.Errorf("error names no allowlist command: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want exit 0, got %v", err)
			}
			if !strings.Contains(rec.Body, tc.wantRules) {
				t.Errorf("write lacks %s:\n%s", tc.wantRules, rec.Body)
			}
			open := strings.Contains(errb.String(), "now open")
			if open != strings.Contains(tc.wantRules, "0.0.0.0/0") {
				t.Errorf("open warning printed: %v; stderr %q",
					open, errb.String())
			}
		})
	}
}

// TestDBNameSwapsOnlyTheDatabase: --dbname is refused when a URI path
// segment cannot carry it, and otherwise changes the database and
// nothing else.
func TestDBNameSwapsOnlyTheDatabase(t *testing.T) {
	for _, bad := range []string{"", "a/b", "a?b", "a#b", "a\nb"} {
		t.Run("refuses "+bad, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
				http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))
			err := runAuthed(t, rt, out, url, "database",
				"connection-string", testDatabaseID, "--dbname", bad)
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Fatalf("want exit %d, got %v", ExitUsage, err)
			}
		})
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))
		if err := runAuthed(t, rt, out, url, append([]string{"database",
			"connection-string", testDatabaseID}, args...)...); err != nil {
			t.Fatalf("connection-string: %v", err)
		}
		return strings.TrimSpace(out.String())
	}
	plain := run(t)
	swapped := run(t, "--dbname", "my app")
	if want := strings.Replace(plain, "/mydb?", "/my%20app?", 1); swapped != want {
		t.Errorf("--dbname changed more than the database:\n got %s\nwant %s",
			swapped, want)
	}

	t.Run("env pull", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		dir := inProject(t)
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			http.StatusOK, databaseWithConnectionJSON(testDatabaseID)))
		if err := runAuthed(t, rt, out, url, "database", "env", "pull",
			testDatabaseID, "--dbname", "other"); err != nil {
			t.Fatalf("env pull: %v", err)
		}
		b := readFile(t, filepath.Join(dir, ".env"))
		if !strings.Contains(b, ":5432/other?sslmode=require") {
			t.Errorf(".env does not name the other database: %q", b)
		}
	})
}
