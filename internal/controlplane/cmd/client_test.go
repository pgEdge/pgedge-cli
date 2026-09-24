package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/errbody"
	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// writeCertPair writes a self-signed cert (usable as a CA) and its
// key to temp files and returns their paths.
func writeCertPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(
		rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantNil      bool
		wantCode     int
		wantExact    string
		wantContains string
	}{
		{name: "ok", status: http.StatusOK, body: "body", wantNil: true},
		{name: "created", status: http.StatusCreated, body: "body", wantNil: true},
		{
			name:      "not found",
			status:    http.StatusNotFound,
			body:      "body",
			wantCode:  ExitNotFound,
			wantExact: "resource not found (404): body",
		},
		{
			name:      "unauthorized",
			status:    http.StatusUnauthorized,
			body:      `{"name":"invalid_join_token"}`,
			wantCode:  ExitAuth,
			wantExact: `authentication error (401): {"name":"invalid_join_token"}`,
		},
		{
			name:     "forbidden",
			status:   http.StatusForbidden,
			body:     "body",
			wantCode: ExitAuth,
		},
		{
			name:     "req timeout",
			status:   http.StatusRequestTimeout,
			body:     "body",
			wantCode: ExitTimeout,
		},
		{
			name:     "gw timeout",
			status:   http.StatusGatewayTimeout,
			body:     "body",
			wantCode: ExitTimeout,
		},
		{
			name:     "server error",
			status:   http.StatusInternalServerError,
			body:     "body",
			wantCode: ExitGeneral,
		},
		// control-plane's storage layer reports a key miss as a
		// 500 server_error instead of a 404; the CLI remaps that one
		// shape to exit 4 to match the sibling `database get` path.
		{
			name:   "storage key not found remapped to not found",
			status: http.StatusInternalServerError,
			body: `{"name":"server_error","message":"failed to fetch ` +
				`hosts from storage: \"/hosts/nosuchhost\": key not found"}`,
			wantCode:     ExitNotFound,
			wantContains: "resource not found (500",
		},
		// A genuine server_error that is NOT a storage key-miss must
		// stay exit 1 -- this is the guard against the fix becoming a
		// blanket 500->404 remap.
		{
			name:     "unrelated server_error stays general",
			status:   http.StatusInternalServerError,
			body:     `{"name":"server_error","message":"panic: nil pointer"}`,
			wantCode: ExitGeneral,
		},
		// A server_error whose message merely contains the bare words
		// "key not found" for an unrelated reason (broken key material,
		// not a missing resource) must also stay exit 1 -- the bare
		// substring match this replaced would have wrongly remapped it.
		{
			name:   "unrelated key-not-found message stays general",
			status: http.StatusInternalServerError,
			body: `{"name":"server_error","message":"failed to decrypt ` +
				`join token: signing key not found in vault"}`,
			wantCode: ExitGeneral,
		},
		// Malformed JSON under a 500 also stays exit 1: it does not
		// parse into the {name, message} shape isStorageKeyNotFound
		// requires, so it falls through to the generic branch.
		{
			name:     "malformed json server error stays general",
			status:   http.StatusInternalServerError,
			body:     `{"name":"server_error", not json`,
			wantCode: ExitGeneral,
		},
		// route miss: exact mux body, with and without trailing newline
		{
			name:         "route miss no newline",
			status:       http.StatusNotFound,
			body:         "404 page not found",
			wantCode:     ExitGeneral,
			wantContains: "does not serve an endpoint this command needs",
		},
		{
			name:         "route miss with newline",
			status:       http.StatusNotFound,
			body:         "404 page not found\n",
			wantCode:     ExitGeneral,
			wantContains: "does not serve an endpoint this command needs",
		},
		// the route-miss message names the
		// support floor even though this path fires without ever
		// knowing the server's actual version.
		{
			name:         "route miss names the support floor",
			status:       http.StatusNotFound,
			body:         "404 page not found",
			wantCode:     ExitGeneral,
			wantContains: "supports Control Plane >= " + SupportFloor,
		},
		// genuine miss: JSON APIError body keeps the legacy path exactly
		{
			name:     "json api error",
			status:   http.StatusNotFound,
			body:     `{"name":"not_found","message":"database not found"}`,
			wantCode: ExitNotFound,
			wantExact: `resource not found (404): ` +
				`{"name":"not_found","message":"database not found"}`,
		},
		// empty body stays legacy
		{
			name:      "empty body",
			status:    http.StatusNotFound,
			body:      "",
			wantCode:  ExitNotFound,
			wantExact: "resource not found (404): ",
		},
		// other text stays legacy
		{
			name:      "other text",
			status:    http.StatusNotFound,
			body:      "gateway says no",
			wantCode:  ExitNotFound,
			wantExact: "resource not found (404): gateway says no",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkResponse(tc.status, tc.body)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error, got nil")
			}
			ee, ok := err.(*ExitError)
			if !ok {
				t.Fatalf("want *ExitError, got %T", err)
			}
			if ee.Code() != tc.wantCode {
				t.Errorf("code = %d, want %d", ee.Code(), tc.wantCode)
			}
			if ee.Error() == "" {
				t.Error("empty error message")
			}
			if tc.wantExact != "" && ee.Error() != tc.wantExact {
				t.Errorf("message = %q, want exact %q", ee.Error(), tc.wantExact)
			}
			if tc.wantContains != "" && !strings.Contains(ee.Error(), tc.wantContains) {
				t.Errorf("message = %q, want contains %q", ee.Error(), tc.wantContains)
			}
		})
	}
}

