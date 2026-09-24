package dryrun_test

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
)

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// okResponse is the minimum a passthrough test needs back.
func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
}

func TestSafeMethodsPassThrough(t *testing.T) {
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodOptions,
	} {
		t.Run(method, func(t *testing.T) {
			run := dryrun.New()
			reached := false
			rt := dryrun.Wrap(roundTripperFunc(
				func(*http.Request) (*http.Response, error) {
					reached = true
					return okResponse(), nil
				}), run)

			req, err := http.NewRequest(method,
				"https://example.test/x", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RoundTrip(req); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reached {
				t.Error("did not reach the base transport")
			}
			if run.Intercepted() {
				t.Error("a safe method was recorded as a write")
			}
		})
	}
}

func TestWritesAreInterceptedAndRecorded(t *testing.T) {
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			run := dryrun.New()
			rt := dryrun.Wrap(roundTripperFunc(
				func(*http.Request) (*http.Response, error) {
					t.Error("base transport reached for a write")
					return okResponse(), nil
				}), run)

			req, err := http.NewRequest(method,
				"https://example.test/v1/db",
				strings.NewReader(`{"name":"mydb"}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rt.RoundTrip(req)
			if err == nil {
				t.Fatal("want an abort error, got nil")
			}
			if resp != nil {
				t.Error("a synthetic response would let the caller " +
					"parse a fabricated result")
			}
			if !run.Intercepted() {
				t.Fatal("write was not recorded")
			}
			got := run.Request()
			if got.Method != method {
				t.Errorf("method = %q, want %q", got.Method, method)
			}
			if !strings.Contains(string(got.Body), "mydb") {
				t.Errorf("body lost a field: %q", got.Body)
			}
			if !got.BodyIsJSON {
				t.Error("a JSON body was not marked as JSON")
			}
		})
	}
}

// TestAbortErrorNamesTheRequest pins the one thing the error itself has
// to carry: if a command ever swallowed the Run, this message is the
// entire explanation the user would get.
func TestAbortErrorNamesTheRequest(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	req, err := http.NewRequest(http.MethodPost,
		"https://example.test/v1/db", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"dry run", "POST", "/v1/db"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestSecretValuesNeverReachTheRecord(t *testing.T) {
	// One case per shape httplog.RedactBody handles: a top-level
	// scalar secret, one nested inside a wrapper object, and a
	// composite value masked whole.
	tests := []struct {
		name, body, secret string
	}{
		{
			name:   "top-level scalar",
			body:   `{"password":"hunter2","name":"mydb"}`,
			secret: "hunter2",
		},
		{
			name:   "nested in a wrapper",
			body:   `{"services":[{"init_tokens":"tok-abc","x":1}]}`,
			secret: "tok-abc",
		},
		{
			name:   "composite masked whole",
			body:   `{"credentials":{"s3_key":"AKIA","deep":"nope"}}`,
			secret: "AKIA",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := dryrun.New()
			rt := dryrun.Wrap(nil, run)
			req, err := http.NewRequest(http.MethodPost,
				"https://example.test/v1/db",
				strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RoundTrip(req); err == nil {
				t.Fatal("want an abort error")
			}
			if got := string(run.Request().Body); strings.Contains(
				got, tc.secret) {
				t.Fatalf("record leaked %q: %s", tc.secret, got)
			}
		})
	}
}

func TestURLUserinfoIsRedacted(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	req, err := http.NewRequest(http.MethodPost,
		"https://id:s3cr3t@example.test/v1/db", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an abort error")
	}
	if got := run.Request().URL; strings.Contains(got, "s3cr3t") {
		t.Fatalf("URL leaked userinfo: %q", got)
	}
}

// TestGetBodyIsPreferredAndRequestStaysIntact covers how the generated
// oapi-codegen clients build requests: over a bytes.Reader with GetBody
// set. Consuming req.Body instead would leave the caller holding a
// request whose body is spent.
func TestGetBodyIsPreferredAndRequestStaysIntact(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	payload := `{"name":"mydb"}`
	req, err := http.NewRequest(http.MethodPost,
		"https://example.test/v1/db", bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody == nil {
		t.Fatal("precondition: NewRequest over a bytes.Reader " +
			"should set GetBody")
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an abort error")
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}
	if buf.String() != payload {
		t.Errorf("body after interception = %q, want %q",
			buf.String(), payload)
	}
}

// TestBodylessWriteRecordsNoBody pins the distinction between "there
// was no body" and "the body was empty" — RedactBody renders both as
// the empty string, and they are different facts.
func TestBodylessWriteRecordsNoBody(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	req, err := http.NewRequest(http.MethodDelete,
		"https://example.test/v1/db/7f3a", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an abort error")
	}
	if got := run.Request().Body; got != nil {
		t.Errorf("Body = %q, want nil for a bodyless write", got)
	}
}

func TestBodyIsBoundedByMaxDumpBytes(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	// Valid JSON, comfortably past the cap.
	big := `{"note":"` + strings.Repeat("x", httplog.MaxDumpBytes*2) + `"}`
	req, err := http.NewRequest(http.MethodPost,
		"https://example.test/v1/db", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an abort error")
	}
	got := run.Request().Body
	if len(got) >= len(big) {
		t.Errorf("body was not bounded: %d bytes recorded of %d",
			len(got), len(big))
	}
	if !strings.Contains(string(got), "truncated") {
		t.Error("a clipped body must say that it was clipped")
	}
	// A truncated document is no longer JSON, and the record must say
	// so rather than inviting a renderer to emit it as one.
	if run.Request().BodyIsJSON {
		t.Error("truncated body reported as valid JSON")
	}
}

// TestNonJSONBodyIsNotMarkedJSON covers RedactBody's pass-through for a
// payload that never parsed — the shape body_text exists to render.
func TestNonJSONBodyIsNotMarkedJSON(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	req, err := http.NewRequest(http.MethodPost,
		"https://example.test/v1/db", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want an abort error")
	}
	if run.Request().BodyIsJSON {
		t.Error("a non-JSON body was marked as JSON")
	}
}

func TestFirstWriteWins(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(nil, run)
	for _, path := range []string{"/first", "/second"} {
		req, err := http.NewRequest(http.MethodPost,
			"https://example.test"+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.RoundTrip(req); err == nil {
			t.Fatal("want an abort error")
		}
	}
	if got := run.Request().URL; !strings.Contains(got, "/first") {
		t.Errorf("URL = %q, want the first write", got)
	}
}

func TestWrapWithNilRunReturnsBaseUnchanged(t *testing.T) {
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return okResponse(), nil
	})
	got := dryrun.Wrap(base, nil)
	if fmt.Sprintf("%p", got) != fmt.Sprintf("%p",
		http.RoundTripper(base)) {
		t.Error("Wrap with a nil Run must return base itself, not a " +
			"wrapper that decides to do nothing")
	}
}

// TestNilRunIsInert covers the normal, non-dry-run path: every check
// site calls Pass unconditionally.
func TestNilRunIsInert(t *testing.T) {
	var run *dryrun.Run
	run.Pass("must not panic")
	if got := run.Checks(); got != nil {
		t.Errorf("Checks() = %v, want nil", got)
	}
	if run.Request() != nil {
		t.Error("Request() on a nil Run must be nil")
	}
	if run.Intercepted() {
		t.Error("Intercepted() on a nil Run must be false")
	}
}

func TestPassRecordsInOrder(t *testing.T) {
	run := dryrun.New()
	run.Pass("first %s", "check")
	run.Pass("second")
	want := []string{"first check", "second"}
	got := run.Checks()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChecksReturnsACopy stops a renderer from holding a slice the Run
// may still append to.
func TestChecksReturnsACopy(t *testing.T) {
	run := dryrun.New()
	run.Pass("one")
	got := run.Checks()
	got[0] = "mutated"
	if run.Checks()[0] != "one" {
		t.Error("Checks() handed out its backing array")
	}
}

// TestConcurrentUseIsRaceFree exists because the --wait paths poll from
// more than one goroutine and the suite runs under -race.
func TestConcurrentUseIsRaceFree(t *testing.T) {
	run := dryrun.New()
	rt := dryrun.Wrap(roundTripperFunc(
		func(*http.Request) (*http.Response, error) {
			return okResponse(), nil
		}), run)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run.Pass("check %d", i)
			method := http.MethodGet
			if i%2 == 0 {
				method = http.MethodPost
			}
			req, err := http.NewRequest(method,
				"https://example.test/v1/db", http.NoBody)
			if err != nil {
				return
			}
			_, _ = rt.RoundTrip(req)
			_ = run.Intercepted()
			_ = run.Checks()
		}(i)
	}
	wg.Wait()
	if len(run.Checks()) != 8 {
		t.Errorf("recorded %d checks, want 8", len(run.Checks()))
	}
}

// TestMutatingGETsMatchEitherSpellingOfThePath is this package's own
// coverage of the both-spellings rule, which shipped without it: all 25
// tests here passed with reads() reverted to matching URL.Path alone.
//
// It takes both spellings, and each row proves one of them necessary.
// URL.Path is percent-DECODED, so a "/" inside a path parameter becomes a
// real separator there and defeats a [^/]+ in the pattern; only
// EscapedPath() still matches. The reverse case exists too — a path
// written with %2F where the pattern expects "/" decodes INTO the covered
// path and matches only on Path.
func TestMutatingGETsMatchEitherSpellingOfThePath(t *testing.T) {
	cancel := regexp.MustCompile(
		`(^|/)v1/databases/[^/]+/tasks/[^/]+/cancel$`)
	initRE := regexp.MustCompile(`(^|/)v1/cluster/init$`)

	tests := []struct {
		name     string
		url      string
		patterns []*regexp.Regexp
		wantSent bool
		why      string
	}{
		{
			name:     "clean path matches on both spellings",
			url:      "https://h/v1/databases/db1/tasks/t1/cancel",
			patterns: []*regexp.Regexp{cancel},
		},
		{
			name:     "slash inside a path parameter",
			url:      "https://h/v1/databases/db1%2F/tasks/t1/cancel",
			patterns: []*regexp.Regexp{cancel},
			why: "URL.Path decodes db1%2F to db1/, splitting the " +
				"segment; only EscapedPath still matches",
		},
		{
			name:     "slash in the middle of a path parameter",
			url:      "https://h/v1/databases/a%2Fb/tasks/t1/cancel",
			patterns: []*regexp.Regexp{cancel},
		},
		{
			name:     "encoded separator where the pattern wants a real one",
			url:      "https://h/v1/cluster%2Finit",
			patterns: []*regexp.Regexp{initRE},
			why: "EscapedPath keeps %2F and does not match; only the " +
				"decoded Path does",
		},
		{
			name:     "behind a base-URL path prefix",
			url:      "https://h/api/v1/databases/db1%2F/tasks/t1/cancel",
			patterns: []*regexp.Regexp{cancel},
		},
		{
			name:     "an ordinary read is still sent",
			url:      "https://h/v1/databases/db1/tasks",
			patterns: []*regexp.Regexp{cancel, initRE},
			wantSent: true,
		},
		{
			name:     "a read whose id merely contains a slash",
			url:      "https://h/v1/databases/a%2Fb",
			patterns: []*regexp.Regexp{cancel, initRE},
			wantSent: true,
		},
		{
			name:     "no patterns at all: every GET is a read",
			url:      "https://h/v1/databases/db1/tasks/t1/cancel",
			patterns: nil,
			wantSent: true,
			why:      "the starfleet module supplies no patterns",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := dryrun.New()
			sent := false
			rt := dryrun.Wrap(roundTripperFunc(
				func(*http.Request) (*http.Response, error) {
					sent = true
					return okResponse(), nil
				}), run, tc.patterns...)

			req, err := http.NewRequest(http.MethodGet, tc.url,
				http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = rt.RoundTrip(req)

			if sent != tc.wantSent {
				t.Errorf("sent = %v, want %v (%s)\n  Path        = %q"+
					"\n  EscapedPath = %q", sent, tc.wantSent, tc.why,
					req.URL.Path, req.URL.EscapedPath())
			}
			if run.Intercepted() == tc.wantSent {
				t.Errorf("Intercepted() = %v alongside sent = %v",
					run.Intercepted(), sent)
			}
		})
	}
}
