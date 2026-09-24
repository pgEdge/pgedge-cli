package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// This mirrors internal/starfleet/byoc/cmd/wait.go on purpose: saas
// serves one task resource under both prefixes, `GET /managed/v1/tasks`
// and `GET /byoc/v1/tasks` take the same seven query parameters, and
// both answer the same `Task` schema. Both copies have the
// unreadable-2xx guard and instant ranking. The age floor and the
// degraded warning are managed-only, because byoc refuses the write
// when the pre-mutation read fails and so needs no floor. Sharing the
// code needs a generics or interface seam over each module's generated
// `*api.ClientWithResponses`, which one caller cannot shape.

// taskListLookback bounds how many recent tasks newestSubjectTask
// requests. The API returns tasks newest-first, so a subject's newest
// task is always within the first page; an explicit limit guards
// against the server's default page size silently truncating it.
const taskListLookback = 100

// Wait flags. Shared across every asynchronous command; only one such
// command runs per process invocation, so a single set of
// package-level vars is safe.
var (
	waitFlag         bool
	followFlag       bool
	waitTimeoutFlag  int
	waitIntervalFlag int
)

// addWaitFlags registers --wait, --follow, --wait-timeout and
// --wait-interval on an asynchronous command. The API accepts the
// request and spawns a task, so without --wait the command exits when
// the request is accepted, not when the work completes. --follow is
// --wait plus the task's step messages streamed to stderr in place of
// the bare status polls.
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

// newestSubjectTask returns the most recent task for subjectID, ranked
// by newestOf, or nil when the subject has no tasks. It returns the
// task rather than its id because discovery needs created_at too: see
// taskBaseline.accepts.
func newestSubjectTask(
	ctx context.Context, client *api.ClientWithResponses, subjectID string,
) (*api.Task, error) {
	// A caller with no deadline gets requestBound(): the --timeout in
	// force, or 30 seconds when it is 0. captureTaskBaseline calls on
	// context.Background(), so without this a hung baseline read under
	// --timeout 0 would outlive --wait-timeout. byoc's
	// newestSubjectTaskID and controlplane's followPollTimeout answer
	// the same hazard the same way.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestBound())
		defer cancel()
	}
	sid := subjectID
	limit := taskListLookback
	resp, err := client.ListManagedTasksWithResponse(
		ctx, &api.ListManagedTasksParams{SubjectId: &sid, Limit: &limit},
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if err := checkResponse(
		resp.StatusCode(), string(resp.Body)); err != nil {
		return nil, err
	}

	// A 2xx the client could not read is not an empty list. Read as
	// one, captureTaskBaseline would see no prior task, set no floor,
	// and waiting would accept the first task it saw.
	//
	// ParseListManagedTasksResponse sets JSON200 only for a json
	// Content-Type and status exactly 200. Its catch-all arm unmarshals
	// any other json response into an Error, so a non-200 json 2xx
	// returns an error above unless its body decodes as one (any JSON
	// object, or null). That case lands here, as does a 2xx whose
	// Content-Type lacks "json": a gateway's 204 with no Content-Type
	// header, or a 200 carrying text/plain.
	if resp.JSON200 == nil {
		return nil, newExitError(fmt.Sprintf(
			"list tasks: HTTP %d carried no readable task list",
			resp.StatusCode()), ExitGeneral)
	}
	tasks := resp.JSON200
	if len(*tasks) == 0 {
		return nil, nil
	}

	return newestOf(*tasks), nil
}

// newestOf returns the newest of tasks, by parsed instant when every
// created_at parses and by string comparison otherwise.
//
// The mode is decided once for the slice because a per-pair fallback is
// not a total order. A=12:30:00-01:00 (13:30Z), B=13:00:00Z and a
// zoneless C=12:45:00 cycle: A beats B by instant, B beats C and C
// beats A by string, so a max-scan's answer would depend on array
// order. String comparison is wrong about offsets but transitive. A
// mixed-format response is rarer still than the all-unparseable one
// accepts handles, so this closes a hazard, not an observed bug.
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
		if tasks[i].CreatedAt > tasks[best].CreatedAt { //nolint:gosec // G602: best is always an index from range tasks
			best = i
		}
	}
	newest := tasks[best]
	return &newest
}

