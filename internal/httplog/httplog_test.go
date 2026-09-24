package httplog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sort"
	"strings"
	"testing"
)

func TestLevelFor(t *testing.T) {
	tests := []struct {
		name    string
		verbose bool
		debug   bool
		want    Level
	}{
		{name: "neither", want: Off},
		{name: "verbose only", verbose: true, want: Verbose},
		// --debug implies --verbose: the ordering is the documented
		// contract, so `--debug` alone must not be weaker than
		// `--verbose` alone.
		{name: "debug only", debug: true, want: Debug},
		{name: "both", verbose: true, debug: true, want: Debug},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LevelFor(tt.verbose, tt.debug); got != tt.want {
				t.Errorf("LevelFor(%v, %v) = %v, want %v",
					tt.verbose, tt.debug, got, tt.want)
			}
		})
	}
}

// Off must return the base transport itself, not a wrapper that decides
// to print nothing: the non-diagnostic path is every normal command, and
// it should allocate nothing and add no indirection.
func TestWrapOffReturnsBase(t *testing.T) {
	base := http.DefaultTransport
	if got := Wrap(base, io.Discard, Off); got != base {
		t.Errorf("Wrap at Off = %T, want the base transport itself", got)
	}
	if got := Wrap(base, nil, Debug); got != base {
		t.Errorf("Wrap with a nil writer = %T, want the base "+
			"transport itself", got)
	}
}

