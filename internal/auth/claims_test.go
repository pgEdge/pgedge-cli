package auth

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

func jwt(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) +
		".s"
}

func TestParseClaims(t *testing.T) {
	c, err := ParseClaims(jwt(`{"sub":"auth0|1","email":"a@example.com",` +
		`"exp":1700000000,"pgedge_tenant_id":"t2",` +
		`"pgedge_tenant_ids":["t1","t2"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "auth0|1" || c.Email != "a@example.com" ||
		!c.ExpiresAt().Equal(time.Unix(1700000000, 0)) ||
		c.TenantID != "t2" || len(c.Tenants()) != 2 {
		t.Errorf("claims = %+v", c)
	}
	if !c.HasTenant("T1") || c.HasTenant("t3") {
		t.Error("HasTenant should match the list, ignoring case")
	}

	for _, bad := range []string{"one-part", "a.!!!.c", jwt("not json")} {
		if _, err := ParseClaims(bad); err == nil {
			t.Errorf("ParseClaims(%q) should fail", bad)
		}
	}
}

func TestClaimsTenantsFallsBackToTheDefault(t *testing.T) {
	for _, tc := range []struct {
		c    Claims
		want int
	}{
		{Claims{TenantID: "t"}, 1},
		{Claims{}, 0},
	} {
		if got := len(tc.c.Tenants()); got != tc.want {
			t.Errorf("%+v: %d tenants, want %d", tc.c, got, tc.want)
		}
	}
	if !(&Claims{}).ExpiresAt().IsZero() {
		t.Error("no exp should be the zero time")
	}
}

func TestSessionForBindsTheAPIURL(t *testing.T) {
	tok := &CachedToken{AccessToken: jwt(`{"sub":"u"}`),
		Fingerprint: SessionFingerprint("https://a", "u")}
	if !tok.SessionFor("https://a") {
		t.Error("a session should match its own API URL")
	}
	if tok.SessionFor("https://b") {
		t.Error("a session must not match another API URL")
	}
	// An API client's binding never passes for a session.
	tok.Fingerprint = Fingerprint("https://a", "u", "")
	if tok.SessionFor("https://a") {
		t.Error("an API client fingerprint passed as a session")
	}
	if (&CachedToken{AccessToken: "x", Fingerprint: "y"}).SessionFor("a") ||
		(&CachedToken{}).SessionFor("a") {
		t.Error("an unreadable or unbound token is not a session")
	}
}

func TestResolveCredentialsReportsABrowserSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
	a := &Auth{Profile: "p", Module: "starfleet"}
	ap := &config.StarfleetProfile{Auth: config.AuthBrowser}
	creds, src, err := a.ResolveCredentials(ap, "", "")
	if err != nil || creds != nil || src != SourceBrowser {
		t.Fatalf("got %v %q %v, want the browser source", creds, src, err)
	}
	// Flags still outrank the profile.
	if _, src, _ := a.ResolveCredentials(ap, "i", "s"); src != SourceFlags {
		t.Errorf("source = %q, want flags", src)
	}
}
