package selfupdate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

// The fixtures are the real Rekor answers for v0.5.0-alpha.2's
// checksums.txt, captured on 2026-08-26. A log entry is immutable, so
// they cannot go stale; they are here because the JSON shape Rekor
// returns is the one thing about this client no unit test could
// otherwise pin.
const (
	alpha2Digest = "95af530d31add3ab3a58ccd599ee93baa" +
		"28f2eac138219a23e81543237af4d72"
	alpha2UUID = "108e9186e8c5677ad55bde126e458e5b" +
		"bc82b7f0e07dd042d186c3c8a508aecc631234f00ec54735"
	alpha2Signature = "MEQCICBZe1lvo+iYmfOZ5EiUzCK0dVDdAt756SKEFeVMVgcn" +
		"AiBds6jVxESwHoWzuxGqf8Xc7UBVL+LR5DcY+3VcNzBmKg=="
	alpha2LogIndex       = 2582067347
	alpha2IntegratedTime = 1787609913
	alpha2ProofLogIndex  = 2460163085
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return body
}

// rekorStub serves index and entry responses, recording the paths it
// was asked for. byUUID, when set for a UUID, overrides entry for that
// one fetch; a nil body there means the fetch 404s.
type rekorStub struct {
	index  []byte
	entry  []byte
	byUUID map[string][]byte
	status int
	paths  []string
	// raw holds r.RequestURI. r.URL.Path arrives DECODED, so it shows
	// the same thing whether or not the UUID was escaped; only the raw
	// request line can tell those two apart.
	raw []string
}

func (s *rekorStub) start(t *testing.T) *rekorFinder {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			s.paths = append(s.paths, r.URL.Path)
			s.raw = append(s.raw, r.RequestURI)

			if s.status != 0 {
				w.WriteHeader(s.status)

				return
			}

			if strings.HasPrefix(r.URL.Path, "/api/v1/index") {
				_, _ = w.Write(s.index)

				return
			}

			uuid := path.Base(r.URL.Path)
			if body, ok := s.byUUID[uuid]; ok {
				if body == nil {
					w.WriteHeader(http.StatusNotFound)

					return
				}

				_, _ = w.Write(body)

				return
			}

			_, _ = w.Write(s.entry)
		}))
	t.Cleanup(srv.Close)

	return &rekorFinder{baseURL: srv.URL, client: srv.Client()}
}

// alpha2Sig is the release's signature, the one a lookup must select.
func alpha2Sig(t *testing.T) []byte {
	t.Helper()

	sig, err := base64.StdEncoding.DecodeString(alpha2Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	return sig
}

// strangerEntry is a log entry for the same digest signed by somebody
// else: the shape a Rekor index answer is full of, since anyone may
// sign our checksums.txt and log it. It has to be a REAL signature
// from a REAL key — parsing a hashedrekord verifies the signature
// against the digest, so a fabricated one would be skipped as
// unreadable and would never reach the selection this exercises.
func strangerEntry(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("stranger key: %v", err)
	}

	der, err := x509.CreateCertificate(rand.Reader,
		&x509.Certificate{SerialNumber: big.NewInt(1)},
		&x509.Certificate{SerialNumber: big.NewInt(1)},
		&key.PublicKey, key)
	if err != nil {
		t.Fatalf("stranger certificate: %v", err)
	}

	digest, err := hex.DecodeString(alpha2Digest)
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}

	sig, err := ecdsa.SignASN1(rand.Reader, key, digest)
	if err != nil {
		t.Fatalf("stranger signature: %v", err)
	}

	certPEM := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der})

	return rewriteFixtureSignature(t,
		base64.StdEncoding.EncodeToString(sig),
		base64.StdEncoding.EncodeToString(certPEM))
}

// rewriteFixtureSignature swaps the signature and certificate in the
// captured entry, leaving everything else — including the digest it is
// indexed under — exactly as Rekor returned it.
func rewriteFixtureSignature(t *testing.T, sig, cert string) []byte {
	t.Helper()

	var byUUID map[string]map[string]any
	if err := json.Unmarshal(fixture(t, "rekor-entry.json"), &byUUID); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for _, e := range byUUID {
		encoded, ok := e["body"].(string)
		if !ok {
			t.Fatal("fixture entry has no body")
		}

		body, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode body: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		spec, _ := parsed["spec"].(map[string]any)

		spec["signature"] = map[string]any{
			"content":   sig,
			"publicKey": map[string]any{"content": cert},
		}

		rewritten, err := json.Marshal(parsed)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		e["body"] = base64.StdEncoding.EncodeToString(rewritten)
	}

	out, err := json.Marshal(byUUID)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	return out
}

