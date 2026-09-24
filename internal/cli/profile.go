package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// NewProfileCmd builds the `pgedge profile` command group: list, show
// and use switch between the named profiles in ~/.pgedge/cli/config.yaml
// without hand-editing YAML. Every module borrows the active profile, so
// this lives on the root command rather than inside any one module.
func NewProfileCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "Manage named CLI profiles",
		Long: `profile lists, inspects and switches between the named
profiles stored in ~/.pgedge/cli/config.yaml. Each profile carries its
own per-module connection settings (starfleet, controlplane); the active
one is chosen by --profile or current_profile in the config file, in
that order.

Example:
  pgedge profile list
  pgedge profile show
  pgedge profile use prod`,

		// This pair is permanent; internal/starfleet/cmd/root.go says
		// why. Cobra returns flag.ErrHelp for a non-runnable command
		// before ValidateArgs, so without RunE, cobra.NoArgs is dead
		// code and `pgedge profile stray` prints help and exits 0.
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newProfileListCmd(rt),
		newProfileShowCmd(rt),
		newProfileUseCmd(rt),
	)
	return cmd
}

// --- report structs -----------------------------------------------------

// profileListEntry is one row of `pgedge profile list`. CPURL is a
// comma-joined string, not a slice, because an HA controlplane profile
// can name several base URLs, and the value must render the same in the
// table's one "CONTROLPLANE URL" column and in -o json/-o yaml.
//
// Resolved is false only for a hand-edited current_profile naming no
// configured profile, seen from one of the two commands annotated to run
// anyway. Every configured profile resolves, as does the built-in
// "default". It is a field, not an absence, so a script can test whether
// its active profile is real while the row still names that profile.
type profileListEntry struct {
	Name         string `json:"name"`
	Active       bool   `json:"active"`
	Resolved     bool   `json:"resolved"`
	StarfleetURL string `json:"starfleet_url"`
	CPURL        string `json:"controlplane_url"`
}

// profileShowReport is `pgedge profile show`'s payload. It never
// carries the Starfleet secret's value: StarfleetHasSecret reports only
// whether one is stored, in every output format.
type profileShowReport struct {
	Name               string   `json:"name"`
	Active             bool     `json:"active"`
	StarfleetURL       string   `json:"starfleet_url"`
	StarfleetClientID  string   `json:"starfleet_client_id"`
	StarfleetHasSecret bool     `json:"starfleet_has_secret"`
	CPURLs             []string `json:"controlplane_urls,omitempty"`
}

// profileUseResult is `pgedge profile use`'s payload. The command has
// no API response of its own to render, but a mutating verb must
// still produce something coherent under -o json/-o yaml.
type profileUseResult struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// --- table rows -----------------------------------------------------------

var profileListColumns = []string{"NAME", "ACTIVE", "STARFLEET URL", "CONTROLPLANE URL"}

type profileListRow struct{ e profileListEntry }

// Columns renders an unresolved profile's ACTIVE cell as
// "yes (unresolved)". The table has no Resolved column because only the
// hand-edited case is ever unresolved, so the column would read "yes" on
// every row of a healthy install. -o json carries the field on every
// entry, because a script cannot read a parenthetical.
func (r profileListRow) Columns() []string {
	active := output.BoolYesNo(r.e.Active)
	if r.e.Active && !r.e.Resolved {
		active += " (unresolved)"
	}
	return []string{
		r.e.Name,
		active,
		orNotConfigured(r.e.StarfleetURL),
		r.e.CPURL,
	}
}

// profileFieldRow renders one FIELD/VALUE line of `pgedge profile
// show`'s text table.
type profileFieldRow struct{ field, value string }

func (r profileFieldRow) Columns() []string {
	return []string{r.field, r.value}
}

// --- list -------------------------------------------------------------------

func newProfileListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured profiles",
		Long: `list shows every named profile in the config file — its
Starfleet API URL, its Control Plane base URL(s), and whether it is the
one currently active.

The active profile always appears, even on a fresh install with no
config file at all: it resolves to a default Starfleet API URL the same
way 'pgedge doctor' does.

An active profile that is not configured at all — only reachable by
hand-editing current_profile — shows as 'yes (unresolved)' with no
Starfleet URL. Fix it with 'pgedge profile use <name>'.

Example:
  pgedge profile list
  pgedge profile list -o json`,
		Annotations: map[string]string{
			AnnotationProfileRepair: "reports the configured profiles, " +
				"which is what an operator needs to see when " +
				"current_profile names none of them",
		},
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runProfileList(rt)
		},
	}
}

func runProfileList(rt *module.Runtime) error {
	names := rt.Config.ProfileNames()
	if !slices.Contains(names, rt.Profile) {
		names = append(names, rt.Profile)
		sort.Strings(names)
	}

	entries := make([]profileListEntry, 0, len(names))
	for _, name := range names {
		e := profileListEntry{
			Name:     name,
			Active:   name == rt.Profile,
			Resolved: CheckProfileName(rt.Config, name) == nil,
			CPURL: strings.Join(
				rt.Config.ControlplaneProfile(name).EffectiveBaseURLs(), ", "),
		}
		// An unresolved profile gets no Starfleet URL. starfleetURLFor
		// would return the production default, but no command dials it:
		// the guard refuses the profile except here and `profile use`.
		if e.Resolved {
			e.StarfleetURL = starfleetURLFor(rt, name)
		}
		entries = append(entries, e)
	}

	if rt.Output.Structured() {
		return rt.Output.Print(entries, nil)
	}

	rows := make([]output.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, profileListRow{e})
	}
	return rt.Output.Print(rows, profileListColumns)
}

