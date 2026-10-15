package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestDoctorProbeSendsTheUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
	defer srv.Close()

	rt, _, _ := testsupport.NewRuntime(t, "", "json")
	rt.UserAgent = "pgedge-cli/test"
	if info := checkAPI(rt, &conn.Flags{APIURL: srv.URL}); !info.Reachable {
		t.Fatalf("probe did not reach the stub: %+v", info)
	}
	if got != "pgedge-cli/test" {
		t.Errorf("server saw User-Agent %q, want pgedge-cli/test", got)
	}
}
