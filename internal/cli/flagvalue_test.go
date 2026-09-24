package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// newFlagSet builds a flag set carrying the three flag types these
// helpers read, parsed from args so Changed() reports what a real
// invocation would.
func newFlagSet(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("region", "", "")
	fs.Int("capacity", 0, "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	return fs
}

// wantUsage asserts err is a *UsageError — the shape ExitCode maps to
// 2 — and that its message names the flag. Asserting the code and not
// only the type is what stops a helper returning a plain error that
// silently exits 1, which is the defect this whole change closes.
func wantUsage(t *testing.T, err error, flag string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a usage error naming %s, got nil", flag)
	}
	if got := ExitCode(err); got != ExitUsage {
		t.Errorf("exit = %d, want %d; err=%v", got, ExitUsage, err)
	}
	if !strings.Contains(err.Error(), flag) {
		t.Errorf("message %q does not name %s", err.Error(), flag)
	}
}

func TestOptionalStringFlag(t *testing.T) {
	const remedy = "name a region, or omit the flag to let the API choose"

	t.Run("omitted sends nothing and is not an error", func(t *testing.T) {
		fs := newFlagSet(t)
		v, send, err := OptionalStringFlag(fs, "region", remedy)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if send {
			t.Error("send = true for an omitted flag; the field must " +
				"be left out so the API applies its own default")
		}
		if v != "" {
			t.Errorf("value = %q, want empty", v)
		}
	})

	t.Run("an explicitly empty value is a usage error", func(t *testing.T) {
		fs := newFlagSet(t, "--region", "")
		_, send, err := OptionalStringFlag(fs, "region", remedy)
		wantUsage(t, err, "--region")
		if send {
			t.Error("send = true alongside an error")
		}
		if !strings.Contains(err.Error(), remedy) {
			t.Errorf("message %q drops the remedy", err.Error())
		}
	})

	// A trailing space on an unset shell variable, or a copy-paste
	// that picked one up, used to reach a create body whose field has
	// no update path anywhere.
	for _, v := range []string{" ", "   ", "\t", "\n"} {
		t.Run("a whitespace-only value is a usage error", func(t *testing.T) {
			fs := newFlagSet(t, "--region", v)
			_, send, err := OptionalStringFlag(fs, "region", remedy)
			wantUsage(t, err, "--region")
			if send {
				t.Error("send = true alongside an error")
			}
		})
	}

	t.Run("a real value is sent", func(t *testing.T) {
		fs := newFlagSet(t, "--region", "us-east-1")
		v, send, err := OptionalStringFlag(fs, "region", remedy)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !send || v != "us-east-1" {
			t.Errorf("got (%q, %v), want (\"us-east-1\", true)", v, send)
		}
	})

	// The value itself is NOT trimmed. Refusing a blank one is this
	// helper's job; silently rewriting a real one is not, and a caller
	// who typed a padded value should see it sent as typed rather than
	// quietly altered.
	t.Run("a padded real value is passed through as typed", func(t *testing.T) {
		fs := newFlagSet(t, "--region", " us-east-1 ")
		v, send, err := OptionalStringFlag(fs, "region", remedy)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !send || v != " us-east-1 " {
			t.Errorf("got (%q, %v), want the value unchanged", v, send)
		}
	})
}

func TestOptionalIntFlag(t *testing.T) {
	t.Run("omitted sends nothing", func(t *testing.T) {
		fs := newFlagSet(t)
		v, send, err := OptionalIntFlag(fs, "capacity", 1)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if send || v != 0 {
			t.Errorf("got (%d, %v), want (0, false)", v, send)
		}
	})

	// An explicit 0 and a negative are both refused. "Omitted" stays
	// the way to say "let the API choose"; an explicit out-of-range
	// number is a typo, and the API answers it by substituting a
	// default and reporting success.
	for _, v := range []string{"0", "-5"} {
		t.Run("explicit "+v+" is a usage error", func(t *testing.T) {
			fs := newFlagSet(t, "--capacity", v)
			_, send, err := OptionalIntFlag(fs, "capacity", 1)
			wantUsage(t, err, "--capacity")
			if send {
				t.Error("send = true alongside an error")
			}
		})
	}

	t.Run("a value at the floor is sent", func(t *testing.T) {
		fs := newFlagSet(t, "--capacity", "1")
		v, send, err := OptionalIntFlag(fs, "capacity", 1)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !send || v != 1 {
			t.Errorf("got (%d, %v), want (1, true)", v, send)
		}
	})
}

func TestParseTimeFlag(t *testing.T) {
	t.Run("an RFC3339 value parses", func(t *testing.T) {
		got, err := ParseTimeFlag("--created-after", "2026-08-01T00:00:00Z")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	// The message must name the expected shape rather than echo Go's
	// layout string, which is what byoc's four hand-rolled sites did.
	// An empty value is malformed, not absent. Whether to CALL this
	// at all is the flag set's business (see Changed elsewhere); once
	// called, "" is a value the caller supplied and it does not parse.
	t.Run("an empty value is a usage error", func(t *testing.T) {
		_, err := ParseTimeFlag("--created-after", "")
		wantUsage(t, err, "--created-after")
	})

	t.Run("a malformed value is a usage error", func(t *testing.T) {
		_, err := ParseTimeFlag("--created-after", "notatime")
		wantUsage(t, err, "--created-after")
		if !strings.Contains(err.Error(), "RFC3339") {
			t.Errorf("message %q does not name the expected format",
				err.Error())
		}
		if strings.Contains(err.Error(), "2006") {
			t.Errorf("message %q echoes Go's layout string at the user",
				err.Error())
		}
	})
}
