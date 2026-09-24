package cmd

import (
	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// What each byoc list endpoint MEASURABLY does with --limit. Neither
// value is ever sent to the API — the CLI still asks for whatever
// --limit and --offset say — and neither gates a refusal; they feed
// cli.PrintTruncationHint's guess at whether a returned page might
// have been cut short. None of these endpoints reports a total count
// in its response (checked against every List*Response and
// GetBackupRepositoryInfoResponse in
// internal/starfleet/byoc/api/client.gen.go), so a size-based guess is
// the only signal available.
//
// The table stays in this package rather than moving to internal/cli
// with the mechanism: what /byoc/v1/clusters does is a module fact,
// and its provenance belongs beside it.
//
// Evidence — the API's own clamp on Limit for each operation, which
// gives the values below. backupInfo (get) defaults its limit to 100
// but never clamps a caller-supplied value; the only bound is the
// pgBackRest inventory's own length, so there is no known cap to
// record here.
//
// Cross-checked live (2026-08-07):
// a bare `task list` returned 25 rows twice, and `--limit 200`
// returned 100 — both match taskDefaults below exactly.
var (
	backupStoreDefaults      = cli.PageDefaults{Def: 10, Cap: 100}
	clusterDefaults          = cli.PageDefaults{Def: 10, Cap: 100}
	databaseDefaults         = cli.PageDefaults{Def: 10, Cap: 100}
	ingressDefaults          = cli.PageDefaults{Def: 10, Cap: 100}
	taskDefaults             = cli.PageDefaults{Def: 25, Cap: 100}
	backupRepositoryDefaults = cli.PageDefaults{Def: 10, Cap: 100}
	backupInfoDefaults       = cli.PageDefaults{Def: 100, Cap: 0}
)
