// Package cmd wires the pgEdge Control Plane command tree: the module
// root, its persistent connection flags, and the HTTP API client
// shared by every resource command.
package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// defaultBaseURL is used when neither profile nor flag supplies a
// Control Plane URL. The Control Plane serves plain HTTP on :3000 by
// default (mTLS is opt-in).
const defaultBaseURL = "http://localhost:3000"

// routeMissBody is the exact body Go's default mux serves for a
// path the server never registered. A Control Plane answers a
// genuine resource miss through its handlers with a JSON APIError
// body (verified live), so this text identifies a server that does
// not serve the endpoint at all — a CP older than the CLI's API
// description, or a base URL that is not a CP API root (issue #34).
const routeMissBody = "404 page not found"

// Exit codes used by controlplane commands.
const (
	ExitOK       = 0
	ExitGeneral  = 1
	ExitUsage    = 2
	ExitTimeout  = 3
	ExitNotFound = 4
	ExitAuth     = 5
)

// ExitError carries a message and a process exit code. Exported so
// main.go can extract the code via the cli.coder interface.
type ExitError struct {
	msg  string
	code int
}

func (e *ExitError) Error() string { return e.msg }
func (e *ExitError) Code() int     { return e.code }

// maxBodyExcerpt bounds how many characters of a response body may
// reach the user's terminal in an error message.
//
// It matters more here than the number suggests. Before the error-body
// repair was installed above, a non-JSON body died inside the generated
// parser and never reached this function, so every branch below could
// echo the whole thing harmlessly. Now that those bodies are routed
// here deliberately, an unbounded branch would print a proxy's entire
// HTML interstitial — the failure the starfleet module's identical constant
// records having already happened once.
const maxBodyExcerpt = 512

// bodyExcerpt renders a response body for an error message, bounded at
// maxBodyExcerpt and with any credential-bearing content redacted.
//
// internal/httplog owns every "what must never be printed" rule in this
// CLI, and this is deliberately a thin wrapper over it rather than its
// own copy: --debug dumps controlplane's traffic through the same rules, and two
// implementations of that judgement is how one of them ends up wrong.
// Trailing whitespace is trimmed, as in the starfleet module (#576).
func bodyExcerpt(body string) string {
	return strings.TrimRight(
		httplog.RedactBody([]byte(body), maxBodyExcerpt), " \t\r\n")
}