// newServer returns a stub that answers every request with status and
// body, plus the captured request body.
func newServer(t *testing.T, status int, body string,
) (url string, gotBody *string) {
	t.Helper()
	captured := ""
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			captured = string(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
	t.Cleanup(srv.Close)
	return srv.URL, &captured
}

// do drives one request through a Transport at lvl and returns the log
// and the response body as the caller downstream would see it.
func do(t *testing.T, lvl Level, req *http.Request,
) (log, respBody string) {
	t.Helper()
	var buf strings.Builder
	c := &http.Client{
		Transport: Wrap(http.DefaultTransport, &buf, lvl),
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return buf.String(), string(raw)
}

func getReq(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// Verbose output is the existing contract, unchanged: request line,
// masked Authorization when present, status. It must NOT carry headers
// or bodies — that is what distinguishes it from Debug, and a Verbose
// run that dumped payloads would bury its own output.
func TestVerboseShapeUnchanged(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{"id":"abc"}`)
	req := getReq(t, url)
	req.Header.Set("Authorization", "Bearer super-secret")
	req.Header.Set("Accept", "application/json")

	log, _ := do(t, Verbose, req)

	for _, want := range []string{
		"> GET " + url, "> Authorization: " + mask, "< 200 OK",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log = %q, want %q", log, want)
		}
	}
	for _, unwanted := range []string{
		"super-secret", `{"id":"abc"}`, "> Accept:",
	} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, must not contain %q", log, unwanted)
		}
	}
}

// TestHeaderLinesMasksCredentialHeaders covers the exported helper
// directly, including the case the Transport path cannot reach: a
// hand-built header map with a non-canonical key. The dry-run report
// calls this, so a canonicalisation regression here would leak a
// bearer token into a report rather than into a --debug log.
func TestHeaderLinesMasksCredentialHeaders(t *testing.T) {
	h := http.Header{
		"authorization": {"Bearer super-secret"},
		"Content-Type":  {"application/json"},
		"X-Api-Key":     {"key-abc"},
		"Accept":        {"application/json", "text/plain"},
	}
	lines := HeaderLines(h)
	joined := strings.Join(lines, "\n")

	for _, leak := range []string{"super-secret", "key-abc"} {
		if strings.Contains(joined, leak) {
			t.Errorf("lines = %q, leaked %q", joined, leak)
		}
	}
	for _, want := range []string{
		"authorization: " + mask,
		"X-Api-Key: " + mask,
		"Content-Type: application/json",
		"Accept: application/json",
		"Accept: text/plain",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("lines = %q, want %q", joined, want)
		}
	}
	if !sort.SliceIsSorted(lines, func(i, j int) bool {
		return lines[i] < lines[j]
	}) {
		t.Errorf("lines are not sorted: %q", lines)
	}
}

// TestHeaderLinesMasksOnceRegardlessOfValueCount pins the behaviour the
// Transport had before HeaderLines was extracted: a masked header
// contributes one line, not one per value, so the count of values it
// carried is not itself disclosed.
func TestHeaderLinesMasksOnceRegardlessOfValueCount(t *testing.T) {
	h := http.Header{"Cookie": {"a=1", "b=2", "c=3"}}
	if got := HeaderLines(h); len(got) != 1 {
		t.Errorf("HeaderLines = %q, want a single masked line", got)
	}
}

func TestHeaderLinesEmpty(t *testing.T) {
	if got := HeaderLines(http.Header{}); len(got) != 0 {
		t.Errorf("HeaderLines = %q, want none", got)
	}
}

func TestDebugDumpsHeadersAndBodies(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{"id":"abc"}`)
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, url, strings.NewReader(`{"name":"prod"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer super-secret")
	req.Header.Set("Accept", "application/json")

	log, _ := do(t, Debug, req)

	for _, want := range []string{
		"> POST " + url,
		"> Accept: application/json",
		"> Authorization: " + mask,
		`> {"name":"prod"}`,
		"< 200 OK",
		"< Content-Type: application/json",
		`< {"id":"abc"}`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log = %q, want %q", log, want)
		}
	}
	if strings.Contains(log, "super-secret") {
		t.Errorf("log = %q, leaked the bearer token", log)
	}
}

// The whole reason the flag exists: an explicit null must be
// distinguishable from an omitted key. Anything that re-serialised the
// body through a decoder would erase exactly this difference, so it is
// asserted on the dump rather than on the parsed value.
func TestDebugDistinguishesNullFromOmitted(t *testing.T) {
	t.Run("explicit null is shown", func(t *testing.T) {
		url, _ := newServer(t, http.StatusOK,
			`{"id":"abc","plan_expires_at":null}`)
		log, _ := do(t, Debug, getReq(t, url))
		if !strings.Contains(log, `"plan_expires_at":null`) {
			t.Errorf("log = %q, want the explicit null", log)
		}
	})
	t.Run("omitted key is absent", func(t *testing.T) {
		url, _ := newServer(t, http.StatusOK, `{"id":"abc"}`)
		log, _ := do(t, Debug, getReq(t, url))
		if strings.Contains(log, "plan_expires_at") {
			t.Errorf("log = %q, invented an omitted key", log)
		}
	})
}

// Dumping must not consume what the caller reads next. The generated
// parsers read the body after the transport returns, so a transport that
// left it drained would break every command at exactly the moment
// someone turned --debug on to diagnose one.
func TestBodyReachesTheCallerIntact(t *testing.T) {
	const want = `{"id":"abc","plan_expires_at":null}`
	for _, lvl := range []Level{Off, Verbose, Debug} {
		t.Run(lvl.String(), func(t *testing.T) {
			url, _ := newServer(t, http.StatusOK, want)
			_, got := do(t, lvl, getReq(t, url))
			if got != want {
				t.Errorf("downstream body = %q, want %q", got, want)
			}
		})
	}
}

// The request body must survive too, and must not be consumed by the
// dump: the server has to receive it.
func TestRequestBodyReachesTheServer(t *testing.T) {
	const want = `{"name":"prod"}`
	url, captured := newServer(t, http.StatusOK, `{}`)
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, url, strings.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	do(t, Debug, req)
	if *captured != want {
		t.Errorf("server received %q, want %q", *captured, want)
	}
}

// A request with a body but no GetBody cannot be logged without
// consuming it, so the transport clones. RoundTrip is documented as not
// modifying its argument, and a caller that retries would otherwise
// send an empty body the second time.
func TestRequestWithoutGetBodyIsNotMutated(t *testing.T) {
	const want = `{"name":"prod"}`
	url, captured := newServer(t, http.StatusOK, `{}`)
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, url, strings.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = nil
	origBody := req.Body

	var buf strings.Builder
	c := &http.Client{
		Transport: Wrap(http.DefaultTransport, &buf, Debug),
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if *captured != want {
		t.Errorf("server received %q, want %q", *captured, want)
	}
	if !strings.Contains(buf.String(), "> "+want) {
		t.Errorf("log = %q, want the request body", buf.String())
	}
	if req.Body != origBody {
		t.Error("RoundTrip replaced req.Body; it must clone instead")
	}
}

// A userinfo password in the base URL must not reach the log. `--api-url
// https://id:secret@host` is a plausible thing for someone to type, and
// url.Redacted() is what keeps it out of the request line.
func TestURLUserinfoIsRedacted(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{}`)
	for _, lvl := range []Level{Verbose, Debug} {
		t.Run(lvl.String(), func(t *testing.T) {
			req := getReq(t, url)
			req.URL.User = neturl.UserPassword("id", "urlsecret")
			log, _ := do(t, lvl, req)
			if strings.Contains(log, "urlsecret") {
				t.Errorf("log = %q, leaked the userinfo password", log)
			}
			if !strings.Contains(log, "xxxxx") {
				t.Errorf("log = %q, want Redacted()'s placeholder", log)
			}
		})
	}
}

// A request with no Authorization header must not gain a redaction line
// it never earned — that would report a credential where there is none.
func TestAbsentAuthHeaderPrintsNoLine(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{}`)
	log, _ := do(t, Verbose, getReq(t, url))
	if strings.Contains(log, "Authorization") {
		t.Errorf("log = %q, want no Authorization line", log)
	}
}

// A transport error keeps the existing shape and propagates unchanged.
func TestTransportErrorIsReportedAndPropagated(t *testing.T) {
	req := getReq(t, "http://127.0.0.1:1")
	var buf strings.Builder
	c := &http.Client{
		Transport: Wrap(http.DefaultTransport, &buf, Debug),
	}
	if _, err := c.Do(req); err == nil {
		t.Fatal("want a transport error")
	}
	if !strings.Contains(buf.String(), "< error:") {
		t.Errorf("log = %q, want an error line", buf.String())
	}
}

// fakeBase lets a test drive RoundTrip against a response it controls,
// including one whose body fails to read.
type fakeBase struct {
	resp *http.Response
	err  error
	got  *http.Request
}

func (f *fakeBase) RoundTrip(req *http.Request) (*http.Response, error) {
	f.got = req
	return f.resp, f.err
}

// errReader yields prefix and then fails, standing in for a truncated
// or reset response.
type errReader struct {
	prefix string
	n      int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.n < len(e.prefix) {
		n := copy(p, e.prefix[e.n:])
		e.n += n
		return n, nil
	}
	return 0, io.ErrUnexpectedEOF
}

func (e *errReader) Close() error { return nil }

// A body that cannot be read for the dump must not fail the request.
// The dump says so, the request still succeeds, and what *was* read
// still reaches the caller — a diagnostic that turned a working command
// into a failing one would be worse than no diagnostic.
func TestUnreadableResponseBodyDoesNotFailTheRequest(t *testing.T) {
	base := &fakeBase{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       &errReader{prefix: `{"id":"ab`},
	}}
	var buf strings.Builder
	tr := &Transport{Base: base, Out: &buf, Level: Debug}

	resp, err := tr.RoundTrip(getReq(t, "http://example.invalid"))
	if err != nil {
		t.Fatalf("RoundTrip failed on an unreadable body: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if !strings.Contains(buf.String(), "body unavailable") {
		t.Errorf("log = %q, want it to say the body was unavailable",
			buf.String())
	}
	if string(raw) != `{"id":"ab` {
		t.Errorf("downstream body = %q, want the bytes that did read",
			raw)
	}
}

// A GetBody that fails is the same class: log nothing, send the request.
func TestFailingGetBodyStillSendsTheRequest(t *testing.T) {
	base := &fakeBase{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
	}}
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "http://example.invalid",
		strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, io.ErrUnexpectedEOF
	}

	var buf strings.Builder
	tr := &Transport{Base: base, Out: &buf, Level: Debug}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip failed on an unreadable request body: %v",
			err)
	}
	if base.got == nil {
		t.Fatal("the request never reached the base transport")
	}
	if strings.Contains(buf.String(), `{"a":1}`) {
		t.Errorf("log = %q, want no request body after GetBody failed",
			buf.String())
	}
}

