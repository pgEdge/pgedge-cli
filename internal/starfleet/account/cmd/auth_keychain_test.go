package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// kcLogin runs `auth login` with flags against a token stub, with kc
// as the runtime's keychain, and returns the reloaded profile.
func kcLogin(t *testing.T, kc keychain.Store, extra ...string) (
	*module.Runtime, *config.StarfleetProfile, string,
) {
	rt, prof, _, errb := kcLoginBuffers(t, kc, extra...)
	return rt, prof, errb.String()
}

// kcLoginBuffers is kcLogin that also hands back the runtime's output
// buffers, reset, for a follow-up command.
func kcLoginBuffers(t *testing.T, kc keychain.Store, extra ...string) (
	rt *module.Runtime, prof *config.StarfleetProfile,
	out, errb *bytes.Buffer,
) {
	t.Helper()
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, "{}"))
	rt, out, errb = testsupport.NewRuntime(t, "", "text")
	rt.Keychain = kc
	args := append([]string{"auth", "login", "--client-id", "kid",
		"--client-secret", "ksecret", "--api-url", url}, extra...)
	if err := runAccount(t, rt, out, args...); err != nil {
		t.Fatalf("login: %v (stderr %q)", err, errb.String())
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	prof = cfg.StarfleetProfile("default")
	stderr := errb.String()
	out.Reset()
	errb.Reset()
	errb.WriteString(stderr)
	return rt, prof, out, errb
}

func TestLoginStoresTheSecretInTheKeychain(t *testing.T) {
	kc := &keychain.Fake{}
	rt, prof, stderr := kcLogin(t, kc)
	if prof.ClientSecret != "" || prof.ClientID != "kid" {
		t.Errorf("config profile = %+v, want the id and no secret", prof)
	}
	got, err := kc.Get(conn.Store(rt).KeychainAccount())
	if err != nil || got != "ksecret" {
		t.Errorf("keychain = %q, %v; want ksecret", got, err)
	}
	if !strings.Contains(stderr, "OS keychain") ||
		strings.Contains(stderr, "ksecret") {
		t.Errorf("stderr = %q", stderr)
	}

	// And every later command finds it there.
	res, err := conn.ResolveCredentials(rt, "", "", "")
	if err != nil || res.Source != auth.SourceKeychain ||
		res.Creds.ClientSecret != "ksecret" {
		t.Errorf("resolve after login = %+v, %v", res, err)
	}
}

func TestLoginMovesAPlaintextSecretOutOfTheFile(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetStarfleetProfile("default", &config.StarfleetProfile{
		ClientID: "old", ClientSecret: "old-plaintext"})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, "{}"))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	// NewRuntime isolated a fresh HOME; point it back at the seeded one.
	rt.Config = cfg
	rt.Keychain = &keychain.Fake{}
	if err := runAccount(t, rt, out, "auth", "login", "--client-id", "kid",
		"--client-secret", "ksecret", "--api-url", url); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if s := reloaded.StarfleetProfile("default").ClientSecret; s != "" {
		t.Errorf("file still holds a secret: %q", s)
	}
}

func TestLoginFallsBackToTheFileWhenTheKeychainFails(t *testing.T) {
	_, prof, stderr := kcLogin(t, &keychain.Fake{Err: errors.New("no dbus")})
	if prof.ClientSecret != "ksecret" {
		t.Errorf("file secret = %q, want the fallback", prof.ClientSecret)
	}
	if !strings.Contains(stderr, "plain text") ||
		!strings.Contains(stderr, "no dbus") {
		t.Errorf("stderr = %q, want the fallback warning and cause", stderr)
	}
}

func TestLoginInsecureStorageSkipsTheKeychain(t *testing.T) {
	kc := &keychain.Fake{}
	_, prof, stderr := kcLogin(t, kc, "--insecure-storage")
	if kc.Sets != 0 {
		t.Errorf("keychain Set called %d times", kc.Sets)
	}
	if prof.ClientSecret != "ksecret" {
		t.Errorf("file secret = %q", prof.ClientSecret)
	}
	if strings.Contains(stderr, "Warning") {
		t.Errorf("a chosen file store warned: %q", stderr)
	}
}

func TestLoginToTheFileDropsAStaleKeychainEntry(t *testing.T) {
	kc := &keychain.Fake{}
	rt, _, out, _ := kcLoginBuffers(t, kc)
	if err := runAccount(t, rt, out, "auth", "login", "--client-id", "kid",
		"--client-secret", "ksecret", "--api-url",
		rt.Config.StarfleetProfile("default").APIURL,
		"--insecure-storage"); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); !errors.Is(err,
		keychain.ErrNotFound) {
		t.Errorf("stale keychain entry survived: %v", err)
	}
}

func TestLoginWarnsWhenTheEnvPairIsSet(t *testing.T) {
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, "{}"))
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	t.Setenv(auth.EnvClientID, "eid")
	rt.Keychain = &keychain.Fake{}
	if err := runAccount(t, rt, out, "auth", "login", "--client-id", "kid",
		"--client-secret", "ksecret", "--api-url", url); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), "PGEDGE_CLIENT_ID") {
		t.Errorf("stderr = %q, want the env warning", errb.String())
	}
}

func TestLogoutRemovesTheKeychainEntry(t *testing.T) {
	kc := &keychain.Fake{}
	rt, _, out, _ := kcLoginBuffers(t, kc)
	if err := runAccount(t, rt, out, "auth", "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); !errors.Is(err,
		keychain.ErrNotFound) {
		t.Errorf("entry survived logout: %v", err)
	}
}

