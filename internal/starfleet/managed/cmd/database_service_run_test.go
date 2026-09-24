package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// serviceTypesIn returns the service_type values present in a captured
// PATCH body, so a test can assert which services survived a write.
func serviceTypesIn(t *testing.T, body string) []string {
	t.Helper()
	var payload struct {
		Services []struct {
			ServiceType string `json:"service_type"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("PATCH body was not JSON: %v (%q)", err, body)
	}
	out := make([]string, 0, len(payload.Services))
	for _, s := range payload.Services {
		out = append(out, s.ServiceType)
	}
	return out
}

// --- service list / get ---

func TestServiceListRendersRows(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	err := runAuthed(t, rt, out, url,
		"database", "service", "list", testDatabaseID)
	if err != nil {
		t.Fatalf("service list: %v", err)
	}
	// STATE is derived by the API. ENDPOINT is the uri the API reports,
	// passed through — the fixture carries one, as a current API
	// response does for any database whose domain is assigned. The
	// derivation that runs when it is absent is pinned by
	// TestServiceEndpoint, which is also the only place the two are
	// made to disagree.
	for _, want := range []string{
		"abc12345", "mcp", "STATE", "running", "ENDPOINT",
		"https://demo-db.use2.example.com/mcp",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q; got:\n%s", want, out.String())
		}
	}
	// PORT stays absent. The API always sends 443 for a managed
	// service, so a column would repeat one constant down every row
	// while the dialable locator already carries it. The fixture's 8080
	// must not reach the table.
	for _, unwanted := range []string{"PORT", "8080"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("output has %q, which the table folds into "+
				"ENDPOINT; got:\n%s", unwanted, out.String())
		}
	}
}

// TestServiceListJSONKeepsStateAndPort pins the machine-output
// contract: dropping STATE/PORT from the table must not touch -o
// json, which renders the API object as-is. When the platform starts
// populating these fields, json output already carries them
// unchanged.
func TestServiceListJSONKeepsStateAndPort(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	err := runAuthed(t, rt, out, url,
		"database", "service", "list", testDatabaseID)
	if err != nil {
		t.Fatalf("service list: %v", err)
	}
	for _, want := range []string{`"state":"running"`, `"port":8080`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json output missing %q; got:\n%s", want, out.String())
		}
	}
}

func TestServiceListEmptyIsNotAnError(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	err := runAuthed(t, rt, out, url,
		"database", "service", "list", testDatabaseID)
	if err != nil {
		t.Fatalf("no services treated as an error: %v", err)
	}
}

// TestServiceGetAddressesByType pins the divergence from byoc: a
// managed service is named by type, because the API matches the stored
// list by type and a database carries at most one of each.
func TestServiceGetAddressesByType(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	err := runAuthed(t, rt, out, url,
		"database", "service", "get", testDatabaseID, "mcp")
	if err != nil {
		t.Fatalf("service get: %v", err)
	}
	if !strings.Contains(out.String(), "abc12345") {
		t.Errorf("output missing the service id; got:\n%s", out.String())
	}
}

func TestServiceGetUnknownTypeIsAUsageError(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	err := runAuthed(t, rt, out, url,
		"database", "service", "get", testDatabaseID, "nosuch")
	if err == nil {
		t.Fatal("an unknown service type was accepted")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
}

func TestServiceGetMissingTypeIsNotFound(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	err := runAuthed(t, rt, out, url,
		"database", "service", "get", testDatabaseID, "rag")
	if err == nil {
		t.Fatal("an undeployed service type was reported as found")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
		t.Errorf("want exit %d, got %v", ExitNotFound, err)
	}
}

// --- the preservation contract ---

// TestServiceRemovePreservesOtherTypes is the whole reason service
// writes read-modify-write. UpdateManagedDatabaseInput.Services
// REPLACES the stored list, so removing one type must send the other
// two back or they are torn down with it.
func TestServiceRemovePreservesOtherTypes(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "service", "remove",
		testDatabaseID, "mcp", "--force")
	if err != nil {
		t.Fatalf("service remove: %v", err)
	}

	got := serviceTypesIn(t, rec.Body)
	if len(got) != 2 {
		t.Fatalf("expected 2 surviving services, got %d: %v", len(got), got)
	}
	for _, want := range []string{"rag", "postgrest"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was destroyed by removing mcp; sent: %v", want, got)
		}
	}
	for _, g := range got {
		if g == "mcp" {
			t.Error("mcp survived its own removal")
		}
	}
	// A surviving service is echoed back WHOLE, including the fields
	// the API declares readOnly. Stripping them is the tempting
	// refactor — they cannot be written, so why send them — and it is
	// how an untouched service's config gets half-sent. The API takes
	// uri and ignores it (probed 2026-08-17; a body with an UNDECLARED
	// field is what earns a 400).
	if !strings.Contains(rec.Body,
		`"uri":"https://demo-db.use2.example.com/rag"`) {
		t.Errorf("the preserved rag service lost its uri; sent:\n%s",
			rec.Body)
	}
}

func TestServiceRemoveMissingTypeIsNotFound(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	err := runAuthed(t, rt, out, url, "database", "service", "remove",
		testDatabaseID, "rag", "--force")
	if err == nil {
		t.Fatal("removing an undeployed type was reported as success")
	}
}

// TestServiceApplyPreservesOtherTypes proves the same for a deploy: a
// write to one service type must carry the others along.
func TestServiceApplyPreservesOtherTypes(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes")
	if err != nil {
		t.Fatalf("mcp update: %v", err)
	}

	got := serviceTypesIn(t, rec.Body)
	if len(got) != 3 {
		t.Fatalf("expected all 3 services sent back, got %d: %v",
			len(got), got)
	}
	// Same readOnly-echo contract the remove path is held to, asserted
	// separately because this is the OTHER list builder:
	// buildServiceList, not `service remove`'s own filter. A mutation to
	// one is invisible to the other's test.
	if !strings.Contains(rec.Body,
		`"uri":"https://demo-db.use2.example.com/rag"`) {
		t.Errorf("the preserved rag service lost its uri; sent:\n%s",
			rec.Body)
	}
}

// --- MCP merge behaviour ---

// TestMCPUpdateOnlyChangesPassedFlags pins the merge. The deployed
// config must be the base, so an unrelated flag cannot silently reset
// another field.
func TestMCPUpdateOnlyChangesPassedFlags(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, mcpServiceJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--embedding-model", "text-embedding-3-large")
	if err != nil {
		t.Fatalf("mcp update: %v", err)
	}

	var payload struct {
		Services []struct {
			MCPConfig map[string]any `json:"mcp_config"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rec.Body), &payload); err != nil {
		t.Fatalf("PATCH body was not JSON: %v", err)
	}
	if len(payload.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(payload.Services))
	}
	cfg := payload.Services[0].MCPConfig

	if cfg["embedding_model"] != "text-embedding-3-large" {
		t.Errorf("embedding_model not applied: %v", cfg["embedding_model"])
	}
	// The critical one: allow_writes was true on the deployed service
	// and was NOT passed, so it must still be true. Assigning a bool
	// flag unconditionally would silently revoke write access here.
	if cfg["allow_writes"] != true {
		t.Errorf("allow_writes was reset to %v by an unrelated flag",
			cfg["allow_writes"])
	}
	// MCP secrets come back on GET and are echoed unchanged.
	if cfg["init_tokens"] != "tok-stored" {
		t.Errorf("init_tokens not preserved: %v", cfg["init_tokens"])
	}
}

