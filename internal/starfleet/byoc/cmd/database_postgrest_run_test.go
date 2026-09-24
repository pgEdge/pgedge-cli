package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// dbWithPostgRESTBody is a database carrying both an MCP and a
// PostgREST service, so the read-modify-write path has one service to
// preserve and one to replace.
const dbWithPostgRESTBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
	`"services":[` +
	`{"service_id":"svc-1","service_type":"mcp","state":"available",` +
	`"mcp_config":{"allow_writes":true}},` +
	`{"service_id":"svc-2","service_type":"postgrest",` +
	`"state":"available","postgrest_config":{"db_schemas":"public",` +
	`"db_anon_role":"web_anon","max_rows":1000}}]}`

// capturedApply records the body of the update request the CLI sends,
// so a test can assert on what actually went over the wire rather than
// only on the exit status. calls counts non-GET, non-/nodes requests —
// the deploy/update guard's essential assertion is that a
// refusal sends exactly zero of these, not merely that it returns an
// error.
type capturedApply struct {
	mu    sync.Mutex
	body  []byte
	calls int
}

// handler serves the database GET, its cluster's nodes, and records the
// applying request's body.
func (c *capturedApply) handler(dbBody string) http.HandlerFunc {
	nodes := `[{"id":"host-1","name":"n1","region":"us-east-1",` +
		`"instance_type":"r7g.medium","ip_address":"10.0.0.1"}]`
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			testsupport.JSONHandler(200, nodes)(w, r)
		case r.Method == http.MethodGet:
			testsupport.JSONHandler(200, dbBody)(w, r)
		default:
			data, _ := io.ReadAll(r.Body)
			c.mu.Lock()
			c.body = data
			c.calls++
			c.mu.Unlock()
			testsupport.JSONHandler(200, `{}`)(w, r)
		}
	}
}

// callCount returns the number of non-GET, non-/nodes requests recorded
// so far.
func (c *capturedApply) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// services decodes the recorded request body into its service list.
func (c *capturedApply) services(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.body) == 0 {
		t.Fatal("no apply request was recorded")
	}
	var payload struct {
		Services []map[string]any `json:"services"`
	}
	if err := json.Unmarshal(c.body, &payload); err != nil {
		t.Fatalf("decode apply body: %v\nbody: %s", err, c.body)
	}
	return payload.Services
}

// serviceOfType returns the single service of the given type from a
// recorded service list.
func serviceOfType(
	t *testing.T, svcs []map[string]any, svcType string,
) map[string]any {
	t.Helper()
	for _, svc := range svcs {
		if svc["service_type"] == svcType {
			return svc
		}
	}
	t.Fatalf("no %q service in payload: %+v", svcType, svcs)
	return nil
}

