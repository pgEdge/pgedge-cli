package auth

import (
	"encoding/base64"
	"testing"
	"time"
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