// checkResponse maps a non-2xx HTTP status to a descriptive
// *ExitError, or returns nil on success.
//
// Every branch renders the EXCERPT, never the raw body. CLASSIFICATION
// still reads the FULL body — the routeMissBody comparison below is
// exact, and truncating first could turn a route miss into a generic
// error. Only what the user is shown is bounded.
func checkResponse(status int, body string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	excerpt := bodyExcerpt(body)
	switch status {
	case http.StatusNotFound:
		if strings.TrimSpace(body) == routeMissBody {
			return &ExitError{
				msg: "this server does not serve an endpoint this " +
					"command needs: the reply is the router's plain " +
					"\"404 page not found\", not a Control Plane " +
					"error.\n(Either the Control Plane is older than " +
					"the API this CLI was built against, or the base " +
					"URL is not a Control Plane API root. Run " +
					"'pgedge controlplane doctor' to see each server's " +
					"version. This CLI supports Control Plane >= " +
					SupportFloor + "; a server below that floor would " +
					"404 here.)",
				code: ExitGeneral,
			}
		}
		return &ExitError{
			msg:  fmt.Sprintf("resource not found (%d): %s", status, excerpt),
			code: ExitNotFound,
		}
	case http.StatusConflict:
		if isClusterNotInitialized(body) {
			return &ExitError{
				msg: "this Control Plane has no cluster yet — run " +
					"'pgedge controlplane cluster init' to create one, or " +
					"'pgedge controlplane cluster join' to join an existing " +
					"cluster.\n(The server answered 409 " +
					"cluster_not_initialized. Every verb that reads " +
					"or writes cluster state answers this way until " +
					"one exists; 'version', 'doctor', 'config' and " +
					"the two cluster commands above do not.)",
				code: ExitGeneral,
			}
		}
		return &ExitError{
			msg:  fmt.Sprintf("API error (%d): %s", status, excerpt),
			code: ExitGeneral,
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		// The spec pinned at CP_TAG declares one 401
		// (invalid_join_token, on cluster join); 403 rides along
		// because the exit-code table classes both as auth, and a
		// proxy in front of a Control Plane can answer either.
		return &ExitError{
			msg: fmt.Sprintf("authentication error (%d): %s",
				status, excerpt),
			code: ExitAuth,
		}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &ExitError{
			msg:  fmt.Sprintf("request timed out (%d): %s", status, excerpt),
			code: ExitTimeout,
		}
	case http.StatusInternalServerError:
		if isStorageKeyNotFound(body) {
			return &ExitError{
				msg: fmt.Sprintf(
					"resource not found (%d, a control-plane storage "+
						"key-miss reported as server_error rather than "+
						"404 -- see pgEdge/pgedge-cli#252): %s",
					status, excerpt),
				code: ExitNotFound,
			}
		}
		return &ExitError{
			msg:  fmt.Sprintf("API error (%d): %s", status, excerpt),
			code: ExitGeneral,
		}
	default:
		return &ExitError{
			msg:  fmt.Sprintf("API error (%d): %s", status, excerpt),
			code: ExitGeneral,
		}
	}
}

// clusterNotInitialized is the error name a Control Plane with no
// cluster answers with, on every endpoint except /v1/version.
//
// Measured against control-plane v0.10.1: GET /v1/{cluster,hosts,
// databases,tasks} each answer HTTP 409 with exactly
// {"name":"cluster_not_initialized","message":"this operation is
// invalid on an uninitialized cluster"}.
const clusterNotInitialized = "cluster_not_initialized"

// isClusterNotInitialized reports whether a 409 body is that error.
//
// It keys on the "name" FIELD rather than matching the message text,
// because the message is prose the server may reword while the name is
// the contract. A body that does not parse is not this error: the
// generic 409 branch handles it, which is the safe direction -- a
// wrong "run cluster init" would send an operator to create a second
// cluster.
func isClusterNotInitialized(body string) bool {
	var e struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		return false
	}
	return e.Name == clusterNotInitialized
}

// storageKeyMissPattern matches control-plane's storage layer
// reporting a key miss for a specific resource: "failed to fetch
// <resource> from storage: "<path>": key not found". The resource
// name is left open (\S+) because hosts, databases and clusters all
// go through the same storage layer and can hit the same server bug.
//
// It does NOT match on the bare words "key not found" appearing
// anywhere in a message. A genuine Control Plane fault can contain
// that phrase for an unrelated reason -- "failed to decrypt join
// token: signing key not found in vault" describes broken key
// material, not a missing resource, and remapping that to exit 4
// would tell an operator "not found" when the control plane's own
// key material is actually broken.
var storageKeyMissPattern = regexp.MustCompile(
	`^failed to fetch \S+ from storage: "[^"]+": key not found`)

// isStorageKeyNotFound reports whether a 500 body is control-plane's
// storage layer answering a key miss under server_error rather than a
// genuine fault.
//
// Keyed on BOTH the name AND the message matching
// storageKeyMissPattern, not "name == server_error" alone: that name
// also covers real faults (a panic, a marshal failure), and remapping
// every one of those to exit 4 would tell an operator their target
// does not exist when the control plane is actually broken. A body
// that does not parse, or whose message does not match the pattern,
// falls through to the generic 500 branch, which is the safe
// direction.
func isStorageKeyNotFound(body string) bool {
	var e struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		return false
	}
	return e.Name == "server_error" &&
		storageKeyMissPattern.MatchString(e.Message)
}

// connConfig is the fully-resolved connection to a Control Plane.
type connConfig struct {
	baseURLs   []string
	caCert     string
	clientCert string
	clientKey  string
	insecure   bool
	timeout    time.Duration
}

