package cmd

import (
	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// What each byoc list endpoint measurably does with --limit: the API's
// default and its clamp. These are never sent and never refuse
// anything; they feed cli.PrintTruncationHint's guess at whether a page
// was cut short, the only signal available because no List*Response
// or GetBackupRepositoryInfoResponse carries a total count. They live
// here, not in internal/cli, because they are module facts.
//
// backupInfo defaults its limit to 100 but never clamps a
// caller-supplied value, so it has no known cap. Cross-checked live
// (2026-08-07): a bare `task list` returned 25 rows twice, and
// `--limit 200` returned 100.
var (
	backupStoreDefaults      = cli.PageDefaults{Def: 10, Cap: 100}
	clusterDefaults          = cli.PageDefaults{Def: 10, Cap: 100}
	databaseDefaults         = cli.PageDefaults{Def: 10, Cap: 100}
	ingressDefaults          = cli.PageDefaults{Def: 10, Cap: 100}
	taskDefaults             = cli.PageDefaults{Def: 25, Cap: 100}
	backupRepositoryDefaults = cli.PageDefaults{Def: 10, Cap: 100}
	backupInfoDefaults       = cli.PageDefaults{Def: 100, Cap: 0}
)
