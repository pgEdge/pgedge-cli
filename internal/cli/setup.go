package cli

import (
	"fmt"
	"os"

	"github.com/pgEdge/pgedge-cli/internal/config"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// SetupError marks a failure that happened while building the
// Runtime, before any command ran. It is a runtime failure (exit 1),
// not a cobra parse error, so main must not promote it to exit 2.
type SetupError struct{ Err error }

func (e *SetupError) Error() string { return e.Err.Error() }
func (e *SetupError) Unwrap() error { return e.Err }

// setupRuntime populates rt from fs, the executing command's merged,
// already-parsed flag set. Reading fs rather than scanning argv means a
// global flag takes effect only when cobra bound the token to it: a value
// consumed by another flag, or a token after `--`, is invisible here as
// it is to every other flag.
func setupRuntime(rt *module.Runtime, fs *pflag.FlagSet) error {
	cfgPath, _ := fs.GetString("config")
	// An empty --config is malformed, not absent: `--config
	// "$MY_CONFIG"` with the variable unset would silently resolve the
	// built-in default profile, whose api_url is production, the
	// fail-open direction the missing-file check closes. Only fs.Changed
	// tells the two apart, as it does for --output below.
	if fs.Changed("config") && cfgPath == "" {
		return &UsageError{
			Msg: "--config given an empty value: name a file, or omit " +
				"the flag to use the default"}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return &SetupError{Err: err}
	}

	profileFlag, _ := fs.GetString("profile")
	// An empty --profile is malformed too: `--profile "$P"` with the
	// variable unset would silently resolve current_profile, and since a
	// profile is a tenant, that is a different account from the one the
	// operator meant. `profile use`, the current_profile repair, stays
	// reachable: profileguard's carve-out keys on ProfileExplicit being
	// false, which omitting the flag still gives.
	if fs.Changed("profile") && profileFlag == "" {
		return &UsageError{
			Msg: "--profile given an empty value: name a profile, or " +
				"omit the flag to use the active one"}
	}
	profile := cfg.ResolveProfile(profileFlag)

	format := cfg.Output.Format
	if format == "" {
		format = "text"
	}
	// --output defaults to "text", so fs.GetString returns "text" even
	// when the user gave nothing, which would stomp a config-file
	// output.format. Only an explicit --output overrides it.
	if fs.Changed("output") {
		if v, _ := fs.GetString("output"); v != "" {
			format = v
		}
	}
	// An unsupported format is a usage error (exit 2), caught here so it
	// fails the same way for every command, whether or not it renders.
	if !output.ValidFormat(format) {
		return &UsageError{Msg: fmt.Sprintf(
			"unsupported output format: %q (want text, json, yaml)",
			format)}
	}

	noColor, _ := fs.GetBool("no-color")
	colorOK := !noColor &&
		os.Getenv("NO_COLOR") == "" &&
		term.IsTerminal(int(os.Stdout.Fd()))

	verbose, _ := fs.GetBool("verbose")
	debug, _ := fs.GetBool("debug")

	// --dry-run is registered per mutating leaf (see MarkMutating), not
	// on root, so most flag sets lack it, and Lookup finds that out
	// where GetString would return an error. NoOptDefVal makes a bare
	// `--dry-run` parse to "checks"; fs.Changed tells it from the
	// untouched default.
	var dry *dryrun.Run
	if f := fs.Lookup(DryRunFlag); f != nil && fs.Changed(DryRunFlag) {
		on, err := parseDryRun(f.Value.String())
		if err != nil {
			return err
		}
		if on {
			dry = dryrun.New()
		}
	}

	if rt.Stdin == nil {
		rt.Stdin = os.Stdin
	}
	if rt.Stdout == nil {
		rt.Stdout = os.Stdout
	}
	if rt.Stderr == nil {
		rt.Stderr = os.Stderr
	}

	rt.Config = cfg
	rt.Profile = profile
	// The value test is unreachable while the empty --profile rejection
	// above stands, and nothing in the suite fails without it. It keeps
	// the field correct if that rejection is narrowed: an empty --profile
	// names no profile, so Profile comes from current_profile, and
	// calling that explicit would shut profileguard's repair carve-out.
	rt.ProfileExplicit = fs.Changed("profile") && profileFlag != ""
	rt.Output = &output.Renderer{
		Format: format,
		Color:  colorOK && output.IsText(format),
		Out:    os.Stdout,
		Err:    os.Stderr,
	}
	rt.Verbose = verbose
	rt.Debug = debug
	rt.DryRun = dry
	return nil
}
