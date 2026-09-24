// Package httplog renders HTTP requests and responses to a diagnostic
// stream at an ordered level, and is the one place in this CLI that
// decides what must never be printed.
//
// It exists as a shared package rather than a helper in each module
// because two of the three rules here are security rules. A masking bug
// fixed in one copy and not the other is invisible until it isn't, and
// per-module copies of shared rules have drifted in this repo before —
// that is what internal/testsupport exists to prevent for env lists.
package httplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// Level is how much of an exchange reaches the diagnostic stream. The
// values are ordered, so a comparison against Debug is meaningful and
// callers never have to enumerate.
type Level int

const (
	// Off prints nothing.
	Off Level = iota
	// Verbose prints the request line, a masked Authorization header
	// when one is present, and the response status with elapsed time.
	// It deliberately carries no bodies: a verbose run that dumped
	// payloads would bury the progress information it exists to show.
	Verbose
	// Debug prints everything Verbose does, plus every request and
	// response header and both bodies.
	Debug
)

// String names the level for diagnostics and test output.
func (l Level) String() string {
	switch l {
	case Off:
		return "off"
	case Verbose:
		return "verbose"
	case Debug:
		return "debug"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// LevelFor maps the two global flags onto a level. **--debug implies
// --verbose**: that ordering is the documented contract, and stating it
// is what stops `--verbose --debug` from being a combination nobody can
// predict. Two booleans where one is a superset of the other are only
// coherent if the superset relation is defined somewhere, and this is
// where.
func LevelFor(verbose, debug bool) Level {
	switch {
	case debug:
		return Debug
	case verbose:
		return Verbose
	default:
		return Off
	}
}

// MaxDumpBytes bounds each dumped body.
//
// 8 KB rather than the 512 that internal/starfleet/conn uses for error
// messages: that cap is right for quoting what a proxy said in a
// one-line error, and wrong here, where it would clip the middle of the
// cluster payload somebody enabled --debug to read. Unbounded is not an
// option either — an unbounded echo is a bug this repo has already
// fixed once, when a 200 KB proxy error page landed whole on stderr.
const MaxDumpBytes = 8192

// mask replaces a credential rather than eliding it, so the log shows
// that a header was present and carried something.
const mask = "████"

// maskedHeaders are the header names whose values are never printed, at
// any level. Keys are in textproto canonical form; lookups canonicalise
// first, so a lower-case or oddly-cased spelling in a hand-built
// http.Header map cannot slip a value through.
var maskedHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"X-Api-Key":           true,
}

// secretFieldNames are the JSON object keys whose presence, at any
// depth, makes a body unsafe to echo into a diagnostic stream.
//
// A gap here is a live credential echo: until the service-config names
// joined this list, `--debug` on a byoc or managed service write
// printed MCP bearer tokens, LLM API keys and the PostgREST signing
// secret in clear text — into a terminal, a CI log, or a pasted issue.
//
// Matching is by EXACT key, which is what lets `token` sit here beside
// the deliberately-unredacted `token_budget` (an int) and `token_type`
// ("Bearer"). Adding a substring match would blank bodies that carry
// neither.
//
// TestEverySuspiciousAPIFieldIsClassified holds this list against the
// generated clients: any new field whose name looks credential-bearing
// must be added here or listed in notSecrets with a reason.
var secretFieldNames = []string{
	// Auth: the token endpoint and stored client credentials.
	"access_token", "refresh_token", "client_secret", "auth0_secret",
	// Database and role credentials.
	"password",
	// Cloud-provider credentials, byoc and cp.
	"credentials", "azure_key", "gcs_key", "s3_key", "s3_key_secret",
	// Service configs: MCP, RAG and PostgREST.
	"init_tokens", "init_users", "embedding_api_key", "api_key",
	"jwt_secret",
	// Invite and cluster-join tokens.
	"token",
}

// Wrap returns base wrapped so that traffic through it is rendered to
// out at lvl. At Off, or with no writer, it returns base itself rather
// than a wrapper that decides to print nothing — the quiet path is every
// normal command, and it should add neither an allocation nor a layer of
// indirection.
func Wrap(base http.RoundTripper, out io.Writer,
	lvl Level) http.RoundTripper {
	if lvl <= Off || out == nil {
		return base
	}
	return &Transport{Base: base, Out: out, Level: lvl}
}

// Transport renders each exchange to Out. Use Wrap rather than
// constructing one directly, so the Off case stays uniform.
type Transport struct {
	Base  http.RoundTripper
	Out   io.Writer
	Level Level
}

// RoundTrip renders req, delegates to Base, and renders the response.
//
// It never fails a request for a diagnostic reason: a body it cannot
// read for the dump is reported as unavailable and the exchange
// continues.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	// Redacted() masks any userinfo password, which an --api-url of the
	// form https://id:secret@host would otherwise print here.
	fmt.Fprintf(t.Out, "> %s %s\n", req.Method, req.URL.Redacted())

	send := req
	if t.Level >= Debug {
		t.writeHeaders(">", req.Header)
		var raw []byte
		raw, send = requestBody(req)
		if len(raw) > 0 {
			fmt.Fprintf(t.Out, "> %s\n", RedactBody(raw, MaxDumpBytes))
		}
	} else if req.Header.Get("Authorization") != "" {
		fmt.Fprintf(t.Out, "> Authorization: %s\n", mask)
	}

	start := time.Now()
	resp, err := base.RoundTrip(send)
	elapsed := time.Since(start).Round(time.Millisecond)

	if err != nil {
		fmt.Fprintf(t.Out, "< error: %v (%s)\n", err, elapsed)
		return nil, err
	}

	fmt.Fprintf(t.Out, "< %d %s (%s)\n", resp.StatusCode,
		http.StatusText(resp.StatusCode), elapsed)
	if t.Level >= Debug {
		t.writeHeaders("<", resp.Header)
		t.writeResponseBody(resp)
	}
	return resp, nil
}