func TestLogoutWithAnUnreachableKeychainStillClearsTheFile(t *testing.T) {
	tests := []struct {
		name     string
		login    []string
		wantWarn bool
	}{
		// The secret went to the keychain, so failing to remove it
		// is worth saying.
		{"secret was in the keychain", nil, true},
		// The keychain never held it: a host without one stays quiet.
		{"secret was in the file", []string{"--insecure-storage"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, _, out, errb := kcLoginBuffers(t, &keychain.Fake{},
				tt.login...)
			errb.Reset()
			rt.Keychain = &keychain.Fake{Err: errors.New("locked")}
			if err := runAccount(t, rt, out, "auth", "logout"); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load("")
			if err != nil {
				t.Fatal(err)
			}
			if p := cfg.StarfleetProfile("default"); p.ClientSecret != "" ||
				p.ClientID != "" {
				t.Errorf("file credentials survived: %+v", p)
			}
			if got := strings.Contains(errb.String(), "locked"); got != tt.wantWarn {
				t.Errorf("warned = %v, want %v; stderr %q", got,
					tt.wantWarn, errb.String())
			}
		})
	}
}

func TestAuthStatusReportsEnvCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	t.Setenv(auth.EnvClientID, "eid")
	t.Setenv(auth.EnvClientSecret, "es")
	if err := runAccount(t, rt, out, "auth", "status"); err != nil {
		t.Fatal(err)
	}
	var report authStatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if report.Source != auth.SourceEnv || report.ClientID != "eid" ||
		report.APIURL != conn.DefaultAPIURL || report.TokenValid {
		t.Errorf("report = %+v", report)
	}
}

func TestAuthStatusTextSaysEnvTokensAreNeverCached(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	t.Setenv(auth.EnvClientID, "eid")
	t.Setenv(auth.EnvClientSecret, "es")
	if err := runAccount(t, rt, out, "auth", "status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "never cached") {
		t.Errorf("status = %q", out.String())
	}
}

func TestAuthStatusNamesAnUnreadableKeychain(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	rt.Config.SetStarfleetProfile("default",
		&config.StarfleetProfile{ClientID: "kid"})
	rt.Keychain = &keychain.Fake{Err: errors.New("locked")}
	err := runAccount(t, rt, out, "auth", "status")
	wantAuthExit(t, err, conn.ExitAuth)
	var report authStatusReport
	// cobra appends its "Error:" line to the same buffer.
	if jErr := json.NewDecoder(out).Decode(&report); jErr != nil {
		t.Fatalf("%v: %s", jErr, out.String())
	}
	if !strings.Contains(report.Problem, "keychain") {
		t.Errorf("problem = %q", report.Problem)
	}
}

func TestAuthStatusEnvWithExplicitProfileIsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	t.Setenv(auth.EnvClientID, "eid")
	t.Setenv(auth.EnvClientSecret, "es")
	rt.ProfileExplicit = true
	err := runAccount(t, rt, out, "auth", "status")
	wantAuthExit(t, err, conn.ExitUsage)
}

func TestWhoamiEnvHalfPairIsUsage(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	t.Setenv(auth.EnvClientSecret, "es")
	err := runAccount(t, rt, out, "auth", "whoami")
	wantAuthExit(t, err, conn.ExitUsage)
}

func TestDoctorReportsEnvCredentials(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	t.Setenv(auth.EnvClientID, "eid")
	t.Setenv(auth.EnvClientSecret, "es")
	if err := runDoctor(rt, &conn.Flags{APIURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "never cached") ||
		strings.Contains(out.String(), "expired or missing") {
		t.Errorf("doctor = %s", out.String())
	}
}

func TestDoctorNamesAnUnreadableKeychain(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	rt.Config.SetStarfleetProfile("default",
		&config.StarfleetProfile{ClientID: "kid"})
	rt.Keychain = &keychain.Fake{Err: errors.New("locked")}
	if err := runDoctor(rt, &conn.Flags{APIURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"problem":"could not read`) {
		t.Errorf("doctor = %s", out.String())
	}
}

// TestAnUnavailableKeychainIsAskedOnce: on a host whose keychain hangs,
// each call waits out the timeout, so neither the login fallback nor a
// logout of a file-held secret may ask it again.
func TestAnUnavailableKeychainIsAskedOnce(t *testing.T) {
	kc := &keychain.Fake{Err: errors.New("no dbus")}
	rt, _, out, _ := kcLoginBuffers(t, kc)
	if kc.Sets != 1 || kc.Deletes != 0 {
		t.Errorf("login: %d sets, %d deletes; want 1 and 0",
			kc.Sets, kc.Deletes)
	}
	if err := runAccount(t, rt, out, "auth", "logout"); err != nil {
		t.Fatal(err)
	}
	if kc.Deletes != 0 {
		t.Errorf("logout of a file-held secret asked the keychain")
	}
}

func TestLoginDropsItsKeychainEntryWhenTheSaveFails(t *testing.T) {
	testsupport.ClearEnv(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := dir + "/config.yaml"
	seed := &config.Config{}
	seed.SetStarfleetProfile("default", &config.StarfleetProfile{
		ClientID: "old", ClientSecret: "old-plaintext"})
	if err := seed.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, "{}"))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rt.Config = cfg
	kc := &keychain.Fake{}
	rt.Keychain = kc
	if err := runAccount(t, rt, out, "auth", "login", "--client-id", "kid",
		"--client-secret", "ksecret", "--api-url", url); err == nil {
		t.Fatal("login succeeded against an unwritable config directory")
	}
	if _, err := kc.Get(conn.Store(rt).KeychainAccount()); !errors.Is(err,
		keychain.ErrNotFound) {
		t.Errorf("keychain entry survived a failed save: %v", err)
	}
}
