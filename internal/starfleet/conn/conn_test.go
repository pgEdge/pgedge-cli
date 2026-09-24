package conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestResolveAPIURLPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		flag    string
		want    string
	}{
		{"default", "", "", DefaultAPIURL},
		{"profile", "https://p.example", "",
			"https://p.example"},
		{"flag beats profile", "https://p.example",
			"https://f.example", "https://f.example"},
		{"flag beats default", "", "https://f.example",
			"https://f.example"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsupport.ClearEnv(t)
			got := ResolveAPIURL(
				&config.StarfleetProfile{APIURL: tc.profile}, tc.flag)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A nil profile is tolerated even though Config.StarfleetProfile never
// returns one, so ResolveAPIURL is safe for callers holding a raw
// pointer.
func TestResolveAPIURLNilProfile(t *testing.T) {
	testsupport.ClearEnv(t)
	if got := ResolveAPIURL(nil, ""); got != DefaultAPIURL {
		t.Errorf("got %q, want %q", got, DefaultAPIURL)
	}
}

// The exchange must go through the generated operation and decode the
// spec's AccessToken shape.
func TestAuthenticatorExchange(t *testing.T) {
	testsupport.ClearEnv(t)
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	defer srv.Close()

	a := &Authenticator{APIURL: srv.URL, HTTPClient: srv.Client()}
	tok, err := a.Exchange(context.Background(), "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/account/v1/oauth/token" {
		t.Errorf("path = %q, want /account/v1/oauth/token", gotPath)
	}
	if !strings.Contains(gotBody, `"client_id":"id"`) {
		t.Errorf("body = %q, want the client_id", gotBody)
	}
	if !strings.Contains(gotBody, `"client_secret":"secret"`) {
		t.Errorf("body = %q, want the client_secret", gotBody)
	}
	if tok.AccessToken != "tok-123" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if d := time.Until(tok.ExpiresAt); d < 59*time.Minute ||
		d > 61*time.Minute {
		t.Errorf("ExpiresAt implies %v, want ~1h", d)
	}
}

func TestAuthenticatorExchangeNon200(t *testing.T) {
	testsupport.ClearEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"bad creds"}`))
		}))
	defer srv.Close()

	a := &Authenticator{APIURL: srv.URL, HTTPClient: srv.Client()}
	_, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	if !strings.Contains(err.Error(), "bad creds") {
		t.Errorf("error = %v, want the server body", err)
	}
}

// The server's explanation must reach the user even when the error
// body does not fit the spec's Error model. Error.code is an integer
// in openapi/account.yaml, so a body carrying a string code fails to
// unmarshal; the generated response parser would surrender the whole
// body and report only a Go unmarshal error. Exchange must not.
func TestAuthenticatorExchangeTypeMismatchedErrorBody(t *testing.T) {
	testsupport.ClearEnv(t)
	const body = `{"code":"invalid_client",` +
		`"message":"the secret you used was rotated"}`
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(body))
		}))
	defer srv.Close()

	a := &Authenticator{APIURL: srv.URL, HTTPClient: srv.Client()}
	_, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	if !strings.Contains(err.Error(),
		"the secret you used was rotated") {
		t.Errorf("error = %v, want the server's message", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want the HTTP status", err)
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("error = %v, leaked a Go unmarshal error", err)
	}
}

// A 200 that carries no access_token must be rejected outright. Left
// unguarded it would be cached for its full advertised TTL and every
// later call would send an empty bearer token.
func TestAuthenticatorExchangeEmptyAccessToken(t *testing.T) {
	testsupport.ClearEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token_type": "Bearer",
				"expires_in": 3600,
			})
		}))
	defer srv.Close()

	a := &Authenticator{APIURL: srv.URL, HTTPClient: srv.Client()}
	tok, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatalf("expected an error, got token %+v", tok)
	}
	if !strings.Contains(err.Error(), "no access_token") {
		t.Errorf("error = %v, want a no-access_token error", err)
	}
}

// An unparseable success body is a decode error, distinct from an
// empty token — and it must still name the endpoint, the status and
// the body. A proxy or SSO interstitial that intercepts the token
// endpoint answers 2xx with HTML, and a bare "invalid character '<'"
// gives the user nothing to act on.
func TestAuthenticatorExchangeUndecodableBody(t *testing.T) {
	testsupport.ClearEnv(t)
	tests := []struct {
		name        string
		contentType string
		body        string
		wantInBody  string
	}{
		{"json content type", "application/json",
			"not json at all", "not json at all"},
		{"proxy html interstitial", "text/html",
			"<html><body>Sign in to continue</body></html>",
			"Sign in to continue"},
		{"empty body", "application/json", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", tc.contentType)
					_, _ = w.Write([]byte(tc.body))
				}))
			defer srv.Close()

			a := &Authenticator{
				APIURL:     srv.URL,
				HTTPClient: srv.Client(),
			}
			_, err := a.Exchange(context.Background(), "id", "secret")
			if err == nil {
				t.Fatal("expected a decode error")
			}
			msg := err.Error()
			if !strings.Contains(msg, "decode token response") {
				t.Errorf("error = %v, want a decode error", err)
			}
			if !strings.Contains(msg, srv.URL) {
				t.Errorf("error = %v, want the endpoint URL", err)
			}
			if !strings.Contains(msg, "200 OK") {
				t.Errorf("error = %v, want the HTTP status", err)
			}
			if tc.wantInBody != "" &&
				!strings.Contains(msg, tc.wantInBody) {
				t.Errorf("error = %v, want the response body %q",
					err, tc.wantInBody)
			}
		})
	}
}

// tokenCanary is shaped like a real bearer token so that any path
// echoing a response body verbatim will show it. It is a literal, not
// a credential: the point is that it must NOT reach an error message.
const tokenCanary = "CANARY-TOKEN-abc.DEF.ghi"

// A 2xx body that fails to decode into AccessToken can still CARRY a
// live token: an OAuth implementation answering `expires_in` as a
// string or a float satisfies the wire format and fails Go's typed
// unmarshal. Echoing such a body would print a bearer token to
// stderr. The endpoint, the status and a diagnostic naming the field
// that actually failed must survive; the token value must not.
func TestAuthenticatorExchangeDecodeErrorRedactsToken(t *testing.T) {
	testsupport.ClearEnv(t)
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "2xx with a string expires_in",
			status: http.StatusOK,
			body: `{"access_token":"` + tokenCanary +
				`","expires_in":"3600","token_type":"Bearer"}`,
		},
		{
			name:   "2xx with a float expires_in",
			status: http.StatusOK,
			body: `{"access_token":"` + tokenCanary +
				`","expires_in":3600.5}`,
		},
		{
			name:   "2xx with the token nested under a wrapper",
			status: http.StatusOK,
			body: `{"data":{"access_token":"` + tokenCanary +
				`"},"expires_in":"3600"}`,
		},
		{
			name:   "2xx carrying only a refresh_token",
			status: http.StatusOK,
			body: `{"refresh_token":"` + tokenCanary +
				`","expires_in":"3600"}`,
		},
		{
			name:   "non-2xx echoing a token",
			status: http.StatusBadRequest,
			body:   `{"access_token":"` + tokenCanary + `"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
			defer srv.Close()

			a := &Authenticator{
				APIURL:     srv.URL,
				HTTPClient: srv.Client(),
			}
			_, err := a.Exchange(context.Background(), "id", "secret")
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			if strings.Contains(msg, tokenCanary) {
				t.Errorf("the token canary reached the error message:"+
					"\n%s", msg)
			}
			// httplog masks the credential's VALUE and leaves the rest
			// of the body readable, so the assertion is that the mask
			// is present — not that the whole body was replaced. That
			// is a strict improvement for this error path: the reader
			// now sees WHICH field was malformed instead of only a list
			// of key names.
			if !strings.Contains(msg, "████") {
				t.Errorf("error = %v, want the credential's value "+
					"masked", err)
			}
			// The diagnostic must stay actionable. Only the 2xx decode
			// branch names the endpoint — the non-2xx message has
			// always carried the status and body alone, and this fix
			// deliberately does not reshape it.
			if tc.status == http.StatusOK {
				if !strings.Contains(msg, srv.URL) {
					t.Errorf("error = %v, want the endpoint URL", err)
				}
				if !strings.Contains(msg, "200 OK") {
					t.Errorf("error = %v, want the HTTP status", err)
				}
				// The wrapped json error names the offending field,
				// which is the whole diagnostic value of this branch.
				if !strings.Contains(msg, "expires_in") {
					t.Errorf("error = %v, want the failing field named",
						err)
				}
			}
		})
	}
}

// The redaction must not swallow the case the echo exists for. A proxy
// or SSO interstitial answers 2xx with HTML, which does not parse as
// JSON, so its body must still reach the operator in full.
func TestAuthenticatorExchangeHTMLInterstitialStaysVisible(t *testing.T) {
	testsupport.ClearEnv(t)
	const page = "<html><body>Sign in to continue via SSO</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(page))
		}))
	defer srv.Close()

	a := &Authenticator{APIURL: srv.URL, HTTPClient: srv.Client()}
	_, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatal("expected a decode error")
	}
	msg := err.Error()
	if !strings.Contains(msg, page) {
		t.Errorf("error = %v, want the interstitial body verbatim", err)
	}
	if strings.Contains(msg, "redacted") {
		t.Errorf("error = %v, want no redaction for an HTML body", err)
	}
	if !strings.Contains(msg, "200 OK") ||
		!strings.Contains(msg, srv.URL) {
		t.Errorf("error = %v, want the status and the endpoint", err)
	}
}

// Neither error path may paste an entire document into stderr. The
// non-2xx path has echoed an unbounded body since before this package
// existed; the 2xx decode path inherited the same shape.
func TestAuthenticatorExchangeBoundsEchoedBody(t *testing.T) {
	testsupport.ClearEnv(t)
	huge := strings.Repeat("x", 200_000)
	tests := []struct {
		name   string
		status int
	}{
		{"2xx decode failure", http.StatusOK},
		{"non-2xx", http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(huge))
				}))
			defer srv.Close()

			a := &Authenticator{
				APIURL:     srv.URL,
				HTTPClient: srv.Client(),
			}
			_, err := a.Exchange(context.Background(), "id", "secret")
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			if len(msg) > 2000 {
				t.Errorf("error message is %d bytes, want it bounded",
					len(msg))
			}
			if !strings.Contains(msg, "truncated") {
				t.Errorf("error = %v, want it to say it truncated", err)
			}
		})
	}
}

// A nil HTTPClient must fall back to a working default rather than
// panicking, and a dead address must surface as a transport error.
func TestAuthenticatorExchangeTransportError(t *testing.T) {
	testsupport.ClearEnv(t)
	a := &Authenticator{APIURL: "http://127.0.0.1:1"}
	_, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if !strings.Contains(err.Error(), "token request") {
		t.Errorf("error = %v, want a token-request error", err)
	}
}

func TestAuthenticatorExchangeBadURL(t *testing.T) {
	testsupport.ClearEnv(t)
	a := &Authenticator{APIURL: "http://[::1", HTTPClient: http.DefaultClient}
	_, err := a.Exchange(context.Background(), "id", "secret")
	if err == nil {
		t.Fatal("expected an error for an unparseable base URL")
	}
	if !strings.Contains(err.Error(), "token request to http://[::1") {
		t.Errorf("error = %v, want the offending base URL named", err)
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v, want the URL-parse cause", err)
	}
}

// Resolve must reuse a cached token that is comfortably unexpired,
// making no network call at all.
func TestResolveUsesCachedToken(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// The profile's URL is unreachable on purpose: reusing the cache
	// must involve no dial at all. It is also the URL the seeded token
	// is bound to, since the binding covers the endpoint (#146).
	const profileURL = "http://127.0.0.1:1"

	a := &auth.Auth{Profile: "dev", Module: "starfleet"}
	if err := a.SaveToken(&auth.CachedToken{
		AccessToken: "cached-tok",
		ExpiresAt:   time.Now().Add(2 * time.Hour),
		Fingerprint: auth.Fingerprint(profileURL, "id", "secret"),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       profileURL, // would fail if dialled
		ClientID:     "id",
		ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "cached-tok" {
		t.Errorf("Token = %q, want the cached token", c.Token)
	}
	if c.APIURL != profileURL {
		t.Errorf("APIURL = %q, want the profile URL", c.APIURL)
	}
	if c.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://example.invalid", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BearerEditor(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer cached-tok" {
		t.Errorf("Authorization = %q", got)
	}
}

// A cached token inside the refresh window must be discarded and
// replaced by a fresh exchange, and the fresh token must land in the
// cache.
func TestResolveRefreshesStaleToken(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fresh-tok",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	defer srv.Close()

	store := &auth.Auth{Profile: "dev", Module: "starfleet"}
	if err := store.SaveToken(&auth.CachedToken{
		AccessToken: "stale-tok",
		ExpiresAt:   time.Now().Add(time.Minute),
		Fingerprint: auth.Fingerprint(srv.URL, "id", "secret"),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       srv.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "fresh-tok" {
		t.Errorf("Token = %q, want the freshly exchanged token", c.Token)
	}
	if calls != 1 {
		t.Errorf("token endpoint hit %d times, want 1", calls)
	}
	cached, err := store.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if cached.AccessToken != "fresh-tok" {
		t.Errorf("cached token = %q, want the fresh one",
			cached.AccessToken)
	}
}

// A cache file that is not valid JSON must be treated as "no token":
// exactly one exchange, and the real token served. Guards against a
// future LoadToken that returns a zero CachedToken instead of an
// error on bad JSON, which would have Resolve serve an empty token
// with nothing failing.
func TestResolveIgnoresCorruptCache(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cacheDir := filepath.Join(home, ".pgedge", "cli", "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(cacheDir, "dev-starfleet.json")
	if err := os.WriteFile(cacheFile,
		[]byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "recovered-tok",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       srv.URL,
		ClientID:     "id",
		ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "recovered-tok" {
		t.Errorf("Token = %q, want the freshly exchanged token", c.Token)
	}
	if calls != 1 {
		t.Errorf("token endpoint hit %d times, want 1", calls)
	}
	// The corrupt file must have been replaced, not left to fail again.
	cached, err := Store(rt).LoadToken()
	if err != nil {
		t.Fatalf("cache still unreadable after recovery: %v", err)
	}
	if cached.AccessToken != "recovered-tok" {
		t.Errorf("cached = %q, want recovered-tok", cached.AccessToken)
	}
}

// seedCache writes a token cache file directly with os.WriteFile
// rather than through auth.Auth.SaveToken, so a "legacy" fixture (no
// binding fingerprint field at all) can exist even though SaveToken
// itself now refuses to write one. Passing both id and secret empty
// produces exactly that legacy shape; anything else stamps the
// Fingerprint for that (apiURL, id, secret) triple.
func seedCache(t *testing.T, path, apiURL, id, secret string,
	ttl time.Duration) {
	t.Helper()
	tok := &auth.CachedToken{
		AccessToken: "seeded-tok",
		ExpiresAt:   time.Now().Add(ttl),
	}
	if id != "" || secret != "" {
		tok.Fingerprint = auth.Fingerprint(apiURL, id, secret)
	}
	raw, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestResolveCredentialBinding is the Task 2 table from the plan: the
// hot path (token()) must re-exchange exactly when the cached token
// was not minted for the connection that would be used now, and never
// otherwise. Every re-exchange case also confirms the new cache file
// left behind is bound to the connection that just minted it, and the
// "unchanged" case confirms the seeded token — not a fresh one — comes
// back untouched.
//
// "Connection" is credential AND endpoint since #146, so the seed
// callback is handed the profile's own URL and the table carries a
// flagAPIURL column. The other-host case runs against a SECOND stub,
// and the per-stub counters are what distinguish "re-exchanged"
// from "re-exchanged against the right host" — a single shared counter
// would score a token minted by the wrong server as a pass.
func TestResolveCredentialBinding(t *testing.T) {
	testsupport.ClearEnv(t)
	const configID, configSecret = "id", "secret"

	cases := []struct {
		name string
		// seed receives the cache path and the profile's API URL, the
		// endpoint a correctly-bound fixture must name.
		seed          func(t *testing.T, cacheFile, apiURL string)
		flagID        string
		flagSecret    string
		useOtherHost  bool
		wantExchanges int
	}{
		{
			name: "unchanged connection reuses the cache",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, configID, configSecret, 2*time.Hour)
			},
			wantExchanges: 0,
		},
		{
			name: "rekey forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, configID, "old-secret", 2*time.Hour)
			},
			wantExchanges: 1,
		},
		{
			name: "different id forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, "other-id", configSecret, 2*time.Hour)
			},
			wantExchanges: 1,
		},
		{
			name: "legacy unbound cache forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, "", "", 2*time.Hour)
			},
			wantExchanges: 1,
		},
		{
			name: "corrupt cache forces a fresh exchange",
			seed: func(t *testing.T, p, _ string) {
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantExchanges: 1,
		},
		{
			name: "within refresh window forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, configID, configSecret, time.Minute)
			},
			wantExchanges: 1,
		},
		{
			name: "flag override forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, configID, configSecret, 2*time.Hour)
			},
			flagID:        "flag-id",
			flagSecret:    "flag-secret",
			wantExchanges: 1,
		},
		{
			// #146: identical credentials, different endpoint. The
			// seed is bound to the profile's host and is otherwise
			// perfectly reusable — the first row of this table proves
			// that — so only the URL can account for the re-exchange.
			name: "another api url forces a fresh exchange",
			seed: func(t *testing.T, p, apiURL string) {
				seedCache(t, p, apiURL, configID, configSecret, 2*time.Hour)
			},
			useOtherHost:  true,
			wantExchanges: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			srv := newTokenStub(t, "fresh-tok")
			other := newTokenStub(t, "other-host-tok")

			cfg := &config.Config{}
			cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
				APIURL:       srv.url(),
				ClientID:     configID,
				ClientSecret: configSecret,
			})
			rt := &module.Runtime{
				Config:  cfg,
				Profile: "dev",
				Output:  &output.Renderer{Format: "table"},
				Stderr:  io.Discard,
			}

			cacheFile := filepath.Join(
				home, ".pgedge", "cli", "cache", "dev-starfleet.json")
			tc.seed(t, cacheFile, srv.url())

			// wantURL is both the URL passed as --api-url and the one
			// the resulting cache entry must be bound to.
			flagAPIURL, wantURL, wantToken := "", srv.url(), "fresh-tok"
			if tc.useOtherHost {
				flagAPIURL = other.url()
				wantURL, wantToken = other.url(), "other-host-tok"
			}

			c, err := Resolve(rt, tc.flagID, tc.flagSecret, flagAPIURL, RequestTimeout)
			if err != nil {
				t.Fatal(err)
			}

			// Count the exchanges at the host that was supposed to
			// answer, and assert the other host was left alone.
			got, idle := srv.hits(), other.hits()
			if tc.useOtherHost {
				got, idle = other.hits(), srv.hits()
			}
			if got != int64(tc.wantExchanges) {
				t.Errorf("token endpoint hit %d times, want %d",
					got, tc.wantExchanges)
			}
			if idle != 0 {
				t.Errorf("the host that was not configured was dialled "+
					"%d times, want 0", idle)
			}

			if tc.wantExchanges == 0 {
				if c.Token != "seeded-tok" {
					t.Errorf("Token = %q, want the seeded token reused "+
						"unchanged", c.Token)
				}
				return
			}
			// The returned token must be the answering stub's, proving
			// the cached/seeded value was rejected rather than reused.
			if c.Token != wantToken {
				t.Errorf("Token = %q, want %q — the token freshly "+
					"exchanged with the host being dialled, not the "+
					"seeded one", c.Token, wantToken)
			}
			wantID, wantSecret := configID, configSecret
			if tc.flagID != "" {
				wantID, wantSecret = tc.flagID, tc.flagSecret
			}
			cached, err := (&auth.Auth{
				Profile: "dev", Module: "starfleet"}).LoadToken()
			if err != nil {
				t.Fatalf("reload cache after re-exchange: %v", err)
			}
			if !cached.MintedBy(wantURL, wantID, wantSecret) {
				t.Errorf("new cache file is not bound to the connection " +
					"that just minted it (fingerprint mismatch)")
			}
		})
	}
}

// tokenStub is a stand-in for one Starfleet endpoint's token endpoint. It
// answers every token exchange with a token naming itself, and counts
// the exchanges it was asked for, so a test can assert not just "a
// token was minted" but "minted by THIS host and not that one".
//
// The counter is atomic because the handler runs on net/http's own
// goroutine; the assertions here are about who was dialled, and a
// data race in the instrument would make that answer unreliable
// exactly when the test is telling the truth.
type tokenStub struct {
	srv      *httptest.Server
	token    string
	requests atomic.Int64
}

func newTokenStub(t *testing.T, token string) *tokenStub {
	t.Helper()
	s := &tokenStub{token: token}
	s.srv = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			s.requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": s.token,
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *tokenStub) url() string { return s.srv.URL }

func (s *tokenStub) hits() int64 { return s.requests.Load() }

// TestResolveDoesNotReplayACachedTokenToADifferentHost is issue #146.
//
// The cached token is a live bearer credential minted by one
// endpoint's auth server. Before this fix the cache hit was decided on
// expiry and the credential fingerprint alone, so `--api-url
// <otherhost>` with unchanged credentials kept the cached token and
// sent it, in an Authorization header, to a host that never minted it.
//
// The test is built around its own positive control, because the
// dangerous way to write it is one that passes for the wrong reason: a
// fixture that is not actually reusable would show "a fresh exchange
// happened" in step 2 whether or not the URL is bound. Step 1
// therefore proves the cache IS reused when nothing changes, so the
// re-exchange in step 2 can only be attributable to the URL.
func TestResolveDoesNotReplayACachedTokenToADifferentHost(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	origin := newTokenStub(t, "origin-tok")
	other := newTokenStub(t, "other-tok")

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       origin.url(),
		ClientID:     "id",
		ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	// Step 0 — cold cache: one exchange against the profile's host.
	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "origin-tok" {
		t.Fatalf("Token = %q, want the origin host's token", c.Token)
	}
	if origin.hits() != 1 {
		t.Fatalf("origin exchanges = %d, want 1", origin.hits())
	}

	// Step 1 — the positive control. Nothing changed, so the cache
	// must be reused with no exchange at all. Without this, step 2
	// proves nothing.
	if c, err = Resolve(rt, "", "", "", RequestTimeout); err != nil {
		t.Fatal(err)
	}
	if c.Token != "origin-tok" {
		t.Fatalf("Token = %q, want the cached origin token reused", c.Token)
	}
	if origin.hits() != 1 {
		t.Fatalf("origin exchanges = %d after an unchanged resolve, "+
			"want the cache reused (1)", origin.hits())
	}

	// Step 2 — the bug. Same credentials, different host: the cached
	// token must NOT be handed to the other host.
	c, err = Resolve(rt, "", "", other.url(), RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token == "origin-tok" {
		t.Errorf("Token = %q: the origin host's bearer token was "+
			"replayed to %s", c.Token, other.url())
	}
	if c.Token != "other-tok" {
		t.Errorf("Token = %q, want a token freshly minted by the "+
			"host being dialled", c.Token)
	}
	if other.hits() != 1 {
		t.Errorf("other-host exchanges = %d, want 1 — the CLI must "+
			"authenticate against the host it is about to call",
			other.hits())
	}
	if origin.hits() != 1 {
		t.Errorf("origin exchanges = %d, want 1 — the override must "+
			"not dial the profile's host", origin.hits())
	}

	// Step 3 — the new cache is bound to the host that minted it, so
	// repeating the override reuses rather than re-exchanging. This is
	// what keeps the fix from degenerating into "never cache".
	if c, err = Resolve(rt, "", "", other.url(), RequestTimeout); err != nil {
		t.Fatal(err)
	}
	if c.Token != "other-tok" {
		t.Errorf("Token = %q, want the cached other-host token", c.Token)
	}
	if other.hits() != 1 {
		t.Errorf("other-host exchanges = %d after a repeat override, "+
			"want the cache reused (1)", other.hits())
	}

	// Step 4 — and the binding is symmetric: dropping back to the
	// profile's host must not replay the other host's token either.
	if c, err = Resolve(rt, "", "", "", RequestTimeout); err != nil {
		t.Fatal(err)
	}
	if c.Token != "origin-tok" {
		t.Errorf("Token = %q, want a token minted by the origin host",
			c.Token)
	}
	if origin.hits() != 2 {
		t.Errorf("origin exchanges = %d, want 2 — returning to the "+
			"profile's host must re-authenticate against it",
			origin.hits())
	}
}

// --api-url and --client-id/--client-secret must beat the profile, and
// the exchange must carry the flag credentials.
func TestResolveFlagOverrides(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "flag-tok",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       "http://127.0.0.1:1",
		ClientID:     "cfg-id",
		ClientSecret: "cfg-secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "flag-id", "flag-secret", srv.URL, RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIURL != srv.URL {
		t.Errorf("APIURL = %q, want the flag URL %q", c.APIURL, srv.URL)
	}
	if c.Token != "flag-tok" {
		t.Errorf("Token = %q", c.Token)
	}
	if !strings.Contains(gotBody, `"client_id":"flag-id"`) {
		t.Errorf("body = %q, want the flag client_id", gotBody)
	}
}

func TestResolveNoCredentialsIsAuthExit(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	rt := &module.Runtime{
		Config:  &config.Config{},
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}
	_, err := Resolve(rt, "", "", "", RequestTimeout)
	if err == nil {
		t.Fatal("expected an error with no credentials")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitAuth {
		t.Errorf("err = %v, want ExitError with ExitAuth", err)
	}
}

// TestResolveHalfSuppliedFlagPairDoesNotFallBack is the end-to-end half
// of the credential-flag pairing rule. --client-id alone used to be
// discarded in silence and the next source consulted instead, so a
// command typed with the wrong ID authenticated as whoever that source
// named and reported success. A full, working pair sits in rt.Config
// here; the stub would answer a token request using it, and the
// assertion that it was never called is what proves the resolver
// stopped rather than fell through to config.
func TestResolveHalfSuppliedFlagPairDoesNotFallBack(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var tokenCalls int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"access_token":"leaked","expires_in":3600}`))
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		ClientID:     "cfg-id",
		ClientSecret: "cfg-secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	_, err := Resolve(rt, "only-an-id", "", srv.URL, RequestTimeout)
	if err == nil {
		t.Fatal("want an error for --client-id without --client-secret")
	}
	// ExitUsage, not ExitAuth: a half-supplied flag pair is the operator
	// mistyping the command, and the reference has always documented it
	// as "usage error (exit 2)". This assertion said ExitAuth and passed,
	// because ExitAuth was also 2 — the collision made a mis-tagged error
	// indistinguishable from a correctly tagged one, and moving auth to 5
	// is what exposed it.
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("err = %v (code %d), want ExitError with ExitUsage (%d)",
			err, ee.Code(), ExitUsage)
	}
	if !strings.Contains(err.Error(), "--client-secret") {
		t.Errorf("err = %v, should name the missing flag", err)
	}
	if tokenCalls != 0 {
		t.Errorf("token endpoint called %d times, want 0 — the "+
			"half-supplied pair fell through to the profile config",
			tokenCalls)
	}
}

// A failed exchange is an auth-class failure, not a generic one.
func TestResolveExchangeFailureIsAuthExit(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		ClientID: "id", ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}
	_, err := Resolve(rt, "", "", srv.URL, RequestTimeout)
	if err == nil {
		t.Fatal("expected an error when the exchange fails")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitAuth {
		t.Fatalf("err = %v, want ExitError with ExitAuth", err)
	}
	if !strings.Contains(ee.Error(), "authentication failed") {
		t.Errorf("message = %q, want the authentication-failed prefix",
			ee.Error())
	}
}

func TestLoginCachesToken(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "login-tok",
				"token_type":   "Bearer",
				"expires_in":   7200,
			})
		}))
	defer srv.Close()

	rt := &module.Runtime{
		Config:  &config.Config{},
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}
	tok, err := Login(context.Background(), rt, srv.URL, "id", "secret", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "login-tok" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}

	cached, err := Store(rt).LoadToken()
	if err != nil {
		t.Fatalf("token was not cached: %v", err)
	}
	if cached.AccessToken != "login-tok" {
		t.Errorf("cached = %q, want login-tok", cached.AccessToken)
	}
}

func TestLoginExchangeError(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	rt := &module.Runtime{
		Config:  &config.Config{},
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}
	_, err := Login(context.Background(), rt, "http://127.0.0.1:1", "id", "secret", RequestTimeout)
	if err == nil {
		t.Fatal("expected an error from an unreachable endpoint")
	}
	if !strings.Contains(err.Error(),
		"token request to http://127.0.0.1:1") {
		t.Errorf("error = %v, want the endpoint named", err)
	}
	if _, err := Store(rt).LoadToken(); err == nil {
		t.Error("a failed exchange must not write a cache entry")
	}
}

// An invalid profile name cannot produce a cache path, so the save
// must fail rather than write outside the cache directory.
func TestLoginCacheWriteError(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok",
				"token_type":   "Bearer",
				"expires_in":   60,
			})
		}))
	defer srv.Close()

	rt := &module.Runtime{
		Config:  &config.Config{},
		Profile: "../escape",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}
	_, err := Login(context.Background(), rt, srv.URL, "id", "secret", RequestTimeout)
	if err == nil {
		t.Fatal("expected an error for an unsafe profile name")
	}
	if !strings.Contains(err.Error(), `invalid profile "../escape"`) {
		t.Errorf("error = %v, want the rejected profile named", err)
	}
	// Nothing may have been written anywhere under HOME — in
	// particular not outside the cache directory the name tried to
	// escape.
	var written []string
	if walkErr := filepath.WalkDir(home,
		func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				written = append(written, p)
			}
			return nil
		}); walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(written) != 0 {
		t.Errorf("files written despite the error: %v", written)
	}
}

func TestStoreUsesStarfleetModule(t *testing.T) {
	testsupport.ClearEnv(t)
	rt := &module.Runtime{Profile: "dev"}
	s := Store(rt)
	if s.Profile != "dev" || s.Module != "starfleet" {
		t.Errorf("Store = %+v, want profile dev module cloud", s)
	}
}

// logoutRuntime is the fixture the Logout tests share: an isolated
// HOME, a cleared environment, and a runtime whose warnings are
// captured rather than printed.
func logoutRuntime(t *testing.T) (*module.Runtime, *strings.Builder) {
	t.Helper()
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	var errb strings.Builder
	return &module.Runtime{
		Config:  &config.Config{},
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  &errb,
	}, &errb
}

// cachePaths returns the current cache file path and a superseded one
// that Logout no longer touches, for the "dev" profile under the test's
// isolated HOME.
func cachePaths(t *testing.T) (current, legacy string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli", "cache")
	return filepath.Join(dir, "dev-starfleet.json"),
		filepath.Join(dir, "dev-account.json")
}

// writeCache plants a token cache file at p, creating its directory.
func writeCache(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(&auth.CachedToken{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Logout clears the current <profile>-starfleet.json.
func TestLogoutClearsCurrentCache(t *testing.T) {
	rt, errb := logoutRuntime(t)
	current, _ := cachePaths(t)
	writeCache(t, current)

	if err := Logout(rt); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := os.Stat(current); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dev-starfleet.json survived logout: %v", err)
	}
	if errb.String() != "" {
		t.Errorf("unexpected warning: %q", errb.String())
	}
}

// TestLogoutLeavesTheLegacyCacheAlone records what each cache-key
// rename gives up, so it is a stated consequence rather than a
// surprise. Logout deletes exactly one file, the current
// <profile>-starfleet.json; a token cached under a superseded key —
// <profile>-account.json, from before the cloud merge — is written by
// nothing, cleaned by nothing, and survives on a machine that ran an
// older build until it expires or someone removes it by hand.
//
// It is asserted rather than merely noted because the alternative — a
// silent behaviour change around a credential-bearing file — is how a
// leftover token stops being anybody's job. `rm` on the file is the
// whole fix, and it belongs to the operator, not to logout.
func TestLogoutLeavesTheLegacyCacheAlone(t *testing.T) {
	rt, errb := logoutRuntime(t)
	current, legacy := cachePaths(t)
	writeCache(t, current)
	writeCache(t, legacy)

	if err := Logout(rt); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := os.Stat(current); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dev-starfleet.json survived logout: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("dev-account.json: %v — logout is not expected to remove "+
			"it, but it must not fail on it either", err)
	}
	if errb.String() != "" {
		t.Errorf("unexpected warning: %q", errb.String())
	}
}

// No cache present at all: logout on a profile that was never signed in
// is a no-op, not an error.
func TestLogoutSucceedsWithNoCaches(t *testing.T) {
	rt, errb := logoutRuntime(t)
	if err := Logout(rt); err != nil {
		t.Fatalf("Logout with no caches: %v", err)
	}
	if errb.String() != "" {
		t.Errorf("unexpected warning: %q", errb.String())
	}
}

// The account cache failing to clear is a real failure and must
// propagate: logout cannot claim success while the token it exists to
// remove is still readable.
func TestLogoutPropagatesCurrentCacheError(t *testing.T) {
	rt, _ := logoutRuntime(t)
	// An unresolvable profile makes cachePath fail for both handles.
	rt.Profile = ".."
	if err := Logout(rt); err == nil {
		t.Fatal("want error when the account cache cannot be cleared")
	}
}

// TestHTTPClientFor pins the wiring from the global flags to a level.
// The wire format and the redaction rules are internal/httplog's to
// test; what belongs here is that each flag combination reaches the
// right level, and that the quiet case allocates nothing.
func TestHTTPClientFor(t *testing.T) {
	tests := []struct {
		name    string
		verbose bool
		debug   bool
		want    httplog.Level
	}{
		{name: "neither flag", want: httplog.Off},
		{name: "verbose", verbose: true, want: httplog.Verbose},
		// --debug alone must reach Debug: it implies --verbose, so a
		// user who passes only --debug gets the superset, not nothing.
		{name: "debug alone", debug: true, want: httplog.Debug},
		{name: "both", verbose: true, debug: true, want: httplog.Debug},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testsupport.ClearEnv(t)
			rt := &module.Runtime{
				Verbose: tt.verbose, Debug: tt.debug, Stderr: io.Discard,
			}
			c := HTTPClientFor(rt, RequestTimeout)
			// The error-body repair is unconditional (issue #140), so
			// it is always the outermost layer here and there is no
			// longer a plain-client case. httplog still adds no layer
			// of its own when Off, so what sits underneath the repair
			// is what distinguishes the levels.
			eb, ok := c.Transport.(*errbody.Transport)
			if !ok {
				t.Fatalf("transport = %T, want *errbody.Transport",
					c.Transport)
			}
			if tt.want == httplog.Off {
				if _, logging := eb.Base.(*httplog.Transport); logging {
					t.Error("a quiet runtime installed the http logger")
				}
				return
			}
			tr, ok := eb.Base.(*httplog.Transport)
			if !ok {
				t.Fatalf("transport under the repair = %T, want "+
					"*httplog.Transport", eb.Base)
			}
			if tr.Level != tt.want {
				t.Errorf("level = %v, want %v", tr.Level, tt.want)
			}
		})
	}
}

// TestHTTPClientForReachesStderr is the end-to-end half: the client
// HTTPClientFor builds must actually write to the runtime's stderr, and
// must not print the bearer token. A level plumbed correctly into a
// transport that writes nowhere would satisfy the table above.
func TestHTTPClientForReachesStderr(t *testing.T) {
	testsupport.ClearEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"plan_expires_at":null}`)
		}))
	defer srv.Close()

	var buf strings.Builder
	rt := &module.Runtime{Debug: true, Stderr: &buf}
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer super-secret")
	resp, err := HTTPClientFor(rt, RequestTimeout).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	log := buf.String()
	// The response body is the point of --debug over --verbose, and an
	// explicit null is the payload question that motivated the flag.
	if !strings.Contains(log, `"plan_expires_at":null`) {
		t.Errorf("log = %q, want the response body", log)
	}
	if strings.Contains(log, "super-secret") {
		t.Errorf("log = %q, leaked the bearer token", log)
	}
}

