package cmd

import (
	"encoding/json"
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

// writeAccountToken writes a cached account token for the default
// profile with the given expiry, bound to the "id"/"secret" pair every
// doctor test below authenticates with via --client-id/--client-secret.
// checkAuth's token branches are reachable without a token exchange.
func writeAccountToken(t *testing.T, expiresAt time.Time, apiURL string) {
	t.Helper()
	writeAccountTokenBoundTo(t, expiresAt, apiURL, "id", "secret")
}

// writeAccountTokenBoundTo writes a cached token whose
// binding_fingerprint is bound to the given (apiURL, clientID,
// clientSecret) triple, so callers can seed a cache that mismatches
// the connection a test actually authenticates with — in either the
// credential or the API URL (#146).
//
// apiURL is a parameter rather than a fixture constant because every
// doctor test names a fresh httptest stub, and a seed bound to
// anything else would report a mismatch for a reason the test never
// intended.
func writeAccountTokenBoundTo(
	t *testing.T, expiresAt time.Time, apiURL, clientID, clientSecret string,
) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	raw, err := json.Marshal(&auth.CachedToken{
		AccessToken: "tok",
		ExpiresAt:   expiresAt,
		Fingerprint: auth.Fingerprint(apiURL, clientID, clientSecret),
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "default-starfleet.json"),
		raw, 0o600); err != nil {
		t.Fatalf("write token cache: %v", err)
	}
}

// writeLegacyAccountToken writes a cache file with no binding
// fingerprint field at all, under either its current or its former
// name — the pre-binding shape (#107 D5) — so tests can exercise
// "unbound cache treated as mismatch" without going through
// auth.Auth.SaveToken, which now refuses to write one.
func writeLegacyAccountToken(t *testing.T, expiresAt time.Time) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	body := `{"access_token":"tok","expires_at":"` +
		expiresAt.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(
		filepath.Join(dir, "default-starfleet.json"),
		[]byte(body), 0o600); err != nil {
		t.Fatalf("write token cache: %v", err)
	}
}

// writeCorruptAccountToken writes a cache file that exists and is
// readable but is not valid JSON, so auth.LoadToken fails at the
// json.Unmarshal rather than at the os.ReadFile — the "present but
// unusable" state, distinct from "absent".
func writeCorruptAccountToken(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "default-starfleet.json"),
		[]byte("{not json at all"), 0o600); err != nil {
		t.Fatalf("write corrupt token cache: %v", err)
	}
}

// stubReachableAPI returns the URL of a local server answering 200, for
// checkAPI to probe. Doctor tests must never dial a real host.
func stubReachableAPI(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// unreachableAddr returns an address that refuses connections, so
// checkAPI takes its transport-error branch hermetically.
func unreachableAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()
	return addr
}

func TestAccountDoctorWithNoCredentials(t *testing.T) {
	// NewRuntime isolates HOME and supplies no flags or profile
	// config, so this is genuinely the no-credentials case regardless
	// of what the developer's own shell has exported.
	rt, out, _ := testsupport.NewRuntime(t, "", "json")

	// Must not error, and must not dial the default API.
	if err := runDoctor(rt, &conn.Flags{
		APIURL: "http://127.0.0.1:1",
	}); err != nil {
		t.Fatalf("doctor must not fail without credentials: %v", err)
	}
	// Assert the auth *verdict*, not the key name: `"authenticated"`
	// alone matches whatever the value is, so it could never fail for
	// the reason this test exists.
	if !strings.Contains(out.String(), `"authenticated":false`) {
		t.Errorf("expected authenticated false with no credentials:\n%s",
			out.String())
	}
	if !strings.Contains(out.String(), `"token_valid":false`) {
		t.Errorf("expected token_valid false with no credentials:\n%s",
			out.String())
	}
	if strings.Contains(out.String(), "api.pgedge.com") {
		t.Errorf("doctor resolved past the --api-url override:\n%s",
			out.String())
	}
}

func TestAccountDoctorTextWithNoCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")

	if err := runDoctor(rt, &conn.Flags{
		APIURL: unreachableAddr(t),
	}); err != nil {
		t.Fatalf("doctor must not fail without credentials: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"Auth", "not authenticated", "API connectivity", "unreachable",
		"Environment",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor output missing %q:\n%s", want, s)
		}
	}
}

func TestAccountDoctorFullyHealthy(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	apiURL := stubReachableAPI(t)
	writeAccountToken(t, time.Now().Add(time.Hour), apiURL)

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"authenticated via flags", "expires", "200",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor output missing %q:\n%s", want, s)
		}
	}
}

