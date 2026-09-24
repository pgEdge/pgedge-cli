package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestTransportErrors drives the transport-error branch each command
// takes when its API call fails at the connection level (not with an
// HTTP status). The stub answers /account/v1/oauth/token normally, then
// breaks the connection for the resource call.
func TestTransportErrors(t *testing.T) {
	for _, tc := range byocCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.BrokenHandler())
			if err := runAuthed(t, rt, out, url, tc.args...); err == nil {
				t.Fatalf("%s: expected transport error", tc.name)
			}
		})
	}
}

// TestNoCredentials drives the `client, err := clientFromCmd(...);
// if err != nil { return err }` branch present in every command:
// credentials are absent, so client construction fails before any
// network call.
func TestNoCredentials(t *testing.T) {
	for _, tc := range byocCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			// runByoc (not runAuthed): only --api-url, no credentials.
			// ResolveCredentials fails before anything is dialed.
			args := append([]string{}, tc.args...)
			args = append(args, "--api-url", "http://127.0.0.1:0")
			if err := runByoc(t, rt, out, args...); err == nil {
				t.Fatalf("%s: expected auth error", tc.name)
			}
		})
	}
}
