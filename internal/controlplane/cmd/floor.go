package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// SupportFloor is the minimum supported Control Plane version (decided
// 2026-08-05; dev-snapshot version mislabeling is why a floor exists).
// It must match openapi/SOURCE's CP_TAG pin, the revision the spec is
// vendored from; TestControlplaneSupportFloorMatchesSource enforces it.
const SupportFloor = "0.10.0"

// parseSemverish parses the leading major.minor[.patch] of s, so a
// git-describe tail, "-dirty" or build metadata is ignored. A dev
// build's "dev" or "docker" gets ok=false, never a guessed version.
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

// belowFloor's ok=false means unknown, never below, so a dev version
// string never produces a spurious warning.
func belowFloor(version string) (below, ok bool) {
	v, ok := parseSemverish(version)
	if !ok {
		return false, false
	}
	floor, floorOK := parseSemverish(SupportFloor)
	if !floorOK {
		return false, false
	}
	return compareSemverish(v, floor) < 0, true
}

func floorWarning(version string) string {
	below, ok := belowFloor(version)
	if !ok || !below {
		return ""
	}
	// belowFloor does not constrain the string: parseSemverish reads
	// only the numeric prefix, so "0.9.0\nforged line" arrives whole.
	return fmt.Sprintf(
		"control-plane server version %s is below the supported "+
			"floor %s (supported: >= %s)",
		output.Sanitize(version), SupportFloor, SupportFloor)
}

// warnBelowFloor is called only where a version is already in hand:
// the multi-URL failover walk in selectBaseURLContext, and
// `controlplane version`, which warns only for a single base URL
// because with more the walk already warned about the same server. A
// single-URL command never probes, and a probe only for this warning
// would double its round trips. doctor reports the floor as its own
// row instead.
func warnBelowFloor(rt *module.Runtime, version string) {
	if msg := floorWarning(version); msg != "" {
		fmt.Fprintln(rt.Stderr, "warning: "+msg)
	}
}
