// Package dryrun records what a mutating command WOULD have sent, and
// stops it from being sent.
//
// The mechanism is a transport rather than a check at each call site,
// for the same reason internal/httplog is: there are two places in this
// CLI where an HTTP client is built, and roughly fifty verbs that issue
// writes through them. A per-verb flag test would be fifty chances to
// forget one. Intercepting below the generated clients also means the
// reported request is the ACTUAL bytes those clients assembled — a
// preview rendered independently at the command layer could drift from
// what the client really sends, and a dry run that lies about the
// request is worse than no dry run.
//
// The Run is the channel, not the error. RoundTrip must return an error
// to abort — returning a synthetic response would let the caller parse
// a fabricated body and report a fabricated result — but nothing
// downstream may be required to preserve that error. It cannot be:
// internal/controlplane/cmd.networkError formats its cause with %v into an
// ExitError carrying no Unwrap, so the chain is already gone by the
// time any write site returns. Callers therefore ask the Run whether an
// interception happened, and the error is only what unwound the stack.
package dryrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"

	"github.com/pgEdge/pgedge-cli/internal/httplog"
)

// Run is one dry run's state: the checks that passed, and the write
// that was stopped.
//
// A nil *Run means dry-run is off, and every method tolerates one. That
// is what lets check sites call Pass unconditionally instead of each
// guarding on a mode flag — fifty guarded call sites is fifty chances
// to write the guard backwards.
//
// The mutex is not speculative. Commands that poll (the --wait paths)
// run requests from more than one goroutine, and `make test` runs under
// -race, so an unsynchronised append here would surface as a flake in
// somebody else's test rather than as a bug in this file.
type Run struct {
	mu      sync.Mutex
	checks  []string
	request *Request
}

// Request is the write that would have been sent.
type Request struct {
	Method string
	// URL comes from url.URL.Redacted, so an --api-url carrying
	// userinfo credentials cannot be echoed back out of the report.
	URL    string
	Header http.Header
	// Body has already been through httplog.RedactBody: secret values
	// are masked and the whole thing is bounded. It is never the raw
	// payload, so no consumer of this field can leak one.
	Body []byte
	// BodyIsJSON reports whether Body still parses as JSON after
	// masking. It does not when RedactBody took its fail-closed path
	// and returned prose instead of a document, which is exactly when a
	// renderer must not present the value as JSON.
	BodyIsJSON bool
}

// New returns an active Run.
func New() *Run { return &Run{} }

// Pass records that a client-side check proved something. Callers state
// what was PROVED, in the past tense, naming the concrete subject:
// "database 7f3a5c1e-0000-4000-8000-000000000000 resolved"
// rather than "checked the name".
//
// It is a no-op on a nil receiver — the normal, non-dry-run path.
func (r *Run) Pass(format string, args ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, fmt.Sprintf(format, args...))
}

// Checks returns a copy of the ledger, in the order it was recorded.
// A copy because the caller is a renderer and the Run may still be
// written to by a goroutine that has not finished unwinding.
func (r *Run) Checks() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.checks...)
}

// Request returns the intercepted write, or nil if none happened.
func (r *Run) Request() *Request {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.request
}

// Intercepted reports whether a write was stopped. A true here is the
// command's outcome, whatever error the caller also received.
func (r *Run) Intercepted() bool {
	return r.Request() != nil
}

// record stores the first intercepted write and reports whether it was
// the first. First wins rather than last: no verb in the tree issues two
// writes today, and if one ever does, "we stopped at the first" is a
// true statement about what happened whereas a report showing the second
// would describe a request that could only have been built after the
// first had already succeeded — which it did not.
func (r *Run) record(req *Request) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.request != nil {
		return false
	}
	r.request = req
	return true
}

// Wrap returns base wrapped so that any request that changes state is
// recorded into run and never sent.
//
// mutatingGETs are path patterns for GET requests that change state
// anyway. THE METHOD IS NOT ENOUGH, and assuming it was shipped a bug:
// Control Plane declares `GET /v1/cluster/init` and `GET
// /v1/databases/{id}/tasks/{id}/cancel`, so a method-only rule let
// `pgedge controlplane task cancel --dry-run` really cancel the task and report
// success. Callers supply the patterns for their own API because the
// knowledge belongs to whoever vendored the spec — the starfleet module
// passes none, having no such operation in any of its three specs.
//
// With a nil run it returns base itself rather than a wrapper that
// decides to do nothing, matching httplog.Wrap: the quiet path is every
// normal command and should add neither an allocation nor a layer.
func Wrap(base http.RoundTripper, run *Run,
	mutatingGETs ...*regexp.Regexp) http.RoundTripper {
	if run == nil {
		return base
	}
	return &Transport{Base: base, Run: run, MutatingGETs: mutatingGETs}
}

