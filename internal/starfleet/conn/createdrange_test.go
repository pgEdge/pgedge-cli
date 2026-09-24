package conn

import (
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/spf13/pflag"
)

// These pin the PROPERTY the enumerated command gate can only pin by
// example: an explicitly empty --created-after is a usage error and an
// OMITTED one sends nothing. The gate's case-count floor does not hold
// that property: swapping each row's "" case for another malformed
// string keeps both counts intact while the empty-is-absent mutation
// survives. Asserted here at the source, where thinning the call-site
// table cannot reach.
func TestApplyCreatedRange(t *testing.T) {
	newFS := func(t *testing.T, args ...string) *pflag.FlagSet {
		t.Helper()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("created-after", "", "")
		fs.String("created-before", "", "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parsing %v: %v", args, err)
		}
		return fs
	}

	t.Run("omitted sets nothing", func(t *testing.T) {
		var after, before *time.Time
		if err := ApplyCreatedRange(newFS(t), &after, &before); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if after != nil || before != nil {
			t.Errorf("got (%v, %v), want both nil — an omitted flag "+
				"must send no param at all", after, before)
		}
	})

	for _, flag := range []string{"--created-after", "--created-before"} {
		t.Run(flag+" explicitly empty is a usage error", func(t *testing.T) {
			var after, before *time.Time
			err := ApplyCreatedRange(
				newFS(t, flag, ""), &after, &before)
			if err == nil {
				t.Fatalf("%s \"\" was accepted as absent", flag)
			}
			if got := cli.ExitCode(err); got != cli.ExitUsage {
				t.Errorf("exit = %d, want %d", got, cli.ExitUsage)
			}
		})

		t.Run(flag+" malformed is a usage error", func(t *testing.T) {
			var after, before *time.Time
			err := ApplyCreatedRange(
				newFS(t, flag, "notatime"), &after, &before)
			if got := cli.ExitCode(err); got != cli.ExitUsage {
				t.Errorf("exit = %d, want %d; err=%v",
					got, cli.ExitUsage, err)
			}
		})
	}

	t.Run("both valid values are set", func(t *testing.T) {
		var after, before *time.Time
		fs := newFS(t,
			"--created-after", "2026-08-01T00:00:00Z",
			"--created-before", "2026-08-02T00:00:00Z")
		if err := ApplyCreatedRange(fs, &after, &before); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if after == nil || before == nil {
			t.Fatalf("got (%v, %v), want both set", after, before)
		}
		if !after.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("after = %v", after)
		}
		if !before.Equal(time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("before = %v", before)
		}
	})
}