func TestRekorFinderParsesARealEntry(t *testing.T) {
	stub := &rekorStub{
		index: fixture(t, "rekor-index.json"),
		entry: fixture(t, "rekor-entry.json"),
	}
	finder := stub.start(t)

	digest, err := hex.DecodeString(alpha2Digest)
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}

	entries, err := finder.Entries(digest, alpha2Sig(t))
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	entry := entries[0]

	if got := entry.LogIndex(); got != alpha2LogIndex {
		t.Errorf("log index = %d, want %d", got, alpha2LogIndex)
	}

	if got := entry.IntegratedTime().Unix(); got != alpha2IntegratedTime {
		t.Errorf("integrated time = %d, want %d",
			got, alpha2IntegratedTime)
	}

	if !entry.HasInclusionPromise() {
		t.Error("entry carries no inclusion promise")
	}

	if !entry.HasInclusionProof() {
		t.Error("entry carries no inclusion proof")
	}

	// The proof's index is its position in the tree the checkpoint
	// describes, not the entry's global index. Carrying the entry's
	// index over instead makes the proof fail, so pin them apart.
	proofIndex := entry.TransparencyLogEntry().GetInclusionProof().GetLogIndex()
	if proofIndex != alpha2ProofLogIndex {
		t.Errorf("inclusion proof index = %d, want %d",
			proofIndex, alpha2ProofLogIndex)
	}

	want, err := base64.StdEncoding.DecodeString(alpha2Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	if string(entry.Signature()) != string(want) {
		t.Error("entry signature does not match the release's .sig")
	}

	if len(stub.paths) != 2 {
		t.Fatalf("requested %v, want an index then an entry", stub.paths)
	}

	if !strings.Contains(stub.paths[1], alpha2UUID) {
		t.Errorf("entry fetched from %q, want the indexed UUID",
			stub.paths[1])
	}
}

func TestRekorFinderCapsFetches(t *testing.T) {
	uuids := make([]string, 0, maxRekorEntries+3)
	for range maxRekorEntries + 3 {
		uuids = append(uuids, `"`+alpha2UUID+`"`)
	}

	stub := &rekorStub{
		index: []byte("[" + strings.Join(uuids, ",") + "]"),
		entry: fixture(t, "rekor-entry.json"),
	}
	finder := stub.start(t)

	if _, err := finder.Entries([]byte("digest"), alpha2Sig(t)); err != nil {
		t.Fatalf("Entries: %v", err)
	}

	// One index request plus at most maxRekorEntries entry requests.
	if got := len(stub.paths) - 1; got != maxRekorEntries {
		t.Errorf("fetched %d entries, want the cap of %d",
			got, maxRekorEntries)
	}
}

// Ours arriving last in a crowded index must still be found: the cap
// is on how many are FETCHED, and it sits at sigstore-go's own 32, so
// a stranger cannot push ours out of the window by signing first.
func TestRekorFinderFindsOursAfterStrangers(t *testing.T) {
	const strangers = 9

	uuids := make([]string, 0, strangers+1)
	byUUID := map[string][]byte{}

	for i := range strangers {
		uuid := fmt.Sprintf("stranger%02d", i)
		uuids = append(uuids, `"`+uuid+`"`)
		byUUID[uuid] = strangerEntry(t)
	}

	uuids = append(uuids, `"`+alpha2UUID+`"`)

	stub := &rekorStub{
		index:  []byte("[" + strings.Join(uuids, ",") + "]"),
		entry:  fixture(t, "rekor-entry.json"),
		byUUID: byUUID,
	}
	finder := stub.start(t)

	entries, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want only ours", len(entries))
	}

	if entries[0].LogIndex() != alpha2LogIndex {
		t.Errorf("selected the wrong entry: log index %d",
			entries[0].LogIndex())
	}
}

// One unreadable entry in the answer is not a verdict. Everything but
// ours was put there by somebody else, so a planted 404 must not be
// able to stop the lookup reaching ours.
func TestRekorFinderSkipsUnreadableEntries(t *testing.T) {
	byUUID := map[string][]byte{
		"missing": nil,
		"garbage": []byte("{"),
	}

	stub := &rekorStub{
		index:  []byte(`["missing","garbage","` + alpha2UUID + `"]`),
		entry:  fixture(t, "rekor-entry.json"),
		byUUID: byUUID,
	}
	finder := stub.start(t)

	entries, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want ours to have survived", len(entries))
	}
}

