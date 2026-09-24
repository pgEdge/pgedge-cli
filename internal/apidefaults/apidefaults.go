// Package apidefaults owns platform-level API defaults: the pgEdge
// Starfleet base URL and its resolution rule (flag > profile > default).
// It sits outside the modules so internal/cli can print effective URLs
// without importing one; starfleet's conn package re-exports it.
package apidefaults

import "github.com/pgEdge/pgedge-cli/internal/config"

// StarfleetAPIURL is the default pgEdge Starfleet API base URL, used when
// neither the active profile nor --api-url supplies one.
const StarfleetAPIURL = "https://api.pgedge.com"

// ResolveStarfleetAPIURL applies the base-URL precedence.
func ResolveStarfleetAPIURL(ap *config.StarfleetProfile,
	flagAPIURL string) string {
	apiURL := ""
	if ap != nil {
		apiURL = ap.APIURL
	}
	if apiURL == "" {
		apiURL = StarfleetAPIURL
	}
	if flagAPIURL != "" {
		apiURL = flagAPIURL
	}
	return apiURL
}
