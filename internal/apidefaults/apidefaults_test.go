package apidefaults

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

func TestResolveStarfleetAPIURL(t *testing.T) {
	cases := []struct {
		name string
		ap   *config.StarfleetProfile
		flag string
		want string
	}{
		{"default", &config.StarfleetProfile{}, "", StarfleetAPIURL},
		{"nil profile", nil, "", StarfleetAPIURL},
		{"profile wins over default",
			&config.StarfleetProfile{APIURL: "https://p"}, "",
			"https://p"},
		{"flag wins over profile",
			&config.StarfleetProfile{APIURL: "https://p"},
			"https://f", "https://f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveStarfleetAPIURL(tc.ap, tc.flag); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuth0For(t *testing.T) {
	saved := auth0Apps
	t.Cleanup(func() { auth0Apps = saved })
	auth0Apps = map[string]Auth0App{
		"https://ready": {Domain: "d", Audience: "a", ClientID: "c"},
		"https://empty": {Domain: "d", Audience: "a"},
	}
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://ready", true},
		{"https://ready/", true},
		{"https://empty", false},
		{"https://unknown", false},
	} {
		if _, ok := Auth0For(tc.url); ok != tc.want {
			t.Errorf("Auth0For(%q) = %v, want %v", tc.url, ok, tc.want)
		}
	}
}

func TestAuth0AppsCoverBothAPIs(t *testing.T) {
	for _, u := range []string{StarfleetAPIURL,
		"https://devapi.pgedge.com"} {
		app, ok := auth0Apps[u]
		if !ok || app.Domain == "" || app.Audience != u {
			t.Errorf("%s: %+v", u, app)
		}
	}
}
