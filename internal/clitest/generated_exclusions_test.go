package clitest

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The generated API clients are named in two places that must agree:
// .golangci.yml keeps linters off them, and scripts/coverage-gate.sh keeps
// them out of the coverage profile. Neither was tied to the tree, so a
// re-vendoring that moves a package left one rule matching a path that no
// longer exists and three generated packages outside every exclusion.
// These tests make the tree the truth source: a moved package
// fails the build instead of silently changing what is linted and what is
// measured.
//
// linters.exclusions.paths is matched against FILE paths, so the checks
// over it measure a file path too. The semantics were confirmed by
// experiment against the pinned 2.8.0: an unanchored regexp over the
// module-relative, slash-separated path, extending past the directory to
// the base name. Measuring a directory string instead reports a working
// `client\.gen\.go` rule as dead.
//
// The coverage filter is checked differently — by running it and reading
// what survives, rather than by reading the pattern. See
// TestCoverageFilterDropsExactlyTheGeneratedPackages.

const (
	golangciConfigPath  = "../../.golangci.yml"
	coverageGatePath    = "../../scripts/coverage-gate.sh"
	makefilePath        = "../../Makefile"
	goModPath           = "../../go.mod"
	generatedClientFile = "client.gen.go"
)

// goFilesMatching walks the module for .go files whose base name satisfies
// keep, returning module-relative slash paths. One walker serves every
// check here so they cannot disagree about which files exist. It fails on
// an empty result: a walk that finds no work looks exactly like a passing
// assertion.
func goFilesMatching(t *testing.T, what string, keep func(base string) bool) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var found []string
	// fpath, not path: this file imports the path package, and gocritic's
	// importShadow does not reach a closure parameter, so a shadow here
	// would surface only as a confusing type error at the call site.
	err := filepath.WalkDir(root, func(fpath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(fpath) != ".go" || !keep(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, fpath)
		if relErr != nil {
			return relErr
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) == 0 {
		t.Fatalf("found no %s anywhere under %s — the walk found no work, "+
			"so every assertion over it would pass vacuously", what, root)
	}
	sort.Strings(found)
	return found
}

// goFiles is the universe every exclusion pattern is measured against.
func goFiles(t *testing.T) []string {
	t.Helper()
	return goFilesMatching(t, ".go files", func(string) bool { return true })
}

// generatedClients returns the path of every generated client, discovered
// by walking for the file `make generate` writes rather than from a list,
// so discovery cannot disagree with codegen.
func generatedClients(t *testing.T) []string {
	t.Helper()
	return goFilesMatching(t, generatedClientFile, func(base string) bool {
		return base == generatedClientFile
	})
}

// generatedPackages returns the directories holding a generated client.
func generatedPackages(t *testing.T) map[string]struct{} {
	t.Helper()
	pkgs := make(map[string]struct{})
	for _, f := range generatedClients(t) {
		pkgs[path.Dir(f)] = struct{}{}
	}
	return pkgs
}

// modulePath reads the module path from go.mod. Coverage-profile lines
// carry full import paths, so the pattern the gate applies at build time
// sees this prefix — and a pattern anchored with ^ would match every
// profile line here while still satisfying a module-relative check.
func modulePath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read %s: %v", goModPath, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			mod := strings.TrimSpace(rest)
			if mod == "" {
				t.Fatalf("%s declares an empty module path", goModPath)
			}
			return mod
		}
	}
	t.Fatalf("%s has no module line", goModPath)
	return ""
}

