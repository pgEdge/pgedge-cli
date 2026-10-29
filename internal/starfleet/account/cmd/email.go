package cmd

import (
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/output"
)

// matchEmail returns the items whose email equals addr, ignoring case.
func matchEmail[T any](items []T, addr string, email func(T) string) []T {
	var out []T
	for _, it := range items {
		if strings.EqualFold(strings.TrimSpace(email(it)),
			strings.TrimSpace(addr)) {
			out = append(out, it)
		}
	}
	return out
}

// errNoEmailMatch is exit 4, distinct from a failed read's own code.
func errNoEmailMatch(kind, addr string) error {
	return newExitError(fmt.Sprintf("no %s for %s", kind,
		output.Sanitize(addr)), ExitNotFound)
}