func TestResolveConnection(t *testing.T) {
	t.Run("default when nothing set", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		cmd := NewControlplaneCmd(rt)
		c := mustResolveConnection(t, rt, cmd)
		if len(c.baseURLs) != 1 || c.baseURLs[0] != defaultBaseURL {
			t.Errorf("baseURLs = %v, want [%s]", c.baseURLs, defaultBaseURL)
		}
		if c.insecure {
			t.Error("insecure should default false")
		}
	})

	t.Run("profile file supplies values", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
			BaseURL:            "https://from-profile:3000",
			CACert:             "/p/ca.pem",
			ClientCert:         "/p/cert.pem",
			ClientKey:          "/p/key.pem",
			InsecureSkipVerify: true,
		})
		cmd := NewControlplaneCmd(rt)
		c := mustResolveConnection(t, rt, cmd)
		if len(c.baseURLs) != 1 || c.baseURLs[0] != "https://from-profile:3000" {
			t.Errorf("baseURLs = %v", c.baseURLs)
		}
		if c.caCert != "/p/ca.pem" || c.clientCert != "/p/cert.pem" ||
			c.clientKey != "/p/key.pem" || !c.insecure {
			t.Errorf("profile fields not carried: %+v", c)
		}
	})

	t.Run("profile alone supplies multiple base_urls", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
			BaseURLs: []string{"https://p1:3000", "https://p2:3000"},
		})
		cmd := NewControlplaneCmd(rt)
		c := mustResolveConnection(t, rt, cmd)
		want := []string{"https://p1:3000", "https://p2:3000"}
		if !reflect.DeepEqual(c.baseURLs, want) {
			t.Errorf("baseURLs = %v, want %v", c.baseURLs, want)
		}
	})

	t.Run("flag beats profile", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
			BaseURL:            "https://from-profile:3000",
			CACert:             "/p/ca.pem",
			ClientCert:         "/p/cert.pem",
			ClientKey:          "/p/key.pem",
			InsecureSkipVerify: false,
		})
		cmd := NewControlplaneCmd(rt)
		if err := cmd.ParseFlags([]string{
			"--base-url=https://from-flag:3000",
			"--ca-cert=/f/ca.pem",
			"--client-cert=/f/cert.pem",
			"--client-key=/f/key.pem",
			"--insecure",
		}); err != nil {
			t.Fatal(err)
		}
		c := mustResolveConnection(t, rt, cmd)
		if len(c.baseURLs) != 1 || c.baseURLs[0] != "https://from-flag:3000" {
			t.Errorf("baseURLs = %v, want flag value", c.baseURLs)
		}
		if c.caCert != "/f/ca.pem" || c.clientCert != "/f/cert.pem" ||
			c.clientKey != "/f/key.pem" || !c.insecure {
			t.Errorf("flag fields not carried: %+v", c)
		}
	})

	t.Run("repeated flag yields multiple", func(t *testing.T) {
		rt, _, _ := newTestRuntime(t, "", "text")
		cmd := NewControlplaneCmd(rt)
		if err := cmd.ParseFlags([]string{
			"--base-url=http://a:3000", "--base-url=http://b:3000",
		}); err != nil {
			t.Fatal(err)
		}
		c := mustResolveConnection(t, rt, cmd)
		want := []string{"http://a:3000", "http://b:3000"}
		if !reflect.DeepEqual(c.baseURLs, want) {
			t.Errorf("baseURLs = %v, want %v", c.baseURLs, want)
		}
	})
}

