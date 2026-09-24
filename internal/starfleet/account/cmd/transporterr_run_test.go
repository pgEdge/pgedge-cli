package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTransportErrors drives the transport-error branch each
// invite/membership/client/tenant command takes when its API call
// fails at the connection level (not with an HTTP status). The stub
// answers /account/v1/oauth/token normally, then breaks the connection for
// the resource call. Ported from internal/byoc/cmd's identically-named
// test when invite and membership moved here.
func TestTransportErrors(t *testing.T) {
	for _, tc := range accountCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.BrokenHandler())
			if err := runAuthedAccount(t, rt, out, url, tc.args...); err == nil {
				t.Fatalf("%s: expected transport error", tc.name)
			}
		})
	}
}

// TestNoCredentials drives the `client, err := clientFromCmd(...); if
// err != nil { return err }` branch present in every
// invite/membership/client/tenant command: credentials are absent, so
// client construction fails before any network call.
func TestNoCredentials(t *testing.T) {
	for _, tc := range accountCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			// runAccount (not runAuthedAccount): only --api-url, no
			// credentials. ResolveCredentials fails before anything is
			// dialed.
			args := append([]string{}, tc.args...)
			args = append(args, "--api-url", "http://127.0.0.1:0")
			if err := runAccount(t, rt, out, args...); err == nil {
				t.Fatalf("%s: expected auth error", tc.name)
			}
		})
	}
}
