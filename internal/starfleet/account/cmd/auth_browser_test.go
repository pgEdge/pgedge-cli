package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/signin"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// tenantsBody is the stub API's GET /account/v1/tenants answer.
const tenantsBody = `[` +
	`{"id":"t-a","name":"Alpha","plan":"enterprise"},` +
	`{"id":"t-b","name":"Beta","plan":"managed"}]`

// signInStubs stands up a fake Auth0 and a fake API, and points the
// login command's seams at them. The fake Auth0 issues an access token
// reaching tenants, with the first as its default.
type signInStubs struct {
	api      string
	tenants  []string
	verifier atomic.Value
	issued   atomic.Value // the access and ID tokens, space-joined
	reply    func(q url.Values) url.Values
	apiHits  map[string]int
	headers  []http.Header
}

// stubCode is the authorization code the fake browser returns.
const stubCode = "auth-code-7f3a9c"

func newSignInStubs(t *testing.T, tenants ...string) *signInStubs {
	t.Helper()
	s := &signInStubs{tenants: tenants, apiHits: map[string]int{}}
	s.reply = func(q url.Values) url.Values {
		return url.Values{"code": {stubCode}, "state": {q.Get("state")}}
	}
	auth0 := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			s.verifier.Store(r.PostForm.Get("code_verifier"))
			at := testsupport.SessionToken(t, "auth0|u1",
				time.Now().Add(24*time.Hour), s.tenants...)
			it := testsupport.FakeJWT(t,
				map[string]any{"email": "user@example.com"})
			s.issued.Store(at + " " + it)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": at, "id_token": it, "expires_in": 86400,
			})
		}))
	t.Cleanup(auth0.Close)
	api := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			s.apiHits[r.URL.Path]++
			s.headers = append(s.headers, r.Header.Clone())
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/account/v1/tenants":
				_, _ = io.WriteString(w, tenantsBody)
			case testsupport.TokenPath:
				_, _ = io.WriteString(w, testsupport.TokenBody)
			default:
				_, _ = io.WriteString(w, "[]")
			}
		}))
	t.Cleanup(api.Close)
	s.api = api.URL

	savedFor, savedFlow, savedTTY := auth0For, signInFlow,
		promptIsInteractive
	t.Cleanup(func() {
		auth0For, signInFlow, promptIsInteractive = savedFor, savedFlow,
			savedTTY
	})
	auth0For = func(string) (apidefaults.Auth0App, bool) {
		return apidefaults.Auth0App{Domain: "unused",
			Audience: "aud", ClientID: "cid"}, true
	}
	promptIsInteractive = func(*module.Runtime) bool { return false }
	signInFlow = func(rt *module.Runtime, app apidefaults.Auth0App,
		_ time.Duration) *signin.Flow {
		return &signin.Flow{
			BaseURL: auth0.URL, ClientID: app.ClientID,
			Audience: app.Audience, Ports: []int{0},
			Wait: 5 * time.Second, Out: rt.Stderr,
			HTTPClient: conn.SignInHTTPClient(rt, 5*time.Second),
			Open: func(target string) error {
				u, _ := url.Parse(target)
				q := u.Query()
				resp, err := http.Get(q.Get("redirect_uri") + "?" +
					s.reply(q).Encode())
				if err == nil {
					_ = resp.Body.Close()
				}
				return err
			},
		}
	}
	return s
}

func login(t *testing.T, rt *module.Runtime, s *signInStubs,
	args ...string) error {
	t.Helper()
	full := append([]string{"auth", "login", "--api-url", s.api}, args...)
	return runAccount(t, rt, &bytes.Buffer{}, full...)
}

func exitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code()
	}
	return -1
}

func TestBrowserLoginDebugOutputCarriesNoSecrets(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	rt.Debug = true
	if err := login(t, rt, s); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb.String(), "/oauth/token") {
		t.Fatalf("--debug did not log the exchange:\n%s", errb.String())
	}
	issued, _ := s.issued.Load().(string)
	verifier, _ := s.verifier.Load().(string)
	secrets := append(strings.Fields(issued), verifier, stubCode)
	for _, secret := range secrets {
		if secret == "" || strings.Contains(errb.String(), secret) {
			t.Errorf("--debug output carries %q", secret)
		}
	}
}