// TestMCPUpdateCanDisableAllowWrites is the other half: passing
// --allow-writes=false must actually turn it off, so the Changed()
// gating is not simply ignoring the flag.
func TestMCPUpdateCanDisableAllowWrites(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, mcpServiceJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes=false")
	if err != nil {
		t.Fatalf("mcp update: %v", err)
	}
	if !strings.Contains(rec.Body, `"allow_writes":false`) {
		t.Errorf("--allow-writes=false was not applied; body: %s", rec.Body)
	}
}

// --- RAG merge behaviour ---

// TestRAGUpdatePreservesDeployedPipelines pins that an update touching
// only --top-n keeps the deployed pipelines. Rebuilding the config from
// flags would send an empty pipeline list, which the API rejects.
func TestRAGUpdatePreservesDeployedPipelines(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "rag", "update",
		testDatabaseID, "--top-n", "7")
	if err != nil {
		t.Fatalf("rag update: %v", err)
	}
	if !strings.Contains(rec.Body, `"name":"docs"`) {
		t.Errorf("deployed pipeline was dropped; body: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"top_n":7`) {
		t.Errorf("--top-n was not applied; body: %s", rec.Body)
	}
}

// TestRAGUpdateWithoutDeployedServiceExplainsItself pins the
// deploy/update guard: updating a database with no RAG service deployed
// fails client-side, naming `rag deploy` as the fix, rather than
// sending a request that can only 400.
func TestRAGUpdateWithoutDeployedServiceExplainsItself(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	err := runAuthed(t, rt, out, url, "database", "rag", "update",
		testDatabaseID, "--top-n", "5")
	if err == nil {
		t.Fatal("an unsatisfiable RAG update was accepted")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
		t.Errorf("want exit %d, got %v", ExitGeneral, err)
	}
	if !strings.Contains(err.Error(), "rag deploy") {
		t.Errorf("error does not point at rag deploy: %v", err)
	}
}

// --- PostgREST bounds ---