// TestAccountDoctorFullyHealthyReportsTokenBound pins the JSON half of
// the "bound" case: token_bound:true, and — the security constraint —
// the fingerprint value itself appears nowhere in the output, in any
// field, under any name.
func TestAccountDoctorFullyHealthyReportsTokenBound(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	apiURL := stubReachableAPI(t)
	writeAccountToken(t, time.Now().Add(time.Hour), apiURL)

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"token_bound":true`) {
		t.Errorf("expected token_bound:true for a bound cache:\n%s", s)
	}
	fp := auth.Fingerprint(apiURL, "id", "secret")
	if strings.Contains(s, fp) {
		t.Errorf("doctor JSON leaked the binding fingerprint:\n%s", s)
	}
}

// TestAccountDoctorMismatchedTokenIsWarning covers D7's new row: the
// credential resolves and a token is cached and unexpired, but it was
// minted by a different credential (a rekey, or a one-off flag
// override hitting a profile-minted cache). That must be a warning
// naming the divergence, not the "ok" wording doctor has always used —
// doctor's whole job is to never again assert something it has not
// checked.
func TestAccountDoctorMismatchedTokenIsWarning(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	apiURL := stubReachableAPI(t)
	writeAccountTokenBoundTo(
		t, time.Now().Add(time.Hour), apiURL, "id", "old-secret")

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"the cached token was minted with a different credential or API URL") {
		t.Errorf("expected the mismatch warning:\n%s", s)
	}
	if !strings.Contains(s, "the next command will re-authenticate") {
		t.Errorf("expected the self-correction explanation:\n%s", s)
	}
	if strings.Contains(s, "authenticated via flags (expires") {
		t.Errorf("doctor still reports the unqualified ok wording for a "+
			"mismatched cache:\n%s", s)
	}
}

// TestAccountDoctorTokenBoundToADifferentAPIURLIsWarning is issue #146
// at the diagnostic. The credential is byte-identical to the one that
// minted the cached token; only the endpoint differs. Before the URL
// joined the binding, doctor reported this state as a flat `ok /
// authenticated via flags` — asserting the cached token belonged to
// the connection about to be used, which is exactly what it had not
// checked.
//
// The seeded URL is a second live stub rather than a bare string so
// the two URLs differ only in port: nothing here can pass because one
// of them failed to parse or failed to answer.
func TestAccountDoctorTokenBoundToADifferentAPIURLIsWarning(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	apiURL := stubReachableAPI(t)
	otherURL := stubReachableAPI(t)
	if apiURL == otherURL {
		t.Fatalf("stubs collided on %s; the test would prove nothing", apiURL)
	}
	writeAccountTokenBoundTo(
		t, time.Now().Add(time.Hour), otherURL, "id", "secret")

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"the cached token was minted with a different credential or API URL") {
		t.Errorf("expected the mismatch warning for a token bound to "+
			"another endpoint:\n%s", s)
	}
	if strings.Contains(s, "authenticated via flags (expires") {
		t.Errorf("doctor reports ok for a token minted by a host other "+
			"than the one configured:\n%s", s)
	}
}

// TestAccountDoctorTokenBoundToADifferentAPIURLJSON is the
// machine-readable half of #146: token_bound:false on an identical
// credential, with authenticated:true — the credential resolves fine,
// it is the binding that does not match.
func TestAccountDoctorTokenBoundToADifferentAPIURLJSON(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	apiURL := stubReachableAPI(t)
	otherURL := stubReachableAPI(t)
	writeAccountTokenBoundTo(
		t, time.Now().Add(time.Hour), otherURL, "id", "secret")

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("expected authenticated:true:\n%s", s)
	}
	if !strings.Contains(s, `"token_bound":false`) {
		t.Errorf("expected token_bound:false for a token bound to "+
			"another endpoint:\n%s", s)
	}
}

// TestAccountDoctorMismatchedTokenJSON is the machine-readable half:
// token_bound:false, still authenticated:true (the credential itself
// resolves fine), and — again — no fingerprint value anywhere.
func TestAccountDoctorMismatchedTokenJSON(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	apiURL := stubReachableAPI(t)
	writeAccountTokenBoundTo(
		t, time.Now().Add(time.Hour), apiURL, "id", "old-secret")

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("expected authenticated:true:\n%s", s)
	}
	if !strings.Contains(s, `"token_bound":false`) {
		t.Errorf("expected token_bound:false for a mismatched cache:\n%s", s)
	}
	for _, fp := range []string{
		auth.Fingerprint(apiURL, "id", "secret"),
		auth.Fingerprint(apiURL, "id", "old-secret"),
	} {
		if strings.Contains(s, fp) {
			t.Errorf("doctor JSON leaked a binding fingerprint:\n%s", s)
		}
	}
}

