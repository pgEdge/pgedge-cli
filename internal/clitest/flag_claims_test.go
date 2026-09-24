package clitest

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- 4. the flag-name check --------------------------------------------------
//
// The three checks above gate a doc's claims about task envelopes,
// status vocabulary and response fields. None of them gates the other
// thing a doc asserts on nearly every line: a FLAG. That class shipped
// a real defect — a troubleshooting row in skills/pgedge-controlplane/SKILL.md
// citing a fabricated `--cp-url`, which exists nowhere in the CLI.
// The row stayed green
// because no gate compared a doc's `--flag` tokens against the flags
// the cobra tree actually declares. This check is the derived form:
// every flag-shaped token in hand-written doc text must resolve
// against the shipped command tree, so `--cp-url` and whatever gets
// invented next fail without anyone adding a string.
//
// WHY ONE GLOBAL UNIVERSE AND NOT PER-MODULE SCOPING. The other checks
// scope by module because their vocabularies genuinely collide (controlplane's
// task_id is byoc's fabrication). Flags do not collide that way — a
// fabricated flag exists in NO module — and legitimate cross-module
// flag mentions are routine: the byoc skill quotes account's flags,
// and the index documents the root's. Scoping per module would demand
// correct-for markers on all of those correct sentences to catch a
// defect class that has never occurred (a real flag claimed on the
// wrong module).
// So the universe is the whole tree, and the residual class is stated
// below instead of half-caught.
//
// WHAT THIS DOES NOT CATCH, STATED SO NOBODY ASSUMES OTHERWISE:
//
//   - a REAL flag claimed on the WRONG command or module. `--wait`
//     exists, so a doc telling an agent to pass it to a verb that
//     lacks it passes this check. Binding flags to the command whose
//     section they sit in is the fuller gate, same as field_claims'
//     per-command binding, and it shares that check's mis-anchoring
//     hazard.
//   - a flag documented with the wrong argument or default. This
//     check validates existence, nothing else.
//
// EXTERNAL TOOLS' FLAGS. The docs legitimately quote flags that belong
// to other programs: cosign's --certificate, the skills CLI's
// --global. A doc-gate marker is the wrong instrument for those —
// deliberately-wrong asserts the phrase is WRONG (these are right),
// and correct-for asserts it is right under
// another MODULE's scope (these belong to no module). So external
// flags live in externalToolFlags below: each entry names the tool
// that owns the flag and the exact files allowed to mention it, and
// TestExternalToolFlagAllowlistEntriesAreLive fails on any entry that
// has gone stale, the same way a marker that excuses nothing fails.
// The per-file scoping is what keeps this from decaying into a global
// amnesty: a fabricated `--certificate` in a skill file is still
// caught, because only README.md may say it.

// flagClaimRe matches a flag-shaped token: two hyphens, then a
// kebab-case name starting with a letter. The leading-letter
// requirement is what keeps table rules (`|----|`) and horizontal
// rules out. Backticks are deliberately NOT required, unlike
// fieldClaimRe: half the docs' flag mentions sit unbackticked inside
// workflow code fences, which is exactly where the fabricated
// `--cp-url` row lived.
//
// The letter class is A-Za-z, not just a-z: every real
// pgedge flag is lowercase-kebab (knownFlagNames only ever collects
// names off pflag.Flag.Name, which this codebase always declares in
// lowercase), so a token this pattern catches with any uppercase
// letter in it is never a real flag under any case. Before this, the
// pattern's lowercase-only leading class meant `--Force`, `--Api-Url`
// and `--DEBUG` were never even extracted as candidate tokens — not
// rejected, just invisible to the whole check, so a mis-cased or
// fabricated-with-uppercase flag shipped clean. Widening the class is
// safe by construction: it only ever adds tokens for the comparison
// below to judge, so no doc that only ever writes real lowercase-kebab
// flags (every one of them, verified above) can newly fail.
var flagClaimRe = regexp.MustCompile(`--[A-Za-z][A-Za-z0-9-]*`)

