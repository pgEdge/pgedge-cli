package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

func TestNormalizeCIDR(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"203.0.113.7", "203.0.113.7/32", true},
		{" 203.0.113.7 ", "203.0.113.7/32", true},
		{"203.0.113.5/24", "203.0.113.0/24", true},
		{"203.0.113.0/24", "203.0.113.0/24", true},
		{"0.0.0.0/0", "0.0.0.0/0", true},
		{"1.2.3.4/0", "0.0.0.0/0", true},
		{"2001:db8::1", "2001:db8::1", false},
		{"2001:db8::/32", "2001:db8::/32", false},
		{"not-an-ip", "not-an-ip", false},
		{"203.0.113.7/33", "203.0.113.7/33", false},
	}
	for _, tc := range cases {
		got, ok := normalizeCIDR(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeCIDR(%q) = %q,%v; want %q,%v",
				tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRuleIndexMatchesTypedThenNormalised(t *testing.T) {
	rules := []api.IPAllowlistRule{
		{Cidr: "203.0.113.7/32"}, {Cidr: "198.51.100.0/24"},
	}
	cases := []struct {
		in   string
		want int
	}{
		{"203.0.113.7/32", 0},
		{"203.0.113.7", 0},
		{"198.51.100.9/24", 1},
		{"192.0.2.1", -1},
		{"garbage", -1},
	}
	for _, tc := range cases {
		if got := ruleIndex(rules, tc.in); got != tc.want {
			t.Errorf("ruleIndex(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestMergeRulesSkipsPresentAndDedupesInputs(t *testing.T) {
	current := []api.IPAllowlistRule{{Cidr: "203.0.113.7/32"}}
	merged, skipped := mergeRules(current,
		[]string{"203.0.113.7", "198.51.100.9", "198.51.100.9/32"}, "office")
	if len(skipped) != 1 || skipped[0] != "203.0.113.7" {
		t.Fatalf("skipped = %v, want [203.0.113.7]", skipped)
	}
	if len(merged) != 2 {
		t.Fatalf("merged = %v, want the existing rule plus one", merged)
	}
	if merged[1].Cidr != "198.51.100.9" {
		t.Errorf("new rule sent as %q, want as typed", merged[1].Cidr)
	}
	if merged[1].Label == nil || *merged[1].Label != "office" {
		t.Errorf("label not applied: %v", merged[1].Label)
	}
	if merged[0].Label != nil {
		t.Errorf("existing rule gained a label")
	}
}

func TestCheckAllowlistBounds(t *testing.T) {
	fifty := make([]api.IPAllowlistRule, 50)
	if err := checkAllowlistBounds(fifty, ""); err != nil {
		t.Errorf("50 rules refused: %v", err)
	}
	var ee *ExitError
	if err := checkAllowlistBounds(append(fifty, api.IPAllowlistRule{}),
		""); !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("51 rules: want exit 2, got %v", err)
	}
	long := ""
	for range 65 {
		long += "é"
	}
	if err := checkAllowlistBounds(nil, long); !asExitError(err, &ee) ||
		ee.Code() != ExitUsage {
		t.Errorf("65-rune label: want exit 2, got %v", err)
	}
	if err := checkAllowlistBounds(nil, long[:len(long)-len("é")]); err != nil {
		t.Errorf("64-rune label refused: %v", err)
	}
}

func TestEndpointName(t *testing.T) {
	if got := endpointName(""); got != "postgres" {
		t.Errorf("empty type = %q, want postgres", got)
	}
	if got := endpointName(api.ServiceConfigServiceType("mcp")); got != "mcp" {
		t.Errorf("mcp = %q", got)
	}
}
