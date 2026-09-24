package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// taskListLookback bounds how many recent tasks
// newestSubjectTaskID requests. The API returns tasks newest-first,
// so the newest task for a subject is always within the first page;
// an explicit limit guards against the server's default page size
// silently truncating it.
const taskListLookback = 100

// Wait flags. Shared across every asynchronous command (create/delete
// of task-backed resources); only one such command runs per process
// invocation, so a single set of package-level vars is safe.
var (
	waitFlag         bool
	followFlag       bool
	waitTimeoutFlag  int
	waitIntervalFlag int
)

// addWaitFlags registers --wait, --follow, --wait-timeout and
// --wait-interval on an asynchronous command. These operations have the API accept the
// request and spawn a task, so without --wait the command exits as
// soon as the request is accepted, not when the work completes.
// --follow is --wait plus the task's step messages streamed to stderr
// in place of the bare status polls.
func addWaitFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&waitFlag, "wait", false,
		"Wait for the operation's task to reach a terminal state")
	cmd.Flags().BoolVar(&followFlag, "follow", false,
		"Stream the task's step messages until it reaches a terminal state")
	cmd.Flags().IntVar(&waitTimeoutFlag, "wait-timeout", 600,
		"Max seconds to wait when --wait/--follow is set")
	cmd.Flags().IntVar(&waitIntervalFlag, "wait-interval", 5,
		"Polling interval in seconds when --wait/--follow is set")
}

// tracking reports whether this invocation follows its task to a
// terminal state — via --wait or --follow. Prior-task capture at the
// mutation call sites keys off it.
func tracking() bool { return waitFlag || followFlag }

// newestSubjectTaskID returns the id of the most recent task for
// subjectID, or "" if the subject has no tasks.
//
// A caller with no deadline of its own gets one here, at
// requestBound() — the --timeout in force, or the 30-second default
// when --timeout is 0, so a raised bound is honoured and only the
// unbounded case is floored. The pre-mutation baseline captures call
// this on context.Background() with the HTTP client as their only
// bound, so an unbounded client made a hung baseline read outlive
// the --wait-timeout the user also passed. cp met the same hazard on
// its follow poll and answered it the same way (followPollTimeout).
// The wait loop's own calls arrive deadline-bearing and pass through
// untouched.
func newestSubjectTaskID(
	ctx context.Context, client *api.ClientWithResponses, subjectID string,
) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestBound())
		defer cancel()
	}
	sid := subjectID
	limit := taskListLookback
	resp, err := client.ListTasksWithResponse(
		ctx, &api.ListTasksParams{SubjectId: &sid, Limit: &limit},
	)
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return "", err
	}

	// A 2xx THE CLIENT COULD NOT READ is not an empty list. Collapsing
	// the two is what let a stale task through: "" here means "the
	// subject has no tasks", and discovery then accepts the first task
	// it sees, which on a database with history is an old one. byoc's
	// design is to PROPAGATE a failed pre-mutation read and refuse the
	// write, and that design never covered this state because it
	// produced no error to propagate. Returning one puts it back under
	// the design rather than giving byoc a second rule.
	//
	// The reachable shape is narrower than "any non-200 2xx", which
	// matters to anyone writing a fixture for it.
	// ParseListTasksResponse sets JSON200 only on a json
	// Content-Type AND status exactly 200, but its catch-all arm is
	// `Contains(Content-Type, "json") && true`, which unmarshals into
	// an Error -- so a 204 or 202 sent AS json fails at unmarshal and
	// returns an ordinary error, handled above. What lands here is a
	// 2xx whose Content-Type does not contain "json": a gateway's 204
	// with no Content-Type header, or a 200 carrying text/plain.
	if resp.JSON200 == nil {
		return "", newExitError(fmt.Sprintf(
			"list tasks: HTTP %d carried no readable task list",
			resp.StatusCode()), ExitGeneral)
	}
	tasks := resp.JSON200
	if len(*tasks) == 0 {
		return "", nil
	}

	return newestOf(*tasks).Id, nil
}