// Transport intercepts writes. Use Wrap rather than constructing one
// directly, so the off case stays uniform.
type Transport struct {
	Base http.RoundTripper
	Run  *Run

	// MutatingGETs match the PATH of a GET request that changes state.
	// See Wrap.
	MutatingGETs []*regexp.Regexp
}

// reads reports whether req only reads, and so may be sent.
//
// A safe method is necessary but not sufficient: an API is free to
// declare a state-changing operation as GET, and Control Plane declares
// two. Checking the pattern list is what stops a dry run from carrying
// one out.
//
// EVERY PATTERN IS TESTED AGAINST BOTH SPELLINGS OF THE PATH, and it
// takes both. url.URL.Path is percent-DECODED, so a "/" inside a path
// parameter — a mistyped `--database "db1/"` reaches the wire as `db1%2F`
// — appears there as a real separator, splits a segment, and defeats a
// `[^/]+` in the pattern. EscapedPath() keeps it as `%2F` and matches.
// The reverse case exists too: a path written `/v1/cluster%2Finit`
// decodes to the covered path and matches only on Path. Matching on
// EITHER is what covers both, and "when in doubt, treat it as a write"
// is the correct bias for this feature.
//
// Anything that fails to match still gets sent, so the decision here is
// fail-OPEN by construction. That is why the patterns are deliberately
// generous rather than exact.
func (t *Transport) reads(req *http.Request) bool {
	if !safeMethods[req.Method] {
		return false
	}
	decoded, escaped := req.URL.Path, req.URL.EscapedPath()
	for _, re := range t.MutatingGETs {
		if re.MatchString(decoded) || re.MatchString(escaped) {
			return false
		}
	}
	return true
}

// safeMethods are the methods that, by RFC, do not change state. They
// are a starting point, not the rule — see reads.
//
// The reads are the point: the checks worth running are the ones that
// need one. The create-vs-reconfigure intent guard can only tell deploy
// from update after fetching the database, and it is the only thing
// standing between `mcp deploy --allow-writes` and a silent privilege
// escalation on a read-only service. A dry run that skipped it
// would print a confident request preview with the sharpest check
// missing.
//
// TRACE is absent deliberately: it is safe by RFC, and nothing in this
// CLI issues one. Listing it would imply somewhere does.
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// RoundTrip sends requests that only read and stops everything else.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.reads(req) {
		base := t.Base
		if base == nil {
			base = http.DefaultTransport
		}
		return base.RoundTrip(req)
	}

	rec := &Request{
		Method: req.Method,
		URL:    req.URL.Redacted(),
		Header: req.Header.Clone(),
	}
	// A bodyless write (most DELETEs) leaves Body nil rather than an
	// empty slice, so a renderer can tell "there was no body" from
	// "the body was the empty string" — RedactBody returns "" for both,
	// and they are different facts.
	if raw := readBodyCopy(req); len(raw) > 0 {
		masked := []byte(httplog.RedactBody(raw, httplog.MaxDumpBytes))
		rec.Body = masked
		rec.BodyIsJSON = json.Valid(masked)
	}
	t.Run.record(rec)

	// The report replaces this message in every normal path, so it is
	// not what the user reads. It still has to stand on its own: if some
	// command ever swallowed the Run and only the error survived, this
	// is the whole explanation the user would get.
	return nil, fmt.Errorf("dry run: %s %s was not sent",
		req.Method, req.URL.Redacted())
}

// readBodyCopy returns a copy of req's body, leaving req untouched.
//
// It prefers GetBody, which hands back an independent reader — the
// generated oapi-codegen clients always set it, since they build
// requests over a bytes.Reader. The fallback consumes req.Body and puts
// an equivalent reader back, because RoundTrip is documented as not
// modifying its argument and a caller that retried would otherwise send
// an empty body.
//
// A body it cannot read yields nil rather than an error: the request is
// already being stopped, and failing the dry run over a diagnostic read
// would report nothing at all when reporting the method and URL is
// still useful.
func readBodyCopy(req *http.Request) []byte {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil
		}
		defer func() { _ = rc.Close() }()
		raw, err := io.ReadAll(rc)
		if err != nil {
			return nil
		}
		return raw
	}

	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	return raw
}