func TestNewExitError(t *testing.T) {
	testsupport.ClearEnv(t)
	err := NewExitError("boom", ExitTimeout)
	if err.Error() != "boom" {
		t.Errorf("Error() = %q, want boom", err.Error())
	}
	if err.Code() != ExitTimeout {
		t.Errorf("Code() = %d, want %d", err.Code(), ExitTimeout)
	}
}

// TestExitCodeValues pins the numbers themselves, because they are a
// user-facing contract that scripts branch on. Named pairs rather than
// two parallel slices: the old version compared got[i] to want[i] and
// reported "exit code 2 = 5", where "2" was a slice index and not a code,
// which is close to the most confusing thing it could have said about a
// list of exit codes.
//
// 2 is ExitUsage and auth is 5. See the constant block in conn.go for
// why they are that way round.
func TestExitCodeValues(t *testing.T) {
	testsupport.ClearEnv(t)
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"ExitOK", ExitOK, 0},
		{"ExitGeneral", ExitGeneral, 1},
		{"ExitUsage", ExitUsage, 2},
		{"ExitTimeout", ExitTimeout, 3},
		{"ExitNotFound", ExitNotFound, 4},
		{"ExitAuth", ExitAuth, 5},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	// The cross-package agreement (conn vs internal/cli vs
	// internal/controlplane/cmd) is gated in internal/clitest, not here: internal/cli
	// imports this package, so importing it back from this internal test
	// file would be an import cycle.
}

