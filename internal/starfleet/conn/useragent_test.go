package conn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

// TestStarfleetClientsSendTheUserAgent covers both constructors, quiet
// and under --debug, since each level builds a different chain.
func TestStarfleetClientsSendTheUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
	defer srv.Close()

	builders := map[string]func(*module.Runtime) *http.Client{
		"HTTPClientFor": func(rt *module.Runtime) *http.Client {
			return HTTPClientFor(rt, RequestTimeout)
		},
		"authHTTPClientFor": func(rt *module.Runtime) *http.Client {
			return authHTTPClientFor(rt, RequestTimeout)
		},
	}
	for name, build := range builders {
		for _, debug := range []bool{false, true} {
			got = ""
			rt := &module.Runtime{UserAgent: "pgedge-cli/test",
				Debug: debug, Stderr: &strings.Builder{}}
			req, err := http.NewRequestWithContext(context.Background(),
				http.MethodGet, srv.URL, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := build(rt).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if got != "pgedge-cli/test" {
				t.Errorf("%s (debug=%v): server saw User-Agent %q",
					name, debug, got)
			}
		}
	}
}
