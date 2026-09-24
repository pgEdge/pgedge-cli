package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestParseDryRunAcceptsOnlyChecks(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr string
	}{
		{
			name:  "the bare form's value",
			value: "checks",
			want:  true,
		},
		{
			// The message has to name the missing capability, not just
			// reject the value: a kubectl or helm user typing this needs
			// to learn why it cannot work here.
			name:    "server names the missing capability",
			value:   "server",
			wantErr: "validate endpoint",
		},
		{
			// The spelling a bool flag would have accepted, and the one
			// kubectl had to break. It must be rejected from the start.
			name:    "true is not a value",
			value:   "true",
			wantErr: "checks",
		},
		{
			// Reserved but deliberately unadvertised, so it errors like
			// any other unknown value rather than hinting at a mode that
			// does not exist.
			name:    "offline is reserved, not accepted",
			value:   "offline",
			wantErr: "checks",
		},
		{
			name:    "empty",
			value:   "",
			wantErr: "checks",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDryRun(tc.value)
			if got != tc.want {
				t.Errorf("parseDryRun(%q) = %v, want %v",
					tc.value, got, tc.want)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			// Exit 2, not 1: a bad flag value is a usage mistake.
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Errorf("error is %T, want *UsageError", err)
			}
		})
	}
}

func TestMarkMutatingSetsAnnotationAndFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "create"}
	MarkMutating(cmd)

	if !IsMutating(cmd) {
		t.Error("annotation not set")
	}
	f := cmd.Flags().Lookup(DryRunFlag)
	if f == nil {
		t.Fatalf("--%s not registered", DryRunFlag)
	}
	if f.NoOptDefVal != "checks" {
		t.Errorf("NoOptDefVal = %q, want %q — without it the bare "+
			"--dry-run form demands a value", f.NoOptDefVal, "checks")
	}
	if f.Value.Type() != "string" {
		t.Errorf("flag type = %q, want string: a bool accepts "+
			"--dry-run=true, the spelling kubectl had to break in 1.23",
			f.Value.Type())
	}
	if f.Usage == "" {
		t.Error("no usage text; Short/usage strings are user-facing")
	}
	// The honesty requirement: the help text must say that reads happen
	// and that nothing server-side is validated.
	for _, want := range []string{"credentials", "validates nothing"} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("usage %q does not mention %q", f.Usage, want)
		}
	}
}

// TestMarkMutatingIsIdempotent covers a leaf that gets marked twice
// through a shared constructor: pflag panics on a duplicate flag
// registration, and this CLI never panics.
func TestMarkMutatingPreservesExistingAnnotations(t *testing.T) {
	cmd := &cobra.Command{
		Use:         "create",
		Annotations: map[string]string{"other": "kept"},
	}
	MarkMutating(cmd)
	if cmd.Annotations["other"] != "kept" {
		t.Error("MarkMutating clobbered an unrelated annotation")
	}
	if !IsMutating(cmd) {
		t.Error("annotation not set")
	}
}

func TestIsMutatingFalseByDefault(t *testing.T) {
	if IsMutating(&cobra.Command{Use: "list"}) {
		t.Error("a plain command reported as mutating")
	}
	// An annotation map that exists but carries something else must not
	// read as mutating.
	if IsMutating(&cobra.Command{
		Use:         "list",
		Annotations: map[string]string{"other": "x"},
	}) {
		t.Error("an unrelated annotation read as mutating")
	}
}
