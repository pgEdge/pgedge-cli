// Package conn owns the single pgEdge Starfleet API connection: base-URL
// and credential precedence, the token exchange and cache, the shared
// HTTP client, and the exit codes every Starfleet module reports.
//
// It builds no generated client. Each product's api package has its own
// ClientOption and *Client types with no interface in common, so Resolve
// returns the ingredients and each module assembles its own client,
// which also keeps conn from importing every api package.
package conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	accountapi "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/spf13/pflag"
)

// DefaultAPIURL re-exports the platform default from internal/apidefaults.
const DefaultAPIURL = apidefaults.StarfleetAPIURL

// moduleName keys the token cache,
// ~/.pgedge/cli/cache/<profile>-starfleet.json. The account, byoc and
// managed sub-trees all share this one token.
const moduleName = "starfleet"

// Flags holds the values of the starfleet module's persistent connection
// flags, bound once on the starfleet root and read by child commands at
// run time, after cobra has parsed the command line.
type Flags struct {
	APIURL       string
	ClientID     string
	ClientSecret string
	// Timeout bounds one HTTP exchange, defaulting to RequestTimeout;
	// zero disables the bound, as controlplane's --timeout does.
	Timeout time.Duration
}

// Exit codes reported by Starfleet commands. They are the CLI-wide
// vocabulary: internal/cli and internal/controlplane/cmd declare the
// same numbers, and the starfleet cmd packages re-export these.
//
// 2 always means a malformed command, the Unix convention and
// cli.ExitUsage, which main.go gives anything cobra rejects. Auth is 5
// so a script can tell a typo from a rejected credential (decided
// 2026-07-31).
const (
	ExitOK       = 0
	ExitGeneral  = 1
	ExitUsage    = 2
	ExitTimeout  = 3
	ExitNotFound = 4
	ExitAuth     = 5
)

// ExitError carries a message and a process exit code. main.go
// extracts the code via cli.ExitCode.
type ExitError struct {
	msg  string
	code int
}

// NewExitError builds an ExitError with the given message and code.
func NewExitError(msg string, code int) *ExitError {
	return &ExitError{msg: msg, code: code}
}

func (e *ExitError) Error() string { return e.msg }

// Code returns the process exit code associated with the error.
func (e *ExitError) Code() int { return e.code }

// isRouteMiss reports whether a 404 came from a router rather than a
// handler: it is a route miss unless the body parses as a JSON object
// with a "code" field. Every Starfleet handler 404 carries one, since the
// vendored specs declare Error with `required: [code, message]`, while
// the API's router answers an unregistered path with
// {"message":"Not Found"} (measured 2026-08-06). A proxy's HTML page, a
// reformatted copy of that body and an empty body all count as misses.
//
// Content-Type is not consulted. CheckResponse never sees headers
// (plumbing them in would touch 85 call sites), and the one case a
// header would change, pgEdge JSON under the wrong Content-Type, is
// better answered as a real not-found.
func isRouteMiss(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return true
	}
	var probe struct {
		Code json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return true
	}
	return len(probe.Code) == 0
}

// planDenialPhrase opens the 400 the API returns when the tenant's plan
// lacks a byoc or managed capability. The message is "plan does not
// allow creating <resource>", so the tail
// varies per resource ("... creating cloud account read") and only this
// prefix is stable (measured 2026-08-06). The API's other plan refusal,
// "database limit for plan reached", is left as a plain 400.
const planDenialPhrase = "plan does not allow"

// busyStatusPhrase marks a managed 400 that means "not available yet,
// retry". Backup checks the status itself and answers `backup requires
// status %q, current status is %q` (measured 2026-08-22, 3 of 3), where
// the writes that take a status reservation answer 409 instead (6 of 6,
// same day). Matching the shared head rather than a verb catches the
// next status-gated write too: of the API's 400s
// only status-gated ones carry it, and none is on a byoc or account path.
const busyStatusPhrase = "requires status"

// byocBusyStatusPhrase is byoc rotate-password's refusal when the
// database is not available. It includes "database" because five of the
// API's six "not in available status" refusals
// report the cluster's status, and a failed cluster never becomes
// available, so "wait and retry" would never end (three of the five also
// spell it "cluster in not in"). The database check runs before the
// cluster read, so a broken cluster still gets this wording.
//
// The database itself may be `failed`, which never settles either. That
// is tolerable here because the `get` the message points at reports
// `failed`; in the cluster case the same `get` would say `available` and
// contradict the message.
const byocBusyStatusPhrase = "database is not in available status"

