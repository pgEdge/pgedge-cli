// Package auth resolves API credentials and manages cached access
// tokens per profile and module under ~/.pgedge/cli/cache/.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/atomicfile"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
)

// RefreshWindow is how close to expiry a cached token may be before
// callers treat it as stale and exchange credentials again.
const RefreshWindow = 5 * time.Minute

// Credentials holds a resolved client ID and secret.
type Credentials struct {
	ClientID     string
	ClientSecret string
}

// Auth performs credential resolution and token-cache management for
// one profile+module pair. The token exchange itself belongs to
// internal/starfleet/conn, which owns the typed /account/v1/oauth/token call.
type Auth struct {
	Profile  string
	Module   string
	CacheDir string // empty => ~/.pgedge/cli/cache

	// Keychain holds the profile's secret when the config file does
	// not; nil means unavailable. ConfigPath is the file the profile
	// lives in, which keys the entry (keychain.Account).
	Keychain   keychain.Store
	ConfigPath string
}

// Credential sources ResolveCredentials reports.
const (
	SourceFlags    = "flags"
	SourceEnv      = "env"
	SourceKeychain = "keychain"
	SourceConfig   = "config"
)

// The environment pair. Empty counts as unset.
const (
	EnvClientID     = "PGEDGE_CLIENT_ID"
	EnvClientSecret = "PGEDGE_CLIENT_SECRET" //nolint:gosec // G101: a variable name, not a credential
)

// CachedToken is the on-disk token cache format.
type CachedToken struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Fingerprint binds the token to the API base URL, client ID and
	// client secret that minted it (see Fingerprint). A cache file older
	// than the field, or one using the older name credential_fingerprint,
	// decodes to "", which MintedBy treats as a mismatch, costing one
	// silent re-exchange.
	// The name says binding because the digest covers the URL too.
	Fingerprint string `json:"binding_fingerprint,omitempty"`
}

// fingerprintDomain separates the binding fingerprint from any other hash
// of the same secret, now or later, so it is fixed and not configurable.
// Its version tracks the shape of the hashed tuple, not the code: v2 added
// the API URL, and a domain that outlived its tuple would weaken the
// separation it exists for.
const fingerprintDomain = "pgedge-cli/token-cache/v2"

// Fingerprint derives a stable, one-way identifier for the connection
// that minted a cached token: SHA-256 over fingerprintDomain, the
// resolved API base URL, the client ID and the client secret, with a NUL
// byte before each input so ("a","bc") and ("ab","c") differ.
//
// apiURL is hashed exactly as resolved, unnormalized, the same string the
// CLI is about to dial. A false mismatch costs one extra token exchange
// with that host; a false match hands a live bearer token to a host that
// never minted it, and normalizing can only produce the second kind. A
// trailing slash therefore re-exchanges, by design.
//
// The digest is never printed; only whether it matches (MintedBy) is
// user-facing. Hashing the secret is acceptable in a file that already
// holds a live bearer token at 0600, and the client ID goes into the same
// hash rather than beside it in the clear. The API URL is no secret, and
// both diagnostics print it, but it shares the digest so that "same
// connection" is one comparison in one place, not two mechanisms that
// must agree.
func Fingerprint(apiURL, clientID, clientSecret string) string {
	h := sha256.New()
	h.Write([]byte(fingerprintDomain))
	h.Write([]byte{0})
	h.Write([]byte(apiURL))
	h.Write([]byte{0})
	h.Write([]byte(clientID))
	h.Write([]byte{0})
	h.Write([]byte(clientSecret))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// MintedBy reports whether t was minted for the given API base URL by the
// given credential pair. An empty Fingerprint (a cache file written
// before the field existed, or before it covered the URL) never matches:
// the safe answer to unknown provenance is no. The URL is part of the
// question because a cached token is a bearer credential for one
// endpoint's auth server; matching on the credential alone would let
// `--api-url otherhost` send a live token to a host that never issued it.
//
// Plain == is deliberate: both operands are local, one from this 0600
// cache file and the other from the resolved credential (flags, env,
// keychain or the 0600 config file), so there is no remote timing channel,
// and anyone placed to time this can already read both inputs.
func (t *CachedToken) MintedBy(apiURL, clientID, clientSecret string) bool {
	if t.Fingerprint == "" {
		return false
	}
	return t.Fingerprint == Fingerprint(apiURL, clientID, clientSecret)
}

// validatePathSegment rejects identifiers that could escape the
// cache directory once embedded in a file name (e.g. a --profile
// value of "../../etc/passwd"). Profile comes from --profile or the
// config, so this is a real guard.
func validatePathSegment(kind, name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) ||
		name == "." || name == ".." {
		return fmt.Errorf("auth: invalid %s %q", kind, name)
	}
	return nil
}

