package output

import (
	"strings"
	"time"
)

// OneLine collapses every run of whitespace in s to a single space and
// trims the ends, for values not written for a table cell. API error
// messages arrive wrapped, or with a stack trace or a Postgres DETAIL
// line folded in, and Print would show each newline in a cell as a
// literal \n. Nothing is dropped.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// DerefString returns the pointed-to string, or "" when the pointer is
// nil. Optional API fields are rendered as pointers, and a blank cell
// is the right rendering for absent.
func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// FormatTime renders an API timestamp as its date part. API times are
// RFC3339 strings; tables show the date only.
func FormatTime(t string) string {
	if len(t) <= 10 {
		return t
	}
	return t[:10]
}

// FormatDate renders a time.Time as its date part, for the generated
// fields whose spec declares format: date-time and so arrive parsed
// rather than as RFC3339 strings. A zero time renders blank, matching
// how an absent optional field renders.
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// BoolYesNo renders a boolean as "yes" or "no" for table output.
func BoolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