func TestBrowserLoginTenantChoice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		code    int
		stderr  string
		tenants []string
	}{
		{name: "only one", tenants: []string{"t-b"}, want: "t-b"},
		{name: "flag by id", args: []string{"--tenant", "T-B"},
			want: "t-b"},
		{name: "flag by name", args: []string{"--tenant", "beta"},
			want: "t-b"},
		{name: "flag unknown", args: []string{"--tenant", "Gamma"},
			code: ExitUsage, stderr: "t-a, t-b"},
		{name: "several, no terminal", want: "t-a",
			stderr: "using Alpha (t-a), the account default"},
		{name: "no tenant", tenants: []string{}, code: ExitAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenants := tc.tenants
			if tenants == nil {
				tenants = []string{"t-a", "t-b"}
			}
			s := newSignInStubs(t, tenants...)
			rt, _, errb := testsupport.NewRuntime(t, "", "text")
			err := login(t, rt, s, tc.args...)
			if tc.code != 0 {
				if exitCode(err) != tc.code {
					t.Fatalf("err = %v, want exit %d", err, tc.code)
				}
				if tc.stderr != "" &&
					!strings.Contains(err.Error(), tc.stderr) {
					t.Errorf("err = %v, want %q", err, tc.stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if got := rt.Config.StarfleetProfile("default").TenantID; got != tc.want {
				t.Errorf("tenant = %q, want %q", got, tc.want)
			}
			if !strings.Contains(errb.String(), tc.stderr) {
				t.Errorf("stderr lacks %q:\n%s", tc.stderr, errb.String())
			}
		})
	}
}

func TestBrowserLoginFailures(t *testing.T) {
	t.Run("auth0 refuses", func(t *testing.T) {
		s := newSignInStubs(t, "t-a")
		s.reply = func(q url.Values) url.Values {
			return url.Values{"state": {q.Get("state")},
				"error":             {"access_denied"},
				"error_description": {"Service not available in your region"}}
		}
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		err := login(t, rt, s)
		if exitCode(err) != ExitAuth ||
			!strings.Contains(err.Error(), "not available in your region") {
			t.Fatalf("err = %v", err)
		}
		if rt.Config.StarfleetProfile("default").IsBrowserSession() {
			t.Error("a refused sign-in saved a session")
		}
	})
	t.Run("tenant with an API client", func(t *testing.T) {
		s := newSignInStubs(t, "t-a")
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		if err := login(t, rt, s, "--client-id", "i", "--client-secret",
			"s", "--tenant", "t"); exitCode(err) != ExitUsage {
			t.Fatalf("err = %v, want exit 2", err)
		}
	})
}

func TestBrowserLoginKeepsTheSecretWhenTheSaveFails(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	kc := &keychain.Fake{}
	rt.Keychain = kc
	if err := login(t, rt, s, "--client-id", "kid",
		"--client-secret", "ks"); err != nil {
		t.Fatalf("client login: %v", err)
	}
	dir := filepath.Dir(rt.Config.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := login(t, rt, s); err == nil {
		t.Fatal("login succeeded with an unwritable config")
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); err != nil {
		t.Errorf("a failed save removed the API client's secret: %v", err)
	}
}

func TestLoginSwitchesProfileKind(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	kc := &keychain.Fake{}
	rt.Keychain = kc

	if err := login(t, rt, s, "--client-id", "kid",
		"--client-secret", "ks"); err != nil {
		t.Fatalf("client login: %v", err)
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); err != nil {
		t.Fatalf("client secret not in the keychain: %v", err)
	}

	if err := login(t, rt, s); err != nil {
		t.Fatalf("browser login: %v\n%s", err, errb.String())
	}
	p := rt.Config.StarfleetProfile("default")
	if p.ClientID != "" || p.ClientSecret != "" || !p.IsBrowserSession() {
		t.Errorf("after browser login, profile = %+v", p)
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); !errors.Is(err,
		keychain.ErrNotFound) {
		t.Errorf("the API client's secret survived: %v", err)
	}

	if err := login(t, rt, s, "--client-id", "kid2",
		"--client-secret", "ks2"); err != nil {
		t.Fatalf("client login again: %v", err)
	}
	p = rt.Config.StarfleetProfile("default")
	if p.IsBrowserSession() || p.TenantID != "" || p.ClientID != "kid2" {
		t.Errorf("after client login, profile = %+v", p)
	}
	tok, err := conn.Store(rt).LoadToken()
	if err != nil || tok.SessionFor(s.api) {
		t.Errorf("the session token survived the switch: %+v", tok)
	}
}

func TestWhoamiLogoutForASession(t *testing.T) {
	s := newSignInStubs(t, "t-a", "t-b")
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	testsupport.SeedSession(t, rt, s.api, "u1",
		time.Now().Add(time.Hour), "t-b", "t-a")

	var got whoamiReport
	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("whoami json: %v\n%s", err, out.String())
	}
	if s.apiHits["/account/v1/clients"] != 0 {
		t.Error("whoami listed API clients for a browser session")
	}
	if got.TenantID != "t-b" || got.TenantCount != 2 || got.ClientID != "" {
		t.Errorf("whoami = %+v", got)
	}
	if h := s.headers[len(s.headers)-1]; h.Get(conn.SelectedTenantHeader) != "t-b" {
		t.Errorf("whoami headers = %v", h)
	}

	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	cfg, _ := config.Load("")
	if p := cfg.StarfleetProfile("default"); p.IsBrowserSession() ||
		p.TenantID != "" || p.APIURL != s.api {
		t.Errorf("after logout, profile = %+v", p)
	}
	if _, err := conn.Store(rt).LoadToken(); err == nil {
		t.Error("logout left the session token")
	}
}

func TestStatusForAnExpiredSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seed  func(t *testing.T, rt *module.Runtime, api string)
		token string
	}{
		{"expired", func(t *testing.T, rt *module.Runtime, api string) {
			testsupport.SeedSession(t, rt, api, "u1",
				time.Now().Add(-time.Minute), "t-a")
		}, "Token:        expired"},
		{"other API", func(t *testing.T, rt *module.Runtime, _ string) {
			testsupport.SeedSession(t, rt, "https://elsewhere", "u1",
				time.Now().Add(time.Hour), "t-a")
		}, "Token:        stale"},
		{"missing", func(t *testing.T, rt *module.Runtime, api string) {
			rt.Config.SetStarfleetProfile("default",
				&config.StarfleetProfile{APIURL: api,
					Auth: config.AuthBrowser})
		}, "Token:        not cached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			api := "https://api.example"
			tc.seed(t, rt, api)
			err := runAccount(t, rt, &bytes.Buffer{}, "auth", "status",
				"--api-url", api)
			if exitCode(err) != ExitAuth ||
				!strings.Contains(err.Error(), "auth login") {
				t.Fatalf("err = %v, want exit 5", err)
			}
			if !strings.Contains(out.String(), tc.token) {
				t.Errorf("status:\n%s", out.String())
			}
		})
	}
}

