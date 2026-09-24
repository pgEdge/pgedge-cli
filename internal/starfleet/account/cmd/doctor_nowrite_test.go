package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/auth"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// `cloud doctor` must not change the state it reports.
//
// It presents as a read-only diagnostic — its own help says it is
// "safe to run precisely when authentication is broken" — but its
// tenant probe used to resolve through conn.Resolve, so a cold cache
// made doctor mint AND persist a token. checkAuth runs first, so the
// command printed "token expired or missing" and exited having left a
// valid token behind.

// cacheFile is the profile's token cache under the test's isolated
// HOME. Named once so every assertion below agrees on what "the cache"
// means.
func cacheFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(
		os.Getenv("HOME"), ".pgedge", "cli", "cache", "default-starfleet.json")
}

// stubTokenAPI is a stub that answers the token exchange AND the
// tenants call, recording every path it is asked for.
//
// It deliberately serves a WORKING token: the point is that doctor
// could have cached one and did not, which a stub that refused
// authentication could not distinguish from doctor never trying.
func stubTokenAPI(t *testing.T) (url string, paths func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.URL.Path)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(r.URL.Path, "oauth/token"):
				_, _ = io.WriteString(w, testsupport.TokenBody)
			case strings.HasSuffix(r.URL.Path, "/tenants"):
				_, _ = io.WriteString(w,
					`[{"id":"t-1","name":"Acme","plan":"enterprise"}]`)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// TestDoctorDoesNotWriteTheTokenCache is the core of the rule: on a COLD
// cache, doctor authenticates, reports the tenant, and leaves no file
// behind.
//
// The tenant assertion is half the test. Without it the fix could be
// "stop probing", which would pass a no-write check while destroying
// the row's reason for existing (plan gating is why an empty
// byoc list is not a routing failure).
func TestDoctorDoesNotWriteTheTokenCache(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	apiURL, paths := stubTokenAPI(t)

	if _, err := os.Stat(cacheFile(t)); !os.IsNotExist(err) {
		t.Fatalf("cache exists before the run: %v", err)
	}

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	if _, err := os.Stat(cacheFile(t)); !os.IsNotExist(err) {
		t.Errorf("doctor wrote %s; a diagnostic must not change the "+
			"state it reports", cacheFile(t))
	}
	// It really did authenticate — otherwise "no file written" would be
	// satisfied by doing nothing at all.
	var exchanged bool
	for _, p := range paths() {
		if strings.Contains(p, "oauth/token") {
			exchanged = true
		}
	}
	if !exchanged {
		t.Errorf("doctor made no token exchange (paths %v); the "+
			"no-write assertion above proves nothing", paths())
	}
	if !strings.Contains(out.String(), "Acme") {
		t.Errorf("doctor did not report the tenant:\n%s", out.String())
	}
}

// TestDoctorDoesNotEvictAnExistingToken pins a sharper
// consequence: a cached token is bound to the API base URL, so a doctor
// run against a DIFFERENT endpoint used to replace the profile's
// working token with one minted elsewhere.
//
// `doctor --api-url <somewhere-else>` is a natural thing to type when
// working out which endpoint answers, which is what made this worth
// fixing rather than documenting.
func TestDoctorDoesNotEvictAnExistingToken(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	otherURL, _ := stubTokenAPI(t)

	// Seed a healthy token for the profile's own host, which is NOT the
	// host doctor is about to be pointed at.
	const homeURL = "https://api.pgedge.invalid"
	writeAccountTokenBoundTo(
		t, time.Now().Add(time.Hour), homeURL, "id", "secret")
	before, err := os.ReadFile(cacheFile(t))
	if err != nil {
		t.Fatalf("read seeded cache: %v", err)
	}

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       otherURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	after, err := os.ReadFile(cacheFile(t))
	if err != nil {
		t.Fatalf("cache disappeared: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("doctor --api-url <other> rewrote the cache\n"+
			"before: %s\nafter:  %s", before, after)
	}
	// And the surviving token is still the one bound to the profile's
	// own host, not a re-binding to the host doctor dialled.
	var tok auth.CachedToken
	if err := json.Unmarshal(after, &tok); err != nil {
		t.Fatalf("unmarshal cache: %v", err)
	}
	if !tok.MintedBy(homeURL, "id", "secret") {
		t.Error("the cached token is no longer bound to the profile's " +
			"own host")
	}
}

