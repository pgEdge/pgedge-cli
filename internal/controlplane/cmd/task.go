package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

var taskColumns = []string{"TASK-ID", "TYPE", "STATUS", "SCOPE",
	"ENTITY", "CREATED"}

// scopeRequired names where to find the value, not just the flags:
// the reader is usually already stuck on a failed create.
func scopeRequired() error {
	return &ExitError{
		msg: "provide --database or --host: a task belongs to the " +
			"database or host it ran against, and 'pgedge controlplane task " +
			"list' shows that value in its ENTITY column",
		code: ExitUsage,
	}
}

type taskRow struct {
	id, typ, status, scope, entity, created string
}

func (r taskRow) Columns() []string {
	return []string{r.id, r.typ, output.ColorStatus(r.status),
		r.scope, r.entity, r.created}
}

func taskRowFrom(t api.Task) taskRow {
	return taskRow{
		id: t.TaskId.String(), typ: t.Type, status: string(t.Status),
		scope: string(t.Scope), entity: t.EntityId,
		created: formatDate(t.CreatedAt),
	}
}

// printTaskError is where a provisioning failure explains itself: a
// failed create marks its instances `failed` with no reason, and a spec
// that fails planning creates no instance, so `database get` has
// nothing to show.
//
// Printed verbatim, unlike `database get`'s cells: it has a line to
// itself, and these deeply wrapped `%w` chains read better intact.
func printTaskError(rt *module.Runtime, t api.Task) error {
	msg := strings.TrimSpace(output.DerefString(t.Error))
	if msg == "" {
		return nil
	}
	fmt.Fprintf(rt.Output.Out, "\nError\n%s\n", msg)
	return nil
}

func parseTaskID(arg string) (uuid.UUID, error) {
	tid, err := uuid.Parse(arg)
	if err != nil {
		return uuid.UUID{}, &ExitError{
			msg:  fmt.Sprintf("invalid task id %q: %v", arg, err),
			code: ExitUsage,
		}
	}
	return tid, nil
}

// NewTaskCmd builds `pgedge controlplane task`.
func NewTaskCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"tasks"},
		Short:   "Inspect Control Plane tasks",
		Long: `task lists asynchronous operations and shows their status
and logs. Mutations (database create, host remove, ...) spawn tasks.

Example:
  pgedge controlplane task list
  pgedge controlplane task get --database my-db <task_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newTaskListCmd(rt), newTaskGetCmd(rt), newTaskLogsCmd(rt),
		newTaskCancelCmd(rt))
	return cmd
}

func newTaskListCmd(rt *module.Runtime) *cobra.Command {
	var scope, entity, database, host string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Long: `list shows recent tasks, newest first. Filter the global
list by --scope (database or host) and --entity-id, or list a single
resource's tasks with --database or --host. Cap with --limit.

