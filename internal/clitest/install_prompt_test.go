package clitest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// --- the install-prompt duplication check ------------------------------------
//
// The AI-agent install prompt has to exist in two files. README.md is
// rendered by GitHub and docs/getting-started.md by MkDocs, and no
// include mechanism serves both: mkdocs.yml does not load
// pymdownx.snippets, and GitHub would not honour a snippet if it did.
// So the prompt is duplicated, and duplication without a gate drifts —
// getting-started.md had already fallen four releases behind on the
// release tag it names while pointing at README.md for the prompt
// itself, so a reader on the docs site could not copy the thing the
// page was describing.
//
// A drifted copy here is worse than ordinary doc rot. The prompt is
// pasted into an agent that runs it unattended, so a stale step is
// executed rather than read past.
//
// WHY BYTE-FOR-BYTE, AND WHAT THAT COSTS. The comparison is exact
// rather than normalised. Whitespace inside the block is significant —
// the prompt is an indented markdown code block, and the four-space
// indent is what keeps it a code block in both renderers — so a check
// that collapsed whitespace could pass a copy that renders as prose in
// one of the two. The cost is that a purely cosmetic rewrap in one file
// fails the build. That is the intended trade: the fix is to rewrap
// both, which is the behaviour being enforced.
//
// SCOPE. This holds that the two copies AGREE. It cannot tell whether
// what they agree on is correct — that the release tag named still
// exists, that the download pattern still matches a published asset, or
// that the skill names are real. Those need the network, and a gate
// that reaches the network fails on a train. Re-verify the prompt
// against a real release when cutting one.

const (
	installPromptBegin = "<!-- install-prompt: begin -->"
	installPromptEnd   = "<!-- install-prompt: end -->"
)

// installPromptFiles are the two documents carrying the prompt. Both
// are checked for exactly one well-formed region.
//
// This list is not the authority on WHICH files carry a prompt —
// TestInstallPromptCopiesAreDiscovered walks the tree and fails if the
// two disagree. A hardcoded list alone is blind to a third copy added
// elsewhere, which was demonstrated rather than argued: a drifted copy
// in a new docs file passed the whole package.
var installPromptFiles = []string{
	"../../README.md",
	"../../docs/getting-started.md",
}

// installPromptSkipDirs are directories whose markdown never counts as
// a copy. Agent worktrees under .claude are the reason this exists: a
// reviewer's disposable checkout of this very repo contains both
// documents, and walking into one would report the tree as carrying
// four copies of a prompt that is really two.
var installPromptSkipDirs = map[string]bool{
	".git":         true,
	".claude":      true,
	"node_modules": true,
	"site":         true,
	".worktrees":   true,
	"vendor":       true,
}

