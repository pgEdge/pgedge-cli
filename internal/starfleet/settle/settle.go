// Package settle is the end of a Starfleet --wait: after a write's task
// succeeds, it re-reads the subject until the subject's status says the
// write is done. managed and byoc share it; each supplies its own read
// and its own status vocabulary.
package settle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// Gone is the end state a delete waits for: the read answering 404, or
// a status of "deleted", which is what a soft-deleted managed branch
// reads as (measured 2026-10-28).
const Gone = "deleted"

// Target is the subject a wait re-reads and the status that means the
// write is done.
type Target struct {
	Noun string
	ID   string
	Want string
	// Transitional is the part of the module's status vocabulary a
	// subject passes through on its way somewhere else. Any other
	// status is where it has come to rest.
	Transitional []string
	// Read returns the subject's status, or a conn.ExitNotFound error
	// once it is gone.
	Read func(ctx context.Context) (string, error)
}

// Await polls t until it reaches t.Want, sharing the deadline the task
// wait used. A succeeded task can leave the status a poll behind, so a
// transitional status is polled through; any other status is final and
// exits 1 naming it. bound is one request's allowance.
func Await(
	rt *module.Runtime, t Target,
	deadline time.Time, timeout int, interval, bound time.Duration,
) error {
	last := ""
	for {
		if last != "" && time.Now().After(deadline) {
			return timeoutError(t, timeout, last)
		}
		// Floored at one request allowance, as task wait's poll is, so
		// a task that succeeds on the deadline still gets its read.
		pollDeadline := deadline
		if floor := time.Now().Add(bound); floor.After(deadline) {
			pollDeadline = floor
		}
		ctx, cancel := context.WithDeadline(
			context.Background(), pollDeadline)
		status, err := t.Read(ctx)
		expired := time.Now().After(deadline)
		cancel()

		var ee *conn.ExitError
		switch {
		case err != nil && t.Want == Gone &&
			errors.As(err, &ee) && ee.Code() == conn.ExitNotFound:
			status = Gone
		case err != nil && errors.Is(err, context.DeadlineExceeded) &&
			expired:
			return timeoutError(t, timeout, last)
		case err != nil:
			return err
		}

		if status == t.Want {
			fmt.Fprintf(rt.Stderr, "%s %s: %s.\n", output.Sanitize(t.Noun),
				output.Sanitize(t.ID), output.Sanitize(status))
			return nil
		}
		if !slices.Contains(t.Transitional, status) {
			return conn.NewExitError(fmt.Sprintf("%s %s is %q, not %s",
				t.Noun, output.Sanitize(t.ID), output.Sanitize(status),
				t.Want), conn.ExitGeneral)
		}
		fmt.Fprintf(rt.Stderr, "%s %s: %s...\n", output.Sanitize(t.Noun),
			output.Sanitize(t.ID), output.Sanitize(status))
		last = status
		time.Sleep(interval)
	}
}

func timeoutError(t Target, timeout int, last string) error {
	msg := fmt.Sprintf("timed out after %ds waiting for %s %s to be %s",
		timeout, t.Noun, t.ID, t.Want)
	if last != "" {
		msg += fmt.Sprintf(" (last status: %s)", output.Sanitize(last))
	}
	return conn.NewExitError(msg, conn.ExitTimeout)
}