Example:
  pgedge controlplane task list
  pgedge controlplane task list --database my-db
  pgedge controlplane task list --scope host --limit 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if database != "" && host != "" {
				return &ExitError{
					msg:  "--database and --host are mutually exclusive",
					code: ExitUsage,
				}
			}
			if (database != "" || host != "") &&
				(scope != "" || entity != "") {
				return &ExitError{
					msg: "--database/--host cannot be combined with " +
						"--scope/--entity-id",
					code: ExitUsage,
				}
			}
			// control-plane.json declares no bounds on any of its
			// five limit parameters.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			jsonMode := rt.Output.Structured()
			var limitPtr *int64
			if sendLimit {
				l := int64(limit)
				limitPtr = &l
			}
			var tasks []api.Task
			switch {
			case database != "":
				resp, err := client.ListDatabaseTasksWithResponse(
					context.Background(), database,
					&api.ListDatabaseTasksParams{Limit: limitPtr})
				if err != nil {
					return networkError("list database tasks", err)
				}
				if err := checkResponse(resp.StatusCode(),
					string(resp.Body)); err != nil {
					return err
				}
				if jsonMode {
					return rt.Output.Print(resp.JSON200, nil)
				}
				if resp.JSON200 != nil {
					tasks = resp.JSON200.Tasks
				}
			case host != "":
				resp, err := client.ListHostTasksWithResponse(
					context.Background(), host,
					&api.ListHostTasksParams{Limit: limitPtr})
				if err != nil {
					return networkError("list host tasks", err)
				}
				if err := checkResponse(resp.StatusCode(),
					string(resp.Body)); err != nil {
					return err
				}
				if jsonMode {
					return rt.Output.Print(resp.JSON200, nil)
				}
				if resp.JSON200 != nil {
					tasks = resp.JSON200.Tasks
				}
			default:
				params := &api.ListTasksParams{}
				if scope != "" {
					s := api.ListTasksParamsScope(scope)
					params.Scope = &s
				}
				if entity != "" {
					params.EntityId = &entity
				}
				params.Limit = limitPtr
				resp, err := client.ListTasksWithResponse(
					context.Background(), params)
				if err != nil {
					return networkError("list tasks", err)
				}
				if err := checkResponse(resp.StatusCode(),
					string(resp.Body)); err != nil {
					return err
				}
				if jsonMode {
					return rt.Output.Print(resp.JSON200, nil)
				}
				if resp.JSON200 != nil {
					tasks = resp.JSON200.Tasks
				}
			}
			if len(tasks) == 0 {
				fmt.Fprintln(rt.Stderr, "No tasks found.")
				return nil
			}
			rows := make([]output.Row, 0, len(tasks))
			for _, tk := range tasks {
				rows = append(rows, taskRowFrom(tk))
			}
			return rt.Output.Print(rows, taskColumns)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "",
		"Filter the global list by scope: database or host")
	cmd.Flags().StringVar(&entity, "entity-id", "",
		"Filter the global list by entity (database_id or host_id)")
	cmd.Flags().StringVar(&database, "database", "",
		"List tasks for a single database")
	cmd.Flags().StringVar(&host, "host", "",
		"List tasks for a single host")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum tasks to return")
	return cmd
}

