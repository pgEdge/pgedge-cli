package output

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitize makes a server-controlled string safe to print to a
// terminal, by escaping every character that would otherwise forge
// structure or misrepresent what the server sent. Unescaped, a newline
// splits one record into several rows, breaking the contract that one
// table row is one record (measured on a managed metrics instance-name
// cell: two forged rows), and an ANSI escape repaints the screen.
// `-o json` never needed this, because encoding/json escapes control
// characters.
//
// The rule is printability, not control-ness. unicode.IsControl misses
// U+202E RIGHT-TO-LEFT OVERRIDE, so a database named
// "invoice<U+202E>gnp.txt" displays as "invoicetxt.png" (Trojan
// Source); U+200B and U+FEFF, which let two different rows render
// identically and so forge an identity; and U+2028 and U+2029, which
// some consumers treat as line breaks. unicode.IsPrint is false for all
// of them and true for every legitimate value but the spaces below, and
// it is the rule strconv.Quote uses, which is why %q handles all this.
//
// Every non-ASCII space is escaped on purpose. IsPrint admits only
// U+0020, so U+00A0, U+2007, U+202F and U+3000 are escaped even though a
// no-break space is legitimate in pasted prose: it imitates a space, the
// forgery U+200B commits, and U+3000 is double-width while tabwriter
// counts runes, so it misaligns the column. unicode.IsGraphic differs
// from IsPrint by exactly category Zs and would otherwise pass the whole
// suite, so TestSanitizeEscapesWhatForgesStructure pins the boundary.
//
// The Unicode tables are the stdlib's, so a code point unassigned today,
// and so escaped, becomes printable when Go ships a newer Unicode
// version: a value's rendering can change on a Go upgrade with no diff
// here.
//
// The backslash is escaped too, which makes the escaping injective.
// Otherwise a literal \n and a real newline render identically, and a
// consumer that un-escapes turns the literal text into a newline. It
// escapes rather than strips because a forged value is exactly when an
// operator needs to see what the server sent.
//
// No ANSI sequence is preserved. ColorEnabled is never assigned outside
// tests, so Bold and ColorStatus return their input unchanged and this
// package emits no escape sequences in production. Preserving any would
// let a value open red with no reset and bleed into every later row and
// the shell prompt. If colour is wired up, this function has to learn
// about it, and so does the header path, which is deliberately not
// sanitized: a bold header would survive while a coloured status
// rendered as its literal escape text.
func Sanitize(s string) string {
	// Fast path: almost every cell is ordinary text, and this keeps the
	// allocation off it. ValidString is needed because an invalid byte
	// decodes to utf8.RuneError, which is printable, so without it a raw
	// 0xff would pass unchanged where the slow path escapes it.
	// TestFastPathMatchesTheSlowPath holds the two paths together.
	if utf8.ValidString(s) && !strings.ContainsFunc(s, needsEscape) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\':
			// What makes the escaping injective. See above.
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == utf8.RuneError && size == 1:
			// Invalid UTF-8: the raw byte, spelled. The size test
			// matters: a legitimate U+FFFD is three bytes, decodes to
			// the same rune and is printable, and must not become
			// twelve characters.
			writeHexEscape(&b, s[i:i+size])
		case needsEscape(r):
			writeHexEscape(&b, s[i:i+size])
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// needsEscape reports whether r must not reach a terminal as itself.
//
// The backslash is included because escaping it is what makes Sanitize
// injective, and excluding it would put every value containing one on
// the fast path unchanged -- where the slow path would have doubled it.
func needsEscape(r rune) bool {
	return r == '\\' || !unicode.IsPrint(r)
}

// writeHexEscape spells raw bytes as \xNN.
func writeHexEscape(b *strings.Builder, raw string) {
	const hex = "0123456789abcdef"
	for i := 0; i < len(raw); i++ {
		b.WriteString(`\x`)
		b.WriteByte(hex[raw[i]>>4])
		b.WriteByte(hex[raw[i]&0x0f])
	}
}
