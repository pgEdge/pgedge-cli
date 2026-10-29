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
		if strings.EqualFold(strings.TrimSpace(email(it)), addr) {
			out = append(out, it)
		}
	}
	return out
}

// emailArg trims --email and refuses a blank one.
func emailArg(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", newExitError("--email needs an address", ExitUsage)
	}
	return addr, nil
}

// errNoListBody keeps a 2xx without a parseable list from reading as
// "no match".
func errNoListBody(what string) error {
	return newExitError("list "+what+": the API returned no list",
		ExitGeneral)
}

// errNoEmailMatch is exit 4, distinct from a failed read's own code.
func errNoEmailMatch(kind, addr string) error {
	return newExitError(fmt.Sprintf("no %s for %s", kind,
		output.Sanitize(addr)), ExitNotFound)
}
