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

// routeMissBody is the exact body Go's default mux serves for an
// unregistered path. A Control Plane answers a genuine resource miss
// with a JSON APIError (verified live), so this text means the server
// does not serve the endpoint at all: a CP older than the vendored
// spec, or a base URL that is not a CP API root.
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

// maxBodyExcerpt bounds how many characters of a response body reach
// the terminal in an error message. The errbody repair in httpClientFor
// routes non-JSON bodies here, so an unbounded branch would print a
// proxy's whole HTML interstitial; starfleet's identical constant exists
// because that has already happened once.
const maxBodyExcerpt = 512

// bodyExcerpt renders a response body for an error message, bounded at
// maxBodyExcerpt with credentials redacted. It wraps internal/httplog,
// which owns every never-print rule, rather than copying it: --debug
// dumps traffic through the same rules, and two implementations of that
// judgement is how one ends up wrong.
func bodyExcerpt(body string) string {
	return strings.TrimRight(
		httplog.RedactBody([]byte(body), maxBodyExcerpt), " \t\r\n")
}

// checkResponse maps a non-2xx HTTP status to a descriptive
// *ExitError, or returns nil on success. Messages show the excerpt, but
// classification reads the full body: the routeMissBody comparison is
// exact, and truncating first could hide a route miss.
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
						"404): %s",
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

// isClusterNotInitialized reports whether a 409 body is that error. It
// keys on the name, which is the contract, not the message prose. A
// body that does not parse falls to the generic 409 branch, the safe
// direction: a wrong "run cluster init" would send an operator to
// create a second cluster.
func isClusterNotInitialized(body string) bool {
	var e struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		return false
	}
	return e.Name == clusterNotInitialized
}

// storageKeyMissPattern matches the Control Plane storage layer's key
// miss: `failed to fetch <resource> from storage: "<path>": key not
// found`. The resource is left open because hosts, databases and
// clusters share that layer and its bug. It does not match "key not
// found" anywhere in a message: "failed to decrypt join token: signing
// key not found in vault" is broken key material, not a missing
// resource, and exit 4 would tell the operator "not found".
var storageKeyMissPattern = regexp.MustCompile(
	`^failed to fetch \S+ from storage: "[^"]+": key not found`)

