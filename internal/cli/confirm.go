package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"golang.org/x/term"
)

// Confirm gates a destructive operation behind user confirmation.
//
//   - force bypasses the prompt and proceeds.
//   - In a non-interactive context (stdin is not a terminal)
//     without force, it returns a *UsageError so scripts and AI
//     agents fail loudly rather than hang on a prompt or silently
//     no-op.
//   - On a terminal it prompts; a "y"/"Y" answer proceeds, anything
//     else is treated as a rejection.
//
// It returns nil when the caller should proceed.
func Confirm(rt *module.Runtime, prompt string, force bool) error {
	return confirmWith(rt, prompt, force,
		term.IsTerminal(int(os.Stdin.Fd())))
}

// confirmWith is the testable core of Confirm: isTTY is passed in
// rather than detected, so tests can exercise both branches without
// a real terminal.
func confirmWith(
	rt *module.Runtime, prompt string, force, isTTY bool,
) error {
	// A dry run destroys nothing, so it asks no confirmation; otherwise
	// `delete --dry-run` would prompt on a terminal and fail off one. It
	// precedes the force check so the ledger records what the real run
	// would ask for, and sits in this shared helper to cover every
	// destructive verb in the tree.
	if rt.DryRun != nil {
		if !force {
			rt.DryRun.Pass(
				"would require confirmation (--force bypasses)")
		}
		return nil
	}

	if force {
		return nil
	}

	if !isTTY {
		return &UsageError{
			Msg: "this operation is destructive; run with " +
				"--force to confirm, or run interactively to " +
				"be prompted",
		}
	}

	fmt.Fprintf(rt.Stderr, "%s [y/N]: ", output.Sanitize(prompt))
	reader := bufio.NewReader(rt.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)
	if answer == "y" || answer == "Y" {
		return nil
	}

	return &UsageError{Msg: "aborted: not confirmed"}
}