// ownsFlagNames is the fourth arm of the allowance partition described
// on annotatedSentence.spend. Flag names carry their `--` prefix
// through extraction, markers and reporting alike, so the prefix is
// the whole predicate — and ownsValueNames and ownsFieldNames both
// exclude it, which keeps the partition total and disjoint.
func ownsFlagNames(name string) bool {
	return strings.HasPrefix(name, "--")
}

// externalToolFlags is the allowlist of non-pgedge flags the docs may
// mention, each bound to the tool that owns it and the files allowed
// to say it. Adding an entry is a reviewed statement of ownership;
// TestExternalToolFlagAllowlistEntriesAreLive reports the entry the
// moment it stops matching anything, so the list cannot silently
// outlive the text it excuses.
//
// A key is either one document, or a MODULE reference directory as
// ReferenceModuleDir spells it, which stands for that module's index
// and every page under it. The split moved sentences from an index
// onto a page without changing which module owns them, and naming the
// file would have withdrawn the allowance from wherever the sentence
// landed — see externalFlagDocs.
var externalToolFlags = map[string]struct {
	tool  string
	files map[string]bool
}{
	"--certificate": {"cosign verify-blob",
		map[string]bool{"../../README.md": true}},
	"--certificate-identity-regexp": {"cosign verify-blob",
		map[string]bool{"../../README.md": true}},
	"--certificate-oidc-issuer": {"cosign verify-blob",
		map[string]bool{"../../README.md": true}},
	"--signature": {"cosign verify-blob",
		map[string]bool{"../../README.md": true}},
	"--ignore-missing": {"sha256sum -c",
		map[string]bool{"../../README.md": true}},
	"--env": {"docker run",
		map[string]bool{"../../docs/ci.md": true}},
	"--jq": {"the gh CLI (gh api)",
		map[string]bool{"../../llms.txt": true}},
	"--global": {"the skills CLI (npx skills add)",
		map[string]bool{
			"../../README.md":               true,
			"../../skills/pgedge/SKILL.md":  true,
			"../../docs/getting-started.md": true,
		}},
	"--skill": {"the skills CLI (npx skills add)",
		map[string]bool{
			"../../README.md":              true,
			"../../skills/pgedge/SKILL.md": true,
		}},
	"--fail": {"curl",
		map[string]bool{
			"../../docs/managed/services.md":   true,
			"../../docs/byoc/services.md":      true,
			"../../internal/starfleet/managed": true,
		}},
	"--disable-triggers": {"pg_restore",
		map[string]bool{
			"../../docs/managed/load-data.md":  true,
			"../../docs/byoc/databases.md":     true,
			"../../internal/starfleet/managed": true,
			"../../internal/starfleet/byoc":    true,
		}},
	"--schema-only": {"pg_dump and pg_restore",
		map[string]bool{
			"../../docs/managed/load-data.md":            true,
			"../../docs/managed/migrate-into-managed.md": true,
		}},
	"--argjson": {"jq",
		map[string]bool{
			"../../docs/managed/monitoring-and-alerts.md": true,
			"../../docs/byoc/monitoring-and-alerts.md":    true,
		}},
	"--arg": {"jq",
		map[string]bool{
			"../../docs/controlplane/monitoring-and-alerts.md": true,
		}},
	"--data-only": {"pg_restore",
		map[string]bool{
			"../../docs/managed/load-data.md":  true,
			"../../internal/starfleet/managed": true,
		}},
	"--no-owner": {"pg_restore",
		map[string]bool{
			"../../docs/managed/load-data.md":            true,
			"../../docs/managed/export-data.md":          true,
			"../../docs/managed/migrate-into-managed.md": true,
			"../../internal/starfleet/managed":           true,
		}},
	"--no-acl": {"pg_restore",
		map[string]bool{
			"../../internal/starfleet/managed": true,
		}},
	"--no-privileges": {"pg_restore",
		map[string]bool{
			"../../docs/managed/export-data.md":          true,
			"../../docs/managed/load-data.md":            true,
			"../../docs/managed/migrate-into-managed.md": true,
		}},
	"--clean": {"pg_restore",
		map[string]bool{"../../docs/managed/export-data.md": true}},
	"--if-exists": {"pg_restore",
		map[string]bool{"../../docs/managed/export-data.md": true}},
	"--globals-only": {"pg_dumpall",
		map[string]bool{"../../docs/managed/export-data.md": true}},
	"--no-role-passwords": {"pg_dumpall",
		map[string]bool{"../../docs/managed/export-data.md": true}},
}

