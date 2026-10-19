package signin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
		BaseURL:  srv.URL,
		ClientID: "cid",
		Audience: "https://api.example",
		Ports:    []int{0},
		Open:     open,
		Wait:     5 * time.Second,
		Out:      &out,
	}, &out
}

func TestRunExchangesTheCodeWithTheVerifier(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, out := newFlow(srv, browser(t, a, &q, approve))

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
		"scope":                 "openid profile email",
		"code_challenge_method": "S256",
	} {
		if q.Get(k) != want {
			t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if strings.Contains(q.Get("scope"), "offline_access") {
		t.Error("step 1 must not ask for offline_access")
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
	if !strings.Contains(out.String(), srv.URL+"/authorize?") {
		t.Errorf("the URL was not printed:\n%s", out.String())
	}
}

func TestRunFailsWhenTheVerifierDoesNotMatch(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, _ := newFlow(srv, func(target string) error {
		err := browser(t, a, &q, approve)(target)
		a.challenge = "something-else"
		return err
	})
	_, err := f.Run(context.Background())
	var denied *DeniedError
	if !errors.As(err, &denied) ||
		!strings.Contains(err.Error(), "PKCE") {
		t.Fatalf("err = %v, want the token endpoint's refusal", err)
	}
}

func TestRunRejectsAWrongState(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, _ := newFlow(srv, browser(t, a, &q, func(url.Values) url.Values {
		return url.Values{"code": {"c"}, "state": {"forged"}}
	}))
	if _, err := f.Run(context.Background()); !errors.Is(err,
		ErrStateMismatch) {
		t.Fatalf("err = %v, want ErrStateMismatch", err)
	}
	if a.form != nil {
		t.Error("a forged state reached the token endpoint")
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

func TestRunRejectsACallbackWithNoCode(t *testing.T) {
	a := &fakeAuth0{}
	srv := a.serve(t)
	var q url.Values
	f, _ := newFlow(srv, browser(t, a, &q, func(q url.Values) url.Values {
		return url.Values{"state": {q.Get("state")}}
	}))
	if _, err := f.Run(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "no authorization code") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFailsWhenEveryPortIsBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	busy := ln.Addr().(*net.TCPAddr).Port

	f, _ := newFlow(httptest.NewServer(http.NotFoundHandler()), nil)
	f.Ports = []int{busy}
	_, err = f.Run(context.Background())
	if !errors.Is(err, ErrPortsBusy) ||
		!strings.Contains(err.Error(), fmt.Sprint(busy)) {
		t.Fatalf("err = %v, want ErrPortsBusy naming %d", err, busy)
	}
}

func TestRunTimesOut(t *testing.T) {
	f, out := newFlow(httptest.NewServer(http.NotFoundHandler()),
		func(string) error { return errors.New("no display") })
	f.Wait = 50 * time.Millisecond
	if _, err := f.Run(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if !strings.Contains(out.String(), "Could not open a browser (no "+
		"display)") {
		t.Errorf("a failed launch was not reported:\n%s", out.String())
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f, _ := newFlow(httptest.NewServer(http.NotFoundHandler()),
		func(string) error { cancel(); return nil })
	if _, err := f.Run(ctx); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

func TestExchangeFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"plain error", 500, "boom", "500"},
		{"no token", 200, `{"expires_in":1}`, "no access_token"},
		{"not json", 200, `<html>`, "decode response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				}))
			defer srv.Close()
			f := &Flow{BaseURL: srv.URL}
			_, err := f.exchange(context.Background(), "c", "r", "v")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	f := &Flow{BaseURL: "http://127.0.0.1:0"}
	if _, err := f.exchange(context.Background(), "c", "r",
		"v"); err == nil {
		t.Error("an unreachable token endpoint should fail")
	}
	f = &Flow{BaseURL: "://bad"}
	if _, err := f.exchange(context.Background(), "c", "r",
		"v"); err == nil {
		t.Error("a malformed base URL should fail")
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
