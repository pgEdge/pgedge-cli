package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestDeviceLoginSavesTheBrowserSession checks that --device, and the
// switch to a code when no browser opens, store what a browser sign-in
// stores, apart from sign_in, so renewal and logout cannot tell them
// apart. It also checks which URLs the browser is asked to open.
func TestDeviceLoginSavesTheBrowserSession(t *testing.T) {
	type stored struct {
		profile config.StarfleetProfile
		refresh string
	}
	const deviceURL = "https://auth.example/activate"
	signIn := func(t *testing.T, ssh bool, openErr error,
		args ...string) (stored, string, []string) {
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
		return stored{p, refresh}, errb.String(), s.opened
	}

	want, _, _ := signIn(t, false, nil)
	if want.profile.Auth != config.AuthBrowser || want.refresh == "" ||
		want.profile.SignIn != config.SignInBrowser {
		t.Fatalf("browser sign-in stored %+v", want)
	}
	for _, tc := range []struct {
		name    string
		openErr error
		args    []string
		stderr  string
		ssh     bool
		browser bool // the browser path, not a code, is wanted
		// opensDevice: the device code's URL is opened, after the code
		// is printed.
		opensDevice bool
	}{
		{name: "--device", args: []string{"--device"},
			stderr: "WXYZ-1234", opensDevice: true},
		{name: "--device whose opener fails", args: []string{"--device"},
			openErr: errors.New("exec: xdg-open: not found"),
			stderr:  "WXYZ-1234", opensDevice: true},
		{name: "--device over ssh with no display", ssh: true,
			args: []string{"--device"}, stderr: "WXYZ-1234"},
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
			got, errb, opened := signIn(t, tc.ssh, tc.openErr, tc.args...)
			if tc.ssh && strings.Contains(errb, "could not open") {
				t.Errorf("tried the opener over SSH:\n%s", errb)
			}
			wantProfile := want
			if !tc.browser {
				wantProfile.profile.SignIn = config.SignInDevice
			}
			if got != wantProfile {
				t.Errorf("stored %+v, want %+v", got, wantProfile)
			}
			if gotOpen := slices.Contains(opened, deviceURL); gotOpen !=
				tc.opensDevice {
				t.Errorf("opened %q, want the device URL opened: %v",
					opened, tc.opensDevice)
			}
			if tc.ssh && !tc.browser && len(opened) != 0 {
				t.Errorf("opened %q over SSH", opened)
			}
			if tc.opensDevice && strings.Contains(errb, "xdg-open") {
				t.Errorf("a failed open printed:\n%s", errb)
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
