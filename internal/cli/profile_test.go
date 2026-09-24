package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// profileRuntime returns an env-isolated Runtime with two configured
// profiles, "dev" (active) and "prod", each with a distinct Starfleet API
// URL and client ID, and "dev" additionally carrying a secret and a
// cp connection. testsupport.NewRuntime supplies the HOME isolation
// that matters enormously here, since config.Load resolves the
// config path from --config or else $HOME/.pgedge/cli/config.yaml — no
// environment variable steers it.
func profileRuntime(t *testing.T, format string) (
	rt *module.Runtime, stdout, stderr *bytes.Buffer,
) {
	t.Helper()
	rt, out, errOut := testsupport.NewRuntime(t, "", format)
	rt.Profile = "dev"
	rt.Config.SetStarfleetProfile("dev", &config.StarfleetProfile{
		APIURL:       "https://staging.example",
		ClientID:     "dev-client-id",
		ClientSecret: "super-secret-value",
	})
	rt.Config.SetStarfleetProfile("prod", &config.StarfleetProfile{
		APIURL:   "https://api.pgedge.com",
		ClientID: "prod-client-id",
	})
	rt.Config.SetControlplaneProfile("dev", &config.ControlplaneProfile{
		BaseURL: "https://cp.example",
	})
	return rt, out, errOut
}

// --- list ---------------------------------------------------------------

func TestProfileListMarksActiveProfile(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"NAME", "ACTIVE", "STARFLEET URL", "CONTROLPLANE URL",
		"dev", "prod",
		"https://staging.example", "https://api.pgedge.com",
		"https://cp.example",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("profile list missing %q:\n%s", want, s)
		}
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var devLine, prodLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "dev") {
			devLine = l
		}
		if strings.HasPrefix(l, "prod") {
			prodLine = l
		}
	}
	if !strings.Contains(devLine, "yes") {
		t.Errorf("dev row should be marked active:\n%s", devLine)
	}
	if !strings.Contains(prodLine, "no") {
		t.Errorf("prod row should not be marked active:\n%s", prodLine)
	}
}