// resolveConnection resolves the connection for cmd from, in
// increasing precedence: profile file, then the cp root's persistent
// flags. This is the single connection resolver for the module
// (byoc's precedence logic was spread across three files).
func resolveConnection(
	rt *module.Runtime, cmd *cobra.Command,
) (connConfig, error) {
	p := rt.Config.ControlplaneProfile(rt.Profile)
	c := connConfig{
		baseURLs:   p.EffectiveBaseURLs(),
		caCert:     p.CACert,
		clientCert: p.ClientCert,
		clientKey:  p.ClientKey,
		insecure:   p.InsecureSkipVerify,
		timeout:    30 * time.Second,
	}
	if cmd.Flags().Changed("base-url") {
		if urls, err := cmd.Flags().GetStringArray("base-url"); err == nil {
			c.baseURLs = urls
		}
	}
	if v, _ := cmd.Flags().GetString("ca-cert"); v != "" {
		c.caCert = v
	}
	if v, _ := cmd.Flags().GetString("client-cert"); v != "" {
		c.clientCert = v
	}
	if v, _ := cmd.Flags().GetString("client-key"); v != "" {
		c.clientKey = v
	}
	if cmd.Flags().Changed("insecure") {
		c.insecure, _ = cmd.Flags().GetBool("insecure")
	}
	if len(c.baseURLs) == 0 {
		c.baseURLs = []string{defaultBaseURL}
	}

	// The flag is read FIRST and the profile value is parsed only if
	// the flag did not supply one. Precedence here is flag > profile >
	// default, so a caller who names a timeout on the command line has
	// already replaced whatever the profile says -- including a value
	// that will not parse. Refusing anyway would remove the one escape
	// hatch from a bad profile.
	if cmd.Flags().Changed("timeout") {
		if d, err := cmd.Flags().GetDuration("timeout"); err == nil {
			c.timeout = d
		}
		return c, nil
	}
	if p.Timeout != "" {
		d, err := time.ParseDuration(p.Timeout)
		if err != nil {
			// c is returned POPULATED, with the 30s default standing.
			// Verbs that send a request propagate the error; the
			// display verbs (config view, doctor) show the failure as
			// a row, because refusing to run is the wrong answer from
			// the commands an operator reaches for to find out what is
			// wrong with their profile.
			return c, &ExitError{
				msg: fmt.Sprintf(
					"profile %q: timeout %q is not a duration — use a "+
						"Go duration such as 30s, 5m or 1h, remove the "+
						"field to take the 30s default, or pass "+
						"--timeout to override it for one command",
					rt.Profile, p.Timeout),
				code: ExitUsage,
			}
		}
		c.timeout = d
	}
	return c, nil
}

