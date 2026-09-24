// Package selfupdate implements `pgedge self update`: resolving a
// release tag from a fetched release list, verifying the downloaded
// asset, and swapping it into place. This file holds the shapes later
// tasks' Source ladder, verification and swap steps build on.
package selfupdate

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string
}

// Release is one GitHub (or mirror) release: a tag and its assets.
type Release struct {
	TagName string
	Assets  []Asset
}
