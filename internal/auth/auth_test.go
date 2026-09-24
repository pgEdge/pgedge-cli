package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

func TestResolveCredentials(t *testing.T) {
	tests := []struct {
		name       string
		flagID     string
		flagSecret string
		profile    *config.StarfleetProfile
		wantID     string
		wantSource string
		wantErr    bool
	}{
		{name: "flags win over config",
			flagID: "fid", flagSecret: "fs",
			profile: &config.StarfleetProfile{
				ClientID: "cid", ClientSecret: "cs"},
			wantID: "fid", wantSource: "flags"},
		{name: "config fallback",
			profile: &config.StarfleetProfile{
				ClientID: "cid", ClientSecret: "cs"},
			wantID: "cid", wantSource: "config"},
		{name: "nothing anywhere errors",
			profile: &config.StarfleetProfile{}, wantErr: true},
		// A half-supplied flag pair must not fall through, even when
		// config holds a complete pair that would have answered. The
		// whole point is that the operator typed something the CLI
		// cannot honour.
		{name: "flag id without flag secret errors",
			flagID: "fid",
			profile: &config.StarfleetProfile{
				ClientID: "cid", ClientSecret: "cs"},
			wantErr: true},
		{name: "flag secret without flag id errors",
			flagSecret: "fs",
			profile: &config.StarfleetProfile{
				ClientID: "cid", ClientSecret: "cs"},
			wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			clearCredentialEnv(t)
			a := &Auth{Profile: "default", Module: "account"}
			creds, source, err := a.ResolveCredentials(
				tt.profile, tt.flagID, tt.flagSecret)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if creds.ClientID != tt.wantID {
				t.Errorf("id = %q, want %q",
					creds.ClientID, tt.wantID)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q",
					source, tt.wantSource)
			}
		})
	}
}

// TestResolveCredentialsHalfFlagPairNamesTheMissingFlag checks the
// message, not just the error: "no credentials found" would be a lie
// here — a complete pair is sitting in config, and the resolver is
// refusing it on purpose. The operator needs to be told which flag is
// missing, since the mistake is almost always a shell quoting accident
// that swallowed one value.
func TestResolveCredentialsHalfFlagPairNamesTheMissingFlag(t *testing.T) {
	cases := []struct {
		name       string
		flagID     string
		flagSecret string
		wantSubstr string
	}{
		{"id without secret", "fid", "", "--client-secret"},
		{"secret without id", "", "fs", "--client-id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			a := &Auth{Profile: "default", Module: "account"}
			_, _, err := a.ResolveCredentials(
				&config.StarfleetProfile{
					ClientID: "cid", ClientSecret: "cs"},
				tc.flagID, tc.flagSecret)
			if err == nil {
				t.Fatal("want an error for a half-supplied flag pair")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not name %q",
					err, tc.wantSubstr)
			}
			if strings.Contains(err.Error(), "no credentials found") {
				t.Errorf("error %q claims nothing was found, but a "+
					"complete pair was available in config", err)
			}
		})
	}
}

// TestFlagCredentials covers the flag-only half of resolution on its
// own, because `starfleet auth login` needs exactly that half and must
// not have the rest: falling through to config would skip the
// prompt for anyone who already has stored credentials and silently
// re-save them, which is the opposite of what someone signing in as a
// different client asked for.
//
// The three-way return is the contract. Neither flag set is (nil, nil)
// — "nothing supplied, ask the caller to decide", not an error — and it
// is the case a caller is most likely to get wrong by testing only the
// error.
func TestFlagCredentials(t *testing.T) {
	cases := []struct {
		name       string
		flagID     string
		flagSecret string
		wantCreds  *Credentials
		wantErr    bool
		wantSubstr string
	}{
		{
			name: "both supplied", flagID: "fid", flagSecret: "fs",
			wantCreds: &Credentials{"fid", "fs"},
		},
		{
			name: "neither supplied",
			// nil creds AND nil error: the caller prompts.
		},
		{
			name: "id without secret", flagID: "fid",
			wantErr: true, wantSubstr: "--client-secret",
		},
		{
			name: "secret without id", flagSecret: "fs",
			wantErr: true, wantSubstr: "--client-id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds, err := FlagCredentials(tc.flagID, tc.flagSecret)

			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error for a half-supplied pair")
				}
				var partial *PartialFlagPairError
				if !errors.As(err, &partial) {
					t.Errorf("err = %T, want *PartialFlagPairError", err)
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error %q does not name %q",
						err, tc.wantSubstr)
				}
				if creds != nil {
					t.Errorf("creds = %+v, want nil alongside an error",
						creds)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch {
			case tc.wantCreds == nil && creds != nil:
				t.Errorf("creds = %+v, want nil so the caller prompts",
					creds)
			case tc.wantCreds != nil && creds == nil:
				t.Fatal("creds = nil, want the supplied flag pair")
			case tc.wantCreds != nil && *creds != *tc.wantCreds:
				t.Errorf("creds = %+v, want %+v", creds, tc.wantCreds)
			}
		})
	}
}

