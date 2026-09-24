package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// statusAPIURL is the endpoint the `auth status` tests below name
// explicitly, in both the seeded cache's binding and the command's
// own --api-url.
//
// It is a fabricated address rather than an httptest stub because
// `auth status` dials nothing — it reports resolution and cache state
// only. It is passed explicitly rather than left to the default so the
// binding a test seeds and the binding the command computes come from
// one visible value: a token's fingerprint now covers the API URL
// too, so a test that seeded one endpoint while the command
// resolved another would report a mismatch for a reason it never
// meant to exercise.
const statusAPIURL = "https://api.example.test"

// wantAuthExit fails t unless err is a *conn.ExitError carrying want.
func wantAuthExit(t *testing.T, err error, want int) {
	t.Helper()
	var ee *conn.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v (%T), want *conn.ExitError", err, err)
	}
	if ee.Code() != want {
		t.Errorf("exit code = %d, want %d", ee.Code(), want)
	}
}

// TestAuthLeavesRejectStrayArgument pins the Args: cobra.NoArgs fix on
// status, login and logout: without it, a forgotten "-o" (`auth status
// json`) or any other stray token ran silently instead of failing, the
// same gap the pure-router gate does not reach because these
// are leaves, not routers. Matters more now that status is a scripting
// primitive an agent might call with a typo'd flag.
//
// Every one of these three verbs already returns a non-nil error on a
// bare, hermetic Runtime for reasons that have nothing to do with
// Args — status/login fail credential resolution or stdin read, and a
// naive "err != nil" assertion passes on that unrelated failure
// whether or not the Args gate exists, which is exactly the false
// pass a first attempt at this test produced. Asserting the exact
// cobra.NoArgs message (`unknown command "stray" for ...`) is what
// pins the gate itself: RunE never runs when Args rejects first, so
// only the Args violation can produce this specific text.
func TestAuthLeavesRejectStrayArgument(t *testing.T) {
	for _, verb := range []string{"status", "login", "logout"} {
		t.Run(verb, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAccount(t, rt, out, "auth", verb, "stray")
			if err == nil {
				t.Fatalf("auth %s: expected a usage error for a stray "+
					"argument", verb)
			}
			const want = `unknown command "stray" for`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("auth %s: err = %q, want it to contain %q "+
					"(a different error means the failure is incidental, "+
					"not the Args gate)", verb, err.Error(), want)
			}
		})
	}
}

func TestAuthHelp(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAccount(t, rt, out, "auth", "--help"); err != nil {
		t.Fatalf("auth help: %v", err)
	}
	for _, want := range []string{"login", "status", "logout"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("auth help missing %q: %q", want, out.String())
		}
	}
}

// TestAuthStatusNotAuthenticated pins state (b): no credentials
// anywhere. auth status is the only reason to run this command, so
// reporting it must be a non-zero (auth-failure) exit — see the exit
// code comment in newAuthStatusCmd.
func TestAuthStatusNotAuthenticated(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAccount(t, rt, out, "auth", "status")
	if !strings.Contains(out.String(), "Not authenticated") {
		t.Errorf("want 'Not authenticated', got %q", out.String())
	}
	wantAuthExit(t, err, conn.ExitAuth)
}

// TestAuthStatusJSONNotAuthenticated is state (b) in the machine
// format: a single parseable object on stdout carrying
// authenticated:false, and no "problem" key (that is reserved for the
// half-supplied-pair diagnosis, not the ordinary no-credentials case —
// matching cloud doctor's authInfo convention).
func TestAuthStatusJSONNotAuthenticated(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	err := runAccount(t, rt, out, "auth", "status")
	s := out.String()
	if !strings.Contains(s, `"authenticated":false`) {
		t.Errorf("want authenticated:false, got %q", s)
	}
	if strings.Contains(s, `"problem"`) {
		t.Errorf("no-credentials case must not carry a problem key: %q", s)
	}
	if strings.Contains(s, `"token_valid":true`) {
		t.Errorf("want token_valid:false, got %q", s)
	}
	wantAuthExit(t, err, conn.ExitAuth)
}