func TestCheckResponse(t *testing.T) {
	testsupport.ClearEnv(t)
	// body defaults to "body" for every row except 404: a bare,
	// code-less string is no longer a resource miss (issue #101), so
	// the 404 row needs a realistic handler-shaped body to still
	// exercise ExitNotFound here.
	tests := []struct {
		status int
		body   string
		want   int // 0 == expect nil error
	}{
		{200, "body", 0}, {204, "body", 0}, {299, "body", 0},
		{401, "body", ExitAuth}, {403, "body", ExitAuth},
		{404, `{"code":404,"message":"body"}`, ExitNotFound},
		{408, "body", ExitTimeout}, {504, "body", ExitTimeout},
		{409, "body", ExitGeneral},
		{400, "body", ExitGeneral}, {500, "body", ExitGeneral},
	}
	for _, tc := range tests {
		err := CheckResponse(tc.status, tc.body)
		if tc.want == 0 {
			if err != nil {
				t.Errorf("status %d: got %v, want nil",
					tc.status, err)
			}
			continue
		}
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != tc.want {
			t.Errorf("status %d: got %v, want code %d",
				tc.status, err, tc.want)
		}
	}
}

// The message must carry both the status and the server body so the
// user can see what the API actually said.
func TestCheckResponseMessages(t *testing.T) {
	testsupport.ClearEnv(t)
	// body defaults to "detail" for every row except 404: a bare,
	// code-less string is no longer a resource miss (issue #101), so
	// the 404 row needs a realistic handler-shaped body to still
	// exercise the resource-not-found message here.
	tests := []struct {
		status int
		body   string
		want   string
	}{
		{401, "detail", "authentication error (401): detail"},
		{404, `{"code":404,"message":"detail"}`,
			`resource not found (404): {"code":404,"message":"detail"}`},
		{408, "detail", "request timed out (408): detail"},
		{500, "detail", "API error (500): detail"},
	}
	for _, tc := range tests {
		err := CheckResponse(tc.status, tc.body)
		if err == nil {
			t.Fatalf("status %d: got nil error", tc.status)
		}
		if err.Error() != tc.want {
			t.Errorf("status %d: got %q, want %q",
				tc.status, err.Error(), tc.want)
		}
	}
}

