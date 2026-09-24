// Package errbody repairs one specific lie an upstream proxy tells: an
// error response whose body is not JSON, served under a JSON
// Content-Type.
//
// It is shared by the Starfleet connection and the Control Plane client,
// which are built from generated clients that dispatch on the header and
// were reported against the same failure. It imports no module's
// generated api package — internal/clitest's layering gate holds that —
// so it knows nothing about how a caller classifies a status. Its job is
// to put the response back on the path where that can happen.
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
// so a mislabelled body fails INSIDE the parser, the command's
// `if err != nil` fires first, and CheckResponse never runs. The user
// got `invalid character 'N' looking for beginning of value` instead of
// the route-miss message (#140), for a body that was a perfectly clear
// `Not Found` from nginx.
//
// On this path the relabel alone is enough: the catch-all stops
// matching, the raw bytes still reach the caller — Parse*Response
// assigns response.Body before the switch — and CheckResponse then
// classifies them exactly as it already does for a text/plain body.
//
// Scope is narrow because this is a lie-detector and a false positive
// would swallow a real decode error:
//
//   - Error statuses only. On a 2xx an unparseable body is a genuine
//     contract violation and must keep erroring loudly.
//   - JSON Content-Type only. Anything else already reaches
//     CheckResponse.
//   - Bodies that are not a JSON OBJECT — see isJSONObject for why an
//     object rather than valid JSON is the test. A well-formed error
//     object is left alone: it parses into the generated Error type,
//     which is what CheckResponse's discriminator reads to tell a route
//     miss from a resource miss.
type Transport struct {
	Base http.RoundTripper
}

// Wrap adds the repair unless base is nil.
//
// Unlike httplog.Wrap and dryrun.Wrap there is no off switch: a
// mislabelled error body is equally unreadable with and without
// --verbose, and the bug was reported on a plain run.
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
		// Substituting NoBody is the point here, not the relabel: every
		// Parse*Response calls io.ReadAll(rsp.Body) BEFORE it switches on
		// Content-Type, and that panics on a nil Body. Real transports
		// always set one, but this wraps an arbitrary RoundTripper.
		resp.Body = http.NoBody
		resp.Header.Set("Content-Type", plainContentType)
		return resp, nil
	}

	body, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// RoundTrip must return a readable body whatever is decided below.
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if rerr != nil {
		// A partial body is not a whole JSON object either, and keeping
		// the JSON label would reproduce the symptom this exists to
		// remove — `unexpected end of JSON input` instead of the status.
		resp.Header.Set("Content-Type", plainContentType)
		return resp, nil
	}

	if isJSONObject(body) {
		return resp, nil
	}
	resp.Header.Set("Content-Type", plainContentType)
	return resp, nil
}

// plainContentType is what a relabelled body is served as. Any value
// without "json" in it would do — the generated catch-all tests for
// that substring — but text/plain is what the proxies that cause this
// should have sent.
const plainContentType = "text/plain; charset=utf-8"

// isJSONObject reports whether body is a JSON object, or the literal
// null that unmarshals into a struct without complaint.
//
// The parser unmarshals into a STRUCT, so a map accepts AT LEAST what
// the generated Error struct accepts — an object, or null — while a
// bare JSON string (`"Not Found"`), an array or a number is valid JSON
// that fails against both, exactly as the original bug did. That is why
// json.Valid is the wrong test.
//
// The superset is deliberate: an object whose field TYPES disagree with
// the caller's error model passes here and still fails in the parser,
// leaving the #140 symptom.
//
// Neither product produces such a shape. For Starfleet it would be
// `{"code":"invalid_client"}` with code a string, but all three vendored
// Starfleet specs declare code as an integer and saas's oapi.Error
// serialises it that way; the one place that shape is quoted — Exchange's
// doc comment, a real token-endpoint rejection — is reached through
// authHTTPClientFor, which carries no repair at all. For Control Plane
// the model is APIError, `{message, name}`, both strings, so the
// equivalent is an object whose message is itself an object.
//
// Closing the gap would mean re-declaring each caller's error type in a
// package that imports no api — a worse trade, and one the layering gate
// would refuse.
func isJSONObject(body []byte) bool {
	var probe map[string]json.RawMessage
	return json.Unmarshal(body, &probe) == nil
}