func TestDatabasePostgRESTDeployRun(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon"); err != nil {
			t.Fatalf("postgrest deploy: %v", err)
		}
		if !strings.Contains(errb.String(), "PostgREST service applied") {
			t.Errorf("missing applied message: %q", errb.String())
		}

		svc := serviceOfType(t, rec.services(t), "postgrest")
		cfg, ok := svc["postgrest_config"].(map[string]any)
		if !ok {
			t.Fatalf("no postgrest_config in payload: %+v", svc)
		}
		if cfg["db_schemas"] != "public" {
			t.Errorf("db_schemas = %v, want \"public\"", cfg["db_schemas"])
		}
		if cfg["db_anon_role"] != "web_anon" {
			t.Errorf("db_anon_role = %v, want \"web_anon\"",
				cfg["db_anon_role"])
		}
	})

	t.Run("deploy on an existing service is refused", func(t *testing.T) {
		// dbWithPostgRESTBody already carries a postgrest service, so
		// the deploy/update guard must refuse this before
		// sending anything, rather than reconfiguring it silently.
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithPostgRESTBody))
		err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon")
		if err == nil {
			t.Fatal("deploy against an already-deployed PostgREST " +
				"service was accepted")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
			t.Errorf("want exit %d, got %v", ExitGeneral, err)
		}
		if !strings.Contains(err.Error(), "postgrest update") {
			t.Errorf("error does not point at postgrest update: %v", err)
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.body) != 0 {
			t.Errorf("a request was sent despite the guard: %s", rec.body)
		}
	})

	t.Run("rejects an out-of-range flag before the request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithServiceBody))
		err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon", "--max-rows", "20000")
		if err == nil {
			t.Fatal("expected an error for --max-rows 20000")
		}
		if !strings.Contains(err.Error(), "must be between 1 and 10000") {
			t.Errorf("error = %q, want the range message", err.Error())
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.body) != 0 {
			t.Errorf("a request was sent despite the bad flag: %s", rec.body)
		}
	})

	t.Run("database not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"deploy", testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon"); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestDatabasePostgRESTUpdateRun(t *testing.T) {
	t.Run("partial update preserves deployed settings", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithPostgRESTBody))
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500"); err != nil {
			t.Fatalf("postgrest update: %v", err)
		}

		svc := serviceOfType(t, rec.services(t), "postgrest")
		cfg, ok := svc["postgrest_config"].(map[string]any)
		if !ok {
			t.Fatalf("no postgrest_config in payload: %+v", svc)
		}
		if cfg["max_rows"] != float64(500) {
			t.Errorf("max_rows = %v, want 500", cfg["max_rows"])
		}
		// Neither required field was passed on the command line, so
		// both must have come from the deployed service.
		if cfg["db_schemas"] != "public" {
			t.Errorf("db_schemas = %v, want preserved \"public\"",
				cfg["db_schemas"])
		}
		if cfg["db_anon_role"] != "web_anon" {
			t.Errorf("db_anon_role = %v, want preserved \"web_anon\"",
				cfg["db_anon_role"])
		}
	})

	// Preserves an existing MCP service and its config when
	// updating postgrest. dbWithPostgRESTBody carries both.
	t.Run("preserves an existing MCP service and its config", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithPostgRESTBody))
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500"); err != nil {
			t.Fatalf("postgrest update: %v", err)
		}

		mcp := serviceOfType(t, rec.services(t), "mcp")
		cfg, ok := mcp["mcp_config"].(map[string]any)
		if !ok {
			t.Fatalf("mcp_config dropped from preserved service: %+v", mcp)
		}
		if cfg["allow_writes"] != true {
			t.Errorf("allow_writes = %v, want true", cfg["allow_writes"])
		}
	})

	// errors when nothing is deployed to merge with pins the
	// deploy/update guard: updating a database with no
	// PostgREST service deployed fails client-side, naming `postgrest
	// deploy` as the fix, rather than sending a request that can only
	// 400.
	t.Run("errors when nothing is deployed to merge with", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t, rec.handler(dbWithServiceBody))
		err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500")
		if err == nil {
			t.Fatal("expected an error updating a non-deployed service")
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
			t.Errorf("want exit %d, got %v", ExitGeneral, err)
		}
		if !strings.Contains(err.Error(), "postgrest deploy") {
			t.Errorf("error = %q, want it to point at postgrest deploy",
				err.Error())
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if len(rec.body) != 0 {
			t.Errorf("a request was sent anyway: %s", rec.body)
		}
	})
}

// TestServiceApplyEmitsJSON guards the machine-readable output contract
// for all three service verbs. They used to write nothing at all to
// stdout under -o json — the "applied" line goes to stderr and
// trackMutation's hint is table/text-only — so a caller could not read
// back the service id, port or domain it had just created. A live
// lifecycle run caught this as an empty response body.
func TestServiceApplyEmitsJSON(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// dbBody is the database the stub serves. An update verb needs a
		// body that already carries its own service: the
		// update path merges with what is deployed, so `rag update`
		// against a database with no RAG service is now a clean
		// client-side error rather than a request. A deploy verb needs
		// the opposite: a body that does NOT already carry a
		// service of the type being deployed, or the deploy/update guard
		// refuses it before sending anything.
		dbBody string
	}{
		{"mcp deploy", []string{"database", "mcp", "deploy",
			testDatabaseID}, dbTwoNodePostgRESTBody},
		{"rag update", []string{"database", "rag", "update",
			testDatabaseID, "--top-n", "5"}, dbWithRAGBody},
		{"postgrest deploy", []string{"database", "postgrest", "deploy",
			testDatabaseID, "--db-schemas", "public",
			"--db-anon-role", "web_anon"}, dbWithServiceBody},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			rec := &capturedApply{}
			// The stub echoes the database back, as the real API does.
			handler := func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet &&
					!strings.HasSuffix(r.URL.Path, "/nodes") {
					testsupport.JSONHandler(200, tc.dbBody)(w, r)
					return
				}
				rec.handler(tc.dbBody)(w, r)
			}
			url := testsupport.NewAuthedServer(t, handler)
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			if out.Len() == 0 {
				t.Fatalf("%s: nothing written to stdout under -o json",
					tc.name)
			}
			var db map[string]any
			if err := json.Unmarshal(out.Bytes(), &db); err != nil {
				t.Fatalf("%s: stdout is not a JSON object: %v\nbody: %s",
					tc.name, err, out.String())
			}
			if db["id"] != testDatabaseID {
				t.Errorf("%s: id = %v, want %q",
					tc.name, db["id"], testDatabaseID)
			}
			if _, ok := db["services"]; !ok {
				t.Errorf("%s: rendered database carries no services key",
					tc.name)
			}
		})
	}
}