// knownFlagNamesCache holds the one global flag universe.
var knownFlagNamesCache map[string]bool

// knownFlagNames returns every flag name the shipped tree declares —
// local and persistent, on every command — with the `--` prefix
// attached, plus cobra's implicit --help (injected at execute time, so
// walking the tree never sees it).
func knownFlagNames(t *testing.T) map[string]bool {
	t.Helper()
	if knownFlagNamesCache != nil {
		return knownFlagNamesCache
	}

	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{"--help": true}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		collect := func(f *pflag.Flag) { names["--"+f.Name] = true }
		c.LocalFlags().VisitAll(collect)
		c.PersistentFlags().VisitAll(collect)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	// A universe that came back tiny means the walk found nothing and
	// every flag mention is about to be reported. Fail loudly instead.
	if len(names) < 100 {
		t.Fatalf("only %d flag names collected from the command tree — "+
			"the walk is broken and this check would flag correct docs "+
			"wholesale", len(names))
	}

	knownFlagNamesCache = names
	return names
}

// checkFlagNameClaims returns one violation per flag name sec mentions
// that no command in the shipped tree declares.
func checkFlagNameClaims(t *testing.T, path string,
	sec docSection) []string {
	t.Helper()

	known := knownFlagNames(t)

	var violations []string
	for _, sentence := range annotateSentences(sec.module,
		normalizeWhitespace(sec.text)) {

		seen := make(map[string]bool)
		for _, raw := range flagClaimRe.FindAllString(sentence.text, -1) {
			// known and externalToolFlags are looked up with raw, the
			// token exactly as written, NOT lowercased: every real
			// pgedge flag (knownFlagNames, off pflag.Flag.Name) and
			// every externalToolFlags entry is already lowercase-kebab,
			// so this lookup is case-sensitive by construction. That is
			// what makes a mis-cased mention of a real flag — `--Force`
			// for `--force` — fail to resolve rather than silently
			// matching after normalisation: lowercasing
			// the token here would make `--Force` equal the real
			// `--force` and erase the very mis-casing this check exists
			// to catch. name is the lowercased form used only for this
			// sentence's own marker bookkeeping (seen, spend), which
			// must match doc-gate markers regardless of the case an
			// author happened to type them in, same as every other
			// claim check in this file.
			name := strings.ToLower(raw)
			if known[raw] || seen[name] {
				continue
			}
			if ext, ok := externalToolFlags[raw]; ok &&
				(ext.files[path] || ext.files[ReferenceModuleDir(path)]) {
				seen[name] = true
				continue
			}
			if sentence.spend(name, ownsFlagNames) {
				seen[name] = true
				continue
			}
			seen[name] = true
			violations = append(violations, fmt.Sprintf(
				"%s: names flag %q, which no command in the CLI "+
					"declares. Correct the flag, or if the sentence names "+
					"it in order to document its absence, annotate it: "+
					"`<!-- doc-gate: deliberately-wrong %s — <reason> "+
					"-->`. If it belongs to a program that is not pgedge, "+
					"add it to externalToolFlags in flag_claims_test.go "+
					"with the owning tool and this file. Sentence: %q",
				path, raw, raw, strings.TrimSpace(sentence.text)))
		}

		violations = append(violations,
			unusedMarkerViolations(path, &sentence, ownsFlagNames)...)
	}
	return violations
}

