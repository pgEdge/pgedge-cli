package testsupport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// exemptEnv names variables production code reads that ClearEnv
// deliberately does NOT clear, each with the reason and, crucially,
// where the isolation actually happens instead. An exemption that does
// not say where the leak is closed is not an exemption, it is a hole
// with a comment on it.
var exemptEnv = map[string]string{
	"HOME": "isolation SETS this (t.Setenv(HOME, t.TempDir()) in " +
		"NewRuntime) rather than clearing it — every config and cache " +
		"path is derived from it, so an empty HOME is not isolation, " +
		"it is a different failure",
}

// TestEnvIsolationCoversProductionReads is the gate the hostEnv list
// never had before this test existed. The list is hand-maintained, and
// its own comment admits that nothing else gates it — a new
// resolution-steering variable could be added to production code and no
// test would say a word. The failure mode is not a red test; it is a
// green suite that quietly reads the developer's shell, and in the
// worst case dials a real API out of `make test`.
//
// It works by parsing production sources for os.Getenv/os.LookupEnv
// calls and resolving the argument, including through a constant —
// internal/cli reads envShell, not a literal, so a scan for string
// literals at call sites finds nothing there. os.UserHomeDir counts as
// a read of HOME. Every name maps to ALL the files that read it, not
// just one: SHELL is read both as envShell's constant (in
// internal/cli/completion.go) and as a bare literal (in
// internal/cli/doctor.go).
//
// A file list alone is not enough to prove constant resolution still
// works: if envShell were inlined back to a literal, the read would
// still be attributed to completion.go (same file, same name), so
// merely requiring "SHELL's file list includes completion.go" would
// not notice the regression. The scanner therefore also returns
// constResolved: names mapped to the files where the *specific* read
// went through the identifier-lookup branch (an os.Getenv(someConst)
// call), never the string-literal branch. Control 2 below asserts
// against constResolved, not found, so it fails the moment
// completion.go's read stops being constant-resolved — regardless of
// what doctor.go does.
//
// Shrinking the list is exactly when a re-added name goes unnoticed:
// deleting the account credential/API-URL set (after the PGEDGE_BYOC_*
// set before it) took the list to hostEnv alone, which makes this gate
// cheaper to satisfy, not less necessary.
func TestEnvIsolationCoversProductionReads(t *testing.T) {
	reads, constResolved := scanProductionEnvReads(t)

	// Positive control 1. A sweep that silently matched nothing would
	// report a clean result identical to a real pass.
	for _, want := range []string{"NO_COLOR", "SHELL"} {
		if _, ok := reads[want]; !ok {
			t.Fatalf("scanner found no read of %s — it is read in "+
				"production code, so the scan is broken, not the "+
				"isolation. Found: %v", want, sortedKeys(reads))
		}
	}
	if len(reads) < 6 {
		t.Fatalf("scanner found only %d env reads (%v); expected the "+
			"full production set — suspect the scan",
			len(reads), sortedKeys(reads))
	}

	// Positive control 2, and the one that actually exercises constant
	// resolution rather than merely a bare literal: SHELL must be
	// CONSTANT-resolved in internal/cli/completion.go specifically —
	// not merely present somewhere in SHELL's file list, which
	// internal/cli/doctor.go's bare literal read would also satisfy.
	// Asserting against constResolved (rather than reads) is what
	// makes inlining envShell back to a literal fail this control:
	// doctor.go's literal read never appears in constResolved, so
	// there is nothing left to quietly stand in for completion.go.
	const wantConstSite = "internal/cli/completion.go"
	if !containsString(constResolved["SHELL"], wantConstSite) {
		t.Fatalf("constResolved[SHELL] = %v; expected it to include %s "+
			"via constant resolution — got only doctor.go's literal read, "+
			"or none at all", constResolved["SHELL"], wantConstSite)
	}

	cleared := map[string]bool{}
	for _, name := range clearedEnv() {
		cleared[name] = true
	}

	// Every name production code reads must be cleared or exempt.
	for name, where := range reads {
		switch {
		case cleared[name]:
		case exemptEnv[name] != "":
		default:
			t.Errorf("%s is read by production code (%s) but is neither "+
				"cleared by ClearEnv nor listed in exemptEnv. Add it to "+
				"hostEnv, or exempt it with the reason AND where it is "+
				"isolated instead", name, strings.Join(where, ", "))
		}
	}

	// And nothing may be listed that is no longer read: a stale entry
	// makes the list look more complete than it is.
	for _, name := range clearedEnv() {
		if _, ok := reads[name]; !ok {
			t.Errorf("ClearEnv clears %s but no production code reads "+
				"it — drop it, or the list stops describing anything",
				name)
		}
	}
	for name, reason := range exemptEnv {
		if reason == "" {
			t.Errorf("exemptEnv[%s] has no reason", name)
		}
		if _, ok := reads[name]; !ok {
			t.Errorf("exemptEnv lists %s but no production code reads "+
				"it — a dead exemption is a future hole", name)
		}
	}
}