// Surviving nothing with no failure along the way is not an outage:
// the answer was readable and simply held nobody's signature but a
// stranger's. Empty and no error; the caller's threshold fails closed.
func TestRekorFinderReturnsNothingWhenOnlyStrangersRemain(t *testing.T) {
	stub := &rekorStub{
		index:  []byte(`["stranger"]`),
		entry:  fixture(t, "rekor-entry.json"),
		byUUID: map[string][]byte{"stranger": strangerEntry(t)},
	}
	finder := stub.start(t)

	entries, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("got %d entries, want none", len(entries))
	}
}

// But surviving nothing BECAUSE every fetch failed is an outage, and
// must say so. Left as an empty answer it reaches the user as "not
// enough verified log entries" — tampering language for a log having
// a bad day.
func TestRekorFinderReportsAnOutageWhenEveryFetchFails(t *testing.T) {
	stub := &rekorStub{
		index:  fixture(t, "rekor-index.json"),
		byUUID: map[string][]byte{alpha2UUID: nil},
	}
	finder := stub.start(t)

	_, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err == nil {
		t.Fatal("Entries reported an outage as an empty answer")
	}

	if !strings.Contains(err.Error(), "transparency log") {
		t.Errorf("error does not name the transparency log: %v", err)
	}

	for _, word := range []string{"signature", "verif"} {
		if strings.Contains(err.Error(), word) {
			t.Errorf("outage error uses %q, which reads as tampering: %v",
				word, err)
		}
	}
}

func TestRekorFinderReportsHTTPFailure(t *testing.T) {
	stub := &rekorStub{status: http.StatusBadGateway}
	finder := stub.start(t)

	_, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err == nil {
		t.Fatal("Entries ignored a 502")
	}

	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error does not carry the status: %v", err)
	}
}

func TestRekorFinderReportsUnreachableLog(t *testing.T) {
	finder := &rekorFinder{
		baseURL: "http://127.0.0.1:1",
		client:  http.DefaultClient,
	}

	if _, err := finder.Entries([]byte("digest"), nil); err == nil {
		t.Fatal("Entries ignored an unreachable log")
	}
}

func TestRekorFinderRejectsAMalformedIndex(t *testing.T) {
	stub := &rekorStub{
		index: []byte("{"),
		entry: fixture(t, "rekor-entry.json"),
	}
	finder := stub.start(t)

	if _, err := finder.Entries([]byte("digest"), nil); err == nil {
		t.Fatal("Entries accepted a malformed index")
	}
}

// Each malformation below is the ONLY entry in the answer, so nothing
// survives and the lookup reports the log as unusable rather than
// returning an empty slice that would read as a bad signature. That
// they are SKIPPED rather than fatal is what
// TestRekorFinderSkipsUnreadableEntries proves, where ours survives
// beside them.
func TestRekorFinderReportsWhenEveryEntryIsUnreadable(t *testing.T) {
	cases := map[string][]byte{
		"entry is not JSON":    []byte("{"),
		"entry is empty":       []byte("{}"),
		"body is not base64":   []byte(`{"x":{"body":"!!","logID":"00"}}`),
		"body is not an entry": []byte(`{"x":{"body":"bm90IGpzb24=","logID":"00"}}`),
		"log id is not hex":    []byte(`{"x":{"body":"e30=","logID":"zz"}}`),
	}

	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &rekorStub{
				index: fixture(t, "rekor-index.json"),
				entry: entry,
			}
			finder := stub.start(t)

			_, err := finder.Entries([]byte("digest"), nil)
			if err == nil {
				t.Fatal("Entries reported an unusable answer as empty")
			}

			if !strings.Contains(err.Error(), "transparency log") {
				t.Errorf("error does not name the transparency log: %v", err)
			}
		})
	}
}

// The UUID reaches the URL escaped, so an index answer cannot steer
// the fetch off the entries path. Asserted on the RAW request line:
// r.URL.Path is decoded by the time a handler sees it, so it reads
// identically with and without the escaping and proves nothing.
func TestRekorFinderEscapesTheUUID(t *testing.T) {
	stub := &rekorStub{
		index: []byte(`["../../evil"]`),
		entry: fixture(t, "rekor-entry.json"),
	}
	finder := stub.start(t)

	// The fixture's signature is not the one asked for, so nothing
	// survives; where the request WENT is the whole point here.
	_, _ = finder.Entries([]byte("digest"), nil)

	if len(stub.raw) != 2 {
		t.Fatalf("requested %v, want an index then an entry", stub.raw)
	}

	rest, found := strings.CutPrefix(
		stub.raw[1], "/api/v1/log/entries/")
	if !found {
		t.Fatalf("entry fetched from %q, outside the entries path",
			stub.raw[1])
	}

	// Escaped, the UUID is one path segment however many slashes the
	// index answer put in it. Unescaped, "../../evil" brings its own.
	if strings.Contains(rest, "/") {
		t.Errorf("UUID spans %d path segments in %q, want 1",
			strings.Count(rest, "/")+1, stub.raw[1])
	}
}