// newestOf returns the newest of tasks, ranking by parsed INSTANT when
// EVERY created_at parses and lexicographically otherwise.
//
// What this replaced ranked created_at as a STRING, justified as
// "RFC3339 and therefore lexicographically sortable" -- which RFC3339
// does not promise. A non-UTC offset breaks it (14:00:00+02:00 is
// 12:00Z and sorts AFTER 13:00:00Z) and so does a fractional second,
// since `.` sorts before `Z`. Only the newest task is returned, so
// ranking the wrong one newest means the real task is never considered.
// saas sends whole-second UTC today, so this closes a hazard rather
// than fixing an observed bug.
//
// The mode is decided ONCE for the slice, and that is the point: a
// per-pair fallback is not a total order and can cycle --
// A=12:30:00-01:00 (13:30Z), B=13:00:00Z, C=12:45:00 with no zone. A
// beats B by instant, B beats C by string, C beats A by string, so a
// linear max-scan returns whichever answer the array order favours.
// The string comparison was wrong about offsets but transitive, so it
// at least answered the same thing every time.
//
// managed carries the same function over its own generated api.Task
// (#335). Neither package can see the other's type.
func newestOf(tasks []api.Task) *api.Task {
	if len(tasks) == 0 {
		return nil
	}
	times := make([]time.Time, len(tasks))
	allParse := true
	for i, t := range tasks {
		at, err := time.Parse(time.RFC3339, t.CreatedAt)
		if err != nil {
			allParse = false
			break
		}
		times[i] = at
	}
	best := 0
	for i := range tasks {
		if allParse {
			if times[i].After(times[best]) {
				best = i
			}
			continue
		}
		if tasks[i].CreatedAt > tasks[best].CreatedAt {
			best = i
		}
	}
	newest := tasks[best]
	return &newest
}

// getTaskByID fetches a single task by id.
func getTaskByID(
	ctx context.Context, client *api.ClientWithResponses, taskID string,
) (*api.Task, error) {
	resp, err := client.ListTasksWithResponse(
		ctx, &api.ListTasksParams{Id: &taskID},
	)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	if err := checkResponse(resp.StatusCode(), string(resp.Body)); err != nil {
		return nil, err
	}

	tasks := resp.JSON200
	if tasks == nil || len(*tasks) == 0 {
		return nil, newExitError(
			fmt.Sprintf("task %q not found", taskID),
			ExitNotFound)
	}
	t := (*tasks)[0]
	return &t, nil
}

// waitForSubjectTask discovers the task spawned by a resource
// mutation on subjectID and polls it until it reaches a terminal
// state. Progress lines go to rt.Stderr so stdout stays parseable in
// machine output modes.
//
// priorTaskID is the newest task for the subject captured *before*
// the mutation; the newly-created task is the first task whose id
// differs from it. The mutation request returns no task id and the
// task takes a moment to appear, so discovery and polling share a
// single deadline. Each poll request is bounded by the shorter of
// that deadline and the per-request bound (--timeout; the ctx
// deadline still holds when that is 0), so a hung request cannot
// outlive --wait-timeout and does not spend all of it either — it
// ends the wait at exit 1, naming the request rather than
// --wait-timeout.
//
// Returns nil when the task succeeds, an *ExitError with ExitGeneral
// when it fails, and an *ExitError with ExitTimeout if the deadline
// passes (whether or not a task was ever discovered).
func waitForSubjectTask(
	rt *module.Runtime,
	client *api.ClientWithResponses,
	subjectID, priorTaskID string,
	timeout, interval int,
	follow bool,
) error {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	if interval < 1 {
		interval = 1
	}
	iv := time.Duration(interval) * time.Second

	taskID := ""
	seen := 0
	for {
		if time.Now().After(deadline) {
			return timeoutError(timeout, subjectID, taskID)
		}

		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		var stepErr error
		if taskID == "" {
			newest, err := newestSubjectTaskID(ctx, client, subjectID)
			stepErr = err
			if err == nil && newest != "" && newest != priorTaskID {
				taskID = newest
				fmt.Fprintf(rt.Stderr, "Tracking task %s...\n", output.Sanitize(taskID))
			}
		} else {
			t, err := getTaskByID(ctx, client, taskID)
			stepErr = err
			if err == nil {
				if follow {
					seen = printNewMessages(rt, t.Messages, seen)
				}
				switch t.Status {
				case "succeeded":
					cancel()
					fmt.Fprintf(rt.Stderr, "Task %s: succeeded.\n", output.Sanitize(t.Id))
					return nil
				case "failed":
					cancel()
					msg := fmt.Sprintf("task %s failed", t.Id)
					if t.Error != nil && *t.Error != "" {
						msg = fmt.Sprintf("task %s failed: %s",
							output.Sanitize(t.Id),
							output.Sanitize(*t.Error))
					}
					return newExitError(msg, ExitGeneral)
				default:
					if !follow {
						fmt.Fprintf(rt.Stderr, "Task %s: %s...\n",
							output.Sanitize(t.Id),
							output.Sanitize(t.Status))
					}
				}
			}
		}
		// Read before cancel(): afterwards ctx.Err() is Canceled
		// for every outcome and cannot say whether the deadline is
		// what ran out. controlplane's followTask reads it the same way.
		waitExpired := ctx.Err() != nil
		cancel()

		if stepErr != nil {
			// Only a wait that really expired may report --wait-timeout.
			// the per-request bound caps each poll, so one slow
			// poll fails with exactly the error a whole-wait expiry
			// raises -- measured 2026-08-22: both are a *url.Error
			// satisfying errors.Is(err, context.DeadlineExceeded)
			// with Timeout() true, so the error cannot tell them
			// apart. Without waitExpired a 600-second wait would
			// report itself expired 30 seconds in, with 570 unspent.
			//
			// The errors.Is half is deliberately untested: this ctx
			// carries the SAME deadline, so a request still running
			// when it passes is cancelled and yields a deadline
			// error, while one that finishes earlier leaves
			// waitExpired false. They come apart only in a
			// sub-millisecond race no test can produce. It stays
			// because dropping it would report any error landing on
			// the deadline as --wait-timeout.
			if errors.Is(stepErr, context.DeadlineExceeded) &&
				waitExpired {
				return timeoutError(timeout, subjectID, taskID)
			}
			return stepErr
		}

		time.Sleep(iv)
	}
}

