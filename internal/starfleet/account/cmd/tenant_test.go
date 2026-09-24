package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testTenantID = "aaaabbbb-cccc-dddd-eeee-ffff00001111"

// Every optional field present.
const tenantFullBody = `{"id":"` + testTenantID + `",` +
	`"name":"acme","plan":"scale","plan_trial":true,` +
	`"domain":"acme.example","external_id":"ext-1",` +
	`"plan_expires_at":"2027-01-01T00:00:00Z",` +
	`"created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

// Every optional field absent — the nil-pointer path.
const tenantMinimalBody = `{"id":"` + testTenantID + `",` +
	`"name":"acme","created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

func TestTenantListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotPath string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testsupport.JSONHandler(200, `[`+tenantFullBody+`]`)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err != nil {
			t.Fatalf("tenant list: %v", err)
		}
		for _, want := range []string{"acme", "scale", "yes"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output %q missing %q", out.String(), want)
			}
		}
		if gotPath != "/account/v1/tenants" {
			t.Errorf("path = %q, want /account/v1/tenants", gotPath)
		}
	})

	// Every optional field is a pointer; absent must render blank,
	// not panic.
	t.Run("nil optional fields render blank", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+tenantMinimalBody+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err != nil {
			t.Fatalf("tenant list minimal: %v", err)
		}
		if !strings.Contains(out.String(), "acme") {
			t.Errorf("output %q missing the name", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+tenantFullBody+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err != nil {
			t.Fatalf("tenant list json: %v", err)
		}
		if !strings.Contains(out.String(), testTenantID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("empty list notices on stderr", func(t *testing.T) {
		rt, out, errBuf := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err != nil {
			t.Fatalf("tenant list empty: %v", err)
		}
		if !strings.Contains(errBuf.String(), "No tenants found") {
			t.Errorf("stderr = %q, want the empty notice",
				errBuf.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// TestTenantGetRun is modelled on TestClientGetRun in
// apiclient_test.go.
func TestTenantGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotPath string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testsupport.JSONHandler(200, tenantFullBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get: %v", err)
		}
		for _, want := range []string{"acme", "scale", "yes"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output %q missing %q", out.String(), want)
			}
		}
		if gotPath != "/account/v1/tenants/"+testTenantID {
			t.Errorf("path = %q, want /account/v1/tenants/%s", gotPath,
				testTenantID)
		}
	})

	// The nil-optional-fields path through get is a different code
	// path than list's (single-object JSON200, not a slice), so it
	// needs its own case rather than relying on list's coverage.
	t.Run("nil optional fields render blank", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantMinimalBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get minimal: %v", err)
		}
		if !strings.Contains(out.String(), "acme") {
			t.Errorf("output %q missing the name", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantFullBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get json: %v", err)
		}
		if !strings.Contains(out.String(), testTenantID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	// Must fail before any HTTP call is made.
	t.Run("invalid id fails before any request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/tenants") {
				called = true
			}
			testsupport.JSONHandler(200, tenantFullBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", "not-a-uuid"); err == nil {
			t.Fatal("expected an error for a non-UUID id")
		}
		if called {
			t.Error("made an HTTP call despite an invalid id")
		}
	})

	t.Run("nil body", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(202, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get nil body: %v", err)
		}
		if !strings.Contains(errb.String(), "No tenant data returned") {
			t.Errorf("missing nil-body message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

func TestTenantUpdateRun(t *testing.T) {
	t.Run("sends only the changed field", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotBody, gotMethod string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/tenants/") {
				b, _ := io.ReadAll(r.Body)
				gotBody, gotMethod = string(b), r.Method
			}
			testsupport.JSONHandler(200, tenantFullBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "update", testTenantID,
			"--name", "acme2"); err != nil {
			t.Fatalf("tenant update: %v", err)
		}
		if gotMethod != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", gotMethod)
		}
		if !strings.Contains(gotBody, `"name":"acme2"`) {
			t.Errorf("body = %q, want the new name", gotBody)
		}
	})

	// UpdateTenantInput has exactly one field, so no flags means
	// there is nothing to send — fail before the request.
	t.Run("no flags is a usage error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/tenants/") {
				called = true
			}
			testsupport.JSONHandler(200, tenantFullBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "update", testTenantID); err == nil {
			t.Fatal("expected an error with no flags")
		}
		if called {
			t.Error("sent a PATCH with nothing to change")
		}
	})

	t.Run("invalid id fails before any request", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		called := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/account/v1/tenants") {
				called = true
			}
			testsupport.JSONHandler(200, tenantFullBody)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "update", "not-a-uuid",
			"--name", "x"); err == nil {
			t.Fatal("expected an error for a non-UUID id")
		}
		if called {
			t.Error("made an HTTP call despite an invalid id")
		}
	})

	t.Run("nil body", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(202, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "update", testTenantID,
			"--name", "x"); err != nil {
			t.Fatalf("tenant update nil body: %v", err)
		}
		if !strings.Contains(errb.String(), "No tenant data returned") {
			t.Errorf("missing nil-body message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "update", testTenantID,
			"--name", "x"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// TestTenantRendering pins the format-specific claims llms-full.txt
// and SKILL.md make about Tenant's five optional pointer fields, the
// same class of gate TestClientYAMLKeySpelling and
// TestClientStripPreservesNilShape provide for ApiClient. Nothing
// gated any of these claims before this test: a mutant that changed
// tenantRowFrom's nil branch from `trial := ""` to `trial := "no"`
// passed the whole suite, including TestTenantListRun's and
// TestTenantGetRun's "nil optional fields render blank" subtests,
// which assert only that "acme" appears in the output — never that
// anything is actually blank. Every subtest below is run through the
// command tree, in the format it claims, against both fixture bodies.
func TestTenantRendering(t *testing.T) {
	t.Run("text: minimal body leaves TRIAL blank, not \"no\"", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantMinimalBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get minimal -o text: %v", err)
		}
		// Nothing else in this fixture (the UUID, "acme", the date)
		// contains "yes" or "no" as a substring, so either one
		// appearing can only be the TRIAL cell rendering a
		// plan_trial that was never set.
		if strings.Contains(out.String(), "yes") ||
			strings.Contains(out.String(), "no") {
			t.Errorf("stdout = %q, an absent plan_trial must render "+
				"TRIAL as a blank cell, never the literal word "+
				"\"yes\" or \"no\"", out.String())
		}
	})

	t.Run("text: full body renders TRIAL as yes", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantFullBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get full -o text: %v", err)
		}
		if !strings.Contains(out.String(), "yes") {
			t.Errorf("stdout = %q, want \"yes\" for plan_trial=true",
				out.String())
		}
	})

	t.Run("json: minimal body omits every optional key", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantMinimalBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get minimal -o json: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON (%v): %q", err, out.String())
		}
		for _, key := range []string{
			"domain", "external_id", "plan", "plan_expires_at",
			"plan_trial",
		} {
			if v, ok := got[key]; ok {
				t.Errorf("json key %q = %v, want omitted for an "+
					"absent optional field, not present (even as "+
					"null)", key, v)
			}
		}
	})

	t.Run("json: full body carries every optional key", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantFullBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get full -o json: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON (%v): %q", err, out.String())
		}
		for _, key := range []string{
			"domain", "external_id", "plan", "plan_expires_at",
			"plan_trial",
		} {
			if _, ok := got[key]; !ok {
				t.Errorf("json key %q missing, want present for a "+
					"populated optional field", key)
			}
		}
	})

	t.Run("yaml: minimal body omits absent optional keys, as json does",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, tenantMinimalBody))
			if err := runAuthedAccount(t, rt, out, url,
				"tenant", "get", testTenantID); err != nil {
				t.Fatalf("tenant get minimal -o yaml: %v", err)
			}
			// yaml is json in another encoding, so an omitempty field
			// that is absent from one is absent from the other. There
			// is no longer a second regime in which yaml alone shows
			// every key as null.
			for _, key := range []string{
				"domain", "external_id", "plan_expires_at", "plan_trial",
			} {
				if strings.Contains(out.String(), key+":") {
					t.Errorf("yaml carried absent optional key %q: %q",
						key, out.String())
				}
			}
			// The keys that are always populated must still be there,
			// or this test would pass on empty output.
			for _, key := range []string{"id", "name", "created_at"} {
				if !strings.Contains(out.String(), key+":") {
					t.Errorf("yaml output missing required key %q: %q",
						key, out.String())
				}
			}
		})

	t.Run("yaml: full body carries every optional key populated",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, tenantFullBody))
			if err := runAuthedAccount(t, rt, out, url,
				"tenant", "get", testTenantID); err != nil {
				t.Fatalf("tenant get full -o yaml: %v", err)
			}
			for _, want := range []string{
				"domain: acme.example",
				"external_id: ext-1",
				"plan: scale",
				"plan_trial: true",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("yaml output missing %q: %q", want,
						out.String())
				}
			}
			// The lowercased Go field names must not appear.
			for _, stale := range []string{
				"externalid", "planexpiresat", "plantrial",
			} {
				if strings.Contains(out.String(), stale) {
					t.Errorf("yaml used the Go field name %q: %q",
						stale, out.String())
				}
			}
			if !strings.Contains(out.String(), "plan_expires_at:") ||
				!strings.Contains(out.String(), "2027-01-01") {
				t.Errorf("yaml missing a populated plan_expires_at: %q",
					out.String())
			}
		})

	// The docs claim list returns a bare array and get returns a bare
	// object, neither wrapped in an envelope. Unmarshalling into the
	// exact documented shape is what catches a wrapper: []map[string]any
	// fails to unmarshal a `{"tenants": [...]}` wrapper outright, and a
	// wrapped get response leaves the top-level "id" key absent.
	t.Run("json: list is a bare array, not wrapped", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `[`+tenantFullBody+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "list"); err != nil {
			t.Fatalf("tenant list -o json: %v", err)
		}
		var got []map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not a bare JSON array (%v): %q", err,
				out.String())
		}
		if len(got) != 1 {
			t.Fatalf("list returned %d elements, want 1: %q",
				len(got), out.String())
		}
		if got[0]["id"] != testTenantID {
			t.Errorf("element id = %v, want %s", got[0]["id"],
				testTenantID)
		}
	})

	t.Run("json: get is a bare object, not wrapped", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantFullBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get -o json: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("stdout is not JSON (%v): %q", err, out.String())
		}
		// A wrapper (e.g. {"tenant": {...}}) leaves these top-level
		// keys absent even though the same bytes are present deeper
		// in the document.
		if got["id"] != testTenantID {
			t.Errorf("top-level id = %v, want %s — get must not "+
				"nest the tenant under an envelope key", got["id"],
				testTenantID)
		}
		if _, ok := got["name"]; !ok {
			t.Errorf("top-level name key missing: %q", out.String())
		}
	})

	// The docs list table columns as ID, NAME, PLAN, TRIAL, DOMAIN,
	// CREATED and say there is no column for external_id or
	// plan_expires_at. Every other table assertion in this file is a
	// Contains check, which cannot catch an extra column; this pins
	// the exclusion directly by looking for values that exist only on
	// the two undocumented fields.
	t.Run("text: no column for external_id or plan_expires_at", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tenantFullBody))
		if err := runAuthedAccount(t, rt, out, url,
			"tenant", "get", testTenantID); err != nil {
			t.Fatalf("tenant get -o text: %v", err)
		}
		for _, absent := range []string{"ext-1", "2027-01-01"} {
			if strings.Contains(out.String(), absent) {
				t.Errorf("stdout = %q, table output must not carry "+
					"external_id (%q) or plan_expires_at — neither "+
					"is a documented column", out.String(), absent)
			}
		}
	})
}
