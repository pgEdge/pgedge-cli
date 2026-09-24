package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// The helpers here tell an omitted optional flag from one given a value
// the caller got wrong, which `if v != ""` cannot: `--region "$REGION"`
// with the variable unset sent no region and exited 0, and a backup
// store's region has no update path in the spec, the generated client
// or the CLI. Only Flags().Changed tells the two apart, so every helper
// takes the flag set rather than the value.
//
// They live in internal/cli, as ParseUUIDArg does, because the
// controlplane and starfleet modules cannot share an error type and
// UsageError is the one shape both trees can return.
//
// They are not for a value read back from an API response: an empty
// field the server sent is not a flag the caller can omit.

// OptionalStringFlag reports whether an optional string flag should be
// sent, refusing an explicitly empty value.
//
// remedy completes "--flag given an empty value: " and must say both
// what a real value looks like and what omitting the flag does, as
// --config's and --pg-version's do. Omitting the flag stays a valid way
// to take the API's default, so send is false with no error.
func OptionalStringFlag(fs *pflag.FlagSet, flag, remedy string) (
	value string, send bool, err error,
) {
	v, _ := fs.GetString(flag)
	if !fs.Changed(flag) {
		return "", false, nil
	}
	// TrimSpace, not v == "": `--region "$REGION "` with the variable
	// unset is empty too. The value itself is not trimmed: refusing a
	// blank is this helper's job, rewriting a real value is not.
	if strings.TrimSpace(v) == "" {
		return "", false, &UsageError{Msg: fmt.Sprintf(
			"--%s given an empty value: %s", flag, remedy)}
	}
	return v, true, nil
}

// RequiredStringFlag returns a required string flag's value, refusing a
// blank one.
//
// Cobra's MarkFlagRequired tests only Changed, so `--size ""` satisfies
// it and the blank reaches the request body. There is no "omit it"
// branch to offer, so the remedy says only what a real value looks
// like. A blank stays exit 2 even when the flag's vocabulary can only
// be checked against a list the CLI has to fetch.
func RequiredStringFlag(fs *pflag.FlagSet, flag, remedy string) (
	string, error,
) {
	v, _ := fs.GetString(flag)
	if strings.TrimSpace(v) == "" {
		return "", &UsageError{Msg: fmt.Sprintf(
			"--%s given an empty value: %s", flag, remedy)}
	}
	return v, nil
}

// OptionalIntFlag is the same contract for a numeric flag, refusing
// anything below lowest.
//
// An explicit 0 is refused along with a negative. No caller means
// either, and the API's answer is worse than a rejection: byoc created
// a cluster for --volume-size -5 with its 100 GB default and reported
// success. Omitting the flag is how you ask for the default.
func OptionalIntFlag(fs *pflag.FlagSet, flag string, lowest int) (
	value int, send bool, err error,
) {
	return OptionalIntFlagInRange(fs, flag, lowest, NoUpperBound)
}

// NoUpperBound is the highest to pass OptionalIntFlagInRange when the
// spec declares no maximum for a parameter.
//
// The server may still clamp; the contract just publishes no bound, and
// a bound the CLI invented would refuse a value the API accepts the day
// the API raises its cap.
//
// A clamp is visible only where PrintTruncationHint reports it, as
// `managed task list --limit 500` returning 100 rows does. That is per
// verb, not a property of this constant:
//
//   - byoc's seven --limit verbs, and managed's `task list`,
//     `backup list` and `database branch list`, print the hint whenever
//     a page comes back full.
//   - `managed database list` prints one only when the caller set
//     --limit. The endpoint applies no default page, so an unbounded
//     read is the whole result and cannot have been truncated.
//   - `controlplane task list` never prints one, so a clamp there is
//     invisible to the caller as well as absent from the contract.
//   - The unpaginated catalogue verbs take no paging flags.
const NoUpperBound = 0

// LimitLowest and OffsetLowest are the floors for the two paging flags
// every list verb in every module spells the same way.
//
// A limit of zero asks for a page of nothing, which no caller means and
// which the API answers with a full default page instead of an error.
// An offset of zero is the first page, an ordinary value, so it is
// accepted and sent.
//
// Unlike the per-endpoint maxima these are not spec-derived, so they
// live here rather than in a module: managed declares exactly these two
// minima and its paging spec test asserts the agreement, while byoc and
// controlplane declare no paging bounds and would otherwise each invent
// the same two numbers.
const (
	LimitLowest  = 1
	OffsetLowest = 0
)

// OptionalIntFlagInRange is OptionalIntFlag with a declared maximum.
//
// highest must come from the endpoint's OWN parameter in the vendored
// spec, never from a constant shared across endpoints: `limit` is
// {min 1, max 1000} on /managed/v1/databases and {min 1, max 100} on
// /managed/v1/backups. Pass NoUpperBound where the spec declares none.
//
// The refusal is spec-driven, and its message says only that. What the
// server does with an over-large value on these two endpoints is
// unmeasured. The measured clamp (`--limit 500` returning 100 rows on
// a Managed dev tenant) is on /managed/v1/tasks, which declares no
// maximum and never reaches this branch.
func OptionalIntFlagInRange(
	fs *pflag.FlagSet, flag string, lowest, highest int,
) (value int, send bool, err error) {
	v, _ := fs.GetInt(flag)
	if !fs.Changed(flag) {
		return 0, false, nil
	}
	// Both messages share the prefix "--<flag> must be at ", which
	// internal/clitest's refusedTheFlag matches to tell this refusal
	// from any other exit 2, such as an unfilled required flag.
	// Changing the prefix silently narrows the five paging gates that
	// call it, so change them together.
	if v < lowest {
		return 0, false, &UsageError{Msg: fmt.Sprintf(
			"--%s must be at least %d (got %d), or omit the flag to "+
				"use the default", flag, lowest, v)}
	}
	if highest != NoUpperBound && v > highest {
		return 0, false, &UsageError{Msg: fmt.Sprintf(
			"--%s must be at most %d (got %d): that is the maximum "+
				"this endpoint's contract declares", flag, highest, v)}
	}
	return v, true, nil
}

// ParseTimeFlag parses an RFC3339 flag value, naming the flag and the
// shape expected rather than echoing Go's layout string.
//
// flag carries its dashes, because callers pass a literal.
func ParseTimeFlag(flag, value string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, &UsageError{Msg: fmt.Sprintf(
			"invalid %s value %q: expected an RFC3339 timestamp "+
				"(e.g. 2026-08-01T00:00:00Z)", flag, value)}
	}
	return at, nil
}
