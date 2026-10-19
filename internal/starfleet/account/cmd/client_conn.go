package cmd

import (
	"fmt"
	"io"
	"net/http"

	"github.com/pgEdge/pgedge-cli/internal/module"
	accountapi "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// Exit codes and the error type are owned by conn; aliased here so
// this tree reads naturally.
const (
	ExitOK       = conn.ExitOK
	ExitGeneral  = conn.ExitGeneral
	ExitUsage    = conn.ExitUsage
	ExitTimeout  = conn.ExitTimeout
	ExitNotFound = conn.ExitNotFound
	ExitAuth     = conn.ExitAuth
)

// ExitError is conn.ExitError.
type ExitError = conn.ExitError

func newExitError(msg string, code int) *ExitError {
	return conn.NewExitError(msg, code)
}

func checkResponse(status int, body string) error {
	return conn.CheckResponse(status, body)
}

// checkEmptyBodyResponse applies checkResponse to an UNTYPED generated
// response — from `client.DeleteFoo(...)`, not DeleteFooWithResponse.
//
// Operations whose success carries no body must call it this way.
// Every generated Parse*Response ends in a
// `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals the body into Error for ANY status; with no typed 2xx
// case, an empty-bodied success reaches it and the wrapper reports
// "unexpected end of JSON input". The API answers DeleteClient with a
// 204 carrying the JSON Content-Type, which net/http keeps on a 204.
//
// Only the parser is bypassed; the generated request builder stays.
// checkResponse accepts any 2xx, with or without that Content-Type.
// TestAccountEmptyBodySuccessIsNotReportedAsFailure holds the contract.
func checkEmptyBodyResponse(resp *http.Response, what string) error {
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", what, err)
	}
	return checkResponse(resp.StatusCode, string(body))
}

// clientFromCmd builds an authenticated Accounts API client, reading
// the starfleet root's persistent overrides off cmd's inherited flag
// set.
func clientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*accountapi.ClientWithResponses, error) {
	client, _, err := connClientFromCmd(rt, cmd)
	return client, err
}

// connClientFromCmd is clientFromCmd that also returns the resolved
// connection, for a caller that needs to know whose it is.
func connClientFromCmd(
	rt *module.Runtime, cmd *cobra.Command,
) (*accountapi.ClientWithResponses, *conn.Conn, error) {
	c, err := conn.Resolve(rt, conn.ReadFlags(cmd.Flags()))
	if err != nil {
		return nil, nil, err
	}
	client, err := accountClientFor(c)
	return client, c, err
}

// newEphemeralAccountClient is newAccountClient for a caller that must
// not write the token cache: it resolves through conn.ResolveEphemeral,
// so a cache miss mints a token for this process only.
//
// `doctor` must stay the only caller: every other command wants the
// token cached, which keeps a session to one token exchange.
// TestOnlyDoctorResolvesEphemerally pins the caller list.
func newEphemeralAccountClient(
	rt *module.Runtime, f conn.Flags,
) (*accountapi.ClientWithResponses, *conn.Conn, error) {
	c, err := conn.ResolveEphemeral(rt, f)
	if err != nil {
		return nil, nil, err
	}
	client, err := accountClientFor(c)
	return client, c, err
}

// accountClientFor builds the generated client over an already-resolved
// connection, so the two constructors above differ only in whether the
// token may reach disk.
func accountClientFor(
	c *conn.Conn,
) (*accountapi.ClientWithResponses, error) {
	client, err := accountapi.NewClientWithResponses(
		c.APIURL,
		accountapi.WithRequestEditorFn(c.BearerEditor),
		accountapi.WithHTTPClient(c.HTTPClient),
	)
	if err != nil {
		return nil, fmt.Errorf("create account API client: %w", err)
	}
	return client, nil
}