// TestAuthStatusYAMLNotAuthenticated is the same state (b) assertion
// against -o yaml, so the third format is not left unexercised.
func TestAuthStatusYAMLNotAuthenticated(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "yaml")
	err := runAccount(t, rt, out, "auth", "status")
	s := out.String()
	if !strings.Contains(s, "authenticated: false") {
		t.Errorf("want authenticated: false, got %q", s)
	}
	wantAuthExit(t, err, conn.ExitAuth)
}

// TestAuthStatusNamesAHalfSuppliedFlagPair is `auth status`'s half of
// the misdiagnosis `cloud doctor` had. Both commands exist to explain
// a broken connection, and a bare "Not authenticated." for a
// half-supplied flag pair sends the operator to `auth login` when they
// are already logged in — the profile below holds a complete pair that
// the incomplete flags are overriding.
func TestAuthStatusNamesAHalfSuppliedFlagPair(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantContains string
	}{
		{"id without secret", []string{"--client-id", "only-an-id"},
			"--client-id given without --client-secret"},
		{"secret without id",
			[]string{"--client-secret", "only-a-secret"},
			"--client-secret given without --client-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{
					ClientID: "cfg-id", ClientSecret: "cfg-secret"})

			args := append([]string{"auth", "status"}, tc.args...)
			err := runAccount(t, rt, out, args...)
			s := out.String()
			if !strings.Contains(s, tc.wantContains) {
				t.Errorf("status did not name the cause %q:\n%s",
					tc.wantContains, s)
			}
			// The credential values must never be echoed back.
			for _, secret := range []string{"cfg-secret", "only-a-secret"} {
				if strings.Contains(s, secret) {
					t.Errorf("status echoed a secret value:\n%s", s)
				}
			}
			// ExitUsage, not ExitAuth. status must not report exit 0 for
			// a half pair, but it is not an authentication failure
			// either: the operator mistyped the command, and no
			// credential was ever offered to anyone. This asserted
			// ExitAuth and passed only because ExitAuth was 2 — the same
			// mis-tagging conn.Resolve had, in a second place.
			wantAuthExit(t, err, conn.ExitUsage)
		})
	}
}

// TestAuthStatusJSONHalfSuppliedFlagPair is state (a) in the machine
// format: the problem key is populated with the same cause named in
// text, and the exit code is ExitUsage — deliberately NOT the code the
// plain no-credentials case reports, because a mistyped command and an
// absent credential are different problems with different fixes.
func TestAuthStatusJSONHalfSuppliedFlagPair(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	rt.Config.SetStarfleetProfile(rt.Profile,
		&config.StarfleetProfile{
			ClientID: "cfg-id", ClientSecret: "cfg-secret"})

	err := runAccount(t, rt, out,
		"auth", "status", "--client-id", "only-an-id")
	s := out.String()
	if !strings.Contains(s,
		`"problem":"--client-id given without --client-secret`) {
		t.Errorf("want a problem field naming the cause, got %q", s)
	}
	if !strings.Contains(s, `"authenticated":false`) {
		t.Errorf("want authenticated:false, got %q", s)
	}
	if strings.Contains(s, "cfg-secret") {
		t.Errorf("status echoed a secret value:\n%s", s)
	}
	wantAuthExit(t, err, conn.ExitUsage)
}

// TestAuthStatusValidToken pins state (c): credentials resolve and a
// cached, unexpired token is present. Exit 0, token_valid:true.
func TestAuthStatusValidToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	writeAccountToken(t, time.Now().Add(time.Hour), statusAPIURL)

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail once authenticated: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("want authenticated:true, got %q", s)
	}
	if !strings.Contains(s, `"token_valid":true`) {
		t.Errorf("want token_valid:true, got %q", s)
	}
	if !strings.Contains(s, `"client_id":"id"`) {
		t.Errorf("want client_id in report, got %q", s)
	}
}

