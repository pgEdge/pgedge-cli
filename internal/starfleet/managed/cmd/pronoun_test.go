package cmd

import "testing"

func TestPronoun(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "it"},
		{2, "them"},
	} {
		if got := pronoun(tc.n); got != tc.want {
			t.Errorf("pronoun(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
