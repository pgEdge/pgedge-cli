package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// testsupport.RunAuthed supplies --client-id id, so a stub client
// record carrying auth0_id "id" is the caller's own record and any
// other value is somebody else's.
const whoamiOwnClient = `{"id":"7c9e6679-7425-40de-944b-e07fc1f90ae7",` +
	`"name":"ci-runner","description":"CI integration tests",` +
	`"auth0_id":"id","created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

const whoamiOtherClient = `{"id":"11111111-1111-1111-1111-111111111111",` +
	`"name":"somebody-else","description":"not ours",` +
	`"auth0_id":"other","created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

const whoamiTenant = `{"id":"3f2a9c1e-5b7d-4e8a-9c6f-2d1b0a7e4f53",` +
	`"name":"acme","plan":"enterprise","plan_trial":false,` +
	`"created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

const whoamiTrialTenant = `{"id":"3f2a9c1e-5b7d-4e8a-9c6f-2d1b0a7e4f53",` +
	`"name":"acme","plan":"enterprise","plan_trial":true,` +
	`"created_at":"2026-07-01T00:00:00Z",` +
	`"updated_at":"2026-07-02T00:00:00Z"}`

// whoamiStub routes the two reads whoami makes. Every other path is a
// 404, so a command that called a third endpoint fails loudly rather
// than reading one of these two bodies by accident.
func whoamiStub(clients, tenants string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/account/v1/clients":
			testsupport.JSONHandler(200, clients)(w, r)
		case "/account/v1/tenants":
			testsupport.JSONHandler(200, tenants)(w, r)
		default:
			testsupport.JSONHandler(404,
				`{"code":404,"message":"no such path"}`)(w, r)
		}
	}
}

func TestAuthWhoamiRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOtherClient+`,`+whoamiOwnClient+`]`,
			`[`+whoamiTenant+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"Client ID:     id",
			"ci-runner",
			"CI integration tests",
			"7c9e6679-7425-40de-944b-e07fc1f90ae7",
			"acme",
			"3f2a9c1e-5b7d-4e8a-9c6f-2d1b0a7e4f53",
			"enterprise",
			url,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output %q missing %q", got, want)
			}
		}
		// The other tenant's client must not be reported as ours.
		if strings.Contains(got, "somebody-else") {
			t.Errorf("output names another client's record: %q", got)
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOwnClient+`]`, `[`+whoamiTenant+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami json: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		want := map[string]any{
			"client_id":          "id",
			"client_name":        "ci-runner",
			"client_record_id":   "7c9e6679-7425-40de-944b-e07fc1f90ae7",
			"client_description": "CI integration tests",
			"tenant_id":          "3f2a9c1e-5b7d-4e8a-9c6f-2d1b0a7e4f53",
			"tenant_name":        "acme",
			"plan":               "enterprise",
			"api_url":            url,
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %v, want %v", k, got[k], v)
			}
		}
		// Both are omitempty and both are at their zero value here, so
		// each key is absent rather than present-and-zero. tenant_count
		// is asserted here because the reference promises it "carries a
		// number only above one": widen the threshold to `> 0` and it
		// appears on every single-tenant call, which nothing else in
		// this file would notice.
		for _, key := range []string{"plan_trial", "tenant_count"} {
			if _, ok := got[key]; ok {
				t.Errorf("%s should be omitted at its zero value: %q",
					key, out.String())
			}
		}
	})

	t.Run("yaml keys match json keys", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOwnClient+`]`, `[`+whoamiTenant+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami yaml: %v", err)
		}
		for _, want := range []string{
			"client_id:", "client_name:", "client_record_id:",
			"client_description:",
			"tenant_id:", "tenant_name:", "plan:", "api_url:",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("yaml %q missing key %q", out.String(), want)
			}
		}
	})

	t.Run("trial plan is named", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOwnClient+`]`, `[`+whoamiTrialTenant+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami trial: %v", err)
		}
		if !strings.Contains(out.String(), "trial") {
			t.Errorf("output should name the trial: %q", out.String())
		}
	})

	// The credential authenticated, so the API's principal check already
	// found a live client for the tenant — but the list is a separate
	// read, and whoami must report the identity it DOES have rather
	// than fail or invent a name.
	t.Run("own client absent from the list", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOtherClient+`]`, `[`+whoamiTenant+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami unmatched: %v", err)
		}
		if !strings.Contains(out.String(), "acme") {
			t.Errorf("tenant should still be reported: %q", out.String())
		}
		if strings.Contains(out.String(), "somebody-else") {
			t.Errorf("another client's name was reported: %q",
				out.String())
		}
		if !strings.Contains(errb.String(), "No API client") {
			t.Errorf("missing unmatched notice: %q", errb.String())
		}
	})

	t.Run("no tenant returned", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOwnClient+`]`, `[]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami no tenant: %v", err)
		}
		if !strings.Contains(out.String(), "ci-runner") {
			t.Errorf("client should still be reported: %q", out.String())
		}
		if !strings.Contains(errb.String(), "No tenant") {
			t.Errorf("missing no-tenant notice: %q", errb.String())
		}
	})

	// More than one tenant cannot happen for a client credential
	// today, so the count is reported rather than silently dropped.
	t.Run("more than one tenant reports the count", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		second := strings.Replace(whoamiTenant, `"acme"`, `"other"`, 1)
		url := testsupport.NewAuthedServer(t, whoamiStub(
			`[`+whoamiOwnClient+`]`, `[`+whoamiTenant+`,`+second+`]`))
		if err := runAuthedAccount(t, rt, out, url,
			"auth", "whoami"); err != nil {
			t.Fatalf("auth whoami two tenants: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		if got["tenant_count"] != float64(2) {
			t.Errorf("tenant_count = %v, want 2: %q",
				got["tenant_count"], out.String())
		}
		// WHICH tenant is reported, not merely how many. The count
		// alone leaves the index unobserved, so reading the last
		// element instead of the first would pass.
		if got["tenant_name"] != "acme" {
			t.Errorf("tenant_name = %v, want the FIRST tenant "+
				"\"acme\": %q", got["tenant_name"], out.String())
		}
	})

	t.Run("clients read fails", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/account/v1/clients" {
				testsupport.JSONHandler(500, `boom`)(w, r)
				return
			}
			testsupport.JSONHandler(200, `[`+whoamiTenant+`]`)(w, r)
		})
		err := runAuthedAccount(t, rt, out, url, "auth", "whoami")
		if err == nil {
			t.Fatal("expected an error when the clients read fails")
		}
		requireNotUnknownCommand(t, err)
	})

	t.Run("tenants read fails", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/account/v1/tenants" {
				testsupport.JSONHandler(500, `boom`)(w, r)
				return
			}
			testsupport.JSONHandler(200,
				`[`+whoamiOwnClient+`]`)(w, r)
		})
		err := runAuthedAccount(t, rt, out, url, "auth", "whoami")
		if err == nil {
			t.Fatal("expected an error when the tenants read fails")
		}
		requireNotUnknownCommand(t, err)
	})

	t.Run("transport error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.BrokenHandler())
		err := runAuthedAccount(t, rt, out, url, "auth", "whoami")
		if err == nil {
			t.Fatal("expected an error on a transport failure")
		}
		requireNotUnknownCommand(t, err)
	})
}