// HeaderLines renders h as "Name: value" lines in sorted order, with
// the values of maskedHeaders replaced by the mask. Callers add their
// own prefix.
//
// It is exported because the dry-run report renders headers too, and
// this package's whole reason for existing is that a masking rule with
// two implementations has one that is wrong. maskedHeaders keeps exactly
// one consumer path: this function.
//
// Sorting is for reproducibility: Go's header map has no order, and an
// unordered dump is hard to diff between two runs.
func HeaderLines(h http.Header) []string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		// Canonicalise before the lookup, so a lower-case or oddly-cased
		// spelling in a hand-built http.Header map cannot slip a value
		// through.
		if maskedHeaders[textproto.CanonicalMIMEHeaderKey(name)] {
			lines = append(lines, fmt.Sprintf("%s: %s", name, mask))
			continue
		}
		for _, v := range h[name] {
			lines = append(lines, fmt.Sprintf("%s: %s", name, v))
		}
	}
	return lines
}

// writeHeaders renders h to the diagnostic stream behind prefix.
func (t *Transport) writeHeaders(prefix string, h http.Header) {
	for _, line := range HeaderLines(h) {
		fmt.Fprintf(t.Out, "%s %s\n", prefix, line)
	}
}

// writeResponseBody dumps resp's body and replaces it with an equivalent
// reader, so every downstream consumer — the generated response parsers
// included — still sees an intact body. Reading it here without putting
// it back would break every command at exactly the moment somebody
// turned --debug on to diagnose one.
//
// The bytes are unchanged, so Content-Length stays honest.
func (t *Transport) writeResponseBody(resp *http.Response) {
	if resp.Body == nil {
		return
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintf(t.Out, "< <body unavailable: %v>\n", err)
		return
	}
	// No line at all for an empty body: "< " on its own reads as "the
	// server sent an empty string", which is a different fact.
	if len(raw) > 0 {
		fmt.Fprintf(t.Out, "< %s\n", RedactBody(raw, MaxDumpBytes))
	}
}

