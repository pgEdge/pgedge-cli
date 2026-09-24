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