// findInstallPromptFiles walks the repository for markdown carrying the
// begin marker, returning paths in the same "../../"-relative spelling
// as installPromptFiles.
func findInstallPromptFiles(t *testing.T) []string {
	t.Helper()

	// Anchor the walk root rather than trusting "../..". If this
	// package ever moves, an unanchored root silently scans the
	// wrong tree — one level up is a directory of sibling checkouts,
	// each containing a README that would read as a rogue copy.
	const root = "../.."
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repository root (no go.mod): %v — this "+
			"walk would scan the wrong tree", root, err)
	}

	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if installPromptSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		// .txt as well as .md: llms.txt is the most agent-facing
		// install document this repo has, and a .md-only walk could
		// not see it — which is not hypothetical, since llms.txt went
		// on recommending `go install ...@latest` after the README
		// stopped.
		if !strings.HasSuffix(d.Name(), ".md") &&
			!strings.HasSuffix(d.Name(), ".txt") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), installPromptBegin) {
			found = append(found, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	return found
}

// extractInstallPrompt returns the text between the markers in content,
// or a violation describing why it could not. The markers themselves are
// excluded from the returned region so that a rename of the marker pair
// does not read as a content change.
func extractInstallPrompt(path, content string) (region string, violations []string) {
	nBegin := strings.Count(content, installPromptBegin)
	nEnd := strings.Count(content, installPromptEnd)

	// Counting rather than searching once: a second begin marker would
	// otherwise be invisible, and the region would silently be the span
	// to the FIRST end marker. This repo has already shipped a gate
	// whose anchor resolved onto the wrong span.
	if nBegin != 1 || nEnd != 1 {
		return "", []string{fmt.Sprintf(
			"%s: expected exactly one %q and one %q, found %d and %d. "+
				"The prompt region must be unambiguous, or the gate "+
				"compares a span nobody intended",
			path, installPromptBegin, installPromptEnd, nBegin, nEnd)}
	}

	i := strings.Index(content, installPromptBegin)
	j := strings.Index(content, installPromptEnd)
	if j < i {
		return "", []string{fmt.Sprintf(
			"%s: %q appears before %q", path,
			installPromptEnd, installPromptBegin)}
	}

	return content[i+len(installPromptBegin) : j], nil
}

// checkInstallPromptCopies returns one violation per way the regions
// disagree, or per malformed region.
func checkInstallPromptCopies(docs map[string]string) []string {
	var violations []string

	regions := make(map[string]string, len(docs))
	for path, content := range docs {
		region, errs := extractInstallPrompt(path, content)
		if len(errs) > 0 {
			violations = append(violations, errs...)
			continue
		}

		// A gate over two empty regions passes while checking nothing,
		// which looks exactly like a gate that is working. The prompt's
		// first instruction is the cheapest thing that cannot be true
		// of an empty or gutted region.
		if !strings.Contains(region, "Install the pgEdge CLI on this machine") {
			violations = append(violations, fmt.Sprintf(
				"%s: the install-prompt region does not contain the "+
					"prompt's opening instruction. Either the prompt was "+
					"rewritten and this guard's anchor needs moving, or "+
					"the region is empty and this gate was comparing "+
					"nothing", path))
			continue
		}
		regions[path] = region
	}
	// Fewer than two well-formed regions means there is nothing to
	// compare. This is an explicit guard, not a shortcut: without it
	// paths[0] below indexes an empty slice and the gate panics, which
	// this repo forbids outright. A single-file map is also reported
	// rather than passing silently, since a gate that compares one
	// thing against nothing returns no violations and looks healthy.
	if len(regions) < 2 {
		if len(violations) == 0 {
			violations = append(violations, fmt.Sprintf(
				"only %d well-formed install-prompt region(s) found "+
					"across %d document(s); at least two are needed for "+
					"the comparison to mean anything",
				len(regions), len(docs)))
		}
		return violations
	}

	var paths []string
	for p := range regions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// Compare every copy against one arbitrary reference rather than
	// pairwise: with two files these are the same, and with three the
	// message stays readable.
	ref := paths[0]
	for _, p := range paths[1:] {
		if regions[p] != regions[ref] {
			violations = append(violations, fmt.Sprintf(
				"%s and %s carry different install prompts. They are "+
					"duplicated on purpose (no include mechanism serves "+
					"both GitHub and MkDocs) and must be edited together. "+
					"%s",
				ref, p, firstInstallPromptDiff(regions[ref], regions[p])))
		}
	}
	return violations
}

// firstInstallPromptDiff names the first differing line, so a failure
// says where to look rather than only that two blocks differ.
func firstInstallPromptDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return fmt.Sprintf(
				"First difference at region line %d:\n  %q\n  %q",
				i+1, la[i], lb[i])
		}
	}
	return fmt.Sprintf(
		"One region has %d lines and the other %d; the shorter is a "+
			"prefix of the longer", len(la), len(lb))
}

// TestInstallPromptCopiesMatch holds the two copies of the AI-agent
// install prompt byte-for-byte.
func TestInstallPromptCopiesMatch(t *testing.T) {
	docs := make(map[string]string, len(installPromptFiles))
	for _, path := range installPromptFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		docs[path] = string(content)
	}

	for _, msg := range checkInstallPromptCopies(docs) {
		t.Error(msg)
	}
}

