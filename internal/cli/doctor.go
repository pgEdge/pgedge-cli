package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/pgEdge/pgedge-cli/internal/apidefaults"
	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/selfupdate"
	"github.com/spf13/cobra"
)

// DoctorDeps injects doctor's externally-facing dependency for tests;
// a nil field takes the production default. It is SelfDeps's Source
// seam alone, since doctor downloads and swaps nothing.
type DoctorDeps struct {
	// Source fetches the release list checkLatestVersion resolves
	// against. nil builds the same production ladder `pgedge self
	// update` uses: the unauthenticated HTTP rung, falling back to gh.
	Source selfupdate.Source
	// NoVersionCheck skips the "Latest version" row's release lookup,
	// the one call doctor makes off the machine. A preset value is the
	// --no-version-check flag's default.
	NoVersionCheck bool
}

// --- report structs ----------------------------------------------------------

// doctorVersionInfo is the version block of the install report. It is
// named apart from versionInfo (version.go), which is the `pgedge
// version` payload; the two carry different fields for different
// commands and only happen to share a package.
type doctorVersionInfo struct {
	CLI  string `json:"cli"`
	Go   string `json:"go"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type latestInfo struct {
	// Checked is false under --no-version-check, when Latest and
	// UpToDate carry their zero values rather than a finding.
	Checked  bool   `json:"checked"`
	Latest   string `json:"latest"`
	Current  string `json:"current"`
	UpToDate bool   `json:"up_to_date"`
}

// configInfo covers the config file, which the install owns. It
// deliberately says nothing about the token cache: that file is the
// starfleet module's object, so whether a token exists — and whether it
// is still valid, which matters far more — is `pgedge starfleet doctor`'s
// to report (see internal/starfleet/account/cmd/doctor.go,
// authInfo.TokenValid).
type configInfo struct {
	Dir string `json:"dir"`
	// Path is the whole file, because under --config it need not be
	// config.yaml, and Dir alone cannot say which file was checked.
	Path         string `json:"path"`
	ConfigExists bool   `json:"config_exists"`
}

type shellInfo struct {
	Shell  string `json:"shell"`
	InPath bool   `json:"in_path"`
}

// connectionInfo is one line per configured module connection: what
// the install can see, without authenticating against any of it.
//
// Status is a verdict on the *configuration*, never on the connection
// working — nothing here is dialled. "ok" means a command could be
// attempted; "warning" means it would fail before it left the machine
// for want of credentials. starfleet cannot authenticate without a
// client ID and secret, while controlplane has no login and its mTLS
// keypair is optional, so only a starfleet row without credentials is a
// warning.
type connectionInfo struct {
	Module   string `json:"module"`
	Profile  string `json:"profile"`
	URL      string `json:"url"`
	HasCreds bool   `json:"has_credentials"`
	Status   string `json:"status"`
	// Detail is the human-readable reason behind Status; the text table
	// renders it and json/yaml carry it so a machine reader gets the
	// same caveat.
	Detail string `json:"detail"`
}

// doctorReport is the install-level report. It deliberately carries no
// auth or reachability verdict: proving a connection works means
// authenticating against it, which is the owning module's job
// (`pgedge starfleet doctor`, `pgedge controlplane doctor`).
type doctorReport struct {
	Version       doctorVersionInfo `json:"version"`
	LatestVersion latestInfo        `json:"latest_version"`
	Config        configInfo        `json:"config"`
	Shell         shellInfo         `json:"shell"`
	InstallMethod string            `json:"install_method"`
	Connections   []connectionInfo  `json:"connections"`
}

// --- command wiring -----------------------------------------------------------

// NewDoctorCmd builds the `pgedge doctor` command.
func NewDoctorCmd(rt *module.Runtime, deps *DoctorDeps) *cobra.Command {
	if deps == nil {
		deps = &DoctorDeps{}
	}
	var noVersionCheck bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the pgedge installation",
		Long: `doctor checks the pgedge install itself: version, config
file, shell integration, install method, and one line per connection
the active profile configures. It authenticates against nothing, so it
works when auth is broken.

The "Latest version" row is the one check that leaves the machine: it
asks api.github.com for the newest release. --no-version-check skips
it, for an air-gapped or restricted network, and the row then reads
"not checked" rather than warning about a lookup you did not want.

For a verdict on a specific connection, run that module's own doctor:
'pgedge starfleet doctor' for pgEdge Starfleet, 'pgedge controlplane doctor' for a
Control Plane.

Example:
  pgedge doctor
  pgedge doctor -o json
  pgedge doctor --no-version-check`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			run := *deps
			run.NoVersionCheck = noVersionCheck
			return runDoctor(rt, &run)
		},
	}
	cmd.Flags().BoolVar(&noVersionCheck, "no-version-check",
		deps.NoVersionCheck,
		"Skip the release lookup on api.github.com")
	return cmd
}

// --- check implementations ---------------------------------------------------

func checkVersion() doctorVersionInfo {
	return doctorVersionInfo{
		CLI:  Version,
		Go:   runtime.Version(),
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
}

// checkLatestVersion asks src for the release list and resolves the
// newest tag — the same resolution `pgedge self update` runs with an
// empty --version. src only reads here: doctor takes no --dry-run, and
// this call downloads nothing (Resolve only inspects TagName), so no
// dryrun-wrapped client is needed. If a write is ever added here it
// must go through one — nothing else would stop it.
//
// The error is returned rather than folded into latestInfo because
// runDoctor's text row distinguishes a gh-unauthenticated failure from
// any other (errors.Is), a distinction latestInfo's JSON/yaml shape
// has no field for.
func checkLatestVersion(src selfupdate.Source) (latestInfo, error) {
	info := latestInfo{Current: Version, Checked: true}

	// doctor prints one row and moves on, so the whole check — both
	// ladder rungs, including a gh exec that has no timeout of its
	// own — is bounded far tighter than an update's fetch.
	ctx, cancel := context.WithTimeout(
		context.Background(), selfupdate.DoctorCheckTimeout)
	defer cancel()

	releases, err := src.Releases(ctx)
	if err != nil {
		return info, err
	}

	release, err := selfupdate.Resolve(releases, "")
	if err != nil {
		return info, err
	}

	info.Latest = release.TagName
	info.UpToDate = selfupdate.IsCurrent(release.TagName, Version)
	return info, nil
}

// checkConfig reports on the config file this invocation is actually
// reading. It takes the loaded *Config rather than re-deriving the
// default, because under --config those are different files.
func checkConfig(cfg *config.Config) configInfo {
	configPath := ""
	if cfg != nil {
		configPath = cfg.Path()
	}
	if configPath == "" {
		// Nil safety only: cfg is never nil on a path reaching
		// runDoctor (a Load failure returns before the run hooks, and
		// Load always sets a path), but Path() on a nil *Config panics,
		// which this repo bans, and doctor is what an operator runs
		// when everything else is broken. This branch keeps the guard
		// from reporting Dir("").
		p, err := config.DefaultPath()
		if err != nil {
			return configInfo{}
		}
		configPath = p
	}
	info := configInfo{
		Dir:  filepath.Dir(configPath),
		Path: configPath,
	}

	if _, err := os.Stat(configPath); err == nil {
		info.ConfigExists = true
	}
	return info
}

func checkShell() shellInfo {
	// Not envShell (completion.go): the env-isolation scanner
	// (internal/testsupport's TestEnvIsolationCoversProductionReads)
	// resolves a constant only within the file that declares it, so a
	// cross-file reference to that name would report as unscannable
	// rather than as a read of SHELL.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "unknown"
	}

	_, err := exec.LookPath("pgedge")
	return shellInfo{
		Shell:  shell,
		InPath: err == nil,
	}
}

func checkInstallMethod() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return selfupdate.InstallMethodFrom(resolved)
}

// connectionsInfo lists the connections the active profile configures,
// without contacting any of them.
//
// The Starfleet connection is always listed: it resolves to a
// default base URL even on a first run, so "which API would a Starfleet
// command talk to" always has an answer worth printing. Control Plane
// connections are listed only when the profile names a base URL,
// because controlplane has no default worth asserting for the user.
func connectionsInfo(rt *module.Runtime) []connectionInfo {
	ap := rt.Config.StarfleetProfile(rt.Profile)
	account := connectionInfo{
		Module:  "starfleet",
		Profile: rt.Profile,
		// No flag override: this is the install's view of the profile.
		URL: apidefaults.ResolveStarfleetAPIURL(ap, ""),
		// Presence only — a credential value is never printed or hashed.
		HasCreds: ap.ClientID != "" && hasStarfleetSecret(rt, rt.Profile, ap),
	}
	// The Starfleet API needs a client ID and secret, so their absence is
	// the difference between "a command might work" and "a command
	// cannot even try". Never "ok" on a connection with no credentials.
	if account.HasCreds {
		account.Status = "ok"
		account.Detail = "credentials found, not verified — " +
			"run 'pgedge starfleet doctor'"
	} else {
		account.Status = "warning"
		account.Detail = "no credentials — run 'pgedge starfleet auth login'"
	}
	conns := []connectionInfo{account}

	cp := rt.Config.ControlplaneProfile(rt.Profile)
	// The Control Plane's only credential is an mTLS client keypair, and
	// it is optional: a plain-HTTP control plane needs none, so an
	// absent keypair is not a warning here the way absent Starfleet
	// credentials are. The detail says mTLS rather than "credentials"
	// so the row cannot be read as "unconfigured but fine".
	controlplaneCreds := cp.ClientCert != "" && cp.ClientKey != ""
	controlplaneDetail := "no login required, mTLS disabled, not verified"
	if controlplaneCreds {
		controlplaneDetail = "no login required, mTLS configured, not verified"
	}
	for _, u := range cp.EffectiveBaseURLs() {
		conns = append(conns, connectionInfo{
			Module:   "controlplane",
			Profile:  rt.Profile,
			URL:      u,
			HasCreds: controlplaneCreds,
			Status:   "ok",
			Detail:   controlplaneDetail,
		})
	}
	return conns
}

// --- runner -------------------------------------------------------------------

func runDoctor(rt *module.Runtime, deps *DoctorDeps) error {
	if deps == nil {
		deps = &DoctorDeps{}
	}
	var (
		latest    = latestInfo{Current: Version}
		latestErr error
	)
	if !deps.NoVersionCheck {
		src := deps.Source
		if src == nil {
			src = productionSource(rt)
		}
		latest, latestErr = checkLatestVersion(src)
	}
	report := doctorReport{
		Version:       checkVersion(),
		LatestVersion: latest,
		Config:        checkConfig(rt.Config),
		Shell:         checkShell(),
		InstallMethod: checkInstallMethod(),
		Connections:   connectionsInfo(rt),
	}

	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}

	v := report.Version

	// Latest version status
	latestStatus := "warning"
	latestDetail := "could not check"
	switch {
	case !report.LatestVersion.Checked:
		latestStatus = "ok"
		latestDetail = "not checked (--no-version-check)"
	case latestErr == nil && report.LatestVersion.UpToDate:
		latestStatus = "ok"
		latestDetail = fmt.Sprintf("%s (up to date)",
			report.LatestVersion.Current)
	case latestErr == nil:
		latestDetail = fmt.Sprintf("%s available — run 'pgedge self update'",
			report.LatestVersion.Latest)
	case errors.Is(latestErr, selfupdate.ErrGHUnauthenticated):
		latestDetail = "could not check (gh not available/authenticated)"
	}

	// Config status
	configStatus := "ok"
	if !report.Config.ConfigExists {
		configStatus = "warning"
	}

	// Shell status
	shellSt := "ok"
	shellDet := report.Shell.Shell + ", in PATH"
	if !report.Shell.InPath {
		shellSt = "warning"
		shellDet = report.Shell.Shell + ", not in PATH"
	}

	rows := []output.Row{
		output.CheckRow{
			Check:  "Version",
			Status: "ok",
			Details: fmt.Sprintf("%s (%s, %s/%s)",
				v.CLI, v.Go, v.OS, v.Arch),
		},
		output.CheckRow{
			Check:   "Latest version",
			Status:  latestStatus,
			Details: latestDetail,
		},
		output.CheckRow{
			Check:  "Config",
			Status: configStatus,
			// The real filename, not a literal: under --config it is
			// not config.yaml.
			Details: fmt.Sprintf("%s (%s: %s)",
				report.Config.Dir,
				filepath.Base(report.Config.Path),
				output.BoolYesNo(report.Config.ConfigExists)),
		},
		output.CheckRow{
			Check: "Shell", Status: shellSt, Details: shellDet,
		},
		output.CheckRow{
			Check:   "Install method",
			Status:  "ok",
			Details: report.InstallMethod,
		},
	}
	// One row per connection. The status judges the configuration only —
	// every Detail ends in "not verified" for the ones that could be
	// attempted, because this command dials nothing.
	for _, c := range report.Connections {
		rows = append(rows, output.CheckRow{
			Check:  "Connection",
			Status: c.Status,
			Details: fmt.Sprintf("%s (%s): %s (%s)",
				c.Module, c.Profile, c.URL, c.Detail),
		})
	}

	return rt.Output.Print(rows, output.CheckHeaders())
}
