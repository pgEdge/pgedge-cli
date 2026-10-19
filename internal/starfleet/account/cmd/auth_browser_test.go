package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/auth"
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
	reply    func(q url.Values) url.Values
	apiHits  map[string]int
	headers  []http.Header
}

func newSignInStubs(t *testing.T, tenants ...string) *signInStubs {
	t.Helper()
	s := &signInStubs{tenants: tenants, apiHits: map[string]int{}}
	s.reply = func(q url.Values) url.Values {
		return url.Values{"code": {"c"}, "state": {q.Get("state")}}
	}
	auth0 := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			s.verifier.Store(r.PostForm.Get("code_verifier"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": testsupport.SessionToken(t, "auth0|u1",
					time.Now().Add(24*time.Hour), s.tenants...),
				"id_token": testsupport.FakeJWT(t,
					map[string]any{"email": "user@example.com"}),
				"expires_in": 86400,
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

func TestSignedInUser(t *testing.T) {
	id := testsupport.FakeJWT(t, map[string]any{"email": "id@example.com"})
	for _, tc := range []struct {
		claims auth.Claims
		id     string
		want   string
	}{
		{auth.Claims{Subject: "s", Email: "at@example.com"}, id,
			"id@example.com"},
		{auth.Claims{Subject: "s", Email: "at@example.com"}, "",
			"at@example.com"},
		{auth.Claims{Subject: "s"}, "opaque", "s"},
	} {
		if got := signedInUser(&tc.claims, tc.id); got != tc.want {
			t.Errorf("signedInUser(%+v, %q) = %q, want %q",
				tc.claims, tc.id, got, tc.want)
		}
	}
}

func TestDefaultSignInFlowTargetsAuth0(t *testing.T) {
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	f := signInFlow(rt, apidefaults.Auth0App{Domain: "auth.example",
		Audience: "aud", ClientID: "cid"}, time.Second)
	if f.BaseURL != "https://auth.example" || f.ClientID != "cid" ||
		f.Audience != "aud" || f.Open == nil || f.Out != errb ||
		len(f.Ports) != 3 || f.Ports[0] != 8484 ||
		f.Wait != signin.DefaultWait || f.HTTPClient == nil {
		t.Errorf("default flow = %+v", f)
	}
}

func TestBrowserLoginSavesASession(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	if err := login(t, rt, s); err != nil {
		t.Fatalf("login: %v\n%s", err, errb.String())
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.StarfleetProfile("default")
	if *p != (config.StarfleetProfile{APIURL: s.api,
		Auth: config.AuthBrowser, TenantID: "t-a"}) {
		t.Errorf("profile = %+v", p)
	}
	tok, err := conn.Store(rt).LoadToken()
	if err != nil || !tok.SessionFor(s.api) ||
		tok.User != "user@example.com" ||
		time.Until(tok.ExpiresAt) < 23*time.Hour {
		t.Fatalf("cached token = %+v, %v", tok, err)
	}
	if v, _ := s.verifier.Load().(string); len(v) != 43 {
		t.Errorf("token exchange carried verifier %q", v)
	}
	for _, want := range []string{
		"/authorize?",
		"Signed in as user@example.com.",
		"Tenant: Alpha (t-a)",
		"The session lasts until " + tok.ExpiresAt.Format(time.RFC3339),
		"Profile default saved to",
	} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
}

func TestBrowserLoginTenantChoice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		tty     bool
		stdin   string
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
		{name: "several, prompt", tty: true, stdin: "2\n", want: "t-b",
			stderr: "2) Beta (t-b)"},
		{name: "prompt, bad answer", tty: true, stdin: "9\n",
			code: ExitUsage},
		{name: "prompt, no answer", tty: true, code: ExitUsage},
		{name: "no tenant", tenants: []string{}, code: ExitAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenants := tc.tenants
			if tenants == nil {
				tenants = []string{"t-a", "t-b"}
			}
			s := newSignInStubs(t, tenants...)
			promptIsInteractive = func(*module.Runtime) bool {
				return tc.tty
			}
			rt, _, errb := testsupport.NewRuntime(t, tc.stdin, "text")
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

func TestBrowserLoginWithoutTenantNamesUsesIDs(t *testing.T) {
	s := newSignInStubs(t, "t-x", "t-y")
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	if err := login(t, rt, s, "--api-url", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errb.String(), "Tenant: t-x\n") {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

func TestBrowserLoginFailures(t *testing.T) {
	t.Run("not available", func(t *testing.T) {
		s := newSignInStubs(t, "t-a")
		auth0For = apidefaults.Auth0For
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		err := login(t, rt, s)
		if exitCode(err) != ExitAuth ||
			!strings.Contains(err.Error(), s.api) ||
			!strings.Contains(err.Error(), "--client-id") {
			t.Fatalf("err = %v", err)
		}
	})
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
	t.Run("unreadable token", func(t *testing.T) {
		s := newSignInStubs(t, "t-a")
		flow := signInFlow
		signInFlow = func(rt *module.Runtime, app apidefaults.Auth0App,
			d time.Duration) *signin.Flow {
			f := flow(rt, app, d)
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"access_token":"opaque"}`)
				}))
			t.Cleanup(srv.Close)
			f.BaseURL = srv.URL
			return f
		}
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		if err := login(t, rt, s); exitCode(err) != ExitAuth {
			t.Fatalf("err = %v", err)
		}
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"tenant with an API client",
			[]string{"--client-id", "i", "--client-secret", "s",
				"--tenant", "t"}},
		{"insecure storage alone", []string{"--insecure-storage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSignInStubs(t, "t-a")
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			if err := login(t, rt, s, tc.args...); exitCode(err) !=
				ExitUsage {
				t.Fatalf("err = %v, want exit 2", err)
			}
		})
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

func TestStatusWhoamiLogoutForASession(t *testing.T) {
	s := newSignInStubs(t, "t-a", "t-b")
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, s.api, "u1",
		time.Now().Add(time.Hour), "t-b", "t-a")

	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"User:         u1@example.com",
		"Auth source:  browser", "Tenant:       t-b", "Token:        valid"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "eyJ") {
		t.Error("status printed the token")
	}

	rt.Output.Format = "json"
	var got whoamiReport
	out.Reset()
	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "whoami"); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("whoami json: %v\n%s", err, out.String())
	}
	if s.apiHits["/account/v1/clients"] != 0 {
		t.Error("whoami listed API clients for a browser session")
	}
	if got.User != "u1@example.com" || got.TenantID != "t-b" ||
		got.TenantName != "Beta" || got.Plan != "managed" ||
		got.TenantCount != 2 || got.ClientID != "" {
		t.Errorf("whoami = %+v", got)
	}
	if h := s.headers[len(s.headers)-1]; h.Get(conn.SelectedTenantHeader) != "t-b" {
		t.Errorf("whoami headers = %v", h)
	}
	rt.Output.Format = "text"

	out.Reset()
	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "whoami"); err != nil {
		t.Fatalf("whoami text: %v", err)
	}
	if !strings.Contains(out.String(), "User:          u1@example.com") ||
		strings.Contains(out.String(), "Client ID") {
		t.Errorf("whoami text:\n%s", out.String())
	}

	errb.Reset()
	if err := runAccount(t, rt, &bytes.Buffer{}, "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(errb.String(), "Session token removed") {
		t.Errorf("logout said %q", errb.String())
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

func TestDoctorForASession(t *testing.T) {
	s := newSignInStubs(t, "t-a", "t-b")
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, s.api, "u1",
		time.Now().Add(time.Hour), "t-b", "t-a")
	if err := runAccount(t, rt, &bytes.Buffer{}, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, want := range []string{"signed in as u1@example.com via browser",
		"Beta (t-b, plan: managed)", "reporting the selected one"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor lacks %q:\n%s", want, out.String())
		}
	}

	rt2, out2, _ := testsupport.NewRuntime(t, "", "text")
	rt2.Profile = "dev"
	testsupport.SeedSession(t, rt2, s.api, "u1",
		time.Now().Add(-time.Hour), "t-a")
	if err := runAccount(t, rt2, &bytes.Buffer{}, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out2.String(),
		"auth login --profile dev") {
		t.Errorf("doctor did not point at login:\n%s", out2.String())
	}
}

func TestSessionTenantFlagOnResourceCommands(t *testing.T) {
	s := newSignInStubs(t, "t-a", "t-b")
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, s.api, "u1",
		time.Now().Add(time.Hour), "t-a", "t-b")
	if err := runAccount(t, rt, &bytes.Buffer{}, "tenant", "list",
		"--tenant", "t-b"); err != nil {
		t.Fatalf("tenant list: %v", err)
	}
	if h := s.headers[len(s.headers)-1]; h.Get(conn.SelectedTenantHeader) != "t-b" {
		t.Errorf("headers = %v", h)
	}
	err := runAccount(t, rt, &bytes.Buffer{}, "tenant", "list", "--tenant", "t-z")
	if exitCode(err) != ExitUsage || !strings.Contains(err.Error(),
		"t-a, t-b") {
		t.Errorf("err = %v, want a usage error listing the tenants", err)
	}
}
