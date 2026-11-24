package clitest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The install steps an agent runs live once, in INSTALL.md. Every
// user-facing install section carries only one line that fetches
// them, so the line never changes when the steps do.
//
// "with curl" is part of the contract: an agent's own web-fetch tool
// may hand the page to a smaller model to summarise, which garbles
// exact commands. curl returns the bytes.

const (
	installPromptBegin = "<!-- install-prompt: begin -->"
	installPromptEnd   = "<!-- install-prompt: end -->"

	installStepsFile = "INSTALL.md"
	installStepsPath = "../../" + installStepsFile
	installStepsURL  = "https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/" +
		installStepsFile
	installLine = "Fetch " + installStepsURL + " with curl and follow it."
)

// installPromptRequired are the copies that must exist. The walk finds
// any others, and holds them to the same line.
var installPromptRequired = []string{
	"../../README.md",
	"../../docs/getting-started.md",
}

// installPromptSkipDirs keeps agent worktrees under .claude, which
// hold a whole checkout of this repo, from reading as extra copies.
var installPromptSkipDirs = map[string]bool{
	".git":         true,
	".claude":      true,
	"node_modules": true,
	"site":         true,
	".worktrees":   true,
	"vendor":       true,
}

// findInstallPromptFiles returns every .md and .txt file in the repo
// carrying the begin marker, spelled "../../"-relative.
func findInstallPromptFiles(t *testing.T) []string {
	t.Helper()

	const root = "../.."
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repository root (no go.mod): %v", root, err)
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

// checkInstallPrompt returns one violation per way the marked region
// in content fails to be exactly the one install line.
func checkInstallPrompt(path, content string) []string {
	nBegin := strings.Count(content, installPromptBegin)
	nEnd := strings.Count(content, installPromptEnd)
	if nBegin != 1 || nEnd != 1 {
		return []string{fmt.Sprintf(
			"%s: expected exactly one %q and one %q, found %d and %d",
			path, installPromptBegin, installPromptEnd, nBegin, nEnd)}
	}
	i := strings.Index(content, installPromptBegin)
	j := strings.Index(content, installPromptEnd)
	if j < i {
		return []string{fmt.Sprintf("%s: %q appears before %q",
			path, installPromptEnd, installPromptBegin)}
	}

	var lines []string
	for _, l := range strings.Split(content[i+len(installPromptBegin):j], "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		return []string{fmt.Sprintf(
			"%s: the install-prompt region holds %d non-blank lines; it "+
				"must hold the one line that fetches %s, with the steps "+
				"in that file", path, len(lines), installStepsFile)}
	}

	var violations []string
	line := lines[0]
	if !strings.Contains(line, installStepsURL) {
		violations = append(violations, fmt.Sprintf(
			"%s: the install line does not fetch %s", path, installStepsURL))
	}
	if !strings.Contains(line, "with curl") {
		violations = append(violations, fmt.Sprintf(
			"%s: the install line does not say \"with curl\"; without "+
				"it an agent may fetch the steps through a summarising "+
				"tool that garbles the commands", path))
	}
	// Four spaces keep it a code block in GitHub and MkDocs alike, so
	// it renders as one copyable line rather than as prose.
	if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "     ") {
		violations = append(violations, fmt.Sprintf(
			"%s: the install line must be a four-space indented code "+
				"block", path))
	}
	if strings.TrimSpace(line) != installLine {
		violations = append(violations, fmt.Sprintf(
			"%s: the install line reads\n  %q\nwant\n  %q",
			path, strings.TrimSpace(line), installLine))
	}
	return violations
}

// TestInstallPromptCopiesCarryTheOneLine holds every marked copy, and
// at least the required ones, to the single fetch line.
func TestInstallPromptCopiesCarryTheOneLine(t *testing.T) {
	found := findInstallPromptFiles(t)
	have := map[string]bool{}
	for _, f := range found {
		have[f] = true
	}
	for _, want := range installPromptRequired {
		if !have[want] {
			t.Errorf("%s carries no %s region; the walk found %v",
				want, installPromptBegin, found)
		}
	}
	for _, path := range found {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range checkInstallPrompt(path, string(content)) {
			t.Error(msg)
		}
	}

	// The line fetches main's copy of the file this repo ships.
	if _, err := os.Stat(installStepsPath); err != nil {
		t.Errorf("the install line fetches %s, which is not at the "+
			"repository root: %v", installStepsURL, err)
	}
}

