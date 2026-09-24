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
// REST API — the ladder's first rung. It carries no credentials: an
// authenticated transport would need the private-asset endpoint dance,
// and that need disappears the day the repo goes public, so it is
// deliberately not built (see the design spec).
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
	// Downloads have no overall timeout — a release binary on a slow
	// link can take longer than any fixed guess — but a server that
	// accepts the connection and then never answers is still bounded:
	// 10s to see response headers.
	//
	// CLONE DefaultTransport rather than building a bare one. A
	// zero-value http.Transport proxies nothing (Proxy nil, where
	// DefaultTransport sets ProxyFromEnvironment) and bounds neither
	// the dial nor the TLS handshake. The proxy half is the one that
	// reaches a user: an enterprise sitting behind an HTTPS_PROXY
	// could reach the API fine, because every other client here ends
	// at DefaultTransport, and then watch `self update` alone fail to
	// download. Clone is also what newAPIClient does with its own
	// transport (internal/controlplane/cmd/client.go).
	//
	// HTTP/2 was NOT among the losses, though the shape suggests it:
	// Transport.protocols() only takes its conservative branch when a
	// TLSClientConfig or a custom dialer is set, and the bare one set
	// neither, so it fell through and enabled HTTP/2 anyway.
	// ForceAttemptHTTP2 reads false on it and means nothing there.
	dlBase := http.DefaultTransport.(*http.Transport).Clone()
	dlBase.ResponseHeaderTimeout = 10 * time.Second
	return &HTTPSource{
		baseAPI: baseAPI,
		baseDL:  baseDL,
		// The list call gets a hard 10s timeout: it is a small JSON
		// response and a hang here should fail fast.
		list:   &http.Client{Timeout: 10 * time.Second},
		dl:     &http.Client{Transport: dlBase},
		dlBase: dlBase,
	}
}

// Logging renders this rung's traffic to out at lvl, the way every
// API client in this CLI answers --verbose and --debug (#402). The
// download client is capped at Verbose whatever lvl says: a release
// archive is megabytes of binary, and dumping it would bury the one
// line --debug was turned on to read. It returns s for chaining.
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
// A 404 reads as "repository not found or private" — the release
// list may simply be behind auth, never "no releases exist" — and
// the Chain relies on that distinction to fall through to gh rather
// than reporting a real "nothing to update" state.
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
		// A truncated body must leave NOTHING behind. The gh rung is
		// tried next on this same path, and `gh release download`
		// refuses to overwrite an existing file, so a partial write
		// here would turn a recoverable failure into a dead ladder.
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
