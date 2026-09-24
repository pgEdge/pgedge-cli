package selfupdate

import (
	"strings"
	"testing"
)

func TestResolveNewestBySemver(t *testing.T) {
	rs := []Release{{TagName: "v0.5.0-alpha.2"},
		{TagName: "v0.5.0-alpha.10"}, {TagName: "v0.4.9"}}
	got, err := Resolve(rs, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v0.5.0-alpha.10" {
		t.Fatalf("got %s, want v0.5.0-alpha.10 (numeric pre-release "+
			"identifiers; lexical compare would pick alpha.2)", got.TagName)
	}
}

// TestResolveStableBeatsOwnPrerelease pins the GA-selection path: a
// stable tag must outrank its own pre-release in the same candidate
// list. semver.Compare already orders "v0.5.0" > "v0.5.0-alpha.2" (a
// pre-release always sorts below its stable release), but that is an
// x/mod behavior this package depends on, not one it defines, so it
// is pinned here rather than assumed.
func TestResolveStableBeatsOwnPrerelease(t *testing.T) {
	rs := []Release{{TagName: "v0.5.0-alpha.2"},
		{TagName: "v0.5.0"}, {TagName: "v0.4.9"}}
	got, err := Resolve(rs, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v0.5.0" {
		t.Fatalf("got %s, want v0.5.0 (a stable release must beat its "+
			"own pre-release)", got.TagName)
	}
}

func TestResolvePinnedExactTag(t *testing.T) {
	rs := []Release{{TagName: "v0.5.0-alpha.2"},
		{TagName: "v0.5.0-alpha.10"}, {TagName: "v0.4.9"}}
	got, err := Resolve(rs, "v0.5.0-alpha.2")
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v0.5.0-alpha.2" {
		t.Fatalf("got %s, want v0.5.0-alpha.2", got.TagName)
	}
}

func TestResolvePinnedWithoutVPrefix(t *testing.T) {
	rs := []Release{{TagName: "v0.5.0-alpha.2"}, {TagName: "v0.4.9"}}
	got, err := Resolve(rs, "0.5.0-alpha.2")
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v0.5.0-alpha.2" {
		t.Fatalf("got %s, want v0.5.0-alpha.2", got.TagName)
	}
}

func TestResolveUnknownTagListsThreeNewest(t *testing.T) {
	rs := []Release{
		{TagName: "v0.5.0-alpha.2"},
		{TagName: "v0.5.0-alpha.10"},
		{TagName: "v0.4.9"},
		{TagName: "v0.4.8"},
	}
	_, err := Resolve(rs, "v9.9.9")
	if err == nil {
		t.Fatal("want error for unknown tag, got nil")
	}
	msg := err.Error()
	newestFirstWant := []string{"v0.5.0-alpha.10", "v0.5.0-alpha.2", "v0.4.9"}
	lastIdx := -1
	for _, want := range newestFirstWant {
		idx := strings.Index(msg, want)
		if idx < 0 {
			t.Fatalf("error %q missing newest tag %q", msg, want)
		}
		if idx < lastIdx {
			t.Fatalf("error %q lists %q before an earlier tag; want "+
				"newest-first order %v", msg, want, newestFirstWant)
		}
		lastIdx = idx
	}
	if strings.Contains(msg, "v0.4.8") {
		t.Errorf("error %q should list only the three newest tags, not v0.4.8", msg)
	}
}

// TestResolveUnknownTagListsFewerThanThree covers a candidate pool
// with only two valid-semver tags: the message must list exactly
// those two, with no padding to reach three.
func TestResolveUnknownTagListsFewerThanThree(t *testing.T) {
	rs := []Release{
		{TagName: "v0.5.0"},
		{TagName: "v0.4.9"},
		{TagName: "not-a-version"},
	}
	_, err := Resolve(rs, "v9.9.9")
	if err == nil {
		t.Fatal("want error for unknown tag, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"v0.5.0", "v0.4.9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing tag %q", msg, want)
		}
	}
	if strings.Contains(msg, "not-a-version") {
		t.Errorf("error %q should not list the invalid-semver tag", msg)
	}
}

func TestResolveEmptyReleaseList(t *testing.T) {
	if _, err := Resolve(nil, ""); err == nil {
		t.Fatal("want error for empty release list, got nil")
	}
	if _, err := Resolve(nil, "v1.0.0"); err == nil {
		t.Fatal("want error for empty release list with pinned tag, got nil")
	}
}

func TestResolveSkipsInvalidSemverTag(t *testing.T) {
	rs := []Release{{TagName: "not-a-version"}, {TagName: "v1.0.0"}}
	got, err := Resolve(rs, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v1.0.0" {
		t.Fatalf("got %s, want v1.0.0 (invalid-semver release should be "+
			"skipped, not fatal)", got.TagName)
	}
}

func TestResolveNoValidSemverTags(t *testing.T) {
	rs := []Release{{TagName: "not-a-version"}, {TagName: "also-bad"}}
	if _, err := Resolve(rs, ""); err == nil {
		t.Fatal("want error when no release has a valid version tag, got nil")
	}
}

func TestIsCurrent(t *testing.T) {
	cases := []struct {
		name    string
		tag     string
		current string
		want    bool
	}{
		{"tag has v, current does not", "v0.5.0", "0.5.0", true},
		{"neither has v", "0.5.0", "0.5.0", true},
		{"both have v", "v0.5.0", "v0.5.0", true},
		{"different versions", "v0.5.1", "0.5.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCurrent(c.tag, c.current); got != c.want {
				t.Errorf("IsCurrent(%q, %q) = %v, want %v",
					c.tag, c.current, got, c.want)
			}
		})
	}
}