// TestSkillDocsNameOnlyRealFlags runs the flag-name check over the same
// doc set the other doc gates cover.
func TestSkillDocsNameOnlyRealFlags(t *testing.T) {
	files := scanDocFiles(t)

	scanned := make(map[string]bool, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanned[path] = true

		for _, sec := range moduleSections(path, string(content)) {
			for _, msg := range checkFlagNameClaims(t, path, sec) {
				t.Error(msg)
			}
		}
	}

	assertAllNamedDocsScanned(t, scanned)
}

// TestFlagClaimCheckCatchesFabricatedFlag is the acceptance test, and
// its first case is the historical defect itself: the `--cp-url` row
// that shipped. The second case is unbackticked and mid-fence, the
// form workflow blocks actually use, so the gate cannot quietly narrow
// itself to backticked prose.
func TestFlagClaimCheckCatchesFabricatedFlag(t *testing.T) {
	cases := map[string]string{
		"the historical --cp-url row": "If the connection fails, " +
			"check `--cp-url` points at the control plane.",
		"unbackticked inside a fence": "```\npgedge starfleet byoc cluster " +
			"create --wait --creation-mode fast\n```",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkFlagNameClaims(t, "fixture.md",
				docSection{module: "byoc", text: fixture})
			if len(got) == 0 {
				t.Fatalf("checkFlagNameClaims did not flag %q", fixture)
			}
		})
	}
}

// TestFlagClaimCheckCatchesMisCasedFlag pins the A-Za-z class. Before
// it, flagClaimRe only matched a lowercase leading letter, so any
// flag-shaped token containing an uppercase letter was never even
// extracted as a candidate — not rejected, invisible — and passed the
// whole file clean. All three cases here name a REAL flag under the
// wrong case (--force, --api-url, --debug), which is the sharper form
// of the bug: a naive fix that resolves the token by lowercasing it
// before comparing to the known set would make `--Force` equal the
// real `--force` and erase the mis-casing, so this also pins that the
// comparison stays case-sensitive against the known (always
// lowercase-kebab) flag set.
func TestFlagClaimCheckCatchesMisCasedFlag(t *testing.T) {
	cases := map[string]string{
		"--Force":   "Pass `--Force` to skip the confirmation prompt.",
		"--Api-Url": "Set `--Api-Url` to point at a different backend.",
		"--DEBUG":   "Add `--DEBUG` for verbose logging.",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkFlagNameClaims(t, "fixture.md",
				docSection{module: "byoc", text: fixture})
			if len(got) != 1 {
				t.Fatalf("checkFlagNameClaims did not flag %q as "+
					"exactly one violation: got %d: %v", fixture,
					len(got), got)
			}
			if !strings.Contains(got[0], name) {
				t.Fatalf("violation message %q does not name the "+
					"original mis-cased token %q — reporting the "+
					"lowercased form instead would leave the author "+
					"unable to find the actual typo", got[0], name)
			}
		})
	}
}

// TestFlagClaimCheckAcceptsRealFlags is the negative control: real
// flags from four different declaration sites — a root persistent
// flag, a module persistent flag, a leaf-local flag and cobra's
// implicit --help — must all pass, or a clean run proves nothing.
func TestFlagClaimCheckAcceptsRealFlags(t *testing.T) {
	fixture := "Pass `--profile` to pick a profile, `--client-id` to " +
		"override credentials, --wait on the verb, `--base-url` for " +
		"cp, and `--help` anywhere."
	got := checkFlagNameClaims(t, "fixture.md",
		docSection{module: "byoc", text: fixture})
	if len(got) != 0 {
		t.Fatalf("checkFlagNameClaims flagged real flags: %v", got)
	}
}