// requestBody returns req's body for logging, and the request to send
// onward.
//
// It prefers GetBody, which hands back an independent copy and leaves
// req untouched — the generated oapi-codegen clients always set it,
// since they build requests over a bytes.Reader. When GetBody is absent
// the body can only be read by consuming it, so the request is cloned
// and the replacement given to the clone: RoundTrip is documented as not
// modifying its argument, and a caller that retried a mutated request
// would send an empty body the second time.
func requestBody(req *http.Request) ([]byte, *http.Request) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, req
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, req
		}
		defer func() { _ = rc.Close() }()
		raw, err := io.ReadAll(rc)
		if err != nil {
			return nil, req
		}
		return raw, req
	}

	raw, readErr := io.ReadAll(req.Body)
	_ = req.Body.Close()
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(raw))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(raw)), nil
	}
	if readErr != nil {
		// Send what was read rather than failing the request for a
		// diagnostic reason; the server's own response is the better
		// error.
		return nil, clone
	}
	return raw, clone
}

// RedactBody renders a body for a diagnostic stream: bounded at limit
// characters, with the VALUE of every credential-bearing field masked
// and everything else passed through byte for byte.
//
// Byte-for-byte outside the masked spans is essential, and it is why
// this does not decode-and-re-encode. Re-encoding erases the difference
// between an explicit `"plan_expires_at": null` and an omitted key,
// which is precisely the distinction a typed parser already collapses
// and the wire dump exists to reveal. So the decoder here is used only
// to LOCATE each secret value's byte span; the surrounding bytes are
// never rewritten, reordered or reformatted.
//
// Masking values rather than replacing the whole body is what keeps
// --debug useful. A service write reduced to its top-level key names
// is the single key "services" — enough to know a secret was present,
// not enough to debug anything. Masking per value keeps allow_writes,
// embedding_model and the service id readable while the key beside
// them is not.
//
// Every field in secretFieldNames is masked at any depth: a wrapper
// object would otherwise smuggle a token past a top-level-only check.
// A composite value (an object, as with byoc's `credentials`) is masked
// whole, so nothing nested inside one can survive.
//
// It fails CLOSED. If the spans cannot be computed for any reason, it
// falls back to replacing the whole body rather than risking an echo.
//
// HTML does not parse as JSON, so the proxy/SSO-interstitial case that
// motivates dumping bodies at all is untouched.
func RedactBody(raw []byte, limit int) string {
	var doc any
	if json.Unmarshal(raw, &doc) != nil || !carriesSecret(doc) {
		return truncate(raw, limit)
	}

	if masked, err := maskSecretValues(raw); err == nil {
		return truncate(masked, limit)
	}
	return wholeBodySummary(doc)
}

// wholeBodySummary is the fail-closed fallback: it says what shape
// arrived without saying what was in it.
func wholeBodySummary(doc any) string {
	obj, _ := doc.(map[string]any)
	if len(obj) == 0 {
		return "<redacted: the body carries a credential field>"
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("<redacted: the body carries a credential "+
		"field; top-level keys: %s>", strings.Join(keys, ", "))
}

// maskSecretValues returns raw with each secret field's value replaced
// by a quoted mask, and every other byte untouched.
func maskSecretValues(raw []byte) ([]byte, error) {
	spans, err := secretValueSpans(raw)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		// carriesSecret said yes and the walk found nothing: the two
		// disagree, so do not guess.
		return nil, errors.New("no secret value spans located")
	}

	replacement := []byte(`"` + mask + `"`)
	out := make([]byte, 0, len(raw))
	prev := 0
	for _, s := range spans {
		if s.start < prev || s.end > len(raw) || s.start >= s.end {
			return nil, errors.New("secret value span out of range")
		}
		out = append(out, raw[prev:s.start]...)
		out = append(out, replacement...)
		prev = s.end
	}
	return append(out, raw[prev:]...), nil
}