// TestInstallPromptCheckCatchesDrift is the positive control.
func TestInstallPromptCheckCatchesDrift(t *testing.T) {
	wrap := func(body string) string {
		return "intro\n" + installPromptBegin + "\n\n" + body + "\n\n" +
			installPromptEnd + "\n"
	}
	good := wrap("    " + installLine)

	cases := []struct {
		name, doc, want string
	}{
		{"the line passes", good, ""},
		{"a missing region", "no markers", "expected exactly one"},
		{"a second region", good + good, "expected exactly one"},
		{"reversed markers", installPromptEnd + "\n" + installPromptBegin,
			"appears before"},
		{"an empty region", wrap(""), "holds 0 non-blank lines"},
		{"the old multi-line prompt", wrap("    " + installLine +
			"\n    1. Preflight: check curl"), "holds 2 non-blank lines"},
		{"no \"with curl\"", wrap("    " + strings.Replace(installLine,
			" with curl", "", 1)), "\"with curl\""},
		{"a branch instead of main", wrap("    " + strings.Replace(
			installLine, "/main/", "/v1/", 1)), "does not fetch"},
		{"another file", wrap("    " + strings.Replace(installLine,
			"INSTALL.md", "README.md", 1)), "does not fetch"},
		{"prose, not a code block", wrap(installLine),
			"four-space indented"},
		{"a reworded line", wrap("    " + strings.Replace(installLine,
			"follow it", "do it", 1)), "the install line reads"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(checkInstallPrompt("x.md", tc.doc), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected no violations, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected a violation containing %q, got:\n%s",
					tc.want, got)
			}
		})
	}
}

// installHandCommands are commands INSTALL.md runs, each with the
// hand-install page that documents the same command for a person. A
// command changed in one place and not the other fails here.
var installHandCommands = []struct{ command, handDoc string }{
	{"curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh",
		"../../README.md"},
	{"self update", "../../docs/updating.md"},
	{"claude plugin marketplace update pgedge-cli", "../../docs/agent-skills.md"},
	{"claude plugin update pgedge@pgedge-cli", "../../docs/agent-skills.md"},
	{"codex plugin marketplace upgrade pgedge-cli", "../../docs/agent-skills.md"},
	{"codex plugin add pgedge@pgedge-cli", "../../docs/agent-skills.md"},
	{"skills add 'pgEdge/pgedge-cli#", "../../docs/agent-skills.md"},
	// Windows: npm is the install route, and the windows CI job runs
	// the same completion command.
	{"npm install -g @pgedge/cli@beta", "../../README.md"},
	{"completion install --shell powershell --force --write-rc",
		"../../.github/workflows/ci.yml"},
}

// installWindows keeps a Windows agent off install.sh, which reports
// MINGW and MSYS shells as unsupported, and off syntax PowerShell
// rejects.
var installWindows = []string{
	"`MINGW`, `MSYS` or `CYGWIN`",
	"never run it on Windows",
	"`& \"<path>\" version`",
	"`curl.exe`",
}

// installOrder pairs commands whose order INSTALL.md fixes: a plugin
// updated before its marketplace is refreshed stays on the old release.
var installOrder = [][2]string{
	{"claude plugin marketplace update pgedge-cli",
		"claude plugin update pgedge@pgedge-cli --scope <scope>"},
	{"codex plugin marketplace upgrade pgedge-cli",
		"codex plugin add pgedge@pgedge-cli"},
}

// installNoPrompt are spellings that keep a step from waiting on a
// question the agent cannot answer, or from installing for one project
// only. Each was measured to matter: without `| cat` the install script
// blocks on its skills question in a terminal.
var installNoPrompt = []string{
	"install.sh | sh 2>&1 | cat",
	"completion install --force --write-rc </dev/null",
}

var (
	skillFlagRe   = regexp.MustCompile(`\s-s\s+([a-z][a-z0-9-]*)`)
	skillsAddRe   = regexp.MustCompile("`npx -y skills add [^`]*`")
	skillsFlagsRe = regexp.MustCompile(`\s-g(\s.*)?\s-y$`)
)

