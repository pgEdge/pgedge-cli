package clitest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The exact set of managed leaves carrying wait flags, as a written-down
// decision. It is the SECOND check, not the population: what derives
// the population is TestManagedWaitFlagsFollowTheCode at the bottom of
// this file, over the package's own function references.
//
// That order matters, because this list on its own was bypassable in
// two lines — delete `addWaitFlags` from `rag deploy`, delete its entry
// here, and `make test`, `make docs-check`, `make lint-docs` and
// `make lint` all stay green while both references still claim the
// flag. Review did exactly that. A hand list cannot police its own
// membership, which is why the derived gate exists and why this one
// must never be described as what pins the set.
//
// What it still earns: it is the human-readable record of which verbs
// were MEANT to wait, so a rename or a deliberate change has to be
// typed out here rather than absorbed. And it covers a dimension the
// derived gate does not see, because it reads the built tree rather
// than the source — a verb whose flags arrive from somewhere other
// than a package-local reference.
//
// The set is spelled out rather than inferred from "is this verb
// mutating", because the two are not the same question.
// `backup create` is mutating, asynchronous, and correctly has NO
// wait flags — its backup-managed task reaches succeeded while the
// backup record is still pending (measured), so a task wait would
// report success over a backup still in progress; the reference
// documents polling `backup get` to a terminal status instead.
var managedWaitVerbs = []string{
	// Pre-existing.
	"starfleet managed backup restore",
	"starfleet managed database create",
	"starfleet managed database delete",
	"starfleet managed database resize",
	// #359: rotation spawns rotate-password-managed and returned no
	// handle; byoc's rotate had the flags all along.
	"starfleet managed database rotate-password",
	// Added by #261. All seven service leaves, because applyServices is
	// the single write behind them: registering on fewer would leave a
	// verb reaching trackMutation, and printing `Monitor with:`,
	// without declaring the flag it names.
	"starfleet managed database mcp deploy",
	"starfleet managed database mcp update",
	"starfleet managed database rag deploy",
	"starfleet managed database rag update",
	"starfleet managed database postgrest deploy",
	"starfleet managed database postgrest update",
	"starfleet managed database service remove",
	// Reaches trackMutation through applyAllowlist, on both
	// its PATCH branch and its applyServices branch.
	"starfleet managed database allowlist add",
	"starfleet managed database allowlist remove",
	"starfleet managed database allowlist set",
	"starfleet managed database allowlist open",
	"starfleet managed database allowlist clear",
	// Both reach trackMutation: create has no prior tasks so it waits
	// against a zero baseline, delete captures one before the request
	// the same way `database delete` does.
	"starfleet managed database branch create",
	"starfleet managed database branch delete",
}

// waitFlags are what addWaitFlags registers. All four or none: a verb
// with --wait but no --wait-timeout would accept a wait it cannot
// bound. The #356 rename made the two bounds unambiguous:
// --wait-timeout/--wait-interval govern the wait, --timeout (the
// starfleet root's) bounds one request — which also freed metrics'
// `value,unit` lookback (now --window, #358) from needing an
// exemption here.
var waitFlags = []string{"wait", "follow", "wait-timeout", "wait-interval"}

// partialWaitFlagsExempt names verbs that carry one of those flag NAMES
// for an unrelated reason, with the reason. Found by this gate on its
// first run, which is the argument for asserting the set rather than
// trusting a fixture.
var partialWaitFlagsExempt = map[string]string{
	"starfleet managed task wait": "waiting IS the verb, so there is no " +
		"--wait to declare. It takes --follow, --wait-timeout and " +
		"--wait-interval with the polling meaning.",
}

