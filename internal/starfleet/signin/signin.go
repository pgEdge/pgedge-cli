// Package signin runs the browser half of a pgEdge Starfleet sign-in:
// an OAuth 2.0 authorization-code flow with PKCE against Auth0, with
// the redirect caught on a loopback listener.
package signin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/output"
)

// DefaultPorts are the loopback ports the Auth0 app lists as callbacks;
// any other port is refused by Auth0.
var DefaultPorts = []int{8484, 8485, 8486}

// DefaultWait bounds how long Run waits for the browser to come back.
const DefaultWait = 5 * time.Minute

// Errors Run reports. Callers map them to exit codes.
var (
	ErrPortsBusy = errors.New("signin: no free callback port")
	ErrTimeout   = errors.New("timed out waiting for the browser sign-in")
	ErrCancelled = errors.New("sign-in cancelled")
)

// DeniedError is an error Auth0 returned on the callback, such as
// access_denied.
type DeniedError struct {
	Code        string
	Description string
}

func (e *DeniedError) Error() string {
	if e.Description == "" {
		return "sign-in refused: " + e.Code
	}
	return "sign-in refused: " + e.Description
}

// Flow is one sign-in. BaseURL is https://<auth0 domain>; tests point
// it, Ports and Open at fakes.
type Flow struct {
	BaseURL    string
	ClientID   string
	Audience   string
	Ports      []int
	Open       func(string) error
	Wait       time.Duration
	HTTPClient *http.Client
	// Out receives the URL and progress lines.
	Out io.Writer
}

// Tokens is what the token endpoint returns. RefreshToken is empty
// when the Auth0 app does not grant refresh tokens.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type callback struct {
	code string
	err  error
}

// Run listens for the callback, opens the browser, waits for the
// redirect and exchanges the code for tokens.
func (f *Flow) Run(ctx context.Context) (*Tokens, error) {
	ln, port, err := listen(f.Ports)
	if err != nil {
		return nil, err
	}
	verifier, err := randomString()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	state, err := randomString()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	redirect := redirectURI(port)

	results := make(chan callback, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, results),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(
			context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	authURL := f.authorizeURL(redirect, challengeFor(verifier), state)
	fmt.Fprintf(f.Out, "Opening your browser to sign in to pgEdge "+
		"Starfleet. If it does not open, visit:\n  %s\n", output.Sanitize(authURL))
	if f.Open != nil {
		if err := f.Open(authURL); err != nil {
			fmt.Fprintf(f.Out, "Could not open a browser (%s); open "+
				"the URL above to continue.\n", output.Sanitize(err.Error()))
		}
	}
	fmt.Fprintln(f.Out, "Waiting for the sign-in to finish "+
		"(Ctrl-C to cancel)...")

	wait := f.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	var cb callback
	select {
	case cb = <-results:
	case <-timer.C:
		return nil, ErrTimeout
	case <-ctx.Done():
		return nil, ErrCancelled
	}
	if cb.err != nil {
		return nil, cb.err
	}
	return f.exchange(ctx, cb.code, redirect, verifier)
}

// redirectURI must match a callback the Auth0 app registers exactly.
func redirectURI(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", port)
}

// listen binds the first free port of ports on 127.0.0.1.
func listen(ports []int) (net.Listener, int, error) {
	if len(ports) == 0 {
		ports = DefaultPorts
	}
	for _, p := range ports {
		ln, err := net.Listen("tcp",
			net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return nil, 0, fmt.Errorf("%w: ports %s on 127.0.0.1 are all in "+
		"use; free one and retry", ErrPortsBusy, joinPorts(ports))
}

func joinPorts(ports []int) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

func (f *Flow) authorizeURL(redirect, challenge, state string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.ClientID},
		"redirect_uri":          {redirect},
		"audience":              {f.Audience},
		"scope":                 {"openid profile email offline_access"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return strings.TrimRight(f.BaseURL, "/") + "/authorize?" + q.Encode()
}

// randomString is 32 random bytes, base64url without padding: the
// PKCE verifier length RFC 7636 recommends.
func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("signin: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const (
	pageDone   = "Signed in to pgEdge Starfleet. You can close this tab."
	pageFailed = "Sign-in to pgEdge Starfleet failed. Return to the " +
		"terminal for details. You can close this tab."
)

// callbackHandler answers /callback; only the first result is kept. A
// request without this sign-in's state is refused and ignored, so any
// web page that hits the port cannot end the sign-in.
func callbackHandler(state string, results chan<- callback) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		var cb callback
		switch {
		case q.Get("error") != "":
			cb.err = &DeniedError{
				Code:        q.Get("error"),
				Description: q.Get("error_description"),
			}
		case q.Get("code") == "":
			cb.err = errors.New("the sign-in response carried no " +
				"authorization code")
		default:
			cb.code = q.Get("code")
		}
		page := pageDone
		if cb.err != nil {
			page = pageFailed
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>pgEdge "+
			"Starfleet</title><p>"+page+"</p>\n")
		select {
		case results <- cb:
		default:
		}
	})
	return mux
}

// exchange trades the code for tokens at /oauth/token.
func (f *Flow) exchange(ctx context.Context,
	code, redirect, verifier string) (*Tokens, error) {
	return postToken(ctx, f.HTTPClient, f.BaseURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {f.ClientID},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	})
}

// Refresh trades a refresh token for new tokens. Auth0 rotates refresh
// tokens, so the old one is dead once this succeeds. A refusal is a
// *DeniedError; IsInvalidGrant says whether the token itself is dead.
func Refresh(ctx context.Context, client *http.Client,
	baseURL, clientID, refreshToken string) (*Tokens, error) {
	return postToken(ctx, client, baseURL, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	})
}

// IsInvalidGrant reports Auth0 refusing a refresh token: expired,
// reused after rotation, revoked, or its user blocked.
func IsInvalidGrant(err error) bool {
	var d *DeniedError
	return errors.As(err, &d) && d.Code == "invalid_grant"
}

// Revoke revokes a refresh token at /oauth/revoke.
func Revoke(ctx context.Context, client *http.Client,
	baseURL, clientID, token string) error {
	resp, raw, err := postForm(ctx, client, baseURL, "/oauth/revoke",
		url.Values{"client_id": {clientID}, "token": {token}})
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if d := deniedFrom(raw); d != nil {
			return d
		}
		return fmt.Errorf("revoke returned %s", resp.Status)
	}
	return nil
}

func postToken(ctx context.Context, client *http.Client, baseURL string,
	form url.Values) (*Tokens, error) {
	resp, raw, err := postForm(ctx, client, baseURL, "/oauth/token", form)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if d := deniedFrom(raw); d != nil {
			return nil, d
		}
		return nil, fmt.Errorf("token exchange returned %s", resp.Status)
	}
	var tok Tokens
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("token exchange: decode response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("token exchange returned no access_token")
	}
	return &tok, nil
}

func postForm(ctx context.Context, client *http.Client, baseURL,
	path string, form url.Values) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, raw, nil
}

// deniedFrom reads an OAuth error body, or returns nil.
func deniedFrom(raw []byte) *DeniedError {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return &DeniedError{Code: e.Error, Description: e.Description}
	}
	return nil
}

// browserCommand is the command that opens target on goos.
func browserCommand(goos, target string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	}
	return "xdg-open", []string{target}
}

// OpenBrowser opens target in the platform's default browser.
func OpenBrowser(target string) error {
	name, args := browserCommand(runtime.GOOS, target)
	cmd := exec.Command(name, args...) //nolint:gosec // G204: a fixed opener; target is our own authorize URL
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
