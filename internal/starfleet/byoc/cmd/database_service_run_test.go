package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// dbWithServiceBody is a database carrying one MCP service, used by the
// service, mcp, and rag command tests that read-modify-write services.
const dbWithServiceBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
	`"services":[{"service_id":"svc-1","service_type":"mcp",` +
	`"state":"available","port":8080,"public_domain":"mcp.example.com"}]}`

// dbNoServiceBody is a database with no services deployed at all, for
// tests that must exercise a genuine first `deploy`: the
// deploy/update guard refuses `deploy` outright when a service of that
// type is already present, so a deploy test can no longer reuse a
// fixture that happens to carry one.
const dbNoServiceBody = `{"id":"` + testDatabaseID + `","name":"mydb",` +
	`"status":"available","pg_version":"16","cluster_id":"` +
	testClusterID + `","created_at":"2024-03-15T10:30:00Z","services":[]}`

func TestDatabaseServiceListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, dbWithServiceBody))
		if err := runAuthed(t, rt, out, url,
			"database", "service", "list", testDatabaseID); err != nil {
			t.Fatalf("service list: %v", err)
		}
		if !strings.Contains(out.String(), "svc-1") {
			t.Errorf("missing service id: %q", out.String())
		}
		if !strings.Contains(out.String(), "https://mcp.example.com") {
			t.Errorf("missing public endpoint: %q", out.String())
		}
		if strings.Contains(out.String(), "8080") {
			t.Errorf("internal port leaked into the table: %q", out.String())
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, databaseBody))
		if err := runAuthed(t, rt, out, url,
			"database", "service", "list", testDatabaseID); err != nil {
			t.Fatalf("service list json: %v", err)
		}
	})

	// TestDatabaseServiceListRun/"json keeps raw fields" pins the
	// machine-output contract: dropping PORT/DOMAIN from the table
	// must not touch -o json, which renders the API object as-is.
	t.Run("json keeps raw fields", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, dbWithServiceBody))
		if err := runAuthed(t, rt, out, url,
			"database", "service", "list", testDatabaseID); err != nil {
			t.Fatalf("service list json: %v", err)
		}
		for _, want := range []string{`"port":8080`, `"public_domain":"mcp.example.com"`} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("json output missing %q; got:\n%s", want, out.String())
			}
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"database", "service", "list", testDatabaseID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestDatabaseServiceGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, dbWithServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "service",
			"get", testDatabaseID, "svc-1"); err != nil {
			t.Fatalf("service get: %v", err)
		}
		if !strings.Contains(out.String(), "svc-1") {
			t.Errorf("missing service id: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, dbWithServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "service",
			"get", testDatabaseID, "svc-1"); err != nil {
			t.Fatalf("service get json: %v", err)
		}
	})

	t.Run("missing service", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, dbWithServiceBody))
		if err := runAuthed(t, rt, out, url, "database", "service",
			"get", testDatabaseID, "nope"); err == nil {
			t.Fatal("expected error for unknown service")
		}
	})
}

func TestDatabaseServiceRemoveRun(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			testsupport.JSONHandler(200, dbWithServiceBody)(w, r)
			return
		}
		testsupport.JSONHandler(200, `{}`)(w, r)
	}

	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, handler)
		if err := runAuthed(t, rt, out, url, "database", "service",
			"remove", testDatabaseID, "mcp", "--force"); err != nil {
			t.Fatalf("service remove: %v", err)
		}
		if !strings.Contains(errb.String(), "removal requested") {
			t.Errorf("missing removal message: %q", errb.String())
		}
	})

	t.Run("invalid database id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, handler)
		if err := runAuthed(t, rt, out, url, "database", "service",
			"remove", "not-a-uuid", "mcp", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, handler)
		if err := runAuthed(t, rt, out, url, "database", "service",
			"remove", testDatabaseID, "mcp"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

// TestSvcToConfigPreservesPostgREST guards a read-modify-write hole. A
// service of another type, preserved while one service is applied, must
// keep its configuration: dropping it sends postgrest_config: null for
// a service the caller never asked to change.
func TestSvcToConfigPreservesPostgREST(t *testing.T) {
	svc := api.Service{
		ServiceId:   "svc-1",
		ServiceType: api.ServiceServiceTypePostgrest,
		PostgrestConfig: &api.PostgRESTServiceConfig{
			DbSchemas:  "public",
			DbAnonRole: "web_anon",
		},
		HostId: nullable.NewNullableWithValue("host-1"),
	}

	cfg := svcToConfig(svc)
	if cfg.PostgrestConfig == nil {
		t.Fatal("PostgrestConfig dropped by svcToConfig")
	}
	if cfg.PostgrestConfig.DbSchemas != "public" {
		t.Errorf("DbSchemas = %q, want \"public\"",
			cfg.PostgrestConfig.DbSchemas)
	}
	if cfg.PostgrestConfig.DbAnonRole != "web_anon" {
		t.Errorf("DbAnonRole = %q, want \"web_anon\"",
			cfg.PostgrestConfig.DbAnonRole)
	}
}

// remove builds its write from the list without the named type, so a
// type that is not deployed would resend the list unchanged and report
// a removal.
func TestServiceRemoveUndeployedTypeSendsNothing(t *testing.T) {
	rec := &capturedApply{}
	url := testsupport.NewAuthedServer(t, rec.handler(postgrestOnlyDBBody))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "service", "remove",
		testDatabaseID, "mcp", "--force")
	requireExitOne(t, err)
	if !strings.Contains(err.Error(), "service list") {
		t.Errorf("error does not name service list: %v", err)
	}
	if rec.callCount() != 0 {
		t.Errorf("calls = %d, want no write", rec.callCount())
	}
}

// The write replaces the whole service list, so it must be built from a
// read taken after the prompt. A service deployed between the first read
// and the write must survive the removal.
func TestServiceRemoveRereadsAfterPrompt(t *testing.T) {
	later := `{"id":"` + testDatabaseID + `","name":"mydb",` +
		`"status":"available","pg_version":"16","cluster_id":"` +
		testClusterID + `","created_at":"2024-03-15T10:30:00Z",` +
		`"services":[{"service_id":"svc-1","service_type":"mcp",` +
		`"state":"available"},{"service_id":"svc-2",` +
		`"service_type":"postgrest","state":"available",` +
		`"postgrest_config":{"db_schemas":"public",` +
		`"db_anon_role":"web_anon"}}]}`
	gets := 0
	rec := &capturedApply{}
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets++
				body := dbWithServiceBody
				if gets > 1 {
					body = later
				}
				testsupport.JSONHandler(200, body)(w, r)
				return
			}
			rec.handler(later)(w, r)
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "service", "remove",
		testDatabaseID, "mcp", "--force"); err != nil {
		t.Fatalf("service remove: %v", err)
	}
	if rec.callCount() != 1 {
		t.Fatalf("writes = %d, want 1", rec.callCount())
	}
	if !strings.Contains(string(rec.body), `"postgrest"`) {
		t.Errorf("write dropped the service deployed after the first "+
			"read: %s", rec.body)
	}
	if strings.Contains(string(rec.body), `"mcp"`) {
		t.Errorf("write kept the removed service: %s", rec.body)
	}
}