// TestResolveCredentialsSharesTheFlagRule guards the extraction: the
// half-pair rule has to live in exactly one place, or `login` and every
// resource command can drift into disagreeing about the same input.
// Asserting both call sites produce the same error type is what makes
// the shared helper essential rather than incidental.
func TestResolveCredentialsSharesTheFlagRule(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &Auth{Profile: "default", Module: "account"}

	_, _, resolveErr := a.ResolveCredentials(nil, "fid", "")
	_, flagErr := FlagCredentials("fid", "")

	var fromResolve, fromFlags *PartialFlagPairError
	if !errors.As(resolveErr, &fromResolve) {
		t.Fatalf("ResolveCredentials err = %v, want *PartialFlagPairError",
			resolveErr)
	}
	if !errors.As(flagErr, &fromFlags) {
		t.Fatalf("FlagCredentials err = %v, want *PartialFlagPairError",
			flagErr)
	}
	if *fromResolve != *fromFlags {
		t.Errorf("half-pair error differs by call site: "+
			"ResolveCredentials %+v, FlagCredentials %+v",
			fromResolve, fromFlags)
	}
}

// testToken is the token every cache-writing test below saves; the
// value is irrelevant, only whether SaveToken can place the file. It
// carries a Fingerprint because SaveToken now refuses an unbound
// token (see TestSaveTokenRejectsEmptyFingerprint) — the tests in this
// file that exercise unrelated failure paths (mkdir, write, $HOME)
// need a token that would otherwise be accepted.
func testToken() *CachedToken {
	return &CachedToken{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(time.Hour),
		Fingerprint: Fingerprint("https://api.example.test",
			"fake-client-id", "fake-client-secret"),
	}
}

func TestCachePathHomeErrorPropagates(t *testing.T) {
	t.Setenv("HOME", "")
	// CacheDir empty forces cachePath to resolve $HOME, which fails.
	a := &Auth{Profile: "default", Module: "byoc"}
	if err := a.SaveToken(testToken()); err == nil {
		t.Fatal("want error when $HOME cannot be resolved")
	}
}

func TestSaveTokenMkdirAllError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A regular file in place of the cache directory's parent makes
	// MkdirAll fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &Auth{Profile: "default", Module: "byoc",
		CacheDir: filepath.Join(blocker, "cache")}
	if err := a.SaveToken(testToken()); err == nil {
		t.Fatal("want error creating cache dir under a file")
	}
}

func TestSaveTokenWriteFileError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Pre-create a directory at the cache file's path so WriteFile
	// fails with "is a directory".
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "default-byoc.json")
	if err := os.MkdirAll(cachePath, 0o700); err != nil {
		t.Fatal(err)
	}

	a := &Auth{Profile: "default", Module: "byoc", CacheDir: cacheDir}
	if err := a.SaveToken(testToken()); err == nil {
		t.Fatal("want error writing cache over a directory")
	}
}

