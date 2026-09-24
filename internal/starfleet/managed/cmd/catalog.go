package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// The catalog checks refuse a --size or --region the API has not
// published. The vocabulary is whatever `size list` and `region list`
// return today, so a hardcoded list would refuse the size the API adds
// next, the failure the spec-pinned enums in specenums_test.go catch
// for the fixed vocabularies.
//
// They therefore run after clientFromCmd, unlike the --name,
// --pg-version and --display-name checks, which keep exit 2. With no
// credentials, `--size enormous` answers exit 5, because the CLI cannot
// tell whether the value is wrong without asking. A blank value never
// gets this far: cli.RequiredStringFlag and cli.OptionalStringFlag
// refuse it at exit 2 before the client.
//
// Only a value absent from the list is refused. `size list` publishes
// the currently-active row per name, so a name whose rows are all
// deprecated or retired is refused here at exit 2. Whether the API
// would have accepted such a create is unmeasured; `size get <id>`
// still reads such a row, "regardless of its lifecycle status".
//
// Both fail open when the catalog read returns nothing, warning and
// sending the value anyway, so they can never refuse something the API
// would accept: refusing would ground a create on an upstream blip the
// caller cannot fix. resolveSoleRegion refuses the same empty list
// because it has nothing to send. The warning goes to stderr
// unconditionally, not only to the ledger, which is read only under
// --dry-run, so an operator who is not dry-running can still tell a
// passed check from one that never ran. backupStoreWarning on `byoc
// cluster create` has the same shape.

// uncheckable reports a check that could not run, to stderr and to the
// dry-run ledger, in one wording so the two cannot drift.
func uncheckable(rt *module.Runtime, flag, value, plural string) {
	msg := fmt.Sprintf("%s %q not checked: the API published no %s to "+
		"check it against", flag, value, plural)
	fmt.Fprintln(rt.Stderr, "warning: "+msg)
	rt.DryRun.Pass("%s", msg)
}

// fetchSizeNames returns the size names `size list` publishes.
func fetchSizeNames(
	ctx context.Context, client *api.ClientWithResponses,
) ([]string, error) {
	resp, err := client.ListSizesWithResponse(ctx, &api.ListSizesParams{})
	if err != nil {
		return nil, fmt.Errorf("list sizes: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	// Deduplicated defensively: managed.yaml has this endpoint "return
	// the currently-active managed-size row for each size name", so
	// this should never fire, but a duplicate would otherwise appear
	// twice in the "expected one of" message.
	var names []string
	for _, s := range *resp.JSON200 {
		if !slices.Contains(names, s.Name) {
			names = append(names, s.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// fetchRegionNames returns the regions `region list` publishes.
func fetchRegionNames(
	ctx context.Context, client *api.ClientWithResponses,
) ([]string, error) {
	resp, err := client.ListManagedRegionsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	names := make([]string, 0, len(*resp.JSON200))
	for _, r := range *resp.JSON200 {
		names = append(names, r.Region)
	}
	slices.Sort(names)
	return names, nil
}

// validateSize refuses a --size the API has not published, and records
// the pass in the dry-run ledger.
func validateSize(
	ctx context.Context, rt *module.Runtime,
	client *api.ClientWithResponses, size string,
) error {
	names, err := fetchSizeNames(ctx, client)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		uncheckable(rt, "size", size, "sizes")
		return nil
	}
	if !slices.Contains(names, size) {
		return newExitError(fmt.Sprintf(
			"unknown size %q (expected one of: %s)",
			size, strings.Join(names, ", ")), ExitUsage)
	}
	rt.DryRun.Pass("size %q is published", size)
	return nil
}

// validateRegion refuses a --region the API has not published. The
// server's own refusal, "no active managed cluster in region %q",
// states the fact `region list` reports ("a region that can host a
// managed database"), so the two agree on what a valid region is.
func validateRegion(
	ctx context.Context, rt *module.Runtime,
	client *api.ClientWithResponses, region string,
) error {
	names, err := fetchRegionNames(ctx, client)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		uncheckable(rt, "region", region, "regions")
		return nil
	}
	if !slices.Contains(names, region) {
		return newExitError(fmt.Sprintf(
			"unknown region %q (expected one of: %s)",
			region, strings.Join(names, ", ")), ExitUsage)
	}
	rt.DryRun.Pass("region %q is published", region)
	return nil
}
