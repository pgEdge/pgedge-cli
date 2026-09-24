package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// taskColumns are the table headers shared by task list, get and wait.
var taskColumns = []string{"ID", "NAME", "STATUS", "SUBJECT", "CREATED"}

// NewTaskCmd builds the `pgedge starfleet managed task` command group, which
// inspects the asynchronous tasks the platform spawns for mutations.
// The plural "tasks" is kept as a plural alias (unlisted in help).
//
// The API serves the same task resource under both product prefixes, so
// this is byoc's task group over `/managed/v1/tasks`. Tasks are scoped
// per product: a managed database's tasks appear here, not under
// `pgedge starfleet byoc task`.
func NewTaskCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks"},
		Short:   "Inspect pgEdge Managed tasks",
		Long: `task inspects the asynchronous tasks the platform spawns
when you create, resize or delete a managed database.

Use these commands to list recent tasks, read a single task, or
block until a task reaches a terminal state.

Example:
  pgedge starfleet managed task list --subject-id <database_id>
  pgedge starfleet managed task wait <task_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newTaskListCmd(rt),
		newTaskGetCmd(rt),
		newTaskWaitCmd(rt),
	)
	return cmd
}

// --- list ---

func newTaskListCmd(rt *module.Runtime) *cobra.Command {
	var (
		limit       int
		offset      int
		subjectID   string
		subjectKind string
		name        string
		status      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Long: `list shows recent tasks, newest first.

Use it to track the progress of a mutation: filter by --subject-id
to see the tasks for one database, or by --status to find failures.