func TestClearToken(t *testing.T) {
	t.Run("removes an existing cache file", func(t *testing.T) {
		cacheDir := t.TempDir()
		p := filepath.Join(cacheDir, "default-byoc.json")
		if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		a := &Auth{Profile: "default", Module: "byoc",
			CacheDir: cacheDir}
		if err := a.ClearToken(); err != nil {
			t.Fatalf("ClearToken: %v", err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("cache file still present after ClearToken")
		}
	})

	t.Run("no-ops when the cache file is absent", func(t *testing.T) {
		a := &Auth{Profile: "default", Module: "byoc",
			CacheDir: t.TempDir()}
		if err := a.ClearToken(); err != nil {
			t.Errorf("ClearToken on absent file: %v", err)
		}
	})

	// logout's promise is that no on-disk copy of the token survives
	// it. atomicfile.Write cleans its staging file up with a deferred
	// Remove, which a signal does not run — so a process killed
	// between the write and the rename leaves a 0600 file holding a
	// live bearer token under a name nothing else enumerates. Before
	// the atomic write there was no such file and the promise was
	// unconditional; this keeps it that way.
	t.Run("removes staging files an interrupted write left behind",
		func(t *testing.T) {
			cacheDir := t.TempDir()
			a := &Auth{Profile: "default", Module: "starfleet",
				CacheDir: cacheDir}
			if err := a.SaveToken(testToken()); err != nil {
				t.Fatal(err)
			}
			// Exactly the shape atomicfile.Write leaves: os.CreateTemp
			// appends random digits to the prefix.
			orphan := filepath.Join(cacheDir,
				".default-starfleet.json.tmp3141592653")
			if err := os.WriteFile(
				orphan, []byte(`{"access_token":"live-tok"}`), 0o600,
			); err != nil {
				t.Fatal(err)
			}

			if err := a.ClearToken(); err != nil {
				t.Fatalf("ClearToken: %v", err)
			}
			if _, err := os.Stat(orphan); !os.IsNotExist(err) {
				t.Errorf("a staging file holding a live token survived " +
					"logout")
			}
			left, err := os.ReadDir(cacheDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 0 {
				var names []string
				for _, e := range left {
					names = append(names, e.Name())
				}
				t.Errorf("cache dir still holds %v after logout", names)
			}
		})

	// The sweep must not reach past the profile+module it was asked
	// to clear. A shared cache directory holds every profile's token,
	// and logout is scoped to one.
	t.Run("leaves another profile's files alone", func(t *testing.T) {
		cacheDir := t.TempDir()
		other := filepath.Join(cacheDir, "other-starfleet.json")
		otherStaging := filepath.Join(cacheDir,
			".other-starfleet.json.tmp271828")
		for _, p := range []string{other, otherStaging} {
			if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		a := &Auth{Profile: "default", Module: "starfleet",
			CacheDir: cacheDir}
		if err := a.SaveToken(testToken()); err != nil {
			t.Fatal(err)
		}
		if err := a.ClearToken(); err != nil {
			t.Fatalf("ClearToken: %v", err)
		}

		for _, p := range []string{other, otherStaging} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("logout removed another profile's %s: %v",
					filepath.Base(p), err)
			}
		}
	})

	// Profile names are user-controlled and validatePathSegment
	// rejects only path separators, so a name full of glob
	// metacharacters is reachable. It must be matched literally: with
	// filepath.Glob, `*` here would expand to every other profile's
	// staging file and logout would delete tokens it was never asked
	// to touch.
	t.Run("treats a glob-shaped profile name literally",
		func(t *testing.T) {
			cacheDir := t.TempDir()
			victim := filepath.Join(cacheDir, ".victim-starfleet.json.tmp99")
			if err := os.WriteFile(
				victim, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}

			a := &Auth{Profile: "*", Module: "starfleet", CacheDir: cacheDir}
			if err := a.ClearToken(); err != nil {
				t.Fatalf("ClearToken: %v", err)
			}
			if _, err := os.Stat(victim); err != nil {
				t.Errorf("a profile named %q swept another profile's "+
					"staging file: %v", "*", err)
			}
		})

	// The sweep must not fail open. A cache directory that can be
	// written but not read (0300) lets the primary Remove succeed
	// while ReadDir fails EACCES, so a swallowed error there would
	// return success with a live token still staged — the very hole
	// the sweep closes, one door further in. No state the CLI itself
	// produces reaches this mode; an operator who set it by hand is
	// owed an error rather than a silent no-op.
	t.Run("reports an unreadable cache dir rather than skipping",
		func(t *testing.T) {
			cacheDir := t.TempDir()
			a := &Auth{Profile: "default", Module: "starfleet",
				CacheDir: cacheDir}
			if err := a.SaveToken(testToken()); err != nil {
				t.Fatal(err)
			}
			orphan := filepath.Join(cacheDir,
				".default-starfleet.json.tmp42")
			if err := os.WriteFile(
				orphan, []byte(`{"access_token":"live-tok"}`), 0o600,
			); err != nil {
				t.Fatal(err)
			}

			// Write+execute, no read: Remove works, ReadDir does not.
			if err := os.Chmod(cacheDir, 0o300); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(cacheDir, 0o700) })

			err := a.ClearToken()
			if err == nil {
				t.Fatal("ClearToken returned success on an unreadable " +
					"cache dir; a staged token may have survived silently")
			}
			if !strings.Contains(err.Error(), "scan cache dir") {
				t.Errorf("err = %v, want it to name the failed scan", err)
			}
		})

	t.Run("propagates a cachePath resolution error", func(t *testing.T) {
		t.Setenv("HOME", "")
		a := &Auth{Profile: "default", Module: "byoc"}
		if err := a.ClearToken(); err == nil {
			t.Fatal("want error when $HOME cannot be resolved")
		}
	})

	t.Run("wraps a non-ENOENT removal error", func(t *testing.T) {
		cacheDir := t.TempDir()
		p := filepath.Join(cacheDir, "default-byoc.json")
		// A non-empty directory at the cache path makes os.Remove
		// fail with something other than "not exist".
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(p, "child"), []byte("x"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		a := &Auth{Profile: "default", Module: "byoc",
			CacheDir: cacheDir}
		if err := a.ClearToken(); err == nil {
			t.Fatal("want error removing a non-empty directory")
		}
	})
}

