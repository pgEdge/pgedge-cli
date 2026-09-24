package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

func TestConfigSetPersists(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	// Real files: config set checks a cert path is readable before it
	// writes the profile, so the fictional paths this fixture
	// used to pass now fail the command rather than exercising it.
	caPath, keyPath := writeCertPair(t)
	certPath := caPath
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "set",
		"--base-url", "https://cp-1:3000",
		"--ca-cert", caPath,
		"--client-cert", certPath,
		"--client-key", keyPath,
		"--insecure"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	reloaded, err := config.Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p := reloaded.ControlplaneProfile("default")
	if len(p.BaseURLs) != 1 || p.BaseURLs[0] != "https://cp-1:3000" {
		t.Errorf("BaseURLs = %v, want [https://cp-1:3000]", p.BaseURLs)
	}
	if p.BaseURL != "" {
		t.Errorf("legacy BaseURL should be cleared, got %q", p.BaseURL)
	}
	if got := p.CACert; got != caPath {
		t.Errorf("CACert = %q, want %q", got, caPath)
	}
	if got := p.ClientCert; got != certPath {
		t.Errorf("ClientCert = %q, want %q", got, certPath)
	}
	if got := p.ClientKey; got != keyPath {
		t.Errorf("ClientKey = %q, want %q", got, keyPath)
	}
	if got := p.InsecureSkipVerify; got != true {
		t.Errorf("InsecureSkipVerify = %v, want true", got)
	}
}

func TestConfigSetPersistsTimeout(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "set", "--timeout", "45s"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	reloaded, err := config.Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.ControlplaneProfile("default").Timeout; got != "45s" {
		t.Errorf("Timeout = %q, want 45s", got)
	}
}

func TestConfigView(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "view",
		"--base-url", "http://example:3000"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config view: %v", err)
	}
	if !strings.Contains(out.String(), "http://example:3000") {
		t.Errorf("view missing resolved url: %q", out.String())
	}
}

func TestConfigViewShowsTimeout(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "view", "--timeout", "45s"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config view: %v", err)
	}
	if !strings.Contains(out.String(), "timeout") {
		t.Errorf("view missing timeout row: %q", out.String())
	}
	if !strings.Contains(out.String(), "45s") {
		t.Errorf("view missing timeout value: %q", out.String())
	}
}

func TestConfigViewJSON(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "view",
		"--base-url", "http://example:3000"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config view json: %v", err)
	}
	if !strings.Contains(out.String(),
		"\"base_urls\":[\"http://example:3000\"]") {
		t.Errorf("view json missing base_urls: %q", out.String())
	}
	if !strings.Contains(out.String(), "\"timeout\":") {
		t.Errorf("view json missing timeout key: %q", out.String())
	}
}

func TestConfigViewYAML(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "yaml")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "view",
		"--base-url", "http://example:3000"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config view yaml: %v", err)
	}
	if !strings.Contains(out.String(), "http://example:3000") ||
		!strings.Contains(out.String(), "base_urls") {
		t.Errorf("view yaml missing base_urls list: %q", out.String())
	}
}

func TestConfigSetPersistsMultipleBaseURLs(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "set",
		"--base-url", "http://a:3000", "--base-url", "http://b:3000"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	reloaded, err := config.Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p := reloaded.ControlplaneProfile("default")
	want := []string{"http://a:3000", "http://b:3000"}
	if !reflect.DeepEqual(p.BaseURLs, want) {
		t.Errorf("BaseURLs = %v, want %v", p.BaseURLs, want)
	}
}

// A cert path is checked where it is accepted, not one command later.
// Otherwise `config set --ca-cert /typo.crt` exits 0 and
// the failure surfaces on the next verb that built a client — a
// different command, possibly a different session.
func TestConfigSetRejectsAnUnreadableCertPath(t *testing.T) {
	for _, flag := range []string{
		"--ca-cert", "--client-cert", "--client-key",
	} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			cmd := NewControlplaneCmd(rt)
			missing := t.TempDir() + "/not-there.pem"
			cmd.SetArgs([]string{"config", "set",
				"--base-url", "https://cp-1:3000", flag, missing})
			cmd.SetOut(out)
			cmd.SetErr(out)
			err := cmd.Execute()
			assertExitUsage(t, err)
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("message %q does not name the path", err.Error())
			}
			// The profile must not be written: a config set that
			// reports failure and saves anyway is worse than one that
			// saves silently, because the next read disagrees with
			// what the operator was told.
			reloaded, lerr := config.Load("")
			if lerr != nil {
				t.Fatalf("reload: %v", lerr)
			}
			if p := reloaded.ControlplaneProfile("default"); len(p.BaseURLs) != 0 {
				t.Errorf("profile was saved despite the error: %+v", p)
			}
		})
	}
}

// The control: a readable file is still accepted, so the check cannot
// be "fixed" by refusing the flags outright.
func TestConfigSetAcceptsReadableCertPaths(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	certPath, keyPath := writeCertPair(t)
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"config", "set",
		"--base-url", "https://cp-1:3000",
		"--ca-cert", certPath,
		"--client-cert", certPath,
		"--client-key", keyPath})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	reloaded, err := config.Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p := reloaded.ControlplaneProfile("default"); p.CACert != certPath ||
		p.ClientCert != certPath || p.ClientKey != keyPath {
		t.Errorf("cert paths not persisted: %+v", p)
	}
}
