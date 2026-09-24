package selfupdate

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	rekorpb "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

const (
	// rekorPublicBaseURL is the public-good transparency log cosign
	// uploads to when it signs keylessly, so it is where the entry for
	// our checksums.txt signature lives.
	rekorPublicBaseURL = "https://rekor.sigstore.dev"

	// ctLogThreshold requires one verified SignedCertificate Timestamp,
	// matching `cosign verify-blob`'s default.
	ctLogThreshold = 1

	// maxRekorEntries bounds how many log entries one artifact digest
	// may drag in. Anyone can sign our checksums.txt and log it, so the
	// index answer is attacker-influenced and must not be unbounded.
	// 32 is sigstore-go's own cap, so a smaller one here would only
	// hide entries the verifier would have accepted.
	maxRekorEntries = 32

	// maxRekorResponseBytes caps a single Rekor reply. The log is a
	// third party on the update path; a reply that runs forever must
	// not be able to exhaust memory instead of failing.
	maxRekorResponseBytes = 1 << 20

	rekorTimeout = 30 * time.Second
)

// tlogFinder returns the transparency-log entries that record OUR
// signature over an artifact digest. The signature is a parameter
// because Rekor indexes by digest alone: a lookup answer is a mix of
// everyone who ever signed those bytes, and only ours is evidence.
type tlogFinder interface {
	Entries(digest, signature []byte) ([]*tlog.Entry, error)
}

// TrustedMaterial is everything VerifySignature needs beyond the files
// a release ships: the Sigstore roots the certificate must chain to,
// and the transparency-log lookup that dates it.
//
// The log lookup belongs here rather than inside VerifySignature
// because it is the other half of the same trust decision — swapping
// in a test CA is only hermetic if the log it consults is swapped with
// it.
type TrustedMaterial struct {
	roots root.TrustedMaterial
	tlog  tlogFinder

	// skipSCT opts OUT of certificate-transparency verification, and
	// exists only for tests: the ephemeral CA they sign with embeds no
	// SCT. Strict is the ZERO VALUE, so a TrustedMaterial built
	// anywhere in this package verifies SCTs unless someone wrote the
	// opt-out down.
	skipSCT bool
}

// SigstoreCacheDir is where the refreshed TUF trust root is cached.
//
// sigstore-go's own default is ~/.sigstore/root, which this CLI has
// no business writing: ~/.pgedge/cli is the only directory it owns
// (#393), and a directory a user has never heard of appearing in
// their home is a surprise a verification step should not spring.
func SigstoreCacheDir() (string, error) {
	dir, err := config.DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sigstore"), nil
}

// sigstoreTUFOptions is sigstore-go's public-good TUF configuration
// with only the cache location changed.
func sigstoreTUFOptions() (*tuf.Options, error) {
	dir, err := SigstoreCacheDir()
	if err != nil {
		return nil, err
	}
	return tuf.DefaultOptions().WithCachePath(dir), nil
}

// fetchTrustedRoot is root.FetchTrustedRootWithOptions, indirected so
// a test can prove the options actually reach sigstore-go. The
// options ARE the fix here: a call site that dropped them and went
// back to root.FetchTrustedRoot() would silently cache under
// ~/.sigstore again while every test of the path helpers still
// passed.
var fetchTrustedRoot = root.FetchTrustedRootWithOptions

// ProductionTrustedMaterial loads the public-good Sigstore trust root
// (sigstore-go's embedded TUF root, refreshed from the CDN and cached
// under SigstoreCacheDir) and points the log lookup at public Rekor.
func ProductionTrustedMaterial() (*TrustedMaterial, error) {
	opts, err := sigstoreTUFOptions()
	if err != nil {
		return nil, fmt.Errorf("load sigstore trust root: %w", err)
	}

	roots, err := fetchTrustedRoot(opts)
	if err != nil {
		return nil, fmt.Errorf("load sigstore trust root: %w", err)
	}

	return &TrustedMaterial{
		roots: roots,
		tlog: &rekorFinder{
			baseURL: rekorPublicBaseURL,
			client:  &http.Client{Timeout: rekorTimeout},
		},
	}, nil
}

