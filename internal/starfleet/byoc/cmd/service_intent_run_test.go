package cmd

import (
	"bytes"
	"errors"
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
// tree, once per service type, mirroring managed's
// service_intent_run_test.go over byoc's own fixtures and generated
// types.
//
// dbWithServiceBody (mcp only) and dbWithRAGBody (rag only) already
// exist; postgrestOnlyDBBody fills the third gap so every "a different
// type is deployed" cell has a single-service fixture to reach for.

const postgrestOnlyDBBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
	`"services":[{"service_id":"svc-2","service_type":"postgrest",` +
	`"state":"available","postgrest_config":{"db_schemas":"public",` +
	`"db_anon_role":"web_anon"}}]}`

// requireExitOne asserts err is an *ExitError with ExitGeneral (1) —
// the precedent set by the existing "no RAG/PostgREST service
// deployed" sibling errors (database_rag.go, database_postgrest.go).
func requireExitOne(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
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
	return testsupport.RunAuthed(t, root, out, srvURL, byocArgs(args)...)
}

// --- mcp ---

func TestServiceIntentMCP(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
			testDatabaseID); err != nil {
			t.Fatalf("mcp deploy: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(dbWithServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
				testDatabaseID)
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "mcp update") {
				t.Errorf("error does not name mcp update: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
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
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(dbWithServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			err := runAuthedSilent(t, rt, out, url, "database", "mcp",
				"deploy", testDatabaseID)
			requireExitOne(t, err)
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty — a refusal must not "+
					"write anything under -o json", out.String())
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 3: update, present -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithServiceBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "mcp", "update",
			testDatabaseID, "--allow-writes"); err != nil {
			t.Fatalf("mcp update: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "update",
				testDatabaseID, "--allow-writes")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "mcp deploy") {
				t.Errorf("error does not name mcp deploy: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(dbWithRAGBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, "database", "mcp",
				"deploy", testDatabaseID); err != nil {
				t.Fatalf("mcp deploy alongside an existing rag "+
					"service: %v", err)
			}
			if rec.callCount() != 1 {
				t.Errorf("calls = %d, want exactly 1 write — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.callCount())
			}
		})

	// cell 6: the privilege-flip regression this issue exists to close.
	// A deployed MCP service with allow_writes:false must not be
	// silently escalated by `mcp deploy --allow-writes`.
	t.Run("cell 6: deploy --allow-writes on a read-only mcp is refused",
		func(t *testing.T) {
			const readOnlyMCP = `{"id":"` + testDatabaseID + `",` +
				`"name":"mydb","status":"available","pg_version":"16",` +
				`"cluster_id":"` + testClusterID + `",` +
				`"created_at":"2024-03-15T10:30:00Z","services":[` +
				`{"service_id":"svc-1","service_type":"mcp",` +
				`"state":"running","mcp_config":{"allow_writes":false}}]}`
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(readOnlyMCP))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
				testDatabaseID, "--allow-writes")
			requireExitOne(t, err)
			// The essential assertion: zero writes reached the
			// stub. An error raised AFTER a write would pass a naive
			// nil-check but still have escalated the service.
			if rec.callCount() != 0 {
				t.Fatalf("calls = %d, want 0 — allow-writes was sent "+
					"despite the guard", rec.callCount())
			}
			// A follow-up read shows the config untouched.
			rt2, out2, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt2, out2, url, "database", "service",
				"get", testDatabaseID, "svc-1"); err != nil {
				t.Fatalf("follow-up service get: %v", err)
			}
			if !strings.Contains(out2.String(), "mcp") {
				t.Errorf("follow-up read lost the service: %q",
					out2.String())
			}
		})
}

// --- rag ---

// ragDeployArgs returns the flags a first `rag deploy` needs, writing a
// pipeline config file for the duration of the test. One definition of
// that flag list, shared with the exit-code table.
func ragDeployArgs(t *testing.T, dbID string) []string {
	t.Helper()
	return ragArgsWithConfig("deploy", dbID,
		ragConfigPath(t, ragConfigCase{body: validPipelineJSON}))
}

func TestServiceIntentRAG(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		args := ragDeployArgs(t, testDatabaseID)
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("rag deploy: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(dbWithRAGBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := ragDeployArgs(t, testDatabaseID)
			err := runAuthed(t, rt, out, url, args...)
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "rag update") {
				t.Errorf("error does not name rag update: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 3: update, present -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithRAGBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "rag", "update",
			testDatabaseID, "--top-n", "7"); err != nil {
			t.Fatalf("rag update: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "rag", "update",
				testDatabaseID, "--top-n", "7")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "rag deploy") {
				t.Errorf("error does not name rag deploy: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(dbWithServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			args := ragDeployArgs(t, testDatabaseID)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("rag deploy alongside an existing mcp "+
					"service: %v", err)
			}
			if rec.callCount() != 1 {
				t.Errorf("calls = %d, want exactly 1 write — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.callCount())
			}
		})
}

// --- postgrest ---

func TestServiceIntentPostgREST(t *testing.T) {
	t.Run("cell 1: deploy, absent -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon"); err != nil {
			t.Fatalf("postgrest deploy: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 2: deploy, present -> exit 1, names update, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(postgrestOnlyDBBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "postgrest",
				"deploy", testDatabaseID, "--db-schemas", "public",
				"--db-anon-role", "web_anon")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "postgrest update") {
				t.Errorf("error does not name postgrest update: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 3: update, present -> one write", func(t *testing.T) {
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t,
			rec.handler(postgrestOnlyDBBody))
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500"); err != nil {
			t.Fatalf("postgrest update: %v", err)
		}
		if rec.callCount() != 1 {
			t.Errorf("calls = %d, want exactly 1 write", rec.callCount())
		}
	})

	t.Run("cell 4: update, absent -> exit 1, names deploy, no writes",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t, rec.handler(dbNoServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "postgrest",
				"update", testDatabaseID, "--max-rows", "500")
			requireExitOne(t, err)
			if !strings.Contains(err.Error(), "postgrest deploy") {
				t.Errorf("error does not name postgrest deploy: %v", err)
			}
			if rec.callCount() != 0 {
				t.Errorf("calls = %d, want 0 — a write reached the "+
					"stub despite the guard", rec.callCount())
			}
		})

	t.Run("cell 5: deploy, a different type deployed -> allowed",
		func(t *testing.T) {
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.handler(dbWithServiceBody))
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, "database", "postgrest",
				"deploy", testDatabaseID, "--db-schemas", "public",
				"--db-anon-role", "web_anon"); err != nil {
				t.Fatalf("postgrest deploy alongside an existing mcp "+
					"service: %v", err)
			}
			if rec.callCount() != 1 {
				t.Errorf("calls = %d, want exactly 1 write — the guard "+
					"must match on type, not on \"any service exists\"",
					rec.callCount())
			}
		})
}
