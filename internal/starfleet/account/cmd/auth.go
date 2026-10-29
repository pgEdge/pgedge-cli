package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// NewAuthCmd builds the `pgedge starfleet auth` command group. The
// starfleet root owns f, so every verb below reads the one set of
// connection flags bound there.
func NewAuthCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage pgEdge Starfleet authentication",
		Long: `auth manages the credentials pgedge uses to reach the
pgEdge Starfleet API for the active profile.

Use it to sign in, inspect the current credential source and token
state, read the identity the API accepts you as, or sign out.

Example:
  pgedge starfleet auth login
  pgedge starfleet auth status
  pgedge starfleet auth whoami`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newAuthLoginCmd(rt, f))
	cmd.AddCommand(newAuthStatusCmd(rt, f))
	cmd.AddCommand(newAuthWhoamiCmd(rt, f))
	cmd.AddCommand(newAuthLogoutCmd(rt))
	return cmd
}

func newAuthLoginCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	var insecureStorage, apiClient, secretStdin bool
	var secretFile string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with pgEdge Starfleet",
		Long: `login signs the active profile in to pgEdge Starfleet, either as
you, through the browser, or as an API client.

With no credential flags, login opens your browser on the pgEdge
sign-in page and prints the same URL on stderr, so you can open it by
hand when no browser starts. Once you sign in, every command runs as
you, in your tenant. The session renews itself while in use. It ends
after a period without use, or after a fixed maximum however much it
is used; run login again to start a new one. The profile stores the
API URL, the tenant and the fact that it is a browser sign-in; the
access token goes to the token cache and the token that renews the
session to the OS keychain, never the config file. Where no keychain
can be used, the session lasts until the time login prints. When you
belong to several tenants, login asks which to use when it can
prompt, and uses your account's default tenant otherwise. --tenant
picks one by ID or name without asking. Browser sign-in is available only for pgEdge
Starfleet APIs that have a sign-in app; for any other --api-url,
login fails with exit 5 and asks for an API client.

An API client is the unattended path, for CI and scripts. Supply
--client-id and --client-secret, or --api-client to be prompted for
them; the secret is never echoed. In place of --client-secret,
--client-secret-stdin reads the secret from standard input and
--client-secret-file reads it from a file, which keeps it out of the
process list and shell history; one trailing newline is dropped. The
three secret flags are mutually exclusive (exit 2). login exchanges them for an access
token and saves them to the profile. The secret goes to the OS
keychain (Keychain on macOS, the Secret Service on Linux, Credential
Manager on Windows); the client ID and API URL go to
~/.pgedge/cli/config.yaml. Where no keychain can be used, login
writes the secret to the config file instead and warns that it did.
--insecure-storage writes it to the config file without trying the
keychain. Supplying only one of the pair is a usage error (exit 2)
rather than a prompt for the other, so a mistyped flag cannot sign
you in under a different identity.

A profile holds one kind of credential: signing in one way removes
what the other way stored. Run this once per profile. The sign-in
serves every pgEdge Starfleet module — account, byoc and managed —
which share one connection and one token. Provide --api-url to
target a non-default API endpoint.

login ignores PGEDGE_CLIENT_ID and PGEDGE_CLIENT_SECRET, and warns
when they are set, because every other command uses them instead of
the profile until they are unset.

Example:
  pgedge starfleet auth login
  pgedge starfleet auth login --tenant TENANT_ID
  pgedge starfleet auth login --client-id ID --client-secret-file FILE
  pgedge starfleet auth login --client-id ID --client-secret-stdin < FILE
  pgedge starfleet auth login --api-client --insecure-storage`,
		Annotations: map[string]string{
			cli.AnnotationProfileExempt: "creates the named profile, " +
				"so a new name is the point rather than a typo",
		},
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			secret, err := loginSecret(rt, c, f.ClientSecret,
				secretStdin, secretFile)
			if err != nil {
				return err
			}
			// Flags only, not the full resolution chain; see
			// auth.FlagCredentials for why.
			creds, err := auth.FlagCredentials(f.ClientID, secret)
			if err != nil {
				// Half a pair is a typo, the code conn.Resolve returns.
				return newExitError(err.Error(), ExitUsage)
			}

			if creds == nil && !apiClient {
				if insecureStorage {
					return newExitError("--insecure-storage stores an "+
						"API client's secret: add --api-client or "+
						"--client-id with --client-secret, "+
						"--client-secret-stdin or "+
						"--client-secret-file", ExitUsage)
				}
				if err := browserLogin(rt, f); err != nil {
					return err
				}
				warnEnvPair(rt)
				return nil
			}
			if f.Tenant != "" {
				return newExitError("--tenant applies only to a "+
					"browser sign-in: an API client is scoped to one "+
					"tenant", ExitUsage)
			}

			var clientID, clientSecret string
			if creds != nil {
				clientID, clientSecret = creds.ClientID, creds.ClientSecret
			} else {
				clientID, clientSecret, err = promptCredentials(rt)
				if err != nil {
					return err
				}
			}

			if clientID == "" || clientSecret == "" {
				return fmt.Errorf(
					"client ID and secret are required")
			}

			cp := rt.Config.StarfleetProfile(rt.Profile)
			apiURL := conn.ResolveAPIURL(cp, f.APIURL)

			tok, err := conn.Login(context.Background(), rt, apiURL, clientID, clientSecret,
				f.Timeout)
			if err != nil {
				// ExitAuth, as conn.Resolve tags the same failure: a
				// plain error would exit 1.
				return newExitError(
					fmt.Sprintf("authentication failed: %v", err),
					ExitAuth)
			}

			store := conn.Store(rt)
			prof := &config.StarfleetProfile{
				APIURL: apiURL, ClientID: clientID}
			var kcErr error
			if !insecureStorage {
				kcErr = keychainSet(store, clientSecret)
			}
			if insecureStorage || kcErr != nil {
				prof.ClientSecret = clientSecret
				// A secret from an earlier keychain login would
				// otherwise outlive this one, unread but not gone. An
				// unavailable keychain has nothing to delete, and
				// asking again would wait out its timeout twice.
				if !errors.Is(kcErr, keychain.ErrUnavailable) {
					_ = keychainDelete(store)
				}
			}
			rt.Config.SetStarfleetProfile(rt.Profile, prof)
			if err := rt.Config.Save(); err != nil {
				// A file secret from an earlier login would still win
				// over the entry just written, so do not leave it.
				if prof.ClientSecret == "" {
					_ = keychainDelete(store)
				}
				return fmt.Errorf("save credentials: %w", err)
			}
			if cp.IsBrowserSession() {
				if prev, err := store.SessionRefreshToken(); err == nil {
					revokeDiscarded(rt, conn.ResolveAPIURL(cp, ""), prev)
				}
				if err := store.ClearSessionRefreshToken(); err != nil {
					fmt.Fprintf(rt.Stderr, "Warning: could not remove the "+
						"previous session from the OS keychain: %s\n",
						output.Sanitize(err.Error()))
				}
			}

			// The file Save() actually wrote, not DefaultPath(): Save
			// writes the --config value when one was given.
			path := rt.Config.Path()
			fmt.Fprintf(rt.Stderr, "Authenticated. Token expires %s.\n",
				tok.ExpiresAt.Format(time.RFC3339))
			switch {
			case prof.ClientSecret == "":
				fmt.Fprintf(rt.Stderr, "Client secret saved to the OS "+
					"keychain; client ID and API URL saved to %s\n", path)
			case kcErr != nil:
				fmt.Fprintf(rt.Stderr, "Warning: the OS keychain could "+
					"not be used (%v), so the client secret is stored in "+
					"plain text.\nCredentials saved to %s\n",
					output.Sanitize(kcErr.Error()), path)
			default:
				fmt.Fprintf(rt.Stderr,
					"Credentials saved to %s\n", path)
			}
			warnEnvPair(rt)
			return nil
		},
	}
	cmd.Flags().BoolVar(&insecureStorage, "insecure-storage", false,
		"store the client secret in the config file, not the OS keychain")
	cmd.Flags().BoolVar(&apiClient, "api-client", false,
		"sign in with an API client, prompting for its ID and secret")
	cmd.Flags().BoolVar(&secretStdin, "client-secret-stdin", false,
		"read the API client secret from standard input")
	cmd.Flags().StringVar(&secretFile, "client-secret-file", "",
		"read the API client secret from this file")
	return cmd
}

