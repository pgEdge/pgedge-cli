// Package cmd wires the pgEdge BYOC command tree: the module root,
// its persistent flags, and the authenticated API client shared by
// every resource command.
package cmd

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// Exit codes and the error type are owned by the starfleet module's own
// conn package: byoc is a sub-tree of the starfleet module and shares
// that one connection, so it reports the starfleet module's exit-code
// vocabulary. Aliased here so the byoc tree reads naturally and so
// main.go's exit-code extraction is unchanged.
//
// This aliasing layer is permanent package-local vocabulary, not
// migration residue — deleting it was considered deliberately, and it
// stays:
//
//   - Each command tree keeps its own exit-code vocabulary as a
//     package-local name. These lines are the single place recording
//     that byoc reports account's set and not its own, and deleting them
//     would scatter that one fact across every site that currently just
//     says ExitGeneral.
//   - newExitError is not decoration. conn.ExitError's fields are
//     unexported, so byoc cannot write &ExitError{...} at all; some
//     constructor has to exist, and having it named the same as controlplane's
//     keeps the two trees' error construction reading alike.
//   - Repointing every reference at conn. is a ~170-reference diff with
//     no behaviour change that also leaves cp as the only tree with
//     local exit vocabulary — a worse asymmetry than the one it
//     removes.
const (
	ExitOK       = conn.ExitOK
	ExitGeneral  = conn.ExitGeneral
	ExitUsage    = conn.ExitUsage
	ExitTimeout  = conn.ExitTimeout
	ExitNotFound = conn.ExitNotFound
	ExitAuth     = conn.ExitAuth
)

// ExitError is conn.ExitError. See the note on the exit codes above.
type ExitError = conn.ExitError

// newExitError is the constructor for ExitError, whose fields are
// unexported in conn.
func newExitError(msg string, code int) *ExitError {
	return conn.NewExitError(msg, code)
}

// checkResponse inspects the HTTP status and returns a descriptive
// *ExitError for any non-2xx status, or nil on success.
func checkResponse(status int, body string) error {
	return conn.CheckResponse(status, body)
}

// checkEmptyBodyResponse applies checkResponse to an UNTYPED generated
// response — the *http.Response returned by `client.DeleteFoo(...)`
// rather than by `client.DeleteFooWithResponse(...)`.
//
// Operations whose success carries no body must call it this way. Every
// generated Parse*Response ends in a
// `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals the body into the spec's Error model for ANY status, 2xx
// included; where the operation also has no typed 2xx case, an
// empty-bodied success has nothing else to match, so json.Unmarshal of
// 0 bytes fails and the *WithResponse wrapper reports a call that
// SUCCEEDED as "unexpected end of JSON input".
//
// Bypassing only the response parser keeps the generated request
// builder, URL construction and parameter handling in play. This is the
// same bypass, for the same catch-all, that conn.Exchange applies to
// the token endpoint and that internal/starfleet/account/cmd's `client
// delete` applies to DeleteClient.
//
// checkResponse accepts any 2xx, so this is correct whether or not the
// server sends a JSON Content-Type alongside the empty response.
// TestEmptyBodySuccessIsNotReportedAsFailure holds the contract.
func checkEmptyBodyResponse(resp *http.Response, what string) error {
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", what, err)
	}
	return checkResponse(resp.StatusCode, string(body))
}

// newAPIClient builds an authenticated BYOC API client over the
// starfleet module's resolved connection.
func newAPIClient(rt *module.Runtime,
	flagID, flagSecret, flagAPIURL string, timeout time.Duration,
) (*api.ClientWithResponses, error) {
	c, err := conn.Resolve(rt, flagID, flagSecret, flagAPIURL, timeout)
	if err != nil {
		return nil, err
	}
	client, err := api.NewClientWithResponses(
		c.APIURL,
		api.WithRequestEditorFn(c.BearerEditor),
		api.WithHTTPClient(c.HTTPClient),
	)
	if err != nil {
		return nil, fmt.Errorf("create API client: %w", err)
	}
	return client, nil
}

// clientFromCmd builds an authenticated API client for a resource
// command, reading the byoc root's persistent --client-id,
// --client-secret, and --api-url overrides off cmd's inherited flag
// set. Every resource command's RunE calls it to obtain its client.
func clientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*api.ClientWithResponses, error) {
	id, secret, apiURL, timeout := connFlags(cmd)
	return newAPIClient(rt, id, secret, apiURL, timeout)
}

// connFlags reads the byoc root's persistent connection overrides off
// cmd's inherited flag set.
func connFlags(cmd *cobra.Command) (
	id, secret, apiURL string, timeout time.Duration,
) {
	id, _ = cmd.Flags().GetString("client-id")
	secret, _ = cmd.Flags().GetString("client-secret")
	apiURL, _ = cmd.Flags().GetString("api-url")
	// Fail safe, the way controlplane's reader does: a failed read must keep
	// the 30-second default, because the error value would be zero —
	// an UNBOUNDED client, the most dangerous misread available.
	timeout = conn.RequestTimeout
	if v, err := cmd.Flags().GetDuration("timeout"); err == nil {
		timeout = v
	}
	requestTimeoutFlag = timeout
	return id, secret, apiURL, timeout
}

// requestTimeoutFlag is the resolved --timeout for this invocation,
// recorded by connFlags so the wait machinery's request floors honour
// a raised bound instead of capping it at the default. Package-level
// for the same reason the wait flags are: one command runs per
// process invocation.
var requestTimeoutFlag time.Duration

// requestBound is the per-request allowance the wait machinery uses
// where an unbounded request could defeat --wait-timeout: the
// --timeout value when one is in force, and the 30-second default
// when --timeout is 0. Review measured both failure modes this
// two-sided rule closes: a plain conn.RequestTimeout floor cut a
// raised --timeout 300s to 30s and refused the write, and no floor
// at all let a hung read outlive --wait-timeout forever.
func requestBound() time.Duration {
	if requestTimeoutFlag > 0 {
		return requestTimeoutFlag
	}
	return conn.RequestTimeout
}
