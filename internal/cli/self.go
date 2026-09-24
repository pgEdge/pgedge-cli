package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/selfupdate"
	"github.com/spf13/cobra"
)

// selfUpdateReport is the structured payload `self update` prints.
// Elsewhere in this CLI a success carrying no body prints nothing to
// stdout; here the report is the body, in all three formats, and this
// command is the only exception.
type selfUpdateReport struct {
	Previous string `json:"previous" yaml:"previous"`
	New      string `json:"new" yaml:"new"`
	// UpdateAvailable is set by --check and by nothing else: true or
	// false, always present, because "is there an update?" is the
	// whole question --check was asked. Every other path leaves it
	// nil so json/yaml omit it rather than print a field the run
	// never evaluated.
	UpdateAvailable *bool         `json:"update_available,omitempty" yaml:"update_available,omitempty"`
	Modules         []moduleDelta `json:"modules" yaml:"modules"`
}

// moduleDelta is one module's version line in the report.
type moduleDelta struct {
	Name     string `json:"name" yaml:"name"`
	Previous string `json:"previous" yaml:"previous"`
	New      string `json:"new" yaml:"new"`
}

// selfUpdateRow renders one selfUpdateReport line as a text table row:
// COMPONENT PREVIOUS NEW.
type selfUpdateRow struct{ Component, Previous, New string }

func (r selfUpdateRow) Columns() []string {
	return []string{r.Component, r.Previous, r.New}
}

// SelfDeps injects self update's externally-facing dependencies for
// tests; a nil field takes the production default.
type SelfDeps struct {
	// Source fetches releases and downloads assets. nil builds the
	// production ladder: the unauthenticated HTTP rung, falling back
	// to gh.
	Source selfupdate.Source

	// Runner execs the newly-installed binary's version probe. nil
	// uses exec.CommandContext.
	Runner selfupdate.Runner

	// Resolve locates and refusal-checks the swap target. nil uses
	// selfupdate.ResolveTarget.
	Resolve func() (string, error)

	// Swap renames the staged binary onto the target. nil uses
	// selfupdate.Swap. Injectable so a test can observe WHERE the
	// rename reads from: staging anywhere but target's own directory
	// is unrenameable across a filesystem boundary, and no hermetic
	// test can conjure one.
	Swap func(target, staged string) error

	// Trusted supplies the Sigstore trust material VerifySignature
	// checks the release's checksums.txt against. nil calls
	// selfupdate.ProductionTrustedMaterial, which reaches public
	// Sigstore infrastructure — tests inject a hermetic one built
	// from selfupdate.NewTestTrustedMaterial instead.
	Trusted *selfupdate.TrustedMaterial
}

// exitAuthError marks a self-update failure caused by an
// unauthenticated gh session, so cli.ExitCode maps it to exit 5 — this
// repo's convention for "the credential is fine, the session isn't"
// (internal/starfleet/conn.ExitAuth). selfupdate has no exit-code concept
// of its own, so the classification happens here rather than there.
type exitAuthError struct{ err error }

func (e *exitAuthError) Error() string { return e.err.Error() }
func (e *exitAuthError) Unwrap() error { return e.err }
func (e *exitAuthError) Code() int     { return 5 }

// classifySelfUpdateError promotes a ladder failure caused by an
// unauthenticated gh session to exit 5; every other ladder failure
// (network, rate limit, gh missing) stays a plain error and maps to
// the default exit 1.
func classifySelfUpdateError(err error) error {
	if errors.Is(err, selfupdate.ErrGHUnauthenticated) {
		return &exitAuthError{err: err}
	}
	return err
}

// releaseAssetName derives the release archive's filename from tag:
// BINARY_VERSION_OS_ARCH.tar.gz, as install.sh derives it, or .zip on
// Windows, which install.sh does not serve.
//
// tag comes from --version or, without it, the fetched release feed, so
// the name is checked for path traversal before any Download. A
// conforming name is a bare filename, so filepath.Base(name) == name is
// the whole check.
func releaseAssetName(tag string) (string, error) {
	name := fmt.Sprintf("pgedge_%s_%s_%s.%s",
		strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH,
		archiveExtension(runtime.GOOS))

	if filepath.Base(name) != name {
		return "", fmt.Errorf(
			"refusing to download %q: not a bare filename", name)
	}
	return name, nil
}