// TestAccountDoctorLegacyUnboundTokenIsWarning covers D5's invalidation
// rule end to end: a cache file written before this field existed has
// no binding fingerprint at all, and must report exactly the same
// warning as an explicit mismatch — provenance unknown is provenance
// untrusted, not a third state to explain. A file carrying the older
// `credential_fingerprint` name decodes into the same state, which is
// why renaming the key needed no migration code (#146).
func TestAccountDoctorLegacyUnboundTokenIsWarning(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	writeLegacyAccountToken(t, time.Now().Add(time.Hour))

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       stubReachableAPI(t),
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"the cached token was minted with a different credential or API URL") {
		t.Errorf("expected the mismatch warning for a legacy cache:\n%s", s)
	}
}

// TestAccountDoctorPreBindingKeyNameIsUnbound pins the D3 upgrade
// story: a cache written by the build that shipped #107 spells the
// field `credential_fingerprint`, which no longer decodes, so the
// token reads as unbound and is discarded. This is the one-off cost of
// the rename, and it must be exactly that — a warning and a
// re-authentication, never a silently accepted binding.
func TestAccountDoctorPreBindingKeyNameIsUnbound(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	apiURL := stubReachableAPI(t)

	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"access_token":"tok","expires_at":"` +
		time.Now().Add(time.Hour).Format(time.RFC3339) +
		`","credential_fingerprint":"` +
		auth.Fingerprint(apiURL, "id", "secret") + `"}`
	if err := os.WriteFile(
		filepath.Join(dir, "default-starfleet.json"),
		[]byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s,
		"the cached token was minted with a different credential or API URL") {
		t.Errorf("a cache using the pre-#146 field name must read as "+
			"unbound:\n%s", s)
	}
}

func TestAccountDoctorExpiredToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	// One URL for both the seed and the flag. The expiry branch
	// short-circuits before MintedBy, so a mismatched pair would pass
	// today — and would quietly start testing the wrong thing if that
	// switch were ever reordered.
	apiURL := stubReachableAPI(t)
	writeAccountToken(t, time.Now().Add(-time.Hour), apiURL)

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "token expired or missing") {
		t.Errorf("expected the expired-token warning:\n%s", out.String())
	}
}

// TestAccountDoctorCorruptTokenCache covers the third token state, the
// one between "no cache" and "a cache that parses": a file that exists
// but is not valid JSON. checkAuth funnels every LoadToken error into
// the same branch, so an unparseable cache and an absent one produce
// the same report — that is fine, but it must be pinned, because doctor
// exists precisely to be runnable when authentication is broken and a
// panic or a returned error here would take away the one command that
// still works. Distinguishing the two states would mean a new report
// field; not doing that is a decision, not an oversight.
func TestAccountDoctorCorruptTokenCache(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	writeCorruptAccountToken(t)

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       stubReachableAPI(t),
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor must not fail on a corrupt cache: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "token expired or missing") {
		t.Errorf("expected the token warning for a corrupt cache:\n%s", s)
	}
	// The credential source is still resolvable and must still be
	// reported: a broken cache says nothing about the credentials.
	if !strings.Contains(s, "flags") {
		t.Errorf("expected the credential source to still be named:\n%s",
			s)
	}
}

// TestAccountDoctorCorruptTokenCacheJSON pins the machine-readable half
// of the same state: token_valid false, and no expires_at at all rather
// than a zero time formatted as a real-looking timestamp.
func TestAccountDoctorCorruptTokenCacheJSON(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	writeCorruptAccountToken(t)

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       stubReachableAPI(t),
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor must not fail on a corrupt cache: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `"authenticated":true`) {
		t.Errorf("expected authenticated true:\n%s", s)
	}
	if !strings.Contains(s, `"token_valid":false`) {
		t.Errorf("expected token_valid false:\n%s", s)
	}
	if strings.Contains(s, "expires_at") {
		t.Errorf("expires_at must be omitted when the cache did not "+
			"parse:\n%s", s)
	}
}

