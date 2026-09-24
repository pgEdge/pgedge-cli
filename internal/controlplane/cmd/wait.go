package cmd

import (
	"context"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// isTerminal reports whether a task status is final.
func isTerminal(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

// taskSource polls one database- or host-scoped task. getTask/getLog
// return the decoded body, status, raw body and transport error.
type taskSource struct {
	taskID  openapi_types.UUID
	getTask func(context.Context) (*api.Task, int, string, error)
	getLog  func(context.Context) (*api.TaskLog, int, string, error)
	// canceledOK makes "canceled" a success; only `task cancel` sets it.
	canceledOK bool
}

func databaseTaskSource(
	client *api.ClientWithResponses, dbID string,
	taskID openapi_types.UUID,
) taskSource {
	return taskSource{
		taskID: taskID,
		getTask: func(ctx context.Context) (*api.Task, int, string, error) {
			resp, err := client.GetDatabaseTaskWithResponse(
				ctx, dbID, taskID)
			if err != nil {
				return nil, 0, "", err
			}
			return resp.JSON200, resp.StatusCode(), string(resp.Body), nil
		},
		getLog: func(ctx context.Context) (*api.TaskLog, int, string, error) {
			resp, err := client.GetDatabaseTaskLogWithResponse(
				ctx, dbID, taskID, &api.GetDatabaseTaskLogParams{})
			if err != nil {
				return nil, 0, "", err
			}
			return resp.JSON200, resp.StatusCode(), string(resp.Body), nil
		},
	}
}

func hostTaskSource(
	client *api.ClientWithResponses, hostID string,
	taskID openapi_types.UUID,
) taskSource {
	return taskSource{
		taskID: taskID,
		getTask: func(ctx context.Context) (*api.Task, int, string, error) {
			resp, err := client.GetHostTaskWithResponse(
				ctx, hostID, taskID)
			if err != nil {
				return nil, 0, "", err
			}
			return resp.JSON200, resp.StatusCode(), string(resp.Body), nil
		},
		getLog: func(ctx context.Context) (*api.TaskLog, int, string, error) {
			resp, err := client.GetHostTaskLogWithResponse(
				ctx, hostID, taskID, &api.GetHostTaskLogParams{})
			if err != nil {
				return nil, 0, "", err
			}
			return resp.JSON200, resp.StatusCode(), string(resp.Body), nil
		},
	}
}

// waitFollowOpts binds the async flags per command, not as globals.
type waitFollowOpts struct {
	wait     bool
	follow   bool
	timeout  int
	interval int
}

func addWaitFollowFlags(cmd *cobra.Command) *waitFollowOpts {
	o := &waitFollowOpts{}
	cmd.Flags().BoolVar(&o.wait, "wait", false,
		"Wait for the task to reach a terminal state")
	cmd.Flags().BoolVar(&o.follow, "follow", false,
		"Stream the task log until it reaches a terminal state")
	// run() passes o.timeout only to waitForTask: --follow has no
	// overall bound.
	cmd.Flags().IntVar(&o.timeout, "wait-timeout", 600,
		"Max seconds to wait when --wait is set (--follow is unbounded)")
	cmd.Flags().IntVar(&o.interval, "wait-interval", 3,
		"Polling interval in seconds when --wait is set")
	return o
}

func (o *waitFollowOpts) run(
	rt *module.Runtime, src taskSource,
) error {
	switch {
	case o.follow:
		return followTask(rt, src)
	case o.wait:
		return waitForTask(rt, src, o.timeout, o.interval)
	default:
		return nil
	}
}

// waitForTask polls until the task is terminal, with progress on
// rt.Stderr.
func waitForTask(
	rt *module.Runtime, src taskSource, timeout, interval int,
) error {
	if interval < 1 {
		interval = 1
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	iv := time.Duration(interval) * time.Second
	waitExpired := func() error {
		return &ExitError{
			msg: fmt.Sprintf(
				"timed out after %ds waiting for task %s",
				timeout, src.taskID),
			code: ExitTimeout,
		}
	}
	for {
		if time.Now().After(deadline) {
			return waitExpired()
		}
		ctx, cancel := context.WithDeadline(
			context.Background(), deadline)
		task, status, body, err := src.getTask(ctx)
		cancel()
		if err != nil {
			// ctx carries the loop's deadline, so an error after it
			// is --wait-timeout expiring mid-poll: exit 3, not a
			// networkError naming --timeout at exit 1.
			if time.Now().After(deadline) {
				return waitExpired()
			}
			return networkError("get task", err)
		}
		if err := checkResponse(status, body); err != nil {
			return err
		}
		if task != nil && isTerminal(string(task.Status)) {
			if src.canceledOK && string(task.Status) == "canceled" {
				fmt.Fprintf(rt.Stderr, "Task %s canceled.\n", task.TaskId)
				return nil
			}
			return terminalResult(rt, task)
		}
		if task != nil {
			fmt.Fprintf(rt.Stderr, "Task %s: %s...\n",
				task.TaskId, output.Sanitize(string(task.Status)))
		}
		time.Sleep(iv)
	}
}

func terminalResult(rt *module.Runtime, t *api.Task) error {
	switch t.Status {
	case "failed":
		msg := fmt.Sprintf("task %s failed", t.TaskId)
		if t.Error != nil && *t.Error != "" {
			msg = fmt.Sprintf("task %s failed: %s", t.TaskId,
				output.Sanitize(*t.Error))
		}
		return &ExitError{msg: msg, code: ExitGeneral}
	case "canceled":
		return &ExitError{
			msg:  fmt.Sprintf("task %s was canceled", t.TaskId),
			code: ExitGeneral,
		}
	default:
		fmt.Fprintf(rt.Stderr, "Task %s: %s.\n", t.TaskId,
			output.Sanitize(string(t.Status)))
		return nil
	}
}

// followPollTimeout bounds one log poll under --follow: --wait-timeout
// does not reach followTask and --timeout may be 0. A var only so a
// test can shorten it.
var followPollTimeout = 30 * time.Second

func followTask(rt *module.Runtime, src taskSource) error {
	seen := 0
	for {
		ctx, cancel := context.WithTimeout(
			context.Background(), followPollTimeout)
		log, status, body, err := src.getLog(ctx)
		// Read before cancel(): after it, ctx.Err() is Canceled for
		// every outcome and cannot say whether the bound fired.
		pollExpired := ctx.Err() != nil
		cancel()
		if err != nil {
			// followTask's own bound, so naming --timeout would
			// misdiagnose it.
			if pollExpired {
				return &ExitError{
					msg: fmt.Sprintf(
						"timed out after %s reading task %s's log; "+
							"--follow bounds each log poll itself, "+
							"independently of --timeout",
						followPollTimeout, src.taskID),
					code: ExitTimeout,
				}
			}
			return networkError("get task log", err)
		}
		if err := checkResponse(status, body); err != nil {
			return err
		}
		if log == nil {
			return nil
		}
		for i := seen; i < len(log.Entries); i++ {
			fmt.Fprintf(rt.Stderr, "%s  %s\n",
				log.Entries[i].Timestamp.Format(time.RFC3339),
				output.Sanitize(log.Entries[i].Message))
		}
		seen = len(log.Entries)
		if isTerminal(string(log.TaskStatus)) {
			switch string(log.TaskStatus) {
			case "failed":
				return &ExitError{
					msg:  fmt.Sprintf("task %s failed", src.taskID),
					code: ExitGeneral,
				}
			case "canceled":
				if src.canceledOK {
					fmt.Fprintf(rt.Stderr, "Task %s canceled.\n",
						src.taskID)
					return nil
				}
				return &ExitError{
					msg:  fmt.Sprintf("task %s was canceled", src.taskID),
					code: ExitGeneral,
				}
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}
