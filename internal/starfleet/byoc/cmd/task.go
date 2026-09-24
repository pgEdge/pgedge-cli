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
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// taskColumns are the table headers shared by task list, get, and
// wait.
var taskColumns = []string{"ID", "NAME", "STATUS", "SUBJECT", "CREATED"}

// NewTaskCmd builds the `pgedge starfleet byoc task` command group, which
// inspects the asynchronous tasks the platform spawns for mutations.
// The plural "tasks" is kept as a plural alias (unlisted in help).
func NewTaskCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks"},
		Short:   "Inspect pgEdge BYOC tasks",
		Long: `task inspects the asynchronous tasks the platform spawns
when you create, update, or delete a resource.

Use these commands to list recent tasks, read a single task, or
block until a task reaches a terminal state.

Example:
  pgedge starfleet byoc task list --subject-id <cluster_id>
  pgedge starfleet byoc task wait <task_id>`,
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
to see the tasks for one resource, or by --status to find failures.

Example:
  pgedge starfleet byoc task list
  pgedge starfleet byoc task list --subject-id <cluster_id> --status failed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client: a bad --limit is knowable locally, so
			// it answers 2 rather than 5 for credentials it never needed.
			// byoc.yaml declares no paging bounds on any list endpoint,
			// hence NoUpperBound: the server clamps at 100 today, but a
			// measured clamp is not a published contract and the CLI must
			// not refuse a value the API would accept.
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
			// (#194). The value used to be forwarded verbatim and the
			// API answers a malformed subject_id with 500 "failed to
			// list tasks", so a short ID cost a round trip to be told
			// nothing (#240). The sibling ?id= filter is typed in the
			// contract and gets a framework 400 for free.
			subject, sendSubject, err := cli.OptionalStringFlag(
				cmd.Flags(), "subject-id",
				"pass the subject's full UUID — a cluster, database, "+
					"ingress or backup store — or omit the flag to "+
					"list every subject's tasks")
			if err != nil {
				return err
			}
			var subjectUUID string
			if sendSubject {
				id, err := parseUUIDArg(subject, "subject ID")
				if err != nil {
					return err
				}
				// Canonical, not as typed: uuid.Parse accepts the
				// braced and urn forms, which the API would answer
				// with the same unactionable 500.
				subjectUUID = id.String()
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListTasksParams{}
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
			// the day saas adds a task kind (#231).
			if name != "" {
				params.Name = &name
			}
			if status != "" {
				s := api.ListTasksParamsStatus(status)
				params.Status = &s
			}

			resp, err := client.ListTasksWithResponse(
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
			cli.PrintTruncationHint(rt, len(*tasks), limit,
				taskDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(taskDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.StringVar(&subjectID, "subject-id", "",
		"Filter by subject (full UUID)")
	f.StringVar(&name, "name", "",
		"Filter by task name (e.g. update, delete)")
	f.StringVar(&subjectKind, "subject-kind", "", "Filter by subject kind")
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

Use it to read a task's status, subject, and any error message.
The argument is the task's ID.

Example:
  pgedge starfleet byoc task get <task_id> -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The id is checked here, before the client, so a
			// mistyped one answers 2 locally rather than being
			// forwarded. The task collection's `?id=` filter IS typed
			// in the contract, so it answered a clean 400 -- but exit
			// 1 is not the code for a bad argument, and this verb's
			// sibling `task list --subject-id` already checks its own
			// (#194). The canonical spelling is what gets sent: the
			// braced and urn forms parse locally and would 400.
			id, err := parseUUIDArg(args[0], "task ID")
			if err != nil {
				return err
			}
			taskID := id.String()

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListTasksParams{Id: &taskID}
			resp, err := client.ListTasksWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("get task: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			tasks := resp.JSON200
			if tasks == nil || len(*tasks) == 0 {
				return newExitError(fmt.Sprintf("task %q not found", taskID), ExitNotFound)
			}
			t := (*tasks)[0]

			if rt.Output.Structured() {
				return rt.Output.Print(&t, nil)
			}

			return printTaskDetail(rt, &t)
		},
	}
}

// printTaskDetail renders a single task in text mode: the summary row,
// then a detail block. See the managed module's copy for why the block
// exists (#180) and why only the latest message is shown; the two trees
// keep their own copies because each binds its own generated api.Task.
func printTaskDetail(rt *module.Runtime, t *api.Task) error {
	if err := rt.Output.Print(
		[]output.Row{taskRow(*t)}, taskColumns); err != nil {
		return err
	}

	out := rt.Output.Out
	// These three are ONE LINE each, so a newline in the value forges a
	// line in a block that sits under a sanitized table on stdout —
	// which is where a caller parses. Escaped for that reason (#345).
	fmt.Fprintf(out, "\nCreated  %s\n", output.Sanitize(t.CreatedAt))
	fmt.Fprintf(out, "Updated  %s\n", output.Sanitize(t.UpdatedAt))
	if n := len(t.Messages); n > 0 {
		fmt.Fprintf(out, "Step     %s\n",
			output.Sanitize(formatTaskStep(t.Messages[n-1])))
	}
	// The error body is NOT escaped, and that is the same call controlplane's
	// printTaskError already made: it owns a region below its own
	// heading, it is the last thing printed, and these messages are
	// deeply wrapped `%w` chains that read far better with their line
	// structure intact. A newline here cannot forge a field line above
	// it, so escaping would cost readability and buy nothing.
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
  pgedge starfleet byoc task wait <task_id> --wait-timeout 600
  pgedge starfleet byoc task wait <task_id> --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The id is checked here, before the client, so a
			// mistyped one answers 2 locally rather than being
			// forwarded. The task collection's `?id=` filter IS typed
			// in the contract, so it answered a clean 400 -- but exit
			// 1 is not the code for a bad argument, and this verb's
			// sibling `task list --subject-id` already checks its own
			// (#194). The canonical spelling is what gets sent: the
			// braced and urn forms parse locally and would 400.
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
				// Each poll is bounded by the wait deadline,
				// floored at one requestBound(): --timeout may be
				// 0 (an unbounded client), and a poll with no
				// bound of its own would outlive --wait-timeout
				// forever. The floor keeps the courtesy poll of a
				// --wait-timeout 0 (which reports the task's last
				// status), honours a raised --timeout, and ends a
				// hung poll's wait at most one request allowance
				// late — the same trade cp makes on its follow
				// poll.
				pollDeadline := deadline
				if floor := time.Now().Add(
					requestBound()); floor.After(pollDeadline) {
					pollDeadline = floor
				}
				ctx, cancel := context.WithDeadline(
					context.Background(), pollDeadline)
				params := &api.ListTasksParams{Id: &taskID}
				resp, err := client.ListTasksWithResponse(ctx, params)
				cancel()
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) &&
						time.Now().After(deadline) {
						return newExitError(fmt.Sprintf(
							"timed out after %ds waiting for task %s",
							timeout, taskID), ExitTimeout)
					}
					return fmt.Errorf("poll task: %w", err)
				}
				if err := checkResponse(resp.StatusCode(),
					string(resp.Body)); err != nil {
					return err
				}

				tasks := resp.JSON200
				if tasks == nil || len(*tasks) == 0 {
					return newExitError(
						fmt.Sprintf("task %q not found", taskID),
						ExitNotFound)
				}
				t := (*tasks)[0]

				// Before the terminal check, so a task that finishes
				// between polls still streams the steps that got it
				// there rather than jumping to the outcome.
				if follow {
					seen = printNewMessages(rt, t.Messages, seen)
				}

				switch t.Status {
				case "succeeded":
					if rt.Output.Structured() {
						return rt.Output.Print(&t, nil)
					}
					fmt.Fprintf(rt.Stderr, "Task %s: %s\n",
						output.Sanitize(t.Id), output.Sanitize(t.Status))
					return nil

				case "failed":
					if rt.Output.Structured() {
						_ = rt.Output.Print(&t, nil)
					}
					msg := fmt.Sprintf("task %s failed", t.Id)
					if t.Error != nil && *t.Error != "" {
						msg = fmt.Sprintf("task %s failed: %s",
							output.Sanitize(t.Id),
							output.Sanitize(*t.Error))
					}
					return newExitError(msg, ExitGeneral)

				default:
					// queued or running — show progress on stderr.
					// Under --follow the step messages are the
					// progress report, so the bare status line would
					// only interleave noise into them.
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
		r.id, r.name, output.ColorStatus(r.status), r.subject, r.created,
	}
}

// taskRow converts an api.Task into a taskRowData for table output.
func taskRow(t api.Task) taskRowData {
	subject := fmt.Sprintf("%s/%s", t.SubjectKind, t.SubjectId)
	return taskRowData{
		id:      t.Id,
		name:    t.Name,
		status:  t.Status,
		subject: subject,
		created: output.FormatTime(t.CreatedAt),
	}
}
