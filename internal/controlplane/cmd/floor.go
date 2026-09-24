package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// SupportFloor is THE minimum Control Plane server version this CLI
// module supports (decision 2026-08-05; issue #34's dev-snapshot
// mislabeling is why the policy exists at all). It must be kept in
// sync with the `CP_TAG=` pin in openapi/SOURCE — that file is what
// `make vendor-spec` actually vendors from, and this constant is what
// the running binary compares against. TestControlplaneSupportFloorMatchesSource
// (internal/clitest) parses SOURCE's pin and fails the build the two
// drift apart, so bump both together.
const SupportFloor = "0.10.0"

// parseSemverish extracts a leading major.minor[.patch] run from s,
// tolerant of a "v" prefix and any suffix a Control Plane build might
// append (a git-describe tail like "-3-gabc1234", a "-dirty" marker,
// build metadata). It reports ok=false — never a guessed version —
// for anything that does not start with a recognisable numeric
// version: a dev build's descriptive string ("dev", "docker"), an
// empty string, or more than three dot-separated numeric components.
// A missing patch component defaults to 0.
func parseSemverish(s string) (v [3]int, ok bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	end := 0
	for end < len(s) && (s[end] == '.' || (s[end] >= '0' && s[end] <= '9')) {
		end++
	}
	s = s[:end]
	if s == "" {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > len(v) {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n //nolint:gosec // G602: len(parts) <= len(v) is checked above
	}
	return v, true
}

// compareSemverish returns -1, 0, or 1 as a compares below, equal to,
// or above b, component-wise over major, minor, patch.
func compareSemverish(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// belowFloor reports whether version is parseable AND below
// SupportFloor. ok is false whenever version cannot be parsed as a
// semver-ish major.minor.patch triple — callers must treat that as
// "unknown," never as "below," so a malformed or dev version string
// never produces a spurious warning.
func belowFloor(version string) (below, ok bool) {
	v, ok := parseSemverish(version)
	if !ok {
		return false, false
	}
	floor, floorOK := parseSemverish(SupportFloor)
	if !floorOK {
		// SupportFloor is a package constant under this package's own
		// control; a failure here is a programmer error, not a runtime
		// condition to warn about.
		return false, false
	}
	return compareSemverish(v, floor) < 0, true
}

// floorWarning returns a human-readable warning naming both the
// server's reported version and the support floor when version is
// parseable and below SupportFloor, or "" when the server is at or
// above the floor, or when version cannot be parsed (unknown, so no
// warning — see belowFloor).
func floorWarning(version string) string {
	below, ok := belowFloor(version)
	if !ok || !below {
		return ""
	}
	// Escaped, and the reason is not obvious: belowFloor gating this
	// call does NOT constrain the string. parseSemverish truncates at
	// the first character that is not a digit or a dot and parses only
	// that PREFIX, so "0.9.0\nforged line" parses as 0.9.0, reports
	// below-floor, and arrives here whole. The version is whatever the
	// server put in its /version response (#323).
	return fmt.Sprintf(
		"control-plane server version %s is below the supported "+
			"floor %s (supported: >= %s)",
		output.Sanitize(version), SupportFloor, SupportFloor)
}

// warnBelowFloor prints floorWarning's message to rt.Stderr when
// version is below the floor; it is a no-op otherwise.
//
// It is called only from the two places a controlplane command already has a
// server's version in hand without an extra request purely to learn
// it: the HA failover probe in selectBaseURL (which already calls
// probeVersion to pick a live server), and `cp version` (whose entire
// job is fetching the version). Every other resource command's
// single-base-url fast path in selectBaseURL never probes at all, so
// it never has a version to check — adding a probe there just for
// this warning would double that command's round trips, which the
// design deliberately avoids. `controlplane doctor` also knows every probed
// server's version, but reports its own below-floor finding as a
// warning row in its normal output (see newDoctorCmd) rather than via
// this stderr line, so it does not call this function.
//
// selectBaseURL's failover branch (multi-base-url only) calls this
// unconditionally, since it is the only warn site a plain resource
// command (cluster, host, database, task...) ever reaches. `cp
// version` calls it too, but only when resolveConnection reports a
// single base URL — with more than one, selectBaseURL already probed
// and already warned about the very same server on its way to
// picking it, so version.go skips its own call rather than doubling
// the line. That guard is what keeps this "one-time": there is no
// shared mutable state here, just the one command path (version, with
// several --base-url) where two call sites would otherwise both see
// the same already-known version.
func warnBelowFloor(rt *module.Runtime, version string) {
	if msg := floorWarning(version); msg != "" {
		fmt.Fprintln(rt.Stderr, "warning: "+msg)
	}
}
