package cli

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// newTestRuntime builds a *module.Runtime with the given profiles
// configured, for CheckProfile/GuardProfile tests. It never touches
// disk: config.Config's map is populated directly, exactly the way
// internal/config's own tests do it.
func newTestRuntime(profile string, explicit bool, profiles ...string) *module.Runtime {
	cfg := &config.Config{}
	for _, p := range profiles {
		cfg.SetStarfleetProfile(p, &config.StarfleetProfile{})
	}
	return &module.Runtime{
		Config:          cfg,
		Profile:         profile,
		ProfileExplicit: explicit,
	}
}

// TestProfileGuardCheckProfile is the table test for CheckProfile: a
// configured name and the literal "default" are always nil, and an
// unknown name returns exactly what Config.ValidateProfile returns for
// the same input — not merely an equal-looking string — whether or not
// --profile was the thing that named it.
func TestProfileGuardCheckProfile(t *testing.T) {
	// #150: this case used to assert nil. An unknown name reaching
	// CheckProfile without --profile means current_profile was
	// hand-edited to something that does not exist, and returning nil
	// is what let every command silently dial the default production
	// URL under a profile that was never configured.
	t.Run("not explicit and unknown: still rejected", func(t *testing.T) {
		rt := newTestRuntime("bogus", false, "alpha")
		got := CheckProfile(rt)
		want := rt.Config.ValidateProfile("bogus")
		if got == nil || want == nil {
			t.Fatalf("got=%v want=%v, both must be non-nil", got, want)
		}
		if got.Error() != want.Error() {
			t.Errorf("CheckProfile() = %q, want %q",
				got.Error(), want.Error())
		}
	})

	// The flag path and the config path must not merely both fail —
	// they must fail with byte-identical text, which is the whole
	// reason both route through Config.ValidateProfile.
	t.Run("flag path and config path read identically", func(t *testing.T) {
		fromFlag := CheckProfile(newTestRuntime("bogus", true, "alpha"))
		fromConfig := CheckProfile(newTestRuntime("bogus", false, "alpha"))
		if fromFlag == nil || fromConfig == nil {
			t.Fatalf("flag=%v config=%v, both must be non-nil",
				fromFlag, fromConfig)
		}
		if fromFlag.Error() != fromConfig.Error() {
			t.Errorf("flag path = %q, config path = %q; the two must "+
				"be byte-identical", fromFlag.Error(), fromConfig.Error())
		}
	})

	t.Run("not explicit and configured: nil", func(t *testing.T) {
		rt := newTestRuntime("alpha", false, "alpha")
		if err := CheckProfile(rt); err != nil {
			t.Errorf("CheckProfile() = %v, want nil", err)
		}
	})

	t.Run("not explicit default: nil", func(t *testing.T) {
		rt := newTestRuntime("default", false, "alpha")
		if err := CheckProfile(rt); err != nil {
			t.Errorf("CheckProfile() = %v, want nil", err)
		}
	})

	t.Run("explicit and configured: nil", func(t *testing.T) {
		rt := newTestRuntime("alpha", true, "alpha", "prod")
		if err := CheckProfile(rt); err != nil {
			t.Errorf("CheckProfile() = %v, want nil", err)
		}
	})

	t.Run("explicit default, absent from non-empty config: nil", func(t *testing.T) {
		rt := newTestRuntime("default", true, "alpha")
		if err := CheckProfile(rt); err != nil {
			t.Errorf("CheckProfile() = %v, want nil", err)
		}
	})

	t.Run("explicit default with zero profiles: nil", func(t *testing.T) {
		rt := newTestRuntime("default", true)
		if err := CheckProfile(rt); err != nil {
			t.Errorf("CheckProfile() = %v, want nil", err)
		}
	})

	t.Run("explicit and unknown: ValidateProfile's exact error", func(t *testing.T) {
		rt := newTestRuntime("bogus", true, "alpha")
		got := CheckProfile(rt)
		want := rt.Config.ValidateProfile("bogus")
		if got == nil || want == nil {
			t.Fatalf("got=%v want=%v, both must be non-nil", got, want)
		}
		if got.Error() != want.Error() {
			t.Errorf("CheckProfile() = %q, want %q",
				got.Error(), want.Error())
		}
	})

	t.Run("explicit and unknown with zero profiles configured", func(t *testing.T) {
		rt := newTestRuntime("bogus", true)
		got := CheckProfile(rt)
		want := rt.Config.ValidateProfile("bogus")
		if got == nil || want == nil {
			t.Fatalf("got=%v want=%v, both must be non-nil", got, want)
		}
		if got.Error() != want.Error() {
			t.Errorf("CheckProfile() = %q, want %q",
				got.Error(), want.Error())
		}
	})
}