func newTaskGetCmd(rt *module.Runtime) *cobra.Command {
	var database, host string
	cmd := &cobra.Command{
		Use:   "get <task_id>",
		Short: "Show a task's status",
		Long: `get shows a single task. One of --database or --host is
required, naming the task's scope entity; the ENTITY column of
'pgedge controlplane task list' is that value.

Example:
  pgedge controlplane task get --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
  pgedge controlplane task get --host host-1 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if database == "" && host == "" {
				return scopeRequired()
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			tid, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			var task *api.Task
			var body string
			var status int
			if database != "" {
				resp, rerr := client.GetDatabaseTaskWithResponse(
					context.Background(), database, tid)
				if rerr != nil {
					return networkError("get task", rerr)
				}
				status, body, task = resp.StatusCode(),
					string(resp.Body), resp.JSON200
			} else {
				resp, rerr := client.GetHostTaskWithResponse(
					context.Background(), host, tid)
				if rerr != nil {
					return networkError("get task", rerr)
				}
				status, body, task = resp.StatusCode(),
					string(resp.Body), resp.JSON200
			}
			if err := checkResponse(status, body); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(task, nil)
			}
			if task == nil {
				fmt.Fprintln(rt.Stderr, "No task data returned.")
				return nil
			}
			if err := rt.Output.Print(
				[]output.Row{taskRowFrom(*task)},
				taskColumns); err != nil {
				return err
			}
			return printTaskError(rt, *task)
		},
	}
	cmd.Flags().StringVar(&database, "database", "",
		"Database ID that owns the task (required unless --host)")
	cmd.Flags().StringVar(&host, "host", "",
		"Host ID that owns the task (required unless --database)")
	return cmd
}

func newTaskLogsCmd(rt *module.Runtime) *cobra.Command {
	var database, host string
	cmd := &cobra.Command{
		Use:   "logs <task_id>",
		Short: "Show a task's log",
		Long: `logs prints the log entries for a task. One of --database
or --host is required, naming the task's scope entity; the ENTITY
column of 'pgedge controlplane task list' is that value.

Example:
  pgedge controlplane task logs --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if database == "" && host == "" {
				return scopeRequired()
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			tid, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			var log *api.TaskLog
			var body string
			var status int
			if database != "" {
				resp, rerr := client.GetDatabaseTaskLogWithResponse(
					context.Background(), database, tid,
					&api.GetDatabaseTaskLogParams{})
				if rerr != nil {
					return networkError("get task log", rerr)
				}
				status, body, log = resp.StatusCode(),
					string(resp.Body), resp.JSON200
			} else {
				resp, rerr := client.GetHostTaskLogWithResponse(
					context.Background(), host, tid,
					&api.GetHostTaskLogParams{})
				if rerr != nil {
					return networkError("get task log", rerr)
				}
				status, body, log = resp.StatusCode(),
					string(resp.Body), resp.JSON200
			}
			if err := checkResponse(status, body); err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(log, nil)
			}
			if log == nil {
				fmt.Fprintln(rt.Stderr, "No task log returned.")
				return nil
			}
			for _, e := range log.Entries {
				// Only the message is server text; a newline in it
				// would forge a log line.
				fmt.Fprintf(rt.Stdout, "%s  %s\n",
					e.Timestamp.Format(time.RFC3339),
					output.Sanitize(e.Message))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&database, "database", "",
		"Database ID that owns the task (required unless --host)")
	cmd.Flags().StringVar(&host, "host", "",
		"Host ID that owns the task (required unless --database)")
	return cmd
}

func newTaskCancelCmd(rt *module.Runtime) *cobra.Command {
	var database, host string
	var force bool
	cmd := &cobra.Command{
		Use:   "cancel <task_id>",
		Short: "Cancel a running database task",
		Long: `cancel requests cancellation of a database task. Provide
--database to name the task's database. Only database tasks can be
canceled. This is disruptive; prompts for confirmation (use --force to
skip). Asynchronous; use --wait/--follow to track it.

Example:
  pgedge controlplane task cancel --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
  pgedge controlplane task cancel --database my-db \
    019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90 --force --wait`,
		Args: cobra.ExactArgs(1),
	}
	wf := addWaitFollowFlags(cmd)
	cmd.Flags().StringVar(&database, "database", "",
		"Database ID that owns the task (required)")
	cmd.Flags().StringVar(&host, "host", "",
		"Host ID (cancel is not supported for host tasks)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if host != "" {
			return &ExitError{
				msg:  "cancel is only supported for database tasks",
				code: ExitUsage,
			}
		}
		if database == "" {
			return &ExitError{msg: "provide --database", code: ExitUsage}
		}
		// `.` and `..` survive path escaping and url.Parse resolves
		// them away, so the path no longer matches the cancel operation
		// and --dry-run sends a request instead of reporting. Checked
		// against every Control Plane path template: it cannot reach
		// another state-changing operation.
		if database == "." || database == ".." {
			return &ExitError{
				msg: fmt.Sprintf(
					"invalid --database %q: a database id cannot be a "+
						"path segment", database),
				code: ExitUsage,
			}
		}
		tid, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		if err := cli.Confirm(rt,
			fmt.Sprintf("Cancel task %s?", tid), force); err != nil {
			return err
		}
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		resp, err := client.CancelDatabaseTaskWithResponse(
			context.Background(), database, tid.String())
		if err != nil {
			return networkError("cancel task", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		fmt.Fprintf(rt.Stderr,
			"Cancellation of task %s requested.\n", tid)
		// CancelDatabaseTask's 200 body is a bare Task, not an
		// envelope, so -o json prints the task at the top level.
		if resp.JSON200 != nil {
			if err := emitAccepted(rt, resp.JSON200); err != nil {
				return err
			}
		}
		src := databaseTaskSource(client, database, tid)
		src.canceledOK = true
		return wf.run(rt, src)
	}
	cli.MarkMutating(cmd)

	return cmd
}
