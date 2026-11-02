package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestDeviceLoginSavesTheBrowserSession checks that --device, and the
// switch to a code when no browser opens, store exactly what a browser
// sign-in stores, so status, renewal and logout cannot tell them apart.
func TestDeviceLoginSavesTheBrowserSession(t *testing.T) {
	type stored struct {
		profile config.StarfleetProfile
		refresh string
	}
	signIn := func(t *testing.T, openErr error, args ...string) (stored,
		string) {
		t.Helper()
		s := newSignInStubs(t, "t-a")
		s.issueRefresh = true
		s.openErr = openErr
		rt, _, errb := testsupport.NewRuntime(t, "", "text")
		kc := &keychain.Fake{}
		rt.Keychain = kc
		if err := login(t, rt, s, args...); err != nil {
			t.Fatalf("login %v: %v\n%s", args, err, errb.String())
		}
		cache := filepath.Join(os.Getenv("HOME"), ".pgedge", "cli",
			"cache", "default-starfleet.json")
		if _, err := os.Stat(cache); err != nil {
			t.Errorf("no cached access token: %v", err)
		}
		refresh, _ := kc.Get(conn.Store(rt).SessionAccount())
		p := *rt.Config.StarfleetProfile("default")
		p.APIURL = "" // each stub API has its own port
		return stored{p, refresh}, errb.String()
	}

	want, _ := signIn(t, nil)
	if want.profile.Auth != config.AuthBrowser || want.refresh == "" {
		t.Fatalf("browser sign-in stored %+v", want)
	}
	for _, tc := range []struct {
		name    string
		openErr error
		args    []string
		stderr  string
	}{
		{name: "--device", args: []string{"--device"},
			stderr: "WXYZ-1234"},
		{name: "no browser opens",
			openErr: errors.New("exec: xdg-open: not found"),
			stderr:  "signing in with a code instead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, errb := signIn(t, tc.openErr, tc.args...)
			if got != want {
				t.Errorf("stored %+v, want the browser's %+v", got, want)
			}
			for _, s := range []string{tc.stderr, "WXYZ-1234",
				"Signed in as user@example.com."} {
				if !strings.Contains(errb, s) {
					t.Errorf("stderr lacks %q:\n%s", s, errb)
				}
			}
		})
	}
}

func TestDeviceLoginRefusesAPIClientFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--device", "--api-client"},
		{"--device", "--client-id", "i", "--client-secret", "s"},
		{"--device", "--insecure-storage"},
	} {
		s := newSignInStubs(t, "t-a")
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		if err := login(t, rt, s, args...); exitCode(err) != ExitUsage {
			t.Errorf("%v: err = %v, want exit %d", args, err, ExitUsage)
		}
	}
}
