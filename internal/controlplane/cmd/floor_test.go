package cmd

import (
	"strings"
	"testing"
)

func TestParseSemverish(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   [3]int
		wantOK bool
	}{
		{name: "bare", in: "0.10.0", want: [3]int{0, 10, 0}, wantOK: true},
		{name: "v prefix", in: "v0.10.0", want: [3]int{0, 10, 0}, wantOK: true},
		{name: "V prefix", in: "V0.10.0", want: [3]int{0, 10, 0}, wantOK: true},
		{
			name: "git-describe suffix", in: "v0.10.0-3-gabc1234",
			want: [3]int{0, 10, 0}, wantOK: true,
		},
		{
			name: "dirty suffix", in: "v0.9.1-dirty",
			want: [3]int{0, 9, 1}, wantOK: true,
		},
		{
			name: "build metadata", in: "v1.2.3+build.5",
			want: [3]int{1, 2, 3}, wantOK: true,
		},
		{
			name: "missing patch defaults to 0", in: "v0.9",
			want: [3]int{0, 9, 0}, wantOK: true,
		},
		{name: "major only", in: "v2", want: [3]int{2, 0, 0}, wantOK: true},
		{name: "empty string", in: "", wantOK: false},
		{name: "dev build", in: "dev", wantOK: false},
		{name: "docker tag", in: "docker", wantOK: false},
		{name: "four components", in: "1.2.3.4", wantOK: false},
		{name: "trailing dot", in: "0.10.", wantOK: false},
		{name: "leading dot", in: ".10.0", wantOK: false},
		{name: "letters embedded", in: "v0.x.0", want: [3]int{0, 0, 0}, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSemverish(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("parseSemverish(%q) ok = %v, want %v (got %v)",
					tc.in, ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Errorf("parseSemverish(%q) = %v, want %v",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestCompareSemverish(t *testing.T) {
	tests := []struct {
		name string
		a, b [3]int
		want int
	}{
		{name: "equal", a: [3]int{0, 10, 0}, b: [3]int{0, 10, 0}, want: 0},
		{name: "major less", a: [3]int{0, 10, 0}, b: [3]int{1, 0, 0}, want: -1},
		{name: "minor less", a: [3]int{0, 9, 9}, b: [3]int{0, 10, 0}, want: -1},
		{name: "patch less", a: [3]int{0, 10, 0}, b: [3]int{0, 10, 1}, want: -1},
		{name: "major greater", a: [3]int{1, 0, 0}, b: [3]int{0, 10, 0}, want: 1},
		{name: "patch greater", a: [3]int{0, 10, 5}, b: [3]int{0, 10, 0}, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareSemverish(tc.a, tc.b); got != tc.want {
				t.Errorf("compareSemverish(%v, %v) = %d, want %d",
					tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestBelowFloor(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		wantBelow bool
		wantOK    bool
	}{
		{name: "below floor patch", version: "v0.9.1", wantBelow: true, wantOK: true},
		{name: "below floor minor", version: "v0.8.9", wantBelow: true, wantOK: true},
		{name: "at floor", version: "v0.10.0", wantBelow: false, wantOK: true},
		{name: "above floor patch", version: "v0.10.1", wantBelow: false, wantOK: true},
		{name: "above floor minor", version: "v0.11.0", wantBelow: false, wantOK: true},
		{name: "above floor major", version: "v1.0.0", wantBelow: false, wantOK: true},
		{
			name:    "below floor with git-describe suffix",
			version: "v0.9.1-3-gabc1234", wantBelow: true, wantOK: true,
		},
		{
			name:    "at floor with dev suffix stays parseable",
			version: "v0.10.0-dirty", wantBelow: false, wantOK: true,
		},
		{name: "unparseable dev string", version: "dev", wantBelow: false, wantOK: false},
		{name: "empty string", version: "", wantBelow: false, wantOK: false},
		{name: "garbage", version: "not-a-version", wantBelow: false, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			below, ok := belowFloor(tc.version)
			if ok != tc.wantOK {
				t.Fatalf("belowFloor(%q) ok = %v, want %v",
					tc.version, ok, tc.wantOK)
			}
			if ok && below != tc.wantBelow {
				t.Errorf("belowFloor(%q) below = %v, want %v",
					tc.version, below, tc.wantBelow)
			}
		})
	}
}

func TestFloorWarning(t *testing.T) {
	t.Run("below floor names both versions and the policy", func(t *testing.T) {
		msg := floorWarning("v0.9.1")
		if msg == "" {
			t.Fatal("want a warning for v0.9.1, got none")
		}
		if !strings.Contains(msg, "v0.9.1") {
			t.Errorf("warning does not name the server version: %q", msg)
		}
		if !strings.Contains(msg, SupportFloor) {
			t.Errorf("warning does not name the floor: %q", msg)
		}
		if !strings.Contains(msg, "supported: >= "+SupportFloor) {
			t.Errorf("warning does not state the support policy: %q", msg)
		}
	})

	t.Run("at floor is silent", func(t *testing.T) {
		if msg := floorWarning("v0.10.0"); msg != "" {
			t.Errorf("want no warning at the floor, got %q", msg)
		}
	})

	t.Run("above floor is silent", func(t *testing.T) {
		if msg := floorWarning("v1.2.3"); msg != "" {
			t.Errorf("want no warning above the floor, got %q", msg)
		}
	})

	// Positive control for the "unparseable never warns" rule: a
	// version string that IS below the floor numerically but carries
	// a shape belowFloor rejects would, if this guard were removed,
	// produce a warning. Proving a malformed string that would compare
	// below if parsed still yields no warning is the meaningful case;
	// a version already above the floor would stay silent either way.
	t.Run("malformed dev string never warns", func(t *testing.T) {
		for _, v := range []string{"dev", "", "docker", "0.9.1.2.3"} {
			if msg := floorWarning(v); msg != "" {
				t.Errorf("floorWarning(%q) = %q, want no warning for an "+
					"unparseable version", v, msg)
			}
		}
	})
}
