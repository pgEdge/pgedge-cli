package docgen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CommandProse returns the HAND-WRITTEN prose for one command in a
// reference document: the span between the end of that command's own
// generated block and the start of the next generated block or
// heading. Whitespace is collapsed, so an assertion over the result
// cannot be defeated by a hard wrap falling between two words of the
// phrase it looks for.
//
// The byoc and managed paging gates share it. Each rule stops an
// assertion passing vacuously:
//
//   - the marker must be a whole line, or a marker quoted in prose
//     anchors the span on another command's text.
//   - the marker must be unique: a duplicated cobra registration makes
//     `make docs` emit a duplicate block, and the first would win.
//   - whitespace is collapsed: a line broken between "100 rows by" and
//     "default" defeated a phrase match.
//   - empty prose is an error, or a command whose hand-written half
//     was deleted satisfies every "does not contain" assertion.
//
// It returns an error rather than taking a *testing.T to keep the
// testing import out of this package.
func CommandProse(doc, command string) (string, error) {
	begin := "\n<!-- BEGIN GENERATED: " + command + " -->\n"
	switch n := strings.Count(doc, begin); n {
	case 1:
	case 0:
		return "", fmt.Errorf(
			"no line is exactly the generated marker for %q; the block "+
				"may exist but not be newline-delimited (start of file, "+
				"trailing whitespace, CRLF). Without this anchor a test "+
				"over the result would pass against anything", command)
	default:
		return "", fmt.Errorf(
			"marker for %q appears %d times on a line of its own, want "+
				"exactly 1; the anchor is ambiguous and a test over the "+
				"result could read another command's prose", command, n)
	}
	rest := doc[strings.Index(doc, begin)+len(begin):]

	const end = "<!-- END GENERATED -->"
	e := strings.Index(rest, end)
	if e < 0 {
		return "", fmt.Errorf(
			"generated block for %q is unterminated", command)
	}
	prose := rest[e+len(end):]

	// A leading heading that repeats this command is skipped, or the
	// stop-at-heading below would empty the span. managed repeats the
	// command as a heading after each block; byoc does only for its
	// groups, and controlplane never does.
	//
	// The heading must name the command exactly, as the full path or
	// the last element, or the skip would hand back the next section's
	// prose and defeat the empty-prose error. Measured 2026-09-24:
	// managed's 57 are full paths, byoc's 11 are the last element
	// (`#### cluster`), and none of controlplane's 40 blocks has one.
	//
	// HasSuffix is not enough: `pgedge starfleet managed backup list`
	// ends in "list", so a `database list` with no prose would borrow
	// backup list's. The real documents avoid that only by ordering.
	if start, after, heading, ok := firstHeading(prose); ok &&
		strings.TrimSpace(prose[:start]) == "" {
		fields := strings.Fields(command)
		last := ""
		if len(fields) > 0 {
			last = fields[len(fields)-1]
		}
		if heading == command || (last != "" && heading == last) {
			prose = prose[after:]
		}
	}

	// Stop at whatever comes first: the next command's block, or a
	// heading. Either ends this command's prose.
	if i := strings.Index(prose, "<!-- BEGIN GENERATED"); i >= 0 {
		prose = prose[:i]
	}
	if start, _, _, ok := firstHeading(prose); ok {
		prose = prose[:start]
	}
	if strings.TrimSpace(prose) == "" {
		return "", fmt.Errorf(
			"%q has no hand-written prose after its generated block",
			command)
	}
	return strings.Join(strings.Fields(prose), " "), nil
}

// The two sentence shapes both paging gates assert. They are regexes,
// not literals built from the expected number, because the point is
// to find claims the code does not make. Case-insensitive, or a stale
// claim starting a sentence evades the check.
var (
	// \p{Nd}, not \d: \d is ASCII-only, so full-width digits would
	// read as no claim at all.
	pagingDefaultClaim = regexp.MustCompile(
		`(?i)([\p{Nd}]+) rows by default`)
	// "refused" as well as "clamped": the CLI refuses a --limit above
	// the cap, so a page saying so is true and its number is checked.
	pagingClampClaim = regexp.MustCompile(
		`(?i)above ([\p{Nd}]+) is (?:clamped|refused)`)
)

// CheckPagingClaims is the INVERSE assertion: it reports an error
// unless the page-size and clamp claims a command's prose makes are
// EXACTLY the ones def and clamp can produce. A zero or negative
// argument means the code records no such number, so the prose must
// make no claim of that shape at all.
//
// A Contains over the expected phrase catches only a missing claim,
// not a stale number left beside the fresh one, a drift that has
// shipped twice. "above 200 is clamped" beside the true "above 100 is
// clamped" passed a Contains check, as did a second page size. So
// this finds every sentence of either shape and requires exactly
// {def} and {clamp}.
//
// The caller passes one command's span (CommandProse), so a
// neighbouring verb's page size is out of scope.
func CheckPagingClaims(prose string, def, clamp int) error {
	if err := checkOneClaimShape(
		prose, pagingDefaultClaim, "N rows by default", def,
	); err != nil {
		return err
	}
	return checkOneClaimShape(
		prose, pagingClampClaim, "above N is clamped/refused", clamp)
}

