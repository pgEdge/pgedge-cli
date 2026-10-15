package useragent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestString(t *testing.T) {
	want := "pgedge-cli/0.5.0 (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
	if got := String("0.5.0"); got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestWrapSetsTheHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "caller")
	c := &http.Client{Transport: Wrap(nil, "pgedge-cli/test")}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got != "pgedge-cli/test" {
		t.Errorf("server saw User-Agent %q, want pgedge-cli/test", got)
	}
	if req.Header.Get("User-Agent") != "caller" {
		t.Error("RoundTrip modified the caller's request")
	}
}

func TestWrapWithNoUserAgentAddsNoLayer(t *testing.T) {
	base := http.DefaultTransport
	if got := Wrap(base, ""); got != base {
		t.Errorf("Wrap(base, \"\") = %T, want base unchanged", got)
	}
}