// TestExemptFromProfileGuard pins the subtree-inheritance shape: a
// command with the annotation is exempt, a plain child of an
// unannotated command is not, and a plain child of an ANNOTATED
// command inherits the exemption without carrying the annotation
// itself.
func TestExemptFromProfileGuard(t *testing.T) {
	annotated := &cobra.Command{
		Use: "annotated",
		Annotations: map[string]string{
			AnnotationProfileExempt: "test reason",
		},
	}
	child := &cobra.Command{Use: "child"}
	annotated.AddCommand(child)

	plain := &cobra.Command{Use: "plain"}
	plainChild := &cobra.Command{Use: "plain-child"}
	plain.AddCommand(plainChild)

	if reason, ok := exemptFromProfileGuard(annotated); !ok || reason == "" {
		t.Errorf("annotated command: exempt=%v reason=%q, want true and non-empty",
			ok, reason)
	}
	if reason, ok := exemptFromProfileGuard(child); !ok || reason == "" {
		t.Errorf("child of annotated command: exempt=%v reason=%q, "+
			"want true and non-empty (subtree inheritance)", ok, reason)
	}
	if _, ok := exemptFromProfileGuard(plain); ok {
		t.Error("unannotated command reported exempt")
	}
	if _, ok := exemptFromProfileGuard(plainChild); ok {
		t.Error("child of an unannotated command reported exempt")
	}
}

// TestGuardProfile exercises the short-circuit: an exempt command
// never reaches CheckProfile, so an unknown explicit profile passes
// clean on an exempt command and fails on a non-exempt one.
func TestGuardProfile(t *testing.T) {
	exempt := &cobra.Command{
		Use: "exempt",
		Annotations: map[string]string{
			AnnotationProfileExempt: "never reads config",
		},
	}
	notExempt := &cobra.Command{Use: "not-exempt"}

	rt := newTestRuntime("bogus", true, "alpha")

	if err := GuardProfile(exempt, rt); err != nil {
		t.Errorf("GuardProfile(exempt) = %v, want nil", err)
	}
	if err := GuardProfile(notExempt, rt); err == nil {
		t.Error("GuardProfile(not-exempt) = nil, want an error")
	}
}

// TestGuardProfileRepairCarveOut pins the difference between the two
// annotations, which is the only thing that keeps #150's fix from
// undoing #147's on the annotated commands: a repair command runs
// under an unresolvable current_profile, but an explicit --profile
// naming the same unknown value is still rejected there. A full
// exemption would swallow both.
func TestGuardProfileRepairCarveOut(t *testing.T) {
	repair := &cobra.Command{
		Use: "repair",
		Annotations: map[string]string{
			AnnotationProfileRepair: "rewrites current_profile",
		},
	}
	child := &cobra.Command{Use: "child"}
	repair.AddCommand(child)
	plain := &cobra.Command{Use: "plain"}

	t.Run("config path: repair command runs", func(t *testing.T) {
		rt := newTestRuntime("ghost", false, "alpha")
		if err := GuardProfile(repair, rt); err != nil {
			t.Errorf("GuardProfile(repair) = %v, want nil — a broken "+
				"current_profile must stay repairable", err)
		}
	})

	t.Run("config path: subtree inherits", func(t *testing.T) {
		rt := newTestRuntime("ghost", false, "alpha")
		if err := GuardProfile(child, rt); err != nil {
			t.Errorf("GuardProfile(child of repair) = %v, want nil", err)
		}
	})

	t.Run("config path: plain command still rejected", func(t *testing.T) {
		rt := newTestRuntime("ghost", false, "alpha")
		if err := GuardProfile(plain, rt); err == nil {
			t.Error("GuardProfile(plain) = nil, want an error")
		}
	})

	// The carve-out is scoped to the config path only. Without this
	// case, widening it to a full exemption would pass every other
	// assertion in this file.
	t.Run("flag path: repair command still rejected", func(t *testing.T) {
		rt := newTestRuntime("ghost", true, "alpha")
		got := GuardProfile(repair, rt)
		if got == nil {
			t.Fatal("GuardProfile(repair, --profile ghost) = nil; an " +
				"explicitly typed unknown profile is a typo to catch, " +
				"not a config file to repair")
		}
		want := rt.Config.ValidateProfile("ghost")
		if got.Error() != want.Error() {
			t.Errorf("GuardProfile() = %q, want %q",
				got.Error(), want.Error())
		}
	})

	// A repair command under a healthy config is unremarkable, but if
	// this ever failed the carve-out would be inverting rather than
	// waiving the check.
	t.Run("repair command with a good profile: nil", func(t *testing.T) {
		rt := newTestRuntime("alpha", false, "alpha")
		if err := GuardProfile(repair, rt); err != nil {
			t.Errorf("GuardProfile(repair, good profile) = %v, want nil",
				err)
		}
	})
}

