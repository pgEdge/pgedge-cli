package conn

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/pflag"
)

const sessionAPI = "https://api.example"

// editedHeaders runs c's editor over a fresh request.
func editedHeaders(t *testing.T, c *Conn) (http.Header, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.APIURL+"/x", http.NoBody)
	err := c.BearerEditor(context.Background(), req)
	return req.Header, err
}

func wantExit(t *testing.T, err error, code int, substr string) {
	t.Helper()
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != code {
		t.Fatalf("err = %v, want exit %d", err, code)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("err = %q, want it to contain %q", err, substr)
	}
}

func TestSessionSendsTheProfileTenant(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	tok := testsupport.SeedSession(t, rt, sessionAPI, "u1",
		time.Now().Add(time.Hour), "t-a", "t-b")

	for _, resolveFn := range []func() (*Conn, error){
		func() (*Conn, error) { return Resolve(rt, Flags{}) },
		func() (*Conn, error) { return ResolveEphemeral(rt, Flags{}) },
	} {
		c, err := resolveFn()
		if err != nil {
			t.Fatal(err)
		}
		h, err := editedHeaders(t, c)
		if err != nil {
			t.Fatal(err)
		}
		if h.Get("Authorization") != "Bearer "+tok ||
			h.Get(SelectedTenantHeader) != "t-a" {
			t.Errorf("headers = %v", h)
		}
		if c.Session == nil || c.Session.TenantID != "t-a" ||
			c.Session.User != "u1@example.com" ||
			len(c.Session.TenantIDs) != 2 {
			t.Errorf("session = %+v", c.Session)
		}
	}
}

func TestSessionTenantFlag(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, sessionAPI, "u1",
		time.Now().Add(time.Hour), "t-a", "t-b")

	c, err := Resolve(rt, Flags{Tenant: "t-b"})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := editedHeaders(t, c); h.Get(SelectedTenantHeader) != "t-b" {
		t.Errorf("--tenant did not override the profile: %v", h)
	}

	_, err = Resolve(rt, Flags{Tenant: "t-z"})
	wantExit(t, err, ExitUsage, "t-a, t-b")
}

func TestAPIClientNeverSendsTheTenantHeader(t *testing.T) {
	stub := newTokenStub(t, "client-tok")
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	c, err := Resolve(rt, Flags{ClientID: "id", ClientSecret: "s",
		APIURL: stub.url(), Timeout: RequestTimeout})
	if err != nil {
		t.Fatal(err)
	}
	h, err := editedHeaders(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if h.Get(SelectedTenantHeader) != "" || c.Session != nil {
		t.Errorf("an API client sent %s: %v", SelectedTenantHeader, h)
	}

	_, err = Resolve(rt, Flags{ClientID: "id", ClientSecret: "s",
		APIURL: stub.url(), Tenant: "t-a"})
	wantExit(t, err, ExitUsage, "--tenant applies only")
}

func TestSessionExpiredOrMissing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		seed    func(t *testing.T, rt *module.Runtime)
		apiURL  string
		want    string
	}{
		{"expired", "default", seedExpiring(-time.Minute), "",
			"run 'pgedge starfleet auth login' to"},
		{"inside the refresh window", "default",
			seedExpiring(auth.RefreshWindow / 2), "", "has expired"},
		{"other API URL", "default", seedExpiring(time.Hour),
			"https://other.example", "https://other.example"},
		{"no cached token", "dev", seedProfileOnly, "",
			"auth login --profile dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			rt.Profile = tc.profile
			tc.seed(t, rt)
			_, err := Resolve(rt, Flags{APIURL: tc.apiURL})
			wantExit(t, err, ExitAuth, tc.want)
		})
	}
}