// span is a half-open byte range within a body.
type span struct{ start, end int }

// secretValueSpans locates the byte range of every secret field's value.
//
// The walk tracks container context because a JSON string is a KEY only
// in an object and only in the key position; the same bytes inside an
// array are a value. Getting that wrong would mask an array element
// that merely happens to equal a secret field's name.
func secretValueSpans(raw []byte) ([]span, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	// frame is one open container. expectKey is meaningful only when
	// inObject.
	type frame struct {
		inObject  bool
		expectKey bool
	}
	var stack []frame
	var spans []span

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{':
				stack = append(stack, frame{inObject: true, expectKey: true})
			case '[':
				stack = append(stack, frame{})
			case '}', ']':
				if len(stack) == 0 {
					return nil, errors.New("unbalanced JSON")
				}
				stack = stack[:len(stack)-1]
				if n := len(stack) - 1; n >= 0 && stack[n].inObject {
					stack[n].expectKey = true
				}
			}
			continue
		}

		n := len(stack) - 1
		if n < 0 {
			// A bare scalar document. Nothing to key off.
			continue
		}
		if !stack[n].inObject {
			continue // an array element
		}
		if !stack[n].expectKey {
			stack[n].expectKey = true // that was a value
			continue
		}

		// This token is an object key.
		key, _ := tok.(string)
		stack[n].expectKey = false
		if !isSecretField(key) {
			continue
		}

		start := valueStart(raw, int(dec.InputOffset()))
		if err := skipOneValue(dec); err != nil {
			return nil, err
		}
		spans = append(spans, span{start: start, end: int(dec.InputOffset())})
		// The value was consumed whole, so the next token is a key
		// again — and anything nested inside it is covered by this span.
		stack[n].expectKey = true
	}
	return spans, nil
}

// isSecretField reports whether key is one of secretFieldNames. Exact
// match: see the note on that list.
func isSecretField(key string) bool {
	for _, name := range secretFieldNames {
		if key == name {
			return true
		}
	}
	return false
}

// valueStart advances past the whitespace and the single colon that
// separate a key from its value.
func valueStart(raw []byte, from int) int {
	for i := from; i < len(raw); i++ {
		switch raw[i] {
		case ' ', '\t', '\n', '\r', ':':
			continue
		default:
			return i
		}
	}
	return len(raw)
}

// skipOneValue consumes exactly one complete JSON value, descending
// through a composite so a nested object is consumed whole.
func skipOneValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok || (d != '{' && d != '[') {
		return nil // a scalar
	}
	for depth := 1; depth > 0; {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if dd, ok := t.(json.Delim); ok {
			switch dd {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// carriesSecret reports whether a decoded JSON document contains any of
// secretFieldNames as an object key, at any depth.
func carriesSecret(doc any) bool {
	switch v := doc.(type) {
	case map[string]any:
		for key, child := range v {
			for _, name := range secretFieldNames {
				if key == name {
					return true
				}
			}
			if carriesSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if carriesSecret(child) {
				return true
			}
		}
	}
	return false
}

// truncate bounds raw at limit *characters*, cutting on a rune boundary
// so a multi-byte character is never split, and says that it truncated
// so nobody reads a clipped body as the whole one.
//
// Characters, not bytes: this prose is full of em dashes at 3 bytes
// each, and a byte-wise cut both under-delivers against the limit and
// can leave a replacement character at the seam.
func truncate(raw []byte, limit int) string {
	runes := []rune(string(raw))
	if len(runes) <= limit {
		return string(runes)
	}
	return fmt.Sprintf("%s… (truncated, %d bytes total)",
		string(runes[:limit]), len(raw))
}