// TestAccountDoctorNamesAHalfSuppliedFlagPair is the regression test for
// a misdiagnosis introduced alongside the credential-pairing fix: the
// resolver started rejecting a half-supplied flag pair, checkAuth
// flattened that error into a zero authInfo, and doctor reported "not
// authenticated" — while a complete, working pair sat in the profile,
// ignored only because the incomplete flags outrank it. Doctor exists to
// explain a broken connection, so a wrong explanation is the one output
// it must not produce.
//
// A profile with real credentials is configured deliberately: it is what
// makes "not authenticated" a lie rather than merely unhelpful.
func TestAccountDoctorNamesAHalfSuppliedFlagPair(t *testing.T) {
	cases := []struct {
		name         string
		flags        conn.Flags
		wantContains string
	}{
		{"id without secret", conn.Flags{ClientID: "only-an-id"},
			"--client-id given without --client-secret"},
		{"secret without id", conn.Flags{ClientSecret: "only-a-secret"},
			"--client-secret given without --client-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{
					ClientID: "cfg-id", ClientSecret: "cfg-secret"})
			f := tc.flags
			f.APIURL = stubReachableAPI(t)

			if err := runDoctor(rt, &f); err != nil {
				t.Fatalf("doctor must not fail on a half pair: %v", err)
			}
			s := out.String()
			if !strings.Contains(s, tc.wantContains) {
				t.Errorf("doctor did not name the cause %q:\n%s",
					tc.wantContains, s)
			}
			if strings.Contains(s, "not authenticated") {
				t.Errorf("doctor still reports 'not authenticated' for a "+
					"half-supplied flag pair, with credentials in the "+
					"profile:\n%s", s)
			}
		})
	}
}

// TestAccountDoctorHalfPairJSON pins the machine-readable half, and that
// the new key is scoped: `problem` appears for the half-pair case and is
// absent for the ordinary no-credentials case, so the report's shape is
// unchanged for every input that had nothing to say before.
func TestAccountDoctorHalfPairJSON(t *testing.T) {
	t.Run("half pair carries problem", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		if err := runDoctor(rt, &conn.Flags{
			ClientID: "only-an-id",
			APIURL:   stubReachableAPI(t),
		}); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		s := out.String()
		if !strings.Contains(s,
			`"problem":"--client-id given without --client-secret`) {
			t.Errorf("json missing the problem field:\n%s", s)
		}
		if !strings.Contains(s, `"authenticated":false`) {
			t.Errorf("expected authenticated false:\n%s", s)
		}
	})

	t.Run("no credentials omits problem", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "json", "json")
		if err := runDoctor(rt, &conn.Flags{
			APIURL: stubReachableAPI(t),
		}); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if s := out.String(); strings.Contains(s, "problem") {
			t.Errorf("problem must be omitted when there is nothing "+
				"more specific to say than 'not authenticated':\n%s", s)
		}
	})
}

// TestAccountDoctorJSONFieldNames pins the report's JSON shape: the stub
// URL arrives via conn.Flags.APIURL, and the environment object carries
// only os/arch/no_color_set.
func TestAccountDoctorJSONFieldNames(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")

	if err := runDoctor(rt, &conn.Flags{
		APIURL: stubReachableAPI(t),
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		`"authenticated":false`,
		`"auth"`, `"api"`, `"environment"`,
		`"no_color_set":false`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("json missing %q:\n%s", want, s)
		}
	}
	// The removed env-credential fields must not resurface.
	for _, forbidden := range []string{
		"client_id_set", "client_id_env_var",
		"api_url_set", "api_url_env_var",
		"pgedge_byoc", "pgedge_account",
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("json still carries %q:\n%s", forbidden, s)
		}
	}
}

func TestAccountDoctorNoColorReported(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	t.Setenv("NO_COLOR", "1")

	if err := runDoctor(rt, &conn.Flags{
		APIURL: unreachableAddr(t),
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), `"no_color_set":true`) {
		t.Errorf("expected no_color_set true:\n%s", out.String())
	}
}

func TestAccountDoctorHelp(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAccount(t, rt, out, "doctor", "--help"); err != nil {
		t.Fatalf("doctor help: %v", err)
	}
	if !strings.Contains(out.String(), "doctor") {
		t.Errorf("help missing 'doctor': %q", out.String())
	}
}

func TestAccountDoctorRunsFromTree(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAccount(t, rt, out, "doctor",
		"--api-url", unreachableAddr(t)); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "API connectivity") {
		t.Errorf("output missing 'API connectivity':\n%s", out.String())
	}
}

func TestAccountDoctorRejectsStrayArgument(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAccount(t, rt, out, "doctor", "stray"); err == nil {
		t.Error("expected a usage error for a stray argument")
	}
}

// --- tenant / plan check -----------------------------------------------------