func TestProfileListJSON(t *testing.T) {
	rt, out, _ := profileRuntime(t, "json")
	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"name":"dev"`, `"active":true`,
		`"name":"prod"`, `"active":false`,
		"starfleet_url", "controlplane_url",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("profile list json missing %q:\n%s", want, got)
		}
	}
}

// TestProfileListIncludesUnconfiguredActiveProfile covers the
// fresh-install case: the active profile resolves to "default" but
// was never explicitly configured, and it must still appear with the
// module-wide default Starfleet API URL — the same behaviour
// `pgedge doctor` uses for its always-present cloud connection row.
func TestProfileListIncludesUnconfiguredActiveProfile(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "default") {
		t.Errorf("expected the unconfigured active profile to be "+
			"listed:\n%s", s)
	}
	if !strings.Contains(s, "https://api.pgedge.com") {
		t.Errorf("expected the default Starfleet API URL:\n%s", s)
	}
}

// TestProfileListPhantomRowIsTheGuardsJobNotThisFunctions pins a
// design decision: runProfileList itself is deliberately
// UNCHANGED by the --profile guard. GuardProfile (profileguard.go),
// wired only in cmd/pgedge/main.go, is what stops an unknown explicit
// --profile from ever reaching this function in the real CLI —
// TestUnknownProfileFlag (cmd/pgedge/main_test.go) is the end-to-end
// proof of that. Called directly, as every unit test here does, this
// function still synthesises a row for whatever rt.Profile it is
// given, known or not.
//
// The synthesis path fires for more than 'default': `profile list`
// carries AnnotationProfileRepair, so it is one of the two commands
// that DOES run under an unresolvable current_profile, and the
// synthesised row is what an operator sees while diagnosing exactly
// that — see TestProfileListUnresolvedActiveProfile for what it must
// say. The division of labour is unchanged and is still the point:
// policy lives in the guard, rendering lives here, and this function
// reports the state it is handed rather than refusing it.
func TestProfileListPhantomRowIsTheGuardsJobNotThisFunctions(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rt.Profile = "never-configured"
	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "never-configured") {
		t.Errorf("expected runProfileList to render whatever rt.Profile "+
			"names, unguarded:\n%s", s)
	}
}

func TestProfileListNeverPrintsSecret(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := profileRuntime(t, format)
			if err := runProfileList(rt); err != nil {
				t.Fatalf("profile list: %v", err)
			}
			if strings.Contains(out.String(), "super-secret-value") {
				t.Errorf("profile list leaked the secret value:\n%s",
					out.String())
			}
		})
	}
}

// --- show -----------------------------------------------------------------

func TestProfileShowDefaultsToActiveProfile(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if err := runProfileShow(rt, rt.Profile); err != nil {
		t.Fatalf("profile show: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"NAME", "dev", "ACTIVE", "yes",
		"STARFLEET URL", "https://staging.example",
		"STARFLEET CLIENT ID", "dev-client-id",
		"STARFLEET SECRET", "set",
		"CONTROLPLANE URL", "https://cp.example",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("profile show missing %q:\n%s", want, s)
		}
	}
}

func TestProfileShowNamedProfileNotActive(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if err := runProfileShow(rt, "prod"); err != nil {
		t.Fatalf("profile show: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "prod-client-id") {
		t.Errorf("profile show missing prod's client ID:\n%s", s)
	}
	if !strings.Contains(s, "not set") {
		t.Errorf("prod's secret should report not set:\n%s", s)
	}
	// The "yes"/"no" active marker sits on its own FIELD/VALUE row —
	// assert the ACTIVE row specifically reports "no", not merely
	// that "no" appears anywhere in the table.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "ACTIVE") && !strings.Contains(line, "no") {
			t.Errorf("ACTIVE row for a non-active profile should say "+
				"no:\n%s", line)
		}
	}
}

func TestProfileShowUnconfiguredProfile(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if err := runProfileShow(rt, "ghost"); err != nil {
		t.Fatalf("profile show: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"https://api.pgedge.com", // module default, not dev's or prod's
		"(not set)",              // no client ID configured
		"not set",                // no secret configured
		"(not configured)",       // no cp section
	} {
		if !strings.Contains(s, want) {
			t.Errorf("profile show ghost missing %q:\n%s", want, s)
		}
	}
}

// TestProfileShowNeverPrintsSecretValue is the mutation-critical
// assertion for the task's binding secret-safety requirement: the
// client ID (an identifier) is printed, but the secret's actual value
// must never appear in either output format. Making runProfileShow
// print report.StarfleetHasSecret's underlying value instead of its
// presence flag must fail this test.
func TestProfileShowNeverPrintsSecretValue(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := profileRuntime(t, format)
			if err := runProfileShow(rt, "dev"); err != nil {
				t.Fatalf("profile show: %v", err)
			}
			got := out.String()
			if strings.Contains(got, "super-secret-value") {
				t.Errorf("profile show leaked the secret value in %s "+
					"output:\n%s", format, got)
			}
			// The client ID, in contrast, is expected to appear: it is
			// an identifier, not a credential, and hiding it would make
			// this command useless for confirming which credentials a
			// profile is about to use.
			if !strings.Contains(got, "dev-client-id") {
				t.Errorf("profile show should print the client ID in "+
					"%s output:\n%s", format, got)
			}
		})
	}
}

func TestProfileShowJSONShape(t *testing.T) {
	rt, out, _ := profileRuntime(t, "json")
	if err := runProfileShow(rt, "dev"); err != nil {
		t.Fatalf("profile show: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"name":"dev"`, `"active":true`,
		`"starfleet_url":"https://staging.example"`,
		`"starfleet_client_id":"dev-client-id"`,
		`"starfleet_has_secret":true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("profile show json missing %q:\n%s", want, got)
		}
	}
}

// TestProfileShowYAMLUsesTheSameKeysAsJSON pins what -o yaml actually
// emits, because the reference used to claim otherwise. output.Print
// routes every yaml render through jsonShaped, so the json tags — not
// the lowercased Go field names yaml.v3 would fall back to on a
// tagless struct — are the keys a user sees. Without this, a rename of
// these fields can restore the old, wrong `cloudurl` wording in
// llms.txt with nothing to contradict it.
func TestProfileShowYAMLUsesTheSameKeysAsJSON(t *testing.T) {
	rt, out, _ := profileRuntime(t, "yaml")
	if err := runProfileShow(rt, "dev"); err != nil {
		t.Fatalf("profile show: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"starfleet_url:", "starfleet_client_id:", "starfleet_has_secret:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("profile show yaml missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"cloudurl", "cloudclientid", "cloudhassecret",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("profile show yaml emitted the lowercased Go field "+
				"name %q — the reference documents the json keys:\n%s",
				unwanted, got)
		}
	}
}

// --- use --------------------------------------------------------------------

func TestProfileUseSwitchesAndPersists(t *testing.T) {
	rt, _, errOut := profileRuntime(t, "text")
	if err := runProfileUse(rt, "prod"); err != nil {
		t.Fatalf("profile use: %v", err)
	}
	if rt.Config.CurrentProfile != "prod" {
		t.Errorf("CurrentProfile = %q, want prod", rt.Config.CurrentProfile)
	}
	if !strings.Contains(errOut.String(), "prod") {
		t.Errorf("expected a confirmation naming prod on stderr:\n%s",
			errOut.String())
	}

	// Reload from disk (same isolated HOME) to prove Save actually
	// wrote the change, not only mutated the in-memory Config.
	reloaded, err := config.Load("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.ResolveProfile(""); got != "prod" {
		t.Errorf("ResolveProfile() after reload = %q, want prod", got)
	}
}

func TestProfileUseJSONProducesBody(t *testing.T) {
	rt, out, _ := profileRuntime(t, "json")
	if err := runProfileUse(rt, "prod"); err != nil {
		t.Fatalf("profile use: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"name":"prod"`) ||
		!strings.Contains(got, `"active":true`) {
		t.Errorf("profile use -o json produced no coherent body:\n%s", got)
	}
}