// TestFlagClaimCheckHonoursDeliberatelyWrongMarker pins that this
// check shares the one opt-out. correct-for is deliberately absent
// here: the flag universe is global, so there is no "right under
// another module's scope" for a flag — a marker like that would be
// reported unused, which is the mechanism working.
func TestFlagClaimCheckHonoursDeliberatelyWrongMarker(t *testing.T) {
	t.Run("marker clears the mention", func(t *testing.T) {
		fixture := "There is no `--cp-url` flag; use `--base-url`. " +
			"<!-- doc-gate: deliberately-wrong --cp-url — this " +
			"sentence documents the flag's absence -->"
		if got := checkFlagNameClaims(t, "fixture.md",
			docSection{module: "controlplane", text: fixture}); len(got) != 0 {
			t.Fatalf("the marker did not clear the mention: %v", got)
		}
	})

	t.Run("unannotated control", func(t *testing.T) {
		fixture := "There is no `--cp-url` flag; use `--base-url`."
		if got := checkFlagNameClaims(t, "fixture.md",
			docSection{module: "controlplane", text: fixture}); len(got) != 1 {
			t.Fatalf("want exactly 1 violation without a marker, got "+
				"%d: %v", len(got), got)
		}
	})

	t.Run("stale marker is reported", func(t *testing.T) {
		fixture := "Nothing names any flag here. " +
			"<!-- doc-gate: deliberately-wrong --ghost-flag — stale -->"
		if got := checkFlagNameClaims(t, "fixture.md",
			docSection{module: "byoc", text: fixture}); len(got) != 1 {
			t.Fatalf("want exactly 1 unused-marker violation, got "+
				"%d: %v", len(got), got)
		}
	})
}

// TestFlagClaimCheckExternalAllowlistIsFileScoped proves the per-file
// scoping bites: the same `--global` that README.md may say is a
// violation anywhere else. Without this, the allowlist would be the
// global amnesty its comment promises it is not.
func TestFlagClaimCheckExternalAllowlistIsFileScoped(t *testing.T) {
	fixture := "Add `--global` to install for your user instead."

	if got := checkFlagNameClaims(t, "../../skills/pgedge/SKILL.md",
		docSection{module: "byoc", text: fixture}); len(got) != 0 {
		t.Fatalf("--global was flagged in a file the allowlist names: %v",
			got)
	}
	if got := checkFlagNameClaims(t, "fixture.md",
		docSection{module: "byoc", text: fixture}); len(got) != 1 {
		t.Fatal("--global was accepted outside the allowlisted files — " +
			"the per-file scoping is not doing anything")
	}
}

// externalFlagDocs expands one externalToolFlags key into the
// documents it covers: a module reference directory becomes that
// module's index and every page, and anything else is itself.
func externalFlagDocs(key string) []string {
	if files := ReferenceFilesFor(key); len(files) > 0 && key != "" {
		return files
	}
	return []string{key}
}

// TestExternalToolFlagAllowlistEntriesAreLive fails on any allowlist
// entry that no longer matches text in a file it names — the same
// staleness rule the doc-gate markers live under. An entry that
// excuses nothing makes the next reader believe an exception is doing
// work it is not.
func TestExternalToolFlagAllowlistEntriesAreLive(t *testing.T) {
	for flag, ext := range externalToolFlags {
		if ext.tool == "" {
			t.Errorf("externalToolFlags[%q] names no owning tool", flag)
		}
		for key := range ext.files {
			var mentioned bool
			for _, file := range externalFlagDocs(key) {
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Errorf("externalToolFlags[%q] names unreadable "+
						"file %s: %v", flag, file, err)
					continue
				}
				if strings.Contains(normalizeWhitespace(string(raw)),
					flag) {
					mentioned = true
				}
			}
			if !mentioned {
				t.Errorf("externalToolFlags[%q] is stale for %s — no "+
					"document it covers mentions it any more; remove "+
					"the entry (or the key from it)", flag, key)
			}
		}
	}
}
