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
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// settleGone is the end state a delete waits for: the read answering
// 404.
const settleGone = "deleted"

// subjectStatuses is the documented status vocabulary (the "Database
// status" table in llms.txt). transitionalStatuses is the part of it a
// subject passes through on its way somewhere else; every other status,
// and any status outside the vocabulary, is where it has come to rest.
var (
	subjectStatuses = []string{
		"creating", "available", "modifying", "deleting", "failed",
		"degraded", "suspending", "suspended", "resuming",
	}
	transitionalStatuses = []string{
		"creating", "modifying", "deleting", "suspending", "resuming",
	}
)

// settleTarget is the subject --wait re-reads after its task succeeds,
// and the status that means the write is done. It names no managed API
// type, so byoc can adopt the same contract by supplying its own read.
type settleTarget struct {
	noun string
	id   string
	want string
	// read returns the subject's status, or an ExitNotFound error once
	// it is gone.
	read func(ctx context.Context) (string, error)
}

// awaitSettled polls t until it reaches t.want, sharing the deadline the
// task wait used. A succeeded task can leave the status a poll behind,
// so a transitional status is polled through; any other status is
// final and exits 1 naming it.
func awaitSettled(
	rt *module.Runtime, t settleTarget,
	deadline time.Time, timeout int, interval time.Duration,
) error {
	last := ""
	for {
		if last != "" && time.Now().After(deadline) {
			return settleTimeout(t, timeout, last)
		}
		// Floored at one request allowance, as task wait's poll is, so
		// a task that succeeds on the deadline still gets its read.
		pollDeadline := deadline
		if floor := time.Now().Add(requestBound()); floor.After(deadline) {
			pollDeadline = floor
		}
		ctx, cancel := context.WithDeadline(
			context.Background(), pollDeadline)
		status, err := t.read(ctx)
		expired := time.Now().After(deadline)
		cancel()

		var ee *ExitError
		switch {
		case err != nil && t.want == settleGone &&
			errors.As(err, &ee) && ee.Code() == ExitNotFound:
			status = settleGone
		case err != nil && errors.Is(err, context.DeadlineExceeded) &&
			expired:
			return settleTimeout(t, timeout, last)
		case err != nil:
			return err
		}

		if status == t.want {
			fmt.Fprintf(rt.Stderr, "%s %s: %s.\n", output.Sanitize(t.noun),
				output.Sanitize(t.id), output.Sanitize(status))
			return nil
		}
		if !slices.Contains(transitionalStatuses, status) {
			return newExitError(fmt.Sprintf("%s %s is %q, not %s",
				t.noun, output.Sanitize(t.id), output.Sanitize(status),
				t.want), ExitGeneral)
		}
		fmt.Fprintf(rt.Stderr, "%s %s: %s...\n", output.Sanitize(t.noun),
			output.Sanitize(t.id), output.Sanitize(status))
		last = status
		time.Sleep(interval)
	}
}

func settleTimeout(t settleTarget, timeout int, last string) error {
	msg := fmt.Sprintf("timed out after %ds waiting for %s %s to be %s",
		timeout, t.noun, t.id, t.want)
	if last != "" {
		msg += fmt.Sprintf(" (last status: %s)", output.Sanitize(last))
	}
	return newExitError(msg, ExitTimeout)
}

// databaseSettle reads a database's status.
func databaseSettle(
	client *api.ClientWithResponses, id, want string,
) settleTarget {
	return settleTarget{noun: "database", id: id, want: want,
		read: func(ctx context.Context) (string, error) {
			uid, err := uuid.Parse(id)
			if err != nil {
				return "", newExitError(fmt.Sprintf(
					"invalid database ID %q: %v", id, err), ExitGeneral)
			}
			resp, err := client.GetManagedDatabaseWithResponse(
				ctx, uid, &api.GetManagedDatabaseParams{})
			if err != nil {
				return "", fmt.Errorf("get database: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return "", err
			}
			if resp.JSON200 == nil {
				return "", newExitError(fmt.Sprintf(
					"get database: HTTP %d carried no readable database",
					resp.StatusCode()), ExitGeneral)
			}
			return resp.JSON200.Status, nil
		}}
}

// branchSettle reads a branch's status.
func branchSettle(
	client *api.ClientWithResponses, databaseID, branchID, want string,
) settleTarget {
	return settleTarget{noun: "branch", id: branchID, want: want,
		read: func(ctx context.Context) (string, error) {
			dbID, err := uuid.Parse(databaseID)
			if err != nil {
				return "", newExitError(fmt.Sprintf(
					"invalid database ID %q: %v", databaseID, err),
					ExitGeneral)
			}
			bID, err := uuid.Parse(branchID)
			if err != nil {
				return "", newExitError(fmt.Sprintf(
					"invalid branch ID %q: %v", branchID, err), ExitGeneral)
			}
			resp, err := client.GetBranchWithResponse(
				ctx, dbID, bID, &api.GetBranchParams{})
			if err != nil {
				return "", fmt.Errorf("get branch: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return "", err
			}
			if resp.JSON200 == nil {
				return "", newExitError(fmt.Sprintf(
					"get branch: HTTP %d carried no readable branch",
					resp.StatusCode()), ExitGeneral)
			}
			return resp.JSON200.Status, nil
		}}
}
