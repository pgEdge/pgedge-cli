package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The deploy/update guard matrix (#117).
//
// `deploy` and `update` share one apply helper per service type, and
// until now that helper never asked whether the type it was about to
// write already existed. `mcp deploy --allow-writes` against an
// already-deployed read-only MCP service silently escalated its
// privileges — the hazard the issue reported. guardServiceIntent closes
// both directions: deploy refuses when the type is already deployed,
// pointing at update; update refuses when it is not, pointing at
// deploy. This file drives that guard directly through the command
// tree, once per service type, so a regression in either direction
// surfaces here rather than only in a service-specific test that
// happens to also exercise it.
//
// ragOnlyServiceJSON and postgrestOnlyServiceJSON let the mcp cases
// prove the guard matches on TYPE, not on "any service is deployed" —
// cell 5 of the matrix below.

const ragOnlyServiceJSON = `[
	{"service_id":"rag00001","service_type":"rag","state":"running",
	 "rag_config":{
		"embedding_llm":{"provider":"openai","model":"em"},
		"completion_llm":{"provider":"anthropic","model":"cm"},
		"pipelines":[{"name":"docs","tables":[
			{"table":"t","text_column":"c","vector_column":"v"}]}]}}
]`

const postgrestOnlyServiceJSON = `[
	{"service_id":"pgr00001","service_type":"postgrest","state":"running",
	 "postgrest_config":{"db_schemas":"public","db_anon_role":"anon"}}
]`

// requireExitOne asserts err is an *ExitError with ExitGeneral (1) —
// the precedent set by the existing "no RAG/PostgREST service
// deployed" sibling errors (database_rag.go, database_postgrest.go).
func requireExitOne(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Errorf("want exit %d, got %v", ExitGeneral, err)
	}
}

// runAuthedSilent behaves like runAuthed but matches the real root's
// SilenceErrors/SilenceUsage (internal/cli/root.go: both true). The
// synthetic test root newStarfleetRoot builds does not set them, so a
// command error makes cobra itself print "Error: ..." plus the usage
// text into the same buffer application output goes to — reusing
// runAuthed for a stdout-purity assertion would fail on that
// test-harness artifact, not on anything the real CLI does.
func runAuthedSilent(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	srvURL string, args ...string,
) error {
	t.Helper()
	root := newStarfleetRoot(rt)
	root.SilenceErrors = true
	root.SilenceUsage = true
	return testsupport.RunAuthed(t, root, out, srvURL, managedArgs(args)...)
}

// --- mcp ---

// TestServiceIntentMCP drives the six-cell matrix for mcp. MCP has no
// required deploy flag, so its args are just the database id.
func TestServiceIntentMCP(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, ""),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
			testDatabaseID); err != nil {
			t.Fatalf("mcp deploy: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, mcpServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
				testDatabaseID)
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "mcp update") {
				t.Errorf("error does not name mcp update: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	// Machine-output purity: a guard refusal must not print anything
	// to stdout under -o json. printUpdatedDatabase only ever runs
	// after a successful write, but the guard returns before that
	// point is reached, so this pins the ordering rather than assuming
	// it — a caller parsing stdout as JSON must never see a stray
	// error payload mixed in with real output.
	t.Run("cell 2b: deploy, present, -o json -> stdout stays empty",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, mcpServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			err := runAuthedSilent(t, rt, out, url, "database", "mcp",
				"deploy", testDatabaseID)
			requireExitOne(t, err)
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty — a refusal must not "+
					"write anything under -o json", out.String())
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 3: update, present -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, mcpServiceJSON),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "mcp", "update",
			testDatabaseID, "--allow-writes"); err != nil {
			t.Fatalf("mcp update: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, ""),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "update",
				testDatabaseID, "--allow-writes")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "mcp deploy") {
				t.Errorf("error does not name mcp deploy: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, ragOnlyServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, "database", "mcp",
				"deploy", testDatabaseID); err != nil {
				t.Fatalf("mcp deploy alongside an existing rag "+
					"service: %v", err)
			}
			if rec.Calls != 1 {
				t.Errorf("Calls = %d, want exactly 1 PATCH — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.Calls)
			}
		})

	// cell 6: the privilege-flip regression this issue exists to close.
	// A deployed MCP service with allow_writes:false must not be
	// silently escalated by `mcp deploy --allow-writes`.
	t.Run("cell 6: deploy --allow-writes on a read-only mcp is refused",
		func(t *testing.T) {
			const readOnlyMCP = `[{
				"service_id":"abc12345","service_type":"mcp",
				"state":"running",
				"mcp_config":{"allow_writes":false}}]`
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, readOnlyMCP),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
				testDatabaseID, "--allow-writes")
			requireExitOne(t, err)
			// The essential assertion: zero non-GET requests reached
			// the stub. An error raised AFTER a PATCH would pass a
			// naive nil-check but still have escalated the service.
			if rec.Calls != 0 {
				t.Fatalf("Calls = %d, want 0 — allow-writes was sent "+
					"despite the guard", rec.Calls)
			}
			// A follow-up read shows the config untouched.
			rt2, out3, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt2, out3, url, "database", "service",
				"get", testDatabaseID, "mcp"); err != nil {
				t.Fatalf("follow-up service get: %v", err)
			}
			if !strings.Contains(out3.String(), "mcp") {
				t.Errorf("follow-up read lost the service: %q",
					out3.String())
			}
		})
}

