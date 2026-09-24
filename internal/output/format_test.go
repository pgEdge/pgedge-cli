package output

import (
	"testing"
	"time"
)

func TestDerefString(t *testing.T) {
	val := "us-east-1"
	empty := ""

	tests := []struct {
		name string
		in   *string
		want string
	}{
		{"nil", nil, ""},
		{"value", &val, "us-east-1"},
		{"pointer to empty", &empty, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DerefString(tt.in); got != tt.want {
				t.Errorf("DerefString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2024-03-15T10:30:00Z", "2024-03-15"},
		// Exactly 10 chars: returned whole, not truncated further.
		{"2024-03-15", "2024-03-15"},
		// 11 chars is the first length that truncates.
		{"2024-03-15T", "2024-03-15"},
		{"short", "short"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := FormatTime(tt.input)
			if got != tt.want {
				t.Errorf("FormatTime(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name  string
		input time.Time
		want  string
	}{
		{
			name: "UTC timestamp keeps its date",
			input: time.Date(
				2026, 8, 4, 6, 5, 0, 0, time.UTC),
			want: "2026-08-04",
		},
		{
			// The date rendered is the wire value's own, not a
			// conversion — matching how FormatTime truncates the
			// string it was given.
			name: "offset timestamp keeps its local date",
			input: time.Date(
				2026, 8, 4, 23, 30, 0, 0, time.FixedZone("", -7*3600)),
			want: "2026-08-04",
		},
		{
			name:  "zero time renders blank",
			input: time.Time{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDate(tt.input)
			if got != tt.want {
				t.Errorf("FormatDate(%v) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestBoolYesNo(t *testing.T) {
	if got := BoolYesNo(true); got != "yes" {
		t.Errorf("BoolYesNo(true) = %q, want \"yes\"", got)
	}
	if got := BoolYesNo(false); got != "no" {
		t.Errorf("BoolYesNo(false) = %q, want \"no\"", got)
	}
}

func TestOneLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"already one line", "disk full", "disk full"},
		{"newline becomes a space", "a\nb", "a b"},
		{
			// The shape the Control Plane actually sends: a message
			// with a folded-in Postgres DETAIL line.
			name: "folded detail line",
			in:   "could not start postgres\n  DETAIL:  disk full\n",
			want: "could not start postgres DETAIL: disk full",
		},
		{"tabs and runs collapse", "a\t\t b  \r\n c", "a b c"},
		{"leading and trailing space trimmed", "  padded  ", "padded"},
		{"whitespace only", " \n\t ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OneLine(tc.in); got != tc.want {
				t.Errorf("OneLine(%q) = %q, want %q",
					tc.in, got, tc.want)
			}
		})
	}
}
