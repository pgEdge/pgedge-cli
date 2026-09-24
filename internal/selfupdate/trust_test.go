package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return body
}

// TestSigstoreCacheDirIsUnderTheCLICache pins where the TUF trust
// root is cached. sigstore-go's default is ~/.sigstore/root; this CLI
// writes only inside ~/.pgedge/cli, so the option that moves
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
