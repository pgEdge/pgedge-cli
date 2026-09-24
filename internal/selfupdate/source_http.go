package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/httplog"
)

// repoOwner and repoName are pgEdge/pgedge-cli, spelled once and
// shared by both rungs of the ladder.
const (
	repoOwner = "pgEdge"
	repoName  = "pgedge-cli"
)

// apiRelease and apiAsset mirror the fields of GitHub's release JSON
// that this package uses. Both HTTPSource (a plain GET) and GHSource
// (gh api, which returns the same JSON) decode into these.
type apiRelease struct {
	TagName string     `json:"tag_name"`
	Assets  []apiAsset `json:"assets"`
}

type apiAsset struct {
	Name string `json:"name"`
}

func releasesFromAPI(raw []apiRelease) []Release {
	releases := make([]Release, len(raw))
	for i, r := range raw {
		assets := make([]Asset, len(r.Assets))
		for j, a := range r.Assets {
			assets[j] = Asset(a)
		}
		releases[i] = Release{TagName: r.TagName, Assets: assets}
	}
	return releases
}

// HTTPSource fetches releases and assets from GitHub's unauthenticated
// REST API, the ladder's first rung. It carries no credentials: the
// repository is public, so the private-asset endpoint an authenticated
// transport would need is deliberately not built.
type HTTPSource struct {
	baseAPI string
	baseDL  string
	list    *http.Client
	dl      *http.Client
	// dlBase is the download client's own transport, kept so Logging
	// wraps it rather than whatever is already installed.
	dlBase http.RoundTripper
}

// NewHTTPSource builds an HTTPSource against baseAPI (a GitHub API
// origin, e.g. "https://api.github.com") and baseDL (a download
// origin, e.g. "https://github.com"). Splitting them lets tests point
// each at its own httptest server.
func NewHTTPSource(baseAPI, baseDL string) *HTTPSource {
	// The download client has no overall timeout of its own (the
	// caller's DownloadTimeout context bounds the call), but a server
	// that accepts and never answers gets 10s to send headers.
	//
	// CLONE DefaultTransport: a zero-value http.Transport ignores
	// HTTPS_PROXY (Proxy nil) and bounds neither the dial nor the TLS
	// handshake, so behind an enterprise proxy `self update` alone
	// would fail to download while every other client, which ends at
	// DefaultTransport, works. internal/controlplane/cmd/client.go
	// clones the same way.
	dlBase := http.DefaultTransport.(*http.Transport).Clone()
	dlBase.ResponseHeaderTimeout = 10 * time.Second
	return &HTTPSource{
		baseAPI: baseAPI,
		baseDL:  baseDL,
		// A small JSON response, so a hang fails fast.
		list:   &http.Client{Timeout: 10 * time.Second},
		dl:     &http.Client{Transport: dlBase},
		dlBase: dlBase,
	}
}

// Logging renders this rung's traffic to out at lvl. The download
// client is capped at Verbose: dumping megabytes of binary archive
// would bury the line --debug was turned on to read.
func (s *HTTPSource) Logging(out io.Writer, lvl httplog.Level) *HTTPSource {
	s.list.Transport = httplog.Wrap(http.DefaultTransport, out, lvl)
	dlLvl := lvl
	if dlLvl > httplog.Verbose {
		dlLvl = httplog.Verbose
	}
	s.dl.Transport = httplog.Wrap(s.dlBase, out, dlLvl)
	return s
}

// Releases GETs <baseAPI>/repos/pgEdge/pgedge-cli/releases?per_page=30.
// A 404 is an error, never an empty list, so Chain falls through to gh
// rather than reporting nothing to update.
func (s *HTTPSource) Releases(ctx context.Context) ([]Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=30", s.baseAPI, repoOwner, repoName)
	resp, err := s.get(ctx, s.list, url)
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repository not found or private")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching releases: unexpected status %s", resp.Status)
	}

	var raw []apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding releases: %w", err)
	}
	return releasesFromAPI(raw), nil
}

// Download GETs <baseDL>/pgEdge/pgedge-cli/releases/download/<tag>/<assetName>
// and writes the body to dstDir/<assetName>, returning that path.
func (s *HTTPSource) Download(
	ctx context.Context, tag, assetName, dstDir string,
) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", s.baseDL, repoOwner, repoName, tag, assetName)
	resp, err := s.get(ctx, s.dl, url)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", assetName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: unexpected status %s", assetName, resp.Status)
	}

	dst := filepath.Join(dstDir, assetName)
	f, err := os.Create(dst) //nolint:gosec // G304: dstDir is caller-controlled (a temp dir this package owns), assetName names the release asset being fetched
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", dst, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		// A truncated body must leave NOTHING behind: the gh rung is
		// tried next on this path, and `gh release download` refuses to
		// overwrite an existing file.
		_ = f.Close()
		_ = os.Remove(dst)
		return "", fmt.Errorf("writing %s: %w", dst, err)
	}
	return dst, nil
}

// get issues a GET bound to ctx, so the caller's deadline reaches the
// transport rather than only the client's own fixed timeouts.
func (s *HTTPSource) get(
	ctx context.Context, client *http.Client, url string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
