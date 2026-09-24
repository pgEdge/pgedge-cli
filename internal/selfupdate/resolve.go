package selfupdate

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// canonicalTag prefixes tag with "v" if it lacks one. Release tags are
// always "vX.Y.Z[-pre]"; a pinned tag from the user or the baked-in
// Version string are not guaranteed to be.
func canonicalTag(tag string) string {
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}

// newestFirst returns the releases with a valid semver tag, newest
// first. semver.Compare orders pre-release identifiers numerically
// when they are digits ("alpha.10" > "alpha.2"); a lexical sort would
// get that backwards, which is why this goes through x/mod instead of
// sort.Strings on the raw tag.
func newestFirst(releases []Release) []Release {
	var valid []Release
	for _, r := range releases {
		if semver.IsValid(canonicalTag(r.TagName)) {
			valid = append(valid, r)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool {
		return semver.Compare(canonicalTag(valid[i].TagName), canonicalTag(valid[j].TagName)) > 0
	})
	return valid
}

// Resolve picks the release for want ("" => newest by semver,
// pre-releases included; else exact tag, v-prefix tolerant). A release
// whose tag is not valid semver is skipped when ranking, not treated
// as an error.
func Resolve(releases []Release, want string) (Release, error) {
	if len(releases) == 0 {
		return Release{}, fmt.Errorf("no releases available")
	}

	if want == "" {
		ranked := newestFirst(releases)
		if len(ranked) == 0 {
			return Release{}, fmt.Errorf("no releases with a valid version tag")
		}
		return ranked[0], nil
	}

	wantTag := canonicalTag(want)
	for _, r := range releases {
		if canonicalTag(r.TagName) == wantTag {
			return r, nil
		}
	}

	ranked := newestFirst(releases)
	if len(ranked) > 3 {
		ranked = ranked[:3]
	}
	names := make([]string, len(ranked))
	for i, r := range ranked {
		names[i] = r.TagName
	}
	return Release{}, fmt.Errorf("unknown tag %q; newest releases: %s",
		want, strings.Join(names, ", "))
}

// IsCurrent reports whether tag names the running version, tolerant of
// a "v" prefix on either side — the same tolerance version/doctor use.
func IsCurrent(tag, current string) bool {
	return canonicalTag(tag) == canonicalTag(current)
}