// checkInstallSteps returns one violation per way the steps disagree
// with the hand-install pages, the shipped skills or the plugin
// manifests, or fail to update an existing install.
func checkInstallSteps(steps string, handDocs map[string]string,
	skills []string, pluginIDs []string) []string {
	var violations []string
	steps = normalizeWhitespace(steps)

	for _, c := range installHandCommands {
		if !strings.Contains(steps, c.command) {
			violations = append(violations, fmt.Sprintf(
				"%s does not run %q", installStepsFile, c.command))
		}
		if !strings.Contains(normalizeWhitespace(handDocs[c.handDoc]), c.command) {
			violations = append(violations, fmt.Sprintf(
				"%s runs %q, which %s no longer documents for a person",
				installStepsFile, c.command, c.handDoc))
		}
	}

	for _, want := range installNoPrompt {
		if !strings.Contains(steps, want) {
			violations = append(violations, fmt.Sprintf(
				"%s no longer runs %q", installStepsFile, want))
		}
	}
	for _, want := range installWindows {
		if !strings.Contains(steps, want) {
			violations = append(violations, fmt.Sprintf(
				"%s no longer says %q on its Windows route",
				installStepsFile, want))
		}
	}
	adds := skillsAddRe.FindAllString(steps, -1)
	if len(adds) == 0 {
		violations = append(violations, fmt.Sprintf(
			"%s runs no `npx -y skills add`", installStepsFile))
	}
	for _, a := range adds {
		if !skillsFlagsRe.MatchString(strings.Trim(a, "`")) {
			violations = append(violations, fmt.Sprintf(
				"%s: %s must pass -g, to install for the user, and end "+
					"with -y, so it asks nothing", installStepsFile, a))
		}
	}

	// Rule: run every step on an existing install, never skip one.
	if !strings.Contains(steps, "Run every step even when `pgedge` is already installed") {
		violations = append(violations, fmt.Sprintf(
			"%s no longer tells the agent to run every step on an "+
				"existing install; skipping one leaves it on an old "+
				"release", installStepsFile))
	}
	for _, pair := range installOrder {
		a, b := strings.Index(steps, pair[0]), strings.Index(steps, pair[1])
		if a < 0 || b < 0 || a > b {
			violations = append(violations, fmt.Sprintf(
				"%s must run %q, then %q", installStepsFile, pair[0], pair[1]))
		}
	}

	// The -s selections are real skills, and together cover them all.
	named := map[string]bool{}
	for _, m := range skillFlagRe.FindAllStringSubmatch(steps, -1) {
		named[m[1]] = true
	}
	shipped := map[string]bool{}
	for _, s := range skills {
		shipped[s] = true
		if !named[s] {
			violations = append(violations, fmt.Sprintf(
				"%s selects skills with -s but never names %q, so no "+
					"product answer installs it", installStepsFile, s))
		}
	}
	for s := range named {
		if !shipped[s] {
			violations = append(violations, fmt.Sprintf(
				"%s names -s %q, which is not a shipped skill",
				installStepsFile, s))
		}
	}

	for _, id := range pluginIDs {
		if !strings.Contains(steps, id) {
			violations = append(violations, fmt.Sprintf(
				"%s does not name the plugin %q that the marketplace "+
					"manifests publish", installStepsFile, id))
		}
	}
	sort.Strings(violations)
	return violations
}

// shippedSkillNames lists the skill directories the plugin ships.
func shippedSkillNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../../plugins/pgedge/skills")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no skill directories found")
	}
	return names
}

// marketplacePluginIDs returns <plugin>@<marketplace> for every
// plugin each marketplace manifest lists.
func marketplacePluginIDs(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, path := range []string{
		"../../.claude-plugin/marketplace.json",
		"../../.agents/plugins/marketplace.json",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, p := range m.Plugins {
			ids = append(ids, p.Name+"@"+m.Name)
		}
	}
	if len(ids) == 0 {
		t.Fatal("no plugins found in the marketplace manifests")
	}
	return ids
}