// controlplaneInnerTransport peels the error-body repair off a controlplane client's
// transport chain and returns what it wraps.
//
// Every assertion in this file about the TLS config or the diagnostic
// layer goes through it, which makes each of them ALSO an assertion
// that the repair is installed — the repair is unconditional,
// so a chain without it is a defect wherever it is noticed. That is
// deliberate: these tests exist to catch a client that silently lost a
// layer, and adding one more layer they are blind to would be the same
// mistake in the other direction.
func controlplaneInnerTransport(t *testing.T, hc *http.Client) http.RoundTripper {
	t.Helper()
	eb, ok := hc.Transport.(*errbody.Transport)
	if !ok {
		t.Fatalf("outermost transport = %T, want *errbody.Transport. "+
			"The repair is unconditional, so either it is missing "+
			"entirely or it is NESTED inside another layer — it must "+
			"wrap httplog, not sit under it, or --debug reports the "+
			"Content-Type the repair wrote rather than the one the "+
			"server sent", hc.Transport)
	}
	return eb.Base
}

func TestHTTPClientFor(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")

	t.Run("plain client does not enable insecure", func(t *testing.T) {
		hc, err := httpClientFor(rt, connConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		// The plain branch leaves the cloned default transport's TLS
		// config untouched; it must never opt into skipping verify.
		if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
			t.Error("plain client must not skip TLS verification")
		}
	})

	t.Run("insecure sets skip verify", func(t *testing.T) {
		hc, err := httpClientFor(rt, connConfig{insecure: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
			t.Error("insecure should set InsecureSkipVerify")
		}
	})

	// The diagnostic transport must WRAP the configured base, not
	// replace it. Layering it over http.DefaultTransport instead would
	// silently drop mTLS the moment someone added --debug to diagnose a
	// connection — the failure would look like a server problem.
	t.Run("debug wraps the mTLS transport", func(t *testing.T) {
		dbg, _, _ := newTestRuntime(t, "", "text")
		dbg.Debug = true
		certPath, keyPath := writeCertPair(t)
		hc, err := httpClientFor(dbg, connConfig{
			clientCert: certPath, clientKey: keyPath,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		log, ok := controlplaneInnerTransport(t, hc).(*httplog.Transport)
		if !ok {
			t.Fatalf("transport = %T, want *httplog.Transport",
				controlplaneInnerTransport(t, hc))
		}
		if log.Level != httplog.Debug {
			t.Errorf("level = %v, want debug", log.Level)
		}
		base, ok := log.Base.(*http.Transport)
		if !ok {
			t.Fatalf("wrapped base = %T, want *http.Transport", log.Base)
		}
		if base.TLSClientConfig == nil ||
			len(base.TLSClientConfig.Certificates) == 0 {
			t.Error("wrapping the transport lost the client certificate")
		}
	})

	// Neither flag: the DIAGNOSTIC layer is absent. The error-body
	// repair is still there — it is unconditional, which is why
	// controlplaneInnerTransport peels it first — so this is "no httplog layer",
	// not "no layers at all". It used to be both.
	t.Run("no flags adds no diagnostic layer", func(t *testing.T) {
		hc, err := httpClientFor(rt, connConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := controlplaneInnerTransport(t, hc).(*httplog.Transport); ok {
			t.Error("quiet runtime got a diagnostic transport")
		}
	})

	t.Run("valid ca-cert builds pool", func(t *testing.T) {
		certPath, _ := writeCertPair(t)
		hc, err := httpClientFor(rt, connConfig{caCert: certPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
			t.Error("valid ca-cert should populate RootCAs")
		}
	})

	t.Run("valid client cert/key loads", func(t *testing.T) {
		certPath, keyPath := writeCertPair(t)
		hc, err := httpClientFor(rt, connConfig{
			clientCert: certPath, clientKey: keyPath,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil ||
			len(tr.TLSClientConfig.Certificates) != 1 {
			t.Error("client cert/key should load one certificate")
		}
	})

	t.Run("missing ca-cert file errors", func(t *testing.T) {
		_, err := httpClientFor(rt, connConfig{
			caCert: filepath.Join(t.TempDir(), "nope.pem"),
		})
		assertExitUsage(t, err)
	})

	t.Run("garbage ca-cert errors", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.pem")
		if err := os.WriteFile(bad, []byte("not a cert"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := httpClientFor(rt, connConfig{caCert: bad})
		assertExitUsage(t, err)
		if !strings.Contains(err.Error(), "no certificates found") {
			t.Errorf("want 'no certificates found', got %v", err)
		}
	})

	t.Run("bad client cert/key errors", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.pem")
		if err := os.WriteFile(bad, []byte("nope"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := httpClientFor(rt, connConfig{
			clientCert: bad, clientKey: bad,
		})
		assertExitUsage(t, err)
	})
}

// TestTLSMinVersionFloor asserts that every branch of httpClientFor —
// including a plain https:// base URL with none of
// --ca-cert/--client-cert/--insecure set — pins MinVersion to TLS 1.3.
// All four variants build the same tls.Config literal in client.go,
// but the assertions are written per variant so a future split would
// still be covered.
func TestTLSMinVersionFloor(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")

	t.Run("plain https, no flags pins TLS 1.3", func(t *testing.T) {
		hc, err := httpClientFor(rt, connConfig{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil {
			t.Fatal("expected a TLS config even with no cert/insecure flags")
		}
		if tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)",
				tr.TLSClientConfig.MinVersion, tls.VersionTLS13)
		}
	})

	t.Run("insecure/default path pins TLS 1.3", func(t *testing.T) {
		hc, err := httpClientFor(rt, connConfig{insecure: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil {
			t.Fatal("expected a TLS config")
		}
		if tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)",
				tr.TLSClientConfig.MinVersion, tls.VersionTLS13)
		}
	})

	t.Run("mTLS/cert path pins TLS 1.3", func(t *testing.T) {
		certPath, keyPath := writeCertPair(t)
		hc, err := httpClientFor(rt, connConfig{
			clientCert: certPath, clientKey: keyPath,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil {
			t.Fatal("expected a TLS config")
		}
		if tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)",
				tr.TLSClientConfig.MinVersion, tls.VersionTLS13)
		}
	})

	t.Run("ca-cert path pins TLS 1.3", func(t *testing.T) {
		certPath, _ := writeCertPair(t)
		hc, err := httpClientFor(rt, connConfig{caCert: certPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tr := controlplaneInnerTransport(t, hc).(*http.Transport)
		if tr.TLSClientConfig == nil {
			t.Fatal("expected a TLS config")
		}
		if tr.TLSClientConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)",
				tr.TLSClientConfig.MinVersion, tls.VersionTLS13)
		}
	})
}

// TestTLSMinVersionRejectsTLS12Server proves the MinVersion floor
// actually bites on the wire, not just in the built config struct: a
// handshake against a peer capped at TLS 1.2 must fail with a protocol
// version error, while the same client succeeds against a default
// (TLS-1.3-capable) server. --insecure is used only to skip certificate
// verification for the self-signed httptest certs; it has no bearing on
// the negotiated protocol version.
func TestTLSMinVersionRejectsTLS12Server(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	hc, err := httpClientFor(rt, connConfig{insecure: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oldSrv := httptest.NewUnstartedServer(
		plainHandler(http.StatusOK, "ok"))
	oldSrv.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
	oldSrv.StartTLS()
	defer oldSrv.Close()

	if _, err := hc.Get(oldSrv.URL); err == nil {
		t.Fatal("want a protocol-version error against a TLS-1.2-only " +
			"server, got nil")
	} else if !strings.Contains(err.Error(), "protocol version") {
		t.Errorf("error = %v, want a protocol version mismatch", err)
	}

	newSrv := httptest.NewTLSServer(plainHandler(http.StatusOK, "ok"))
	defer newSrv.Close()
	resp, err := hc.Get(newSrv.URL)
	if err != nil {
		t.Fatalf("unexpected error against a modern TLS server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// mustResolveConnection is for the fixtures that set no timeout:, where
// a resolution error is a bug in the test rather than the case under
// test.
func mustResolveConnection(
	t *testing.T, rt *module.Runtime, cmd *cobra.Command,
) connConfig {
	t.Helper()
	c, err := resolveConnection(rt, cmd)
	if err != nil {
		t.Fatalf("resolveConnection: %v", err)
	}
	return c
}

// assertExitUsage asserts the code the PROCESS will exit with, via the
// same function main.go uses, rather than the concrete error type.
// Some cp rejections now come from the shared internal/cli validators
// and are *cli.UsageError; a type assertion would fail those while the
// contract they carry -- exit 2 -- is identical.
func assertExitUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("code = %d, want ExitUsage(%d); err=%v",
			got, ExitUsage, err)
	}
}

func TestResolveConnectionTimeoutDefault(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	cmd.SetArgs([]string{"version"})
	// Parse flags without running by resolving off a fresh subcommand:
	c := mustResolveConnection(t, rt, cmd)
	if c.timeout != 30*time.Second {
		t.Fatalf("default timeout = %v, want 30s", c.timeout)
	}
}

func TestResolveConnectionTimeoutFlag(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	cmd := NewControlplaneCmd(rt)
	// Use ParseFlags (not PersistentFlags().Set) so the persistent
	// flag set actually merges into cmd.Flags() before resolution —
	// matching TestResolveConnection's "flag beats profile" subtest.
	if err := cmd.ParseFlags([]string{"--timeout=5s"}); err != nil {
		t.Fatal(err)
	}
	c := mustResolveConnection(t, rt, cmd)
	if c.timeout != 5*time.Second {
		t.Fatalf("flag timeout = %v, want 5s", c.timeout)
	}
}

func TestRequestTimeoutAbortsSlowServer(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	err := runControlplane(t, rt, out, srv,
		"version", "--timeout", "20ms")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
}

func TestNetworkError(t *testing.T) {
	err := networkError("get version", errNet{})
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T", err)
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("code = %d, want ExitGeneral(%d)", ee.Code(), ExitGeneral)
	}
	if !strings.Contains(ee.Error(), "control-plane reachable") {
		t.Errorf("missing hint: %v", ee.Error())
	}
}

type errNet struct{}

func (errNet) Error() string { return "dial fail" }

func runControlplaneURLs(t *testing.T, rt *module.Runtime, out *bytes.Buffer,
	args []string, urls ...string,
) error {
	t.Helper()
	cmd := NewControlplaneCmd(rt)
	full := append([]string{}, args...)
	for _, u := range urls {
		full = append(full, "--base-url", u)
	}
	cmd.SetArgs(full)
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd.Execute()
}

func TestFailoverPicksFirstLiveServer(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	down := newServer(t, plainHandler(http.StatusServiceUnavailable, "no"))
	up := newServer(t, jsonHandler(http.StatusOK,
		`{"version":"9.9.9","revision":"abc","arch":"amd64"}`))
	if err := runControlplaneURLs(t, rt, out, []string{"version"}, down, up); err != nil {
		t.Fatalf("version with failover: %v", err)
	}
	if !strings.Contains(out.String(), "9.9.9") {
		t.Errorf("did not fail over to the live server: %q", out.String())
	}
}

func TestFailoverAllDownErrors(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	// Two closed servers: start then immediately close so dials fail.
	s1 := httptest.NewServer(http.NotFoundHandler())
	u1 := s1.URL
	s1.Close()
	s2 := httptest.NewServer(http.NotFoundHandler())
	u2 := s2.URL
	s2.Close()
	err := runControlplaneURLs(t, rt, out, []string{"version"}, u1, u2)
	if err == nil {
		t.Fatal("expected an error when all servers are down")
	}
	if !strings.Contains(err.Error(), u1) ||
		!strings.Contains(err.Error(), u2) {
		t.Errorf("error should name both URLs: %v", err)
	}
}

// TestFailoverWarnsBelowFloor covers the other of item 3's two call
// sites: with more than one --base-url configured, selectBaseURL
// already probes /v1/version to pick a live server, so that version
// is in hand for free and the below-floor warning fires there too.
func TestFailoverWarnsBelowFloor(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	down := newServer(t, plainHandler(http.StatusServiceUnavailable, "no"))
	up := newServer(t, jsonHandler(http.StatusOK, versionBodyFor("v0.9.1")))
	if err := runControlplaneURLs(t, rt, out, []string{"version"}, down, up); err != nil {
		t.Fatalf("version with failover: %v", err)
	}
	got := stderr.String()
	if strings.Count(got, "below the supported") != 1 {
		t.Fatalf("want exactly one below-floor warning, got: %q", got)
	}
	if !strings.Contains(got, "v0.9.1") || !strings.Contains(got, SupportFloor) {
		t.Errorf("warning must name both versions: %q", got)
	}
}

// TestFailoverSingleURLNoProbeNoWarning proves the single-base-url fast
// path in selectBaseURL never probes and therefore never warns, even
// against a server that IS below floor — the design deliberately does
// not add a version probe just for this warning on the common
// single-server path (see warnBelowFloor's doc comment).
func TestFailoverSingleURLNoProbeNoWarning(t *testing.T) {
	rt, out, stderr := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(http.StatusOK, versionBodyFor("v0.9.1")))
	if err := runControlplane(t, rt, out, url, "cluster", "info"); err != nil {
		// The dummy cluster-info call may itself fail against the
		// version stub (no cluster body); only the ABSENCE of the
		// below-floor warning is under test here.
		_ = err
	}
	if got := stderr.String(); strings.Contains(got, "below the supported") {
		t.Errorf("single-base-url fast path must not probe for version "+
			"and so must never warn, got: %q", got)
	}
}

func TestFailoverSingleURLNoProbe(t *testing.T) {
	// One server that ONLY answers /v1/version-less command paths would
	// still work because len<=1 skips the probe. Assert selection
	// returns the single URL unchanged without contacting it.
	rt, _, _ := newTestRuntime(t, "", "text")
	c := connConfig{baseURLs: []string{"http://single:3000"}, timeout: time.Second}
	got, err := selectBaseURL(rt, c)
	if err != nil || got != "http://single:3000" {
		t.Fatalf("got %q err %v, want the single URL, no probe", got, err)
	}
}

// A malformed timeout: used to be dropped on the floor, leaving the
// connection on its 30s default with nothing said. An operator who set
// ten minutes for a slow restore got thirty seconds and a timeout they
// could not explain.
//
// Each of the three values is a plausible
// spelling of a duration and none is a Go duration.
func TestResolveConnectionRejectsAMalformedProfileTimeout(t *testing.T) {
	for _, v := range []string{"30sec", "5", "2 minutes"} {
		t.Run(v, func(t *testing.T) {
			rt, _, _ := newTestRuntime(t, "", "text")
			rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
				Timeout: v,
			})
			cmd := NewControlplaneCmd(rt)
			_, err := resolveConnection(rt, cmd)
			assertExitUsage(t, err)
			for _, want := range []string{"timeout", v, "default"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not name %q",
						err.Error(), want)
				}
			}
		})
	}
}

// The control the rejection needs: a well-formed timeout: is still
// honoured, so the check cannot be "fixed" by refusing the field.
func TestResolveConnectionAcceptsAProfileTimeout(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{Timeout: "10m"})
	cmd := NewControlplaneCmd(rt)
	c, err := resolveConnection(rt, cmd)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if c.timeout != 10*time.Minute {
		t.Errorf("timeout = %v, want 10m", c.timeout)
	}
}

// TestBodyExcerptTrimsTrailingWhitespace pins the trim for this module.
func TestBodyExcerptTrimsTrailingWhitespace(t *testing.T) {
	if got := bodyExcerpt("{\"message\":\"x\"}\r\n\n"); got != `{"message":"x"}` {
		t.Errorf("bodyExcerpt = %q", got)
	}
}
