package signin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeAuth0 serves /oauth/token, accepting only the verifier whose S256
// matches the challenge the browser was sent.
type fakeAuth0 struct {
	challenge string
	form      url.Values
	status    int
	body      string
}

func (a *fakeAuth0) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth/token" || r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			_ = r.ParseForm()
			a.form = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			if a.status != 0 {
				w.WriteHeader(a.status)
				_, _ = io.WriteString(w, a.body)
				return
			}
			if challengeFor(r.PostForm.Get("code_verifier")) != a.challenge {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":"invalid_grant",`+
					`"error_description":"PKCE verification failed"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at","id_token":`+
				`"it","expires_in":86400}`)
		}))
	t.Cleanup(srv.Close)
	return srv
}

// browser stands in for the user's browser: it records the authorize
// URL and follows the redirect with the query reply builds.
func browser(t *testing.T, a *fakeAuth0, got *url.Values,
	reply func(q url.Values) url.Values) func(string) error {
	return func(target string) error {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatalf("authorize URL: %v", err)
		}
		q := u.Query()
		*got = q
		a.challenge = q.Get("code_challenge")
		resp, err := http.Get(q.Get("redirect_uri") + "?" +
			reply(q).Encode())
		if err != nil {
			t.Errorf("callback: %v", err)
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), "close this tab") {
			t.Errorf("callback page = %q", body)
		}
		return nil
	}
}

func approve(q url.Values) url.Values {
	return url.Values{"code": {"the-code"}, "state": {q.Get("state")}}
}

func newFlow(srv *httptest.Server, open func(string) error) (*Flow,
	*bytes.Buffer) {
	var out bytes.Buffer
	return &Flow{
		BaseURL:    srv.URL,
		ClientID:   "cid",
		Audience:   "https://api.example",
		Ports:      []int{0},
		Open:       open,
		Wait:       5 * time.Second,
		Out:        &out,
		HTTPClient: srv.Client(),
	}, &out
}

func TestRunExchangesTheCodeWithTheVerifier(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, _ := newFlow(srv, browser(t, a, &q, approve))

	tok, err := f.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tok.AccessToken != "at" || tok.IDToken != "it" ||
		tok.ExpiresIn != 86400 {
		t.Errorf("tokens = %+v", tok)
	}
	for k, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "cid",
		"audience":              "https://api.example",
		"scope":                 "openid profile email offline_access",
		"code_challenge_method": "S256",
	} {
		if q.Get(k) != want {
			t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") ||
		!strings.HasSuffix(q.Get("redirect_uri"), "/callback") {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	for k, want := range map[string]string{
		"grant_type":   "authorization_code",
		"client_id":    "cid",
		"code":         "the-code",
		"redirect_uri": q.Get("redirect_uri"),
	} {
		if a.form.Get(k) != want {
			t.Errorf("token %s = %q, want %q", k, a.form.Get(k), want)
		}
	}
	if len(a.form.Get("code_verifier")) != 43 {
		t.Errorf("verifier %q is not 32 bytes base64url",
			a.form.Get("code_verifier"))
	}
}

func TestRunIgnoresAWrongStateAndKeepsWaiting(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	genuine := browser(t, a, &q, approve)
	f, _ := newFlow(srv, func(target string) error {
		u, _ := url.Parse(target)
		forged := u.Query().Get("redirect_uri") + "?" + url.Values{
			"code": {"forged-code"}, "state": {"forged"}}.Encode()
		resp, err := http.Get(forged)
		if err != nil {
			t.Fatalf("forged callback: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("forged callback status = %d, want 400",
				resp.StatusCode)
		}
		return genuine(target)
	})
	if _, err := f.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.form.Get("code") != "the-code" {
		t.Errorf("token endpoint got code %q, want the real one",
			a.form.Get("code"))
	}
}

func TestRedirectURIMatchesTheRegisteredCallbacks(t *testing.T) {
	want := []string{
		"http://127.0.0.1:8484/callback",
		"http://127.0.0.1:8485/callback",
		"http://127.0.0.1:8486/callback",
	}
	if len(DefaultPorts) != len(want) {
		t.Fatalf("DefaultPorts = %v", DefaultPorts)
	}
	for i, p := range DefaultPorts {
		if got := redirectURI(p); got != want[i] {
			t.Errorf("redirectURI(%d) = %q, want %q", p, got, want[i])
		}
	}
}

func TestRunReportsAnAuth0Error(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, _ := newFlow(srv, browser(t, a, &q, func(q url.Values) url.Values {
		return url.Values{"state": {q.Get("state")},
			"error":             {"access_denied"},
			"error_description": {"Please verify your email"}}
	}))
	_, err := f.Run(context.Background())
	var denied *DeniedError
	if !errors.As(err, &denied) || denied.Code != "access_denied" ||
		err.Error() != "sign-in refused: Please verify your email" {
		t.Fatalf("err = %v", err)
	}
	if (&DeniedError{Code: "x"}).Error() != "sign-in refused: x" {
		t.Error("a bare code should be reported")
	}
}

func TestRunEndsWithoutACallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, tc := range []struct {
		name string
		open func(string) error
		want error
	}{
		{"timeout", func(string) error { return nil }, ErrTimeout},
		{"cancelled", func(string) error { cancel(); return nil },
			ErrCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newFlow(httptest.NewServer(http.NotFoundHandler()),
				tc.open)
			f.Wait = 50 * time.Millisecond
			if _, err := f.Run(ctx); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBrowserCommand(t *testing.T) {
	for _, tc := range []struct {
		goos, name string
		args       []string
	}{
		{"darwin", "open", []string{"u"}},
		{"linux", "xdg-open", []string{"u"}},
		{"windows", "rundll32",
			[]string{"url.dll,FileProtocolHandler", "u"}},
	} {
		name, args := browserCommand(tc.goos, "u")
		if name != tc.name || strings.Join(args, " ") !=
			strings.Join(tc.args, " ") {
			t.Errorf("%s: %s %v", tc.goos, name, args)
		}
	}
}

func TestChallengeIsS256OfTheVerifier(t *testing.T) {
	// RFC 7636 appendix B.
	got := challengeFor("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("challenge = %q", got)
	}
}
