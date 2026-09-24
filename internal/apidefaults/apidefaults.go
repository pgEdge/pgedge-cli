// Package apidefaults owns platform-level API defaults: the pgEdge
// Starfleet base URL and its resolution rule (flag > profile > default).
// It lives on the platform side of the layering line so that
// internal/cli can print effective URLs without importing any
// module. The starfleet module's conn package consumes it and re-exports
// the names its command trees use.
package apidefaults

import "github.com/pgEdge/pgedge-cli/internal/config"

// StarfleetAPIURL is the default pgEdge Starfleet API base URL, used when
// neither the active profile nor --api-url supplies one.
const StarfleetAPIURL = "https://api.pgedge.com"

// ResolveStarfleetAPIURL applies the base-URL precedence used across every
// Starfleet module: active-profile value, then the default, then the
// --api-url flag (highest).
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
