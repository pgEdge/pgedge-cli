package output

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The value from the original end-to-end probe. It forged two metric
// rows on stdout that a reader could not tell from real ones, which is
// the whole defect in one string.
const forgedInstanceName = "evil-1-1\ncpu_seconds_total\t0\n" +
	"STATUS\tHEALTHY"

// The runes that are NOT control characters and defeated the earlier
// unicode.IsControl rule, named rather than written literally: a raw
// U+0085 in a source literal is a staticcheck error (ST1018), and the
// rest are invisible in a diff.
const (
	rtlOverride      = "\u202e" // Trojan Source
	ltrOverride      = "\u202d"
	zeroWidth        = "\u200b"
	byteOrder        = "\ufeff"
	lineSep          = "\u2028"
	paraSep          = "\u2029"
	softHyphen       = "\u00ad"
	nextLine         = "\u0085" // C1, and this one IS a control
	replacement      = "\ufffd" // legitimate, printable, three bytes
	combiningAcc     = "e\u0301"
	noBreakSpace     = "\u00a0" // legitimate in prose, escaped anyway
	ideographicSpace = "\u3000" // double-width
)

func TestSanitizeEscapesWhatForgesStructure(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"ordinary text is untouched": {
			"my-database-1", "my-database-1"},
		"a newline cannot start a row": {"a\nb", `a\nb`},
		"a tab cannot start a column":  {"a\tb", `a\tb`},
		"a carriage return cannot rewrite the line": {
			"a\rb", `a\rb`},
		"the probe's forged rows collapse to one cell": {
			forgedInstanceName,
			`evil-1-1\ncpu_seconds_total\t0\nSTATUS\tHEALTHY`},
		"an arbitrary CSI is escaped": {"a\x1b[2Jb", `a\x1b[2Jb`},
		"a bare ESC is escaped":       {"a\x1bb", `a\x1bb`},
		"a NUL is escaped":            {"a\x00b", `a\x00b`},
		"a DEL is escaped":            {"a\x7fb", `a\x7fb`},
		"a C1 control is escaped": {
			"a" + nextLine + "b", `a\xc2\x85b`},
		"invalid UTF-8 is escaped rather than passed through": {
			"a\xffb", `a\xffb`},

		// None of these is a control character, and every one passed
		// through the earlier rule. See Sanitize.
		"a right-to-left override cannot disguise a name": {
			"invoice" + rtlOverride + "gnp.txt",
			`invoice\xe2\x80\xaegnp.txt`},
		"a left-to-right override is escaped": {
			"a" + ltrOverride + "b", `a\xe2\x80\xadb`},
		"a zero-width space cannot forge an identity": {
			"prod" + zeroWidth + "db", `prod\xe2\x80\x8bdb`},
		"a byte-order mark is escaped": {
			byteOrder + "db", `\xef\xbb\xbfdb`},
		"a line separator is escaped": {
			"a" + lineSep + "b", `a\xe2\x80\xa8b`},
		"a paragraph separator is escaped": {
			"a" + paraSep + "b", `a\xe2\x80\xa9b`},
		"a soft hyphen is escaped": {
			"a" + softHyphen + "b", `a\xc2\xadb`},

		// The non-ASCII spaces, named rather than left to the rule:
		// unicode.IsGraphic differs from IsPrint by exactly category
		// Zs, so switching to it changes nothing else and survived the
		// entire suite until these existed.
		"a no-break space is escaped": {
			"a" + noBreakSpace + "b", `a\xc2\xa0b`},
		"an ideographic space is escaped": {
			"a" + ideographicSpace + "b", `a\xe3\x80\x80b`},
		"an ordinary space is not escaped": {"a b", "a b"},

		// Injectivity. Without escaping the backslash these two inputs
		// produced the SAME output, so "the value is recoverable" was
		// false -- and a consumer that un-escaped would turn literal
		// text into a real newline.
		"a lone backslash is doubled":    {`a\b`, `a\\b`},
		"the literal text backslash-n":   {`a\nb`, `a\\nb`},
		"an already-escaped value again": {`a\\nb`, `a\\\\nb`},

		// No ANSI survives any more. These were an allowlist until a
		// review showed this package emits none of them in production.
		"our own reset does not survive": {"\033[0m", `\x1b[0m`},
		"our own bold does not survive":  {"\033[1m", `\x1b[1m`},
		"a coloured status is escaped whole": {
			"\033[32mavailable\033[0m",
			`\x1b[32mavailable\x1b[0m`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q)\n got %q\nwant %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestNothingPrintableIsEscaped is the negative control for the
// printability rule, which is broad enough to need one. A legitimate
// U+FFFD is the case that caught the first attempt: it is printable,
// three bytes long, and every upstream system substitutes it for
// undecodable input, so it arrives in real values.
func TestNothingPrintableIsEscaped(t *testing.T) {
	for _, in := range []string{
		"simple", "with space", "dash-and_underscore.dot",
		"\u65e5\u672c\u8a9e", "\U0001f642", combiningAcc, replacement,
		"quote\"and'apostrophe", "brace{}bracket[]", "t\u00e4glich",
	} {
		if got := Sanitize(in); got != in {
			t.Errorf("Sanitize(%q) = %q, but every rune in it is "+
				"printable", in, got)
		}
	}
}

// TestSanitizeIsInjective is the property the earlier idempotence test
// only pretended to check. That test passed for `return s`, and it
// passed BECAUSE the backslash was unescaped -- so it codified the
// ambiguity it should have caught.
//
// Distinct inputs must give distinct outputs, or a reader cannot tell
// which value the server sent.
func TestSanitizeIsInjective(t *testing.T) {
	inputs := []string{
		"a\nb", `a\nb`, `a\\nb`,
		"a\x1b[2Jb", `a\x1b[2Jb`,
		"a\tb", `a\tb`,
		"plain", `pl\ain`,
		"invoice" + rtlOverride + "gnp.txt",
		`invoice\xe2\x80\xaegnp.txt`,
		"a\xffb", `a\xffb`,
	}
	seen := map[string]string{}
	for _, in := range inputs {
		out := Sanitize(in)
		if prev, ok := seen[out]; ok {
			t.Errorf("not injective: %q and %q both give %q, so a "+
				"reader cannot tell which the server sent",
				prev, in, out)
			continue
		}
		seen[out] = in
	}
}

// TestFastPathMatchesTheSlowPath pins the two implementations together.
// The fast path returns its input unchanged, so any input it accepts
// that the slow path would have altered is a silent divergence -- and
// the backslash is exactly such a character, which is why needsEscape
// includes it even though a backslash is printable.
func TestFastPathMatchesTheSlowPath(t *testing.T) {
	for _, in := range []string{
		"plain", `back\slash`, "a\nb", "a" + rtlOverride + "b",
		replacement, combiningAcc, "", "a\xffb",
		strings.Repeat("x", 1000), `\\`, "\u65e5",
		// Pins the IsPrint/IsGraphic boundary. sanitizeSlow hard-codes
		// the rule, so this one input is an independent oracle for it.
		"a" + noBreakSpace + "b",
	} {
		if got, slow := Sanitize(in), sanitizeSlow(in); got != slow {
			t.Errorf("Sanitize(%q) = %q but the slow path gives %q",
				in, got, slow)
		}
	}
}

// sanitizeSlow is Sanitize with the fast path removed, for the test
// above only.
func sanitizeSlow(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == utf8.RuneError && size == 1:
			writeHexEscape(&b, s[i:i+size])
		case !unicode.IsPrint(r):
			writeHexEscape(&b, s[i:i+size])
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// forgingRow is a row whose cells carry the probe's value.
type forgingRow struct{ a, b string }

func (r forgingRow) Columns() []string { return []string{r.a, r.b} }

// TestOneRowPrintsOneLine is the assertion the contract actually makes,
// and the one a cell-level test cannot make on its own: whatever a
// server sends, a table of N records is N lines plus a header.
//
// The forged value sits in the FIRST cell of one row and the LAST cell
// of another, because a review sanitized only cell index 0 and the
// whole suite stayed green -- 1225 subtests, rc=0. Real hostile fields
// are rarely column 0: controlplane's task table carries a status at index 2 and
// managed's service table an endpoint at index 3.
func TestOneRowPrintsOneLine(t *testing.T) {
	// The ROW COUNT is a dimension too, and one row is the common
	// case: 27 production sites render a single-element []output.Row
	// -- every get/show verb across byoc, managed and controlplane -- which is
	// exactly where a server-controlled name lands with nothing else
	// on screen to compare it against. A review sanitized only
	// `len(rows) == 1` tables and the whole suite stayed green,
	// because a three-row fixture never exercises that shape.
	for _, n := range []int{1, 3} {
		for _, format := range []string{"text", "table"} {
			t.Run(fmt.Sprintf("%d rows/%s", n, format), func(t *testing.T) {
				var buf bytes.Buffer
				r := &Renderer{Format: format, Out: &buf, Err: &buf}
				// The forged value goes in the FIRST cell of the first
				// row and the LAST cell of the last, so neither the
				// column index nor the row index can be special-cased.
				// At n == 1 those coincide, which is the case the
				// row-count mutation exploited.
				rows := make([]Row, 0, n)
				for i := range n {
					switch i {
					case 0:
						rows = append(rows,
							forgingRow{forgedInstanceName, "1"})
					case n - 1:
						rows = append(rows,
							forgingRow{"honest", forgedInstanceName})
					default:
						rows = append(rows, forgingRow{"honest", "2"})
					}
				}
				if err := r.Print(rows,
					[]string{"NAME", "VALUE"}); err != nil {
					t.Fatal(err)
				}
				got := strings.TrimRight(buf.String(), "\n")
				lines := strings.Split(got, "\n")
				if len(lines) != n+1 {
					t.Errorf("%d rows plus a header printed %d lines, "+
						"want %d — a server string is forging rows:"+
						"\n%s", n, len(lines), n+1, got)
				}
				// Recoverable from every cell that carried it, which
				// is why this escapes rather than strips. At n == 1 the
				// single row holds it once; above that, twice.
				want := 2
				if n == 1 {
					want = 1
				}
				if c := strings.Count(got, `evil-1-1\n`); c != want {
					t.Errorf("the offending value survives in %d "+
						"cells, want %d:\n%s", c, want, got)
				}
			})
		}
	}
}

// TestStructuredFormatsWereNeverAffected is the negative control for
// the scope claim, and it covers BOTH encoders.
//
// An earlier version asserted json alone while the prose claimed yaml
// too, on the strength of encoding/json's behaviour -- and yaml.v3 is a
// different encoder that renders this value as a literal block scalar
// carrying RAW newlines and a raw tab. So the yaml claim has to be
// about what a parser recovers rather than about the bytes, and that is
// what this asserts.
func TestStructuredFormatsWereNeverAffected(t *testing.T) {
	t.Run("json escapes control characters itself", func(t *testing.T) {
		var buf bytes.Buffer
		r := &Renderer{Format: "json", Out: &buf, Err: &buf}
		if err := r.Print(
			map[string]string{"name": forgedInstanceName},
			nil); err != nil {
			t.Fatal(err)
		}
		body := strings.TrimSuffix(buf.String(), "\n")
		// ANY control character, not one two-byte pair: the earlier
		// assertion looked for "\n\t" specifically, an artefact of this
		// probe string that would have passed a lone raw tab or ESC.
		if i := strings.IndexFunc(body, unicode.IsControl); i >= 0 {
			t.Errorf("a raw control character reached json output at "+
				"offset %d: %q", i, body)
		}
		if !strings.Contains(body, `\n`) {
			t.Errorf("expected the encoder's own escaping: %q", body)
		}
	})

	t.Run("yaml round-trips the value", func(t *testing.T) {
		var buf bytes.Buffer
		r := &Renderer{Format: "yaml", Out: &buf, Err: &buf}
		if err := r.Print(
			map[string]string{"name": forgedInstanceName},
			nil); err != nil {
			t.Fatal(err)
		}
		var back map[string]string
		if err := yaml.Unmarshal(buf.Bytes(), &back); err != nil {
			t.Fatalf("yaml output does not parse: %v\n%s",
				err, buf.String())
		}
		if back["name"] != forgedInstanceName {
			t.Errorf("yaml round-trip changed the value:\n got %q\n"+
				"want %q", back["name"], forgedInstanceName)
		}
	})
}
