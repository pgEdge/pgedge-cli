package metricfmt

import "testing"

// The float64 cases are the reason this package exists: encoding/json
// decodes every number into float64, so a byte count and a process
// count arrive the same way and %v would render one of them in
// scientific notation.
func TestValue(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil renders empty", nil, ""},
		{"string passes through", "us-east-1a", "us-east-1a"},
		{"empty string stays empty", "", ""},
		{"true", true, "true"},
		{"false", false, "false"},
		{"integral float loses the point zero", float64(207), "207"},
		{"byte count is not scientific", float64(474362391), "474362391"},
		{"epoch milliseconds survive intact",
			float64(1786991610000), "1786991610000"},
		{"fractional float keeps its digits", 270.734698, "270.734698"},
		{"negative integral float", float64(-5), "-5"},
		{"an int is not a float64 and falls through", 42, "42"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Value(tc.in); got != tc.want {
				t.Errorf("Value(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
