package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlPlaneClientSendsTheUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
	defer srv.Close()

	rt, _, _ := newTestRuntime(t, "", "text")
	rt.UserAgent = "pgedge-cli/test"
	hc, err := httpClientFor(rt, connConfig{})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != "pgedge-cli/test" {
		t.Errorf("server saw User-Agent %q, want pgedge-cli/test", got)
	}
}