func TestProfileUseTextProducesNoStdoutBody(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if err := runProfileUse(rt, "prod"); err != nil {
		t.Fatalf("profile use: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected no stdout body in text mode, got:\n%s",
			out.String())
	}
}

// TestProfileUseUnknownIsRuntimeError pins the task's deliberate exit
// code decision: switching to an unconfigured profile is a runtime
// (not usage) error, so it must map to cli.ExitError (1), not
// cli.ExitUsage (2). It must also name the profiles that do exist and
// must not touch CurrentProfile or disk.
func TestProfileUseUnknownIsRuntimeError(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	err := runProfileUse(rt, "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown profile")
	}
	if code := ExitCode(err); code != ExitError {
		t.Errorf("ExitCode(%v) = %d, want %d (ExitError)", err, code, ExitError)
	}
	if !strings.Contains(err.Error(), "dev") ||
		!strings.Contains(err.Error(), "prod") {
		t.Errorf("error %v should list the known profiles", err)
	}
	if rt.Config.CurrentProfile != "" {
		t.Errorf("CurrentProfile = %q, want unchanged on error",
			rt.Config.CurrentProfile)
	}
}

func TestProfileUseNeverLogsSecret(t *testing.T) {
	rt, out, errOut := profileRuntime(t, "json")
	if err := runProfileUse(rt, "prod"); err != nil {
		t.Fatalf("profile use: %v", err)
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, "super-secret-value") {
		t.Errorf("profile use leaked the secret value:\n%s", combined)
	}
}

// --- command-tree wiring ----------------------------------------------------

func TestProfileCmdIsRegisteredOnRoot(t *testing.T) {
	root := NewRootCmd(nil)
	for _, c := range root.Commands() {
		if c.Name() == "profile" {
			return
		}
	}
	t.Error("pgedge profile is not registered on the root command")
}

func TestProfileCmdRejectsStrayArgument(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"stray"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected a usage error for a stray argument")
	}
}

func TestProfileCmdWithNoArgsPrintsHelp(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute profile: %v", err)
	}
	if !strings.Contains(out.String(), "profile") {
		t.Errorf("expected help output mentioning profile:\n%s", out.String())
	}
}