// TestClearEnvActuallyClears is the behavioural half. The list above
// only proves the NAMES are accounted for; this proves ClearEnv does
// something with them. A list that is complete and a function that
// ignores it look identical from the outside. The names are spelled
// out literally, not looped from clearedEnv(): looping over the same
// list ClearEnv itself is defined against would make the assertion
// tautological — it would keep passing even if ClearEnv cleared the
// wrong names, so long as clearedEnv() agreed with itself.
func TestClearEnvActuallyClears(t *testing.T) {
	names := []string{"NO_COLOR", "PGEDGE_CLIENT_ID",
		"PGEDGE_CLIENT_SECRET", "SHELL", "XDG_CONFIG_HOME"}
	for _, name := range names {
		t.Setenv(name, "leaked-"+name)
	}
	ClearEnv(t)
	for _, name := range names {
		if got := os.Getenv(name); got != "" {
			t.Errorf("ClearEnv left %s = %q", name, got)
		}
	}
}

// scanProductionEnvReads returns every environment variable name read
// by non-test, non-generated code in the module, mapped to every file
// it was found in (deduplicated, in discovery order) — plus a second
// map, constResolved, holding only the reads whose call site named a
// declared constant/variable rather than a string literal.
func scanProductionEnvReads(t *testing.T) (
	found map[string][]string, constResolved map[string][]string,
) {
	t.Helper()
	root := "../.."
	found = map[string][]string{}
	constResolved = map[string][]string{}
	addTo := func(m map[string][]string, name, file string) {
		for _, existing := range m[name] {
			if existing == file {
				return
			}
		}
		m[name] = append(m[name], file)
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Generated clients are not hand-written and are not where
			// resolution is decided; skip vendored and VCS trees too.
			base := info.Name()
			if base == ".git" || base == "api" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		// Constant and variable string values declared in this file, so
		// os.Getenv(envShell) resolves. internal/cli declares that name
		// as a constant rather than a literal, so without this the scan
		// misses it entirely.
		consts := map[string]string{}
		ast.Inspect(file, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if v, ok := stringLit(vs.Values[i]); ok {
					consts[name.Name] = v
				}
			}
			return true
		})

		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			switch sel.Sel.Name {
			case "UserHomeDir":
				addTo(found, "HOME", rel)
			case "Getenv", "LookupEnv":
				if len(call.Args) != 1 {
					return true
				}
				if v, ok := stringLit(call.Args[0]); ok {
					addTo(found, v, rel)
					return true
				}
				if id, ok := call.Args[0].(*ast.Ident); ok {
					if v, ok := consts[id.Name]; ok {
						addTo(found, v, rel)
						addTo(constResolved, v, rel)
						return true
					}
					// An unresolvable name is reported rather than
					// skipped: silently dropping it is how a variable
					// slips past this gate entirely.
					t.Errorf("%s: os.%s(%s) — cannot resolve the name; "+
						"the scan would miss it, so declare it as a "+
						"string constant in the same file",
						rel, sel.Sel.Name, id.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk production sources: %v", err)
	}
	return found, constResolved
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