// --- show -------------------------------------------------------------------

func newProfileShowCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show a profile's connection settings",
		Long: `show prints one profile's Starfleet API URL, its Starfleet
client ID, whether a Starfleet secret is stored, and any Control
Plane base URLs. The client ID is an identifier, not a credential, so
it is printed; the secret's value is never printed, in any output
format — only its presence.

With no argument it shows the active profile.

Example:
  pgedge profile show
  pgedge profile show prod
  pgedge profile show prod -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := rt.Profile
			if len(args) == 1 {
				name = args[0]
				// The argument is a third way to name a profile, so it
				// goes through the validator --profile and
				// current_profile use, and all three say the same thing.
				// Unchecked, an unknown name would print a plausible
				// report with the production Starfleet URL in it. With no
				// argument, the guard has already validated rt.Profile.
				if err := CheckProfileName(rt.Config, name); err != nil {
					return err
				}
			}
			return runProfileShow(rt, name)
		},
	}
}

func runProfileShow(rt *module.Runtime, name string) error {
	starfleetProf := rt.Config.StarfleetProfile(name)
	report := profileShowReport{
		Name:               name,
		Active:             name == rt.Profile,
		StarfleetURL:       starfleetURLFor(rt, name),
		StarfleetClientID:  starfleetProf.ClientID,
		StarfleetHasSecret: hasStarfleetSecret(rt, name, starfleetProf),
		CPURLs:             rt.Config.ControlplaneProfile(name).EffectiveBaseURLs(),
	}

	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}

	rows := []output.Row{
		profileFieldRow{"NAME", report.Name},
		profileFieldRow{"ACTIVE", output.BoolYesNo(report.Active)},
		profileFieldRow{"STARFLEET URL", report.StarfleetURL},
		profileFieldRow{
			"STARFLEET CLIENT ID", orNotSet(report.StarfleetClientID),
		},
		profileFieldRow{
			"STARFLEET SECRET", secretPresence(report.StarfleetHasSecret),
		},
		profileFieldRow{"CONTROLPLANE URL(S)", controlplaneURLsOrNone(report.CPURLs)},
	}
	return rt.Output.Print(rows, []string{"FIELD", "VALUE"})
}

// --- use --------------------------------------------------------------------

func newProfileUseCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active profile",
		Long: `use points current_profile at an existing profile in
~/.pgedge/cli/config.yaml and saves the change immediately, so every
later command uses it until overridden by --profile.

Naming a profile that is not configured is a runtime error listing
every profile that is: it fails rather than writing a current_profile
no later command could resolve credentials for.

Example:
  pgedge profile use prod
  pgedge profile use prod -o json`,
		Annotations: map[string]string{
			AnnotationProfileRepair: "rewrites current_profile, so it " +
				"is the way out of an unresolvable one",
		},
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runProfileUse(rt, args[0])
		},
	}
}

func runProfileUse(rt *module.Runtime, name string) error {
	if err := rt.Config.SetCurrentProfile(name); err != nil {
		return err
	}
	if err := rt.Config.Save(); err != nil {
		return fmt.Errorf("profile use: save config: %w", err)
	}

	fmt.Fprintf(rt.Stderr, "Active profile set to %q.\n", name)

	if rt.Output.Structured() {
		return rt.Output.Print(
			profileUseResult{Name: name, Active: true}, nil)
	}
	return nil
}

// --- shared helpers -----------------------------------------------------

// starfleetURLFor returns the Starfleet API URL stored on the named
// profile, or the module-wide default when the profile sets none. It
// does not apply --api-url as conn.ResolveAPIURL does: list and show
// report every profile's persisted settings, not the live connection.
func starfleetURLFor(rt *module.Runtime, name string) string {
	if url := rt.Config.StarfleetProfile(name).APIURL; url != "" {
		return url
	}
	return apidefaults.StarfleetAPIURL
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// orNotConfigured renders an empty URL cell. orNotSet marks a field the
// profile could carry and does not; this marks a profile that does not
// exist to carry one.
func orNotConfigured(s string) string {
	if s == "" {
		return "(not configured)"
	}
	return s
}

func secretPresence(has bool) string {
	if has {
		return "set"
	}
	return "not set"
}

func controlplaneURLsOrNone(urls []string) string {
	if len(urls) == 0 {
		return "(not configured)"
	}
	return strings.Join(urls, ", ")
}

// hasStarfleetSecret reports whether the profile's secret is stored
// anywhere: the config file, or the keychain entry login wrote. Only
// presence is read; the value is discarded.
func hasStarfleetSecret(rt *module.Runtime, name string,
	p *config.StarfleetProfile) bool {
	if p.ClientSecret != "" {
		return true
	}
	if p.ClientID == "" || rt.Keychain == nil {
		return false
	}
	_, err := rt.Keychain.Get(keychain.Account(name, rt.Config.SavePath()))
	return err == nil
}
