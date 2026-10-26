package testsupport

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// FakeJWT builds an unsigned JWT carrying claims. The CLI never checks
// a signature, so this is all a test needs.
func FakeJWT(t testing.TB, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal jwt part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) +
		".sig"
}

// SessionToken is a user access token for sub, reaching tenants, with
// the first tenant as the default and exp at expiresAt.
func SessionToken(t testing.TB, sub string, expiresAt time.Time,
	tenants ...string) string {
	t.Helper()
	claims := map[string]any{"sub": sub, "exp": expiresAt.Unix()}
	if len(tenants) > 0 {
		claims["pgedge_tenant_id"] = tenants[0]
		claims["pgedge_tenant_ids"] = tenants
	}
	return FakeJWT(t, claims)
}

// SeedSession makes rt's profile a browser session for apiURL, with a
// cached token for sub reaching tenants (the first is selected),
// expiring at expiresAt.
func SeedSession(t *testing.T, rt *module.Runtime, apiURL, sub string,
	expiresAt time.Time, tenants ...string) string {
	t.Helper()
	tok := SessionToken(t, sub, expiresAt, tenants...)
	selected := ""
	if len(tenants) > 0 {
		selected = tenants[0]
	}
	rt.Config.SetStarfleetProfile(rt.Profile, &config.StarfleetProfile{
		APIURL: apiURL, Auth: config.AuthBrowser, TenantID: selected})
	if err := rt.Config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	store := &auth.Auth{Profile: rt.Profile, Module: "starfleet"}
	if err := store.SaveToken(&auth.CachedToken{
		AccessToken: tok,
		ExpiresAt:   expiresAt,
		Fingerprint: auth.SessionFingerprint(apiURL, sub),
		User:        sub + "@example.com",
	}); err != nil {
		t.Fatalf("save token: %v", err)
	}
	return tok
}