// busyResourceError is the "wait and retry" error for a 400 or 409 that
// means the resource is mid-operation.
//
// It names no verb or resource kind because the 409 has several
// sources: a lost status reservation on restore, resize, a services write or
// rotate-password, and on delete the API's billing teardown, which answers
// "database provisioning in progress; retry shortly" while a provision
// is still running (measured 2026-08-18, 3 of 3). byoc and account share
// this path too, so the server's own message, quoted, says what was
// refused.
//
// The exit code is ExitGeneral: the request was refused, not timed out.
func busyResourceError(status int, excerpt string) *ExitError {
	return &ExitError{
		msg: fmt.Sprintf(
			"the resource is busy with another operation and this "+
				"one needs it idle. Wait for it to settle and "+
				"retry — a 'get' on the resource shows its current "+
				"status, and 'task list' shows what is running.\n"+
				"(server said (%d): %s)",
			status, excerpt),
		code: ExitGeneral,
	}
}

// branchGatedPhrase is the head the API gives the three managed 409s that
// a branch causes: a create at the branch limit, and a database delete
// or resize while branches exist. Live-verified 2026-09-18
// for the first two. These never settle, so busyResourceError's "wait
// and retry" would hold a user forever; the remedy is to delete a
// branch. No "wait" 409 carries the phrase: those say "requires the
// database to be available", and the branch-status ones name "the
// branch", never "the database has".
const branchGatedPhrase = "the database has"

// branchGatedError names the flag where the server names force=true:
// only database delete's refusal offers it, and the CLI keeps the
// API's `force` apart from its own --force on purpose.
func branchGatedError(status int, excerpt string) *ExitError {
	return &ExitError{
		msg: fmt.Sprintf(
			"a branch of this database refuses the request, and "+
				"waiting will not clear it. Delete a branch first; "+
				"where the server offers force=true, the CLI flag is "+
				"--delete-branches on 'database delete'.\n"+
				"(server said (%d): %s)",
			status, excerpt),
		code: ExitGeneral,
	}
}

// CheckResponse inspects an HTTP status and returns a descriptive
// *ExitError for any non-2xx status, or nil on success.
func CheckResponse(status int, body string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	// Classify on the full body, since truncating could drop the phrase
	// or "code" field that decides the exit code, but show only the
	// bounded, redacted excerpt: errbody routes proxy HTML pages here.
	excerpt := bodyExcerpt([]byte(body))
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ExitError{
			msg:  fmt.Sprintf("authentication error (%d): %s", status, excerpt),
			code: ExitAuth,
		}
	case http.StatusBadRequest:
		if strings.Contains(body, planDenialPhrase) {
			// ExitAuth, as for a 403: the credential is fine but the
			// action is not permitted. The API just sends it as a 400.
			return &ExitError{
				msg: fmt.Sprintf(
					"this tenant's plan does not allow this resource: "+
						"the active credential authenticates fine, but "+
						"its tenant's plan doesn't include this "+
						"capability. An enterprise/BYOC-plan tenant "+
						"typically has it — run 'pgedge starfleet doctor' to "+
						"check the active tenant's plan, or ask pgEdge "+
						"about a per-tenant override for this tenant.\n"+
						"(server said (%d): %s)",
					status, excerpt),
				code: ExitAuth,
			}
		}
		if strings.Contains(body, busyStatusPhrase) ||
			strings.Contains(body, byocBusyStatusPhrase) {
			return busyResourceError(status, excerpt)
		}
		return &ExitError{
			msg:  fmt.Sprintf("API error (%d): %s", status, excerpt),
			code: ExitGeneral,
		}
	case http.StatusNotFound:
		if isRouteMiss(body) {
			return &ExitError{
				msg: fmt.Sprintf(
					"this server does not serve an endpoint this "+
						"command needs: the 404 came back without the "+
						"JSON error body every pgEdge Starfleet handler "+
						"sends (a \"code\" field), so a router, proxy "+
						"or gateway answered — not the API.\n"+
						"(Check that the base URL — --api-url or the "+
						"profile's api_url — is a pgEdge Starfleet API "+
						"root, e.g. https://api.pgedge.com. Run "+
						"'pgedge starfleet doctor' to probe the connection.)\n"+
						"(server said (404): %s)",
					excerpt),
				code: ExitGeneral,
			}
		}
		return &ExitError{
			msg:  fmt.Sprintf("resource not found (%d): %s", status, excerpt),
			code: ExitNotFound,
		}
	case http.StatusConflict:
		// A 409 is a lost status reservation, which clears on its own,
		// except for the branch refusals, which never do.
		if strings.Contains(body, branchGatedPhrase) {
			return branchGatedError(status, excerpt)
		}
		return busyResourceError(status, excerpt)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &ExitError{
			msg:  fmt.Sprintf("request timed out (%d): %s", status, excerpt),
			code: ExitTimeout,
		}
	default:
		return &ExitError{
			msg:  fmt.Sprintf("API error (%d): %s", status, excerpt),
			code: ExitGeneral,
		}
	}
}

