package clitest

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	cpcmd "github.com/pgEdge/pgedge-cli/internal/controlplane/cmd"
	accountcmd "github.com/pgEdge/pgedge-cli/internal/starfleet/account/cmd"
	byoccmd "github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/cmd"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// TestExitCodeVocabulariesAgree is the gate on the one fact that four
// packages have to share and none of them owns: what each exit code
// means.
//
// They cannot share a declaration. internal/cli must not import the
// module packages — its `coder` interface exists precisely so it doesn't
// — and internal/cli imports internal/starfleet/conn, so conn cannot
// import cli back either. Four independent declarations is the shape the
// dependency graph forces, so the agreement between them needs a gate
// that lives above all four. That is this package.
//
// This is not hypothetical drift. Before Ant's 2026-07-31 ruling,
// internal/controlplane/cmd declared ExitUsage = 2 while conn declared
// ExitAuth = 2: the numbers matched and the meanings did not, so a
// script could not tell a malformed command from a rejected credential.
// Worse, it hid a real bug — conn.Resolve tagged a half-supplied
// --client-id/--client-secret pair as ExitAuth, which looked correct
// only because ExitAuth was also 2. Moving auth to 5 exposed it.
//
// The ruling: **2 means "the command was malformed", everywhere.** Auth
// failure is 5.
func TestExitCodeVocabulariesAgree(t *testing.T) {
	// Every name that must hold the same number in every package that
	// declares it. A package absent from a row does not declare that
	// concept — internal/cli has no notion of a missing resource.
	// internal/cli has a timeout through cli.ExitCode's deadline
	// branch; it is in the row, and leaving it out is how
	// a fifth vocabulary drifts unnoticed. cp sat out the auth row on
	// the belief it had no authentication, until the pinned spec's 401
	// (invalid_join_token, on cluster join) showed that belief wrong —
	// the same lesson, one row down.
	rows := []struct {
		name string
		want int
		got  map[string]int
	}{
		{"ok", 0, map[string]int{
			"cli":         cli.ExitOK,
			"conn":        conn.ExitOK,
			"cp/cmd":      cpcmd.ExitOK,
			"byoc/cmd":    byoccmd.ExitOK,
			"account/cmd": accountcmd.ExitOK,
		}},
		{"general failure", 1, map[string]int{
			"cli":         cli.ExitError,
			"conn":        conn.ExitGeneral,
			"cp/cmd":      cpcmd.ExitGeneral,
			"byoc/cmd":    byoccmd.ExitGeneral,
			"account/cmd": accountcmd.ExitGeneral,
		}},
		{"usage", 2, map[string]int{
			"cli":         cli.ExitUsage,
			"conn":        conn.ExitUsage,
			"cp/cmd":      cpcmd.ExitUsage,
			"byoc/cmd":    byoccmd.ExitUsage,
			"account/cmd": accountcmd.ExitUsage,
		}},
		{"timeout", 3, map[string]int{
			"cli":         cli.ExitTimeout,
			"conn":        conn.ExitTimeout,
			"cp/cmd":      cpcmd.ExitTimeout,
			"byoc/cmd":    byoccmd.ExitTimeout,
			"account/cmd": accountcmd.ExitTimeout,
		}},
		{"not found", 4, map[string]int{
			"conn":        conn.ExitNotFound,
			"cp/cmd":      cpcmd.ExitNotFound,
			"byoc/cmd":    byoccmd.ExitNotFound,
			"account/cmd": accountcmd.ExitNotFound,
		}},
		{"auth failure", 5, map[string]int{
			"conn":        conn.ExitAuth,
			"cp/cmd":      cpcmd.ExitAuth,
			"byoc/cmd":    byoccmd.ExitAuth,
			"account/cmd": accountcmd.ExitAuth,
		}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for pkg, got := range row.got {
				if got != row.want {
					t.Errorf("%s's %q code = %d, want %d",
						pkg, row.name, got, row.want)
				}
			}
		})
	}

	// No two meanings may share a number. The check above would pass if
	// two rows were both given the same `want` by mistake, which is the
	// exact defect this whole change removes — so assert distinctness
	// rather than trusting the table's author.
	seen := map[int]string{}
	for _, row := range rows {
		if prev, ok := seen[row.want]; ok {
			t.Errorf("exit code %d means both %q and %q; one number, "+
				"one meaning", row.want, prev, row.name)
		}
		seen[row.want] = row.name
	}
}
