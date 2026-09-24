package conn

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// envRuntime is a "dev" profile holding its own full credential and
// api_url, with the env pair set on top of it.
func envRuntime(t *testing.T) *module.Runtime {
	t.Helper()
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(auth.EnvClientID, "eid")
	t.Setenv(auth.EnvClientSecret, "es")
	cfg := &config.Config{}
	cfg.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL: "https://profile.example", ClientID: "cid",
		ClientSecret: "cs"})
	return &module.Runtime{Config: cfg, Profile: "dev",
		Output: &output.Renderer{Format: "table"}, Stderr: io.Discard}
}

func TestResolveCredentialsEnvIsASeparateProfile(t *testing.T) {
	tests := []struct{ flagURL, wantURL string }{
		{"", DefaultAPIURL},
		{"https://flag.example", "https://flag.example"},
	}
	for _, tt := range tests {
		rt := envRuntime(t)
		res, err := ResolveCredentials(rt, "", "", tt.flagURL)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Env || res.Source != auth.SourceEnv ||
			res.Creds.ClientID != "eid" || res.APIURL != tt.wantURL {
			t.Errorf("flag %q: got %+v (creds %+v), want env creds at %s",
				tt.flagURL, res, res.Creds, tt.wantURL)
		}
	}
}

func TestResolveCredentialsProfileKeepsItsURL(t *testing.T) {
	rt := envRuntime(t)
	t.Setenv(auth.EnvClientID, "")
	t.Setenv(auth.EnvClientSecret, "")
	res, err := ResolveCredentials(rt, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Env || res.APIURL != "https://profile.example" {
		t.Errorf("got %+v, want the profile's own URL", res)
	}
}

func TestEnvWithExplicitProfileIsAUsageError(t *testing.T) {
	rt := envRuntime(t)
	rt.ProfileExplicit = true

	_, err := ResolveCredentials(rt, "", "", "")
	var conflict *ProfileConflictError
	if !errors.As(err, &conflict) || !IsUsageError(err) {
		t.Fatalf("err = %v, want a usage-class ProfileConflictError", err)
	}

	_, err = Resolve(rt, "", "", "", time.Second)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("Resolve err = %v, want exit %d", err, ExitUsage)
	}

	// Flags answer before env is consulted, so there is no conflict.
	if _, err := ResolveCredentials(rt, "fid", "fs", ""); err != nil {
		t.Errorf("flags with --profile and env set: %v", err)
	}
}

func TestIsUsageError(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{&auth.PartialFlagPairError{Given: "a", Missing: "b"}, true},
		{&ProfileConflictError{Profile: "dev"}, true},
		{auth.ErrNoCredentials, false},
		{errors.New("keychain"), false},
	} {
		if got := IsUsageError(tt.err); got != tt.want {
			t.Errorf("IsUsageError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// TestEnvCredentialsNeverTouchTheTokenCache: a bound, unexpired cache
// for the very same connection exists, and env mode still exchanges
// and still leaves the file exactly as it was.
func TestEnvCredentialsNeverTouchTheTokenCache(t *testing.T) {
	rt := envRuntime(t)
	var exchanges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testsupport.TokenPath {
				exchanges.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testsupport.TokenBody)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
	t.Cleanup(srv.Close)

	store := Store(rt)
	if err := store.SaveToken(&auth.CachedToken{
		AccessToken: "cached",
		ExpiresAt:   time.Now().Add(time.Hour),
		Fingerprint: auth.Fingerprint(srv.URL, "eid", "es"),
	}); err != nil {
		t.Fatal(err)
	}
	current, _ := cachePaths(t)
	before, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}

	c, err := Resolve(rt, "", "", srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token == "cached" || exchanges.Load() != 1 {
		t.Errorf("token %q after %d exchanges: env mode read the cache",
			c.Token, exchanges.Load())
	}
	after, err := os.ReadFile(current)
	if err != nil || string(after) != string(before) {
		t.Errorf("cache file changed (err %v):\nbefore %s\nafter  %s",
			err, before, after)
	}
}

func TestEnvCredentialsWriteNoCacheFile(t *testing.T) {
	rt := envRuntime(t)
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, "{}"))
	if _, err := Resolve(rt, "", "", url, time.Second); err != nil {
		t.Fatal(err)
	}
	current, _ := cachePaths(t)
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Errorf("env mode wrote %s (stat err %v)", current, err)
	}
}
