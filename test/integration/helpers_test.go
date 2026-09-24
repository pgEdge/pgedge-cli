// Package integration drives the built pgedge binary against a live
// API. It is skipped unless PGEDGE_INTEGRATION=1, so `make test`
// stays hermetic.
package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const defaultProfile = "dev"

var (
	buildOnce   sync.Once
	binPath     string
	buildErr    error
	buildStderr string
)

// requireIntegration skips unless the suite is explicitly enabled.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("PGEDGE_INTEGRATION") != "1" {
		t.Skip("set PGEDGE_INTEGRATION=1 to run integration tests")
	}
}

// profile returns the config profile to drive the CLI with.
func profile() string {
	if p := os.Getenv("PGEDGE_INTEGRATION_PROFILE"); p != "" {
		return p
	}
	return defaultProfile
}

// TestMain builds the CLI once for the whole package run (via
// binary/buildOnce) and removes its temp directory afterward. Cleanup
// lives here rather than on a per-test t.Cleanup because buildOnce
// shares binPath across every test in the package: registering
// cleanup inside the Do closure would tie the directory's lifetime to
// whichever test happened to trigger the build first, and later tests
// would then run against a binary that had already been removed.
func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		// Best-effort cleanup: a leftover temp dir is a nuisance, not
		// a test failure, so its removal error is deliberately
		// discarded.
		_ = os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

// binary builds the CLI once per run and returns its path. Building
// rather than using an installed pgedge guarantees the suite tests
// the working tree.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pgedge-integration")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "pgedge")
		cmd := exec.Command("go", "build", "-o", binPath,
			"./cmd/pgedge")
		cmd.Dir = "../.."
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildErr = err
			buildStderr = stderr.String()
		}
	})
	if buildErr != nil {
		t.Fatalf("build pgedge: %v\n%s", buildErr, buildStderr)
	}
	return binPath
}

// runJSON runs the CLI with JSON output and returns stdout. A
// non-zero exit fails the test with both streams attached.
func runJSON(t *testing.T, args ...string) []byte {
	t.Helper()
	full := append([]string{
		"--profile", profile(), "-o", "json",
	}, args...)

	cmd := exec.Command(binary(t), full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("pgedge %s: %v\nstdout: %s\nstderr: %s",
			strings.Join(full, " "), err,
			stdout.String(), stderr.String())
	}
	return stdout.Bytes()
}

// listOf runs a list verb and decodes it as a JSON array of objects.
func listOf(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	out := runJSON(t, args...)
	var items []map[string]any
	if err := json.Unmarshal(out, &items); err != nil {
		t.Fatalf("pgedge %s: decode list: %v\nbody: %s",
			strings.Join(args, " "), err, truncate(out))
	}
	return items
}

// assertNonEmpty checks that each named key is present, and that its
// value is not the zero value for the types where zero is meaningful.
// Strings must be non-empty and numbers non-zero; booleans and
// composite values are only checked for presence, since false and
// empty collections are legitimate. This closes the one gap in relying
// on the typed client as a regression detector: oapi-codegen renders
// required fields as non-pointer types and encoding/json does not error
// on a missing field, so a silently dropped field would otherwise
// surface as a blank column rather than a failure.
func assertNonEmpty(t *testing.T, what string,
	obj map[string]any, keys []string,
) {
	t.Helper()
	for _, k := range keys {
		v, ok := obj[k]
		if !ok {
			t.Errorf("%s: response is missing key %q", what, k)
			continue
		}
		switch tv := v.(type) {
		case string:
			if tv == "" {
				t.Errorf("%s: key %q is empty", what, k)
			}
		case float64:
			// encoding/json decodes every JSON number into float64.
			if tv == 0 {
				t.Errorf("%s: key %q is zero", what, k)
			}
		case bool:
			// false is a legitimate value for most flags (e.g.
			// deletion_protection), so presence — already checked
			// above — is all that can be asserted here.
		case nil:
			t.Errorf("%s: key %q is null", what, k)
		default:
			// After encoding/json, only []any and map[string]any can
			// reach here. For composite values, presence — checked
			// above — is the only meaningful assertion.
		}
	}
}

func truncate(b []byte) string {
	const maxLen = 400
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
