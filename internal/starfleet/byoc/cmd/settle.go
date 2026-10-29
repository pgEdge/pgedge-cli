package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/settle"
)

// subjectStatuses is the "Resource status" table in llms.txt.
var (
	subjectStatuses = []string{
		"creating", "available", "modifying", "restoring", "maintenance",
		"deleting", "failed", "degraded", "archiving", "archived",
	}
	transitionalStatuses = []string{
		"creating", "modifying", "restoring", "maintenance", "deleting",
		"archiving",
	}
)

type settleTarget = settle.Target

// awaitSettled is settle.Await over byoc's vocabulary.
func awaitSettled(
	rt *module.Runtime, t settleTarget,
	deadline time.Time, timeout int, interval time.Duration,
) error {
	t.Transitional = transitionalStatuses
	return settle.Await(rt, t, deadline, timeout, interval, requestBound())
}

// statusRead adapts one generated GET into a settle read. id is
// parsed here rather than by the caller, because a create learns it
// from a response body.
func statusRead(
	noun, id, want string,
	get func(ctx context.Context, id uuid.UUID) (int, []byte, *string, error),
) settleTarget {
	return settleTarget{Noun: noun, ID: id, Want: want,
		Read: func(ctx context.Context) (string, error) {
			uid, err := uuid.Parse(id)
			if err != nil {
				return "", newExitError(fmt.Sprintf(
					"invalid %s ID %q: %v", noun, id, err), ExitGeneral)
			}
			code, body, status, err := get(ctx, uid)
			if err != nil {
				return "", fmt.Errorf("get %s: %w", noun, err)
			}
			if err := checkResponse(code, string(body)); err != nil {
				return "", err
			}
			if status == nil {
				return "", newExitError(fmt.Sprintf(
					"get %s: HTTP %d carried no readable %s",
					noun, code, noun), ExitGeneral)
			}
			return *status, nil
		}}
}

func databaseSettle(
	client *api.ClientWithResponses, id, want string,
) settleTarget {
	return statusRead("database", id, want,
		func(ctx context.Context, id uuid.UUID) (int, []byte, *string, error) {
			resp, err := client.GetDatabaseWithResponse(
				ctx, id, &api.GetDatabaseParams{})
			if err != nil {
				return 0, nil, nil, err
			}
			if resp.JSON200 == nil {
				return resp.StatusCode(), resp.Body, nil, nil
			}
			return resp.StatusCode(), resp.Body, &resp.JSON200.Status, nil
		})
}

func clusterSettle(
	client *api.ClientWithResponses, id, want string,
) settleTarget {
	return statusRead("cluster", id, want,
		func(ctx context.Context, id uuid.UUID) (int, []byte, *string, error) {
			resp, err := client.GetClusterWithResponse(ctx, id)
			if err != nil {
				return 0, nil, nil, err
			}
			if resp.JSON200 == nil {
				return resp.StatusCode(), resp.Body, nil, nil
			}
			return resp.StatusCode(), resp.Body, &resp.JSON200.Status, nil
		})
}

func backupStoreSettle(
	client *api.ClientWithResponses, id, want string,
) settleTarget {
	return statusRead("backup store", id, want,
		func(ctx context.Context, id uuid.UUID) (int, []byte, *string, error) {
			resp, err := client.GetBackupStoreWithResponse(ctx, id)
			if err != nil {
				return 0, nil, nil, err
			}
			if resp.JSON200 == nil {
				return resp.StatusCode(), resp.Body, nil, nil
			}
			return resp.StatusCode(), resp.Body, &resp.JSON200.Status, nil
		})
}

// withSubjectStatus sharpens a busy refusal with one read of the
// subject: byoc's busy 400 does not name the status, and a failed
// database answers it exactly as a modifying one does, so "wait and
// retry" alone would hold a failed one forever. Any other error, or a
// read that fails, returns err unchanged.
func withSubjectStatus(err error, t settleTarget) error {
	var ee *ExitError
	if !errors.As(err, &ee) || !ee.Busy() {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestBound())
	defer cancel()
	status, rerr := t.Read(ctx)
	if rerr != nil {
		return err
	}
	var next string
	switch {
	case slices.Contains(transitionalStatuses, status):
		next = "it is mid-operation, so retry once it settles"
	case status == "available":
		next = "retry"
	default:
		next = fmt.Sprintf("waiting will not change it; "+
			"'task list --subject-id %s' shows the last task",
			output.Sanitize(t.ID))
	}
	return newExitError(fmt.Sprintf("%s\n%s %s is %q: %s.", ee.Error(),
		t.Noun, output.Sanitize(t.ID), output.Sanitize(status), next),
		ee.Code())
}

const settleGone = settle.Gone

// endState is trackMutation's end argument for a known end state.
func endState(t settleTarget) *settleTarget { return &t }
