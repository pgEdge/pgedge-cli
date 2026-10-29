package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestDoctorVerdict(t *testing.T) {
	ok := authInfo{Authenticated: true, Source: "flags"}
	up := apiInfo{Reachable: true}
	cases := []struct {
		name     string
		report   accountDoctorReport
		verdict  string
		wantCode int
	}{
		{"ready", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{Resolved: true, Plan: "managed"}},
			verdictReady, ExitOK},
		{"no credentials", accountDoctorReport{API: up},
			verdictNoCredentials, ExitAuth},
		{"unreadable keychain", accountDoctorReport{API: up,
			Auth: authInfo{Problem: "keychain", problemCode: ExitAuth}},
			verdictNoCredentials, ExitAuth},
		{"half pair", accountDoctorReport{API: up,
			Auth: authInfo{Problem: "half", problemCode: ExitUsage}},
			verdictCredsMisconfigured, ExitUsage},
		{"session expired", accountDoctorReport{API: up,
			Auth: authInfo{Source: auth.SourceBrowser, Problem: "expired",
				problemCode: ExitAuth}},
			verdictSessionNeedsLogin, ExitAuth},
		{"session renews", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{Skipped: "renews"}},
			verdictSessionUnverified, ExitAuth},
		{"unreachable", accountDoctorReport{Auth: ok,
			Tenant: tenantInfo{err: conn.CheckResponse(401, "")}},
			verdictEndpointUnreachable, ExitGeneral},
		{"rejected", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{err: conn.CheckResponse(401, "")}},
			verdictCredentialRejected, ExitAuth},
		// Exit 5 alone, as a failed exchange carries, is not a refusal.
		{"auth code without refusal", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{err: newExitError("mint", ExitAuth)}},
			verdictTenantUnresolved, ExitGeneral},
		{"server error", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{err: newExitError("500", ExitGeneral)}},
			verdictTenantUnresolved, ExitGeneral},
		{"plain error", accountDoctorReport{Auth: ok, API: up,
			Tenant: tenantInfo{err: errors.New("eof")}},
			verdictTenantUnresolved, ExitGeneral},
		{"no tenants", accountDoctorReport{Auth: ok, API: up},
			verdictTenantUnresolved, ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, code := doctorVerdict(tc.report)
			if v != tc.verdict || code != tc.wantCode {
				t.Errorf("got (%s, %d), want (%s, %d)",
					v, code, tc.verdict, tc.wantCode)
			}
		})
	}
}

// decodeFirst reads the report and ignores cobra's error and usage
// text, which the test root writes to the same buffer.
func decodeFirst(b []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(b)).Decode(v)
}

const verdictTenantsBody = `[{"id":"t-1","name":"acme","plan":"managed"}]`

func TestDoctorStrictExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		strict   bool
		verdict  string
		wantCode int
	}{
		{"ready strict", 200, verdictTenantsBody, true, verdictReady, ExitOK},
		{"rejected strict", 401, `{"code":401,"message":"no"}`, true,
			verdictCredentialRejected, ExitAuth},
		{"rejected default", 401, `{"code":401,"message":"no"}`, false,
			verdictCredentialRejected, ExitOK},
		{"server error strict", 500, `{"code":500,"message":"x"}`, true,
			verdictTenantUnresolved, ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/account/v1/tenants" {
						w.WriteHeader(http.StatusOK)
						return
					}
					testsupport.JSONHandler(tc.status, tc.body)(w, r)
				})
			args := []string{"doctor"}
			if tc.strict {
				args = append(args, "--strict")
			}
			err := runAuthedAccount(t, rt, out, url, args...)
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Errorf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
			var report struct {
				Verdict string `json:"verdict"`
			}
			if jerr := decodeFirst(out.Bytes(), &report); jerr != nil {
				t.Fatalf("decode %q: %v", out.String(), jerr)
			}
			if report.Verdict != tc.verdict {
				t.Errorf("verdict = %q, want %q", report.Verdict, tc.verdict)
			}
		})
	}
}

func TestDoctorStrictUnreachableAndNoCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthedAccount(t, rt, out, unreachableAddr(t),
		"doctor", "--strict")
	if got := cli.ExitCode(err); got != ExitGeneral {
		t.Errorf("unreachable: exit = %d (%v), want 1", got, err)
	}
	if !strings.Contains(out.String(), verdictEndpointUnreachable) {
		t.Errorf("text output lacks the verdict line:\n%s", out.String())
	}

	rt, out, _ = testsupport.NewRuntime(t, "", "json")
	err = runAccount(t, rt, out, "doctor", "--strict",
		"--api-url", unreachableAddr(t))
	if got := cli.ExitCode(err); got != ExitAuth {
		t.Errorf("no credentials: exit = %d (%v), want 5", got, err)
	}
}

func TestAuthStatusVerify(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		verify     bool
		wantCode   int
		wantVerify string // "" means the key must be absent
	}{
		{"accepted", 200, verdictTenantsBody, true, ExitOK, "true"},
		{"rejected", 401, `{"code":401,"message":"no"}`, true, ExitAuth,
			"false"},
		{"server error keeps its code", 500, `{"code":500,"message":"x"}`,
			true, ExitGeneral, "false"},
		{"without verify nothing is read", 401,
			`{"code":401,"message":"no"}`, false, ExitOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			reads := 0
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					reads++
					testsupport.JSONHandler(tc.status, tc.body)(w, r)
				})
			args := []string{"auth", "status"}
			if tc.verify {
				args = append(args, "--verify")
			}
			err := runAuthedAccount(t, rt, out, url, args...)
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Errorf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
			var report map[string]any
			if jerr := decodeFirst(out.Bytes(), &report); jerr != nil {
				t.Fatalf("decode %q: %v", out.String(), jerr)
			}
			v, present := report["verified"]
			switch {
			case tc.wantVerify == "" && (present || reads != 0):
				t.Errorf("verified=%v present=%v reads=%d without "+
					"--verify", v, present, reads)
			case tc.wantVerify != "" && (!present ||
				v != (tc.wantVerify == "true")):
				t.Errorf("verified = %v (present %v), want %s",
					v, present, tc.wantVerify)
			}
		})
	}
}

const inviteAliceBody = `{"id":"i-1","email":"Alice@Example.com",` +
	`"team_name":"t","created_at":"2024-03-15T10:30:00Z",` +
	`"expires_at":"2024-04-15T10:30:00Z","updated_at":"2024-03-15T10:30:00Z"}`

func TestLookupByEmail(t *testing.T) {
	members := `[` + membershipBody + `,` + membershipBodyNotOwner + `]`
	cases := []struct {
		name     string
		args     []string
		status   int
		body     string
		wantCode int
		want     string
		notWant  string
	}{
		{"member match ignores case",
			[]string{"membership", "list", "--email", "ALICE@example.COM"},
			200, members, ExitOK, "alice@example.com", "bob@example.com"},
		{"member no match", []string{"membership", "list", "--email",
			"carol@example.com"}, 200, members, ExitNotFound, "", ""},
		{"member failed read is not absence", []string{"membership",
			"list", "--email", "alice@example.com"}, 500,
			`{"code":500,"message":"x"}`, ExitGeneral, "", ""},
		{"invite match ignores case", []string{"invite", "get",
			"--email", "alice@example.com"}, 200,
			`[` + inviteAliceBody + `]`, ExitOK, "Alice@Example.com", ""},
		{"invite no match", []string{"invite", "get", "--email",
			"bob@example.com"}, 200, `[` + inviteAliceBody + `]`,
			ExitNotFound, "", ""},
		{"member 404 read is not absence", []string{"membership",
			"list", "--email", "alice@example.com"}, 404,
			`{"code":404,"message":"x"}`, ExitGeneral, "", ""},
		{"invite 404 read is not absence", []string{"invite", "get",
			"--email", "alice@example.com"}, 404,
			`{"code":404,"message":"x"}`, ExitGeneral, "", ""},
		{"invite rejected read is not absence", []string{"invite", "get",
			"--email", "alice@example.com"}, 401,
			`{"code":401,"message":"no"}`, ExitAuth, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(tc.status, tc.body))
			err := runAuthedAccount(t, rt, out, url, tc.args...)
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Errorf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
			if tc.want != "" && !strings.Contains(out.String(), tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out.String())
			}
			if tc.notWant != "" &&
				strings.Contains(out.String(), tc.notWant) {
				t.Errorf("output carries %q:\n%s", tc.notWant, out.String())
			}
		})
	}
}

