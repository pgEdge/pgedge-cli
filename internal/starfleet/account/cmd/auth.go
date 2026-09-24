package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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

// NewAuthCmd builds the `pgedge starfleet auth` command group. The cloud
// root owns f, so every verb below reads the one set of connection
// flags bound there.
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
	var insecureStorage bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with pgEdge Starfleet",
		Long: `login takes a client ID and secret, exchanges them for an
access token, and saves them to the active profile. The secret goes
to the OS keychain (Keychain on macOS, the Secret Service on Linux,
Credential Manager on Windows); the client ID and API URL go to
~/.pgedge/cli/config.yaml. Where no keychain can be used, login
writes the secret to the config file instead and warns that it did.
--insecure-storage writes it to the config file without trying the
keychain.

Supply both with --client-id and --client-secret to run it
unattended, or omit them to be prompted; the secret is never echoed
either way. Supplying only one of the pair is a usage error (exit 2)
rather than a prompt for the other, so a mistyped flag cannot sign
you in under a different identity.

Run this once per profile. The credentials serve every pgEdge Starfleet
module — account, byoc and managed — which share one connection and
one token. Provide --api-url to target a non-default API endpoint.

login ignores PGEDGE_CLIENT_ID and PGEDGE_CLIENT_SECRET, and warns
when they are set, because every other command uses them instead of
the profile until they are unset.

Example:
  pgedge starfleet auth login
  pgedge starfleet auth login --api-url https://api.pgedge.com
  pgedge starfleet auth login --client-id ID --client-secret SECRET
  pgedge starfleet auth login --insecure-storage`,
		Annotations: map[string]string{
			cli.AnnotationProfileExempt: "creates the named profile, " +
				"so a new name is the point rather than a typo",
		},
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// Flags first, and flags only. login deliberately does NOT
			// use conn/auth's full resolution chain: it is the command
			// that writes the config, so falling through to env or the
			// stored profile would skip the prompt for anyone already
			// signed in and re-save the very credentials they are here
			// to replace. See auth.FlagCredentials.
			creds, err := auth.FlagCredentials(f.ClientID, f.ClientSecret)
			if err != nil {
				// Half a pair is a typo, not an auth failure — the same
				// call the resource commands make (conn.Resolve) and the
				// same code they return for it.
				return newExitError(err.Error(), ExitUsage)
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
				// ExitAuth, not a plain error. `login` used to wrap this
				// with fmt.Errorf, so nothing implementing coder reached
				// cli.ExitCode and a rejected credential exited 1 here
				// while the identical rejection from any resource command
				// exited ExitAuth. conn.Resolve deliberately tags the same
				// token() failure, so this was the one path that did not.
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

			// The file Save() actually wrote, not DefaultPath(): Save
			// writes the --config value when one was given (#264).
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
			if os.Getenv(auth.EnvClientID) != "" ||
				os.Getenv(auth.EnvClientSecret) != "" {
				fmt.Fprintln(rt.Stderr, "Warning: PGEDGE_CLIENT_ID or "+
					"PGEDGE_CLIENT_SECRET is set, so other commands use "+
					"it instead of this profile until it is unset.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&insecureStorage, "insecure-storage", false,
		"store the client secret in the config file, not the OS keychain")
	return cmd
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

// authStatusReport is `auth status`'s -o json/-o yaml payload. Field
// names mirror `cloud doctor`'s authInfo (doctor.go) wherever the
// concepts match — authenticated, source, problem, token_valid,
// expires_at — so the two commands never describe the same fact under
// two different keys. It is a distinct type rather than a reuse of
// authInfo because status also reports the client ID and the
// resolved API URL, which doctor does not carry; this is its own
// output surface, not another reason to churn authInfo's shape.
//
// ClientSecret never appears here, in any field, under any name: the
// client ID is an identifier and is printed by design (the same
// ruling `profile show` follows), but the secret is a credential and
// must never round-trip through any output format.
type authStatusReport struct {
	Authenticated bool   `json:"authenticated"`
	Source        string `json:"source,omitempty"`
	// Problem carries a credential-resolution failure this command can
	// name more precisely than "not authenticated": a usage error or an
	// unreadable keychain. Absent for the
	// ordinary no-credentials-anywhere case, matching authInfo's own
	// convention.
	Problem    string `json:"problem,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	APIURL     string `json:"api_url,omitempty"`
	TokenValid bool   `json:"token_valid"`
	// TokenBound mirrors authInfo.TokenBound (doctor.go): whether the
	// cached token was minted for the connection now resolved — the
	// credential and the API URL reported above, together. See that
	// field's comment for the "meaningful only alongside TokenValid"
	// caveat and for why one boolean cannot say which of the two
	// diverged; both apply identically here.
	TokenBound bool   `json:"token_bound"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

func newAuthStatusCmd(rt *module.Runtime, f *conn.Flags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current authentication state",
		Long: `status reports which credential source is active for the
current profile, the API URL in effect, and whether a cached token
is present and still valid. -o json and -o yaml carry the same facts
as a single object, so a script can check them without parsing prose.

It exits 5 (the auth-failure code) when no usable
credentials resolve, 2 if you supplied half a credential pair, and 0
whenever they do — even if no token is
cached yet or the cached one has expired, since the CLI mints a new
one on demand the next time it is needed. Run it to confirm you are
signed in before using resource commands.

Example:
  pgedge starfleet auth status
  pgedge starfleet auth status -o json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := conn.ResolveCredentials(
				rt, f.ClientID, f.ClientSecret, f.APIURL)
			if err != nil {
				// Same misdiagnosis `cloud doctor` had: a usage error is
				// not an absence of credentials, and saying "Not
				// authenticated." sends the operator looking for a login
				// they may already have. status is a diagnostic, so it
				// names the cause instead.
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
				// A usage error is the operator mistyping the command,
				// matching conn.Resolve; anything else is "no usable
				// credentials", ExitAuth. doctor's own exit code is
				// deliberately unaffected: it never fails on missing
				// credentials. err carries the remedy, so main.go's
				// "Error: ..." line is never a bare duplicate.
				code := conn.ExitAuth
				if usage {
					code = conn.ExitUsage
				}
				return conn.NewExitError(err.Error(), code)
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

			// A missing or expired token is not a failure: it just means
			// the next command that needs one will fetch it. Credentials
			// resolving is the bar for exit 0, not a warm token cache.
			if res.Env {
				lines = append(lines,
					"Token:        never cached (env credentials)")
				return printAuthStatus(
					rt, report, strings.Join(lines, "\n")+"\n")
			}
			tok, err := conn.Store(rt).LoadToken()
			switch {
			case err != nil:
				lines = append(lines, "Token:        not cached")
			case time.Until(tok.ExpiresAt) <= 0:
				report.ExpiresAt = tok.ExpiresAt.Format(time.RFC3339)
				lines = append(lines, "Token:        expired")
			case !tok.MintedBy(apiURL, creds.ClientID, creds.ClientSecret):
				// Unexpired but minted for a different connection — a
				// rekey, a one-off flag override landing on a
				// profile-minted cache (D3/D7), or an --api-url naming
				// an endpoint other than the one that minted the token
				// (#146). Not a failure: the next command that actually
				// needs a token re-authenticates on its own
				// (conn.token). TokenValid stays true here — it means
				// exactly what it has always meant, unexpired — and
				// TokenBound (left false) is what carries the mismatch;
				// this must agree with doctor's authInfo, which
				// computes TokenValid from expiry alone.
				//
				// apiURL is the value resolved and printed above, so
				// the line below can never describe a different
				// endpoint from the one this report names.
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

			return printAuthStatus(
				rt, report, strings.Join(lines, "\n")+"\n")
		},
	}
}

// printAuthStatus renders report through rt.Output for json/yaml, so
// -o works the same way it does for every other command in the tree,
// or writes text verbatim for the text/table format, preserving the
// exact prose auth status has always printed.
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
client credentials for the active profile, from the OS keychain and
from ~/.pgedge/cli/config.yaml. The API URL and the profile itself
are kept, so 'pgedge starfleet auth login' can sign in again without
re-entering the endpoint.

Example:
  pgedge starfleet auth logout`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := conn.Logout(rt); err != nil {
				return fmt.Errorf("clear token: %w", err)
			}
			// A secret kept in the file means login never left one in
			// the keychain (it deletes a stale entry when it writes the
			// file), so a host without a keychain is not asked. One that
			// cannot be reached warns rather than stopping the file side
			// being cleared.
			cp := rt.Config.StarfleetProfile(rt.Profile)
			if cp.ClientSecret == "" {
				if err := keychainDelete(conn.Store(rt)); err != nil {
					fmt.Fprintf(rt.Stderr, "Warning: could not remove "+
						"the client secret from the OS keychain: %s\n",
						output.Sanitize(err.Error()))
				}
			}
			// Drop the persisted client_id/client_secret so a
			// long-lived secret never outlives the session, keeping
			// the profile's api_url for the next login.
			rt.Config.SetStarfleetProfile(rt.Profile,
				&config.StarfleetProfile{APIURL: cp.APIURL})
			if err := rt.Config.Save(); err != nil {
				return fmt.Errorf("clear credentials: %w", err)
			}
			fmt.Fprintln(rt.Stderr,
				"Logged out. Token and stored credentials removed.")
			return nil
		},
	}
}

// promptCredentials asks for a client ID and secret on stdin. It is
// reached only when neither credential flag was supplied — reading
// stdin unconditionally is what made `login` unusable from a script,
// since a closed stdin fails at the first read no matter what the
// caller passed on the command line.
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

// readSecret reads the client secret. When stdin is an interactive
// terminal the input is not echoed; otherwise (tests, pipes) a plain
// line is read from the buffered reader so the flow stays scriptable.
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