// TestAuthWhoamiClassifiesAHalfSuppliedFlagPair pins the exit code the
// reference and the skill both promise for a half pair. It is the third
// site of that distinction: conn.Resolve draws it, `auth status` draws
// it in TestAuthStatusNamesAHalfSuppliedFlagPair, and whoami draws it
// in its own branch. That branch resolves credentials before building
// the client precisely so it is reachable, and a reachable branch with
// no test is how ExitAuth got mistaken for ExitUsage once already (see
// the sibling test's comment).
//
// No server: the refusal happens before any request, so runAccount is
// enough, and the profile holds a COMPLETE pair that the incomplete
// flags override — which is what makes exit 2 the right answer rather
// than exit 5.
func TestAuthWhoamiClassifiesAHalfSuppliedFlagPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"id without secret", []string{"--client-id", "only-an-id"}},
		{"secret without id",
			[]string{"--client-secret", "only-a-secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{
					ClientID: "cfg-id", ClientSecret: "cfg-secret"})

			args := append([]string{"auth", "whoami"}, tc.args...)
			err := runAccount(t, rt, out, args...)
			var ee *conn.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v (%T), want *conn.ExitError", err, err)
			}
			if ee.Code() != conn.ExitUsage {
				t.Errorf("exit code = %d, want %d (usage)",
					ee.Code(), conn.ExitUsage)
			}
			// Neither credential value may be echoed back.
			for _, secret := range []string{
				"cfg-secret", "only-a-secret",
			} {
				if strings.Contains(out.String(), secret) {
					t.Errorf("whoami echoed a secret value:\n%s",
						out.String())
				}
			}
		})
	}
}

// A credential absent altogether is a different failure from a
// malformed command, and whoami must not collapse the two.
func TestAuthWhoamiWithNoCredentialsExitsAuth(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAccount(t, rt, out, "auth", "whoami")
	var ee *conn.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v (%T), want *conn.ExitError", err, err)
	}
	if ee.Code() != conn.ExitAuth {
		t.Errorf("exit code = %d, want %d (auth)",
			ee.Code(), conn.ExitAuth)
	}
}

// requireNotUnknownCommand keeps the three failure-path subtests
// honest. Each asserts only that an error came back, and cobra returns
// one for an unregistered command too, so without this the subtests
// pass on a routing failure while exercising nothing.
func requireNotUnknownCommand(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error is cobra's routing failure, not the "+
			"command's own: %v", err)
	}
}
