package cmd

import "testing"

// TestFormatTime moved to internal/output with the function itself;
// see internal/output/format_test.go. joinStrings stays local to this
// tree because nothing outside byoc renders a joined region list.

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{"empty", nil, ""},
		{"single", []string{"us-east-1"}, "us-east-1"},
		{"multiple", []string{"us-east-1", "eu-west-1"}, "us-east-1, eu-west-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinStrings(tt.input)
			if got != tt.want {
				t.Errorf("joinStrings(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