// archiveExtension is releaseAssetName's OS-to-extension mapping,
// split out so it can be unit-tested for both branches independent of
// the host running the test.
func archiveExtension(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// NewSelfCmd builds the `pgedge self` command group.
func NewSelfCmd(
	rt *module.Runtime, modules []module.ModuleInfo, deps *SelfDeps,
) *cobra.Command {
	if deps == nil {
		deps = &SelfDeps{}
	}

	self := &cobra.Command{
		Use:   "self",
		Short: "Manage the pgedge binary itself",
		Long: `self groups commands that manage the pgedge binary
itself, rather than any pgEdge product.

Example:
  pgedge self update`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	self.AddCommand(newSelfUpdateCmd(rt, modules, deps))
	return self
}

func newSelfUpdateCmd(
	rt *module.Runtime, modules []module.ModuleInfo, deps *SelfDeps,
) *cobra.Command {
	var (
		wantVersion string
		check       bool
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the pgedge binary to the latest release",
		Long: `update checks GitHub for a newer pgedge release,
verifies its Sigstore signature and checksum, and swaps it into
place. Progress goes to stderr; the version report is the command's
stdout body.

update refuses to touch a Homebrew install or a binary inside a git
working tree — run 'brew upgrade pgedge' or 'make build' instead.

After the swap, any shell completion script 'completion install' put
in place is regenerated by the new binary.

Example:
  pgedge self update
  pgedge self update --check
  pgedge self update --version v0.6.0 --force`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSelfUpdate(
				rt, modules, deps, wantVersion, check, force)
		},
	}
	cmd.Flags().StringVar(&wantVersion, "version", "",
		"Update to a specific release tag instead of the newest")
	cmd.Flags().BoolVar(&check, "check", false,
		"Report whether an update is available; download nothing")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	return cmd
}

// productionSource builds the production download ladder: the
// unauthenticated HTTP rung, falling back to gh, both answering
// --verbose and --debug on rt's stderr.
func productionSource(rt *module.Runtime) selfupdate.Source {
	return newLadder(rt, "https://api.github.com", "https://github.com",
		exec.CommandContext)
}

// newLadder is productionSource with its endpoints and Runner
// injectable, so a test can drive the diagnostics wiring against an
// httptest server instead of GitHub.
func newLadder(
	rt *module.Runtime, baseAPI, baseDL string, run selfupdate.Runner,
) selfupdate.Source {
	lvl := httplog.LevelFor(rt.Verbose, rt.Debug)
	return selfupdate.NewChain(
		selfupdate.NewHTTPSource(baseAPI, baseDL).Logging(rt.Stderr, lvl),
		selfupdate.NewGHSource(run).Logging(rt.Stderr, lvl),
	)
}

func runSelfUpdate(
	rt *module.Runtime, modules []module.ModuleInfo, deps *SelfDeps,
	wantVersion string, check, force bool,
) error {
	src := deps.Source
	if src == nil {
		src = productionSource(rt)
	}
	runner := deps.Runner
	if runner == nil {
		runner = exec.CommandContext
	}
	resolveTarget := deps.Resolve
	if resolveTarget == nil {
		resolveTarget = selfupdate.ResolveTarget
	}

	// Resolved first: it touches no network, so a run doomed to refuse
	// (a Homebrew install, a binary inside a git working tree) fails
	// before a release fetch and a three-asset download.
	//
	// Under --check a refusal is a note on the answer rather than a
	// reason to withhold it, since --check swaps nothing. --check exits
	// 0 either way.
	target, err := resolveTarget()
	if err != nil {
		var refusal *selfupdate.RefusalError
		if !check || !errors.As(err, &refusal) {
			return err
		}
		fmt.Fprintf(rt.Stderr, "note: %s\n", output.Sanitize(refusal.Msg))
	}

	listCtx, cancelList := context.WithTimeout(
		context.Background(), selfupdate.ListTimeout)
	defer cancelList()

	releases, err := src.Releases(listCtx)
	if err != nil {
		return classifySelfUpdateError(err)
	}

	release, err := selfupdate.Resolve(releases, wantVersion)
	if err != nil {
		return err
	}

	// Tags carry a "v"; Version (goreleaser's {{.Version}}) and the
	// probed binary's answer do not.
	newVersion := strings.TrimPrefix(release.TagName, "v")
	report := selfUpdateReport{Previous: Version, New: newVersion}
	for _, m := range modules {
		report.Modules = append(report.Modules,
			moduleDelta{Name: m.Name, Previous: m.Version, New: m.Version})
	}

	if selfupdate.IsCurrent(release.TagName, Version) {
		report.New = report.Previous
		if check {
			available := false
			report.UpdateAvailable = &available
		}
		fmt.Fprintf(rt.Stderr, "pgedge is already up to date (%s)\n", output.Sanitize(Version))
		return printSelfUpdateReport(rt, report)
	}

	if check {
		available := true
		report.UpdateAvailable = &available
		return printSelfUpdateReport(rt, report)
	}

	// Everything below swaps a binary, so it needs a target. Only the
	// --check refusal path leaves it empty, and --check has returned by
	// now, but that rests on control flow four branches up, so it is
	// asserted here, where it matters.
	if target == "" {
		return fmt.Errorf("no swap target resolved")
	}

	if err := Confirm(rt,
		fmt.Sprintf("update %s -> %s?", Version, newVersion),
		force); err != nil {
		return err
	}

	assetName, err := releaseAssetName(release.TagName)
	if err != nil {
		return err
	}

	dstDir, err := os.MkdirTemp("", "pgedge-self-update-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dstDir) }()

	dlCtx, cancelDL := context.WithTimeout(
		context.Background(), selfupdate.DownloadTimeout)
	defer cancelDL()

	fmt.Fprintf(rt.Stderr, "downloading %s...\n", output.Sanitize(assetName))
	archivePath, err := src.Download(dlCtx, release.TagName, assetName, dstDir)
	if err != nil {
		return classifySelfUpdateError(err)
	}

	checksums, err := downloadBytes(dlCtx, src, release.TagName, selfupdate.ChecksumsAsset, dstDir)
	if err != nil {
		return classifySelfUpdateError(err)
	}
	sigBundle, err := downloadBytes(dlCtx, src, release.TagName, selfupdate.BundleAsset, dstDir)
	if err != nil {
		return classifySelfUpdateError(err)
	}

	trusted := deps.Trusted
	if trusted == nil {
		fmt.Fprintln(rt.Stderr, "loading sigstore trust root...")
		trusted, err = selfupdate.ProductionTrustedMaterial()
		if err != nil {
			return fmt.Errorf("load sigstore trust root: %w", err)
		}
	}

	fmt.Fprintln(rt.Stderr, "verifying signature...")
	if err := selfupdate.VerifySignature(checksums, sigBundle, trusted); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}

	fmt.Fprintln(rt.Stderr, "verifying checksum...")
	if err := selfupdate.VerifyChecksum(archivePath, assetName, checksums); err != nil {
		return fmt.Errorf("verify checksum: %w", err)
	}

	fmt.Fprintln(rt.Stderr, "extracting...")
	newBinary, err := selfupdate.ExtractBinary(archivePath, dstDir)
	if err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	fmt.Fprintln(rt.Stderr, "installing...")

	// Staged in TARGET's directory, not the download directory: the
	// swap is a rename, and a rename cannot cross a filesystem
	// boundary. $TMPDIR is a different filesystem from the install
	// directory on the common Linux layout (tmpfs /tmp, binary in
	// ~/.local/bin), where renaming from there fails with EXDEV.
	staged, err := selfupdate.StageBeside(target, newBinary)
	if err != nil {
		return fmt.Errorf("swap binary: %w", err)
	}
	// Unconditional: after a successful rename there is nothing at
	// this path to remove, and after any failure between the two
	// there is.
	defer func() { _ = os.Remove(staged) }()

	swap := deps.Swap
	if swap == nil {
		swap = selfupdate.Swap
	}
	if err := swap(target, staged); err != nil {
		return fmt.Errorf("swap binary: %w", err)
	}

	for i := range report.Modules {
		report.Modules[i].New = "unknown"
	}
	if !probeNewBinary(runner, target, &report) {
		fmt.Fprintln(rt.Stderr,
			"could not verify the new binary's version; "+
				"run 'pgedge version' to confirm")
	}
	refreshCompletions(rt, runner, target)

	return printSelfUpdateReport(rt, report)
}

// downloadBytes downloads name into dstDir via src and returns its
// content; the three assets a self update reads (the archive,
// checksums.txt and its signature bundle) are staged files this
// command never needs to keep around once it has their bytes.
func downloadBytes(
	ctx context.Context, src selfupdate.Source, tag, name, dstDir string,
) ([]byte, error) {
	path, err := src.Download(ctx, tag, name, dstDir)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path) //nolint:gosec // G304: path is src's own return value, inside the staging dir this command created.
}

