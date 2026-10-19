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

// Exit codes and the error type belong to the starfleet conn package:
// byoc shares the starfleet connection, so it reports that exit-code
// set. The aliases are permanent, not migration residue:
//
//   - They are the one place recording that byoc reports conn's set
//     rather than its own; without them that fact is scattered across
//     every site that says ExitGeneral.
//   - managed and controlplane keep package-local names too, and
//     repointing byoc's 216 non-test references (2026-09-24) at conn.
//     changes no behaviour while making byoc the odd one out.
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
// unexported in conn. It shares controlplane's name so the two trees
// build errors alike.
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
// Operations whose success carries no body must call it this way. 49
// of the 50 generated Parse*Response functions end in a
// `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals the body into Error for ANY status, 2xx included; with no
// typed 2xx case to match, an empty success fails as "unexpected end of
// JSON input". Bypassing only the parser keeps the generated request
// builder in play. TestEmptyBodySuccessIsNotReportedAsFailure holds the
// contract.
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
func newAPIClient(rt *module.Runtime, f conn.Flags,
) (*api.ClientWithResponses, error) {
	c, err := conn.Resolve(rt, f)
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
// command from the starfleet root's persistent connection flags. Every
// resource command's RunE calls it to obtain its client.
func clientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*api.ClientWithResponses, error) {
	return newAPIClient(rt, connFlags(cmd))
}

// connFlags reads the persistent connection flags off cmd's inherited
// flag set and records --timeout for the wait machinery.
func connFlags(cmd *cobra.Command) conn.Flags {
	f := conn.ReadFlags(cmd.Flags())
	requestTimeoutFlag = f.Timeout
	return f
}

// requestTimeoutFlag is the resolved --timeout, recorded by connFlags
// so the wait machinery honours a raised bound. Package-level because
// one command runs per process.
var requestTimeoutFlag time.Duration

// requestBound is the per-request allowance the wait machinery uses
// where an unbounded request could defeat --wait-timeout. Both sides
// matter: a fixed conn.RequestTimeout would cut --timeout 300s to 30s,
// and no bound at all lets a hung read outlive --wait-timeout.
func requestBound() time.Duration {
	if requestTimeoutFlag > 0 {
		return requestTimeoutFlag
	}
	return conn.RequestTimeout
}
