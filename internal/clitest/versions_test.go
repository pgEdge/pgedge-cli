package clitest

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// versionsEnvKeys parses ../../versions.env (repo root relative to this
// package) into the set of KEY names it defines, skipping comments and
// blank lines. It fails the test if the file is missing or a non-comment
// line is not KEY=VALUE — the manifest is a build input, not free text.
func versionsEnvKeys(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "versions.env")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	keys := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("versions.env line is not KEY=VALUE: %q", line)
		}
		keys[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return keys
}

// TestModulesHaveVersionEntry guards independent module versioning: every
// module the binary carries (clitest.Modules, the same set main.go
// registers) must have a <NAME>_VERSION entry in versions.env, so the
// Makefile and .goreleaser.yaml can stamp it. Adding a module without a
// version line — the easy thing to forget — fails the build here.
func TestModulesHaveVersionEntry(t *testing.T) {
	keys := versionsEnvKeys(t)
	for _, m := range Modules() {
		want := strings.ToUpper(m.Name()) + "_VERSION"
		if _, ok := keys[want]; !ok {
			t.Errorf("module %q has no %q in versions.env — add it so "+
				"the module's version is stamped at build/release time",
				m.Name(), want)
		}
	}
}

// TestVersionEntriesAreNonEmpty ensures no *_VERSION entry is blank; an
// empty value would stamp a module with an empty version string.
func TestVersionEntriesAreNonEmpty(t *testing.T) {
	for k, v := range versionsEnvKeys(t) {
		if strings.HasSuffix(k, "_VERSION") && v == "" {
			t.Errorf("versions.env: %s is empty", k)
		}
	}
}