// ResolveAPIURL applies the base-URL precedence used across every
// Starfleet module: active-profile value, then the default, then the
// --api-url flag (highest).
func ResolveAPIURL(ap *config.StarfleetProfile, flagAPIURL string) string {
	return apidefaults.ResolveStarfleetAPIURL(ap, flagAPIURL)
}

// RequestTimeout bounds one Starfleet HTTP exchange end to end.
// http.DefaultTransport bounds only the dial and the TLS handshake, and
// call sites pass context.Background(), so without it a server that
// never answers hangs the command.
//
// 30s matches controlplane and is ~8x the slowest call measured
// (2026-08-22): 3639 ms for `database metrics --window 30,days`,
// a 3.67 MB body. Wider windows return the same body, because retention
// bounds it.
const RequestTimeout = 30 * time.Second

// HTTPClientFor returns the client a Starfleet call uses. The layers are
// ordered on purpose. Dry-run wraps outside httplog, so an intercepted
// write is reported once, by the dry-run report, rather than logged as
// well. errbody wraps httplog, so --debug prints the Content-Type the
// server really sent before errbody repairs it. errbody runs even on a
// quiet command, so there is no bare-client shortcut.
//
// timeout is the starfleet root's --timeout, passed explicitly because
// connection settings belong to the module, not to Runtime.
func HTTPClientFor(rt *module.Runtime, timeout time.Duration) *http.Client {
	lvl := httplog.LevelFor(rt.Verbose, rt.Debug)
	return &http.Client{
		Timeout: timeout,
		Transport: dryrun.Wrap(
			errbody.Wrap(
				httplog.Wrap(http.DefaultTransport, rt.Stderr, lvl)),
			rt.DryRun),
	}
}

// authHTTPClientFor is HTTPClientFor without the dry-run layer, for the
// token exchange: that POST would otherwise be intercepted and every dry
// run would fail to authenticate before its first read. The exemption
// is by client rather than by URL path so no path string can drift from
// the spec; TestTokenExchangeIsNotInterceptedByDryRun pins it. The quiet
// branch builds its own client because setting Timeout on
// http.DefaultClient would bound every other caller.
func authHTTPClientFor(rt *module.Runtime, timeout time.Duration) *http.Client {
	lvl := httplog.LevelFor(rt.Verbose, rt.Debug)
	if lvl == httplog.Off {
		return &http.Client{Timeout: timeout}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: httplog.Wrap(http.DefaultTransport, rt.Stderr, lvl),
	}
}

// Authenticator exchanges client credentials for an access token
// through the generated account operation, so the /account/v1/oauth/token
// contract has exactly one typed definition in this repo.
type Authenticator struct {
	APIURL     string
	HTTPClient *http.Client
}

