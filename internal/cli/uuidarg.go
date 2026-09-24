package cli

import (
	"fmt"

	"github.com/google/uuid"
)

// ParseUUIDArg parses a UUID the caller typed, returning a UsageError
// on failure so it exits 2, which the exit-code contract reserves for a
// bad invocation; one function keeps each call site off the general code.
//
// It lives here, not in a module, and returns UsageError, not either
// tree's ExitError, because TestPlatformImportsNoModule forbids a
// platform package importing a module, and controlplane's ExitError has
// no constructor. ExitCode maps UsageError before consulting coder.
//
// kind names the resource so the message says which ID was wrong
// ("cluster ID", "invite ID") at all ten call sites. The exit-code tests
// assert codes, not text, so no gate enforces that.
//
// Not for a UUID from an API response: a malformed ID the server sent
// is a server fault and stays a general error, since the caller did not
// type it. A failed parse refuses; nothing falls through to a prefix
// match.
func ParseUUIDArg(arg, kind string) (uuid.UUID, error) {
	id, err := uuid.Parse(arg)
	if err != nil {
		return uuid.UUID{}, &UsageError{
			Msg: fmt.Sprintf("invalid %s %q: %v", kind, arg, err),
		}
	}
	return id, nil
}