// TestTenantCheckRow pins the rendering of every tenant state. The plan
// is the reason the row exists, so each plan outcome is asserted
// separately rather than through one representative case.
func TestTenantCheckRow(t *testing.T) {
	cases := []struct {
		name       string
		info       tenantInfo
		wantStatus string
		wantHas    []string
		wantLacks  []string
	}{
		{
			name:       "unresolved",
			info:       tenantInfo{},
			wantStatus: "warning",
			wantHas:    []string{"could not resolve tenant"},
			// It must not invent a plan when it read nothing.
			wantLacks: []string{"plan:", "entitlement-denied"},
		},
		{
			name: "enterprise is the only clean plan",
			info: tenantInfo{
				Resolved: true, Name: "acme", ID: "t-1",
				Plan: "enterprise", Count: 1,
			},
			wantStatus: "ok",
			wantHas:    []string{"acme", "t-1", "plan: enterprise"},
			wantLacks:  []string{"entitlement-denied", "trial"},
		},
		{
			name: "managed warns about byoc",
			info: tenantInfo{
				Resolved: true, Name: "acme-corp", ID: "t-2",
				Plan: "managed", Count: 1,
			},
			wantStatus: "warning",
			wantHas: []string{
				"plan: managed",
				"byoc verbs are entitlement-denied on a managed plan",
			},
		},
		{
			name: "developer also warns",
			info: tenantInfo{
				Resolved: true, Name: "old", ID: "t-3",
				Plan: "developer", Count: 1,
			},
			wantStatus: "warning",
			wantHas:    []string{"entitlement-denied on a developer plan"},
		},
		{
			name: "a resolved tenant with no plan says unknown",
			info: tenantInfo{
				Resolved: true, Name: "nop", ID: "t-4", Count: 1,
			},
			wantStatus: "warning",
			wantHas:    []string{"plan: unknown"},
		},
		{
			name: "trial is surfaced",
			info: tenantInfo{
				Resolved: true, Name: "acme", ID: "t-5",
				Plan: "enterprise", PlanTrial: true, Count: 1,
			},
			wantStatus: "ok",
			wantHas:    []string{"[trial]"},
		},
		{
			name: "more than one tenant is not hidden",
			info: tenantInfo{
				Resolved: true, Name: "first", ID: "t-6",
				Plan: "enterprise", Count: 3,
			},
			wantStatus: "ok",
			wantHas:    []string{"3 tenants visible, reporting the first"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := tenantCheckRow(tc.info)
			if row.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (details: %q)",
					row.Status, tc.wantStatus, row.Details)
			}
			if row.Check != "Tenant" {
				t.Errorf("check = %q, want \"Tenant\"", row.Check)
			}
			for _, want := range tc.wantHas {
				if !strings.Contains(row.Details, want) {
					t.Errorf("details %q missing %q", row.Details, want)
				}
			}
			for _, lack := range tc.wantLacks {
				if strings.Contains(row.Details, lack) {
					t.Errorf("details %q must not contain %q",
						row.Details, lack)
				}
			}
		})
	}
}

// TestAccountDoctorReportsTenantPlan drives the whole command against a
// stub that serves a tenant, so the check is exercised through
// runDoctor's wiring rather than only as a pure function.
func TestAccountDoctorReportsTenantPlan(t *testing.T) {
	const body = `[{"id":"t-9","name":"acme-corp",` +
		`"plan":"managed","plan_trial":false,` +
		`"created_at":"2026-01-01T00:00:00Z",` +
		`"updated_at":"2026-01-01T00:00:00Z"}]`

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", format)
			url := testsupport.NewAuthedServer(
				t, testsupport.JSONHandler(200, body))

			if err := runDoctor(rt, &conn.Flags{
				APIURL:       url,
				ClientID:     "id",
				ClientSecret: "secret",
			}); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			s := out.String()
			for _, want := range []string{"managed", "acme-corp"} {
				if !strings.Contains(s, want) {
					t.Errorf("%s output missing %q:\n%s", format, want, s)
				}
			}
		})
	}
}

// TestAccountDoctorTenantUnresolvedWithoutCredentials is the guard that
// the new check cannot break doctor's contract: it must still exit 0
// with no credentials, and must not dial anything to find that out.
func TestAccountDoctorTenantUnresolvedWithoutCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")

	if err := runDoctor(rt, &conn.Flags{
		APIURL: "http://127.0.0.1:1",
	}); err != nil {
		t.Fatalf("doctor must not fail without credentials: %v", err)
	}
	if !strings.Contains(out.String(), `"resolved":false`) {
		t.Errorf("expected tenant resolved false:\n%s", out.String())
	}
}