// httpClientFor builds an *http.Client for c. Every client gets the
// TLS 1.3 floor regardless of which fields c sets; with no cert fields
// beyond that it is otherwise a plain client, and with cert fields it
// configures mTLS. The diagnostic transport is layered on when
// --verbose or --debug asks for it; internal/httplog owns the level,
// the wire format and the redaction rules, shared with the Starfleet
// modules.
func httpClientFor(
	rt *module.Runtime, c connConfig,
) (*http.Client, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	// Every cp connection — including a plain https:// base URL with
	// none of --ca-cert/--client-cert/--insecure set — gets this floor.
	// A self-hosted Control Plane may sit behind a customer-controlled
	// TLS-terminating proxy, so "modern Go server" isn't a guarantee;
	// pinning 1.3 anyway removes the whole 1.2 downgrade/cipher-suite
	// surface, and there is no escape hatch today (see internal/controlplane/llms.txt).
	tlsCfg := &tls.Config{ //nolint:gosec // G402: --insecure is an explicit dev opt-in
		InsecureSkipVerify: c.insecure,
		MinVersion:         tls.VersionTLS13,
	}
	if c.caCert != "" {
		pem, err := os.ReadFile(c.caCert) //nolint:gosec // G304: operator-configured cert path
		if err != nil {
			return nil, &ExitError{
				msg:  fmt.Sprintf("read ca-cert: %v", err),
				code: ExitUsage,
			}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, &ExitError{
				msg:  fmt.Sprintf("ca-cert %s: no certificates found", c.caCert),
				code: ExitUsage,
			}
		}
		tlsCfg.RootCAs = pool
	}
	if c.clientCert != "" || c.clientKey != "" {
		cert, err := tls.LoadX509KeyPair(c.clientCert, c.clientKey)
		if err != nil {
			return nil, &ExitError{
				msg:  fmt.Sprintf("load client cert/key: %v", err),
				code: ExitUsage,
			}
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	base.TLSClientConfig = tlsCfg
	lvl := httplog.LevelFor(rt.Verbose, rt.Debug)
	// dry-run wraps OUTSIDE httplog: reads still log under
	// --verbose/--debug, while an intercepted write never reaches the
	// logger and so is reported once, by the dry-run report, rather than
	// twice in two formats.
	//
	// mutatingGETs is not optional here. Control Plane declares two
	// state-changing operations as GET, so a method-only rule let
	// `controlplane task cancel --dry-run` really cancel the task.
	//
	// Control Plane needs no counterpart to the starfleet module's auth
	// carve-out: it authenticates with mTLS from the TLS config above,
	// not by exchanging a credential over a POST, so there is no write
	// here that a dry run has to let through.
	//
	// The error-body repair sits between them, in the same order and for
	// the same reason as the starfleet module's client (#165). controlplane's
	// generated client carries the identical Content-Type catch-all, so
	// an error body that is not JSON served under a JSON Content-Type
	// fails inside Parse*Response and checkResponse below never runs.
	// A Control Plane's own route miss is Go's default-mux
	// `404 page not found` under text/plain, which reaches checkResponse
	// unaided — this is for the case a reverse proxy or gateway sits in
	// front of one and answers with a mislabelled body, which is
	// precisely the shape #140 was reported against for Starfleet.
	//
	// It wraps httplog rather than nesting inside it so that --debug
	// prints the Content-Type the server really sent, before the repair
	// rewrites it. The other order would make --debug report the
	// corrected header and hide the symptom it is used to diagnose.
	return &http.Client{
		Transport: dryrun.Wrap(
			errbody.Wrap(httplog.Wrap(base, rt.Stderr, lvl)), rt.DryRun,
			mutatingGETs...),
		Timeout: c.timeout,
	}, nil
}

// mutatingGETs are the paths of Control Plane operations that change
// state despite being declared GET. A dry run must stop these exactly
// as it stops a POST.
//
// Both are real, and both were missed by the first version of this
// feature, which trusted the HTTP method:
//
//   - GET /v1/cluster/init (operationId init-cluster)
//   - GET /v1/databases/{database_id}/tasks/{task_id}/cancel
//     (operationId cancel-database-task)
//
// `GET /v1/cluster/join-token` reads a token and is deliberately NOT
// here, even though its operationId contains "join".
//
// TestMutatingGETsCoverTheSpec re-derives this list from
// openapi/control-plane.json, so a re-vendor that adds another
// state-changing GET fails the build instead of silently making
// --dry-run carry it out.
//
// ANCHORED AT A SEGMENT BOUNDARY, not at the start of the path. A
// --base-url may carry a path prefix — a Control Plane behind a reverse
// proxy at https://host/api/ is an ordinary deployment, and nothing here
// rejects one — and the generated client resolves its request paths
// relative to that base. Anchoring with ^ therefore stopped matching the
// moment a prefix appeared, which left the whole bug live for exactly
// those users: `controlplane task cancel --dry-run` against a prefixed base URL
// really cancelled the task and printed the operation's normal success
// output.
var mutatingGETs = []*regexp.Regexp{
	regexp.MustCompile(`(^|/)v1/cluster/init$`),
	regexp.MustCompile(`(^|/)v1/databases/[^/]+/tasks/[^/]+/cancel$`),
}

// newAPIClient builds an API client for connection c against baseURL.
func newAPIClient(
	rt *module.Runtime, c connConfig, baseURL string,
) (*api.ClientWithResponses, error) {
	httpClient, err := httpClientFor(rt, c)
	if err != nil {
		return nil, err
	}
	client, err := api.NewClientWithResponses(
		baseURL, api.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return client, nil
}

// clientFromCmd resolves the connection off cmd's inherited flags and
// builds the API client. Every resource RunE calls it.
func clientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*api.ClientWithResponses, error) {
	c, err := resolveConnection(rt, cmd)
	if err != nil {
		return nil, err
	}
	baseURL, err := selectBaseURL(rt, c)
	if err != nil {
		return nil, err
	}
	return newAPIClient(rt, c, baseURL)
}

// selectBaseURL returns the Control Plane URL to use for this command,
// with no bound on the whole failover walk beyond the per-request
// timeout in c. Every ordinary controlplane verb wants exactly that: the user
// asked for a 30s (or --timeout) budget per server and failing over
// between N servers may legitimately cost N of them.
//
// Callers that must bound the total elapsed time regardless of how many
// candidates are configured — orchestrator detection in `database init`
// is the one today — call selectBaseURLContext with a deadline instead.
func selectBaseURL(rt *module.Runtime, c connConfig) (string, error) {
	return selectBaseURLContext(context.Background(), rt, c)
}

// selectBaseURLContext returns the Control Plane URL to use for this
// command. With 0 or 1 candidate it returns that URL without probing,
// preserving the single-server fast path. With several, it probes each
// in listed order via GET /v1/version and returns the first that
// answers with a 2xx, so the CLI fails over to a live server. If none
// answers it returns an ExitError naming every configured URL — even
// ones the walk never reached because ctx expired first (today no
// deadline-passing caller surfaces that error, so the distinction is
// cosmetic).
//
// ctx bounds the walk as a whole, not each probe: it is passed to every
// probe, and the loop stops as soon as it is done. So a caller that
// hands in a context with an overall deadline gets that deadline for
// the entire failover sequence, however many candidates are configured,
// rather than the per-request timeout multiplied by the candidate count.
func selectBaseURLContext(
	ctx context.Context, rt *module.Runtime, c connConfig,
) (string, error) {
	if len(c.baseURLs) <= 1 {
		if len(c.baseURLs) == 0 {
			return defaultBaseURL, nil
		}
		return c.baseURLs[0], nil
	}
	for _, u := range c.baseURLs {
		if ctx.Err() != nil {
			break
		}
		if v, ok := probeVersion(ctx, rt, c, u); ok {
			if rt.Verbose {
				fmt.Fprintf(rt.Stderr, "controlplane: using %s\n", output.Sanitize(u))
			}
			warnBelowFloor(rt, v)
			return u, nil
		}
		if rt.Verbose {
			fmt.Fprintf(rt.Stderr, "controlplane: %s unreachable, trying next\n", output.Sanitize(u))
		}
	}
	return "", &ExitError{
		msg: fmt.Sprintf(
			"no control-plane server responded to /v1/version; tried: %s",
			strings.Join(c.baseURLs, ", ")),
		code: ExitGeneral,
	}
}

// probeVersion reports whether url's /v1/version endpoint is reachable,
// returning the server version and true on a 2xx response, or "" and
// false on any transport error or non-2xx status. The per-request
// timeout comes from c (via the http client), matching version.go; ctx
// can shorten that further, which is how a caller bounds a whole
// multi-candidate walk (see selectBaseURLContext).
func probeVersion(
	ctx context.Context, rt *module.Runtime, c connConfig, url string,
) (string, bool) {
	client, err := newAPIClient(rt, c, url)
	if err != nil {
		return "", false
	}
	resp, err := client.GetVersionWithResponse(ctx)
	if err != nil || resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return "", false
	}
	if resp.JSON200 != nil {
		return resp.JSON200.Version, true
	}
	return "", true
}

// probeCluster reports whether url's Control Plane has a cluster yet.
//
// It exists because doctor gave a clean bill of health to a Control
// Plane on which nothing works: reachable, correct version, and every
// other verb answering 409 (#255). The state is one call away.
//
// Three-valued on purpose. A caller cannot treat "not initialized" and
// "could not tell" alike: the first names a remedy, and the second
// must not, because telling an operator to run 'cluster init' against
// a cluster that may already exist is the one wrong answer here.
func probeCluster(
	ctx context.Context, rt *module.Runtime, c connConfig, url string,
) (initialized, known bool) {
	client, err := newAPIClient(rt, c, url)
	if err != nil {
		return false, false
	}
	resp, err := client.GetClusterWithResponse(ctx)
	if err != nil {
		return false, false
	}
	if resp.StatusCode() == http.StatusConflict &&
		isClusterNotInitialized(string(resp.Body)) {
		return false, true
	}
	// resp.JSON200, not any 2xx: a 200 carrying a non-JSON body left
	// the typed field nil, and reporting "initialized" from a body
	// never parsed claims knowledge the probe does not have. It is
	// the safe direction -- nobody is sent to cluster init -- but
	// "could not tell" is the honest answer, and it is what an empty
	// 200 body already produced.
	if resp.JSON200 != nil {
		return true, true
	}
	return false, false
}

// checkEmptyBodyResponse applies checkResponse to an UNTYPED generated
// response — the *http.Response from `client.JoinCluster(...)` rather
// than from `client.JoinClusterWithResponse(...)`.
//
// Operations whose success carries no body must call it this way. Every
// generated Parse*Response ends in a
// `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals the body into the spec's Error model for ANY status, 2xx
// included; where the operation also has no typed 2xx case, an
// empty-bodied success has nothing else to match, so json.Unmarshal of
// 0 bytes fails and the *WithResponse wrapper reports a call that
// SUCCEEDED as "unexpected end of JSON input".
//
// Bypassing only the response parser keeps the generated request
// builder, URL construction and parameter handling in play.
// checkResponse accepts any 2xx, so this is correct whether or not the
// server sends that Content-Type.
// TestControlplaneEmptyBodySuccessIsNotReportedAsFailure holds the contract.
func checkEmptyBodyResponse(resp *http.Response, action string) error {
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// A failure part-way through reading the response is a
		// transport failure, so it carries the same reachability hint
		// as one raised by the request itself.
		return networkError(action, err)
	}
	return checkResponse(resp.StatusCode, string(body))
}