// TestCheckResponseConflictNamesTheBusyDatabase pins the 409 branch.
//
// A 409 used to fall through to the default and read "API error
// (409)", indistinguishable from a 500. saas's managed transitions
// answer it from a lost compare-and-swap reservation, so
// it is the one status that means "retry shortly", and the message is
// the only place that can say so — the exit code stays ExitGeneral
// because the contract is pinned at 0/1/2/3/4/5.
//
// The assertions are on the words a user acts on, not the whole
// sentence, so a rewording that keeps the meaning does not fail. The
// negative assertion is the one that catches a regression to the
// default branch.
func TestCheckResponseConflictNamesTheBusyDatabase(t *testing.T) {
	testsupport.ClearEnv(t)

	body := `{"code":409,"message":"database is busy"}`
	err := CheckResponse(http.StatusConflict, body)
	if err == nil {
		t.Fatal("a 409 produced no error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
		t.Fatalf("want ExitGeneral, got %v", err)
	}

	msg := err.Error()
	for _, want := range []string{"busy", "retry", "409"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the 409 message does not carry %q: %q",
				want, msg)
		}
	}
	if !strings.Contains(msg, body) {
		t.Errorf("the 409 message drops the server body: %q", msg)
	}
	if strings.Contains(msg, "API error (409)") {
		t.Errorf("the 409 fell back to the default branch: %q", msg)
	}
}