// A zero-value Base falls back to http.DefaultTransport rather than
// panicking on a nil RoundTripper.
func TestNilBaseFallsBackToDefaultTransport(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{}`)
	var buf strings.Builder
	tr := &Transport{Out: &buf, Level: Verbose}
	resp, err := tr.RoundTrip(getReq(t, url))
	if err != nil {
		t.Fatalf("RoundTrip with a nil Base: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(buf.String(), "< 200") {
		t.Errorf("log = %q, want the response status", buf.String())
	}
}

func TestRedactBody(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		limit    int
		want     string   // exact, when set
		contains []string // substrings that must appear
		absent   []string // substrings that must not
	}{
		{
			name:  "plain json passes through",
			raw:   `{"id":"abc","plan_expires_at":null}`,
			limit: 100,
			want:  `{"id":"abc","plan_expires_at":null}`,
		},
		{
			// HTML is the case these dumps exist for — a proxy or SSO
			// interstitial answering where JSON was expected.
			name:  "html passes through",
			raw:   `<!DOCTYPE html><p>Proxy error</p>`,
			limit: 100,
			want:  `<!DOCTYPE html><p>Proxy error</p>`,
		},
		{
			// The value is masked; the shape around it survives, so a
			// reader still sees which field carried a credential.
			name:     "access_token value is masked",
			raw:      `{"access_token":"live","expires_in":3600}`,
			limit:    100,
			contains: []string{mask, "access_token", "expires_in", "3600"},
			absent:   []string{"live"},
		},
		{
			// A wrapper object must not smuggle a credential past the
			// scan, which is why it recurses.
			name:     "nested token is found",
			raw:      `{"data":{"inner":{"refresh_token":"live"}}}`,
			limit:    100,
			contains: []string{mask, "data", "inner", "refresh_token"},
			absent:   []string{"live"},
		},
		{
			// The request direction: Exchange posts this on every cold
			// start, so --debug would print a client secret without it.
			name:     "client_secret value is masked",
			raw:      `{"client_id":"id","client_secret":"live"}`,
			limit:    100,
			contains: []string{mask, "client_secret", `"client_id":"id"`},
			absent:   []string{"live"},
		},
		{
			name:     "password value is masked",
			raw:      `{"user":"a","password":"live"}`,
			limit:    100,
			contains: []string{mask, `"user":"a"`},
			absent:   []string{"live"},
		},
		{
			name:     "token inside an array is found",
			raw:      `[{"access_token":"live"}]`,
			limit:    100,
			contains: []string{mask, "access_token"},
			absent:   []string{"live"},
		},
		{
			name:     "truncation is stated",
			raw:      `{"a":"` + strings.Repeat("x", 200) + `"}`,
			limit:    20,
			contains: []string{"truncated", "bytes total"},
		},
		{
			name:  "exactly at the limit is not truncated",
			raw:   strings.Repeat("x", 20),
			limit: 20,
			want:  strings.Repeat("x", 20),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactBody([]byte(tt.raw), tt.limit)
			if tt.want != "" && got != tt.want {
				t.Errorf("RedactBody() = %q, want %q", got, tt.want)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("RedactBody() = %q, want substring %q",
						got, want)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("RedactBody() = %q, must not contain %q",
						got, unwanted)
				}
			}
		})
	}
}

// Truncation counts characters, not bytes: an em dash is 3 bytes, and a
// byte-wise cut would both under-report the limit and risk splitting a
// rune into replacement characters.
func TestRedactBodyTruncatesOnRuneBoundaries(t *testing.T) {
	raw := []byte(strings.Repeat("—", 40))
	got := RedactBody(raw, 10)
	if !strings.HasPrefix(got, strings.Repeat("—", 10)) {
		t.Errorf("RedactBody() = %q, want 10 em dashes then a notice",
			got)
	}
	if strings.Contains(got, "�") {
		t.Errorf("RedactBody() = %q, split a rune", got)
	}
}

// Header masking is by canonical name, so a lower-case or oddly-cased
// spelling cannot slip a credential through.
func TestMaskedHeadersAreCaseInsensitive(t *testing.T) {
	url, _ := newServer(t, http.StatusOK, `{}`)
	req := getReq(t, url)
	req.Header["authorization"] = []string{"Bearer super-secret"}
	req.Header["set-cookie"] = []string{"session=super-secret"}
	req.Header["X-Api-Key"] = []string{"super-secret"}

	log, _ := do(t, Debug, req)

	if strings.Contains(log, "super-secret") {
		t.Errorf("log = %q, leaked a masked header's value", log)
	}
}

// An empty body must not print an empty dump line, which reads as
// "the server sent an empty string" rather than "there was no body".
func TestNoBodyPrintsNoBodyLine(t *testing.T) {
	url, _ := newServer(t, http.StatusNoContent, "")
	log, _ := do(t, Debug, getReq(t, url))
	if strings.Contains(log, "< \n") {
		t.Errorf("log = %q, printed an empty body line", log)
	}
	if !strings.Contains(log, "< 204") {
		t.Errorf("log = %q, want the status line", log)
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		lvl  Level
		want string
	}{
		{Off, "off"},
		{Verbose, "verbose"},
		{Debug, "debug"},
		{Level(9), "Level(9)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.lvl.String(); got != tt.want {
				t.Errorf("Level(%d).String() = %q, want %q",
					int(tt.lvl), got, tt.want)
			}
		})
	}
}