// TestCheckProfileName pins the shared policy the guard, `profile
// show`'s argument and `profile list`'s Resolved column all read, so
// the three cannot drift.
func TestCheckProfileName(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetStarfleetProfile("alpha", &config.StarfleetProfile{})

	if err := CheckProfileName(cfg, "alpha"); err != nil {
		t.Errorf("CheckProfileName(alpha) = %v, want nil", err)
	}
	// "default" is the built-in name ResolveProfile itself falls back
	// to, so it passes even with no section of that name — the one
	// documented way a default-shaped profile still materializes.
	if err := CheckProfileName(cfg, "default"); err != nil {
		t.Errorf("CheckProfileName(default) = %v, want nil — the "+
			"built-in name must pass without a matching section", err)
	}
	if err := CheckProfileName(&config.Config{}, "default"); err != nil {
		t.Errorf("CheckProfileName(default, empty config) = %v, want nil",
			err)
	}
	got := CheckProfileName(cfg, "ghost")
	want := cfg.ValidateProfile("ghost")
	if got == nil || want == nil {
		t.Fatalf("got=%v want=%v, both must be non-nil", got, want)
	}
	if got.Error() != want.Error() {
		t.Errorf("CheckProfileName(ghost) = %q, want %q",
			got.Error(), want.Error())
	}
}

// TestProfileUseDoesNotAcceptTheBuiltInDefault pins the one deliberate
// asymmetry between the read paths and the write path. The read paths
// go through CheckProfileName, which accepts "default" without a
// matching section; `profile use` goes through Config.ValidateProfile
// directly and does not.
//
// It exists because the asymmetry reads as an oversight — three
// callers share an exception and a fourth does not — and the obvious
// tidy-up is to route `use` through CheckProfileName too. That would
// make the command write `current_profile: default`, which means
// exactly what removing the line means, so it would be a write whose
// value is "unset". Decided against on 2026-08-08; if it is ever
// revisited, the honest shape is DELETING the key, not writing this
// name into it.
func TestProfileUseDoesNotAcceptTheBuiltInDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetStarfleetProfile("alpha", &config.StarfleetProfile{})
	if _, ok := cfg.Profiles["default"]; ok {
		t.Fatal("fixture has a default section; this test needs one absent")
	}

	// The read side accepts it...
	if err := CheckProfileName(cfg, "default"); err != nil {
		t.Errorf("CheckProfileName(default) = %v, want nil", err)
	}
	// ...and the write side does not.
	if err := cfg.SetCurrentProfile("default"); err == nil {
		t.Error("SetCurrentProfile(default) = nil, want an error — " +
			"`use` selects a configured profile, and the built-in is " +
			"the absence of one")
	}
	// Nothing was written on the way to that error.
	if cfg.CurrentProfile != "" {
		t.Errorf("CurrentProfile = %q after a rejected use, want empty",
			cfg.CurrentProfile)
	}
}