// isStorageKeyNotFound reports whether a 500 body is a storage key miss
// reported as server_error. The message must match as well as the name,
// because server_error also covers real faults (a panic, a marshal
// failure) that must not become exit 4. Anything else falls to the
// generic 500 branch, the safe direction.
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
// increasing precedence, the profile and the controlplane root's
// persistent flags. It is the module's one connection resolver.
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

	// The profile value is parsed only when the flag is absent, so
	// --timeout stays the escape hatch from a profile timeout that will
	// not parse.
	if cmd.Flags().Changed("timeout") {
		if d, err := cmd.Flags().GetDuration("timeout"); err == nil {
			c.timeout = d
		}
		return c, nil
	}
	if p.Timeout != "" {
		d, err := time.ParseDuration(p.Timeout)
		if err != nil {
			// c is returned populated, with the 30s default standing:
			// request verbs propagate the error, while config view and
			// doctor show it as a row, since they are what an operator
			// runs to find out what is wrong with the profile.
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

// httpClientFor builds an *http.Client for c: TLS 1.3 always, mTLS when
// cert fields are set, and the internal/httplog diagnostic transport
// under --verbose or --debug.
func httpClientFor(
	rt *module.Runtime, c connConfig,
) (*http.Client, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	// The floor applies to every connection, plain https:// included. A
	// self-hosted Control Plane may sit behind a customer's
	// TLS-terminating proxy, so a modern server is not guaranteed;
	// pinning 1.3 removes the 1.2 downgrade and cipher-suite surface.
	// There is no escape hatch (internal/controlplane/llms.txt says so).
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
	// Outermost first: dryrun, errbody, httplog.
	//
	// dryrun sits outside httplog, so reads still log under
	// --verbose/--debug while an intercepted write is reported once, by
	// the dry-run report. mutatingGETs is required: two state-changing
	// Control Plane operations are GETs, and a method-only rule lets
	// `controlplane task cancel --dry-run` really cancel the task. Unlike
	// starfleet there is no auth carve-out, because mTLS authenticates in
	// the TLS config and no credential POST needs to get through.
	//
	// errbody: the generated client's Content-Type catch-all makes a
	// non-JSON error body served as JSON fail inside Parse*Response, so
	// checkResponse never runs. The Control Plane's own route miss is
	// text/plain and needs no repair; this is for a proxy or gateway in
	// front of it sending a mislabelled body, the shape seen against
	// Starfleet. It wraps httplog so --debug shows the Content-Type the
	// server really sent, not the repaired one.
	return &http.Client{
		Transport: dryrun.Wrap(
			errbody.Wrap(httplog.Wrap(base, rt.Stderr, lvl)), rt.DryRun,
			mutatingGETs...),
		Timeout: c.timeout,
	}, nil
}

// mutatingGETs are the paths of Control Plane operations that change
// state despite being declared GET; a dry run stops them as it stops a
// POST:
//
//   - GET /v1/cluster/init (operationId init-cluster)
//   - GET /v1/databases/{database_id}/tasks/{task_id}/cancel
//     (operationId cancel-database-task)
//
// GET /v1/cluster/join-token only reads a token and is deliberately
// absent, though its operationId contains "join".
// TestMutatingGETsCoverTheSpec re-derives the list from
// openapi/control-plane.json, so a re-vendor that adds another fails
// the build.
//
// Anchored at a segment boundary, not with ^: a --base-url may carry a
// path prefix (a reverse proxy at https://host/api/), and request paths
// resolve relative to it. Under a ^ anchor a prefixed base URL stops
// matching, and `task cancel --dry-run` really cancels the task.
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

// selectBaseURL is selectBaseURLContext with no bound on the failover
// walk beyond the per-request timeout: an ordinary verb's budget is per
// server, so N servers may cost N of them. Orchestrator detection in
// `database init` needs a total bound and passes its own deadline.
func selectBaseURL(rt *module.Runtime, c connConfig) (string, error) {
	return selectBaseURLContext(context.Background(), rt, c)
}

// selectBaseURLContext returns the Control Plane URL for this command.
// With 0 or 1 candidate it returns that URL without probing. With
// several it probes GET /v1/version in listed order and returns the
// first 2xx. If none answers, the error names every configured URL,
// including any skipped once ctx expired; no deadline-passing caller
// surfaces that error today. ctx bounds the whole walk, not each probe.
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

// probeVersion reports whether url's /v1/version answers 2xx, and the
// version it reports. The per-request timeout comes from c, as in
// version.go; ctx can shorten it (see selectBaseURLContext).
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

// probeCluster reports whether url's Control Plane has a cluster yet,
// so doctor does not pass a server on which every other verb answers
// 409. Three-valued because "could not tell" must not name the remedy:
// sending an operator to 'cluster init' against a cluster that may
// already exist is the one wrong answer.
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
	// JSON200, not any 2xx: a 200 whose body did not parse leaves it
	// nil, and "initialized" from an unparsed body claims knowledge the
	// probe lacks. An empty 200 body already answers "could not tell".
	if resp.JSON200 != nil {
		return true, true
	}
	return false, false
}

// checkEmptyBodyResponse applies checkResponse to an untyped generated
// response (client.JoinCluster, not JoinClusterWithResponse).
// Operations whose success carries no body must use it: each
// Parse*Response ends in a JSON Content-Type catch-all that unmarshals
// any status, 2xx included, into the Error model, so with no typed 2xx
// case an empty success fails as "unexpected end of JSON input".
// Skipping only the parser keeps the generated request builder.
// TestControlplaneEmptyBodySuccessIsNotReportedAsFailure holds this.
func checkEmptyBodyResponse(resp *http.Response, action string) error {
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// A read failure is a transport failure, with the same hint.
		return networkError(action, err)
	}
	return checkResponse(resp.StatusCode, string(body))
}

// networkError wraps a transport failure with a hint: the Control Plane
// may be unreachable or mTLS misconfigured. A timeout gets its own hint
// and exit 3, as in every other module: a slow server is reachable with
// working TLS, so the mTLS hint would send the reader to debug
// --ca-cert when the remedy is --timeout.
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
// reach the server. It uses net.Error.Timeout(), the contract, not Go's
// "Client.Timeout exceeded" text, which has been reworded between
// releases. It cannot tell --timeout from a caller's context deadline:
// on go1.25.5 errors.Is(err, context.DeadlineExceeded) is true for
// both. So a caller with its own deadline reports it first, as
// waitForTask and followTask do, or the hint names the wrong flag.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