func TestInviteGetNeedsExactlyOneSelector(t *testing.T) {
	for _, args := range [][]string{
		{"invite", "get"},
		{"invite", "get", "d6e7f8a9-b0c1-2345-defa-456789012345",
			"--email", "a@example.com"},
	} {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAccount(t, rt, out, args...)
		if got := cli.ExitCode(err); got != ExitUsage {
			t.Errorf("%v: exit = %d (%v), want 2", args, got, err)
		}
	}
}

func TestInviteGetByEmailText(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		200, `[`+inviteAliceBody+`]`))
	if err := runAuthedAccount(t, rt, out, url,
		"invite", "get", "--email", "alice@example.com"); err != nil {
		t.Fatalf("invite get --email: %v", err)
	}
	if !strings.Contains(out.String(), "EMAIL") ||
		!strings.Contains(out.String(), "i-1") {
		t.Errorf("table missing header or row:\n%s", out.String())
	}
}

// tokenStatusServer answers the token exchange with status and every
// other path, the reachability probe and the tenant read, with 200.
func tokenStatusServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == testsupport.TokenPath {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"x","message":"x"}`))
				return
			}
			_, _ = w.Write([]byte(verdictTenantsBody))
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTokenExchangeFailureIsRejectionOnlyWhenRefused(t *testing.T) {
	cases := []struct {
		status     int
		verdict    string
		strictCode int
		verifyCode int
	}{
		{401, verdictCredentialRejected, ExitAuth, ExitAuth},
		{503, verdictTenantUnresolved, ExitGeneral, ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			url := tokenStatusServer(t, tc.status)

			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			err := runAuthedAccount(t, rt, out, url, "doctor", "--strict")
			if got := cli.ExitCode(err); got != tc.strictCode {
				t.Errorf("doctor --strict exit = %d (%v), want %d",
					got, err, tc.strictCode)
			}
			var report struct {
				Verdict string `json:"verdict"`
			}
			if jerr := decodeFirst(out.Bytes(), &report); jerr != nil {
				t.Fatalf("decode %q: %v", out.String(), jerr)
			}
			if report.Verdict != tc.verdict {
				t.Errorf("verdict = %q, want %q", report.Verdict, tc.verdict)
			}

			rt, out, _ = testsupport.NewRuntime(t, "", "json")
			err = runAuthedAccount(t, rt, out, url,
				"auth", "status", "--verify")
			if got := cli.ExitCode(err); got != tc.verifyCode {
				t.Errorf("auth status --verify exit = %d (%v), want %d",
					got, err, tc.verifyCode)
			}
		})
	}
}

func TestAuthStatusVerifyReadsForASession(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	testsupport.SeedSession(t, rt, s.api, "u1",
		time.Now().Add(time.Hour), "t-a")

	if err := runAccount(t, rt, &bytes.Buffer{},
		"auth", "status", "--verify"); err != nil {
		t.Fatalf("auth status --verify: %v", err)
	}
	if s.apiHits["/account/v1/tenants"] != 1 {
		t.Errorf("tenant reads = %d, want 1",
			s.apiHits["/account/v1/tenants"])
	}
	var report map[string]any
	if err := decodeFirst(out.Bytes(), &report); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if report["verified"] != true {
		t.Errorf("verified = %v, want true", report["verified"])
	}
}

func TestInviteGetByEmailPrintsEveryMatch(t *testing.T) {
	second := strings.Replace(inviteAliceBody, `"i-1"`, `"i-2"`, 1)
	second = strings.Replace(second, "Alice@Example.com",
		"alice@example.com", 1)
	other := strings.Replace(inviteAliceBody, "Alice@", "bob@", 1)
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200,
		`[`+inviteAliceBody+`,`+second+`,`+other+`]`))
	if err := runAuthedAccount(t, rt, out, url,
		"invite", "get", "--email", "ALICE@example.com"); err != nil {
		t.Fatalf("invite get --email: %v", err)
	}
	var got []map[string]any
	if err := decodeFirst(out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if len(got) != 2 {
		t.Errorf("got %d invites, want 2:\n%s", len(got), out.String())
	}
}

func TestEmailLookupRefusals(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		body     string
		wantCode int
	}{
		{"blank member email", []string{"membership", "list",
			"--email", "  "}, "[]", ExitUsage},
		{"blank invite email", []string{"invite", "get",
			"--email", " "}, "[]", ExitUsage},
		{"member list without a body", []string{"membership", "list",
			"--email", "a@example.com"}, "null", ExitGeneral},
		{"invite list without a body", []string{"invite", "get",
			"--email", "a@example.com"}, "null", ExitGeneral},
		{"member list answered 204", []string{"membership", "list",
			"--email", "a@example.com"}, "", ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			status := http.StatusOK
			if tc.body == "" {
				status = http.StatusNoContent
			}
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(status, tc.body))
			err := runAuthedAccount(t, rt, out, url, tc.args...)
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Errorf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
		})
	}
}

func TestDoctorBYOCEntitlement(t *testing.T) {
	cases := []struct {
		name         string
		tenantStatus int
		byocStatus   int
		byocBody     string
		verdict      string
		wantBYOC     string // "" means the key must be absent
		wantProbes   int
		wantCode     int
	}{
		{"entitled", 200, 200, `[]`, verdictReady, byocEntitled, 1,
			ExitOK},
		{"plan denied", 200, 400, `{"code":400,"message":"plan does ` +
			`not allow creating cloud account read"}`, verdictReady,
			byocNotEntitled, 1, ExitOK},
		{"server error", 200, 503, `{"code":503,"message":"x"}`,
			verdictReady, byocUnknown, 1, ExitOK},
		{"not ready sends no probe", 401, 200, `[]`,
			verdictCredentialRejected, "", 0, ExitAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			probes := 0
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/account/v1/tenants":
						testsupport.JSONHandler(tc.tenantStatus,
							verdictTenantsBody)(w, r)
					case "/byoc/v1/cloud-accounts":
						probes++
						testsupport.JSONHandler(tc.byocStatus,
							tc.byocBody)(w, r)
					default:
						w.WriteHeader(http.StatusOK)
					}
				})
			err := runAuthedAccount(t, rt, out, url, "doctor", "--strict")
			if got := cli.ExitCode(err); got != tc.wantCode {
				t.Errorf("exit = %d (%v), want %d", got, err, tc.wantCode)
			}
			var report map[string]any
			if jerr := decodeFirst(out.Bytes(), &report); jerr != nil {
				t.Fatalf("decode %q: %v", out.String(), jerr)
			}
			if report["verdict"] != tc.verdict {
				t.Errorf("verdict = %v, want %s", report["verdict"],
					tc.verdict)
			}
			got, present := report["byoc"]
			if (tc.wantBYOC == "" && present) ||
				(tc.wantBYOC != "" && got != tc.wantBYOC) {
				t.Errorf("byoc = %v (present %v), want %q",
					got, present, tc.wantBYOC)
			}
			if probes != tc.wantProbes {
				t.Errorf("probes = %d, want %d", probes, tc.wantProbes)
			}
		})
	}
}
