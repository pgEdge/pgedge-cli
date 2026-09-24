package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryTestIsolatesHome is this package's isolation gate. This
// package's tests read and write real files under $HOME (DefaultPath,
// Load/Save with no explicit path) — Load and
// ResolveProfile read no environment variable, so HOME
// is the only steering variable left to isolate a test from a
// developer's real ~/.pgedge/cli/config.yaml. A test that forgets to pin
// HOME to a temp dir would read, and in some cases overwrite, that real
// file, and CI would never catch it because CI has no real config to
// destroy.
//
// The rule: every test function's first two statements must include a
// t.Setenv("HOME", ...) call — either as the very first statement
// (t.Setenv("HOME", t.TempDir())) or as the second, when the first
// statement builds the temp dir first (home := t.TempDir() then
// t.Setenv("HOME", home)). A small number of tests deliberately set
// HOME to "" to exercise DefaultPath's unresolvable-
// HOME error path — t.Setenv("HOME", "") satisfies the same rule, since
// it is still the isolating call, just pointed at an intentionally
// broken value instead of a temp dir.
//
// It checks the first TWO statements, not merely "somewhere in the
// function": a HOME pin that runs after the test has already touched
// the filesystem or read a stale value isolates nothing, and allowing
// an unbounded search would let that ordering bug hide anywhere in a
// long test body.
//
// This is internal/config's own package-local version of the isolation
// gate every other package's tests get from testsupport.ClearEnv —
// internal/testsupport imports internal/config, so importing it back
// from this internal `package config` test file would be an import
// cycle. The predecessor of this gate (TestEveryTestIsolatesConfigEnv)
// also required PGEDGE_CONFIG/PGEDGE_PROFILE clears; both variables
// stopped being read once config.Load and Config.ResolveProfile moved
// to flags-and-profile-only resolution, so this version narrows the
// rule to what production code actually reads today: HOME.
func TestEveryTestIsolatesHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test sources: %v", err)
	}
	// Positive control 1: an empty glob, or a glob that silently
	// narrowed to one file, means the scan is broken rather than the
	// tests — this package ships config_test.go and write_test.go (and
	// now this file), so anything under 3 is suspect.
	if len(files) < 3 {
		t.Fatalf("globbed %v; this package has more test files than "+
			"that, so the scan is broken rather than the tests", files)
	}

	var checked int
	for _, file := range files {
		raw, err := os.ReadFile(file) //nolint:gosec // G304: glob of this package's own sources
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		lines := strings.Split(string(raw), "\n")

		var inFile int
		for i, line := range lines {
			name, ok := testFuncName(line)
			if !ok {
				continue
			}
			inFile++
			stmts, lineNos := firstStatements(lines, i+1, 2)
			if !anyIsolatesHome(stmts) {
				at := i + 2
				if len(lineNos) > 0 {
					at = lineNos[0]
				}
				t.Errorf("%s:%d: %s's first two statements are %v; "+
					"every test in this package must call "+
					"t.Setenv(\"HOME\", ...) as one of its first two "+
					"statements — see TestEveryTestIsolatesHome's comment",
					file, at, name, stmts)
			}
		}
		// Positive control 2, per file so a file the line scan cannot
		// see is named rather than averaged away by its neighbours: a
		// scan that silently stopped matching `func Test` declarations
		// would report zero violations and look identical to a clean
		// pass.
		if got := strings.Count(string(raw), "\nfunc Test"); got != inFile {
			t.Errorf("%s: scanned %d test functions but the file "+
				"declares %d", file, inFile, got)
		}
		checked += inFile
	}
	if checked == 0 {
		t.Fatal("found no test functions in any *_test.go; the source " +
			"scan is broken, not the tests")
	}
}

// testFuncName returns the name of the test function a line declares.
func testFuncName(line string) (string, bool) {
	const prefix = "func Test"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	name := line[len("func "):]
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
	}
	return name, true
}

// firstStatements returns up to n non-blank, non-comment lines at or
// after from, with their 1-based line numbers.
func firstStatements(lines []string, from, n int) (stmts []string, lineNos []int) {
	for i := from; i < len(lines) && len(stmts) < n; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		stmts = append(stmts, lines[i])
		lineNos = append(lineNos, i+1)
	}
	return stmts, lineNos
}

// anyIsolatesHome reports whether any of the given statement lines is
// the HOME-isolating call: t.Setenv("HOME", ...), for any value,
// including an explicit "" used to force the unresolvable-HOME error
// path.
func anyIsolatesHome(stmts []string) bool {
	for _, s := range stmts {
		if strings.Contains(s, `t.Setenv("HOME"`) {
			return true
		}
	}
	return false
}