// baselineClockMargin is how far before the capture attempt a task may
// have been created and still be accepted as this mutation's, on the
// one path where an age floor is all there is (see taskBaseline).
//
// It covers two things. The API truncates created_at to the second, so
// a task created 0.9s after the floor can report the second before it,
// and the floor is our clock while created_at is the server's. A minute
// is sized for that skew, which can be most of one.
//
// Too tight rejects the mutation's own task, and the wait times out
// naming the floor. Too loose widens a chained-write window: after
// `mcp deploy` without --wait, `rag deploy --wait` on the same database
// finds a task under a minute old, and if its capture fails the floor
// admits the first write's still-running task and reports its outcome.
// The id path excludes that; an age floor cannot. A minute still
// rejects the stale task this guards against: the newest task on the
// devapi fixture that reproduced it was two days old.
const baselineClockMargin = time.Minute

// taskBaseline is what a mutation records about a subject's tasks
// before it runs, so waiting can tell the task it caused from the ones
// already there.
type taskBaseline struct {
	// priorID is the subject's newest task at capture time, and "" when
	// the subject had none or when nothing was captured.
	priorID string
	// notBefore is set only when the capture failed, to the wall clock
	// at the attempt less baselineClockMargin. With a prior id, an id
	// comparison needs no clock; without one, an age floor is all that
	// can tell this mutation's task from a stale one. A floor on the
	// healthy path too would expose every --wait to clock skew, to
	// catch a case ids already catch.
	notBefore time.Time
}

// accepts reports whether t can be the task this mutation spawned.
// With no baseline, waitForSubjectTask would take the first task it
// saw, which on a database with history is an old one. Reproduced end
// to end, --wait then reported a months-old outcome for a write it
// never watched: exit 0 on an old `succeeded`, and exit 1 with a stale
// error message on an old `failed`.
func (b taskBaseline) accepts(t api.Task) (ok, degraded bool) {
	if b.notBefore.IsZero() {
		// The capture worked, and an id comparison is exact.
		return t.Id != b.priorID, false
	}
	created, err := time.Parse(time.RFC3339, t.CreatedAt)
	if err != nil {
		// An unreadable created_at leaves nothing to test. Accepting it
		// beats refusing every task into a timeout over a shape the API
		// has never sent. degraded lets the caller say so, or one format
		// change would silently turn the floor off for every task.
		return true, true
	}
	return !created.Before(b.notBefore), false
}

// getTaskByID fetches a single task by id.
func getTaskByID(
	ctx context.Context, client *api.ClientWithResponses, taskID string,
) (*api.Task, error) {
	resp, err := client.ListManagedTasksWithResponse(
		ctx, &api.ListManagedTasksParams{Id: &taskID},
	)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	if err := checkResponse(
		resp.StatusCode(), string(resp.Body)); err != nil {
		return nil, err
	}

	tasks := resp.JSON200
	if tasks == nil || len(*tasks) == 0 {
		return nil, newExitError(
			fmt.Sprintf("task %q not found", taskID), ExitNotFound)
	}
	t := (*tasks)[0]
	return &t, nil
}