// NewTestTrustedMaterial builds a TrustedMaterial from a caller-minted
// Sigstore root (e.g. sigstore-go's ca.NewVirtualSigstore) and a fixed
// transparency-log answer, for command-level tests outside this
// package that need to drive VerifySignature end to end without
// dialing public Sigstore infrastructure — every in-package fixture
// does the same thing with the unexported literal directly; this is
// that literal's only door for a caller that cannot reach it.
//
// SCT verification is skipped, matching every such fixture: an
// ephemeral CA embeds no SCT.
func NewTestTrustedMaterial(
	roots root.TrustedMaterial, entries []*tlog.Entry,
) *TrustedMaterial {
	return &TrustedMaterial{
		roots:   roots,
		tlog:    fixedTlog{entries: entries},
		skipSCT: true,
	}
}

// fixedTlog answers every Entries lookup with the same fixed list,
// ignoring the digest and signature it is asked about — the caller
// already knows which entries belong to its own fixture signature.
type fixedTlog struct{ entries []*tlog.Entry }

func (f fixedTlog) Entries(_, _ []byte) ([]*tlog.Entry, error) {
	return f.entries, nil
}

// rekorFinder reads Rekor's v1 REST API directly. cosign resolves a
// detached signature the same way — the release ships only the
// certificate and the signature, so the log entry that proves WHEN the
// (ten-minute) certificate was used has to be fetched.
type rekorFinder struct {
	baseURL string
	client  *http.Client
}

func (r *rekorFinder) Entries(digest, signature []byte) (
	[]*tlog.Entry, error,
) {
	uuids, err := r.search(digest)
	if err != nil {
		return nil, err
	}

	if len(uuids) > maxRekorEntries {
		uuids = uuids[:maxRekorEntries]
	}

	entries := make([]*tlog.Entry, 0, len(uuids))

	var skipped error

	for _, uuid := range uuids {
		entry, err := r.entry(uuid)
		if err != nil {
			// One unreadable entry is not a verdict. Everything in the
			// index answer but ours was put there by someone else, so a
			// planted entry that 404s or fails to parse must not be able
			// to stop the lookup reaching ours.
			skipped = err

			continue
		}

		if bytes.Equal(entry.Signature(), signature) {
			entries = append(entries, entry)
		}
	}

	// Surviving nothing after a failure is a broken log, not a bad
	// signature. Returning an empty slice would reach the user as
	// "not enough verified log entries" — tampering language for what
	// is usually a Rekor outage. Fail-closed either way.
	if len(entries) == 0 && skipped != nil {
		return nil, fmt.Errorf("transparency log lookup failed: %w", skipped)
	}

	return entries, nil
}

func (r *rekorFinder) search(digest []byte) ([]string, error) {
	query, err := json.Marshal(map[string]string{
		"hash": "sha256:" + hex.EncodeToString(digest),
	})
	if err != nil {
		return nil, err
	}

	body, err := r.post("/api/v1/index/retrieve", query)
	if err != nil {
		return nil, err
	}

	var uuids []string
	if err := json.Unmarshal(body, &uuids); err != nil {
		return nil, fmt.Errorf("decode transparency log index: %w", err)
	}

	return uuids, nil
}

// rekorLogEntry is the subset of Rekor's LogEntry a verifier needs.
type rekorLogEntry struct {
	Body           string `json:"body"`
	IntegratedTime int64  `json:"integratedTime"`
	LogID          string `json:"logID"`
	LogIndex       int64  `json:"logIndex"`
	Verification   struct {
		SignedEntryTimestamp string `json:"signedEntryTimestamp"`
		InclusionProof       *struct {
			Checkpoint string   `json:"checkpoint"`
			Hashes     []string `json:"hashes"`
			LogIndex   int64    `json:"logIndex"`
			RootHash   string   `json:"rootHash"`
			TreeSize   int64    `json:"treeSize"`
		} `json:"inclusionProof"`
	} `json:"verification"`
}