func (a *Auth) cachePath() (string, error) {
	if err := validatePathSegment("profile", a.Profile); err != nil {
		return "", err
	}
	if err := validatePathSegment("module", a.Module); err != nil {
		return "", err
	}
	dir := a.CacheDir
	if dir == "" {
		d, err := config.DefaultCacheDir()
		if err != nil {
			return "", fmt.Errorf("auth: %w", err)
		}
		dir = d
	}
	name := fmt.Sprintf("%s-%s.json", a.Profile, a.Module)
	return filepath.Join(dir, name), nil
}

// PartialFlagPairError reports that exactly one half of the flag or
// environment credential pair was supplied. It is a type so that
// `starfleet doctor` and `starfleet auth status` can tell a half pair from
// no credentials at all (errors.As, through conn.IsUsageError): the fixes
// differ, and a working pair in the config file is ignored while an
// incomplete override stands. The message text is user-facing and
// expected to change, so it is not what they match on.
type PartialFlagPairError struct {
	// Given is the flag the operator supplied, Missing its counterpart.
	Given, Missing string
}

func (e *PartialFlagPairError) Error() string {
	return e.Given + " given without " + e.Missing +
		": supply both or neither"
}

// FlagCredentials resolves the --client-id/--client-secret pair on its
// own, ahead of any other source. It returns credentials when both are
// supplied, a *PartialFlagPairError when exactly one is, and
// (nil, nil) when neither is, which is not a failure and is for the
// caller to interpret.
//
// Every resource command takes the whole chain through
// ResolveCredentials. `starfleet auth login` takes only this link: it
// writes the config, so falling through would answer "who are you
// signing in as?" from ambient state, skip the prompt for anyone already
// configured, and re-save the credentials being replaced. Sharing this
// function keeps the two from disagreeing about the same input.
func FlagCredentials(flagID, flagSecret string) (*Credentials, error) {
	switch {
	case flagID != "" && flagSecret != "":
		return &Credentials{flagID, flagSecret}, nil
	case flagID != "":
		return nil, &PartialFlagPairError{
			Given: "--client-id", Missing: "--client-secret"}
	case flagSecret != "":
		return nil, &PartialFlagPairError{
			Given: "--client-secret", Missing: "--client-id"}
	}
	return nil, nil
}

// EnvCredentials resolves the PGEDGE_CLIENT_ID/PGEDGE_CLIENT_SECRET
// pair with FlagCredentials' rules: both, neither, or a
// *PartialFlagPairError naming the missing variable, so a typo in a
// pipeline cannot fall through to a stored identity.
func EnvCredentials() (*Credentials, error) {
	id, secret := os.Getenv(EnvClientID), os.Getenv(EnvClientSecret)
	switch {
	case id != "" && secret != "":
		return &Credentials{id, secret}, nil
	case id != "":
		return nil, &PartialFlagPairError{
			Given: EnvClientID, Missing: EnvClientSecret}
	case secret != "":
		return nil, &PartialFlagPairError{
			Given: EnvClientSecret, Missing: EnvClientID}
	}
	return nil, nil
}

// ErrNoCredentials is the "nothing configured anywhere" failure.
var ErrNoCredentials = errors.New("no credentials found — use " +
	"--client-id/--client-secret, set PGEDGE_CLIENT_ID and " +
	"PGEDGE_CLIENT_SECRET, or run 'pgedge starfleet auth login'")

