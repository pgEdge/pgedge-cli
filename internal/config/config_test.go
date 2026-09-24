package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Only the DEFAULT path may be absent. The explicit-path rule is
// TestLoadExplicitMissingPathIsAnError.
func TestLoadMissingDefaultPathReturnsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProfile != "" || len(cfg.Profiles) != 0 {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

// An EXPLICIT --config path that does not exist must fail. Only the
// default path may be absent, because first run has no config file
// yet. Falling back silently re-aims the CLI at the built-in default
// profile, which points at production, so a typo in --config fails
// OPEN in the dangerous direction.
func TestLoadExplicitMissingPathIsAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	cfg, err := Load(missing)
	if err == nil {
		t.Fatalf("want an error for a missing explicit path, got %+v", cfg)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name the path, got %q", err)
	}
}

func TestDefaultPathHomeError(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := DefaultPath(); err == nil {
		t.Fatal("want error when $HOME cannot be resolved")
	}
}

func TestLoadHomeErrorPropagates(t *testing.T) {
	// This test needs HOME itself to be unresolvable so DefaultPath
	// fails: no other isolation is required now that Load consults
	// only the explicit path argument and DefaultPath.
	t.Setenv("HOME", "")
	if _, err := Load(""); err == nil {
		t.Fatal("want error when DefaultPath fails")
	}
}

func TestLoadUnparseableYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(
		p, []byte("not: [valid: yaml"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("want error parsing malformed YAML")
	}
}

func TestLoadReadFilePermissionError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("current_profile: x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	if _, err := Load(p); err == nil {
		t.Fatal("want error reading a file without read permission")
	}
}

func TestSaveHomeErrorPropagates(t *testing.T) {
	t.Setenv("HOME", "")
	cfg := &Config{CurrentProfile: "default"}
	if err := cfg.Save(); err == nil {
		t.Fatal("want error when DefaultPath fails")
	}
}

func TestSaveMkdirAllError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	cfg := &Config{CurrentProfile: "default"}
	cfg.path = filepath.Join(parent, "sub", "config.yaml")
	if err := cfg.Save(); err == nil {
		t.Fatal("want error creating a dir under an unwritable parent")
	}
}

func TestSaveWriteFileError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	// Pre-create a directory where the config file should go, so
	// WriteFile fails.
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{CurrentProfile: "default"}
	cfg.path = p
	if err := cfg.Save(); err == nil {
		t.Fatal("want error writing a file over a directory")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		CurrentProfile: "default",
		Profiles: map[string]Profile{
			"default": {Starfleet: &StarfleetProfile{
				ClientID:     "id1",
				ClientSecret: "sec1",
			}},
		},
	}
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg.path = p
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir perms = %o, want 700", perm)
	}
	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	cp := got.StarfleetProfile("default")
	if cp.ClientID != "id1" || cp.ClientSecret != "sec1" {
		t.Errorf("round trip lost data: %+v", cp)
	}
}

func TestSaveTightensExistingFilePerms(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("current_profile: default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{CurrentProfile: "default"}
	cfg.path = p
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600", perm)
	}
}

func TestResolveProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name    string
		flag    string
		current string
		want    string
	}{
		{name: "flag wins", flag: "f",
			current: "c", want: "f"},
		{name: "config current", current: "c", want: "c"},
		{name: "default fallback", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{CurrentProfile: tt.current}
			if got := c.ResolveProfile(tt.flag); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestControlplaneProfileRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Absent section yields a non-nil empty struct.
	if got := cfg.ControlplaneProfile("default"); got == nil {
		t.Fatal("ControlplaneProfile returned nil")
	}
	cfg.SetControlplaneProfile("default", &ControlplaneProfile{
		BaseURL: "https://cp-1:3000", CACert: "/ca.crt",
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.ControlplaneProfile("default").BaseURL; got !=
		"https://cp-1:3000" {
		t.Errorf("BaseURL = %q, want https://cp-1:3000", got)
	}
}

func TestControlplaneProfileEffectiveBaseURLs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name string
		p    ControlplaneProfile
		want []string
	}{
		{"list wins", ControlplaneProfile{
			BaseURLs: []string{"http://a", "http://b"},
			BaseURL:  "http://legacy"},
			[]string{"http://a", "http://b"}},
		{"legacy singular", ControlplaneProfile{BaseURL: "http://legacy"},
			[]string{"http://legacy"}},
		{"neither", ControlplaneProfile{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.p.EffectiveBaseURLs()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStarfleetProfileReadsStarfleetSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := `
current_profile: dev
profiles:
  dev:
    starfleet:
      api_url: https://staging.example
      client_id: acct-id
      client_secret: acct-secret
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ap := cfg.StarfleetProfile("dev")
	if ap.APIURL != "https://staging.example" {
		t.Errorf("APIURL = %q", ap.APIURL)
	}
	if ap.ClientID != "acct-id" || ap.ClientSecret != "acct-secret" {
		t.Errorf("creds = %q/%q", ap.ClientID, ap.ClientSecret)
	}
}

// TestByocConfigSectionIsIgnored is the regression gate on the deleted
// pre-v0.2 config shim. A `byoc:` section used to stand in for the
// profile's credential section; it is now an unrecognised key, which
// yaml.Unmarshal drops. So the profile resolves empty and the operator
// is told there are no credentials — the loud failure — rather than
// authenticating off a section nothing else in the CLI understands.
//
// The second subtest is the one that would catch a partial revert: with
// both sections present the old shim also preferred the credential
// section, so only the byoc-only case distinguishes "shim gone" from
// "shim back".
func TestByocConfigSectionIsIgnored(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("byoc only resolves empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		raw := `
profiles:
  dev:
    byoc:
      api_url: https://legacy.example.com
      client_id: legacy-id
      client_secret: legacy-secret
`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		ap := cfg.StarfleetProfile("dev")
		if ap.APIURL != "" || ap.ClientID != "" || ap.ClientSecret != "" {
			t.Errorf("StarfleetProfile = %+v, want empty: the byoc: "+
				"section shim is deleted", ap)
		}
	})

	t.Run("cloud is read beside a byoc section", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		raw := `
profiles:
  dev:
    starfleet:
      client_id: new-id
    byoc:
      client_id: old-id
`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.StarfleetProfile("dev").ClientID; got != "new-id" {
			t.Errorf("ClientID = %q, want new-id", got)
		}
	})
}

// TestSetStarfleetProfileWriteRoundTrip verifies that setting a cloud
// profile and saving it replaces the persisted values rather than
// merging with them.
func TestSetStarfleetProfileWriteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `
profiles:
  dev:
    starfleet:
      api_url: https://old.example.com
      client_id: old-id
      client_secret: old-secret
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{
		APIURL:       "https://new.example.com",
		ClientID:     "new-id",
		ClientSecret: "new-secret",
	})
	// No cfg.path assignment here: Load(path) above already set it, and
	// re-setting it would hide a regression in which Load stopped
	// remembering where it read from.
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Assert on the document that is actually on disk, decoded into a
	// generic map rather than back into Config. That is deliberately
	// independent of Load and of Profile's yaml tags: the promise is
	// about the file the user opens in an editor, and a reparse through
	// the same typed structs can only ever tell you the round trip is
	// self-consistent.
	//
	// Matching is structural rather than substring, so a future comment
	// in the file cannot fail the test.
	//
	// The reparse assertions below stayed, but they are the weaker half:
	// they only ever checked client_id. A write that left the previous
	// api_url in place — new credentials, stale endpoint — passes every
	// one of them and is caught here.
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	var doc struct {
		Profiles map[string]map[string]map[string]string `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(onDisk, &doc); err != nil {
		t.Fatalf("parse migrated file as a generic document: %v\n%s",
			err, onDisk)
	}
	sections := doc.Profiles["dev"]
	cloud, ok := sections["starfleet"]
	if !ok {
		t.Fatalf("cloud: key missing from the document:\n%s", onDisk)
	}
	if cloud["client_id"] != "new-id" ||
		cloud["client_secret"] != "new-secret" ||
		cloud["api_url"] != "https://new.example.com" {
		t.Errorf("cloud: section on disk = %v, want the new values",
			cloud)
	}
	for _, stale := range []string{"old-id", "old-secret",
		"https://old.example.com"} {
		if strings.Contains(string(onDisk), stale) {
			t.Errorf("superseded value %q survived in the file:\n%s",
				stale, onDisk)
		}
	}
	// Reload and verify the cloud section is present.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reloaded.Profiles["dev"]
	if !ok {
		t.Fatal("profile not found after reload")
	}
	if p.Starfleet == nil {
		t.Fatal("cloud: section missing after Save")
	}
	if p.Starfleet.ClientID != "new-id" {
		t.Errorf("ClientID = %q, want new-id", p.Starfleet.ClientID)
	}
}

// TestStarfleetProfileAlwaysReturnsACopy pins that mutating the returned
// struct is a no-op. StarfleetProfile used to hand back the live
// c.Profiles[name].Starfleet pointer, so mutate-then-Save persisted by
// accident; SetStarfleetProfile is the only write path.
func TestStarfleetProfileAlwaysReturnsACopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		Profiles: map[string]Profile{
			"dev": {Starfleet: &StarfleetProfile{
				APIURL:       "https://kept.example.com",
				ClientID:     "kept-id",
				ClientSecret: "kept-secret",
			}},
		},
	}
	got := cfg.StarfleetProfile("dev")
	got.APIURL = "https://tampered.example.com"
	got.ClientID = "tampered-id"
	got.ClientSecret = "tampered-secret"

	after := cfg.StarfleetProfile("dev")
	if after.APIURL != "https://kept.example.com" ||
		after.ClientID != "kept-id" ||
		after.ClientSecret != "kept-secret" {
		t.Errorf("mutating the returned struct changed the "+
			"stored profile: %+v", after)
	}
}

// TestStarfleetProfileAbsentProfile verifies that StarfleetProfile returns
// a non-nil empty struct when the profile does not exist.
func TestStarfleetProfileAbsentProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{Profiles: map[string]Profile{}}
	ap := cfg.StarfleetProfile("nonexistent")
	if ap == nil {
		t.Fatal("StarfleetProfile returned nil for absent profile")
	}
	if ap.APIURL != "" || ap.ClientID != "" || ap.ClientSecret != "" {
		t.Errorf("expected empty StarfleetProfile, got %+v", ap)
	}
}

// TestStarfleetProfileNoAccountSection verifies that StarfleetProfile
// returns a non-nil empty struct when a profile exists but carries no
// account section.
func TestStarfleetProfileNoAccountSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{
		Profiles: map[string]Profile{
			"empty": {},
		},
	}
	ap := cfg.StarfleetProfile("empty")
	if ap == nil {
		t.Fatal("StarfleetProfile returned nil for profile with neither section")
	}
	if ap.APIURL != "" || ap.ClientID != "" || ap.ClientSecret != "" {
		t.Errorf("expected empty StarfleetProfile, got %+v", ap)
	}
}

// TestSetStarfleetProfileNilProfilesMap verifies that SetStarfleetProfile
// creates the Profiles map if it does not exist.
func TestSetStarfleetProfileNilProfilesMap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{} // Profiles is nil
	ap := &StarfleetProfile{
		APIURL:       "https://api.example.com",
		ClientID:     "test-id",
		ClientSecret: "test-secret",
	}
	cfg.SetStarfleetProfile("dev", ap)
	if cfg.Profiles == nil {
		t.Fatal("SetStarfleetProfile did not create Profiles map")
	}
	p, ok := cfg.Profiles["dev"]
	if !ok {
		t.Fatal("profile not found after SetStarfleetProfile")
	}
	if p.Starfleet != ap {
		t.Errorf("stored cloud = %+v, want %+v", p.Starfleet, ap)
	}
}

// TestProfileNamesSorted verifies ProfileNames returns configured
// profile names in sorted order regardless of insertion order.
func TestProfileNamesSorted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{}
	cfg.SetStarfleetProfile("prod", &StarfleetProfile{})
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{})
	got := cfg.ProfileNames()
	if len(got) != 2 || got[0] != "dev" || got[1] != "prod" {
		t.Errorf("ProfileNames() = %v, want [dev prod]", got)
	}
}

// TestProfileNamesEmpty verifies ProfileNames returns an empty (not
// nil-panicking) slice when no profiles are configured.
func TestProfileNamesEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{}
	if got := cfg.ProfileNames(); len(got) != 0 {
		t.Errorf("ProfileNames() = %v, want empty", got)
	}
}

// TestValidateProfile is the table test for the extracted check:
// known, unknown, unknown-with-zero-profiles, and an empty name
// against a non-empty config. It also asserts that
// SetCurrentProfile's error for the same input is byte-identical to
// ValidateProfile's — the whole point of extracting one function
// instead of maintaining two copies of the same message.
func TestValidateProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name       string
		profiles   []string
		check      string
		wantErr    bool
		wantSubstr string
	}{
		{
			name:     "known profile",
			profiles: []string{"alpha"},
			check:    "alpha",
			wantErr:  false,
		},
		{
			name:       "unknown profile with configured profiles",
			profiles:   []string{"alpha", "prod"},
			check:      "prd",
			wantErr:    true,
			wantSubstr: "configured profiles: alpha, prod",
		},
		{
			// Both onramps, in one substring, so a message that drops
			// either one fails here. A CP-only operator has no Starfleet
			// credentials, so a zero-profile message naming only
			// `starfleet auth login` points them at a command that cannot
			// create their profile.
			name:     "unknown profile with zero profiles configured",
			profiles: nil,
			check:    "prd",
			wantErr:  true,
			wantSubstr: "no profiles are configured; run " +
				"'pgedge starfleet auth login --profile prd' or " +
				"'pgedge controlplane config set --base-url <url> --profile prd' " +
				"to create one",
		},
		{
			name:       "empty name against a non-empty config",
			profiles:   []string{"alpha"},
			check:      "",
			wantErr:    true,
			wantSubstr: "configured profiles: alpha",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			for _, p := range tc.profiles {
				cfg.SetStarfleetProfile(p, &StarfleetProfile{})
			}
			err := cfg.ValidateProfile(tc.check)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateProfile(%q) = nil, want an error",
					tc.check)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateProfile(%q) = %v, want nil",
					tc.check, err)
			}
			if err != nil && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("ValidateProfile(%q) error = %q, want substring %q",
					tc.check, err.Error(), tc.wantSubstr)
			}

			// Parity: SetCurrentProfile on a fresh copy of the same
			// config must fail with the exact same string for the
			// same input.
			t.Setenv("HOME", t.TempDir())
			cfg2 := &Config{}
			for _, p := range tc.profiles {
				cfg2.SetStarfleetProfile(p, &StarfleetProfile{})
			}
			setErr := cfg2.SetCurrentProfile(tc.check)
			if tc.wantErr {
				if setErr == nil {
					t.Fatalf(
						"SetCurrentProfile(%q) = nil, want an error",
						tc.check)
				}
				if setErr.Error() != err.Error() {
					t.Errorf(
						"SetCurrentProfile error = %q, "+
							"ValidateProfile error = %q, want identical",
						setErr.Error(), err.Error())
				}
			} else if setErr != nil {
				t.Errorf("SetCurrentProfile(%q) = %v, want nil",
					tc.check, setErr)
			}
		})
	}
}

// TestSetCurrentProfileUnknownIsError pins the deliberate design
// choice: switching to a profile that does not exist must fail loudly
// rather than silently write a current_profile that breaks every
// later command's credential resolution. The error must name the
// profiles that do exist, so the operator can immediately correct the
// command instead of guessing.
func TestSetCurrentProfileUnknownIsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{}
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{})
	err := cfg.SetCurrentProfile("nope")
	if err == nil {
		t.Fatal("expected an error for an unknown profile")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("error %v should list the known profiles", err)
	}
	if cfg.CurrentProfile != "" {
		t.Errorf("CurrentProfile = %q, want unchanged on error",
			cfg.CurrentProfile)
	}
}

// TestSetCurrentProfileSwitchesKnownProfile is the positive case:
// naming a configured profile updates CurrentProfile and returns no
// error.
func TestSetCurrentProfileSwitchesKnownProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &Config{}
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{})
	cfg.SetStarfleetProfile("prod", &StarfleetProfile{})
	if err := cfg.SetCurrentProfile("prod"); err != nil {
		t.Fatalf("SetCurrentProfile: %v", err)
	}
	if cfg.CurrentProfile != "prod" {
		t.Errorf("CurrentProfile = %q, want prod", cfg.CurrentProfile)
	}
}

// TestSaveToWritesPermsAndRemembersPath exercises SaveTo directly,
// without ever going through Load: it is the only exported way to
// give a hand-built *Config somewhere to write before its first save.
// It must produce the exact same 0700 dir / 0600 file permissions
// Save does (the config file holds credentials), and it must remember
// path so a later bare Save() call writes back to the same place.
func TestSaveToWritesPermsAndRemembersPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")

	cfg := &Config{}
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{
		APIURL: "https://staging.example",
	})
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perms = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perms = %o, want 700", perm)
	}

	// A second mutation followed by a bare Save (no path argument)
	// must land on the same file SaveTo just created.
	if err := cfg.SetCurrentProfile("dev"); err != nil {
		t.Fatalf("SetCurrentProfile: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save after SaveTo: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.CurrentProfile != "dev" {
		t.Errorf("CurrentProfile after Save = %q, want dev",
			reloaded.CurrentProfile)
	}
}

// TestSaveToThenSetCurrentProfileRoundTrip is the end-to-end path
// `pgedge profile use` relies on: build a config with two profiles,
// persist it with SaveTo (there is no other exported setter for an
// unloaded Config's path), reload it fresh, switch the current
// profile, save, reload again, and confirm ResolveProfile now answers
// with the switched profile — proving the write actually reached
// disk rather than only mutating the in-memory struct.
func TestSaveToThenSetCurrentProfileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")

	cfg := &Config{}
	cfg.SetStarfleetProfile("dev", &StarfleetProfile{
		APIURL: "https://staging.example",
	})
	cfg.SetStarfleetProfile("prod", &StarfleetProfile{
		APIURL: "https://api.pgedge.com",
	})
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.SetCurrentProfile("prod"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}

	final, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := final.ResolveProfile(""); got != "prod" {
		t.Errorf("ResolveProfile() = %q, want prod", got)
	}
}

// An unreadable --config named its operation and path twice, because
// the wrapper added both and the wrapped *os.PathError already
// carried them:
//
//	config: read /tmp: read /tmp: is a directory
//
// The ENOTDIR variant was worse: "not a directory" describes a parent
// the doubled message never names, so the repetition drew the eye to
// the wrong part of a message that was already confusing.
func TestLoadDoesNotDoubleTheOperationAndPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	_, err := Load(dir) // a directory is readable-as-a-name, not as a file
	if err == nil {
		t.Fatal("loading a directory as a config file succeeded")
	}
	msg := err.Error()

	if n := strings.Count(msg, dir); n != 1 {
		t.Errorf("path appears %d times, want 1: %s", n, msg)
	}
	if n := strings.Count(msg, "read "); n != 1 {
		t.Errorf("operation appears %d times, want 1: %s", n, msg)
	}
	// The prefix still identifies which subsystem failed.
	if !strings.HasPrefix(msg, "config: ") {
		t.Errorf("message lost its config: prefix: %s", msg)
	}
}

// The control: an error that does NOT carry its own path must still
// be given one, or the fix trades a doubled path for a missing one.
func TestConfigErrorKeepsThePathWhenTheErrorHasNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := pathErrorf("read", "/some/where", errors.New("bare failure"))
	for _, want := range []string{"read", "/some/where", "bare failure"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("message %q dropped %q", got.Error(), want)
		}
	}
}

func TestDefaultPathIsUnderCLISubdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".pgedge", "cli", "config.yaml")
	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultCacheDirIsBesideConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".pgedge", "cli", "cache")
	if got != want {
		t.Fatalf("DefaultCacheDir = %q, want %q", got, want)
	}
}

// The config file is the only copy of every profile and credential, so
// it is replaced by rename rather than truncated in place. Two
// observable consequences: no staging residue after a write, and a
// write into a read-only directory is refused even when the file itself
// is writable, the documented behaviour change. (Where the staging
// file is created is pinned in internal/atomicfile, not here.)
func TestSaveIsAtomic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("leaves no staging file behind", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		cfg := &Config{CurrentProfile: "default"}
		if err := cfg.SaveTo(p); err != nil {
			t.Fatalf("save: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "config.yaml" {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("config dir holds %v, want only config.yaml", names)
		}
	})

	t.Run("refuses a read-only directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		t.Setenv("TMPDIR", t.TempDir())
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		before := "current_profile: keep\n"
		if err := os.WriteFile(p, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		cfg := &Config{CurrentProfile: "changed"}
		err := cfg.SaveTo(p)
		if err == nil {
			t.Fatal("save into a read-only directory succeeded")
		}
		// The refusal names the config file the user knows, not the
		// staging file the write was attempting.
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not name %s", err, p)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != before {
			t.Errorf("a refused save changed the file: %q", got)
		}
	})
}
