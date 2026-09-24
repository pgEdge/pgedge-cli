package output

import "testing"

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		name string
		in   *int
		want string
	}{
		{"nil renders blank", nil, ""},
		{"zero", intPtr(0), "0 B"},
		{"bytes below a KiB", intPtr(999), "999 B"},
		{"exactly one KiB", intPtr(1024), "1.0 KiB"},
		{"one MiB", intPtr(1048576), "1.0 MiB"},
		{"one and a half MiB", intPtr(1572864), "1.5 MiB"},
		{"one GiB", intPtr(1073741824), "1.0 GiB"},
		{"one TiB", intPtr(1099511627776), "1.0 TiB"},
		{"negative is passed through", intPtr(-5), "-5 B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatBytes(tc.in); got != tc.want {
				t.Errorf("FormatBytes(%v) = %q, want %q",
					tc.in, got, tc.want)
			}
		})
	}
}

func intPtr(i int) *int { return &i }