// probeNewBinary execs the newly-swapped binary's `version -o json`
// and fills report.New (the launcher version) and each module's New
// from the answer, matched by name. It reports whether the probe
// succeeded; report.New keeps the release tag it was already set to,
// and unmatched modules keep the "unknown" the caller pre-filled, on
// failure — there is no operator to retry the probe for, and the swap
// itself already succeeded.
func probeNewBinary(
	runner selfupdate.Runner, target string, report *selfUpdateReport,
) bool {
	ctx, cancel := context.WithTimeout(
		context.Background(), selfupdate.ProbeTimeout)
	defer cancel()

	cmd := runner(ctx, target, "version", "-o", "json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false
	}

	var info versionInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return false
	}

	// An answer that PARSED is not the same as an answer that said
	// anything: `{}` decodes cleanly and would blank the launcher row
	// and every module cell. Only a non-empty value overwrites what
	// the caller pre-filled — the release tag, and "unknown".
	if info.Version != "" {
		report.New = info.Version
	}
	byName := make(map[string]string, len(info.Modules))
	for _, m := range info.Modules {
		byName[m.Name] = m.Version
	}
	for i := range report.Modules {
		if v, ok := byName[report.Modules[i].Name]; ok && v != "" {
			report.Modules[i].New = v
		}
	}
	return true
}

func printSelfUpdateReport(rt *module.Runtime, report selfUpdateReport) error {
	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}

	rows := []output.Row{
		selfUpdateRow{Component: "pgedge", Previous: report.Previous, New: report.New},
	}
	for _, m := range report.Modules {
		rows = append(rows,
			selfUpdateRow{Component: m.Name, Previous: m.Previous, New: m.New})
	}
	return rt.Output.Print(rows, []string{"COMPONENT", "PREVIOUS", "NEW"})
}