// TestCheckResponseConflictOnBranchesDoesNotSayWait pins the one 409
// family that "wait and retry" gets wrong. The bodies are the live
// responses devapi gave on 2026-09-18 to a sixth branch create, to a
// database delete without --delete-branches, and (by construction from
// the same producer file) to a resize; the branch never settles, so a
// user told to wait would wait forever. The two control rows are 409s
// that DO settle and must keep the busy guidance.
func TestCheckResponseConflictOnBranchesDoesNotSayWait(t *testing.T) {
	testsupport.ClearEnv(t)

	const prefix = `{"code":409,"message":"`
	tests := []struct {
		name     string
		message  string
		wantBusy bool
	}{
		{
			name: "branch limit",
			message: "the database has 5 of 5 branches; deleting a " +
				"branch frees its slot",
		},
		{
			name: "database delete with branches",
			message: "the database has 4 branches; delete them first " +
				"or repeat with force=true to delete them with the database",
		},
		{
			name: "database delete with one branch",
			message: "the database has 1 branch; delete it first " +
				"or repeat with force=true to delete it with the database",
		},
		{
			name: "resize with branches",
			message: "resize is unavailable while the database has " +
				"2 branches; delete them first",
		},
		{
			name: "branch delete mid-create",
			message: "the branch is still being created; retry once " +
				"it is available",
			wantBusy: true,
		},
		{
			name: "cascade delete over a branch mid-create",
			message: "branch fc27c2e4-91ba-463c-b441-8706eefad620 is " +
				"still being created; retry once it settles",
			wantBusy: true,
		},
		{
			name: "resize on a busy database",
			message: "resize requires the database to be available; " +
				"it is busy with another operation",
			wantBusy: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := prefix + tt.message + `"}`
			err := CheckResponse(http.StatusConflict, body)
			if err == nil {
				t.Fatal("a 409 produced no error")
			}
			msg := err.Error()
			gotBusy := strings.Contains(msg, "resource is busy")
			if gotBusy != tt.wantBusy {
				t.Errorf("classified as busy = %v, want %v\nrendered: %s",
					gotBusy, tt.wantBusy, msg)
			}
			if !tt.wantBusy {
				for _, want := range []string{"branch", "--delete-branches"} {
					if !strings.Contains(msg, want) {
						t.Errorf("the branch refusal does not carry %q: %s",
							want, msg)
					}
				}
				if strings.Contains(msg, "Wait for it") {
					t.Errorf("the branch refusal tells the user to wait: %s",
						msg)
				}
			}
			if !strings.Contains(msg, tt.message) {
				t.Errorf("the server's message did not survive: %s", msg)
			}
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code() != ExitGeneral {
				t.Errorf("want ExitGeneral, got %v", err)
			}
		})
	}
}

