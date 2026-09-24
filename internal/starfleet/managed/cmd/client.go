// Package cmd wires the pgEdge Managed command tree: the module root,
// its persistent flags, and the authenticated API client shared by
// every resource command.
package cmd

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// Exit codes and the error type belong to the starfleet conn package,
// whose connection managed shares. They are aliased here as byoc does;
// internal/starfleet/byoc/cmd/client.go says why each tree keeps its own
// package-local names.
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

// checkEmptyBodyResponse applies checkResponse to an untyped generated
// response, the *http.Response from `client.DeleteManagedDatabase(...)`
// rather than `client.DeleteManagedDatabaseWithResponse(...)`.
//
// Database delete, branch delete, rotate-password and resize need it.
// None has a typed 2xx case, and every generated Parse*Response ends in
// a `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals any status, 2xx included, into Error. An empty-bodied
// success then fails to unmarshal, and *WithResponse reports it as
// "unexpected end of JSON input".
//
// Resize hides it: its spec declares a 200 with no content schema, so
// it has no JSON200 field, yet saas answers with a full ManagedDatabase
// (`RespondOK(ctx, managedDatabaseModel(...))`). This form accepts
// either body, and either Content-Type, since checkResponse takes any
// 2xx.
//
// Only the response parser is bypassed, so the generated request
// builder stays in play, as in conn.Exchange and byoc's `database
// delete`. TestManagedEmptyBodySuccessIsNotReportedAsFailure holds the
// contract.
func checkEmptyBodyResponse(resp *http.Response, what string) error {
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", what, err)
	}
	return checkResponse(resp.StatusCode, string(body))
}

// newAPIClient builds an authenticated Managed API client over the
// starfleet module's resolved connection, sharing its one credential
// pair and cached token.
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
// command from the connection overrides connFlags reads.
func clientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*api.ClientWithResponses, error) {
	id, secret, apiURL, timeout := connFlags(cmd)
	return newAPIClient(rt, id, secret, apiURL, timeout)
}

// connFlags reads the managed root's persistent connection overrides
// off cmd's inherited flag set.
func connFlags(cmd *cobra.Command) (
	id, secret, apiURL string, timeout time.Duration,
) {
	id, _ = cmd.Flags().GetString("client-id")
	secret, _ = cmd.Flags().GetString("client-secret")
	apiURL, _ = cmd.Flags().GetString("api-url")
	// Fail safe, as controlplane's reader does: a failed read keeps the
	// 30-second default, because its zero value would be an unbounded
	// client.
	timeout = conn.RequestTimeout
	if v, err := cmd.Flags().GetDuration("timeout"); err == nil {
		timeout = v
	}
	requestTimeoutFlag = timeout
	return id, secret, apiURL, timeout
}

// requestTimeoutFlag is the resolved --timeout, recorded by connFlags
// so the wait machinery's request floors honour a raised bound.
// Package-level like the wait flags: one command runs per process.
var requestTimeoutFlag time.Duration

// requestBound is the per-request allowance the wait machinery uses
// where an unbounded request could defeat --wait-timeout: --timeout
// when set, the 30-second default when it is 0. A plain
// conn.RequestTimeout floor cut a raised --timeout 300s to 30s and
// refused the write; no floor let a hung read outlive --wait-timeout.
func requestBound() time.Duration {
	if requestTimeoutFlag > 0 {
		return requestTimeoutFlag
	}
	return conn.RequestTimeout
}
