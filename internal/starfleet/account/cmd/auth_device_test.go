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
	signIn := func(t *testing.T, ssh bool, openErr error,
		args ...string) (stored, string) {
		t.Helper()
		s := newSignInStubs(t, "t-a")
		if ssh {
			sessionEnv = func(k string) string {
				return map[string]string{"SSH_TTY": "/dev/pts/1"}[k]
			}
		}
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

	want, _ := signIn(t, false, nil)
	if want.profile.Auth != config.AuthBrowser || want.refresh == "" {
		t.Fatalf("browser sign-in stored %+v", want)
	}
	for _, tc := range []struct {
		name    string
		openErr error
		args    []string
		stderr  string
		ssh     bool
		browser bool // the browser path, not a code, is wanted
	}{
		{name: "--device", args: []string{"--device"},
			stderr: "WXYZ-1234"},
		{name: "ssh session with no display", ssh: true,
			stderr: "Not opening a browser: this is an SSH session"},
		{name: "--browser over ssh with no display", ssh: true,
			browser: true, args: []string{"--browser"},
			stderr: "Opening your browser"},
		{name: "no browser opens",
			openErr: errors.New("exec: xdg-open: not found"),
			stderr:  "signing in with a code instead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ssh && !tc.browser {
				// Fails the browser path, so only the device path passes.
				tc.openErr = errors.New("opener must not run over SSH")
			}
			got, errb := signIn(t, tc.ssh, tc.openErr, tc.args...)
			if tc.ssh && strings.Contains(errb, "could not open") {
				t.Errorf("tried the opener over SSH:\n%s", errb)
			}
			if got != want {
				t.Errorf("stored %+v, want the browser's %+v", got, want)
			}
			for _, s := range []string{tc.stderr, "WXYZ-1234",
				"Signed in as user@example.com."} {
				if tc.browser && s == "WXYZ-1234" {
					if strings.Contains(errb, s) {
						t.Errorf("signed in with a code:\n%s", errb)
					}
					continue
				}
				if !strings.Contains(errb, s) {
					t.Errorf("stderr lacks %q:\n%s", s, errb)
				}
			}
		})
	}
}

// TestDeviceLoginDebugOutputCarriesNoSecrets: whoever holds the device
// code can collect the tokens once the user confirms it.
func TestDeviceLoginDebugOutputCarriesNoSecrets(t *testing.T) {
	s := newSignInStubs(t, "t-a")
	s.issueRefresh = true
	rt, _, errb := testsupport.NewRuntime(t, "", "text")
	rt.Keychain = &keychain.Fake{}
	rt.Debug = true
	if err := login(t, rt, s, "--device"); err != nil {
		t.Fatalf("login: %v\n%s", err, errb.String())
	}
	if !strings.Contains(errb.String(), "/oauth/device/code") {
		t.Fatalf("--debug did not log the device code request:\n%s",
			errb.String())
	}
	issued, _ := s.issued.Load().(string)
	for _, secret := range append(strings.Fields(issued), "devcode~9f2",
		"refresh~1") {
		if secret == "" || strings.Contains(errb.String(), secret) {
			t.Errorf("--debug output carries %q", secret)
		}
	}
}

func TestDeviceLoginRefusesAPIClientFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--device", "--api-client"},
		{"--device", "--client-id", "i", "--client-secret", "s"},
		{"--device", "--insecure-storage"},
		{"--browser", "--device"},
		{"--browser", "--api-client"},
	} {
		s := newSignInStubs(t, "t-a")
		rt, _, _ := testsupport.NewRuntime(t, "", "text")
		if err := login(t, rt, s, args...); exitCode(err) != ExitUsage {
			t.Errorf("%v: err = %v, want exit %d", args, err, ExitUsage)
		}
	}
}