// ResolveCredentials returns credentials from the highest-priority
// source: flags > environment > profile, and names the source.
//
// A half-supplied flag or env pair is a usage error, not a fallback:
// falling through would let a mistyped ID authenticate as whoever the
// config file names. A half-filled config section is stale state, so
// it falls through to "no credentials found" instead.
//
// The profile's secret comes from the config file when it holds one,
// else from the keychain; login writes one or the other, never both.
func (a *Auth) ResolveCredentials(ap *config.StarfleetProfile,
	flagID, flagSecret string) (*Credentials, string, error) {
	creds, err := FlagCredentials(flagID, flagSecret)
	if err != nil {
		return nil, "", err
	}
	if creds != nil {
		return creds, SourceFlags, nil
	}
	creds, err = EnvCredentials()
	if err != nil {
		return nil, "", err
	}
	if creds != nil {
		return creds, SourceEnv, nil
	}
	if ap == nil || ap.ClientID == "" {
		return nil, "", ErrNoCredentials
	}
	if ap.ClientSecret != "" {
		return &Credentials{ap.ClientID, ap.ClientSecret},
			SourceConfig, nil
	}
	secret, err := a.KeychainSecret()
	switch {
	case err == nil:
		return &Credentials{ap.ClientID, secret}, SourceKeychain, nil
	case errors.Is(err, keychain.ErrNotFound):
		return nil, "", ErrNoCredentials
	default:
		return nil, "", fmt.Errorf("could not read the client secret "+
			"from the OS keychain (%v) — run 'pgedge starfleet auth "+
			"login', adding --insecure-storage to keep the secret in "+
			"the config file instead", err)
	}
}

// KeychainAccount is this profile's keychain entry name.
func (a *Auth) KeychainAccount() string {
	return keychain.Account(a.Profile, a.ConfigPath)
}

// KeychainSecret reads this profile's secret from the keychain.
func (a *Auth) KeychainSecret() (string, error) {
	if a.Keychain == nil {
		return "", keychain.ErrUnavailable
	}
	return a.Keychain.Get(a.KeychainAccount())
}

// SaveToken writes the token to the profile+module cache file with
// 0600 permissions. It refuses a token with no Fingerprint, so no
// caller, including a future one, can write an unbound cache file;
// conn.Login, the only caller, always stamps one first.
func (a *Auth) SaveToken(tok *CachedToken) error {
	if tok.Fingerprint == "" {
		return fmt.Errorf(
			"auth: refusing to save a token with no binding fingerprint")
	}
	p, err := a.cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("auth: create cache dir: %w", err)
	}
	raw, err := json.Marshal(tok) //nolint:gosec // G117: local token cache file, not an exposure
	if err != nil {
		return fmt.Errorf("auth: marshal token: %w", err)
	}
	if err := atomicfile.Write(p, raw, 0o600); err != nil {
		return fmt.Errorf("auth: write cache: %w", err)
	}
	return nil
}

// LoadToken reads the cached token for this profile+module. A missing
// or unparseable cache returns an error; callers treat that as "no
// token".
func (a *Auth) LoadToken() (*CachedToken, error) {
	p, err := a.cachePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p) //nolint:gosec // G304: path validated in cachePath
	if err != nil {
		return nil, err
	}
	var tok CachedToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// ClearToken removes the cached token (auth logout), including any
// staging file an interrupted write left behind, because logout promises
// to remove every on-disk copy of the token. atomicfile.Write cleans up
// with a deferred Remove, so a signal between its write and rename
// (Ctrl-C, SIGKILL, an OOM kill) leaves a 0600 file holding a live bearer
// token under a name no other code path knows. Nothing else in this CLI
// enumerates the cache directory, so without the sweep that token would
// sit there until its 24h expiry while logout reported success. The
// window is tiny; the promise is not conditional.
//
// The sweep matches a literal prefix over os.ReadDir, not filepath.Glob:
// the profile name is user-controlled and validatePathSegment allows
// glob characters, so a profile named `*` would match every other
// profile's staging files.
func (a *Auth) ClearToken() error {
	p, err := a.cachePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("auth: clear token: %w", err)
	}
	return a.clearStagingFiles(p)
}

// clearStagingFiles removes the interrupted-write residue described on
// ClearToken. An absent cache directory means nothing was ever cached,
// and logout must not fail on it.
//
// Every other read failure is reported. A directory at mode 0300 lets
// os.Remove unlink the cache file but fails os.ReadDir with EACCES, and
// swallowing that would report a successful logout with a live token
// still staged. SaveToken creates the directory 0700, so only an
// operator's hand-set mode gets here, and a silent no-op is what would
// keep the token.
func (a *Auth) clearStagingFiles(cacheFile string) error {
	dir := filepath.Dir(cacheFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("auth: scan cache dir: %w", err)
	}
	prefix := atomicfile.StagingPrefix(filepath.Base(cacheFile))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil &&
			!os.IsNotExist(err) {
			return fmt.Errorf("auth: clear staged token: %w", err)
		}
	}
	return nil
}
