package cmd

import "github.com/pgEdge/pgedge-cli/internal/cli"

// The declared bounds on managed's paging parameters, one constant per
// endpoint. `--limit` is spelled the same on every list verb, but
// managed.yaml bounds it per operation, so one shared constant would
// either refuse a value /managed/v1/databases accepts or admit one
// /managed/v1/backups clamps. TestPagingBoundsMatchTheSpec pins each
// to its own parameter, so a re-vendor that moves a bound fails the
// build.
//
// /managed/v1/tasks declares no bounds, hence no taskLimitMax and
// cli.NoUpperBound on `task list`. The server was measured clamping
// `--limit 500` to 100 rows there, but pinning 100 locally would
// refuse a value the API accepts the day saas publishes a higher cap.
// The truncation hint covers the clamp instead: a full page prints
// `Showing first N results...` on stderr.
const (
	// backupLimitMax is `limit`'s maximum on /managed/v1/backups,
	// where it equals the declared default — so --limit can only
	// narrow that page.
	backupLimitMax = 100

	// databaseLimitMax is `limit`'s maximum on /managed/v1/databases.
	databaseLimitMax = 1000
)

// The MINIMA are cli.LimitLowest and cli.OffsetLowest, not constants
// here: managed declares them, byoc and controlplane declare none.
// TestPagingBoundsMatchTheSpec asserts managed's declarations agree
// with those shared floors, so this file also fails the build if saas
// moves a minimum.

// What these three list endpoints measurably do with --limit. Unlike
// the maxima above, which gate a refusal and so come from managed.yaml,
// these gate only the truncation hint and come from saas's source, so
// a measured cap here sets no local ceiling: nothing reads this table
// to refuse a value.
//
// MOVING A VALUE HERE MEANS RE-DATING ITS CITATION IN THE SAME DIFF.
// TestManagedPagingProseMatchesPageDefaults holds the reference to this
// table and `task list` builds its flag help from it, so nothing can
// contradict it, which leaves these comments as the only evidence the
// numbers are true.
//
// Evidence, from the saas checkout at 21e04c45, following each
// parameter from the handler to the query:
//
//	task:     internal/starfleet/tasks/repo/tasks.go:102-105 —
//	          `if input.Limit <= 0 { input.Limit = 25 } else if
//	          input.Limit > 100 { input.Limit = 100 }`. Both numbers
//	          in one place, and neither is in the contract.
//	backup:   internal/starfleet/api/backups.go:167-169 declares
//	          defaultBackupLimit and maxBackupLimit as 100 and 100,
//	          :182 seeds the filter with the default, and :200-202
//	          applies `min(*params.Limit, maxBackupLimit)`.
//	database: internal/starfleet/api/managed_databases.go:43-44 copies
//	          `limit` through with no default and no clamp, and
//	          internal/starfleet/managed_databases/repo/
//	          managed_database_repo.go:249-251 applies it only when it
//	          is positive. So an omitted --limit reads EVERY row.
var (
	// 25 rows when --limit is omitted, clamped to 100. Cross-checked
	// live on a managed dev tenant: a bare `task list` returned 25 and
	// `--limit 500` returned 100.
	taskPageDefaults = cli.PageDefaults{Def: 25, Cap: 100}

	// Def and Cap are the same number, for the reason backupLimitMax
	// records. Since the CLI already refuses a --limit above it
	// locally, this Cap is reachable only by omitting the flag.
	backupPageDefaults = cli.PageDefaults{
		Def: backupLimitMax, Cap: backupLimitMax,
	}

	// Def 0 is a MEASUREMENT, not a gap: /managed/v1/databases applies
	// no default page, so an omitted --limit returns every row and
	// cannot have been truncated. cli.PrintTruncationHint stays silent
	// for that case rather than firing on every non-empty list, and
	// hints only when the caller set --limit. Cap 0 because nothing
	// clamps. Do NOT copy databaseLimitMax across: 1000 is what the
	// CONTRACT allows a caller to ask for, not a page the server
	// imposes.
	databasePageDefaults = cli.PageDefaults{Def: 0, Cap: 0}
)
