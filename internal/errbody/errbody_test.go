package errbody

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubRT serves one canned response, recording nothing. The transport
// under test only reads the response, so the request is irrelevant.
type stubRT struct {
	status      int
	contentType string
	body        string
}

func (s stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	h := http.Header{}
	if s.contentType != "" {
		h.Set("Content-Type", s.contentType)
	}
	return &http.Response{
		StatusCode: s.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func TestErrBodyTransportRelabelsOnlyMislabelledErrors(t *testing.T) {
	const jsonCT = "application/json"

	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantJSONCT  bool
	}{{
		// The bug: nginx text under a JSON header. Relabel it so the
		// generated catch-all stops trying to unmarshal it.
		name:        "non-JSON body on a 404 is relabelled",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        "Not Found",
		wantJSONCT:  false,
	}, {
		// A real error object must keep its JSON label: CheckResponse
		// reads the "code" field to tell a route miss from a resource
		// miss, and that only happens on the JSON path.
		name:        "valid JSON error body keeps its label",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        `{"code":404,"message":"database not found"}`,
		wantJSONCT:  true,
	}, {
		name:        "valid JSON is recognised despite surrounding space",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        "\n  {\"code\":404,\"message\":\"x\"}\n ",
		wantJSONCT:  true,
	}, {
		// The important negative. On a 2xx the command wanted that
		// object; a body that will not parse is a real contract
		// violation and must keep erroring in the parser rather than
		// being quietly relabelled into a success path.
		name:        "invalid JSON on a 200 is left alone",
		status:      http.StatusOK,
		contentType: jsonCT,
		body:        "Not Found",
		wantJSONCT:  true,
	}, {
		name:        "non-JSON content type is left alone",
		status:      http.StatusNotFound,
		contentType: "text/plain",
		body:        "Not Found",
		wantJSONCT:  false,
	}, {
		name:        "empty body on an error is relabelled",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        "",
		wantJSONCT:  false,
	}, {
		// Valid JSON, and still fatal to the generated parser, which
		// unmarshals into a struct. json.Valid would wave these three
		// through and reproduce the original bug.
		name:        "a bare JSON string is relabelled",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        `"Not Found"`,
		wantJSONCT:  false,
	}, {
		name:        "a JSON array is relabelled",
		status:      http.StatusBadGateway,
		contentType: jsonCT,
		body:        `["Not Found"]`,
		wantJSONCT:  false,
	}, {
		name:        "a JSON number is relabelled",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        "404",
		wantJSONCT:  false,
	}, {
		// null is the deliberate exception: it unmarshals into the
		// generated Error struct without error, so it already reaches
		// CheckResponse and needs no help.
		name:        "null is left alone",
		status:      http.StatusNotFound,
		contentType: jsonCT,
		body:        "null",
		wantJSONCT:  true,
	}, {
		// The scenario that motivated bounding CheckResponse's output:
		// a proxy interstitial mislabelled as JSON.
		name:        "an HTML interstitial mislabelled as JSON is relabelled",
		status:      http.StatusBadGateway,
		contentType: jsonCT,
		body:        "<html><body>502 Bad Gateway</body></html>",
		wantJSONCT:  false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := Wrap(stubRT{
				status:      tc.status,
				contentType: tc.contentType,
				body:        tc.body,
			})
			req, err := http.NewRequest(
				http.MethodGet, "http://x/y", http.NoBody)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}

			gotJSON := strings.Contains(
				resp.Header.Get("Content-Type"), "json")
			if gotJSON != tc.wantJSONCT {
				t.Errorf("Content-Type = %q, json=%v, want json=%v",
					resp.Header.Get("Content-Type"), gotJSON,
					tc.wantJSONCT)
			}

			// Whatever was decided, the body must still be readable in
			// full — RoundTrip is documented as returning one, and the
			// parser reads it immediately after.
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("body was not replayable: %v", err)
			}
			if !bytes.Equal(got, []byte(tc.body)) {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

// failingBody yields some bytes and then errors, standing in for a
// connection that drops mid-response.
type failingBody struct{ read bool }

func (f *failingBody) Read(p []byte) (int, error) {
	if f.read {
		return 0, io.ErrUnexpectedEOF
	}
	f.read = true
	n := copy(p, []byte(`{"code":404,`))
	return n, nil
}

func (f *failingBody) Close() error { return nil }

type failingRT struct{}

func (failingRT) RoundTrip(*http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     h,
		Body:       &failingBody{},
	}, nil
}

// TestErrBodyTransportRelabelsATruncatedBody covers the path where the
// body read itself fails. A truncated body is not a whole JSON object,
// so leaving the JSON label on it would hand the parser
// `unexpected end of JSON input` — the very class of message this
// transport exists to keep away from the user.
func TestErrBodyTransportRelabelsATruncatedBody(t *testing.T) {
	req, err := http.NewRequest(
		http.MethodGet, "http://x/y", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := Wrap(failingRT{}).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		t.Errorf("a truncated body kept its JSON label: %q",
			resp.Header.Get("Content-Type"))
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Errorf("body was not replayable after a read error: %v", err)
	}
}

type nilBodyRT struct{}

func (nilBodyRT) RoundTrip(*http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     h,
		Body:       nil,
	}, nil
}

// TestErrBodyTransportSubstitutesANilBody covers the branch that
// matters more for the substitution than for the relabel: every
// generated Parse*Response calls io.ReadAll(rsp.Body) before it looks
// at Content-Type, and io.ReadAll panics on a nil Body — so the
// response has to leave here with a readable one.
func TestErrBodyTransportSubstitutesANilBody(t *testing.T) {
	req, err := http.NewRequest(
		http.MethodGet, "http://x/y", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := Wrap(nilBodyRT{}).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.Body == nil {
		t.Fatal("body is still nil; the generated parser would panic " +
			"reading it")
	}
	// The read a Parse*Response would do, which is the actual claim.
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("substituted body was not readable: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("substituted body = %q, want empty", got)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		t.Errorf("a nil body kept its JSON label: %q",
			resp.Header.Get("Content-Type"))
	}
}