// Exchange posts the client credentials to /account/v1/oauth/token and
// returns the token with an absolute expiry, without touching the cache.
//
// It calls GenerateAccessToken, not GenerateAccessTokenWithResponse: the
// generated parser decodes any error body into Error, whose code is an
// integer, so a rejection such as {"code":"invalid_client",...} would
// surface as a Go unmarshal error and lose the server's message.
func (a *Authenticator) Exchange(ctx context.Context,
	clientID, clientSecret string) (*auth.CachedToken, error) {
	httpClient := a.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: RequestTimeout}
	}
	client, err := accountapi.NewClient(
		a.APIURL,
		accountapi.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("conn: create account client: %w", err)
	}

	body := accountapi.GenerateAccessTokenJSONRequestBody{ //nolint:gosec // G117: secret belongs in the token request body
		ClientId:     clientID,
		ClientSecret: clientSecret,
	}
	resp, err := client.GenerateAccessToken(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("conn: token request to %s: %w",
			a.APIURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("conn: read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"conn: token endpoint returned %s: %s",
			resp.Status, bodyExcerpt(raw))
	}

	// A proxy intercepting the token endpoint can answer 2xx with HTML,
	// so name the endpoint and what it sent, not just the decode error.
	var tok accountapi.AccessToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf(
			"conn: decode token response from %s (%s): %w: %s",
			a.APIURL, resp.Status, err, bodyExcerpt(raw))
	}
	// A 200 with no access_token would otherwise be cached for its
	// full advertised TTL, sending "Authorization: Bearer " on every
	// later call and 401ing with nothing diagnosable.
	if tok.AccessToken == "" {
		return nil, fmt.Errorf(
			"conn: token endpoint returned no access_token: %s",
			bodyExcerpt(raw))
	}
	return &auth.CachedToken{
		AccessToken: tok.AccessToken,
		ExpiresAt: time.Now().Add(
			time.Duration(tok.ExpiresIn) * time.Second),
	}, nil
}

// maxBodyExcerpt bounds how many characters of a response body may
// reach an error message. An unbounded echo once landed a 200KB proxy
// error page whole on the user's stderr; a few hundred characters is
// enough to recognise what answered.
const maxBodyExcerpt = 512

// bodyExcerpt renders a response body for an error message, bounded at
// maxBodyExcerpt and with any credential redacted. The redaction matters
// on the token endpoint: a 2xx body that fails to decode, say with
// expires_in as a string, can still carry a live token. The rules live
// in internal/httplog so that --debug and this apply the same ones.
func bodyExcerpt(raw []byte) string {
	// Not run through output.Sanitize, which escapes backslashes and so
	// would double every `\"` in a JSON error body
	// (TestCheckResponseNeighbouringBadRequestsUnchanged). The excerpt
	// is therefore not control-escaped; accepted 2026-08-22, as a second
	// escaping function would cost more than it protects.
	//
	// The trim stops a trailing newline stranding the closing
	// parenthesis of "(server said ...)".
	return strings.TrimRight(httplog.RedactBody(raw, maxBodyExcerpt),
		" \t\r\n")
}

// Login mints a token and writes it to the profile's cache. It is the
// only caller of SaveToken; see mint.
func Login(ctx context.Context, rt *module.Runtime,
	apiURL, clientID, clientSecret string, timeout time.Duration,
) (*auth.CachedToken, error) {
	tok, err := mint(ctx, rt, apiURL, clientID, clientSecret, timeout)
	if err != nil {
		return nil, err
	}
	if err := Store(rt).SaveToken(tok); err != nil {
		return nil, err
	}
	return tok, nil
}

