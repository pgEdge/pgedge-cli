package cmd

import (
	"net/http"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The empty-body-success contract for the controlplane module. See the long
// explanation on checkEmptyBodyResponse in client.go, and the matching
// gates in internal/starfleet/byoc/cmd and internal/starfleet/account/cmd.
//
// cp has one such operation: JoinCluster. Its generated parser carries
// the `Content-Type contains "json" && true` catch-all and has no typed
// 2xx case, so a success carrying an empty body lands on the catch-all
// and json.Unmarshal of 0 bytes turns it into a failure.
//
// cp has no shared command-case table, so the invocation is inline
// here. If one is ever added, fold this into it rather than duplicating.
//
// When this goes red, fix the COMMAND, not the fixture. Do not
// "simplify" the stub to a bare w.WriteHeader(204) — that drops the
// Content-Type and with it the whole point of the test.

var controlplaneEmptyBodySuccessCases = []struct {
	name string
	args []string
}{
	{
		// JoinCluster
		name: "cluster join",
		args: []string{
			"cluster", "join",
			"--token", "jointoken",
			"--server-url", "https://cp.example.invalid:8443",
		},
	},
}

func TestControlplaneEmptyBodySuccessIsNotReportedAsFailure(t *testing.T) {
	for _, tc := range controlplaneEmptyBodySuccessCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := newServer(t,
				testsupport.JSONHandler(http.StatusNoContent, ""))
			if err := runControlplane(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("204 + JSON Content-Type reported as a "+
					"failure: %v", err)
			}
		})
	}
}