// dbTwoNodePostgRESTBody is a database on a two-node cluster with a
// PostgREST service already placed on host-1.
const dbTwoNodePostgRESTBody = `{"id":"` + testDatabaseID + `",` +
	`"name":"mydb","status":"available","pg_version":"16",` +
	`"cluster_id":"` + testClusterID + `",` +
	`"created_at":"2024-03-15T10:30:00Z","services":[` +
	`{"service_id":"svc-2","service_type":"postgrest",` +
	`"state":"running","host_id":"host-1",` +
	`"postgrest_config":{"db_schemas":"public",` +
	`"db_anon_role":"web_anon","max_rows":1000}}]}`

// twoNodeApplyHandler serves a two-node cluster, so resolveHostIDs
// refuses to guess placement.
func (c *capturedApply) twoNodeHandler(dbBody string) http.HandlerFunc {
	nodes := `[{"id":"host-1","name":"n1","region":"us-east-1",` +
		`"instance_type":"r6g.medium","ip_address":"10.0.0.1"},` +
		`{"id":"host-2","name":"n2","region":"us-east-1",` +
		`"instance_type":"r6g.medium","ip_address":"10.0.0.2"}]`
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			testsupport.JSONHandler(200, nodes)(w, r)
		case r.Method == http.MethodGet:
			testsupport.JSONHandler(200, dbBody)(w, r)
		default:
			data, _ := io.ReadAll(r.Body)
			c.mu.Lock()
			c.body = data
			c.mu.Unlock()
			testsupport.JSONHandler(200, dbBody)(w, r)
		}
	}
}

// TestPostgRESTUpdatePreservesPlacement covers a multi-node cluster,
// where resolveHostIDs refuses to pick nodes for you. An update must
// keep the placement the deploy chose rather than demanding
// --target-nodes again: a live run failed here with "cluster has 2
// nodes (n1, n2) — specify --target-nodes" on a plain --max-rows
// change, and re-specifying placement on every config edit risks
// moving the service by accident.
func TestPostgRESTUpdatePreservesPlacement(t *testing.T) {
	t.Run("update without target-nodes reuses deployed hosts",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rec := &capturedApply{}
			url := testsupport.NewAuthedServer(t,
				rec.twoNodeHandler(dbTwoNodePostgRESTBody))
			if err := runAuthed(t, rt, out, url, "database", "postgrest",
				"update", testDatabaseID, "--max-rows", "500"); err != nil {
				t.Fatalf("postgrest update on a two-node cluster: %v", err)
			}

			svc := serviceOfType(t, rec.services(t), "postgrest")
			hosts, ok := svc["host_ids"].([]any)
			if !ok {
				t.Fatalf("no host_ids in payload: %+v", svc)
			}
			if len(hosts) != 1 || hosts[0] != "host-1" {
				t.Errorf("host_ids = %v, want [host-1]", hosts)
			}
		})

	t.Run("explicit target-nodes still wins", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rec := &capturedApply{}
		url := testsupport.NewAuthedServer(t,
			rec.twoNodeHandler(dbTwoNodePostgRESTBody))
		if err := runAuthed(t, rt, out, url, "database", "postgrest",
			"update", testDatabaseID, "--max-rows", "500",
			"--target-nodes", "n2"); err != nil {
			t.Fatalf("postgrest update with target-nodes: %v", err)
		}

		svc := serviceOfType(t, rec.services(t), "postgrest")
		hosts, _ := svc["host_ids"].([]any)
		if len(hosts) != 1 || hosts[0] != "host-2" {
			t.Errorf("host_ids = %v, want [host-2]", hosts)
		}
	})

	t.Run("deploy on a multi-node cluster still requires target-nodes",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rec := &capturedApply{}
			// No PostgREST service deployed, so there is no placement
			// to inherit and the caller must say where it goes.
			url := testsupport.NewAuthedServer(t,
				rec.twoNodeHandler(dbWithServiceBody))
			err := runAuthed(t, rt, out, url, "database", "postgrest",
				"deploy", testDatabaseID, "--db-schemas", "public",
				"--db-anon-role", "web_anon")
			if err == nil {
				t.Fatal("expected an error without --target-nodes")
			}
			if !strings.Contains(err.Error(), "target-nodes") {
				t.Errorf("error = %q, want it to mention --target-nodes",
					err.Error())
			}
		})
}

