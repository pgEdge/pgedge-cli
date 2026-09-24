package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// MutatesAnnotation marks a command as an API-mutating leaf. The
// structural gates in internal/clitest read it to prove that every verb
// which writes is dry-runnable and that no read-only verb pretends to
// be.
const MutatesAnnotation = "pgedge:mutates"

// DryRunFlag is the flag name, in one place, because setupRuntime and
// internal/clitest's dry-run gates have to agree on it.
const DryRunFlag = "dry-run"

// dryRunChecks is the only accepted value, and the bare --dry-run form's
// meaning: run every client-side check, send no write. It is not spelled
// "client", as kubectl's is, because there no request leaves at all,
// while here reads happen: the create-vs-reconfigure intent guard cannot
// tell deploy from update without one.
const dryRunChecks = "checks"

// dryRunServer is recognised so it can be refused with a real reason: a
// kubectl or helm user will type it, and naming the missing platform
// capability says which checks run where better than "invalid value". It
// is reserved for when saas or Control Plane grows a validate endpoint.
const dryRunServer = "server"

// MarkMutating declares cmd an API-mutating leaf: it sets the annotation
// and registers --dry-run, so the two can never disagree. It is one call,
// not an annotation plus a tree walker in main, because clitest.FullTree()
// builds the tree without cmd/pgedge/main.go, and the gates would inspect
// a tree with no --dry-run on it.
//
// The flag is a string with NoOptDefVal, never a bool. A bool accepts
// --dry-run=true, the spelling kubectl had to break: it shipped a bool,
// found it ambiguous once client- and server-side dry runs both existed,
// and removed the boolean form in 1.23. Helm 3.13 instead added
// --dry-run=server as a new value while bare --dry-run kept working; a
// string flag leaves that path open.
func MarkMutating(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[MutatesAnnotation] = "true"

	cmd.Flags().String(DryRunFlag, "",
		"Run every client-side check, then stop before sending the "+
			"write and report the request that would have been sent. "+
			"Checks that read the API do run, so this needs "+
			"credentials; the server validates nothing until the "+
			"real write")
	cmd.Flags().Lookup(DryRunFlag).NoOptDefVal = dryRunChecks

	_ = cmd.RegisterFlagCompletionFunc(DryRunFlag,
		func(*cobra.Command, []string, string) (
			[]string, cobra.ShellCompDirective,
		) {
			return []string{dryRunChecks},
				cobra.ShellCompDirectiveNoFileComp
		})
}

// IsMutating reports whether cmd carries the annotation.
func IsMutating(cmd *cobra.Command) bool {
	return cmd.Annotations[MutatesAnnotation] == "true"
}

// parseDryRun turns the flag's value into "is dry-run on". Every
// rejection is a *UsageError, so a bad value exits 2 like any other
// malformed flag rather than looking like a runtime failure.
func parseDryRun(value string) (bool, error) {
	switch value {
	case dryRunChecks:
		return true, nil
	case dryRunServer:
		return false, &UsageError{Msg: fmt.Sprintf(
			"--%s=%s is not available: neither pgEdge Starfleet nor "+
				"Control Plane exposes a validate endpoint, so there "+
				"is nothing to ask. Use --%s for client-side checks.",
			DryRunFlag, dryRunServer, DryRunFlag)}
	default:
		return false, &UsageError{Msg: fmt.Sprintf(
			"invalid --%s value %q (want %q, or just --%s)",
			DryRunFlag, value, dryRunChecks, DryRunFlag)}
	}
}