// TestInstallPromptCopiesAreDiscovered holds installPromptFiles to the
// tree rather than to itself. Without this, a third copy added in a new
// document is compared against nothing and drifts freely — measured, a
// drifted copy carrying a hostile `curl | sudo sh` step passed the
// entire package.
func TestInstallPromptCopiesAreDiscovered(t *testing.T) {
	found := findInstallPromptFiles(t)

	if len(found) == 0 {
		t.Fatal("no file in the tree carries " + installPromptBegin +
			" — the walk is broken and every guard keyed on it is " +
			"silently checking nothing")
	}

	want := append([]string(nil), installPromptFiles...)
	sort.Strings(want)

	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the set of documents carrying the install prompt has "+
			"changed.\n  walked the tree and found:\n    %s\n  "+
			"installPromptFiles declares:\n    %s\n"+
			"Add the new copy to installPromptFiles so it joins the "+
			"byte-for-byte comparison, or remove its markers. A copy "+
			"outside that list is compared against nothing.",
			strings.Join(found, "\n    "), strings.Join(want, "\n    "))
	}
}

// TestInstallPromptCheckCatchesDrift is the positive control. Without
// it, a checker that returned no violations for every input — because
// its markers stopped matching, say — would be indistinguishable from
// two documents that agree.
func TestInstallPromptCheckCatchesDrift(t *testing.T) {
	const opening = "Install the pgEdge CLI on this machine"

	good := installPromptBegin + "\n" + opening + "\n    step one\n" +
		installPromptEnd
	drifted := installPromptBegin + "\n" + opening + "\n    step two\n" +
		installPromptEnd

	cases := []struct {
		name string
		docs map[string]string
		want string
	}{{
		name: "identical regions pass",
		docs: map[string]string{"a.md": good, "b.md": good},
		want: "",
	}, {
		name: "a changed line is caught",
		docs: map[string]string{"a.md": good, "b.md": drifted},
		want: "different install prompts",
	}, {
		name: "a trailing-whitespace-only change is caught",
		docs: map[string]string{"a.md": good, "b.md": strings.Replace(
			good, "    step one", "    step one ", 1)},
		want: "different install prompts",
	}, {
		name: "an indent change is caught, since it changes rendering",
		docs: map[string]string{"a.md": good, "b.md": strings.Replace(
			good, "    step one", "  step one", 1)},
		want: "different install prompts",
	}, {
		name: "a missing region is caught",
		docs: map[string]string{"a.md": good, "b.md": "no markers here"},
		want: "expected exactly one",
	}, {
		name: "a duplicated begin marker is caught",
		docs: map[string]string{
			"a.md": good,
			"b.md": installPromptBegin + "\n" + good,
		},
		want: "expected exactly one",
	}, {
		name: "an empty region is caught rather than passing vacuously",
		docs: map[string]string{
			"a.md": installPromptBegin + installPromptEnd,
			"b.md": installPromptBegin + installPromptEnd,
		},
		want: "does not contain the prompt's opening instruction",
	}, {
		name: "reversed markers are caught",
		docs: map[string]string{
			"a.md": good,
			"b.md": installPromptEnd + "\n" + opening + "\n" +
				installPromptBegin,
		},
		want: "appears before",
	}, {
		// A case-insensitive comparison passed every other fixture
		// here, because none of them varied case. Shell commands are
		// case-sensitive, so a copy differing only in case is a real
		// divergence.
		name: "a case-only change is caught",
		docs: map[string]string{"a.md": good, "b.md": strings.Replace(
			good, "    step one", "    Step One", 1)},
		want: "different install prompts",
	}, {
		// Guards paths[0] against an empty slice. This panicked before
		// the length check was made explicit.
		name: "no documents is reported, not panicked on",
		docs: map[string]string{},
		want: "at least two are needed",
	}, {
		name: "a single document is reported rather than passing",
		docs: map[string]string{"a.md": good},
		want: "at least two are needed",
	}, {
		name: "every region malformed reports only the malformations",
		docs: map[string]string{
			"a.md": "no markers", "b.md": "none here either",
		},
		want: "expected exactly one",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkInstallPromptCopies(tc.docs)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("expected no violations, got %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("expected a violation containing %q, got none",
					tc.want)
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("expected a violation containing %q, got:\n%s",
					tc.want, joined)
			}
		})
	}
}
