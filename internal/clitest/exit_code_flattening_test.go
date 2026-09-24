package clitest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// flatteningRe matches an error rebuilt from another error's message
// and assigned the GENERAL exit code.
//
// That shape is the one that loses information. `newExitError(
// err.Error(), ExitUsage)` is an UPGRADE — it takes a plain error that
// would have exited 1 and gives it the code the contract wants — and
// `internal/starfleet/account/cmd/auth.go` does exactly that on purpose.
// Flattening TO ExitGeneral is the opposite: any error already
// carrying a code arrives at 1 instead, and the caller cannot tell.
var flatteningRe = regexp.MustCompile(
	`newExitError\(\s*err\.Error\(\),\s*ExitGeneral\s*\)`)

// TestNoErrorIsFlattenedToTheGeneralCode closes the class
// rather than the one site that showed it.
//
// `byoc cluster create` special-cased its own *ExitError and flattened
// everything else, so a *cli.UsageError passing through would have
// exited 1 — silently undoing the exit-2 guarantee the moment a parse
// moved inside buildClusterCreateBody. There was no live defect; the
// point is that a reasonable future change lands exactly there, and
// neither existing gate covers it (TestMalformedUUIDArgumentIsAUsageError
// enumerates commands and byoc cluster create has no UUID argument;
// TestPrefixResolversKeepTheirDiscardedParse reads the resolvers only).
//
// WHAT THIS DOES NOT READ, stated here rather than left to be
// rediscovered: only the literal `newExitError(err.Error(),
// ExitGeneral)` form. An equivalent written as
// `newExitError(fmt.Sprintf("%v", err), ExitGeneral)`, or through a
// local variable, or with the module's other constructors, would pass.
// The right instinct is the rule, not the regex: an error that
// already carries a code is returned unchanged.
func TestNoErrorIsFlattenedToTheGeneralCode(t *testing.T) {
	roots := []string{
		filepath.Join("..", "starfleet"),
		filepath.Join("..", "controlplane"),
		filepath.Join("..", "cli"),
	}
	scanned := 0
	for _, root := range roots {
		err := filepath.Walk(root,
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					// Generated clients are not ours to police.
					if info.Name() == "api" {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}
				raw, rerr := os.ReadFile(path)
				if rerr != nil {
					return rerr
				}
				scanned++
				for _, m := range flatteningRe.FindAllString(
					string(raw), -1) {
					t.Errorf("%s: %s rebuilds an error and assigns it "+
						"the general code, so a *cli.UsageError "+
						"passing through would exit 1 instead of 2. "+
						"Return the error unchanged: "+
						"cli.ExitCode already maps UsageError before "+
						"consulting the coder interface.", path, m)
				}
				return nil
			})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	// A walk that found nothing has lost its truth source and would
	// pass against anything.
	if scanned < 50 {
		t.Fatalf("scanned only %d files; the walk is not reaching the "+
			"module trees", scanned)
	}
	t.Logf("scanned %d files", scanned)
}
