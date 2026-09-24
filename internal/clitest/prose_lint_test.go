package clitest

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Cobra's Short, Long, Example and flag Usage strings are user-facing
// prose — they render both in --help and (via `make docs`) in
// llms.txt — but Vale never scans them: they are Go string literals,
// not comments, and .vale.ini's `go = md` mapping only extracts real
// `//`/`/* */` comments (Vale's tree-sitter Go-comment extractor), and
// their llms.txt rendering sits inside a <!-- BEGIN GENERATED -->
// block, which scripts/lint-docs.sh deliberately blanks (it's machine
// output). This test is the only gate on that text. The banned-word
// list is read from styles/PgEdge/BannedWords.yml itself; the other
// two rule sets are still hand-kept copies, and for those a word
// added to one side and not the other is a silent gap either way.

// bannedRegexp builds the banned-word regex from
// styles/PgEdge/BannedWords.yml, once per process, not from a copy of
// the list. Word-boundary and case-insensitive, matching Vale's
// `existence` check with ignorecase: true — so the regex SEMANTICS
// agree with Vale by construction. Whether the token LIST matches
// what Vale reads is parseBannedWords' contract, held by its
// fail-closed guards, not by construction.
var bannedRegexp = sync.OnceValues(func() (*regexp.Regexp, error) {
	const path = "../../styles/PgEdge/BannedWords.yml"
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	tokens, err := parseBannedWords(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return regexp.MustCompile(
		`(?i)\b(` + strings.Join(tokens, "|") + `)\b`), nil
})

// postgreSQLRe mirrors `styles/PgEdge/PostgreSQL.yml`: case-sensitive,
// so `postgresql.conf`, `postgresql_conf` and the literal journald
// log-source value `postgresql` never match — they're already
// lowercase. There is no help-text equivalent of that style's
// `PostgreSQL License` carve-out (no command's Short/Long/Example/
// flag usage names the license), so none is needed here.
var postgreSQLRe = regexp.MustCompile(`\bPostgreSQL\b`)

// changeMeRe mirrors `styles/PgEdge/ChangeMe.yml`: the wrong spellings
// of the CLI's `CHANGE-ME` sentinel, case-sensitive so the canonical
// spelling never matches.
var changeMeRe = regexp.MustCompile(`\b(CHANGEME|change-me|CHANGE_ME|ChangeMe)\b`)

// checkProse applies all three rule sets to one piece of help text,
// reporting any hit against t. where identifies the field for the
// failure message (e.g. "Long" or "--pg-version usage").
func checkProse(t *testing.T, where, text string) {
	t.Helper()
	if text == "" {
		return
	}
	re, err := bannedRegexp()
	if err != nil {
		t.Fatal(err)
	}
	if m := re.FindString(text); m != "" {
		t.Errorf("%s: banned word %q (see styles/PgEdge/BannedWords.yml)",
			where, m)
	}
	if postgreSQLRe.MatchString(text) {
		t.Errorf("%s: says PostgreSQL, want Postgres "+
			"(see styles/PgEdge/PostgreSQL.yml)", where)
	}
	if m := changeMeRe.FindString(text); m != "" {
		t.Errorf("%s: wrong CHANGE-ME spelling %q "+
			"(see styles/PgEdge/ChangeMe.yml)", where, m)
	}
}

// TestHelpTextProseConformance runs the house prose rules (banned
// words, the Postgres spelling, and `CHANGE-ME` spelling) over every
// command's Short, Long and Example, and every flag's usage string,
// across the full shipped tree. See the package-level doc comment
// above for why this re-implements `styles/PgEdge/*.yml` instead of
// reusing Vale.
func TestHelpTextProseConformance(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	walk(root, func(c *cobra.Command) {
		path := c.CommandPath()
		t.Run(path, func(t *testing.T) {
			checkProse(t, "Short", c.Short)
			checkProse(t, "Long", c.Long)
			checkProse(t, "Example", c.Example)
			c.LocalFlags().VisitAll(func(f *pflag.Flag) {
				checkProse(t, "--"+f.Name+" usage", f.Usage)
			})
		})
	})
}

// parseBannedWords reads the flat `tokens:` block list Vale runs. It
// fails closed on every shape it cannot prove it read the way Vale
// will:
//
//   - a `tokens:` value that is not empty or a comment. The two shapes
//     that would otherwise parse cleanly are the two that must not —
//     an inline flow list, and a `|` block scalar. Under `tokens: |`
//     this parser would read eight tidy items while Vale reads ONE
//     multi-line string and matches nothing. Measured: a probe file
//     yielding four Vale errors yields zero under that one mutation.
//   - an empty list, which would build a regex matching nothing and
//     pass every prose check vacuously.
//   - a list the parse ended BEFORE THE END OF. The list stops at the
//     first line that is not an item, so a comment or blank line
//     between items — what a maintainer writes when the list grows —
//     would silently drop every later word from the help-text gate
//     while Vale keeps reading all of them. An item-shaped line after
//     the stop is therefore an error, not a leftover.
//
// A trailing "\r" is stripped rather than refused: a CRLF-converted
// file would otherwise put the CR inside every token and the regex
// would match nothing, with Vale unaffected.
func parseBannedWords(raw string) ([]string, error) {
	lines := strings.Split(raw, "\n")

	var tokens []string
	inTokens := false
	stopped := -1
	for i, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		if !inTokens {
			if strings.HasPrefix(l, "tokens:") {
				rest := strings.TrimSpace(strings.TrimPrefix(l, "tokens:"))
				if rest != "" && !strings.HasPrefix(rest, "#") {
					return nil, fmt.Errorf(
						"`tokens:` is not a plain block list: %q", l)
				}
				inTokens = true
			}
			continue
		}
		item, ok := strings.CutPrefix(l, "  - ")
		if !ok {
			stopped = i
			break
		}
		tokens = append(tokens, strings.Trim(item, `"'`))
	}

	if len(tokens) == 0 {
		return nil, errors.New("parsed no tokens")
	}
	if stopped >= 0 {
		for _, l := range lines[stopped:] {
			if strings.HasPrefix(strings.TrimRight(l, " \t\r"), "  - ") {
				return nil, fmt.Errorf(
					"the tokens list is interrupted at line %d: %q still "+
						"looks like an item, and every word after the "+
						"interruption would go ungated in help text while "+
						"Vale keeps reading it", stopped+1,
					strings.TrimSpace(l))
			}
		}
	}
	return tokens, nil
}