func TestTenantFlagIsValidatedByStatusAndDoctor(t *testing.T) {
	s := newSignInStubs(t, "t-a", "t-b")
	for _, tc := range []struct {
		name    string
		session bool
		want    string
	}{
		{"session, unreachable tenant", true, "t-a, t-b"},
		{"API client", false, "--tenant applies only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "json")
			args := []string{"--tenant", "t-z"}
			if tc.session {
				testsupport.SeedSession(t, rt, s.api, "u1",
					time.Now().Add(time.Hour), "t-a", "t-b")
			} else {
				args = append(args, "--client-id", "i",
					"--client-secret", "x", "--api-url", s.api)
			}
			err := runAccount(t, rt, &bytes.Buffer{},
				append([]string{"auth", "status"}, args...)...)
			if exitCode(err) != ExitUsage ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("status err = %v, want exit 2", err)
			}

			out.Reset()
			if err := runAccount(t, rt, &bytes.Buffer{},
				append([]string{"doctor"}, args...)...); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			var got accountDoctorReport
			_ = json.Unmarshal(out.Bytes(), &got)
			if got.Auth.Authenticated ||
				!strings.Contains(got.Auth.Problem, tc.want) {
				t.Errorf("doctor auth = %+v", got.Auth)
			}
		})
	}
}