// waitForSubjectTask discovers the task a mutation on subjectID spawned
// and polls it to a terminal state, with progress on rt.Stderr so
// stdout stays parseable. base is what captureTaskBaseline recorded;
// the new task is the first it accepts.
//
// The mutation response carries no task id and the task takes a moment
// to appear, so discovery and polling share one deadline. Each request
// is bounded by the shorter of that deadline and --timeout (the
// deadline alone when --timeout is 0), so a hung request cannot outlive
// --wait-timeout, nor spend all of it: it ends the wait at exit 1,
// naming the request rather than --wait-timeout.
//
// It returns nil when the task succeeds, ExitGeneral when it fails, and
// ExitTimeout when the deadline passes, whether or not a task was ever
// discovered.
func waitForSubjectTask(
	rt *module.Runtime,
	client *api.ClientWithResponses,
	subjectID string, base taskBaseline,
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
	// Once per wait, not once per poll: discovery re-reads every
	// interval and the same task would otherwise repeat the warning
	// until the deadline.
	saidDegraded := false
	for {
		if time.Now().After(deadline) {
			return timeoutError(timeout, subjectID, taskID, base)
		}

		ctx, cancel := context.WithDeadline(
			context.Background(), deadline)
		var stepErr error
		if taskID == "" {
			newest, err := newestSubjectTask(ctx, client, subjectID)
			stepErr = err
			if err == nil && newest != nil {
				ok, degraded := base.accepts(*newest)
				if degraded && !saidDegraded {
					saidDegraded = true
					fmt.Fprintf(rt.Stderr,
						"Warning: task %s has an unreadable "+
							"created_at (%q), so its age cannot be "+
							"checked against the floor; accepting it "+
							"as this operation's task.\n",
						output.Sanitize(newest.Id), newest.CreatedAt)
				}
				if ok {
					taskID = newest.Id
					fmt.Fprintf(rt.Stderr,
						"Tracking task %s...\n", output.Sanitize(taskID))
				}
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
					fmt.Fprintf(rt.Stderr,
						"Task %s: succeeded.\n", output.Sanitize(t.Id))
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
							output.Sanitize(t.Id), output.Sanitize(t.Status))
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
			// A poll hitting the per-request bound fails with the same
			// error, measured 2026-08-22: both are a *url.Error that
			// satisfies errors.Is(err, context.DeadlineExceeded) with
			// Timeout() true. Without waitExpired a 600-second wait
			// would report itself expired 30 seconds in, 570 unspent.
			//
			// No test covers the errors.Is half: with one shared
			// deadline the two conditions differ only in a
			// sub-millisecond race. Without it, any error landing on
			// the deadline would report as --wait-timeout.
			if errors.Is(stepErr, context.DeadlineExceeded) &&
				waitExpired {
				return timeoutError(timeout, subjectID, taskID, base)
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
// --wait-timeout, naming the task if one was discovered and the subject
// otherwise. With no task and an age floor in force it names the floor:
// every visible task was refused as older than it, which is also what
// clock skew beyond baselineClockMargin looks like, and only the
// printed boundary lets an operator tell.
func timeoutError(
	timeout int, subjectID, taskID string, base taskBaseline,
) error {
	if taskID == "" {
		if !base.notBefore.IsZero() {
			return newExitError(fmt.Sprintf(
				"timed out after %ds waiting for a task on %s created "+
					"at or after %s: the pre-mutation task read failed, "+
					"so older tasks could not be ruled out as this "+
					"operation's",
				timeout, subjectID,
				base.notBefore.Format(time.RFC3339)), ExitTimeout)
		}
		return newExitError(fmt.Sprintf(
			"timed out after %ds waiting for a task on %s",
			timeout, subjectID), ExitTimeout)
	}
	return newExitError(fmt.Sprintf(
		"timed out after %ds waiting for task %s",
		timeout, taskID), ExitTimeout)
}

// trackMutation handles the asynchronous tail of a resource command.
// With --wait it blocks until the spawned task reaches a terminal
// state; --follow does the same while streaming the task's step
// messages instead of bare status polls. Otherwise, in table/text
// output, it prints how to monitor the task; in machine output it
// stays silent so stdout stays parseable.
//
// base must be what captureTaskBaseline recorded before the mutation,
// and is only meaningful when waiting. For a freshly created database
// or branch it is the zero value, since the new subject has no prior
// tasks.
func trackMutation(
	rt *module.Runtime,
	client *api.ClientWithResponses,
	subjectID string, base taskBaseline,
) error {
	if waitFlag || followFlag {
		return waitForSubjectTask(
			rt, client, subjectID, base,
			waitTimeoutFlag, waitIntervalFlag, followFlag,
		)
	}
	if !rt.Output.Structured() {
		fmt.Fprintf(rt.Stderr,
			"Monitor with: pgedge starfleet managed task list --subject-id %s\n",
			output.Sanitize(subjectID))
	}
	return nil
}

// captureTaskBaseline records the subject's newest task before a
// mutation, so waitForSubjectTask can tell the new task from the old
// ones. It is only worth a round trip when we are actually going to
// wait.
//
// A failed read does not fail the mutation: the caller asked for the
// write, not a task list, and byoc's choice to propagate is a tradeoff
// rather than the obviously right one. It leaves an age floor instead,
// so a stale task is refused and the wait either finds the real task
// or times out saying why.
func captureTaskBaseline(
	client *api.ClientWithResponses, subjectID string,
) taskBaseline {
	if !waitFlag && !followFlag {
		return taskBaseline{}
	}
	// Read before the request, not after: the floor has to precede the
	// mutation for "created at or after it" to mean anything.
	attempted := time.Now().UTC()
	prior, err := newestSubjectTask(
		context.Background(), client, subjectID)
	if err != nil {
		return taskBaseline{
			notBefore: attempted.Add(-baselineClockMargin),
		}
	}
	if prior == nil {
		return taskBaseline{}
	}
	return taskBaseline{priorID: prior.Id}
}