// loginSecret returns the client secret from whichever of the three
// secret flags was given, or "" when none was.
func loginSecret(rt *module.Runtime, c *cobra.Command, flagSecret string,
	fromStdin bool, file string,
) (string, error) {
	var given []string
	for _, name := range []string{
		"client-secret", "client-secret-stdin", "client-secret-file",
	} {
		if c.Flags().Changed(name) {
			given = append(given, "--"+name)
		}
	}
	if len(given) > 1 {
		return "", newExitError(strings.Join(given, " and ")+
			" are mutually exclusive: supply the secret one way",
			ExitUsage)
	}
	fileGiven := c.Flags().Changed("client-secret-file")
	if !fromStdin && !fileGiven {
		return flagSecret, nil
	}
	name := "--client-secret-stdin"
	if fileGiven {
		name = "--client-secret-file"
	}
	if fileGiven && file == "" {
		return "", newExitError(name+" needs a path", ExitUsage)
	}
	// Checked before reading, so a missing --client-id does not first
	// wait on a terminal's stdin.
	if id, _ := c.Flags().GetString("client-id"); id == "" {
		return "", newExitError(name+" given without --client-id: "+
			"supply both or neither", ExitUsage)
	}
	var raw []byte
	var err error
	if fileGiven {
		raw, err = os.ReadFile(file) //nolint:gosec // G304: the caller's --client-secret-file
	} else {
		raw, err = io.ReadAll(rt.Stdin)
	}
	if err != nil {
		return "", newExitError(fmt.Sprintf("%s: %v", name, err),
			ExitUsage)
	}
	s := strings.TrimSuffix(string(raw), "\n")
	s = strings.TrimSuffix(s, "\r")
	if s == "" {
		return "", newExitError(name+": the secret is empty", ExitUsage)
	}
	return s, nil
}