func TestManagedWaitFlagsAreExactlyThisSet(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{}
	for _, v := range managedWaitVerbs {
		want[v] = true
	}

	var got []string
	walk(root, func(c *cobra.Command) {
		path := strings.TrimPrefix(c.CommandPath(), "pgedge ")
		if !strings.HasPrefix(path, "starfleet managed ") {
			return
		}
		if c.Flags().Lookup("wait") == nil {
			if _, ok := partialWaitFlagsExempt[path]; ok {
				// The REASON is asserted, not just recorded. An
				// exemption whose stated grounds nothing checks is a
				// sentence, and a future change could gut it silently.
				if path == "starfleet managed task wait" {
					for _, want := range []string{
						"follow", "wait-timeout", "wait-interval",
					} {
						if c.Flags().Lookup(want) == nil {
							t.Errorf("%s: exempt because waiting IS "+
								"the verb, which presumes it still "+
								"carries --%s", path, want)
						}
					}
				}
				return
			}
			// A verb carrying SOME wait flags but not --wait is the
			// shape a partial registration takes, and it would
			// otherwise be invisible here.
			for _, f := range waitFlags {
				if c.Flags().Lookup(f) != nil {
					t.Errorf("%s declares --%s but not --wait; "+
						"addWaitFlags registers all four together. If "+
						"the flag means something else on this verb, "+
						"say so in partialWaitFlagsExempt.", path, f)
					break
				}
			}
			return
		}
		got = append(got, path)

		for _, f := range waitFlags {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("%s declares --wait but not --%s; a wait "+
					"nobody can bound is worse than none", path, f)
			}
		}
	})

	sort.Strings(got)
	for _, path := range got {
		if !want[path] {
			t.Errorf("%s carries wait flags but is not in "+
				"managedWaitVerbs. If that is deliberate, add it here "+
				"WITH a reason — and check the verb really reaches "+
				"trackMutation, because printing `Monitor with:` from "+
				"a verb that spawns no task is a promise the CLI "+
				"cannot keep.", path)
		}
		delete(want, path)
	}
	for path := range want {
		t.Errorf("%s is in managedWaitVerbs but carries NO wait flags. "+
			"A fixture-driven test cannot catch this: guardServiceIntent "+
			"turns the deploy verbs away before the write, so deleting "+
			"addWaitFlags from one of them leaves every other gate "+
			"green (#261). If the removal is deliberate, "+
			"TestManagedWaitFlagsFollowTheCode is the one that decides "+
			"whether it is allowed.", path)
	}

	// No floor here, deliberately. An earlier version had one and it
	// was UNREACHABLE: `want` entries are deleted only for paths found
	// in `got`, and walk visits each command once, so `len(want) == 0`
	// already implies `len(got) >= len(managedWaitVerbs)`. Its message
	// described a state it could never report.
}

// An exemption that excuses nothing reads as a live fact about the
// tree, so both lists are checked against it.
func TestWaitFlagListsAreLive(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		paths[strings.TrimPrefix(c.CommandPath(), "pgedge ")] = true
	})
	for _, v := range managedWaitVerbs {
		if !paths[v] {
			t.Errorf("managedWaitVerbs names %q, which is not a "+
				"command. A renamed verb would otherwise be reported "+
				"as missing its flags forever.", v)
		}
	}
	for path, why := range partialWaitFlagsExempt {
		if !paths[path] {
			t.Errorf("partialWaitFlagsExempt excuses %q (%s), which is "+
				"not a command.", path, why)
		}
	}
}

// The derivation.
//
// managedWaitVerbs above is a hand list, and review bypassed it in two
// lines: delete `addWaitFlags` from `rag deploy`, delete its entry from
// the list, and all four gates stay green while both references still
// claim the flag. Deriving the population from what the code declares
// is this repo's standing rule — a hand list has been bypassed at
// whatever dimension it did not vary, five times in one session — so
// the population is derived here and the list above is kept only as the
// second, weaker check.
//
// The invariant is mechanical and has one sentinel at each end.
// trackMutation is the only function that waits and the only one that
// prints `Monitor with:`; addWaitFlags is the only one that registers
// the four flags. So for every function in internal/starfleet/managed/cmd
// returning a *cobra.Command:
//
//	its body reaches trackMutation  <=>  its body reaches addWaitFlags
//
// Both directions are defects. Reaching trackMutation without the flags
// is the #261 gap — a verb that prints `Monitor with:` and declares no
// --wait. The flags without trackMutation is the opposite promise: a
// --wait the verb accepts and silently ignores.
//
// Reachability runs over the package's own function references with
// CONSTRUCTOR EDGES CUT. A group constructor calls its leaves, so
// without the cut `NewDatabaseMCPCmd` inherits its deploy leaf's
// trackMutation and every group in the tree reads as a mutating verb.
// Cutting them attributes the reach to the constructor that owns the
// RunE.
//
// What this still cannot see:
//
//   - a mutating write reached by some route other than trackMutation.
//     Nothing in managed has one today — all four call sites are here —
//     and the cross-check against the built tree at the end is what
//     notices trackMutation stopping being the sentinel.
//   - a constructor in a SUBdirectory of the package. os.ReadDir is not
//     recursive, but a subdirectory is a different Go package and
//     cannot call either unexported sentinel, so Go's own visibility
//     rules close that one rather than this gate.

// managedCmdSrcDir is the managed cobra tree, read as source rather
// than as a built tree: which flags a constructor registers is a
// property of the tree, but which write it reaches is not.
const managedCmdSrcDir = "../starfleet/managed/cmd"

// managedCmdSource is the parsed non-test source of that package.
type managedCmdSource struct {
	// refs maps a package-level func name to every identifier NAMED
	// anywhere in its body, function literals included -- which is
	// where every RunE lives.
	//
	// Named, not called. An earlier version collected only CallExpr
	// targets that were bare identifiers, and `apply := applyRAGService`
	// followed by `apply(...)` walked straight through it: the reach
	// vanished, the flags came off, and the gate passed. Any mention of
	// a function is a dependency on it, so any mention counts.
	refs map[string]map[string]bool
	// ctors is the subset returning *cobra.Command, mapped to
	// file:line so a failure names where to look.
	ctors map[string]string
	// funcs is every package-level func name declared.
	funcs map[string]bool
}

