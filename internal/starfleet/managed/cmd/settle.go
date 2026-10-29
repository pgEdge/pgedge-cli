package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/settle"
)

const settleGone = settle.Gone

// subjectStatuses is the documented status vocabulary (the "Database
// status" table in llms.txt). transitionalStatuses is the part of it a
// subject passes through on its way somewhere else.
var (
	subjectStatuses = []string{
		"creating", "available", "modifying", "deleting", "failed",
		"degraded", "suspending", "suspended", "resuming",
	}
	transitionalStatuses = []string{
		"creating", "modifying", "deleting", "suspending", "resuming",
	}
)

type settleTarget = settle.Target

// awaitSettled is settle.Await over managed's vocabulary.
func awaitSettled(
	rt *module.Runtime, t settleTarget,
	deadline time.Time, timeout int, interval time.Duration,
) error {
	t.Transitional = transitionalStatuses
	return settle.Await(rt, t, deadline, timeout, interval, requestBound())
}

// databaseSettle reads a database's status.
func databaseSettle(
	client *api.ClientWithResponses, id, want string,
) settleTarget {
	return settleTarget{Noun: "database", ID: id, Want: want,
		Read: func(ctx context.Context) (string, error) {
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
	return settleTarget{Noun: "branch", ID: branchID, Want: want,
		Read: func(ctx context.Context) (string, error) {
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