func (r *rekorFinder) entry(uuid string) (*tlog.Entry, error) {
	body, err := r.get("/api/v1/log/entries/" + url.PathEscape(uuid))
	if err != nil {
		return nil, err
	}

	var byUUID map[string]rekorLogEntry
	if err := json.Unmarshal(body, &byUUID); err != nil {
		return nil, fmt.Errorf("decode transparency log entry: %w", err)
	}

	for _, e := range byUUID {
		return e.toTlogEntry()
	}

	return nil, fmt.Errorf("transparency log entry %s is empty", uuid)
}

func (e rekorLogEntry) toTlogEntry() (*tlog.Entry, error) {
	canonical, err := base64.StdEncoding.DecodeString(e.Body)
	if err != nil {
		return nil, fmt.Errorf("decode transparency log body: %w", err)
	}

	var kv struct {
		Kind    string `json:"kind"`
		Version string `json:"apiVersion"`
	}
	if err := json.Unmarshal(canonical, &kv); err != nil {
		return nil, fmt.Errorf("decode transparency log body: %w", err)
	}

	logID, err := hex.DecodeString(e.LogID)
	if err != nil {
		return nil, fmt.Errorf("decode transparency log id: %w", err)
	}

	set, err := base64.StdEncoding.DecodeString(
		e.Verification.SignedEntryTimestamp)
	if err != nil {
		return nil, fmt.Errorf("decode signed entry timestamp: %w", err)
	}

	tle := &rekorpb.TransparencyLogEntry{
		LogIndex:          e.LogIndex,
		LogId:             &protocommon.LogId{KeyId: logID},
		KindVersion:       &rekorpb.KindVersion{Kind: kv.Kind, Version: kv.Version},
		IntegratedTime:    e.IntegratedTime,
		InclusionPromise:  &rekorpb.InclusionPromise{SignedEntryTimestamp: set},
		CanonicalizedBody: canonical,
	}

	if p := e.Verification.InclusionProof; p != nil {
		rootHash, err := hex.DecodeString(p.RootHash)
		if err != nil {
			return nil, fmt.Errorf("decode inclusion proof root: %w", err)
		}

		hashes := make([][]byte, len(p.Hashes))
		for i, h := range p.Hashes {
			if hashes[i], err = hex.DecodeString(h); err != nil {
				return nil, fmt.Errorf("decode inclusion proof: %w", err)
			}
		}

		// The proof's own LogIndex is its position within the tree the
		// checkpoint describes, which is NOT the entry's global log
		// index; carrying the wrong one over makes the proof fail.
		tle.InclusionProof = &rekorpb.InclusionProof{
			LogIndex:   p.LogIndex,
			RootHash:   rootHash,
			TreeSize:   p.TreeSize,
			Hashes:     hashes,
			Checkpoint: &rekorpb.Checkpoint{Envelope: p.Checkpoint},
		}
	}

	return tlog.ParseTransparencyLogEntry(tle)
}

func (r *rekorFinder) get(path string) ([]byte, error) {
	resp, err := r.client.Get(r.baseURL + path) //nolint:noctx // client carries a timeout.
	if err != nil {
		return nil, fmt.Errorf("query transparency log: %w", err)
	}

	return readRekorResponse(resp)
}

func (r *rekorFinder) post(path string, body []byte) ([]byte, error) {
	resp, err := r.client.Post( //nolint:noctx // client carries a timeout.
		r.baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("query transparency log: %w", err)
	}

	return readRekorResponse(resp)
}

func readRekorResponse(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"transparency log returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRekorResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read transparency log response: %w", err)
	}

	return body, nil
}