func TestCachePathRejectsTraversal(t *testing.T) {
	// Profile and Module are user-controllable (flags, config)
	// and are embedded directly into the cache file name; reject
	// anything that could escape the cache directory.
	tests := []struct {
		name    string
		profile string
		module  string
	}{
		{"profile path separator", "../../etc/passwd", "byoc"},
		{"profile parent dir", "..", "byoc"},
		{"profile current dir", ".", "byoc"},
		{"profile empty", "", "byoc"},
		{"module path separator", "default", "../../etc"},
		{"module empty", "default", ""},
		{"backslash separator", `..\..\secrets`, "byoc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Auth{Profile: tt.profile, Module: tt.module,
				CacheDir: t.TempDir()}
			if _, err := a.cachePath(); err == nil {
				t.Errorf("cachePath(profile=%q, module=%q) = nil error, want error",
					tt.profile, tt.module)
			}
		})
	}
}

func TestCachePathDefaultsUnderCLISubdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := &Auth{Profile: "p", Module: "starfleet"}
	got, err := a.cachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".pgedge", "cli", "cache", "p-starfleet.json")
	if got != want {
		t.Fatalf("cachePath = %q, want %q", got, want)
	}
}

func TestCachePathAcceptsValidNames(t *testing.T) {
	a := &Auth{Profile: "staging-2", Module: "byoc",
		CacheDir: t.TempDir()}
	p, err := a.cachePath()
	if err != nil {
		t.Fatalf("cachePath: %v", err)
	}
	if filepath.Base(p) != "staging-2-byoc.json" {
		t.Errorf("cachePath = %q, want basename staging-2-byoc.json", p)
	}
}

// The cache file name is derived from Module, so an "account" Auth
// must read and write <profile>-account.json. The file holds a live
// bearer token, so its 0600 mode is asserted here too: this is the only
// regression test for the token's permissions at rest.
func TestCachePathUsesModuleName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	a := &Auth{Profile: "dev", Module: "account", CacheDir: dir}
	tok := testToken()
	if err := a.SaveToken(tok); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "dev-account.json")
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("expected cache at %s: %v", want, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file perms = %o, want 600", perm)
	}
	got, err := a.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "tok" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
}