// --- rag ---

// ragDeployArgs returns the flags a first `rag deploy` needs, writing a
// pipeline config file for the duration of the test. One definition of
// that flag list, shared with the exit-code table.
func ragDeployArgs(t *testing.T, dbID string) []string {
	t.Helper()
	return ragArgsWithConfig("deploy", dbID, writePipelineConfig(t))
}

func TestServiceIntentRAG(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, ""),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		args := ragDeployArgs(t, testDatabaseID)
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("rag deploy: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, ragOnlyServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := ragDeployArgs(t, testDatabaseID)
			err := runAuthed(t, rt, out, url, args...)
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "rag update") {
				t.Errorf("error does not name rag update: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 3: update, present -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, ragOnlyServiceJSON),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "rag", "update",
			testDatabaseID, "--top-n", "7"); err != nil {
			t.Fatalf("rag update: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, ""),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "rag", "update",
				testDatabaseID, "--top-n", "7")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "rag deploy") {
				t.Errorf("error does not name rag deploy: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, mcpServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := ragDeployArgs(t, testDatabaseID)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("rag deploy alongside an existing mcp "+
					"service: %v", err)
			}
			if rec.Calls != 1 {
				t.Errorf("Calls = %d, want exactly 1 PATCH — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.Calls)
			}
		})
}

// --- postgrest ---

func TestServiceIntentPostgREST(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, ""),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "anon"); err != nil {
			t.Fatalf("postgrest deploy: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, postgrestOnlyServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "postgrest",
				"deploy", testDatabaseID, "--db-schemas", "public",
				"--db-anon-role", "anon")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "postgrest update") {
				t.Errorf("error does not name postgrest update: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 3: update, present -> one PATCH", func(t *testing.T) {
		rec := &captureRequest{}
		url := testsupport.NewAuthedServer(t, stubGetThenWrite(
			rec, databaseJSON(testDatabaseID, postgrestOnlyServiceJSON),
			http.StatusOK, databaseJSON(testDatabaseID, "")))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500"); err != nil {
			t.Fatalf("postgrest update: %v", err)
		}
		if rec.Calls != 1 {
			t.Errorf("Calls = %d, want exactly 1 PATCH", rec.Calls)
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, ""),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "postgrest",
				"update", testDatabaseID, "--max-rows", "500")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "postgrest deploy") {
				t.Errorf("error does not name postgrest deploy: %v", err)
			}
			if rec.Calls != 0 {
				t.Errorf("Calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.Calls)
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, mcpServiceJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, "database", "postgrest",
				"deploy", testDatabaseID, "--db-schemas", "public",
				"--db-anon-role", "anon"); err != nil {
				t.Fatalf("postgrest deploy alongside an existing mcp "+
					"service: %v", err)
			}
			if rec.Calls != 1 {
				t.Errorf("Calls = %d, want exactly 1 PATCH — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.Calls)
			}
		})
}
