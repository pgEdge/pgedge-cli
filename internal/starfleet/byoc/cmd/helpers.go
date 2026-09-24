package cmd

import (
	"strings"
)

// joinStrings joins a slice of strings with ", " as the separator.
func joinStrings(ss []string) string {
	return strings.Join(ss, ", ")
}

// derefStrings returns the pointed-to slice, or nil when the pointer
// is nil. Optional API list fields arrive as pointers and may be
// explicitly null rather than absent, so callers that only want to
// range or join need one place to flatten both cases.
func derefStrings(ss *[]string) []string {
	if ss == nil {
		return nil
	}
	return *ss
}