// printNewMessages streams the task messages at index seen and later
// to rt.Stderr and returns the new cursor. The API renders messages
// append-only — each step contributes a running and then a terminal
// entry — so an index cursor prints each exactly once.
func printNewMessages(
	rt *module.Runtime, msgs []api.Message, seen int,
) int {
	for i := seen; i < len(msgs); i++ {
		fmt.Fprintf(rt.Stderr, "%s\n", output.Sanitize(formatTaskMessage(msgs[i])))
	}
	if len(msgs) > seen {
		return len(msgs)
	}
	return seen
}

// formatTaskMessage renders one task message on one line: time, the
// step's human title, its status and progress, with a level tag only
// when the level says more than "info".
func formatTaskMessage(m api.Message) string {
	return m.Time + "  " + formatTaskStep(m)
}

// formatTaskStep is formatTaskMessage without the leading timestamp,
// for `task get`'s detail block, which prints the task's times once of
// its own accord rather than once per step.
func formatTaskStep(m api.Message) string {
	var b strings.Builder
	if m.Level != "" && m.Level != "info" {
		b.WriteString("[" + m.Level + "] ")
	}
	b.WriteString(m.Text)
	if m.Status != nil && *m.Status != "" {
		b.WriteString(": " + *m.Status)
	}
	if m.Progress != nil {
		fmt.Fprintf(&b, " (%d%%)", *m.Progress)
	}
	return b.String()
}

// timeoutError builds the ExitTimeout returned when waiting exceeds
// --wait-timeout. The message names the task if one was discovered,
// otherwise the subject.
func timeoutError(timeout int, subjectID, taskID string) error {
	if taskID == "" {
		return newExitError(fmt.Sprintf(
			"timed out after %ds waiting for a task on %s",
			timeout, subjectID), ExitTimeout)
	}
	return newExitError(fmt.Sprintf(
		"timed out after %ds waiting for task %s", timeout, taskID), ExitTimeout)
}

// trackMutation handles the asynchronous tail of a resource command.
// When --wait is set it blocks until the spawned task reaches a
// terminal state; --follow does the same while streaming the task's
// step messages instead of bare status polls. Otherwise, in
// table/text output, it prints how to monitor the task; in machine
// output (json or yaml) it stays silent so stdout remains parseable.
//
// priorTaskID must be the newest task for the subject captured
// before the mutation (only meaningful when waiting). For a freshly
// created resource it is "", since the new resource has no prior
// tasks.
func trackMutation(
	rt *module.Runtime,
	client *api.ClientWithResponses,
	subjectID, priorTaskID string,
) error {
	if tracking() {
		return waitForSubjectTask(
			rt, client, subjectID, priorTaskID,
			waitTimeoutFlag, waitIntervalFlag, followFlag,
		)
	}
	if !rt.Output.Structured() {
		fmt.Fprintf(rt.Stderr,
			"Monitor with: pgedge starfleet byoc task list --subject-id %s\n",
			output.Sanitize(subjectID))
	}
	return nil
}