func TestProfileUseCmdRequiresExactlyOneArg(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")

	zero := NewProfileCmd(rt)
	var zeroOut bytes.Buffer
	zero.SetOut(&zeroOut)
	zero.SetErr(&zeroOut)
	zero.SetArgs([]string{"use"})
	if err := zero.Execute(); err == nil {
		t.Error("expected a usage error for 'profile use' with no argument")
	}

	two := NewProfileCmd(rt)
	var twoOut bytes.Buffer
	two.SetOut(&twoOut)
	two.SetErr(&twoOut)
	two.SetArgs([]string{"use", "dev", "prod"})
	if err := two.Execute(); err == nil {
		t.Error("expected a usage error for 'profile use' with two arguments")
	}
}

func TestProfileShowCmdAcceptsAtMostOneArg(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"show", "dev", "prod"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected a usage error for 'profile show' with two arguments")
	}
}

func TestProfileUseCmdRunsFromTree(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"use", "prod"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute profile use: %v", err)
	}
	if rt.Config.CurrentProfile != "prod" {
		t.Errorf("CurrentProfile = %q, want prod", rt.Config.CurrentProfile)
	}
}

// TestProfileListCmdRunsFromTree exercises newProfileListCmd's own
// RunE closure through a real cobra.Execute(), not just runProfileList
// directly — the two are not the same code path.
func TestProfileListCmdRunsFromTree(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute profile list: %v", err)
	}
}

// TestProfileShowCmdRunsFromTree exercises newProfileShowCmd's RunE
// closure through a real cobra.Execute() with both zero and one
// argument, covering the name-defaulting branch that calling
// runProfileShow directly skips.
func TestProfileShowCmdRunsFromTree(t *testing.T) {
	// runProfileShow writes through rt.Output (bound to out below),
	// not through cobra's own SetOut buffer, so out — not a
	// cmd.SetOut buffer — is what must be asserted on.
	rt, out, _ := profileRuntime(t, "text")

	noArg := NewProfileCmd(rt)
	noArg.SetOut(&bytes.Buffer{})
	noArg.SetErr(&bytes.Buffer{})
	noArg.SetArgs([]string{"show"})
	if err := noArg.Execute(); err != nil {
		t.Fatalf("execute profile show: %v", err)
	}
	if !strings.Contains(out.String(), "dev-client-id") {
		t.Errorf("expected dev's client ID (active profile):\n%s",
			out.String())
	}
	out.Reset()

	withArg := NewProfileCmd(rt)
	withArg.SetOut(&bytes.Buffer{})
	withArg.SetErr(&bytes.Buffer{})
	withArg.SetArgs([]string{"show", "prod"})
	if err := withArg.Execute(); err != nil {
		t.Fatalf("execute profile show prod: %v", err)
	}
	if !strings.Contains(out.String(), "prod-client-id") {
		t.Errorf("expected prod's client ID:\n%s", out.String())
	}
}

// TestProfileUseSaveError drives runProfileUse's Save error branch:
// the profile switch succeeds in memory, but persisting it fails
// because the config directory cannot be created. Must not touch
// CurrentProfile's already-updated in-memory value or panic.
func TestProfileUseSaveError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	rt, _, _ := profileRuntime(t, "text")
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	// SaveTo both writes and remembers this unwritable path, so the
	// next Save (inside runProfileUse) fails deterministically.
	if err := rt.Config.SaveTo(
		filepath.Join(parent, "sub", "config.yaml")); err == nil {
		t.Fatal("expected SaveTo itself to fail against an unwritable parent")
	}

	err := runProfileUse(rt, "prod")
	if err == nil {
		t.Fatal("expected an error when Save cannot write the config")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Errorf("save error leaked the secret value: %v", err)
	}
}

func TestProfileUseCmdUnknownProfileExitCode(t *testing.T) {
	rt, _, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"use", "nope"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for 'profile use nope'")
	}
	if code := ExitCode(err); code != ExitError {
		t.Errorf("ExitCode(%v) = %d, want %d (ExitError)", err, code, ExitError)
	}
}

// --- the argument path ---------------------------------------------------

