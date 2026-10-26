package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTenantHeaderOnlyForASession drives a byoc and a managed leaf and
// the api passthrough through the real root, once as a browser session
// and once as an API client.
func TestTenantHeaderOnlyForASession(t *testing.T) {
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == testsupport.TokenPath {
				_, _ = w.Write([]byte(testsupport.TokenBody))
				return
			}
			seen = append(seen, r.Header.Clone())
			_, _ = w.Write([]byte("[]"))
		}))
	defer srv.Close()

	leaves := [][]string{
		{"byoc", "cluster", "list"},
		{"managed", "database", "list"},
		{"api", "GET", "/account/v1/tenants"},
	}
	for _, leaf := range leaves {
		seen = nil
		rt, _, _ := testsupport.NewRuntime(t, "", "json")
		testsupport.SeedSession(t, rt, srv.URL, "u1",
			time.Now().Add(time.Hour), "t-a", "t-b")
		args := append(append([]string{}, leaf...), "--tenant", "t-b")
		if err := runStarfleet(t, rt, &bytes.Buffer{},
			args...); err != nil {
			t.Fatalf("%v as a session: %v", leaf, err)
		}
		if len(seen) == 0 ||
			seen[0].Get(conn.SelectedTenantHeader) != "t-b" {
			t.Errorf("%v as a session sent %v", leaf, seen)
		}

		seen = nil
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		if err := testsupport.RunAuthed(t, NewStarfleetCmd(rt), out,
			srv.URL, leaf...); err != nil {
			t.Fatalf("%v as an API client: %v", leaf, err)
		}
		for _, h := range seen {
			if h.Get(conn.SelectedTenantHeader) != "" {
				t.Errorf("%v as an API client sent the tenant "+
					"header", leaf)
			}
		}
		if len(seen) == 0 {
			t.Errorf("%v as an API client made no request", leaf)
		}
	}
}