// SaveToken creates the cache directory itself when it is absent, so
// the 0700 mode on that directory is its responsibility to get right.
// TestCachePathUsesModuleName above uses a t.TempDir() that already
// exists (0700 by luck of the umask), which would not catch a
// regression in the MkdirAll mode; this one names a directory SaveToken
// has to create.
func TestSaveTokenCreatesCacheDir0700(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "cache")
	a := &Auth{Profile: "dev", Module: "account", CacheDir: dir}
	if err := a.SaveToken(testToken()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected cache dir at %s: %v", dir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("cache dir perms = %o, want 700", perm)
	}
}

// LoadToken is what `auth status` and `doctor` read to decide whether a
// token is present, so every way it can fail must return an error
// rather than a zero-valued token that would read as "expired".
func TestLoadTokenFailures(t *testing.T) {
	t.Run("unresolvable cache path", func(t *testing.T) {
		t.Setenv("HOME", "")
		a := &Auth{Profile: "default", Module: "account"}
		if _, err := a.LoadToken(); err == nil {
			t.Fatal("want error when $HOME cannot be resolved")
		}
	})

	t.Run("absent cache file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		a := &Auth{Profile: "default", Module: "account",
			CacheDir: t.TempDir()}
		if _, err := a.LoadToken(); err == nil {
			t.Fatal("want error for a missing cache file")
		}
	})

	t.Run("unparseable cache file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		dir := t.TempDir()
		p := filepath.Join(dir, "default-account.json")
		if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		a := &Auth{Profile: "default", Module: "account", CacheDir: dir}
		if _, err := a.LoadToken(); err == nil {
			t.Fatal("want error for a corrupt cache file")
		}
	})
}

// TestFingerprint covers D1: the digest is stable, varies with any one
// input on its own, never leaks an input as a substring, and the
// domain-separating NUL between every field actually does its job.
func TestFingerprint(t *testing.T) {
	const url = "https://api.example.test"

	t.Run("stable across calls", func(t *testing.T) {
		a := Fingerprint(url, "client-a", "secret-a")
		b := Fingerprint(url, "client-a", "secret-a")
		if a != b {
			t.Errorf("Fingerprint not stable: %q vs %q", a, b)
		}
	})

	t.Run("differs when only the id changes", func(t *testing.T) {
		a := Fingerprint(url, "client-a", "secret-x")
		b := Fingerprint(url, "client-b", "secret-x")
		if a == b {
			t.Errorf("Fingerprint identical despite different client id: %q", a)
		}
	})

	t.Run("differs when only the secret changes", func(t *testing.T) {
		a := Fingerprint(url, "client-x", "secret-a")
		b := Fingerprint(url, "client-x", "secret-b")
		if a == b {
			t.Errorf("Fingerprint identical despite different secret: %q", a)
		}
	})

	// Issue #146: the digest must move with the endpoint, or a token
	// minted by one host stays acceptable when another is dialled.
	t.Run("differs when only the api url changes", func(t *testing.T) {
		a := Fingerprint("https://api.example.test", "client-x", "secret-x")
		b := Fingerprint("https://other.example.test", "client-x", "secret-x")
		if a == b {
			t.Errorf("Fingerprint identical despite different api url: %q", a)
		}
	})

	t.Run("non-empty", func(t *testing.T) {
		if Fingerprint(url, "client-a", "secret-a") == "" {
			t.Error("Fingerprint returned empty string")
		}
	})

	t.Run("does not leak any input as a substring", func(t *testing.T) {
		const id, secret = "fake-client-id-abc", "fake-client-secret-xyz"
		fp := Fingerprint(url, id, secret)
		if strings.Contains(fp, id) {
			t.Errorf("Fingerprint %q contains the client id", fp)
		}
		if strings.Contains(fp, secret) {
			t.Errorf("Fingerprint %q contains the client secret", fp)
		}
		if strings.Contains(fp, url) {
			t.Errorf("Fingerprint %q contains the api url", fp)
		}
	})

	// Proves the domain separator sits between EVERY field, not just
	// at the front: without one, each of these pairs would hash the
	// same concatenation.
	t.Run("separator prevents concatenation collisions", func(t *testing.T) {
		cases := []struct {
			name           string
			aURL, aID, aSe string
			bURL, bID, bSe string
		}{
			{"between id and secret",
				url, "a", "bc", url, "ab", "c"},
			{"between url and id",
				"https://a", "b", "c", "https://ab", "", "c"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				a := Fingerprint(tc.aURL, tc.aID, tc.aSe)
				b := Fingerprint(tc.bURL, tc.bID, tc.bSe)
				if a == b {
					t.Errorf("Fingerprint(%q,%q,%q) == "+
						"Fingerprint(%q,%q,%q) = %q, want different "+
						"digests (missing separator?)",
						tc.aURL, tc.aID, tc.aSe,
						tc.bURL, tc.bID, tc.bSe, a)
				}
			})
		}
	})
}

