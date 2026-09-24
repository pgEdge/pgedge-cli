package cli

import (
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// Both annotations take a short reason as their value. Nothing prints
// it; the pinning tests in internal/clitest read it, so neither set can
// grow silently.
const (
	// AnnotationProfileExempt turns the guard off for a command and its
	// subtree.
	AnnotationProfileExempt = "pgedge:profile-exempt"

	// AnnotationProfileRepair waives only the current_profile path, for
	// the commands an operator reaches for when current_profile names a
	// profile that does not exist. The full exemption would also stop
	// `--profile <typo>` being caught there: a name typed on this
	// invocation is a typo to catch, not a broken file to repair.
	AnnotationProfileRepair = "pgedge:profile-repair"
)

// exemptFromProfileGuard walks c and its ancestors, so annotating a
// group covers every command beneath it — "completion" exempts
// "completion install" without annotating each leaf.
func exemptFromProfileGuard(c *cobra.Command) (string, bool) {
	for cur := c; cur != nil; cur = cur.Parent() {
		if reason, ok := cur.Annotations[AnnotationProfileExempt]; ok {
			return reason, true
		}
	}
	return "", false
}

// repairsCurrentProfile reports whether c may run with an
// unresolvable current_profile, walking ancestors on the same
// subtree rule.
func repairsCurrentProfile(c *cobra.Command) (string, bool) {
	for cur := c; cur != nil; cur = cur.Parent() {
		if reason, ok := cur.Annotations[AnnotationProfileRepair]; ok {
			return reason, true
		}
	}
	return "", false
}

// CheckProfile validates whatever profile a command resolved to,
// regardless of what chose it. Rejection is the same error and exit code
// as `pgedge profile use <unknown>`; main.go's hook ordering protects
// that parity. There is no nil guard because root's PersistentPreRunE
// sets rt.Config before any run hook fires.
//
// It is not gated on rt.ProfileExplicit because a hand edit never goes
// through `profile use`: a current_profile naming nothing would
// otherwise send every command to the default production URL.
func CheckProfile(rt *module.Runtime) error {
	return CheckProfileName(rt.Config, rt.Profile)
}

// CheckProfileName is the one definition of a name usable to read
// under: the built-in "default", or a key of the config file. The guard,
// `profile show` and `profile list` all call it, so the flag, config and
// argument paths cannot drift apart. "default" passes with no section of
// that name because ResolveProfile falls back to it on a fresh install.
//
// `profile use` is deliberately not a caller: it goes through
// Config.SetCurrentProfile to Config.ValidateProfile without this
// exception, so `profile use default` fails where `--profile default`
// succeeds. `use` selects a configured profile, and writing
// `current_profile: default` would mean what deleting the line means.
// llms.txt documents the asymmetry; do not add a fourth caller.
func CheckProfileName(cfg *config.Config, name string) error {
	if name == "default" {
		return nil
	}
	return cfg.ValidateProfile(name)
}

// GuardProfile runs CheckProfile unless cmd or an ancestor is annotated
// out of it. main.go wraps every command's run hook with this; see
// wrapProfileGuard for why a wrap and not a PersistentPreRunE.
//
// The full exemption skips the guard; the repair carve-out skips only the
// current_profile path. Without it, `pgedge profile use <good>`, which
// rewrites a broken current_profile, would be refused for the very value
// it exists to replace.
func GuardProfile(cmd *cobra.Command, rt *module.Runtime) error {
	if _, ok := exemptFromProfileGuard(cmd); ok {
		return nil
	}
	if _, ok := repairsCurrentProfile(cmd); ok && !rt.ProfileExplicit {
		return nil
	}
	return CheckProfile(rt)
}