func checkOneClaimShape(
	prose string, re *regexp.Regexp, shape string, want int,
) error {
	var got []int
	for _, m := range re.FindAllStringSubmatch(
		normaliseClaimText(prose), -1,
	) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Reached by a non-ASCII digit, which Atoi rejects, or a
			// number wider than an int. Reported, since dropping it
			// would weaken the set silently.
			return fmt.Errorf("%q claim %q: %v", shape, m[0], err)
		}
		got = append(got, n)
	}

	if want <= 0 {
		if len(got) > 0 {
			return fmt.Errorf(
				"prose makes the %q claim for %v, but the code records "+
					"no such number for this command — a claim nothing "+
					"can produce is stale by definition", shape, got)
		}
		return nil
	}
	if len(got) != 1 || got[0] != want {
		return fmt.Errorf(
			"prose's %q claims are %v, want exactly [%d]; the reference "+
				"and cli.PageDefaults have diverged, and "+
				"cli.PageDefaults is what the truncation hint uses",
			shape, got, want)
	}
	return nil
}

// firstHeading returns the offset of the first ATX heading line in s
// that is not inside a fenced code block, the offset just past that
// line, and the heading's text.
//
// strings.Index(prose, "\n#") is not enough: byoc's example fences sit
// inside the span, so a shell comment such as `# every store in the
// account` would end the span and hide every claim after it. It would
// also match `#tag` and a seven-hash line.
func firstHeading(s string) (start, after int, text string, ok bool) {
	inFence := false
	for off := 0; off < len(s); {
		line, next := s[off:], len(s)
		if nl := strings.IndexByte(s[off:], '\n'); nl >= 0 {
			line, next = s[off:off+nl], off+nl+1
		}
		switch {
		case isFence(line):
			inFence = !inFence
		case !inFence:
			if h, isHeading := atxHeading(line); isHeading {
				return off, next, h, true
			}
		}
		off = next
	}
	return 0, 0, "", false
}

// isFence reports whether line opens or closes a fenced code block.
// Toggling on any fence line rather than matching an opener to its
// closer is deliberate: an info string (```bash) appears only on the
// opener, and these documents nest nothing.
func isFence(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// atxHeading reports whether line is an ATX heading, and returns its
// text. One to six '#' followed by a space, a tab or end of line —
// so `#comment` is not a heading and neither is `#######`.
func atxHeading(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	// At most three columns of indent, per CommonMark: four makes an
	// indented code block, so `    # a comment` must not end the span
	// and hide a paging claim after it. README.md and docs/*.md use
	// 4-space indented blocks, so this is the ordinary case.
	//
	// Columns, not bytes: one leading tab is already a code block,
	// which TestATXHeading pins.
	if indentColumns(line) > 3 {
		return "", false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return "", false
	}
	rest := t[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// normaliseClaimText strips the markdown a paging claim can be wearing
// so that only its words are matched.
//
// The shipped references bold the number, `**25** rows by default`,
// which the regex would not match though a reader sees no difference.
// Backticks go too, and a thousands separator, so "1,000 rows by
// default" is 1000 rather than 000.
//
// A number in words, "two hundred rows by default", stays invisible,
// so a paging claim states its number in digits.
//
// Only these two shapes are normalised. The gates' other assertions
// ("no default page", "no known cap") read the raw span, where split
// emphasis fails loudly: the positive half stops matching.
func normaliseClaimText(s string) string {
	// Zero-width characters are invisible on the page: a ZWSP between
	// the digits and "rows" hid the claim. A no-break space is not
	// here because CommandProse already splits on it (unicode.IsSpace
	// accepts U+00A0), so raw text passed here keeps it.
	for _, zw := range []string{"\u200b", "\u200c", "\u200d", "\ufeff"} {
		s = strings.ReplaceAll(s, zw, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '*', '_', '`':
			// Emphasis and code spans carry no meaning here.
		case ',':
			// A separator only between two digits; every other comma
			// is punctuation and dropping it could join two claims.
			if i > 0 && i+1 < len(s) &&
				isASCIIDigit(s[i-1]) && isASCIIDigit(s[i+1]) {
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// indentColumns measures a line's leading whitespace in COLUMNS, with a
// tab advancing to the next multiple of four, which is how CommonMark
// decides whether a line is an indented code block.
func indentColumns(line string) int {
	col := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col
		}
	}
	return col
}