// A 404 whose body is saas's router-level not-found — the endpoint is
// not served at all — must not read as "the resource does not exist":
// exit 1 with a message naming the real cause, not ExitNotFound.
// Verified against devapi 2026-08-06: an unregistered path answers
// exactly `{"message":"Not Found"}` plus a trailing newline, while a
// genuine resource miss answers a specific JSON error such as
// `{"code":404,"message":"managed database not found"}`.
func TestCheckResponseRouteMiss(t *testing.T) {
	testsupport.ClearEnv(t)
	for _, body := range []string{
		`{"message":"Not Found"}`,
		"{\"message\":\"Not Found\"}\n",
	} {
		err := CheckResponse(404, body)
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("body %q: got %v, want *ExitError", body, err)
		}
		if ee.Code() != ExitGeneral {
			t.Errorf("body %q: code = %d, want %d (route miss is not "+
				"a resource miss)", body, ee.Code(), ExitGeneral)
		}
		const phrase = "does not serve an endpoint this command needs"
		if !strings.Contains(ee.Error(), phrase) {
			t.Errorf("body %q: message %q does not contain %q",
				body, ee.Error(), phrase)
		}
	}
}

// TestCheckResponse404Classification is the edge matrix from the
// route-miss discriminator plan (issue #101): a 404 body is a route
// miss unless it parses as a JSON object carrying a "code" field.
// This is the table that a reverted byte-exact discriminator gets
// wrong (rows named "reworded byte-exact miss" below) and that an
// over-broad "any 404 is a route miss" rule also gets wrong (rows
// named "resource miss" below) — see Task 5's mutation proof.
func TestCheckResponse404Classification(t *testing.T) {
	testsupport.ClearEnv(t)
	const phrase = "does not serve an endpoint this command needs"
	tests := []struct {
		name       string
		body       string
		wantCode   int
		wantPhrase string // empty means "this is a resource miss"
	}{
		{
			name:       "exact router miss",
			body:       `{"message":"Not Found"}`,
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "exact router miss with trailing newline",
			body:       "{\"message\":\"Not Found\"}\n",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: re-spaced",
			body:       `{"message": "Not Found"}`,
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: pretty-printed",
			body:       "{\n  \"message\": \"Not Found\"\n}",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: html interstitial",
			body:       "<html>x</html>",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: nginx plain text",
			body:       "Not Found",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: empty body",
			body:       "",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:     "resource miss: standard handler error",
			body:     `{"code":404,"message":"managed database not found"}`,
			wantCode: ExitNotFound,
		},
		{
			name:     "resource miss: with reason field",
			body:     `{"code":404,"message":"x","reason":"plan_expired"}`,
			wantCode: ExitNotFound,
		},
		{
			name:       "reworded byte-exact miss: message without code",
			body:       `{"message":"not found"}`,
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:     "resource miss: content-type mislabelled",
			body:     `{"code":404,"message":"x"}`,
			wantCode: ExitNotFound,
		},
		{
			name:     "resource miss: code present but zero",
			body:     `{"code":0,"message":"x"}`,
			wantCode: ExitNotFound,
		},
		{
			name:       "reworded byte-exact miss: JSON null",
			body:       "null",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: cp mux body",
			body:       "404 page not found",
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
		{
			name:       "reworded byte-exact miss: JSON array",
			body:       `[{"code":404}]`,
			wantCode:   ExitGeneral,
			wantPhrase: phrase,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckResponse(404, tc.body)
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("body %q: got %v, want *ExitError", tc.body, err)
			}
			if ee.Code() != tc.wantCode {
				t.Errorf("body %q: code = %d, want %d", tc.body,
					ee.Code(), tc.wantCode)
			}
			if tc.wantPhrase == "" {
				want := fmt.Sprintf("resource not found (404): %s", tc.body)
				if ee.Error() != want {
					t.Errorf("body %q: message = %q, want %q", tc.body,
						ee.Error(), want)
				}
				return
			}
			if !strings.Contains(ee.Error(), tc.wantPhrase) {
				t.Errorf("body %q: message %q does not contain %q",
					tc.body, ee.Error(), tc.wantPhrase)
			}
		})
	}
}