// warnEnvPair warns that the env pair outranks the profile just saved.
func warnEnvPair(rt *module.Runtime) {
	if os.Getenv(auth.EnvClientID) != "" ||
		os.Getenv(auth.EnvClientSecret) != "" {
		fmt.Fprintln(rt.Stderr, "Warning: PGEDGE_CLIENT_ID or "+
			"PGEDGE_CLIENT_SECRET is set, so other commands use "+
			"it instead of this profile until it is unset.")
	}
}

// keychainDelete removes the profile's keychain entry, reporting only
// a failure other than "there was none".
func keychainDelete(store *auth.Auth) error {
	if store.Keychain == nil {
		return nil
	}
	err := store.Keychain.Delete(store.KeychainAccount())
	if errors.Is(err, keychain.ErrNotFound) {
		return nil
	}
	return err
}

// keychainSet stores the profile's secret in the keychain.
func keychainSet(store *auth.Auth, secret string) error {
	if store.Keychain == nil {
		return keychain.ErrUnavailable
	}
	return store.Keychain.Set(store.KeychainAccount(), secret)
}

// authStatusReport is `auth status`'s -o json/-o yaml payload. Keys
// match `starfleet doctor`'s authInfo (doctor.go) wherever the concepts
// match, so the two never name one fact two ways. It is a separate type
// because status also reports the client ID and the resolved API URL.
//
// No field carries the client secret: the client ID is an identifier
// and printed by design, as `profile show` does, but the secret must
// never round-trip through any output format.
type authStatusReport struct {
	Authenticated bool   `json:"authenticated"`
	Source        string `json:"source,omitempty"`
	// Problem is as in authInfo: a usage error or an unreadable
	// keychain, absent when there are simply no credentials.
	Problem  string `json:"problem,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	// User and TenantID describe a browser session.
	User       string `json:"user,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`
	APIURL     string `json:"api_url,omitempty"`
	TokenValid bool   `json:"token_valid"`
	// TokenBound means what authInfo.TokenBound (doctor.go) means.
	TokenBound bool   `json:"token_bound"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	// Renews reports a browser session that can renew its own token.
	Renews bool `json:"renews"`
	// Verified is present only under --verify.
	Verified *bool `json:"verified,omitempty"`
}

func newAuthStatusCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	var verify bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current authentication state",
		Long: `status reports which credential source is active for the
current profile, the API URL in effect, and whether a cached token
is present and still valid. -o json and -o yaml carry the same facts
as a single object, so a script can check them without parsing prose.

For a browser sign-in it reports the user, the tenant, when the
current access token expires and whether the session renews itself;
for an API client, its client ID.

It exits 5 (the auth-failure code) when no usable
credentials resolve, 2 if you supplied half a credential pair, and 0
whenever they do. An API client's token may be missing or expired
with exit 0, since the CLI mints a new one on demand the next time it
is needed. A browser session that renews itself does the same, so an
expired access token is exit 0 and renews on next use; one that
cannot renew itself exits 5 once it expires, and 'pgedge starfleet
auth login' starts a new session. status calls no endpoint, so
"authenticated" means credentials resolved, not that the server
accepts them.

--verify also reads the tenant list, as any command would, and
reports "verified". It exits 5 when the server rejects the
credential. Any other failure, such as a 5xx or a timed-out token
exchange, exits with another non-zero code, usually 1.

Example:
  pgedge starfleet auth status
  pgedge starfleet auth status -o json
  pgedge starfleet auth status --verify`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := conn.ResolveCredentials(
				rt, f.ClientID, f.ClientSecret, f.APIURL)
			if err != nil {
				// A usage error is not an absence of credentials; a bare
				// "Not authenticated." would send the operator looking
				// for a login they may already have.
				report := authStatusReport{APIURL: conn.ResolveAPIURL(
					rt.Config.StarfleetProfile(rt.Profile), f.APIURL)}
				text := "Not authenticated.\n"
				usage := conn.IsUsageError(err)
				if usage || !errors.Is(err, auth.ErrNoCredentials) {
					report.Problem = err.Error()
					text = fmt.Sprintf("Not authenticated: %v\n", err)
				}
				if pErr := printAuthStatus(rt, report, text); pErr != nil {
					return pErr
				}
				// Codes match conn.Resolve. err carries the remedy, so
				// main.go's "Error: ..." line is not a bare duplicate.
				code := conn.ExitAuth
				if usage {
					code = conn.ExitUsage
				}
				return conn.NewExitError(err.Error(), code)
			}
			if res.Session {
				return sessionStatus(rt, res, f, verify)
			}
			if f.Tenant != "" {
				return conn.ErrTenantWithAPIClient()
			}
			// Before the token is read, so the Token line reports any
			// token the read minted.
			var verifyErr error
			if verify {
				verifyErr = verifyCredential(rt, f)
			}
			creds, source, apiURL := res.Creds, res.Source, res.APIURL

			report := authStatusReport{
				Authenticated: true,
				Source:        source,
				ClientID:      creds.ClientID,
				APIURL:        apiURL,
			}
			lines := []string{
				fmt.Sprintf("Client ID:    %s", creds.ClientID),
				fmt.Sprintf("Auth source:  %s", source),
				fmt.Sprintf("API URL:      %s", apiURL),
			}

			// A missing or expired token is not a failure: the next
			// command that needs one fetches it.
			if res.Env {
				lines = append(lines,
					"Token:        never cached (env credentials)")
				return printVerifiedStatus(
					rt, report, lines, verify, verifyErr)
			}
			tok, err := conn.Store(rt).LoadToken()
			switch {
			case err != nil:
				lines = append(lines, "Token:        not cached")
			case time.Until(tok.ExpiresAt) <= 0:
				report.ExpiresAt = tok.ExpiresAt.Format(time.RFC3339)
				lines = append(lines, "Token:        expired")
			case !tok.MintedBy(apiURL, creds.ClientID, creds.ClientSecret):
				// Unexpired but minted for a different connection: a
				// rekey, a flag override on a profile-minted cache, or
				// another --api-url. conn.token re-authenticates on the
				// next call. TokenValid stays true, meaning unexpired as
				// in doctor's authInfo; TokenBound, left false, carries
				// the mismatch.
				report.TokenValid = true
				report.ExpiresAt = tok.ExpiresAt.Format(time.RFC3339)
				lines = append(lines,
					"Token:        stale (different credential or API URL)")
			default:
				report.TokenValid = true
				report.TokenBound = true
				report.ExpiresAt = tok.ExpiresAt.Format(time.RFC3339)
				lines = append(lines, fmt.Sprintf(
					"Token:        valid (expires %s)", report.ExpiresAt))
			}

			return printVerifiedStatus(rt, report, lines, verify, verifyErr)
		},
	}
	cmd.Flags().BoolVar(&verify, "verify", false,
		"Check that the server accepts the credential")
	return cmd
}

// verifyCredential lists tenants as doctor does, but through
// conn.Resolve, so an expired session renews as any command would.
// Exit 5 is kept for a refusal of the credential; an exchange that
// failed for any other reason is exit 1.
func verifyCredential(rt *module.Runtime, f *conn.Flags) error {
	cf := *f
	if cf.Timeout <= 0 {
		cf.Timeout = conn.RequestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), cf.Timeout)
	defer cancel()

	err := func() error {
		c, err := conn.Resolve(rt, cf)
		if err != nil {
			return err
		}
		client, err := accountClientFor(c)
		if err != nil {
			return err
		}
		_, err = readTenants(ctx, client)
		return err
	}()
	return conn.UnrefusedAsGeneral(err)
}

// printVerifiedStatus prints the report with its Verified line when
// --verify was given, then returns the verification's failure.
func printVerifiedStatus(rt *module.Runtime, report authStatusReport,
	lines []string, verify bool, verifyErr error,
) error {
	if verify {
		ok := verifyErr == nil
		report.Verified = &ok
		lines = append(lines, "Verified:     "+output.BoolYesNo(ok))
	}
	if err := printAuthStatus(
		rt, report, strings.Join(lines, "\n")+"\n"); err != nil {
		return err
	}
	return verifyErr
}

// sessionState is what status and doctor read from a browser session's
// cache, and the failure a command would hit with it, if any.
type sessionState struct {
	tok                  *auth.CachedToken
	valid, bound, renews bool
	tenant               string
	problem              error
}

// readSession checks a session the way conn.Resolve would, without
// calling the API.
func readSession(rt *module.Runtime, res *conn.Resolved,
	flagTenant string) sessionState {
	var s sessionState
	expired := conn.SessionExpiredError(rt, res.APIURL)
	tok, err := conn.Store(rt).LoadToken()
	if err != nil {
		s.problem = expired
		return s
	}
	s.tok = tok
	s.valid = time.Until(tok.ExpiresAt) > auth.RefreshWindow
	s.bound = tok.SessionFor(res.APIURL)
	_, rerr := conn.Store(rt).SessionRefreshToken()
	s.renews = rerr == nil
	claims, err := auth.ParseClaims(tok.AccessToken)
	if (!s.valid && !s.renews) || !s.bound || err != nil {
		s.problem = expired
		return s
	}
	s.tenant, err = conn.SessionTenant(rt, res.TenantID, flagTenant, claims)
	if err != nil {
		s.problem = err
	}
	return s
}

// sessionStatus reports a browser session. An expired one that cannot
// renew itself exits 5; a tenant the token cannot reach exits 2.
func sessionStatus(
	rt *module.Runtime, res *conn.Resolved, f *conn.Flags, verify bool,
) error {
	var verifyErr error
	if verify {
		verifyErr = verifyCredential(rt, f)
	}
	s := readSession(rt, res, f.Tenant)
	report := authStatusReport{
		Authenticated: s.problem == nil,
		Source:        res.Source,
		APIURL:        res.APIURL,
		TenantID:      s.tenant,
		TokenValid:    s.valid,
		TokenBound:    s.bound,
		Renews:        s.renews,
	}
	if s.tok != nil {
		report.User = s.tok.User
		report.ExpiresAt = s.tok.ExpiresAt.Format(time.RFC3339)
	}
	if s.problem != nil {
		report.Problem = s.problem.Error()
	}
	lines := []string{
		fmt.Sprintf("User:         %s", output.Sanitize(report.User)),
		fmt.Sprintf("Auth source:  %s", report.Source),
		fmt.Sprintf("API URL:      %s", report.APIURL),
		fmt.Sprintf("Tenant:       %s", output.Sanitize(report.TenantID)),
	}
	switch {
	case s.tok == nil:
		lines = append(lines, "Token:        not cached")
	case !s.bound:
		lines = append(lines,
			"Token:        stale (signed in for a different API URL)")
	case !s.valid && s.renews:
		lines = append(lines, "Token:        expired, renews on next use")
	case !s.valid:
		lines = append(lines, "Token:        expired")
	default:
		lines = append(lines, fmt.Sprintf(
			"Token:        valid (expires %s)", report.ExpiresAt))
	}
	renews := "no"
	if s.renews {
		renews = "yes"
	}
	lines = append(lines, "Renews:       "+renews)
	if s.problem != nil {
		verifyErr = s.problem
	}
	return printVerifiedStatus(rt, report, lines, verify, verifyErr)
}

// printAuthStatus renders report through rt.Output for json/yaml, or
// writes text verbatim otherwise.
func printAuthStatus(
	rt *module.Runtime, report authStatusReport, text string,
) error {
	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}
	_, err := fmt.Fprint(rt.Stdout, text)
	return err
}

func newAuthLogoutCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored pgEdge Starfleet credentials and token",
		Long: `logout removes the cached access token and the stored
credential for the active profile: for an API client, its client ID
and secret, from the OS keychain and from ~/.pgedge/cli/config.yaml;
for a browser sign-in, the session and its tenant, after asking pgEdge
to end the session so it cannot be renewed. If that request fails,
logout warns and still removes the session from this machine. The API
URL and the profile itself are kept, so 'pgedge starfleet auth login'
can sign in again without re-entering the endpoint.

Example:
  pgedge starfleet auth logout`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if rt.Config.StarfleetProfile(rt.Profile).IsBrowserSession() {
				endSession(rt)
			}
			if err := conn.Logout(rt); err != nil {
				return fmt.Errorf("clear token: %w", err)
			}
			// A secret kept in the file means login never left one in
			// the keychain (it deletes a stale entry when it writes the
			// file), so a host without a keychain is not asked. One that
			// cannot be reached warns rather than stopping the file side
			// being cleared.
			cp := rt.Config.StarfleetProfile(rt.Profile)
			if !cp.IsBrowserSession() && cp.ClientSecret == "" {
				if err := keychainDelete(conn.Store(rt)); err != nil {
					fmt.Fprintf(rt.Stderr, "Warning: could not remove "+
						"the client secret from the OS keychain: %s\n",
						output.Sanitize(err.Error()))
				}
			}
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{APIURL: cp.APIURL})
			if err := rt.Config.Save(); err != nil {
				return fmt.Errorf("clear credentials: %w", err)
			}
			if cp.IsBrowserSession() {
				fmt.Fprintln(rt.Stderr,
					"Logged out. Session token removed.")
				return nil
			}
			fmt.Fprintln(rt.Stderr,
				"Logged out. Token and stored credentials removed.")
			return nil
		},
	}
}

// endSession revokes the session's refresh token, best effort, and
// removes it from the keychain.
func endSession(rt *module.Runtime) {
	store := conn.Store(rt)
	token, err := store.SessionRefreshToken()
	if err != nil {
		return
	}
	apiURL := conn.ResolveAPIURL(rt.Config.StarfleetProfile(rt.Profile), "")
	if err := conn.RevokeSessionToken(rt, apiURL, token); err != nil {
		fmt.Fprintf(rt.Stderr, "Warning: could not end the session "+
			"with pgEdge (%s); it is removed from this machine.\n",
			output.Sanitize(err.Error()))
	}
	if err := store.ClearSessionRefreshToken(); err != nil {
		fmt.Fprintf(rt.Stderr, "Warning: could not remove the session "+
			"from the OS keychain: %s\n", output.Sanitize(err.Error()))
	}
}

// revokeDiscarded ends a refresh token the profile no longer keeps,
// best effort.
func revokeDiscarded(rt *module.Runtime, apiURL, token string) {
	if err := conn.RevokeSessionToken(rt, apiURL, token); err != nil {
		fmt.Fprintf(rt.Stderr, "Warning: could not end a replaced "+
			"session with pgEdge: %s\n", output.Sanitize(err.Error()))
	}
}

// promptCredentials asks for a client ID and secret on stdin. Call it
// only when neither credential flag was supplied: a closed stdin fails
// at the first read, which would break `login` in a script.
func promptCredentials(rt *module.Runtime) (id, secret string, err error) {
	reader := bufio.NewReader(rt.Stdin)

	fmt.Fprint(rt.Stderr, "Client ID: ")
	id, err = reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("read client ID: %w", err)
	}

	secret, err = readSecret(rt, reader)
	if err != nil {
		return "", "", fmt.Errorf("read secret: %w", err)
	}
	return strings.TrimSpace(id), secret, nil
}

// readSecret reads the client secret, unechoed on a terminal and as a
// plain line otherwise.
func readSecret(rt *module.Runtime, reader *bufio.Reader) (string, error) {
	if file, ok := rt.Stdin.(*os.File); ok &&
		term.IsTerminal(int(file.Fd())) {
		fmt.Fprint(rt.Stderr, "Client Secret: ")
		secretBytes, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(rt.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(secretBytes)), nil
	}

	fmt.Fprint(rt.Stderr, "Client Secret: ")
	secret, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(secret), nil
}
