package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseSpecific are the lines that differ between a snapshot build
// and a release: the version and each archive's checksum and URL.
var releaseSpecific = regexp.MustCompile(`(?m)^(\s*)(version|sha256|url) .*$`)

func mask(cask string) string {
	return releaseSpecific.ReplaceAllString(cask, "$1$2 <masked>")
}

// The tap's cask at da42311 is the hand-fixed one that passed brew
// style and brew audit and installed cleanly. Patching goreleaser's
// output must reproduce it apart from the release-specific lines.
func TestPatchReproducesTheTapCask(t *testing.T) {
	gen, err := os.ReadFile(filepath.Join("testdata", "goreleaser-v2.18.2.rb"))
	if err != nil {
		t.Fatal(err)
	}
	tap, err := os.ReadFile(filepath.Join("testdata", "tap-da42311.rb"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := patch(string(gen))
	if err != nil {
		t.Fatal(err)
	}
	if mask(got) != mask(string(tap)) {
		t.Errorf("patched cask differs from the tap's:\n%s", got)
	}
}

func TestPatchRefuses(t *testing.T) {
	tests := []struct {
		name, cask, want string
	}{
		{"legacy postflight", "cask \"x\" do\n  binary \"x\"\n  postflight do\n  end\nend\n", "postflight stanza on line 3"},
		{"already patched", "cask \"x\" do\n  binary \"x\"\n  postflight_steps do\n  end\nend\n", "postflight_steps stanza"},
		{"preflight", "cask \"x\" do\n  preflight do\n  end\n  binary \"x\"\nend\n", "preflight stanza"},
		{"no artifact", "cask \"x\" do\n  name \"x\"\nend\n", "no binary or completion line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patch(tt.cask)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("patch() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestRunWritesThePatchedCask(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "pgedge.rb")
	if err := run(filepath.Join("testdata", "goreleaser-v2.18.2.rb"), out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), "postflight_steps do") != 1 {
		t.Errorf("want one postflight_steps block, got:\n%s", got)
	}
	if err := run(filepath.Join(dir, "missing.rb"), out); err == nil {
		t.Error("run() on a missing input returned nil")
	}
	if err := run(out, filepath.Join(dir, "again.rb")); err == nil {
		t.Error("run() on an already patched cask returned nil")
	}
}
