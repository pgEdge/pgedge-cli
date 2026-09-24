package cmd

import (
	"fmt"
	"io"
	"net/http"
	"time"

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
// response — the *http.Response from `client.DeleteFoo(...)` rather
// than from `client.DeleteFooWithResponse(...)`.
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
// This is not hypothetical here: the API answers DeleteClient with a
// 204 that carries the JSON Content-Type, which net/http keeps on a
// 204 — so `client delete` reported a successful delete as a failure
// until it was bypassed this way.
//
// Bypassing only the response parser keeps the generated request
// builder, URL construction and parameter handling in play.
// checkResponse accepts any 2xx, so this is correct whether or not the
// server sends that Content-Type.
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
	id, _ := cmd.Flags().GetString("client-id")
	secret, _ := cmd.Flags().GetString("client-secret")
	apiURL, _ := cmd.Flags().GetString("api-url")
	// Fail safe: a failed read keeps the 30-second default rather
	// than the zero value, which would be an unbounded client.
	timeout := conn.RequestTimeout
	if v, err := cmd.Flags().GetDuration("timeout"); err == nil {
		timeout = v
	}
	return newAccountClient(rt, id, secret, apiURL, timeout)
}

// newAccountClient builds an authenticated Accounts API client from
// explicit override values.
//
// It exists because not every caller has a *cobra.Command to read flags
// from: `doctor`'s checks take the resolved conn.Flags struct, matching
// checkAuth and checkAPI, so they stay callable from tests that never
// build a command tree.
func newAccountClient(
	rt *module.Runtime, id, secret, apiURL string,
	timeout time.Duration,
) (*accountapi.ClientWithResponses, error) {
	c, err := conn.Resolve(rt, id, secret, apiURL, timeout)
	if err != nil {
		return nil, err
	}
	return accountClientFor(c)
}

// newEphemeralAccountClient is newAccountClient for a caller that must
// not write the token cache: it resolves through conn.ResolveEphemeral,
// so a cache miss mints a token for this process only.
//
// `doctor` is the only caller and must stay the only one. Every
// other command WANTS the token cached — that is what keeps a session
// to one token exchange — so this is a carve-out for a command whose
// job is to observe, not a better default.
// TestOnlyDoctorResolvesEphemerally pins the caller list.
func newEphemeralAccountClient(
	rt *module.Runtime, id, secret, apiURL string,
	timeout time.Duration,
) (*accountapi.ClientWithResponses, error) {
	c, err := conn.ResolveEphemeral(rt, id, secret, apiURL, timeout)
	if err != nil {
		return nil, err
	}
	return accountClientFor(c)
}

// accountClientFor builds the generated client over an already-resolved
// connection. Shared so the two constructors above differ in exactly
// one thing — whether the token may reach disk — and in nothing else.
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