// TestSaveTokenRejectsEmptyFingerprint guards D2's "unforgeable
// invariant": there must be no path that writes a cache file without
// a Fingerprint, so the guard lives in SaveToken itself, the one
// writer.
func TestSaveTokenRejectsEmptyFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	a := &Auth{Profile: "default", Module: "account", CacheDir: dir}
	tok := &CachedToken{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(time.Hour),
		// Fingerprint deliberately left empty.
	}
	if err := a.SaveToken(tok); err == nil {
		t.Fatal("want error saving a token with an empty Fingerprint")
	}
	p := filepath.Join(dir, "default-account.json")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("SaveToken wrote a file despite rejecting the token: %v", err)
	}
}

// TestSaveLoadRoundTripPreservesFingerprint is the bound-write
// round-trip: a saved Fingerprint must survive save→load, and the
// file mode assertion here is a second guard (alongside
// TestCachePathUsesModuleName) that a bound write does not regress
// the 0600 mode.
func TestSaveLoadRoundTripPreservesFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	a := &Auth{Profile: "dev", Module: "account", CacheDir: dir}
	want := Fingerprint("https://api.example.test",
		"fake-client-id", "fake-client-secret")
	tok := &CachedToken{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(time.Hour),
		Fingerprint: want,
	}
	if err := a.SaveToken(tok); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "dev-account.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("expected cache at %s: %v", p, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file perms = %o, want 600", perm)
	}
	got, err := a.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != want {
		t.Errorf("Fingerprint round-trip = %q, want %q", got.Fingerprint, want)
	}
}

// TestMintedBy covers D3's accept rule in isolation from conn: true
// only for the exact triple that minted the fingerprint, false for a
// rotated secret, a different id, a different API URL, or — the legacy
// case — an empty Fingerprint, which must never match anything.
func TestMintedBy(t *testing.T) {
	const url, other = "https://api.example.test", "https://evil.example.test"
	tok := &CachedToken{
		Fingerprint: Fingerprint(url, "client-a", "secret-a"),
	}
	if !tok.MintedBy(url, "client-a", "secret-a") {
		t.Error("MintedBy false for the exact minting triple")
	}
	if tok.MintedBy(url, "client-a", "secret-rotated") {
		t.Error("MintedBy true despite a rotated secret")
	}
	if tok.MintedBy(url, "client-b", "secret-a") {
		t.Error("MintedBy true despite a different client id")
	}
	// Issue #146: identical credentials, different host. Accepting
	// this is what let a live bearer token be replayed to a host that
	// never minted it.
	if tok.MintedBy(other, "client-a", "secret-a") {
		t.Error("MintedBy true despite a different API URL")
	}

	legacy := &CachedToken{} // Fingerprint left empty
	if legacy.MintedBy(url, "client-a", "secret-a") {
		t.Error("MintedBy true for a legacy token with no Fingerprint")
	}
}

