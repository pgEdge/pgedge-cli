// Package httplog renders HTTP requests and responses to a diagnostic
// stream at an ordered level, and is the one place in this CLI that
// decides what must never be printed. It is shared rather than copied
// per module because a masking fix landed in one copy and not another
// leaks credentials unnoticed.
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
// values are ordered, so callers compare rather than enumerate.
type Level int

const (
	// Off prints nothing.
	Off Level = iota
	// Verbose prints the request line, a masked Authorization header
	// when one is present, and the response status with elapsed time.
	// No bodies: they would bury the progress it exists to show.
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

// LevelFor maps the two global flags onto a level. --debug implies
// --verbose; that is the documented contract, and this is where it is
// defined.
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

// MaxDumpBytes bounds each dumped body; truncate counts it in
// characters. 8 KB rather than the 512 internal/starfleet/conn uses for
// one-line error excerpts, which would clip the payload --debug is
// enabled to read. Unbounded once landed a 200 KB proxy error page
// whole on stderr.
const MaxDumpBytes = 8192

// mask replaces a credential rather than eliding it, so the log shows
// that a header was present and carried something.
const mask = "████"

// maskedHeaders are the header names whose values are never printed, at
// any level. Keys are in textproto canonical form; HeaderLines
// canonicalises before the lookup.
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
// A gap here is a live credential echo: without the service-config
// names, `--debug` on a byoc or managed service write prints MCP bearer
// tokens, LLM API keys and the PostgREST signing secret in clear text,
// into a terminal, a CI log or a pasted issue.
//
// Matching is by EXACT key, which lets `token` sit here beside the
// deliberately unredacted `token_budget` (an int) and `token_type`
// ("Bearer"). A substring match would blank those too.
//
// TestEverySuspiciousAPIFieldIsClassified holds this list against the
// generated clients: any new field whose name looks credential-bearing
// must be added here or listed in notSecrets with a reason.
var secretFieldNames = []string{
	// Auth: the token endpoint and stored client credentials.
	"access_token", "refresh_token", "client_secret", "auth0_secret",
	// Database and role credentials.
	"password",
	// Cloud-provider credentials, byoc and controlplane.
	"credentials", "azure_key", "gcs_key", "s3_key", "s3_key_secret",
	// Service configs: MCP, RAG and PostgREST.
	"init_tokens", "init_users", "embedding_api_key", "api_key",
	"jwt_secret",
	// Invite and cluster-join tokens.
	"token",
}

// Wrap returns base wrapped so that traffic through it is rendered to
// out at lvl. At Off, or with no writer, it returns base itself: the
// quiet path is every normal command, and gets no extra layer.
func Wrap(base http.RoundTripper, out io.Writer,
	lvl Level) http.RoundTripper {
	if lvl <= Off || out == nil {
		return base
	}
	return &Transport{Base: base, Out: out, Level: lvl}
}

// Transport renders each exchange to Out. Use Wrap, so the Off case
// stays uniform.
type Transport struct {
	Base  http.RoundTripper
	Out   io.Writer
	Level Level
}

// RoundTrip renders req, delegates to Base, and renders the response.
// It never fails a request for a diagnostic reason.
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
// Exported so the dry-run report masks headers through the same rule;
// maskedHeaders has no other consumer. Sorted so two runs diff cleanly.
func HeaderLines(h http.Header) []string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		// A lower-case key in a hand-built http.Header map must not
		// slip a value through.
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

// writeResponseBody dumps resp's body and puts an equivalent reader
// back, so the generated response parsers still see an intact body and
// Content-Length stays honest.
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
	// No line for an empty body: a bare "< " reads as an empty string.
	if len(raw) > 0 {
		fmt.Fprintf(t.Out, "< %s\n", RedactBody(raw, MaxDumpBytes))
	}
}

// requestBody returns req's body for logging, and the request to send
// onward.
//
// It prefers GetBody, which leaves req untouched; the generated clients
// build requests over a bytes.Reader, so it is set. Without it the body
// is consumed and the replacement goes on a clone: RoundTrip must not
// modify its argument, and a retried mutated request would send an
// empty body.
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
		// Send what was read; the server's response is the better
		// error.
		return nil, clone
	}
	return raw, clone
}

// RedactBody renders a body for a diagnostic stream: bounded at limit
// characters, with the VALUE of every credential-bearing field masked
// and everything else passed through byte for byte.
//
// It does not decode and re-encode, because that erases the difference
// between an explicit `"plan_expires_at": null` and an omitted key,
// which a typed parser already collapses and the wire dump exists to
// reveal. The decoder only LOCATES each secret value's byte span.
//
// Masking per value rather than replacing the body keeps --debug
// useful: a service write reduced to its top-level keys is the single
// key "services", while per-value masking keeps allow_writes,
// embedding_model and the service id readable.
//
// Secret fields are masked at any depth, so a wrapper object cannot
// smuggle a token past, and a composite value (byoc's `credentials`) is
// masked whole. It fails CLOSED: if the spans cannot be computed, the
// whole body is replaced. HTML does not parse as JSON, so a proxy or
// SSO interstitial passes through untouched.
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
// in an object's key position; otherwise an array element equal to a
// secret field's name would be masked.
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
		// Consumed whole, so nested content is inside this span.
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

// truncate bounds raw at limit characters, never splitting a rune, and
// says it truncated so nobody reads a clipped body as the whole one. A
// byte-wise cut under-delivers on multi-byte text and can leave a
// replacement character at the seam.
func truncate(raw []byte, limit int) string {
	runes := []rune(string(raw))
	if len(runes) <= limit {
		return string(runes)
	}
	return fmt.Sprintf("%s… (truncated, %d bytes total)",
		string(runes[:limit]), len(raw))
}