// TestAuthStatusStaleToken covers the new binding state: credentials
// resolve and a cached, unexpired token exists, but it was minted by a
// different credential (D3/D7). status must still exit 0 — the
// credential itself is fine, and the CLI self-corrects on the next
// command that actually needs a token — but the text must say the
// token is stale rather than valid, and JSON must carry
// token_bound:false.
func TestAuthStatusStaleToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	writeAccountTokenBoundTo(t, time.Now().Add(time.Hour),
		statusAPIURL, "id", "old-secret")

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail on a stale token: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"Token:        stale (different credential or API URL)") {
		t.Errorf("want the stale-token line, got %q", s)
	}
	if strings.Contains(s, "Token:        valid") {
		t.Errorf("must not report a mismatched token as valid: %q", s)
	}
}

// TestAuthStatusTokenBoundToADifferentAPIURL is endpoint binding at
// `auth status`: the credential is identical, only the endpoint moved.
// The cached token belongs to the other host's auth server, so
// reporting it as `valid` here would tell an operator the connection
// is warm when the next command is going to re-authenticate — and,
// before the fix, would have been the visible half of the CLI
// preparing to send that token to a host that never minted it.
func TestAuthStatusTokenBoundToADifferentAPIURL(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	writeAccountTokenBoundTo(t, time.Now().Add(time.Hour),
		"https://other.example.test", "id", "secret")

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail on a stale token: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"Token:        stale (different credential or API URL)") {
		t.Errorf("want the stale-token line for a token minted by "+
			"another endpoint, got %q", s)
	}
	if strings.Contains(s, "Token:        valid") {
		t.Errorf("must not report a token minted by another endpoint "+
			"as valid: %q", s)
	}
}

