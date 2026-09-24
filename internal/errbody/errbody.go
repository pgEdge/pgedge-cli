// Package errbody repairs one specific lie an upstream proxy tells: an
// error response whose body is not JSON, served under a JSON
// Content-Type.
//
// The Starfleet connection and the Control Plane client both use it,
// since both generated clients dispatch on that header. It imports no
// generated api package (internal/clitest's layering gate), so it only
// puts the response back where the caller's own classification runs.
package errbody

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Transport is that repair.
//
// Every generated Parse*Response ends in a catch-all shaped
//
//	case strings.Contains(rsp.Header.Get("Content-Type"), "json"):
//	    if err := json.Unmarshal(bodyBytes, &dest); err != nil {
//	        return nil, err
//	    }
//
// so a mislabelled body fails inside the parser and CheckResponse never
// runs: nginx's plain `Not Found` surfaced as `invalid character 'N'
// looking for beginning of value`. Relabelling is enough, because
// Parse*Response assigns response.Body before the switch and
// CheckResponse then classifies it as any text/plain body.
//
// Scope is narrow, since a false positive would swallow a real decode
// error: error statuses only (an unparseable 2xx is a contract
// violation and must fail loudly), a JSON Content-Type only, and only a
// body that is not a JSON object (see isJSONObject). A well-formed
// error object must still parse into Error, which CheckResponse reads
// to tell a route miss from a resource miss.
type Transport struct {
	Base http.RoundTripper
}

// Wrap adds the repair unless base is nil.
//
// Unlike httplog.Wrap and dryrun.Wrap there is no off switch: a
// mislabelled body is as unreadable on a plain run as a verbose one.
func Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{Base: base}
}

func (t *Transport) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	resp, err := t.Base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	// The generated catch-all's own test: a case-sensitive Contains on
	// "json" over the raw header.
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return resp, nil
	}
	if resp.Body == nil {
		// NoBody matters more than the relabel: Parse*Response reads
		// rsp.Body before its switch, which panics on nil, and this
		// wraps an arbitrary RoundTripper.
		resp.Body = http.NoBody
		resp.Header.Set("Content-Type", plainContentType)
		return resp, nil
	}

	body, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// RoundTrip must return a readable body whatever is decided below.
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if rerr != nil {
		// A partial body keeping its JSON label would fail as
		// `unexpected end of JSON input` instead of showing the status.
		resp.Header.Set("Content-Type", plainContentType)
		return resp, nil
	}

	if isJSONObject(body) {
		return resp, nil
	}
	resp.Header.Set("Content-Type", plainContentType)
	return resp, nil
}

// plainContentType is any value without "json" in it, and text/plain is
// what the proxies should have sent.
const plainContentType = "text/plain; charset=utf-8"

// isJSONObject reports whether body is a JSON object, or the literal
// null that unmarshals into a struct without complaint.
//
// json.Valid is the wrong test: a bare string (`"Not Found"`), an array
// or a number is valid JSON that still fails to unmarshal into the
// generated Error struct. A map accepts at least what that struct does.
//
// An object whose field types disagree with the caller's error model
// passes here and still fails in the parser. Neither product sends one:
// all three Starfleet specs declare code an integer, as the API sends
// it, and the one known
// `{"code":"invalid_client"}`, a token-endpoint rejection, goes through
// authHTTPClientFor, which has no repair. Control Plane's APIError is
// `{message, name}`, both strings. Closing the gap would mean
// re-declaring each error type here, which the layering gate refuses.
func isJSONObject(body []byte) bool {
	var probe map[string]json.RawMessage
	return json.Unmarshal(body, &probe) == nil
}
