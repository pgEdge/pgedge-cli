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
			// Flags only, not the full resolution chain; see
			// auth.FlagCredentials for why.
			creds, err := auth.FlagCredentials(f.ClientID, f.ClientSecret)
			if err != nil {
				// Half a pair is a typo, the code conn.Resolve returns.
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
	Problem    string `json:"problem,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	APIURL     string `json:"api_url,omitempty"`
	TokenValid bool   `json:"token_valid"`
	// TokenBound means what authInfo.TokenBound (doctor.go) means.
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

			return printAuthStatus(
				rt, report, strings.Join(lines, "\n")+"\n")
		},
	}
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