// mint exchanges credentials for a token and stamps its binding
// fingerprint, without touching the cache. Keeping it apart from Login,
// rather than giving Login a flag, leaves Login the only path to disk,
// with auth.SaveToken refusing an unfingerprinted token as a backstop.
// The fingerprint includes apiURL, the host that just answered, and is
// stamped even on an ephemeral token so that no unbound token exists.
func mint(ctx context.Context, rt *module.Runtime,
	apiURL, clientID, clientSecret string, timeout time.Duration,
) (*auth.CachedToken, error) {
	// --timeout 0 unbounds the request the user asked patience for, not
	// the credential exchange before it. A hung exchange is an
	// authentication failure, exit 5.
	if timeout <= 0 {
		timeout = RequestTimeout
	}
	a := &Authenticator{
		APIURL: apiURL,
		// authHTTPClientFor, not HTTPClientFor: this POST must survive a
		// dry run.
		HTTPClient: authHTTPClientFor(rt, timeout),
	}
	tok, err := a.Exchange(ctx, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	tok.Fingerprint = auth.Fingerprint(apiURL, clientID, clientSecret)
	return tok, nil
}

// Logout removes the profile's cached token file and any staging file
// an interrupted write left beside it, and nothing else in the cache
// directory (see auth.ClearToken).
func Logout(rt *module.Runtime) error {
	return Store(rt).ClearToken()
}

// Store returns the credential and cache handle for the active profile.
// It never refreshes, so `auth status` and doctor's Auth row read the
// cache through it rather than through Resolve.
func Store(rt *module.Runtime) *auth.Auth {
	return &auth.Auth{
		Profile:    rt.Profile,
		Module:     moduleName,
		Keychain:   rt.Keychain,
		ConfigPath: rt.Config.SavePath(),
	}
}

// Resolved is a credential together with everything its source decides.
type Resolved struct {
	Creds  *auth.Credentials
	Source string // one of the auth.Source* constants
	APIURL string
	// Env reports env-pair credentials, which never touch the config
	// file or the token cache.
	Env bool
}

// ProfileConflictError reports --profile given while the env pair is
// set: the two name different tenants and neither may silently win.
type ProfileConflictError struct{ Profile string }

func (e *ProfileConflictError) Error() string {
	return fmt.Sprintf("--profile %s given while %s and %s are set: "+
		"unset the variables to use the profile, or drop --profile",
		e.Profile, auth.EnvClientID, auth.EnvClientSecret)
}

// IsUsageError reports a credential failure the operator typed — half a
// flag or env pair, or --profile against the env pair — as opposed to
// credentials being absent or unreadable. The first is ExitUsage, the
// second ExitAuth.
func IsUsageError(err error) bool {
	var partial *auth.PartialFlagPairError
	var conflict *ProfileConflictError
	return errors.As(err, &partial) || errors.As(err, &conflict)
}

// ResolveCredentials resolves the credential for the runtime with
// flags > env > profile and the API URL that goes with it. Env
// credentials are a separate, never-persisted profile: the profile's
// api_url does not apply to them, only --api-url or the default.
func ResolveCredentials(rt *module.Runtime,
	flagID, flagSecret, flagAPIURL string,
) (*Resolved, error) {
	ap := rt.Config.StarfleetProfile(rt.Profile)
	creds, source, err := Store(rt).ResolveCredentials(
		ap, flagID, flagSecret)
	if err != nil {
		return nil, err
	}
	r := &Resolved{Creds: creds, Source: source}
	if source == auth.SourceEnv {
		if rt.ProfileExplicit {
			return nil, &ProfileConflictError{Profile: rt.Profile}
		}
		r.Env = true
		r.APIURL = ResolveAPIURL(nil, flagAPIURL)
		return r, nil
	}
	r.APIURL = ResolveAPIURL(ap, flagAPIURL)
	return r, nil
}

// Conn is a resolved Starfleet connection: everything a generated client
// needs, with none of the client's own types.
type Conn struct {
	APIURL       string
	Token        string
	HTTPClient   *http.Client
	BearerEditor func(context.Context, *http.Request) error
}

// Resolve produces an authenticated connection for the runtime's
// active profile, honouring per-invocation flag overrides. It reuses a
// cached token that is more than auth.RefreshWindow from expiry and
// otherwise exchanges credentials for a fresh one.
func Resolve(rt *module.Runtime,
	flagID, flagSecret, flagAPIURL string, timeout time.Duration,
) (*Conn, error) {
	return resolve(rt, flagID, flagSecret, flagAPIURL, timeout,
		persistToken)
}

// ResolveEphemeral is Resolve for `starfleet doctor`, which must not
// change what it reports: it reuses a valid cached token but never
// writes one. Writing would let doctor report "token expired or missing"
// and leave a fresh token behind, and `doctor --api-url <other>` would
// evict the profile's working token, since a cached token is bound to
// its API URL.
func ResolveEphemeral(rt *module.Runtime,
	flagID, flagSecret, flagAPIURL string, timeout time.Duration,
) (*Conn, error) {
	return resolve(rt, flagID, flagSecret, flagAPIURL, timeout,
		ephemeralToken)
}

// tokenPolicy says whether a freshly minted token may reach the cache.
type tokenPolicy bool

const (
	persistToken   tokenPolicy = true
	ephemeralToken tokenPolicy = false
)

func resolve(rt *module.Runtime,
	flagID, flagSecret, flagAPIURL string, timeout time.Duration,
	policy tokenPolicy,
) (*Conn, error) {
	res, err := ResolveCredentials(rt, flagID, flagSecret, flagAPIURL)
	if err != nil {
		if IsUsageError(err) {
			return nil, NewExitError(err.Error(), ExitUsage)
		}
		return nil, NewExitError(err.Error(), ExitAuth)
	}
	store, apiURL, creds := Store(rt), res.APIURL, res.Creds
	if res.Env {
		store = nil
		policy = ephemeralToken
	}

	value, expiry, err := token(context.Background(), rt, store, apiURL,
		creds, timeout, policy)
	if err != nil {
		return nil, NewExitError(
			fmt.Sprintf("authentication failed: %v", err), ExitAuth)
	}

	// The editor asks a source rather than closing over one token, so a
	// wait that outlives the token re-mints under the same policy instead
	// of turning a timeout (exit 3) into a 401 (exit 5).
	src := &tokenSource{
		value:  value,
		expiry: expiry,
		mint: func(ctx context.Context) (string, time.Time, error) {
			return token(ctx, rt, store, apiURL, creds, timeout, policy)
		},
	}

	return &Conn{
		APIURL:     apiURL,
		Token:      value,
		HTTPClient: HTTPClientFor(rt, timeout),
		BearerEditor: func(ctx context.Context, req *http.Request) error {
			v, err := src.bearer(ctx)
			if err != nil {
				return NewExitError(fmt.Sprintf(
					"authentication failed: %v", err), ExitAuth)
			}
			req.Header.Set("Authorization", "Bearer "+v)
			return nil
		},
	}, nil
}

// tokenSource hands the bearer editor a token more than
// auth.RefreshWindow from expiry, re-minting when it is not. The mutex
// is defensive: no starfleet command is concurrent today, but the editor
// is where one would race. The refresh runs under the editor's ctx, so a
// hung exchange cannot outlive --wait-timeout.
type tokenSource struct {
	mu     sync.Mutex
	value  string
	expiry time.Time
	mint   func(context.Context) (string, time.Time, error)
}

func (ts *tokenSource) bearer(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if time.Until(ts.expiry) > auth.RefreshWindow {
		return ts.value, nil
	}
	v, expiry, err := ts.mint(ctx)
	if err != nil {
		return "", err
	}
	ts.value, ts.expiry = v, expiry
	return v, nil
}

// token returns a valid access token, reusing the cache only when the
// token is more than auth.RefreshWindow from expiry and was minted for
// this call's credential and API URL. Without the URL check,
// `--api-url otherhost` would send a live token to a host that never
// issued it; without the credential check, a rekeyed profile would run
// as the old tenant. A nil store (env credentials) skips the cache.
//
// A mismatch, including a legacy cache with no fingerprint, re-mints
// silently; doctor and `auth status` are where it is reported. So
// alternating between a profile's credential and a flag-supplied one
// re-exchanges on every switch, an accepted cost. The comparison is
// plain == because both operands are local files or argv.
//
// policy governs only the miss path: an ephemeral caller still reuses a
// warm cache.
func token(ctx context.Context, rt *module.Runtime, store *auth.Auth,
	apiURL string, creds *auth.Credentials, timeout time.Duration,
	policy tokenPolicy,
) (string, time.Time, error) {
	if store != nil {
		tok, err := store.LoadToken()
		if err == nil &&
			time.Until(tok.ExpiresAt) > auth.RefreshWindow &&
			tok.MintedBy(apiURL, creds.ClientID, creds.ClientSecret) {
			return tok.AccessToken, tok.ExpiresAt, nil
		}
	}
	if policy == ephemeralToken {
		fresh, err := mint(ctx, rt, apiURL, creds.ClientID,
			creds.ClientSecret, timeout)
		if err != nil {
			return "", time.Time{}, err
		}
		return fresh.AccessToken, fresh.ExpiresAt, nil
	}
	fresh, err := Login(ctx, rt, apiURL, creds.ClientID,
		creds.ClientSecret, timeout)
	if err != nil {
		return "", time.Time{}, err
	}
	return fresh.AccessToken, fresh.ExpiresAt, nil
}

// ApplyCreatedRange fills a list endpoint's --created-after and
// --created-before params from the flags, in one place so that every
// list reports a bad value the same way. A flag given but empty fails
// the parse rather than being dropped, as --config does.
func ApplyCreatedRange(
	fs *pflag.FlagSet, after, before **time.Time,
) error {
	for _, f := range []struct {
		flag string
		dst  **time.Time
	}{
		{"created-after", after},
		{"created-before", before},
	} {
		if !fs.Changed(f.flag) {
			continue
		}
		v, _ := fs.GetString(f.flag)
		at, err := cli.ParseTimeFlag("--"+f.flag, v)
		if err != nil {
			return err
		}
		*f.dst = &at
	}
	return nil
}