// TestSaveTokenIsAtomic covers D5. os.WriteFile truncates in place, so
// a reader arriving mid-write saw an empty or half-written file and a
// crash mid-write left one behind permanently. A temp file plus rename
// makes the replacement all-or-nothing.
//
// Two of the three subtests fail against os.WriteFile and so are
// proofs of the property, not merely guards on it: the concurrent
// reader (whose O_TRUNC window makes LoadToken return a JSON error)
// and the failed save (where an in-place truncate succeeds in an
// unwritable DIRECTORY, because an existing 0600 file opens for
// writing without it, and destroys the cache it was replacing).
//
// The residue check is the one pure guard. It passes against
// os.WriteFile, which trivially leaves no temp file, and exists to
// catch a future atomicfile.Write that forgets to clean up after
// itself.
func TestSaveTokenIsAtomic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("a concurrent reader never sees a partial file",
		func(t *testing.T) {
			dir := t.TempDir()
			a := &Auth{Profile: "dev", Module: "starfleet", CacheDir: dir}
			if err := a.SaveToken(testToken()); err != nil {
				t.Fatal(err)
			}

			var (
				wg        sync.WaitGroup
				torn      atomic.Int64
				reads     atomic.Int64
				writeFail atomic.Int64
				done      = make(chan struct{})
			)

			wg.Go(func() {
				defer close(done)
				for i := range 300 {
					tok := testToken()
					tok.AccessToken = fmt.Sprintf("tok-%d", i)
					if err := a.SaveToken(tok); err != nil {
						writeFail.Add(1)
						return
					}
				}
			})

			wg.Go(func() {
				for {
					select {
					case <-done:
						return
					default:
					}
					// The file exists before the loop starts and is
					// only ever replaced, so every read must find a
					// complete token. An error, or a token with no
					// access_token, means a reader caught the file
					// mid-write.
					got, err := a.LoadToken()
					reads.Add(1)
					if err != nil || got.AccessToken == "" {
						torn.Add(1)
						return
					}
				}
			})

			wg.Wait()
			if writeFail.Load() != 0 {
				t.Fatalf("%d writes failed; the reader assertion below "+
					"proves nothing if the writer stopped early",
					writeFail.Load())
			}
			// The harness must prove it ran: on a starved runner the
			// writer could finish all 300 saves before the reader is
			// ever scheduled, and "no torn reads" out of no reads at
			// all is a vacuous pass.
			if reads.Load() == 0 {
				t.Fatal("the reader never ran; this subtest proved nothing")
			}
			if n := torn.Load(); n != 0 {
				t.Errorf("a concurrent reader observed %d torn or empty "+
					"cache files; the replacement is not atomic", n)
			}
		})

	t.Run("leaves no temp file behind on success", func(t *testing.T) {
		dir := t.TempDir()
		a := &Auth{Profile: "dev", Module: "starfleet", CacheDir: dir}
		if err := a.SaveToken(testToken()); err != nil {
			t.Fatal(err)
		}
		// Two saves, because the interesting residue is the one a
		// replacement leaves.
		if err := a.SaveToken(testToken()); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) != 1 || names[0] != "dev-starfleet.json" {
			t.Errorf("cache dir holds %v, want exactly "+
				"[dev-starfleet.json] — temp residue is not cleaned up",
				names)
		}
	})

	t.Run("a failed save does not destroy the existing cache",
		func(t *testing.T) {
			dir := t.TempDir()
			a := &Auth{Profile: "dev", Module: "starfleet", CacheDir: dir}
			if err := a.SaveToken(testToken()); err != nil {
				t.Fatal(err)
			}

			// Make the cache directory unwritable so the replacement
			// cannot be staged. The existing 0600 file is still
			// writable in its own right, which is exactly how an
			// in-place truncate would have destroyed it here.
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			replacement := testToken()
			replacement.AccessToken = "replacement-tok"
			if err := a.SaveToken(replacement); err == nil {
				t.Fatal("want an error saving into an unwritable cache dir")
			}

			got, err := a.LoadToken()
			if err != nil {
				t.Fatalf("previous cache destroyed by a failed save: %v", err)
			}
			if got.AccessToken != "tok" {
				t.Errorf("AccessToken = %q, want the previous token %q "+
					"left intact", got.AccessToken, "tok")
			}
		})
}