// TestParseBannedWords pins the parser's fail-closed guards. Each
// rejected shape below was demonstrated to kill the help-text gate
// silently while Vale kept working (measured with a probe file):
// without the guard, a comment or blank line between items gated 3 or 4
// of the 8 words, and a CRLF file gated none.
func TestParseBannedWords(t *testing.T) {
	const good = `extends: existence
level: error
ignorecase: true
tokens:
  - synergy
  - leverage
  - paradigm shift
`

	t.Run("the plain block list parses in full", func(t *testing.T) {
		got, err := parseBannedWords(good)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"synergy", "leverage", "paradigm shift"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("parseBannedWords() = %v, want %v", got, want)
		}
	})

	t.Run("CRLF endings are stripped, not folded into tokens", func(t *testing.T) {
		got, err := parseBannedWords(strings.ReplaceAll(good, "\n", "\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		for _, tok := range got {
			if strings.ContainsAny(tok, "\r") {
				t.Errorf("token %q carries a CR — the regex built from "+
					"it would match nothing", tok)
			}
		}
		if len(got) != 3 {
			t.Errorf("parsed %d tokens from the CRLF file, want 3", len(got))
		}
	})

	t.Run("a key after the list is not an interruption", func(t *testing.T) {
		if _, err := parseBannedWords(good + "scope: text\n"); err != nil {
			t.Errorf("a later top-level key was refused: %v", err)
		}
	})

	for name, raw := range map[string]string{
		"a comment between items": strings.Replace(good,
			"  - leverage", "  # marketing words\n  - leverage", 1),
		"a blank line between items": strings.Replace(good,
			"  - leverage", "\n  - leverage", 1),
		"an inline flow list": "tokens: [synergy, leverage]\n",
		"a block scalar":      "tokens: |\n  - synergy\n  - leverage\n",
		"an empty list":       "tokens:\nlevel: error\n",
		"no tokens key":       "extends: existence\n",
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			if _, err := parseBannedWords(raw); err == nil {
				t.Error("parseBannedWords() = nil error, want one — " +
					"this shape kills the gate silently")
			}
		})
	}
}
