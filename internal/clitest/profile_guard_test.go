package clitest

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/spf13/cobra"
)

// exemptProfileGuard pins the commands annotated with
// cli.AnnotationProfileExempt — the six commands #102 (CLI-4) carves
// out of the profile guard, per Design decision 6 of the plan: four
// that never read the config (version, llms, completion, help) and
// two that create the named profile rather than requiring it to
// already exist (starfleet auth login, cp config set).
//
// TestProfileExemptionsArePinned is the two-way check, modelled on
// TestPureRouterExemptionsArePinned above: every annotated command
// must appear here with a non-empty reason, and every entry here must
// still be annotated. A new exemption then cannot be added silently —
// adding the annotation without updating this map fails the build,
// and so does leaving a stale entry after removing an annotation.
var exemptProfileGuard = map[string]string{
	"pgedge version":                 "never reads config",
	"pgedge llms":                    "never reads config",
	"pgedge completion":              "never reads config",
	"pgedge help":                    "never reads config",
	"pgedge starfleet auth login":    "creates the named profile",
	"pgedge controlplane config set": "creates the named profile",
}

// TestProfileExemptionsArePinned walks the full shipped tree and
// asserts exemptProfileGuard names exactly the set of commands
// carrying cli.AnnotationProfileExempt — no more, no less.
func TestProfileExemptionsArePinned(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var commands int
	annotated := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		commands++
		if _, ok := c.Annotations[cli.AnnotationProfileExempt]; ok {
			annotated[c.CommandPath()] = true
		}
	})

	for path := range annotated {
		reason, ok := exemptProfileGuard[path]
		if !ok {
			t.Errorf("%s carries %s but is not in exemptProfileGuard — "+
				"either drop the annotation or list it with a reason",
				path, cli.AnnotationProfileExempt)
			continue
		}
		if reason == "" {
			t.Errorf("%s: exemption must carry a reason", path)
		}
	}
	for path, reason := range exemptProfileGuard {
		if !annotated[path] {
			t.Errorf("%s is listed in exemptProfileGuard but is no "+
				"longer annotated — delete the entry", path)
		}
		if reason == "" {
			t.Errorf("%s: exemptProfileGuard entry must carry a reason",
				path)
		}
	}

	// Positive controls. Without these a walk that matched nothing, or
	// an annotation check that never fired, would pass both loops
	// above vacuously.
	if commands < 20 {
		t.Errorf("found only %d commands; expected 20+ — has the walk "+
			"stopped finding them?", commands)
	}
	if len(annotated) != len(exemptProfileGuard) {
		t.Errorf("found %d annotated commands, want exactly %d",
			len(annotated), len(exemptProfileGuard))
	}
}

// repairCurrentProfile pins the commands annotated with
// cli.AnnotationProfileRepair — the two that must still run when
// current_profile names a profile that does not exist (#150).
//
// The set is deliberately tiny and must stay that way. Every command
// listed here is one that keeps running against a config the CLI has
// just declared broken, so each entry has to earn it by being part of
// the way out: `profile list` names the profiles that do exist, and
// `profile use` writes one of them back to current_profile. Anything
// else belongs behind the rejection.
var repairCurrentProfile = map[string]string{
	"pgedge profile list": "reports the configured profiles",
	"pgedge profile use":  "rewrites current_profile",
}

// TestProfileRepairAnnotationsArePinned is the two-way check for
// cli.AnnotationProfileRepair, mirroring TestProfileExemptionsArePinned
// above: every annotated command must appear here with a non-empty
// reason, and every entry here must still be annotated.
func TestProfileRepairAnnotationsArePinned(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var commands int
	annotated := map[string]bool{}
	walk(root, func(c *cobra.Command) {
		commands++
		if _, ok := c.Annotations[cli.AnnotationProfileRepair]; ok {
			annotated[c.CommandPath()] = true
		}
	})

	for path := range annotated {
		reason, ok := repairCurrentProfile[path]
		if !ok {
			t.Errorf("%s carries %s but is not in repairCurrentProfile "+
				"— either drop the annotation or list it with a reason",
				path, cli.AnnotationProfileRepair)
			continue
		}
		if reason == "" {
			t.Errorf("%s: repair annotation must carry a reason", path)
		}
	}
	for path, reason := range repairCurrentProfile {
		if !annotated[path] {
			t.Errorf("%s is listed in repairCurrentProfile but is no "+
				"longer annotated — delete the entry", path)
		}
		if reason == "" {
			t.Errorf("%s: repairCurrentProfile entry must carry a reason",
				path)
		}
	}

	// Positive controls, for the same reason the exemption gate above
	// carries them: a walk that matched nothing would satisfy both
	// loops vacuously.
	if commands < 20 {
		t.Errorf("found only %d commands; expected 20+ — has the walk "+
			"stopped finding them?", commands)
	}
	if len(annotated) != len(repairCurrentProfile) {
		t.Errorf("found %d annotated commands, want exactly %d",
			len(annotated), len(repairCurrentProfile))
	}
}

// TestNoCommandUsesBareRun forbids cobra's non-error Run hook across
// the shipped tree.
//
// This is a gate rather than a fix because the consequence is silent.
// main.go wraps every run hook twice — wrapProfileGuard then markRan —
// and both wrappers have a bare-Run arm that can only print an error,
// never return one, because Run has no error to return. A command
// written with Run would therefore print "Error: unknown profile ..."
// and still exit 0, reporting success for a command the guard just
// refused to let run. Forbidding Run outright is the only check that
// stays true as the tree grows; the alternative is remembering to
// re-derive the consequence at every new command.
//
// Nothing in the tree uses Run today, so this gate should never fire.
// That is the point: it fires the moment someone reaches for it.
func TestNoCommandUsesBareRun(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	var commands, withRunE int
	walk(root, func(c *cobra.Command) {
		commands++
		if c.RunE != nil {
			withRunE++
		}
		if c.Run != nil {
			t.Errorf("%s sets Run; use RunE instead — a bare Run "+
				"cannot return the profile guard's error, so the "+
				"command would print it and still exit 0",
				c.CommandPath())
		}
	})

	// Positive controls: prove the walk both ran and could see a run
	// hook at all. Without the second, a walk that read the wrong
	// struct field would pass by never finding anything to complain
	// about.
	if commands < 20 {
		t.Errorf("found only %d commands; expected 20+ — has the walk "+
			"stopped finding them?", commands)
	}
	if withRunE < 20 {
		t.Errorf("found only %d commands with RunE; expected 20+ — "+
			"the walk cannot see run hooks", withRunE)
	}
}