// The entity's inclusion accessors report what its entries carry;
// nothing else in the package reads them, so pin them directly.
func TestBlobEntityReportsInclusion(t *testing.T) {
	stub := &rekorStub{
		index: fixture(t, "rekor-index.json"),
		entry: fixture(t, "rekor-entry.json"),
	}
	finder := stub.start(t)

	entries, err := finder.Entries([]byte("digest"), alpha2Sig(t))
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	full := &blobEntity{entries: entries}
	if !full.HasInclusionPromise() || !full.HasInclusionProof() {
		t.Error("entity does not report its entry's inclusion material")
	}

	empty := &blobEntity{}
	if empty.HasInclusionPromise() || empty.HasInclusionProof() {
		t.Error("entity with no entries claims inclusion material")
	}
}

// TestSigstoreCacheDirIsUnderTheCLICache pins where the TUF trust
// root is cached. sigstore-go's default is ~/.sigstore/root; this CLI
// writes only inside ~/.pgedge/cli (#393), so the option that moves
// it is the claim under test — the fetch itself needs live TUF and is
// not exercised here.
func TestSigstoreCacheDirIsUnderTheCLICache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := SigstoreCacheDir()
	if err != nil {
		t.Fatalf("SigstoreCacheDir: %v", err)
	}

	want := filepath.Join(home, ".pgedge", "cli", "cache", "sigstore")
	if dir != want {
		t.Errorf("SigstoreCacheDir = %q, want %q", dir, want)
	}

	// Deriving a path must not create anything: a run that never
	// verifies anything leaves no directory behind.
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("stat %s: %v, want IsNotExist", dir, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".sigstore")); !os.IsNotExist(statErr) {
		t.Errorf("~/.sigstore exists after deriving the cache dir: %v", statErr)
	}
}

// TestSigstoreTUFOptionsCarryTheCachePath is the other half: the
// derived path has to reach sigstore-go, or the default silently
// wins.
func TestSigstoreTUFOptionsCarryTheCachePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	opts, err := sigstoreTUFOptions()
	if err != nil {
		t.Fatalf("sigstoreTUFOptions: %v", err)
	}

	want := filepath.Join(home, ".pgedge", "cli", "cache", "sigstore")
	if opts.CachePath != want {
		t.Errorf("opts.CachePath = %q, want %q", opts.CachePath, want)
	}
	// Everything else stays sigstore-go's public-good default.
	if opts.RepositoryBaseURL != tuf.DefaultOptions().RepositoryBaseURL {
		t.Errorf("opts.RepositoryBaseURL = %q, want the public-good default",
			opts.RepositoryBaseURL)
	}
}

// TestProductionTrustedMaterialPassesTheCachePath pins the CALL SITE.
// The two tests above prove the path is derived correctly and that the
// options carry it; neither notices a ProductionTrustedMaterial that
// went back to root.FetchTrustedRoot() and cached under ~/.sigstore.
// The seam is stubbed, so no live TUF is reached.
func TestProductionTrustedMaterialPassesTheCachePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var gotOpts *tuf.Options
	original := fetchTrustedRoot
	fetchTrustedRoot = func(opts *tuf.Options) (*root.TrustedRoot, error) {
		gotOpts = opts
		return nil, nil
	}
	t.Cleanup(func() { fetchTrustedRoot = original })

	material, err := ProductionTrustedMaterial()
	if err != nil {
		t.Fatalf("ProductionTrustedMaterial: %v", err)
	}
	if material == nil {
		t.Fatal("ProductionTrustedMaterial returned nil material")
	}
	if gotOpts == nil {
		t.Fatal("the trust root was fetched without options")
	}

	want := filepath.Join(home, ".pgedge", "cli", "cache", "sigstore")
	if gotOpts.CachePath != want {
		t.Errorf("CachePath reaching sigstore-go = %q, want %q",
			gotOpts.CachePath, want)
	}
}

// TestProductionTrustedMaterialReportsAFetchFailure covers the other
// arm of the same call.
func TestProductionTrustedMaterialReportsAFetchFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := fetchTrustedRoot
	fetchTrustedRoot = func(*tuf.Options) (*root.TrustedRoot, error) {
		return nil, errors.New("mirror unreachable")
	}
	t.Cleanup(func() { fetchTrustedRoot = original })

	if _, err := ProductionTrustedMaterial(); err == nil {
		t.Fatal("want an error, got nil")
	}
}