// golangciExclusionPaths parses .golangci.yml and returns
// linters.exclusions.paths. Only that key: the sibling `rules` list
// excludes linters per path pattern rather than whole paths, and folding
// the two together would let a narrow per-linter rule satisfy a check
// about blanket exclusion.
func golangciExclusionPaths(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(golangciConfigPath)
	if err != nil {
		t.Fatalf("read %s: %v", golangciConfigPath, err)
	}
	var cfg struct {
		Linters struct {
			Exclusions struct {
				Paths []string `yaml:"paths"`
			} `yaml:"exclusions"`
		} `yaml:"linters"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", golangciConfigPath, err)
	}
	if len(cfg.Linters.Exclusions.Paths) == 0 {
		t.Fatalf("%s declares no linters.exclusions.paths", golangciConfigPath)
	}
	return cfg.Linters.Exclusions.Paths
}

// compilePatterns compiles each pattern, failing by source so a typo in
// one config cannot be reported against another.
func compilePatterns(t *testing.T, source string, patterns []string) []*regexp.Regexp {
	t.Helper()
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("%s: %q is not a valid regexp: %v", source, p, err)
		}
		res = append(res, re)
	}
	return res
}

// TestGolangciExcludesEveryGeneratedClient is the moved-package regression: the
// config named internal/byoc/api, a path the tree lost when the modules
// moved under internal/starfleet, and reached only one of the four clients.
func TestGolangciExcludesEveryGeneratedClient(t *testing.T) {
	patterns := golangciExclusionPaths(t)
	res := compilePatterns(t, golangciConfigPath, patterns)

	for _, client := range generatedClients(t) {
		matched := false
		for _, re := range res {
			if re.MatchString(client) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("no .golangci.yml exclusions.paths pattern matches %q — "+
				"patterns are %v", client, patterns)
		}
	}
}

// TestNoGolangciExclusionReachesHandWrittenCode is the other direction,
// and the reason the exclusions name each client file rather than its
// directory. golangci-lint's own exclusions.generated already suppresses
// issues in a file carrying the "Code generated ... DO NOT EDIT" header —
// measured on 2.8.0 — so a directory-wide rule adds nothing for the
// client and instead un-lints the hand-written types.go and doc.go beside
// it, and every file anyone adds there later.
func TestNoGolangciExclusionReachesHandWrittenCode(t *testing.T) {
	res := compilePatterns(t, golangciConfigPath, golangciExclusionPaths(t))

	for _, f := range goFiles(t) {
		if path.Base(f) == generatedClientFile {
			continue
		}
		for _, re := range res {
			if re.MatchString(f) {
				t.Errorf(".golangci.yml exclusions.paths pattern %q matches "+
					"hand-written file %q — it would never be linted", re, f)
			}
		}
	}
}

// TestEveryGolangciExclusionMatchesSomething catches the other half of
// the drift: a pattern that matches nothing. A dead rule is invisible — lint
// stays green — but whatever it was added to exclude is unprotected, and
// the check above cannot see it.
func TestEveryGolangciExclusionMatchesSomething(t *testing.T) {
	files := goFiles(t)
	for _, p := range golangciExclusionPaths(t) {
		re := compilePatterns(t, golangciConfigPath, []string{p})[0]
		matched := false
		for _, f := range files {
			if re.MatchString(f) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf(".golangci.yml exclusions.paths pattern %q matches no "+
				"file in the tree — it is dead, so the thing it was added "+
				"to exclude is being linted", p)
		}
	}
}

// TestCoverageFilterDropsExactlyTheGeneratedPackages runs the real gate
// over a synthetic profile carrying one line per .go file in the tree and
// compares what survives against the tree. Missing a generated package
// puts the 90% threshold out of reach for reasons no one can fix; keeping
// a hand-written package out silently drops its coverage and flatters the
// number instead. Coverage is excluded per PACKAGE, unlike linting: the
// untested lines that would swamp the threshold are the generated ones,
// and Go reports coverage per package.
//
// This measures the filter's EFFECT, never its spelling — a substring
// heuristic over an unparsed script can only bound one spelling, which is
// what let a second filter drop 461 lines undetected.
//
// The invariant is that the filter must be a function of the IMPORT PATH
// alone. Nothing can prove that from a sample, so the sample is chosen to
// catch the violations that flatter the number: six lines per file over
// the cross-product of statement counts 1, 2 and 9 with both hit counts.
// A real profile's blocks run from one statement to a few dozen, so a
// predicate keyed on "uncovered", on "single-statement" or on any
// threshold up to 9 is sampled. Assert
// whole LINES, not the set of files, or a filter removing one line per
// file is invisible.
//
// Three successively narrower samples were each broken by review: one
// identical line per file, then two lines varying each field
// marginally, then the cross-product of 1 and 2 alone. Power comes from
// the COMBINATIONS sampled, not from each field varying somewhere — the
// last of those missed a filter thresholded at 3 statements because no
// sampled line had three for it to select, which is why 9 is here. A
// predicate keyed on a shape still not sampled — thresholding at 17,
// say — remains invisible, and no finite sample closes that. Widen the
// sample rather than believing the gate proves more than it does.
//
// `go` is stubbed on PATH so `go tool cover -func` returns a fixed passing
// total. The real cover step and the gate's fail-closed contract are
// covered by test/coverage_gate_test.sh; what is under test here is which
// lines reach it.
func TestCoverageFilterDropsExactlyTheGeneratedPackages(t *testing.T) {
	generated := generatedPackages(t)
	files := goFiles(t)
	mod := modulePath(t)
	work := t.TempDir()

	// A profile line is `<import path>/<file>:<block> <stmts> <count>`.
	// The full import path is what the gate sees at build time, and a
	// pattern anchored with ^ would satisfy a module-relative check while
	// filtering none of these.
	var profile strings.Builder
	profile.WriteString("mode: atomic\n")
	wantKept := make(map[string]bool)
	for _, f := range files {
		_, isGenerated := generated[path.Dir(f)]
		for _, block := range []string{
			"1.1,2.2 1 1", "3.4,5.6 1 0",
			"7.7,8.8 2 1", "9.9,10.10 2 0",
			"11.1,12.2 9 1", "13.3,14.4 9 0",
		} {
			line := mod + "/" + f + ":" + block
			profile.WriteString(line + "\n")
			if !isGenerated {
				wantKept[line] = true
			}
		}
	}
	raw := filepath.Join(work, "coverage.raw.out")
	if err := os.WriteFile(raw, []byte(profile.String()), 0o600); err != nil {
		t.Fatalf("write synthetic profile: %v", err)
	}
	filtered := filepath.Join(work, "coverage.out")

	stubGoOnPath(t, work)
	cmd := exec.Command("sh", coverageGatePath, raw, filtered, "0.0")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(work, "bin")+string(os.PathListSeparator)+
			os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", coverageGatePath, err, out)
	}

	body, err := os.ReadFile(filtered)
	if err != nil {
		t.Fatalf("read filtered profile: %v", err)
	}
	survived := make(map[string]bool)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, mod+"/") {
			survived[line] = true
		}
	}
	// Not a vacuity guard — the both-directions comparison below already
	// fails loudly on an empty result. It turns one failure mode into one
	// legible line instead of an error per expected line.
	if len(survived) == 0 {
		t.Fatal("no line survived the filter, so every assertion here " +
			"would pass vacuously")
	}

	for line := range wantKept {
		if !survived[line] {
			t.Errorf("the coverage filter dropped %q — that file is "+
				"hand-written, so its coverage is missing from the gate",
				line)
		}
	}
	for line := range survived {
		if !wantKept[line] {
			t.Errorf("%q survived the coverage filter — it belongs to a "+
				"generated package, so its untested lines count against "+
				"the threshold", line)
		}
	}
}

// stubGoOnPath writes a `go` that prints one passing total, so the gate
// under test reaches its filter without a real cover run.
func stubGoOnPath(t *testing.T, work string) {
	t.Helper()
	bin := filepath.Join(work, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bin, err)
	}
	stub := "#!/bin/sh\nprintf 'total:\\t(statements)\\t100.0%%\\n'\n"
	if err := os.WriteFile(
		filepath.Join(bin, "go"), []byte(stub), 0o700); err != nil {
		t.Fatalf("write go stub: %v", err)
	}
}

// TestMakefileDelegatesTheCoverageFilter holds the shape that makes the
// test above sufficient: one filter, in the script. The Makefile used to
// duplicate it. A reinstated copy there would not fail the effect
// check — that runs the script, not `make test` — so the absence is
// asserted directly.
func TestMakefileDelegatesTheCoverageFilter(t *testing.T) {
	raw, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	recipe := makeRecipe(t, string(raw), "test")

	if !strings.Contains(recipe, "coverage-gate.sh") {
		t.Errorf("the `test` target does not call scripts/coverage-gate.sh, "+
			"so `make test` and CI can gate differently:\n%s", recipe)
	}
	// A tripwire, not a proof: a second filter moved into another script
	// would still pass. It catches the obvious reinstatement, which is
	// the one that actually happened.
	//
	// Matched as a COMMAND TOKEN rather than a substring, and never in a
	// comment: "sed" is inside "used", "based" and "parsed", so a plain
	// recipe comment tripped the substring form and reported a filter
	// the target does not run.
	for _, line := range strings.Split(recipe, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, "\t "), "#") {
			continue
		}
		if m := filterTool.FindStringSubmatch(line); m != nil {
			t.Errorf("the `test` target runs %q — the profile filter belongs "+
				"in scripts/coverage-gate.sh alone:\n%s", m[2], recipe)
		}
	}
}

// filterTool matches a text-filtering command at the start of a line or
// after a shell separator, so it cannot fire on the same word inside a
// longer one.
var filterTool = regexp.MustCompile(
	`(^|[\s|;&(@+-])(grep|egrep|awk|sed|perl|python3?|cut|tr)\b`)

// makeRecipe returns the recipe lines of one Makefile target: the tab-
// indented block after `target:`, stopping at the first line that is
// neither indented nor blank. Blank lines are skipped rather than ending
// the recipe, because make allows them inside one.
//
// Two things that look like the rule are skipped, both of which made an
// earlier version report a confidently wrong diagnosis about a correct
// Makefile: text inside a `define` block, which is data and may contain
// anything (a help block listing `test: run the unit tests` was read as
// the recipe), and a target-specific variable line such as
// `test: export GOFLAGS=...`, which carries no recipe and was reported
// as an empty one.
func makeRecipe(t *testing.T, text, target string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	inDefine := false
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "define "):
			inDefine = true
			continue
		case strings.HasPrefix(line, "endef"):
			inDefine = false
			continue
		case inDefine:
			continue
		}
		if !strings.HasPrefix(line, target+":") {
			continue
		}
		if targetVariable.MatchString(line) {
			continue
		}
		var recipe []string
		for _, l := range lines[i+1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if !strings.HasPrefix(l, "\t") {
				break
			}
			recipe = append(recipe, l)
		}
		if len(recipe) == 0 {
			t.Fatalf("%s: target %q has an empty recipe", makefilePath, target)
		}
		return strings.Join(recipe, "\n")
	}
	t.Fatalf("%s: no target %q", makefilePath, target)
	return ""
}

// targetVariable matches a target-specific variable assignment, which is
// a rule line carrying no recipe.
var targetVariable = regexp.MustCompile(
	`^[^:]+:\s*(export\s+|override\s+)?[A-Za-z_][A-Za-z0-9_]*\s*[:+?]?=`)