// The route-miss message must echo enough of the body to recognise
// what answered — including an HTML interstitial — but bounded, so a
// large proxy error page never lands whole on stderr.
func TestCheckResponseRouteMissEchoesBoundedBody(t *testing.T) {
	testsupport.ClearEnv(t)

	err := CheckResponse(404, "<html>x</html>")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v, want *ExitError", err)
	}
	if !strings.Contains(ee.Error(), "<html>x</html>") {
		t.Errorf("message %q does not echo the body", ee.Error())
	}

	long := strings.Repeat("a", 600)
	err = CheckResponse(404, long)
	if !errors.As(err, &ee) {
		t.Fatalf("got %v, want *ExitError", err)
	}
	if strings.Contains(ee.Error(), long) {
		t.Errorf("message echoed the full 600-char body untruncated")
	}
	if !strings.Contains(ee.Error(), "truncated") {
		t.Errorf("message %q does not indicate truncation", ee.Error())
	}
}

// A 400 whose body is saas's plan-entitlement denial must render
// cleanly as ExitAuth rather than the raw "API error (400): ..."
// passthrough — issue #105. Both message variants must match: the
// resource-specific tail differs ("creating cloud account read" for
// cloud-account list, live-verified against devapi --profile
// dev-managed 2026-08-06; "creating backup store read" for
// backup-store list, from saas's one producer of this message,
// `fmt.Sprintf("plan does not allow creating %s", b.name)`), but the
// "plan does not allow" prefix is stable across both.
func TestCheckResponsePlanDenial(t *testing.T) {
	testsupport.ClearEnv(t)
	for _, body := range []string{
		`{"code":400,"message":"plan does not allow creating cloud account read"}`,
		`{"code":400,"message":"plan does not allow creating backup store read"}`,
	} {
		err := CheckResponse(400, body)
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("body %q: got %v, want *ExitError", body, err)
		}
		if ee.Code() != ExitAuth {
			t.Errorf("body %q: code = %d, want ExitAuth (%d)",
				body, ee.Code(), ExitAuth)
		}
		msg := ee.Error()
		if !strings.Contains(msg,
			"this tenant's plan does not allow this resource") {
			t.Errorf("body %q: message %q does not explain the plan "+
				"denial", body, msg)
		}
		if !strings.Contains(msg, "enterprise/BYOC-plan") {
			t.Errorf("body %q: message %q does not name the fix",
				body, msg)
		}
		if !strings.Contains(msg, body) {
			t.Errorf("body %q: message %q dropped the server's own "+
				"body", body, msg)
		}
	}
}

// A non-plan-denial 400 must keep today's raw passthrough exactly.
// The plan-denial branch matches a substring, not the 400 status
// code alone, so an ordinary bad request — an unparseable field, an
// invalid enum — must not be swept into the new clean rendering.
func TestCheckResponseNonPlanBadRequestUnchanged(t *testing.T) {
	testsupport.ClearEnv(t)
	const body = `{"code":400,"message":"failed to parse start_time"}`
	err := CheckResponse(400, body)
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v, want *ExitError", err)
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("code = %d, want ExitGeneral (%d)", ee.Code(), ExitGeneral)
	}
	want := fmt.Sprintf("API error (400): %s", body)
	if ee.Error() != want {
		t.Errorf("message = %q, want %q", ee.Error(), want)
	}
}

// A 400 carrying saas's status gate means "wait and retry", not "your
// request was malformed" — issue #191.
//
// saas has exactly two producers of this message, both in
// internal/starfleet/managed_databases/svc and both a plain
// NewBadRequestError rather than the 409 its Reserve-based transitions
// answer: `password rotation requires status %q, current status is %q`
// and `backup requires status %q, current status is %q` (saas main
// c2c2bd04; no other call site in 259 uses the phrase, and none of the
// byoc-side ones do).
//
// Both bodies below are live captures from devapi 2026-08-18, taken
// against a throwaway managed database in a Managed dev tenant while it was
// provisioning. The backup producer was captured a second time while
// that database was being deleted, answering the identical message with
// `deleting` in the tail, and Ant captured the rotation one on
// 2026-08-17 with `modifying`. The status value in the tail varies with
// what the database is doing; the phrase does not, which is why the
// discriminator keys on the phrase and not on a status set.
func TestCheckResponseBusyStatusBadRequest(t *testing.T) {
	testsupport.ClearEnv(t)
	for _, body := range []string{
		`{"code":400,"message":"password rotation requires status ` +
			`\"available\", current status is \"creating\""}`,
		`{"code":400,"message":"backup requires status \"available\", ` +
			`current status is \"creating\""}`,
	} {
		err := CheckResponse(http.StatusBadRequest, body)
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("body %q: got %v, want *ExitError", body, err)
		}
		if ee.Code() != ExitGeneral {
			t.Errorf("body %q: code = %d, want ExitGeneral (%d)",
				body, ee.Code(), ExitGeneral)
		}
		msg := ee.Error()
		for _, want := range []string{"busy", "retry", "400"} {
			if !strings.Contains(msg, want) {
				t.Errorf("body %q: message %q does not carry %q",
					body, msg, want)
			}
		}
		if !strings.Contains(msg, body) {
			t.Errorf("body %q: message %q drops the server body",
				body, msg)
		}
		if strings.Contains(msg, "API error (400)") {
			t.Errorf("body %q: fell through to the raw passthrough: %q",
				body, msg)
		}
	}
}