func TestPostgRESTRejectsOutOfRangeValues(t *testing.T) {
	cases := map[string][]string{
		"db-pool low":   {"--db-pool", "0"},
		"db-pool high":  {"--db-pool", "31"},
		"max-rows low":  {"--max-rows", "0"},
		"max-rows high": {"--max-rows", "10001"},
		"jwt too short": {"--jwt-secret", "short"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
				http.StatusOK,
				databaseJSON(testDatabaseID, threeServicesJSON)))

			args := append([]string{"database", "postgrest", "update",
				testDatabaseID}, extra...)
			if err := runAuthed(t, rt, out, url, args...); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestPostgRESTUpdatePreservesDeployedConfig(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "postgrest", "update",
		testDatabaseID, "--max-rows", "500")
	if err != nil {
		t.Fatalf("postgrest update: %v", err)
	}
	// db_schemas and db_anon_role are required and were not passed, so
	// they must have come from the deployed service.
	if !strings.Contains(rec.Body, `"db_schemas":"public"`) {
		t.Errorf("db_schemas was not preserved; body: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, `"db_anon_role":"anon"`) {
		t.Errorf("db_anon_role was not preserved; body: %s", rec.Body)
	}
}

// TestServiceWritesSendNoPlacement pins the managed/byoc divergence in
// the other direction: no command may send node placement, because the
// API discards it and a managed database has no nodes to choose.
func TestServiceWritesSendNoPlacement(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, threeServicesJSON),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "update",
		testDatabaseID, "--allow-writes")
	if err != nil {
		t.Fatalf("mcp update: %v", err)
	}
	for _, forbidden := range []string{"host_ids", "target_nodes"} {
		if strings.Contains(rec.Body, forbidden) {
			t.Errorf("%s was sent on a managed write; body: %s",
				forbidden, rec.Body)
		}
	}
}

// TestNoServiceCommandOffersTargetNodes is the flag-level counterpart:
// the flag must not exist at all, so a script cannot pass something
// that would be silently discarded.
func TestNoServiceCommandOffersTargetNodes(t *testing.T) {
	for _, args := range [][]string{
		{"database", "mcp", "deploy"},
		{"database", "mcp", "update"},
		{"database", "rag", "deploy"},
		{"database", "rag", "update"},
		{"database", "postgrest", "deploy"},
		{"database", "postgrest", "update"},
	} {
		name := strings.Join(args, " ")
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			full := append(append([]string{}, args...),
				testDatabaseID, "--target-nodes", "n1")
			err := runManaged(t, rt, out, full...)
			if err == nil {
				t.Fatal("--target-nodes was accepted on a managed command")
			}
			if !strings.Contains(err.Error(), "target-nodes") {
				t.Errorf("failure was not about the flag: %v", err)
			}
		})
	}
}

// TestServiceWriteCarriesTheExistingServiceID pins the field that makes
// an update an update.
//
// The API splits the incoming service list and validates the halves
// differently: a service with an empty or unknown ServiceID is NEW and
// must supply its API keys; one naming an existing ID is a
// reconfiguration and may omit them. RAG and PostgREST secrets are
// never returned by GET, so without the ID the CLI has nothing to
// re-send and the write is rejected — `rag update --top-n 5` answered
// 400 "api_key is required for provider openai" against the live API
// until the ID was carried.
//
// A stub answering 200 is indifferent to the field, so only an explicit
// assertion keeps this from regressing.
func TestServiceWriteCarriesTheExistingServiceID(t *testing.T) {
	cases := map[string]struct {
		args    []string
		svcType string
		wantID  string
	}{
		"mcp update":       {[]string{"database", "mcp", "update", testDatabaseID, "--allow-writes"}, "mcp", "mcp00001"},
		"rag update":       {[]string{"database", "rag", "update", testDatabaseID, "--top-n", "5"}, "rag", "rag00001"},
		"postgrest update": {[]string{"database", "postgrest", "update", testDatabaseID, "--max-rows", "500"}, "postgrest", "pgr00001"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t, stubGetThenWrite(
				rec, databaseJSON(testDatabaseID, threeServicesJSON),
				http.StatusOK, databaseJSON(testDatabaseID, "")))

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("%s: %v", name, err)
			}

			var payload struct {
				Services []struct {
					ServiceType string  `json:"service_type"`
					ServiceID   *string `json:"service_id"`
				} `json:"services"`
			}
			if err := json.Unmarshal([]byte(rec.Body), &payload); err != nil {
				t.Fatalf("body was not JSON: %v", err)
			}
			var found bool
			for _, s := range payload.Services {
				if s.ServiceType != tc.svcType {
					continue
				}
				found = true
				if s.ServiceID == nil {
					t.Fatalf("%s sent no service_id; the API will treat this "+
						"as a NEW service and demand API keys", tc.svcType)
				}
				if *s.ServiceID != tc.wantID {
					t.Errorf("service_id = %q, want %q (the deployed one)",
						*s.ServiceID, tc.wantID)
				}
			}
			if !found {
				t.Fatalf("no %s service in the request body", tc.svcType)
			}
		})
	}
}

// TestServiceDeployOnEmptyDatabaseSendsNoServiceID is the negative
// control: a FIRST deploy has no ID to adopt, and must not invent one —
// the API assigns it, and a fabricated ID would be classified as
// unknown and rejected anyway.
func TestServiceDeployOnEmptyDatabaseSendsNoServiceID(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""),
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "mcp", "deploy",
		testDatabaseID, "--allow-writes")
	if err != nil {
		t.Fatalf("mcp deploy: %v", err)
	}
	if strings.Contains(rec.Body, `"service_id"`) {
		t.Errorf("a first deploy invented a service_id; body: %s", rec.Body)
	}
}
