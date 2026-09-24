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

// taskListLookback is newestSubjectTaskID's page size. The API returns
// tasks newest-first, so the newest is on the first page; an explicit
// limit keeps the server's default page size from truncating it.
const taskListLookback = 100

// Wait flags. Package-level is safe because only one asynchronous
// command runs per process invocation.
var (
	waitFlag         bool
	followFlag       bool
	waitTimeoutFlag  int
	waitIntervalFlag int
)

// addWaitFlags registers the wait flags on an asynchronous command.
// The API accepts the request and spawns a task, so without --wait the
// command exits on acceptance, not when the work completes.
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
// terminal state. Prior-task capture at the mutation call sites keys
// off it.
func tracking() bool { return waitFlag || followFlag }

// newestSubjectTaskID returns the id of the most recent task for
// subjectID, or "" if the subject has no tasks.
//
// A caller with no deadline gets requestBound() here. The pre-mutation
// baseline captures call this on context.Background(), and with
// --timeout 0 a hung baseline read would otherwise outlive
// --wait-timeout. controlplane's followPollTimeout answers the same
// hazard. The wait loop's calls already carry a deadline.
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

	// A 2xx the client could not read is not an empty list. "" means
	// "the subject has no tasks", and discovery would then accept the
	// first task it saw, which on a database with history is an old
	// one. Returning an error puts this under the rule that a failed
	// pre-mutation read refuses the write.
	//
	// ParseListTasksResponse sets JSON200 only for a json Content-Type
	// and status exactly 200. Its catch-all arm unmarshals any other
	// json response into an Error, so a non-200 json 2xx returns an
	// error above unless its body decodes as one (any JSON object, or
	// null). That case lands here, as does a 2xx whose Content-Type
	// lacks "json": a gateway's 204 with no Content-Type header, or a
	// 200 carrying text/plain.
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
// RFC3339 strings do not sort lexicographically: a non-UTC offset
// breaks it (14:00:00+02:00 is 12:00Z and sorts after 13:00:00Z), and
// so does a fractional second, since `.` sorts before `Z`. The API
// sends whole-second UTC today, so this closes a hazard rather than
// an observed bug.
//
// The mode is decided once for the slice because a per-pair fallback
// is not a total order and can cycle: A=12:30:00-01:00 (13:30Z),
// B=13:00:00Z, C=12:45:00 with no zone. A beats B by instant, B beats
// C by string, C beats A by string, so a max-scan's answer would
// depend on array order.
//
// managed carries a copy over its own generated api.Task, which this
// package cannot see.
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

// waitForSubjectTask discovers the task a mutation spawned on
// subjectID and polls it to a terminal state, writing progress to
// stderr.
//
// priorTaskID is the newest task captured before the mutation; the new
// task is the first whose id differs. The mutation returns no task id
// and the task takes a moment to appear, so discovery and polling
// share one deadline. Each request is bounded by the shorter of that
// deadline and --timeout, so a hung request ends the wait at exit 1,
// naming the request, rather than spending all of --wait-timeout.
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
		// Read before cancel(): afterwards ctx.Err() is Canceled for
		// every outcome and cannot say whether the deadline ran out.
		waitExpired := ctx.Err() != nil
		cancel()

		if stepErr != nil {
			// Only a wait that really expired may report
			// --wait-timeout. One slow poll fails with the same error
			// a whole-wait expiry raises (measured 2026-08-22: both a
			// *url.Error satisfying errors.Is(err,
			// context.DeadlineExceeded) with Timeout() true), so
			// without waitExpired a 600-second wait would report
			// itself expired 30 seconds in, with 570 unspent.
			//
			// The errors.Is half is untested: the two conditions come
			// apart only in a sub-millisecond race at the deadline. It
			// stays so an unrelated error landing on the deadline is
			// not reported as --wait-timeout.
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

// formatTaskMessage renders one task message on one line.
func formatTaskMessage(m api.Message) string {
	return m.Time + "  " + formatTaskStep(m)
}

// formatTaskStep omits the timestamp for `task get`'s detail block,
// which prints the task's times once rather than per step.
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
// --wait-timeout.
func timeoutError(timeout int, subjectID, taskID string) error {
	if taskID == "" {
		return newExitError(fmt.Sprintf(
			"timed out after %ds waiting for a task on %s",
			timeout, subjectID), ExitTimeout)
	}
	return newExitError(fmt.Sprintf(
		"timed out after %ds waiting for task %s", timeout, taskID), ExitTimeout)
}

// trackMutation handles the asynchronous tail of a resource command:
// it waits when tracking, otherwise prints a monitor hint in text
// output.
//
// priorTaskID must be the newest task for the subject captured before
// the mutation. For a freshly created resource it is "", since the new
// resource has no prior tasks.
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