// TestAuthStatusStaleTokenJSON is the machine-readable half: exit 0,
// token_bound:false, and the fingerprint value never appears anywhere
// in the output.
//
// token_valid:true is asserted too, and deliberately, not just
// tolerated: the cached token IS unexpired, and TokenValid has always
// meant exactly that — nothing else. `cloud doctor`'s authInfo
// computes TokenValid from expiry alone and would report
// token_valid:true for this identical on-disk state, so this command
// reporting token_valid:false for it would be a second source of
// truth disagreeing with the first about the same fact. TokenBound is
// the field that carries the mismatch; TokenValid must not also try
// to carry it.
func TestAuthStatusStaleTokenJSON(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	writeAccountTokenBoundTo(t, time.Now().Add(time.Hour),
		statusAPIURL, "id", "old-secret")

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail on a stale token: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"token_valid":true`) {
		t.Errorf("want token_valid:true (the cache IS unexpired; only "+
			"its binding is stale), got %q", s)
	}
	if !strings.Contains(s, `"token_bound":false`) {
		t.Errorf("want token_bound:false, got %q", s)
	}
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("want authenticated:true, got %q", s)
	}
	for _, fp := range []string{
		auth.Fingerprint(statusAPIURL, "id", "secret"),
		auth.Fingerprint(statusAPIURL, "id", "old-secret"),
	} {
		if strings.Contains(s, fp) {
			t.Errorf("status leaked a binding fingerprint: %q", s)
		}
	}
}

// TestAuthStatusValidTokenReportsBound is TestAuthStatusValidToken's
// counterpart for the new field: a token bound to the exact credential
// in use must report token_bound:true.
func TestAuthStatusValidTokenReportsBound(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	writeAccountToken(t, time.Now().Add(time.Hour), statusAPIURL)

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail once authenticated: %v", err)
	}
	if !strings.Contains(out.String(), `"token_bound":true`) {
		t.Errorf("want token_bound:true, got %q", out.String())
	}
}

// TestAuthStatusMissingToken pins state (d)'s "never cached" shape:
// credentials resolve but no token has ever been fetched. This is
// deliberately NOT a failure — the CLI mints a token on demand the
// next time one is needed — so the exit code stays 0, distinguishing
// "not authenticated" (exit 2) from "authenticated, token not warm
// yet" (exit 0).
func TestAuthStatusMissingToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")

	err := runAccount(t, rt, out, "auth", "status",
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail with no cached token: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("want authenticated:true, got %q", s)
	}
	if !strings.Contains(s, `"token_valid":false`) {
		t.Errorf("want token_valid:false, got %q", s)
	}
	if strings.Contains(s, "expires_at") {
		t.Errorf("expires_at must be omitted with no cached token: %q", s)
	}
}

// TestAuthStatusExpiredToken pins state (d)'s other shape: a token
// exists but has expired. Also exit 0 for the same reason as the
// missing-token case, and expires_at is still reported (the value is
// there and useful, only its validity is stale).
func TestAuthStatusExpiredToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	writeAccountToken(t, time.Now().Add(-time.Hour), statusAPIURL)

	err := runAccount(t, rt, out, "auth", "status",
		"--api-url", statusAPIURL,
		"--client-id", "id", "--client-secret", "secret")
	if err != nil {
		t.Fatalf("auth status must not fail with an expired token: %v", err)
	}
	if !strings.Contains(out.String(), "Token:        expired") {
		t.Errorf("want the expired-token line, got %q", out.String())
	}
}

// TestAuthStatusNeverPrintsSecretValue is the mutation-critical
// assertion for the binding secret-safety requirement: the client ID
// is printed (it is an identifier, not a credential, by standing
// ruling), but the secret's value must never appear in any
// output format. A recognisable secret is planted via config (a
// resolution path status does not otherwise exercise in these tests)
// rather than via a flag, so the resolution source itself is also
// covered.
func TestAuthStatusNeverPrintsSecretValue(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", format)
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{
					ClientID:     "cfg-client-id",
					ClientSecret: "unmistakable-secret-value",
				})

			err := runAccount(t, rt, out, "auth", "status")
			if err != nil {
				t.Fatalf("auth status: %v", err)
			}
			got := out.String()
			if strings.Contains(got, "unmistakable-secret-value") {
				t.Errorf("auth status leaked the secret value in %s "+
					"output:\n%s", format, got)
			}
			if !strings.Contains(got, "cfg-client-id") {
				t.Errorf("auth status should print the client ID in "+
					"%s output:\n%s", format, got)
			}
		})
	}
}

func TestAuthLoginThenStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/account/v1/oauth/token" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w,
				`{"access_token":"tok","token_type":"Bearer",`+
					`"expires_in":3600}`)
		}))
	defer srv.Close()

	rt, out, _ := testsupport.NewRuntime(t, "myid\nmysecret\n", "text")
	if err := runAccount(t, rt, out, "auth", "login",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	// The profile must have been persisted for later commands.
	cp := rt.Config.StarfleetProfile("default")
	if cp.ClientID != "myid" {
		t.Errorf("saved client id = %q, want myid", cp.ClientID)
	}
	if cp.APIURL != srv.URL {
		t.Errorf("saved api url = %q, want %q", cp.APIURL, srv.URL)
	}

	// Status should now report the stored credentials and a valid
	// cached token.
	out.Reset()
	if err := runAccount(t, rt, out, "auth", "status",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(out.String(), "myid") {
		t.Errorf("status missing client id: %q", out.String())
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("status missing valid token: %q", out.String())
	}
}

// TestAuthLoginUsesCredentialFlags is the regression gate on the bug
// this pair of tests was written for: --client-id and --client-secret
// were declared on the account root, advertised in `login --help`, and
// read by nothing. `login` prompted regardless, so the documented
// scriptable form failed with "read client ID: EOF" the moment stdin
// was closed — the exact shape a CI job or a provisioning script hits.
//
// Stdin is deliberately EMPTY. That is the assertion: any read of it
// fails immediately, so a login that still reaches the prompt cannot
// pass this test by accident. Asserting on the absence of the "Client
// ID:" banner alone would not — a prompt whose read succeeded from a
// primed buffer still leaves the command unscriptable.
func TestAuthLoginUsesCredentialFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/account/v1/oauth/token" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, testsupport.TokenBody)
		}))
	defer srv.Close()

	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	if err := runAccount(t, rt, out, "auth", "login",
		"--client-id", "flag-id",
		"--client-secret", "unmistakable-secret-value",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login with both credential flags: %v", err)
	}

	cp := rt.Config.StarfleetProfile("default")
	if cp.ClientID != "flag-id" {
		t.Errorf("saved client id = %q, want flag-id", cp.ClientID)
	}
	if cp.ClientSecret != "unmistakable-secret-value" {
		t.Error("the secret supplied by flag was not saved")
	}

	// Neither prompt may appear: there is nothing to ask for.
	if strings.Contains(errb.String(), "Client ID:") ||
		strings.Contains(errb.String(), "Client Secret:") {
		t.Errorf("login prompted despite both flags being supplied:\n%s",
			errb.String())
	}

	// A secret arriving by flag is no more printable than a typed one.
	// term.ReadPassword suppresses the echo of the typed path; nothing
	// suppresses a stray Fprintf, so this is the only guard on it.
	for _, stream := range []struct {
		name string
		got  string
	}{{"stdout", out.String()}, {"stderr", errb.String()}} {
		if strings.Contains(stream.got, "unmistakable-secret-value") {
			t.Errorf("login echoed the flag-supplied secret on %s:\n%s",
				stream.name, stream.got)
		}
	}
}

// TestAuthLoginHalfFlagPairIsUsageError pins the deliberate choice not
// to prompt for the missing half. `login` could reasonably ask for it,
// but every other command in the tree treats half a pair as a typo and
// exits 2 (conn.Resolve, auth status), and a `login` that quietly
// completed the pair from a prompt would be the one command where a
// mistyped --client-id still produced a stored, working credential
// under a different identity.
//
// Exit 2 is ExitUsage; ExitAuth was once also 2, which
// is precisely how the mis-tagging in conn.Resolve stayed invisible.
// Asserting the constant rather than the literal keeps that distinct.
func TestAuthLoginHalfFlagPairIsUsageError(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{
			name:       "id without secret",
			args:       []string{"--client-id", "flag-id"},
			wantSubstr: "--client-secret",
		},
		{
			name:       "secret without id",
			args:       []string{"--client-secret", "flag-secret"},
			wantSubstr: "--client-id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			args := append([]string{"auth", "login"}, tc.args...)
			err := runAccount(t, rt, out, args...)

			if err == nil {
				t.Fatal("want an error for a half-supplied flag pair")
			}
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v (%T), want *ExitError", err, err)
			}
			if ee.Code() != ExitUsage {
				t.Errorf("exit code = %d, want ExitUsage (%d)",
					ee.Code(), ExitUsage)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not name the missing flag %q",
					err, tc.wantSubstr)
			}
			// It must fail on the flags, not by reaching an empty stdin.
			if strings.Contains(err.Error(), "EOF") {
				t.Errorf("login prompted instead of rejecting the "+
					"half pair: %v", err)
			}
			if strings.Contains(errb.String(), "Client ID:") {
				t.Errorf("login prompted before validating the flags:"+
					"\n%s", errb.String())
			}
			// Nothing may be persisted from a rejected invocation.
			if cp := rt.Config.StarfleetProfile("default"); cp != nil &&
				(cp.ClientID != "" || cp.ClientSecret != "") {
				t.Errorf("a rejected login stored credentials: id=%q "+
					"secret set=%v", cp.ClientID, cp.ClientSecret != "")
			}
		})
	}
}

// TestAuthLoginStillPromptsWithoutFlags is the other direction of the
// same change: honouring the flags must not cost the interactive path.
// It also pins that `login` does NOT fall through to config the way
// conn.Resolve does — a complete pair is stored in the profile config
// here, and login must still ask, because someone running `login` is
// signing in as somebody, and answering that from ambient state would
// silently re-save whoever is already configured.
func TestAuthLoginStillPromptsWithoutFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, testsupport.TokenBody)
		}))
	defer srv.Close()

	rt, out, errb := testsupport.NewRuntime(t, "typed-id\ntyped-secret\n",
		"text")
	rt.Config.SetStarfleetProfile("default", &config.StarfleetProfile{
		ClientID:     "cfg-id",
		ClientSecret: "cfg-secret",
	})

	if err := runAccount(t, rt, out, "auth", "login",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if !strings.Contains(errb.String(), "Client ID:") {
		t.Errorf("login should still prompt with no flags:\n%s",
			errb.String())
	}
	cp := rt.Config.StarfleetProfile("default")
	if cp.ClientID != "typed-id" {
		t.Errorf("saved client id = %q, want the typed value "+
			"typed-id, not the config's", cp.ClientID)
	}
}

func TestAuthLogout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w,
				`{"access_token":"tok","token_type":"Bearer",`+
					`"expires_in":3600}`)
		}))
	defer srv.Close()

	rt, out, errb := testsupport.NewRuntime(t, "myid\nmysecret\n", "text")
	if err := runAccount(t, rt, out, "auth", "login",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	if _, err := conn.Store(rt).LoadToken(); err != nil {
		t.Fatalf("expected cached token after login: %v", err)
	}

	out.Reset()
	errb.Reset()
	if err := runAccount(t, rt, out, "auth", "logout"); err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if !strings.Contains(errb.String(), "Logged out") {
		t.Errorf("logout missing confirmation: %q", errb.String())
	}
	if _, err := conn.Store(rt).LoadToken(); err == nil {
		t.Errorf("token cache should be gone after logout")
	}

	// logout also removes the stored client credentials from disk so
	// the persisted secret does not outlive the session.
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	cp := cfg.StarfleetProfile("default")
	if cp.ClientID != "" || cp.ClientSecret != "" {
		t.Errorf("stored credentials should be cleared after logout: "+
			"id=%q secret set=%v", cp.ClientID, cp.ClientSecret != "")
	}
}

// TestAuthLoginCachesUnderStarfleetModule pins the deliberate cache-file
// name that came with the cloud merge: one product, one connection,
// one token, so it lands at ~/.pgedge/cli/cache/<profile>-starfleet.json
// and nothing writes a per-sub-tree name. A cache left under a
// superseded key is ignored rather than migrated, which costs one
// token round trip on the next command.
//
// It is also the end-to-end guard on the token's permissions at rest:
// a real `auth login` creates ~/.pgedge/cli/cache itself, so the 0700
// on the directory and the 0600 on the file are both asserted against
// the path a user's token actually lands on.
func TestAuthLoginCachesUnderStarfleetModule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, testsupport.TokenBody)
		}))
	defer srv.Close()

	rt, out, _ := testsupport.NewRuntime(t, "myid\nmysecret\n", "text")
	if err := runAccount(t, rt, out, "auth", "login",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	cacheDir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	info, err := os.Stat(filepath.Join(cacheDir, "default-starfleet.json"))
	if err != nil {
		t.Fatalf("expected default-starfleet.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token cache perms = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("cache dir perms = %o, want 700", perm)
	}
	if _, err := os.Stat(
		filepath.Join(cacheDir, "default-account.json"),
	); !os.IsNotExist(err) {
		t.Errorf("superseded default-account.json written: %v", err)
	}
}

// login used to report DefaultPath() unconditionally while Save()
// wrote rt.Config's own path, so under --config it told an operator
// their client secret had gone to ~/.pgedge/cli/config.yaml when it had
// gone somewhere else. That is the --config path defect with a credential
// attached: the cost is looking for a secret in the wrong file, or
// leaving one behind in a file believed untouched.
func TestAuthLoginNamesTheFileItActuallyWrote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/account/v1/oauth/token" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, testsupport.TokenBody)
		}))
	defer srv.Close()

	// The file must exist: an explicit --config naming nothing is
	// itself an error, which is a different behaviour from
	// the one under test here.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(
		elsewhere, []byte("current_profile: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(elsewhere)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	rt.Config = cfg
	if err := runAccount(t, rt, out, "auth", "login",
		"--client-id", "flag-id",
		"--client-secret", "a-secret",
		"--api-url", srv.URL); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	if !strings.Contains(errb.String(), elsewhere) {
		t.Errorf("login does not name the file it wrote (%s):\n%s",
			elsewhere, errb.String())
	}
	// And it must not name the default, which it did not write.
	def, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errb.String(), def) {
		t.Errorf("login named %s, which it did not write:\n%s",
			def, errb.String())
	}
	// The control: the credentials really did land in that file.
	reloaded, err := config.Load(elsewhere)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.StarfleetProfile("default").ClientID != "flag-id" {
		t.Error("the credentials were not written to the named file")
	}
}