// returnsCobraCommand reports whether fn returns a *cobra.Command.
func returnsCobraCommand(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		star, ok := r.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Command" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "cobra" {
			return true
		}
	}
	return false
}

// parseManagedCmdSource parses the package. Methods are skipped, since
// including them would only let a method collide with a package-level
// func of the same name.
//
// The identifier sweep is deliberately blunt: it also picks up the
// selector half of `pkg.Foo` and every local variable. That is noise
// rather than error, because only names that ARE package-level funcs
// here become edges — but it does mean a method sharing a name with one
// of them would forge an edge. None does, and the duplicate-name Fatal
// below is the tripwire for the func side of that.
func parseManagedCmdSource(t *testing.T) managedCmdSource {
	t.Helper()
	src := managedCmdSource{
		refs:  map[string]map[string]bool{},
		ctors: map[string]string{},
		funcs: map[string]bool{},
	}
	entries, err := os.ReadDir(managedCmdSrcDir)
	if err != nil {
		t.Fatalf("read %s: %v", managedCmdSrcDir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(managedCmdSrcDir, name)
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			if src.funcs[fn.Name.Name] {
				t.Fatalf("%s: %s is declared twice in the package, so "+
					"this gate's graph would merge two functions' "+
					"references", path, fn.Name.Name)
			}
			src.funcs[fn.Name.Name] = true
			named := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					named[id.Name] = true
				}
				return true
			})
			src.refs[fn.Name.Name] = named
			if returnsCobraCommand(fn) {
				src.ctors[fn.Name.Name] = fmt.Sprintf("%s:%d",
					path, fset.Position(fn.Pos()).Line)
			}
		}
	}
	return src
}

// reachesFn reports whether from's body reaches target through
// package-local function references, with constructor edges cut.
func reachesFn(src managedCmdSource, from, target string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for name := range src.refs[cur] {
			if name == target {
				return true
			}
			if seen[name] || !src.funcs[name] {
				continue
			}
			if _, isCtor := src.ctors[name]; isCtor {
				continue
			}
			seen[name] = true
			queue = append(queue, name)
		}
	}
	return false
}

func TestManagedWaitFlagsFollowTheCode(t *testing.T) {
	src := parseManagedCmdSource(t)

	// Both sentinels must exist. Without them the two sets are empty,
	// every comparison holds, and the gate passes while every verb in
	// the module has lost its flags.
	for _, sentinel := range []string{"trackMutation", "addWaitFlags"} {
		if !src.funcs[sentinel] {
			t.Fatalf("%s is not a func in %s — this gate is built on it "+
				"being the only one of its kind, so a rename or an "+
				"inlining has to be reflected here rather than left to "+
				"pass silently", sentinel, managedCmdSrcDir)
		}
	}
	// The tree is large; a handful of constructors means the parse
	// found one file, not the package.
	if len(src.ctors) < 30 {
		t.Fatalf("only %d command constructors parsed from %s — the "+
			"source walk is broken and this gate is comparing almost "+
			"nothing", len(src.ctors), managedCmdSrcDir)
	}

	var declarers []string
	for _, name := range sortedCtors(src.ctors) {
		where := src.ctors[name]
		tracks := reachesFn(src, name, "trackMutation")
		waits := reachesFn(src, name, "addWaitFlags")
		if waits {
			declarers = append(declarers, name)
		}
		switch {
		case tracks && !waits:
			t.Errorf("%s (%s) reaches trackMutation but never "+
				"addWaitFlags. That verb prints `Monitor with:` and "+
				"declares no --wait, which is the #261 gap. Register "+
				"addWaitFlags(cmd), or stop reaching trackMutation.",
				name, where)
		case waits && !tracks:
			t.Errorf("%s (%s) calls addWaitFlags but reaches no "+
				"trackMutation. Nothing else in the module waits, so "+
				"--wait would be accepted and silently ignored — worse "+
				"than absent, because a script would trust it.",
				name, where)
		}
	}

	// Cross-check the source against the built tree. This is what
	// notices trackMutation or addWaitFlags being inlined away, and a
	// constructor that registers --wait by hand instead of through
	// addWaitFlags: the source side stops counting it while the tree
	// still carries the flag.
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	var live int
	walk(root, func(c *cobra.Command) {
		path := strings.TrimPrefix(c.CommandPath(), "pgedge ")
		if strings.HasPrefix(path, "starfleet managed ") &&
			c.Flags().Lookup("wait") != nil {
			live++
		}
	})
	if live != len(declarers) {
		t.Errorf("%d managed commands declare --wait in the built tree, "+
			"but %d constructors call addWaitFlags (%v). The two must "+
			"agree: a difference means --wait was registered by hand, or "+
			"addWaitFlags is no longer the only thing that registers it.",
			live, len(declarers), declarers)
	}
	t.Logf("%d of %d parsed constructor(s) reach addWaitFlags",
		len(declarers), len(src.ctors))
}

// sortedCtors keeps the failure order stable, so a diff between two
// runs of a mutated tree is readable.
func sortedCtors(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