// TestInstallStepsMatchTheHandInstall holds INSTALL.md to the
// hand-install pages, the shipped skills and the plugin manifests.
func TestInstallStepsMatchTheHandInstall(t *testing.T) {
	steps, err := os.ReadFile(installStepsPath)
	if err != nil {
		t.Fatal(err)
	}
	handDocs := map[string]string{}
	for _, c := range installHandCommands {
		raw, err := os.ReadFile(c.handDoc)
		if err != nil {
			t.Fatal(err)
		}
		handDocs[c.handDoc] = string(raw)
	}
	for _, msg := range checkInstallSteps(string(steps), handDocs,
		shippedSkillNames(t), marketplacePluginIDs(t)) {
		t.Error(msg)
	}
}

// TestInstallStepsCheckCatchesDrift is the positive control.
func TestInstallStepsCheckCatchesDrift(t *testing.T) {
	steps, err := os.ReadFile(installStepsPath)
	if err != nil {
		t.Fatal(err)
	}
	good := string(steps)
	handDocs := map[string]string{}
	for _, c := range installHandCommands {
		handDocs[c.handDoc] += c.command + "\n"
	}
	skills := []string{"pgedge", "pgedge-starfleet", "pgedge-byoc",
		"pgedge-managed", "pgedge-controlplane"}
	ids := []string{"pgedge@pgedge-cli"}

	swap := func(s, a, b string) string {
		const tmp = "\x00"
		s = strings.Replace(s, a, tmp, 1)
		s = strings.Replace(s, b, a, 1)
		return strings.Replace(s, tmp, b, 1)
	}

	cases := []struct {
		name, steps string
		hand        map[string]string
		skills, ids []string
		want        string
	}{
		{"the shipped steps pass", good, handDocs, skills, ids, ""},
		{"a hand page drops a command", good,
			map[string]string{"../../README.md": "nothing"}, skills, ids,
			"no longer documents"},
		{"the steps drop the install script", strings.ReplaceAll(good,
			"install.sh | sh", "install.sh | bash"), handDocs, skills,
			ids, "does not run"},
		{"update before the marketplace refresh", swap(good,
			"claude plugin marketplace update pgedge-cli",
			"claude plugin update pgedge@pgedge-cli --scope <scope>"),
			handDocs, skills, ids, "must run"},
		{"the hang guard dropped", strings.Replace(good,
			"install.sh | sh 2>&1 | cat", "install.sh | sh", 1),
			handDocs, skills, ids, "no longer runs"},
		{"completion may prompt", strings.Replace(good, " </dev/null", "", 1),
			handDocs, skills, ids, "no longer runs"},
		{"skills for one project", strings.Replace(good,
			"-g -s pgedge -s pgedge-controlplane", "-s pgedge -s pgedge-controlplane", 1),
			handDocs, skills, ids, "must pass -g"},
		{"a Windows shell sent to install.sh", strings.Replace(good,
			"`MINGW`, `MSYS` or `CYGWIN`", "`CYGWIN`", 1),
			handDocs, skills, ids, "Windows route"},
		{"the Windows npm route dropped", strings.Replace(good,
			"npm install -g @pgedge/cli@beta", "npm install -g pgedge", 1),
			handDocs, skills, ids, "does not run"},
		{"skills CLI may prompt", strings.Replace(good,
			"-s pgedge-controlplane -y`", "-s pgedge-controlplane`", 1),
			handDocs, skills, ids, "must pass -g"},
		{"the run-every-step rule is gone", strings.Replace(good,
			"Run every step even when", "Skip a step when", 1),
			handDocs, skills, ids, "run every step"},
		{"a skill no answer installs", good, handDocs,
			append(append([]string(nil), skills...), "pgedge-new"), ids,
			"never names \"pgedge-new\""},
		{"a skill that does not ship", good, handDocs, skills[1:], ids,
			"not a shipped skill"},
		{"a renamed marketplace", good, handDocs, skills,
			[]string{"pgedge@pgedge-labs"}, "does not name the plugin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(
				checkInstallSteps(tc.steps, tc.hand, tc.skills, tc.ids), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected no violations, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected a violation containing %q, got:\n%s",
					tc.want, got)
			}
		})
	}
}
