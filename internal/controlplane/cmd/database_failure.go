package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// failureDoc is `database get --failure`'s -o json/yaml document.
type failureDoc struct {
	DatabaseID string   `json:"database_id"`
	State      string   `json:"state"`
	Task       api.Task `json:"task"`
}

// printDatabaseFailure shows the database's state and its latest
// task when that task failed. Anything else is no failure: a sentence
// on stderr and nothing on stdout.
func printDatabaseFailure(
	rt *module.Runtime, client *api.ClientWithResponses, d *api.Database3,
) error {
	one := int64(1)
	resp, err := client.ListDatabaseTasksWithResponse(
		context.Background(), d.Id,
		&api.ListDatabaseTasksParams{Limit: &one})
	if err != nil {
		return networkError("list database tasks", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}
	state := string(d.State)
	if resp.JSON200 == nil || len(resp.JSON200.Tasks) == 0 {
		fmt.Fprintf(rt.Stderr, "Database %s is %s and has no tasks, "+
			"so there is no failure to show.\n",
			output.Sanitize(d.Id), output.Sanitize(state))
		return nil
	}
	task := resp.JSON200.Tasks[0]
	if task.Status != api.TaskStatusFailed {
		fmt.Fprintf(rt.Stderr, "Database %s is %s; its latest task "+
			"%s is %s, so there is no failure to show.\n",
			output.Sanitize(d.Id), output.Sanitize(state),
			task.TaskId, output.Sanitize(string(task.Status)))
		return nil
	}
	if rt.Output.Structured() {
		return rt.Output.Print(failureDoc{
			DatabaseID: d.Id, State: state, Task: task,
		}, nil)
	}
	if err := rt.Output.Print([]output.Row{databaseRow{
		id:      d.Id,
		state:   state,
		created: formatDate(d.CreatedAt),
		updated: formatDate(d.UpdatedAt),
	}}, databaseColumns); err != nil {
		return err
	}
	fmt.Fprintf(rt.Output.Out, "\nLatest task\n")
	if err := rt.Output.Print([]output.Row{taskRowFrom(task)},
		taskColumns); err != nil {
		return err
	}
	return printTaskError(rt, task)
}