// networkError wraps a transport-level failure with a human hint,
// since the Control Plane may simply be unreachable or mTLS may be
// misconfigured.
//
// A timeout gets a different hint. The reachability/mTLS one is not
// merely unhelpful there, it is wrong in the one direction that
// costs: the server answering slowly is reachable, and its TLS is
// fine, so the reader is sent to debug --ca-cert against a server
// that is working (#254). The remedy is --timeout, a flag on the same
// command.
// The CODE follows the hint. Stamping ExitGeneral on a timeout used to
// lose nothing, because a bare timeout was 1 everywhere; #352 made a
// timeout 3 across the CLI, so the same stamp would now DOWNGRADE this
// one and leave cp the only module answering 1 for the event every
// other module answers 3 for.
func networkError(action string, err error) error {
	hint := "(is the control-plane reachable at the configured " +
		"--base-url? for mTLS check --ca-cert/--client-cert or " +
		"use --insecure for a dev server)"
	code := ExitGeneral
	if isTimeout(err) {
		hint = "(the request timed out before the control-plane " +
			"answered, so it may be busy rather than unreachable: " +
			"--timeout bounds each request and --timeout 0 disables " +
			"that bound. A first 'database create' on a host that " +
			"has yet to pull the Postgres image is the usual way to " +
			"exceed it.)"
		code = ExitTimeout
	}
	return &ExitError{
		msg:  fmt.Sprintf("%s: %v\n%s", action, err, hint),
		code: code,
	}
}

// isTimeout reports whether err is a deadline rather than a failure to
// reach the server at all.
//
// net.Error.Timeout() and not a string match on "Client.Timeout
// exceeded": that text is Go's, it has already been reworded once
// between releases (#254 was filed against one wording and reproduced
// against another), and the interface is the contract.
//
// It does not distinguish the http client's own --timeout from a
// caller's context deadline, because as measured on go1.25.5 nothing
// does: errors.Is(err, context.DeadlineExceeded) is true for BOTH.
// So callers that impose their own deadline must report it themselves
// before an error reaches here — waitForTask and followTask both do,
// since naming --timeout for an expired --wait-timeout, or for
// followTask's own poll bound, would be this same bug wearing another
// flag.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