Example:
  pgedge starfleet managed task list
  pgedge starfleet managed task list --subject-id <database_id> --status failed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client: a bad --limit is knowable locally, so
			// it must answer 2 rather than 5 for credentials it never
			// needed. paging.go says why there is no upper bound.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			// --subject-id names a resource, so it takes a full UUID
			// like every other ID input, checked here for exit 2:
			// /managed/v1/tasks answers a malformed subject_id with
			// 500 "failed to list tasks", which names nothing and
			// suggests nothing. The sibling ?id= filter is typed in
			// the contract and gets a framework 400 for free;
			// subject_id is a bare string.
			subject, sendSubject, err := cli.OptionalStringFlag(
				cmd.Flags(), "subject-id",
				"pass the full UUID from `database list`, or omit the "+
					"flag to list every subject's tasks")
			if err != nil {
				return err
			}
			var subjectUUID string
			if sendSubject {
				id, err := parseUUIDArg(subject, "subject ID")
				if err != nil {
					return err
				}
				// The canonical spelling, not what was typed:
				// uuid.Parse accepts the braced and urn forms, which
				// the API would answer with the same unactionable 500.
				subjectUUID = id.String()
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListManagedTasksParams{}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if sendSubject {
				params.SubjectId = &subjectUUID
			}
			if subjectKind != "" {
				params.SubjectKind = &subjectKind
			}
			// Not validated, deliberately: the endpoint publishes
			// `name` as a bare string with no enum, so an allowlist
			// here would refuse a name the API accepts and would rot
			// the day the API adds a task kind.
			if name != "" {
				params.Name = &name
			}
			if status != "" {
				s := api.ListManagedTasksParamsStatus(status)
				params.Status = &s
			}

			resp, err := client.ListManagedTasksWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list tasks: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			tasks := resp.JSON200
			if tasks == nil || len(*tasks) == 0 {
				fmt.Fprintln(rt.Stderr, "No tasks found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*tasks))
			for _, t := range *tasks {
				rows = append(rows, taskRow(t))
			}
			if err := rt.Output.Print(rows, taskColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(
				rt, len(*tasks), limit, taskPageDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(taskPageDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.StringVar(&subjectID, "subject-id", "",
		"Filter by subject (full UUID)")
	f.StringVar(&name, "name", "",
		"Filter by task name (e.g. rotate-password-managed)")
	f.StringVar(&subjectKind, "subject-kind", "",
		"Filter by subject kind")
	f.StringVar(&status, "status", "",
		"Filter by status (queued, running, succeeded, failed)")
	return cmd
}

// --- get ---

func newTaskGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <task_id>",
		Short: "Show task details",
		Long: `get shows the details of a single task.

Use it to read a task's status, subject and any error message. The
argument is the task's ID.

Example:
  pgedge starfleet managed task get <task_id> -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the client, for the reason task wait's own parse
			// gives: the `?id=` filter answers a clean 400, but exit 1
			// is not the code for a bad argument.
			id, err := parseUUIDArg(args[0], "task ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			t, err := getTaskByID(
				context.Background(), client, id.String())
			if err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(t, nil)
			}
			return printTaskDetail(rt, t)
		},
	}
}

// printTaskDetail renders a single task in text mode: the summary row,
// then a label/value block (four fields need no second table) on the
// same writer, as `database get` and `controlplane database get` print
// their nested sections. Without the block a task's progress and
// failure reason were reachable only through `-o json`.
//
// Only the latest step message is shown. `task wait --follow` streams
// the full sequence, and a restore emits fifteen messages whose earlier
// entries say nothing a reader of a finished task needs.
//
// The times are printed in full because the CREATED column is a date
// alone, so two tasks on one subject minutes apart look the same there,
// which is the case when reading a restore's history. The shared column
// formatter backs every table in the CLI, so it is left alone.
//
// The error is trimmed and given its own lines, as controlplane's
// printTaskError does, since a trailing newline would break the block's
// alignment.
//
// json/yaml callers never reach here; they marshal the whole struct.
func printTaskDetail(rt *module.Runtime, t *api.Task) error {
	if err := rt.Output.Print(
		[]output.Row{taskRow(*t)}, taskColumns); err != nil {
		return err
	}

	out := rt.Output.Out
	// These three are one line each, so they are escaped: a newline in
	// the value would forge a line under a sanitized table on stdout,
	// which is where a caller parses.
	fmt.Fprintf(out, "\nCreated  %s\n", output.Sanitize(t.CreatedAt))
	fmt.Fprintf(out, "Updated  %s\n", output.Sanitize(t.UpdatedAt))
	if n := len(t.Messages); n > 0 {
		fmt.Fprintf(out, "Step     %s\n",
			output.Sanitize(formatTaskStep(t.Messages[n-1])))
	}
	// The error is not escaped, as in controlplane's printTaskError: it
	// is printed last, under its own heading, so a newline cannot forge
	// a field line above it, and these wrapped `%w` chains read better
	// with their line structure intact.
	if msg := strings.TrimSpace(output.DerefString(t.Error)); msg != "" {
		fmt.Fprintf(out, "\nError\n%s\n", msg)
	}
	return nil
}

// --- wait ---

func newTaskWaitCmd(rt *module.Runtime) *cobra.Command {
	var (
		timeout  int
		interval int
		follow   bool
	)
	cmd := &cobra.Command{
		Use:   "wait <task_id>",
		Short: "Wait for a task to complete",
		Long: `wait polls a task until it reaches a terminal state
(succeeded or failed).

Use it to block a script until an asynchronous mutation finishes, or
to attach to work this CLI did not start.

--follow streams the task's step messages as they appear, in place of
the bare status lines. This verb always blocks, so --follow only
changes what is reported, and it shows the same stream that --follow
on the mutating verb would have shown.

Exit codes:
  0  task succeeded
  1  task failed or other error
  3  timeout exceeded
  4  no task has that ID

Exit 4 is immediate: an unknown ID is an error, not a task that has
yet to appear. A task read straight after a mutation may need a
moment before it can be waited on, which --wait on the mutating verb
absorbs and this verb cannot.

Example:
  pgedge starfleet managed task wait <task_id> --wait-timeout 900
  pgedge starfleet managed task wait <task_id> --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Checked before the client, so a mistyped id answers 2
			// locally. The `?id=` filter is typed in the contract and
			// would answer a clean 400, but that exits 1, and exit 1
			// is not the code for a bad argument. The canonical
			// spelling is sent: the braced and urn forms parse locally
			// and would 400.
			id, err := parseUUIDArg(args[0], "task ID")
			if err != nil {
				return err
			}
			taskID := id.String()

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			deadline := time.Now().Add(
				time.Duration(timeout) * time.Second)
			iv := time.Duration(interval) * time.Second

			// Cursor into the task's append-only message slice, so
			// each step prints exactly once across polls rather than
			// the whole slice per poll.
			seen := 0

			for {
				// Each poll is bounded by the wait deadline, since
				// --timeout 0 leaves the client unbounded and a poll
				// would outlive --wait-timeout forever. It is floored
				// at one requestBound() to keep the courtesy poll of
				// a --wait-timeout 0, which reports the last status,
				// and to honour a raised --timeout, at the cost of a
				// hung poll ending the wait up to one request
				// allowance late, as controlplane's follow poll does.
				pollDeadline := deadline
				if floor := time.Now().Add(
					requestBound()); floor.After(pollDeadline) {
					pollDeadline = floor
				}
				ctx, cancel := context.WithDeadline(
					context.Background(), pollDeadline)
				t, err := getTaskByID(ctx, client, taskID)
				cancel()
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) &&
						time.Now().After(deadline) {
						return newExitError(fmt.Sprintf(
							"timed out after %ds waiting for task %s",
							timeout, taskID), ExitTimeout)
					}
					return err
				}

				// Before the terminal check, so a task that finishes
				// between polls still streams the steps that got it
				// there rather than jumping to the outcome.
				if follow {
					seen = printNewMessages(rt, t.Messages, seen)
				}

				switch t.Status {
				case "succeeded":
					if rt.Output.Structured() {
						return rt.Output.Print(t, nil)
					}
					fmt.Fprintf(rt.Stderr, "Task %s: %s\n",
						output.Sanitize(t.Id), output.Sanitize(t.Status))
					return nil

				case "failed":
					if rt.Output.Structured() {
						_ = rt.Output.Print(t, nil)
					}
					msg := fmt.Sprintf("task %s failed", t.Id)
					if t.Error != nil && *t.Error != "" {
						msg = fmt.Sprintf("task %s failed: %s",
							output.Sanitize(t.Id),
							output.Sanitize(*t.Error))
					}
					return newExitError(msg, ExitGeneral)

				default:
					// queued or running — progress on stderr. Under
					// --follow the step messages are the progress
					// report, so the bare status line would only
					// interleave noise into them.
					if !follow {
						fmt.Fprintf(rt.Stderr, "Task %s: %s...\n",
							output.Sanitize(t.Id), output.Sanitize(t.Status))
					}
				}

				if time.Now().After(deadline) {
					return newExitError(fmt.Sprintf(
						"timed out after %ds waiting for task %s "+
							"(last status: %s)",
						timeout, taskID, t.Status), ExitTimeout)
				}

				time.Sleep(iv)
			}
		},
	}
	cmd.Flags().IntVar(&timeout, "wait-timeout", 600,
		"Maximum seconds to wait for task completion")
	cmd.Flags().IntVar(&interval, "wait-interval", 5,
		"Polling interval in seconds")
	cmd.Flags().BoolVar(&follow, "follow", false,
		"Stream the task's step messages instead of bare status lines")
	return cmd
}

// --- row adapter ---

type taskRowData struct {
	id, name, status, subject, created string
}

func (r taskRowData) Columns() []string {
	return []string{
		r.id, r.name, output.ColorStatus(r.status),
		r.subject, r.created,
	}
}

// taskRow converts an api.Task into a taskRowData for table output.
func taskRow(t api.Task) taskRowData {
	return taskRowData{
		id:      t.Id,
		name:    t.Name,
		status:  t.Status,
		subject: fmt.Sprintf("%s/%s", t.SubjectKind, t.SubjectId),
		created: output.FormatTime(t.CreatedAt),
	}
}