// dbWithRAGBody is a database carrying a fully configured RAG service on
// a two-node cluster, for the partial-update acceptance tests.
const dbWithRAGBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
	`"services":[{"service_id":"svc-3","service_type":"rag",` +
	`"state":"running","host_id":"host-1","rag_config":{` +
	`"embedding_llm":{"provider":"openai","model":"text-embedding-3-small"},` +
	`"completion_llm":{"provider":"openai","model":"gpt-4o"},` +
	`"top_n":5,"pipelines":[{"name":"p1","tables":[` +
	`{"table":"public.docs","text_column":"content",` +
	`"vector_column":"embedding"}]}]}}]}`

// dbWithMCPOnTwoNodes is a database with an MCP service placed on
// host-1 of a two-node cluster.
const dbWithMCPOnTwoNodes = `{"id":"` + testDatabaseID + `",` +
	`"name":"mydb","status":"available","pg_version":"16",` +
	`"cluster_id":"` + testClusterID + `",` +
	`"created_at":"2024-03-15T10:30:00Z","services":[` +
	`{"service_id":"svc-1","service_type":"mcp","state":"running",` +
	`"host_id":"host-1","mcp_config":{"allow_writes":true}}]}`

// TestMCPUpdatePreservesPlacement is the acceptance test for the
// placement half of partial update. On a multi-node cluster an update must
// leave the service where it is rather than demanding --target-nodes
// again. Committed skipped; the skip is gone with the fix.
func TestMCPUpdatePreservesPlacement(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rec := &capturedApply{}
	url := testsupport.NewAuthedServer(t, rec.twoNodeHandler(dbWithMCPOnTwoNodes))

	if err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes"); err != nil {
		t.Fatalf("mcp update without --target-nodes: %v", err)
	}

	svc := serviceOfType(t, rec.services(t), "mcp")
	hosts, ok := svc["host_ids"].([]any)
	if !ok {
		t.Fatalf("no host_ids in payload: %+v", svc)
	}
	if len(hosts) != 1 || hosts[0] != "host-1" {
		t.Errorf("host_ids = %v, want the deployed [host-1]", hosts)
	}
}

// TestPostgRESTUpdateBlankingARequiredFieldIsUsage pins the one case
// the retained completeness check still answers for, and the reason
// its code is ExitUsage rather than inherited.
//
// `update --db-schemas ""` passes validatePostgRESTFlags -- that
// function's required-field arm is deploy's alone -- then the overlay
// blanks a field the deployed service had filled. The value is one the
// caller typed, so the refusal is theirs, and it can only be caught
// here, after the read that supplied the rest of the config.
func TestPostgRESTUpdateBlankingARequiredFieldIsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rec := &capturedApply{}
	url := testsupport.NewAuthedServer(t, rec.handler(dbWithPostgRESTBody))

	err := runAuthed(t, rt, out, url, "database", "postgrest",
		"update", testDatabaseID, "--db-schemas", "")
	if err == nil {
		t.Fatal("want a refusal for an explicitly blanked --db-schemas")
	}
	var ee *conn.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *conn.ExitError", err)
	}
	if ee.Code() != conn.ExitUsage {
		t.Errorf("code = %d, want ExitUsage (%d); err = %v",
			ee.Code(), conn.ExitUsage, err)
	}
	if !strings.Contains(err.Error(),
		"--db-schemas is required to deploy a PostgREST service") {
		t.Errorf("err = %q, want the completeness message", err.Error())
	}
}
