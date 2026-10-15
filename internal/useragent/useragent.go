// Package useragent names the CLI to the APIs it calls, so server-side
// analytics can tell its traffic apart from the console's and from
// other Go clients, which all send Go's default Go-http-client/1.1.
package useragent

import (
	"fmt"
	"net/http"
	"runtime"
)

// String is the User-Agent for a build of the given version, such as
// "pgedge-cli/0.5.0 (darwin; arm64)".
func String(version string) string {
	return fmt.Sprintf("pgedge-cli/%s (%s; %s)",
		version, runtime.GOOS, runtime.GOARCH)
}

// Wrap returns base wrapped so every request carries ua. An empty ua
// returns base itself, as a test Runtime has none.
func Wrap(base http.RoundTripper, ua string) http.RoundTripper {
	if ua == "" {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{Base: base, UserAgent: ua}
}

// Transport sets the User-Agent header. Use Wrap.
type Transport struct {
	Base      http.RoundTripper
	UserAgent string
}

// RoundTrip sets the header on a clone, since a RoundTripper must not
// modify the caller's request.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.UserAgent)
	return t.Base.RoundTrip(r)
}
