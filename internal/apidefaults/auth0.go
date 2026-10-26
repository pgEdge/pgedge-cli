package apidefaults

import "strings"

// Auth0App is the public Auth0 configuration browser sign-in uses for
// one pgEdge Starfleet API.
type Auth0App struct {
	// Domain is the custom Auth0 domain the /authorize and
	// /oauth/token endpoints live on.
	Domain   string
	Audience string
	// ClientID is the native app's public client ID. Empty means
	// browser sign-in is not available for this API yet.
	ClientID string
}

// auth0Apps is the one place to edit when a client ID is issued.
var auth0Apps = map[string]Auth0App{
	"https://api.pgedge.com": {
		Domain:   "auth.pgedge.com",
		Audience: "https://api.pgedge.com",
		ClientID: "SCexy9LhkGBmBGrBeH2K9jeapuKb6kQb",
	},
	"https://devapi.pgedge.com": {
		Domain:   "devauth.pgedge.com",
		Audience: "https://devapi.pgedge.com",
		ClientID: "ZHg4rTGZCCZf1DpaWdj2agAm7aDRvCUj",
	},
}

// Auth0For returns the Auth0 app for apiURL and whether browser sign-in
// is available for it: the URL is known and its client ID is filled in.
// A trailing slash is ignored.
func Auth0For(apiURL string) (Auth0App, bool) {
	app, ok := auth0Apps[strings.TrimRight(apiURL, "/")]
	return app, ok && app.ClientID != ""
}