// TestDoctorReusesAWarmCacheWithoutExchanging is the other half of the
// policy: ephemeral affects the MISS path only. A doctor run on a
// healthy profile should cost no HTTP at all beyond the reachability
// probe, because reading the cache changes nothing.
func TestDoctorReusesAWarmCacheWithoutExchanging(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	apiURL, paths := stubTokenAPI(t)
	writeAccountToken(t, time.Now().Add(time.Hour), apiURL)
	beforeInfo, err := os.Stat(cacheFile(t))
	if err != nil {
		t.Fatalf("stat seeded cache: %v", err)
	}

	if err := runDoctor(rt, &conn.Flags{
		APIURL:       apiURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	for _, p := range paths() {
		if strings.Contains(p, "oauth/token") {
			t.Errorf("doctor exchanged a token despite a warm, bound "+
				"cache (paths %v)", paths())
		}
	}
	// And the file was not WRITTEN, which is a stricter claim than
	// "its contents did not change".
	//
	// A byte comparison cannot see this: SaveToken marshals the same
	// auth.CachedToken the seed did, so re-persisting a token loaded
	// from the cache round-trips byte-identically. os.SameFile sees it,
	// because the write is atomic — a temp file renamed into place — so
	// any re-save replaces the inode even when the bytes match.
	//
	// The distinction is the whole point. A future
	// post-migration re-save would put doctor back to writing the cache
	// it reports on, with the contents unchanged and a byte comparison
	// none the wiser.
	//
	// Mode and ModTime are checked as well because SameFile alone would
	// not see a touch-on-read or a permission fix-up: both mutate the
	// file in place and leave the inode untouched. Together the three
	// cover every way the warm path could write.
	afterInfo, err := os.Stat(cacheFile(t))
	if err != nil {
		t.Fatalf("cache disappeared: %v", err)
	}
	switch {
	case !os.SameFile(beforeInfo, afterInfo):
		t.Error("doctor replaced a cache file it only needed to read; " +
			"the warm path must not write")
	case beforeInfo.Mode() != afterInfo.Mode():
		t.Errorf("doctor changed the cache's mode (%v -> %v) on a path "+
			"that only reads", beforeInfo.Mode(), afterInfo.Mode())
	case !beforeInfo.ModTime().Equal(afterInfo.ModTime()):
		t.Error("doctor touched the cache's mtime on a path that only " +
			"reads")
	}
}

// TestOnlyDoctorResolvesEphemerally pins the caller list for the
// non-persisting path.
//
// Ephemeral resolution is a carve-out, not a better default: every
// other command WANTS its token cached, because that is what keeps a
// session to one token exchange instead of one per command. A second
// caller appearing by copy-paste would quietly turn the cache off for
// whatever it is, and the symptom — an extra token exchange per
// invocation — is invisible without watching the wire.
//
// It reads the source rather than reflecting, because "who calls this"
// is not observable at runtime. The check is deliberately over the
// whole repo, so a caller added in another module is caught too.
//
// Scope: it matches `symbol(`, so it catches CALLS. Taking the
// function as a value and invoking it through a variable would slip
// past. That is not the threat being defended against — this exists to
// catch a copy-pasted call site, and nobody reaches for a function
// value by accident.
func TestOnlyDoctorResolvesEphemerally(t *testing.T) {
	// Two symbols, two allowlists. conn.ResolveEphemeral is reached
	// only through newEphemeralAccountClient, which in turn is reached
	// only from checkTenant, so both links need pinning — allowing the
	// resolver alone would let a second command call the constructor.
	//
	// The definition sites are listed too: a file that DECLARES the
	// function necessarily names it, and excluding them by pattern
	// would be a rule that quietly stops matching if either is renamed.
	pins := []struct {
		symbol  string
		allowed map[string]string
	}{
		{
			symbol: "ResolveEphemeral(",
			allowed: map[string]string{
				"internal/starfleet/conn/conn.go":               "declares it",
				"internal/starfleet/account/cmd/client_conn.go": "newEphemeralAccountClient, the only caller",
			},
		},
		{
			symbol: "newEphemeralAccountClient(",
			allowed: map[string]string{
				"internal/starfleet/account/cmd/client_conn.go": "declares it",
				"internal/starfleet/account/cmd/doctor.go":      "checkTenant, the reason the carve-out exists",
			},
		},
	}

	root := repoRoot(t)
	for _, pin := range pins {
		var offenders []string
		err := filepath.WalkDir(root,
			func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					switch d.Name() {
					case ".git", ".claude", "node_modules":
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}
				src, rerr := os.ReadFile(path)
				if rerr != nil {
					return rerr
				}
				if !strings.Contains(string(src), pin.symbol) {
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				if _, ok := pin.allowed[filepath.ToSlash(rel)]; !ok {
					offenders = append(offenders, filepath.ToSlash(rel))
				}
				return nil
			})
		if err != nil {
			t.Fatalf("walk for %s: %v", pin.symbol, err)
		}
		if len(offenders) != 0 {
			t.Errorf("%s appears in %v, which is not in the allowed set "+
				"%v.\nEphemeral resolution turns the token cache OFF "+
				"for that command; every command except doctor should "+
				"cache, or a session costs one token exchange per "+
				"invocation. If this is deliberate, add it here with a "+
				"reason.", pin.symbol, offenders, pin.allowed)
		}

		// Two-way: a stale entry would silently re-admit a real caller
		// under a name nobody rechecked.
		for rel, why := range pin.allowed {
			src, rerr := os.ReadFile(filepath.Join(root, rel))
			if rerr != nil {
				t.Errorf("allowed file %s is unreadable: %v", rel, rerr)
				continue
			}
			if !strings.Contains(string(src), pin.symbol) {
				t.Errorf("%s is allowed to name %s (%s) but no longer "+
					"does — drop the entry", rel, pin.symbol, why)
			}
		}
	}
}

// repoRoot walks up from the test's working directory to the module
// root, so the sweep above covers every package rather than this one.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
