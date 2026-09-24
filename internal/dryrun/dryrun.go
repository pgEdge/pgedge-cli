// Package dryrun records what a mutating command WOULD have sent, and
// stops it from being sent.
//
// It is a transport rather than a check at each call site because the
// CLI builds its HTTP client in two places but writes from dozens of
// verbs, and because the reported request is then the bytes the
// generated client really assembled, not a preview that could drift.
//
// The Run is the channel, not the error. RoundTrip must return an error
// to abort — a synthetic response would be parsed as a real result —
// but the error cannot be relied on to survive: controlplane's
// networkError formats its cause with %v into an ExitError with no
// Unwrap. Callers ask the Run whether an interception happened.
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
// A nil *Run means dry-run is off, and every method tolerates one, so
// check sites call Pass unconditionally rather than each guarding on a
// mode flag.
//
// The mutex is needed: the --wait paths issue requests from more than
// one goroutine.
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
	// Body has been through httplog.RedactBody, so secret values are
	// masked and the length is bounded; it is never the raw payload.
	Body []byte
	// BodyIsJSON reports whether Body still parses as JSON. It is
	// false for a body that never was JSON, one truncated at
	// httplog.MaxDumpBytes, and RedactBody's fail-closed prose summary.
	BodyIsJSON bool
}

// New returns an active Run.
func New() *Run { return &Run{} }

// Pass records that a client-side check proved something. Callers state
// what was PROVED, in the past tense, naming the concrete subject:
// "database 7f3a5c1e-0000-4000-8000-000000000000 resolved"
// rather than "checked the name".
func (r *Run) Pass(format string, args ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, fmt.Sprintf(format, args...))
}

// Checks returns a copy of the ledger, in recorded order: a goroutine
// still unwinding may write to the Run while the renderer reads it.
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
// the first. First wins: a second write could only have been built
// after the first succeeded, which in a dry run it did not.
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
// anyway: Control Plane declares `GET /v1/cluster/init` and `GET
// /v1/databases/{id}/tasks/{id}/cancel`, so a method-only rule would
// let `task cancel --dry-run` really cancel. Each caller supplies the
// patterns for the spec it vendors; starfleet's three specs have none.
//
// With a nil run it returns base itself, as httplog.Wrap does, so a
// normal command gets no extra layer.
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

// reads reports whether req only reads, and so may be sent. Reads are
// sent so the checks that need one still run: the create-vs-reconfigure
// guard that stops `mcp deploy --allow-writes` escalating a read-only
// service can only decide after fetching the database.
//
// Each pattern is tried on both spellings of the path. Path is decoded,
// so a mistyped `--database "db1/"` (`db1%2F` on the wire) splits a
// segment and defeats a `[^/]+`; EscapedPath keeps `%2F` and matches.
// Conversely `/v1/cluster%2Finit` matches only on Path.
//
// A request no pattern matches is sent, so this fails open, which is
// why the patterns are generous rather than exact.
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

// safeMethods are the methods that, by RFC, do not change state — a
// starting point, not the rule; see reads. TRACE is left out because
// nothing in this CLI issues one.
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
	// A bodyless write leaves Body nil, not empty: RedactBody returns
	// "" for both, and a renderer must tell them apart.
	if raw := readBodyCopy(req); len(raw) > 0 {
		masked := []byte(httplog.RedactBody(raw, httplog.MaxDumpBytes))
		rec.Body = masked
		rec.BodyIsJSON = json.Valid(masked)
	}
	t.Run.record(rec)

	// The report normally replaces this message, but it must stand
	// alone for a command that loses the Run.
	return nil, fmt.Errorf("dry run: %s %s was not sent",
		req.Method, req.URL.Redacted())
}

// readBodyCopy returns a copy of req's body, leaving req untouched.
//
// It prefers GetBody, which http.NewRequest sets for the bytes.Reader
// the generated clients' typed calls pass. The fallback puts an equivalent
// reader back, because RoundTrip must not modify its argument.
//
// An unreadable body yields nil rather than an error, so the report
// still carries the method and URL.
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
