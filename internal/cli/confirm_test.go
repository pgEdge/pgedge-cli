package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestConfirm(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		force   bool
		isTTY   bool
		wantErr bool
	}{
		{name: "force skips prompt", force: true},
		{name: "tty yes", stdin: "y\n", isTTY: true},
		{name: "tty no rejects", stdin: "n\n", isTTY: true,
			wantErr: true},
		{name: "non-tty without force fails", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &module.Runtime{
				Stdin:  strings.NewReader(tt.stdin),
				Stderr: &strings.Builder{},
			}
			err := confirmWith(rt, "Delete thing?", tt.force,
				tt.isTTY)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfirmWrapperWithForce(t *testing.T) {
	// Exercises the public Confirm wrapper (which detects the real
	// os.Stdin TTY state) rather than the testable confirmWith core.
	// force=true short-circuits before the TTY check matters, so
	// this is hermetic regardless of how the test binary's stdin is
	// wired up.
	rt := &module.Runtime{
		Stdin:  strings.NewReader(""),
		Stderr: &strings.Builder{},
	}
	if err := Confirm(rt, "Delete thing?", true); err != nil {
		t.Errorf("Confirm with force = %v, want nil", err)
	}
}

// TestConfirmUnderDryRunNeverPrompts covers all four combinations of
// force and TTY. A dry run must not read stdin (the prompt would block)
// and must not reject a non-interactive caller (the operation is not
// happening, so there is nothing to confirm).
func TestConfirmUnderDryRunNeverPrompts(t *testing.T) {
	tests := []struct {
		name       string
		force      bool
		isTTY      bool
		wantChecks int
	}{
		{name: "tty, no force", isTTY: true, wantChecks: 1},
		{name: "no tty, no force", wantChecks: 1},
		// With --force the real run would not prompt either, so
		// recording that it "would require confirmation" would be false.
		{name: "tty, force", force: true, isTTY: true},
		{name: "no tty, force", force: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			// Any read of this would be a prompt the dry run must not
			// have issued.
			rt.Stdin = &failingReader{t: t}

			if err := confirmWith(
				rt, "delete it?", tc.force, tc.isTTY); err != nil {
				t.Fatalf("confirmWith: %v", err)
			}
			if got := len(rt.DryRun.Checks()); got != tc.wantChecks {
				t.Errorf("ledger has %d entries, want %d: %q",
					got, tc.wantChecks, rt.DryRun.Checks())
			}
			if tc.wantChecks == 1 && !strings.Contains(
				rt.DryRun.Checks()[0], "confirmation") {
				t.Errorf("ledger entry %q does not mention "+
					"confirmation", rt.DryRun.Checks()[0])
			}
		})
	}
}

// failingReader fails the test if anything reads from it.
type failingReader struct{ t *testing.T }

func (r *failingReader) Read([]byte) (int, error) {
	r.t.Error("a dry run read stdin, so it prompted")
	return 0, io.EOF
}