func TestSessionWaitThatOutlivesTheTokenFailsWithTheSameMessage(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, sessionAPI, "u1",
		time.Now().Add(time.Hour), "t-a")
	c, err := Resolve(rt, Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editedHeaders(t, c); err != nil {
		t.Fatalf("a live session failed: %v", err)
	}
	t.Cleanup(func() { now = time.Now })
	now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	_, err = editedHeaders(t, c)
	wantExit(t, err, ExitAuth, "has expired or is missing")
}

func TestFlagsOverASessionLeaveItsCacheAlone(t *testing.T) {
	stub := newTokenStub(t, "client-tok")
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	sess := testsupport.SeedSession(t, rt, sessionAPI, "u1",
		time.Now().Add(time.Hour), "t-a")
	c, err := Resolve(rt, Flags{ClientID: "id", ClientSecret: "s",
		APIURL: stub.url(), Timeout: RequestTimeout})
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "client-tok" || c.Session != nil {
		t.Fatalf("flags did not win over the session: %+v", c)
	}
	tok, err := Store(rt).LoadToken()
	if err != nil || tok.AccessToken != sess || !tok.SessionFor(sessionAPI) {
		t.Fatalf("the session cache was overwritten: %+v, %v", tok, err)
	}
	if _, err := Resolve(rt, Flags{}); err != nil {
		t.Errorf("the session no longer resolves: %v", err)
	}
}

func TestStaleProfileTenantIsAUsageError(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	testsupport.SeedSession(t, rt, sessionAPI, "u1",
		time.Now().Add(time.Hour), "t-a", "t-b")
	rt.Config.SetStarfleetProfile("default", &config.StarfleetProfile{
		APIURL: sessionAPI, Auth: config.AuthBrowser, TenantID: "t-gone"})
	_, err := Resolve(rt, Flags{})
	wantExit(t, err, ExitUsage, "auth login --tenant")
	if _, err := Resolve(rt, Flags{Tenant: "t-b"}); err != nil {
		t.Errorf("--tenant should override a stale profile tenant: %v", err)
	}
}

func TestSaveSession(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	at := testsupport.SessionToken(t, "u1", exp, "t-a")
	if _, err := SaveSession(rt, sessionAPI, at, "me", exp); err != nil {
		t.Fatal(err)
	}
	tok, err := Store(rt).LoadToken()
	if err != nil || !tok.SessionFor(sessionAPI) || tok.User != "me" ||
		!tok.ExpiresAt.Equal(exp) {
		t.Fatalf("cached = %+v, %v", tok, err)
	}
	if _, err := SaveSession(rt, sessionAPI, "not-a-jwt", "me",
		exp); err == nil {
		t.Error("an unreadable token was saved")
	}
	noSub := testsupport.FakeJWT(t, map[string]any{"exp": 1})
	if _, err := SaveSession(rt, sessionAPI, noSub, "me", exp); err == nil {
		t.Error("a token with no subject was saved")
	}
}

func TestReadFlags(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	fs.String("client-id", "i", "")
	fs.String("client-secret", "s", "")
	fs.String("api-url", "u", "")
	fs.String("tenant", "t", "")
	fs.Duration("timeout", time.Second, "")
	got := ReadFlags(fs)
	if got != (Flags{ClientID: "i", ClientSecret: "s", APIURL: "u",
		Tenant: "t", Timeout: time.Second}) {
		t.Errorf("ReadFlags = %+v", got)
	}
	if ReadFlags(pflag.NewFlagSet("y", pflag.ContinueOnError)).Timeout !=
		RequestTimeout {
		t.Error("a missing --timeout should keep the default")
	}
}

func seedExpiring(in time.Duration) func(*testing.T, *module.Runtime) {
	return func(t *testing.T, rt *module.Runtime) {
		testsupport.SeedSession(t, rt, sessionAPI, "u1",
			time.Now().Add(in), "t-a")
	}
}

func seedProfileOnly(t *testing.T, rt *module.Runtime) {
	rt.Config.SetStarfleetProfile(rt.Profile, &config.StarfleetProfile{
		APIURL: sessionAPI, Auth: config.AuthBrowser})
}