// TestProfileShowUnknownNameRejected: `profile show <unknown>`
// used to fabricate a complete, default-shaped report naming the
// production Starfleet URL — which reads as "this profile exists and
// points at prod" rather than "there is no such profile". It must now
// fail with exactly what the other two paths produce, and print no
// report at all.
func TestProfileShowUnknownNameRejected(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	cmd := NewProfileCmd(rt)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"show", "ghost"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for 'profile show ghost'")
	}
	want := rt.Config.ValidateProfile("ghost")
	if err.Error() != want.Error() {
		t.Errorf("profile show ghost = %q, want %q — the argument path "+
			"must read identically to --profile and current_profile",
			err.Error(), want.Error())
	}
	if code := ExitCode(err); code != ExitError {
		t.Errorf("ExitCode(%v) = %d, want %d (ExitError)",
			err, code, ExitError)
	}
	// The report is the actual harm, not the missing error: a caller
	// reading stdout must not find a profile there.
	if s := out.String(); strings.Contains(s, "ghost") ||
		strings.Contains(s, apidefaults.StarfleetAPIURL) {
		t.Errorf("profile show ghost rendered a report:\n%s", s)
	}
}

// TestProfileShowDefaultAccepted pins the deliberate exception: the
// built-in name is showable on a machine that has profiles but no
// section called `default`, because that is the name ResolveProfile
// itself falls back to. Rejecting it would break `profile show
// default` on precisely the installs where it is the only right
// answer.
func TestProfileShowDefaultAccepted(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	if _, ok := rt.Config.Profiles["default"]; ok {
		t.Fatal("fixture has a default section; this test needs one absent")
	}
	cmd := NewProfileCmd(rt)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"show", "default"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("profile show default = %v, want nil", err)
	}
	if !strings.Contains(out.String(), apidefaults.StarfleetAPIURL) {
		t.Errorf("expected the built-in default's Starfleet URL:\n%s",
			out.String())
	}
}

// --- how list reports an unresolvable active profile ---------------------

// TestProfileListUnresolvedActiveProfile covers the one state
// `profile list` can still be reached in with a broken
// current_profile: the row must say so, and must not print the
// production URL beside a name nothing can connect with. Without that,
// this row is indistinguishable from a healthy one.
func TestProfileListUnresolvedActiveProfile(t *testing.T) {
	rt, out, _ := profileRuntime(t, "text")
	rt.Profile = "ghost" // as a hand-edited current_profile resolves

	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	s := out.String()

	var ghostLine string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ghost") {
			ghostLine = line
		}
	}
	if ghostLine == "" {
		t.Fatalf("no row for the active profile:\n%s", s)
	}
	if !strings.Contains(ghostLine, "unresolved") {
		t.Errorf("ghost row does not mark itself unresolved: %q", ghostLine)
	}
	if strings.Contains(ghostLine, apidefaults.StarfleetAPIURL) {
		t.Errorf("ghost row still advertises the production URL — the "+
			"exact misreport this test guards: %q", ghostLine)
	}
	if !strings.Contains(ghostLine, "(not configured)") {
		t.Errorf("ghost row has no Starfleet URL placeholder: %q", ghostLine)
	}
	// The healthy rows must be untouched by the broken one.
	if !strings.Contains(s, "https://staging.example") {
		t.Errorf("configured profiles stopped rendering:\n%s", s)
	}
}

// TestProfileListResolvedFlagInJSON pins the machine-readable half:
// a parenthetical in a table cell is not something a script can test,
// so every entry carries the boolean.
func TestProfileListResolvedFlagInJSON(t *testing.T) {
	rt, out, _ := profileRuntime(t, "json")
	rt.Profile = "ghost"

	if err := runProfileList(rt); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	var entries []profileListEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal profile list: %v\n%s", err, out.String())
	}

	var seenGhost, seenDev bool
	for _, e := range entries {
		switch e.Name {
		case "ghost":
			seenGhost = true
			if e.Resolved {
				t.Error("ghost entry reports resolved=true")
			}
			if e.StarfleetURL != "" {
				t.Errorf("ghost entry carries starfleet_url %q, want empty",
					e.StarfleetURL)
			}
		case "dev":
			seenDev = true
			if !e.Resolved {
				t.Error("dev entry reports resolved=false")
			}
		}
	}
	if !seenGhost || !seenDev {
		t.Fatalf("expected both a ghost and a dev entry, got %+v", entries)
	}
}
