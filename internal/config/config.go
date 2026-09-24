// Package config loads and saves the pgedge CLI configuration at
// ~/.pgedge/cli/config.yaml with named profiles carrying per-module
// sections.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pgEdge/pgedge-cli/internal/atomicfile"
)

// StarfleetProfile holds the Starfleet API connection for a profile. One
// product means one credential: the account-level commands, byoc and
// managed all read this section, exactly as they share one token on
// the wire.
type StarfleetProfile struct {
	APIURL       string `yaml:"api_url,omitempty"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
}

// ControlplaneProfile holds Control Plane module settings within a profile.
// The Control Plane has no login/token; connection is a base URL plus
// optional mTLS cert file paths.
type ControlplaneProfile struct {
	BaseURL            string   `yaml:"base_url,omitempty"`
	BaseURLs           []string `yaml:"base_urls,omitempty"`
	CACert             string   `yaml:"ca_cert,omitempty"`
	ClientCert         string   `yaml:"client_cert,omitempty"`
	ClientKey          string   `yaml:"client_key,omitempty"`
	InsecureSkipVerify bool     `yaml:"insecure_skip_verify,omitempty"`
	Timeout            string   `yaml:"timeout,omitempty"`
}

// EffectiveBaseURLs returns the candidate Control Plane URLs for the
// profile: BaseURLs when set, else the legacy singular BaseURL as a
// one-element list, else nil (the caller applies the default).
func (p *ControlplaneProfile) EffectiveBaseURLs() []string {
	if len(p.BaseURLs) > 0 {
		return p.BaseURLs
	}
	if p.BaseURL != "" {
		return []string{p.BaseURL}
	}
	return nil
}

// Profile is a named context carrying per-module sections.
type Profile struct {
	Starfleet    *StarfleetProfile    `yaml:"starfleet,omitempty"`
	Controlplane *ControlplaneProfile `yaml:"controlplane,omitempty"`
}

// OutputPrefs holds user output defaults.
type OutputPrefs struct {
	Format string `yaml:"format,omitempty"`
}

// Config is the root of ~/.pgedge/cli/config.yaml.
type Config struct {
	CurrentProfile string             `yaml:"current_profile,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles,omitempty"`
	Output         OutputPrefs        `yaml:"output,omitempty"`

	path string
}

// pathErrorf wraps a filesystem error under the "config: " prefix,
// adding the operation and path only when the error does not already
// carry them. An *os.PathError, which os.ReadFile and os.MkdirAll
// return, already reads "<op> <path>: <err>"; wrapping it again prints
// "config: read /tmp: read /tmp: is a directory". Any other error still
// gets the path named.
func pathErrorf(op, path string, err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("config: %w", err)
	}
	return fmt.Errorf("config: %s %s: %w", op, path, err)
}

// Path returns the file this config was loaded from — the --config
// value when one was given, DefaultPath otherwise. A caller reporting on
// the config reads this rather than re-deriving the default, which under
// --config names a file the invocation is not reading.
func (c *Config) Path() string { return c.path }

// DefaultPath returns ~/.pgedge/cli/config.yaml. The cli/ segment is
// deliberate: ~/.pgedge is shared with other pgEdge tools, and a file
// at its root is a file another tool may want (decided 2026-08-25).
func DefaultPath() (string, error) {
	dir, err := defaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// DefaultCacheDir returns ~/.pgedge/cli/cache, which holds the token
// cache and selfupdate's sigstore cache. It lives here rather than in
// internal/auth so the config and cache paths derive from one root.
func DefaultCacheDir() (string, error) {
	dir, err := defaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache"), nil
}

func defaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home: %w", err)
	}
	return filepath.Join(home, ".pgedge", "cli"), nil
}

// Load reads the config file, resolving its path by precedence:
// the explicit path argument (the --config flag) > DefaultPath.
//
// A missing DEFAULT path is not an error — first run has no config
// file yet — but a missing EXPLICIT one is. Falling back silently would
// resolve the built-in default profile, whose api_url is PRODUCTION:
// failing open in the dangerous direction, indistinguishable from
// success.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	cfg := &Config{path: path}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-specified --config path is intentional
	if os.IsNotExist(err) {
		if explicit {
			return nil, fmt.Errorf(
				"config: %s does not exist (check the path, create "+
					"the file and any parent directory, or omit "+
					"--config to use the default)", path)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, pathErrorf("read", path, err)
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// yaml.Unmarshal cannot reach the unexported path, so the literal's
	// assignment above still holds.
	return cfg, nil
}

// SavePath is the file Save writes: Path, else the default. It keys a
// profile's keychain entry, so login and every later read agree on it.
// A nil Config answers the default.
func (c *Config) SavePath() string {
	if c != nil && c.path != "" {
		return c.path
	}
	p, err := DefaultPath()
	if err != nil {
		return ""
	}
	return p
}

// Save writes the config back to its path with 0600 permissions,
// creating its directory (~/.pgedge/cli by default) with 0700 if needed.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		c.path = p
	}
	return c.writeTo(c.path)
}

// SaveTo writes the config to path and remembers path for any later
// Save call. It is how a test that builds a *Config by hand, rather than
// through Load, chooses where it lands. It shares writeTo with Save, so
// the 0700 directory and 0600 file hold through either entry point.
func (c *Config) SaveTo(path string) error {
	c.path = path
	return c.writeTo(path)
}

// writeTo is Save and SaveTo's shared write path: create the parent
// directory at 0700, render, and replace the file at 0600 in one rename.
// This file is the only copy of every profile and credential, so a
// truncate-in-place write interrupted halfway would lose all of them.
//
// The destination is read first because render merges into it (see
// render). A missing or unreadable destination renders as a new file,
// as on first run.
func (c *Config) writeTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return pathErrorf("create dir", filepath.Dir(path), err)
	}
	existing, err := os.ReadFile(path) //nolint:gosec // G304: the write destination, resolved above
	if err != nil {
		existing = nil
	}
	raw, err := c.render(existing)
	if err != nil {
		return fmt.Errorf("config: render: %w", err)
	}
	// Not pathErrorf: atomicfile's error names the staging file, which
	// the user has never seen and which no longer exists, so the
	// destination is always added.
	if err := atomicfile.Write(path, raw, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// ResolveProfile returns the active profile name:
// flag > current_profile > "default".
func (c *Config) ResolveProfile(flag string) string {
	if flag != "" {
		return flag
	}
	if c.CurrentProfile != "" {
		return c.CurrentProfile
	}
	return "default"
}

// ProfileNames returns the configured profile names, sorted.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateProfile reports an error when name is not a key of
// c.Profiles, and nil when it is. It is the one place the
// unknown-profile text is written, so every caller emits it
// byte-identically.
//
// SetCurrentProfile calls this directly; the read side (the profile
// guard, `profile show`, `profile list`'s resolved column) goes through
// internal/cli.CheckProfileName, which accepts the built-in "default"
// first. So `--profile default` and `profile show default` succeed on a
// config with no `default` section while `profile use default` fails:
// `use` selects a CONFIGURED profile, and the built-in is the absence of
// configuration. llms.txt and the pgedge skill both state this
// exception; it is deliberate.
//
// With no profiles configured, the message names the commands that
// create one rather than an empty list, and names both because this
// package cannot tell which applies: a controlplane-only operator has
// no Starfleet credentials to log in with. Creating the profile they
// name is also why both carry cli.AnnotationProfileExempt.
func (c *Config) ValidateProfile(name string) error {
	if _, ok := c.Profiles[name]; ok {
		return nil
	}
	names := c.ProfileNames()
	if len(names) == 0 {
		return fmt.Errorf(
			"unknown profile %q — no profiles are configured; run "+
				"'pgedge starfleet auth login --profile %s' or "+
				"'pgedge controlplane config set --base-url <url> --profile %s' "+
				"to create one",
			name, name, name)
	}
	return fmt.Errorf(
		"unknown profile %q — configured profiles: %s",
		name, strings.Join(names, ", "))
}

// SetCurrentProfile points current_profile at an existing profile. An
// unknown name is an error rather than a silent write, which would
// break every subsequent command with a confusing "no credentials"
// message once current_profile no longer names anything real.
func (c *Config) SetCurrentProfile(name string) error {
	if err := c.ValidateProfile(name); err != nil {
		return err
	}
	c.CurrentProfile = name
	return nil
}

// StarfleetProfile returns the starfleet section of the named profile.
// It never returns nil; absent profiles yield an empty struct.
//
// The result is always a fresh copy, so mutating it never persists and
// SetStarfleetProfile is the only write path. Handing back the live
// pointer would make mutate-then-Save persist on one path and not on
// another.
func (c *Config) StarfleetProfile(name string) *StarfleetProfile {
	p, ok := c.Profiles[name]
	if !ok || p.Starfleet == nil {
		return &StarfleetProfile{}
	}
	cloud := *p.Starfleet
	return &cloud
}

// SetStarfleetProfile stores the starfleet section under the named profile,
// creating maps as needed.
func (c *Config) SetStarfleetProfile(name string, cp *StarfleetProfile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	p := c.Profiles[name]
	p.Starfleet = cp
	c.Profiles[name] = p
}

// ControlplaneProfile returns the controlplane section of the named
// profile. It never returns nil; absent profiles yield an empty struct.
func (c *Config) ControlplaneProfile(name string) *ControlplaneProfile {
	if p, ok := c.Profiles[name]; ok && p.Controlplane != nil {
		return p.Controlplane
	}
	return &ControlplaneProfile{}
}

// SetControlplaneProfile stores the controlplane section under the named
// profile, creating maps as needed.
func (c *Config) SetControlplaneProfile(name string, cp *ControlplaneProfile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	p := c.Profiles[name]
	p.Controlplane = cp
	c.Profiles[name] = p
}