// The busy-database 400 and the 409 must give byte-identical guidance.
// They are the same condition reached by two saas code paths, so a
// reworded copy of one would tell a user that two identical situations
// are different. Only the quoted server body may differ.
func TestBusyGuidanceIsIdenticalAcross409And400(t *testing.T) {
	testsupport.ClearEnv(t)

	guidance := func(msg string) string {
		if i := strings.Index(msg, "\n(server said"); i >= 0 {
			return msg[:i]
		}
		return msg
	}

	conflict := CheckResponse(http.StatusConflict,
		`{"code":409,"message":"database is busy"}`)
	badRequest := CheckResponse(http.StatusBadRequest,
		`{"code":400,"message":"password rotation requires status `+
			`\"available\", current status is \"creating\""}`)
	if conflict == nil || badRequest == nil {
		t.Fatal("one of the two busy responses produced no error")
	}

	got, want := guidance(badRequest.Error()), guidance(conflict.Error())
	if got != want {
		t.Errorf("the 400 guidance has drifted from the 409's:\n"+
			" 400: %q\n 409: %q", got, want)
	}
	if !strings.Contains(want, "busy") {
		t.Fatalf("neither branch produced busy guidance: %q", want)
	}
}

// The adjacent saas bad requests must stay on the raw passthrough. Both
// bodies below come from the same two functions as the status gate —
// `backup kind must be %q or %q` sits nine lines above it — and neither
// is a wait-and-retry condition: no amount of waiting makes an invalid
// backup kind or an unrotatable role valid.
func TestCheckResponseNeighbouringBadRequestsUnchanged(t *testing.T) {
	testsupport.ClearEnv(t)
	for _, body := range []string{
		`{"code":400,"message":"backup kind must be \"hot\" or ` +
			`\"durable\""}`,
		`{"code":400,"message":"role \"reader\" cannot be rotated on a ` +
			`managed database"}`,
	} {
		err := CheckResponse(http.StatusBadRequest, body)
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("body %q: got %v, want *ExitError", body, err)
		}
		want := fmt.Sprintf("API error (400): %s", body)
		if ee.Error() != want {
			t.Errorf("body %q: message = %q, want %q", body, ee.Error(),
				want)
		}
	}
}

// A wait that straddles token expiry must not die on a 401: the
// bearer editor re-resolves once the token is inside RefreshWindow
// (#375). The stub's tokens expire immediately (expires_in 1), so
// every editor call is inside the window and each must re-exchange —
// the property under test is that the SECOND request carries the
// SECOND token, where the pre-fix closure carried the first forever.
func TestBearerEditorRefreshesAcrossExpiry(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var mints int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/account/v1/oauth/token" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			mints++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": fmt.Sprintf("tok-%d", mints),
				"token_type":   "Bearer",
				"expires_in":   1,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: srv.URL, ClientID: "id", ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "tok-1" {
		t.Fatalf("Token = %q, want the first minted token", c.Token)
	}

	header := func() string {
		req, err := http.NewRequestWithContext(context.Background(),
			http.MethodGet, "http://example.invalid", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.BearerEditor(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		return req.Header.Get("Authorization")
	}

	if got := header(); got != "Bearer tok-2" {
		t.Errorf("first edited request = %q, want the re-minted "+
			"tok-2 (the resolve-time token is already expired)", got)
	}
	if got := header(); got != "Bearer tok-3" {
		t.Errorf("second edited request = %q, want tok-3 — the "+
			"editor must consult the source every time, not close "+
			"over one refresh", got)
	}
	if mints != 3 {
		t.Errorf("token exchanges = %d, want 3 (resolve + one per "+
			"edited request)", mints)
	}
}

// The refresh path must not fire for a healthy token: one exchange at
// resolve, none per request — the pre-#375 fast path is unchanged.
func TestBearerEditorReusesAHealthyToken(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var mints int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			mints++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-long",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: srv.URL, ClientID: "id", ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		req, err := http.NewRequestWithContext(context.Background(),
			http.MethodGet, "http://example.invalid", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.BearerEditor(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer tok-long" {
			t.Errorf("request %d = %q, want the one healthy token", i, got)
		}
	}
	if mints != 1 {
		t.Errorf("token exchanges = %d, want exactly 1", mints)
	}
}

// A refresh must SETTLE the source: once the re-mint hands back a
// healthy token, later requests reuse it. The first stub token dies
// immediately and the second lives an hour, so the exchange count
// separates "refresh updates value and expiry" (2) from "refresh
// updates the value but keeps re-minting" (5) — a mutation #387's
// review measured escaping both original tests. Ephemeral on
// purpose: on the persist path the CACHE absorbs that mutation, so
// only doctor's path can see it.
func TestBearerEditorRefreshSettlesEphemeral(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	var mints int
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			mints++
			expiresIn := 3600
			if mints == 1 {
				expiresIn = 1
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": fmt.Sprintf("tok-%d", mints),
				"token_type":   "Bearer",
				"expires_in":   expiresIn,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: srv.URL, ClientID: "id", ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := ResolveEphemeral(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		req, err := http.NewRequestWithContext(context.Background(),
			http.MethodGet, "http://example.invalid", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.BearerEditor(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer tok-2" {
			t.Errorf("request %d = %q, want the settled tok-2", i, got)
		}
	}
	if mints != 2 {
		t.Errorf("token exchanges = %d, want exactly 2 (resolve + one "+
			"refresh that settles)", mints)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"),
		".pgedge", "cli", "cache")); !os.IsNotExist(err) {
		t.Error("an ephemeral refresh reached the cache directory")
	}
}

// The refresh runs under the editor's ctx — a wait's poll deadline
// bounds a hung mid-wait exchange rather than the exchange outliving
// --wait-timeout on context.Background().
func TestBearerEditorRefreshHonoursTheContext(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-short",
				"token_type":   "Bearer",
				"expires_in":   1,
			})
		}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: srv.URL, ClientID: "id", ClientSecret: "secret",
	})
	rt := &module.Runtime{
		Config:  cfg,
		Profile: "dev",
		Output:  &output.Renderer{Format: "table"},
		Stderr:  io.Discard,
	}

	c, err := Resolve(rt, "", "", "", RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://example.invalid", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BearerEditor(ctx, req); err == nil {
		t.Fatal("a cancelled ctx completed a token exchange — the " +
			"refresh is not honouring the editor's context")
	}
}

// TestServerSaidStaysOnOneLine pins #576: a body ending in a newline
// must not push the closing parenthesis onto a line of its own.
func TestServerSaidStaysOnOneLine(t *testing.T) {
	body := `{"code":409,"message":"the database has 5 of 5 branches"}` + "\n"
	err := CheckResponse(409, body)
	if err == nil || !strings.HasSuffix(err.Error(), `branches"})`) {
		t.Errorf("409 message = %v", err)
	}
	for _, status := range []int{400, 401, 404, 409, 423, 500} {
		if err := CheckResponse(status, body); err == nil ||
			strings.HasSuffix(err.Error(), "\n") ||
			strings.Contains(err.Error(), "\n)") {
			t.Errorf("status %d: message %q", status, err)
		}
	}
	if got := bodyExcerpt([]byte("plain\r\n\n")); got != "plain" {
		t.Errorf("bodyExcerpt = %q, want plain", got)
	}
}
