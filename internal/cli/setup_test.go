package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newSetupFlagSet builds a *pflag.FlagSet mirroring exactly what
// NewRootCmd registers on root.PersistentFlags(), then parses args
// against it — so fs.Changed(...) reports what cobra itself would
// report after parsing the same tokens.
func newSetupFlagSet(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("profile", "", "")
	fs.Bool("debug", false, "")
	fs.BoolP("verbose", "v", false, "")
	fs.StringP("output", "o", "text", "")
	fs.Bool("no-color", false, "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs
}

// writeSetupConfig writes a minimal config.yaml at dir/config.yaml
// carrying the given output format (empty omits the output section)
// and returns its path.
func writeSetupConfig(t *testing.T, dir, format string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	body := "current_profile: default\nprofiles:\n  default: {}\n"
	if format != "" {
		body += "output:\n  format: " + format + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetupRuntimeOutputFormat(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("default honours config output.format", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := writeSetupConfig(t, dir, "yaml")
		fs := newSetupFlagSet(t, "--config", cfgPath)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Output.Format != "yaml" {
			t.Errorf("Output.Format = %q, want yaml", rt.Output.Format)
		}
	})

	t.Run("explicit --output overrides config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := writeSetupConfig(t, dir, "yaml")
		fs := newSetupFlagSet(t, "--config", cfgPath, "--output", "json")
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Output.Format != "json" {
			t.Errorf("Output.Format = %q, want json", rt.Output.Format)
		}
	})

	t.Run("--output not given does not override config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := writeSetupConfig(t, dir, "yaml")
		// --output carries a registered default of "text"; the flag
		// set's GetString would return "text" even though the user
		// typed nothing. fs.Changed must be what gates the override.
		fs := newSetupFlagSet(t, "--config", cfgPath)
		if fs.Changed("output") {
			t.Fatal("test bug: --output reported Changed with no token")
		}
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Output.Format != "yaml" {
			t.Errorf("Output.Format = %q, want yaml (config untouched)",
				rt.Output.Format)
		}
	})

	t.Run("empty config, no flag defaults to text", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "empty.yaml")
		// An EMPTY file, not an absent one: a --config path that does
		// not exist is now an error (#239), and standing in for "no
		// preference" with a missing file would test the rejection
		// rather than the default.
		if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		fs := newSetupFlagSet(t, "--config", cfgPath)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Output.Format != "text" {
			t.Errorf("Output.Format = %q, want text", rt.Output.Format)
		}
	})

	// An empty --config is the same fail-open shape as a mistyped one:
	// `--config "$UNSET"` reaches here as "", which without this check
	// is indistinguishable from omitting the flag.
	t.Run("empty --config value is a usage error", func(t *testing.T) {
		fs := newSetupFlagSet(t, "--config", "")
		err := setupRuntime(&module.Runtime{}, fs)
		if err == nil {
			t.Fatal("want an error for an empty --config value")
		}
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Fatalf("err = %T, want *UsageError (exit 2)", err)
		}
		// The type alone would pass for any message at all, and this
		// error's job is to name the flag the caller got wrong.
		if !strings.Contains(usageErr.Msg, "--config") {
			t.Errorf("message %q should name --config", usageErr.Msg)
		}
	})

	// A mistyped --config used to fall through to the built-in default
	// profile, which points at production. It must fail instead, and
	// as a SetupError so main renders exit 1 rather than a parse error.
	t.Run("missing --config path is a setup error", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "nope.yaml")
		fs := newSetupFlagSet(t, "--config", cfgPath)
		err := setupRuntime(&module.Runtime{}, fs)
		if err == nil {
			t.Fatal("want an error for a missing --config path")
		}
		var setupErr *SetupError
		if !errors.As(err, &setupErr) {
			t.Errorf("err = %T, want *SetupError", err)
		}
		if !strings.Contains(err.Error(), cfgPath) {
			t.Errorf("error should name the path, got %q", err)
		}
	})

	t.Run("invalid format is a UsageError", func(t *testing.T) {
		fs := newSetupFlagSet(t, "--output", "toml")
		rt := &module.Runtime{}
		err := setupRuntime(rt, fs)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("setupRuntime error = %v, want *UsageError", err)
		}
		if !strings.Contains(ue.Msg, "toml") {
			t.Errorf("UsageError.Msg = %q, want it to name toml", ue.Msg)
		}
	})
}

func TestSetupRuntimeBadConfigPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badPath, []byte("not: [valid: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := newSetupFlagSet(t, "--config", badPath)
	rt := &module.Runtime{}
	err := setupRuntime(rt, fs)
	var se *SetupError
	if !errors.As(err, &se) {
		t.Fatalf("setupRuntime error = %v, want *SetupError", err)
	}
}

func TestSetupRuntimeProfileExplicit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("--profile given sets ProfileExplicit", func(t *testing.T) {
		fs := newSetupFlagSet(t, "--profile", "prod")
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if !rt.ProfileExplicit {
			t.Error("ProfileExplicit = false, want true")
		}
		if rt.Profile != "prod" {
			t.Errorf("Profile = %q, want prod", rt.Profile)
		}
	})

	t.Run("--profile absent leaves ProfileExplicit false", func(t *testing.T) {
		fs := newSetupFlagSet(t)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.ProfileExplicit {
			t.Error("ProfileExplicit = true, want false")
		}
	})

	// These asserted the opposite until #246, which answers this input
	// a third way #150 did not consider: reject it rather than pick
	// between explicit and absent. setupRuntime carries why; the
	// guarantee #246 had to preserve is the third case below.
	t.Run("--profile= (explicit empty) is a usage error", func(t *testing.T) {
		fs := newSetupFlagSet(t, "--profile=")
		rt := &module.Runtime{}
		err := setupRuntime(rt, fs)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("setupRuntime error = %v, want *UsageError", err)
		}
		// The type alone would pass for any message at all, including
		// --config's, and this error's job is to name the flag the
		// caller got wrong. #263 closed the same hole one commit
		// earlier in this file.
		if !strings.Contains(ue.Msg, "--profile") {
			t.Errorf("message %q should name --profile", ue.Msg)
		}
	})

	// The separate-argument form, which is what a wrapper actually
	// produces: `--profile "$P"` with P unset. It must be rejected
	// identically, or the rule holds for a spelling nobody types.
	t.Run("--profile \"\" is a usage error", func(t *testing.T) {
		fs := newSetupFlagSet(t, "--profile", "")
		rt := &module.Runtime{}
		err := setupRuntime(rt, fs)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("setupRuntime error = %v, want *UsageError", err)
		}
		if !strings.Contains(ue.Msg, "--profile") {
			t.Errorf("message %q should name --profile", ue.Msg)
		}
	})

	// The case #150 protected, restated as the guarantee #246 must not
	// break: with the flag OMITTED, a broken or absent current_profile
	// still resolves and still reports itself as not explicit, so the
	// repair carve-out still applies.
	t.Run("omitted --profile still resolves current_profile", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".pgedge", "cli")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
			[]byte("current_profile: alpha\nprofiles:\n  alpha: {}\n"),
			0o600); err != nil {
			t.Fatal(err)
		}
		fs := newSetupFlagSet(t)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Profile != "alpha" {
			t.Errorf("Profile = %q, want alpha", rt.Profile)
		}
		if rt.ProfileExplicit {
			t.Error("ProfileExplicit = true, want false")
		}
	})
}

func TestSetupRuntimeColor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// go test's stdout is never a terminal, so Color is false
	// regardless of NO_COLOR here; this pins the boolean short-circuit
	// still evaluates cleanly (no panic, no stale field) with NO_COLOR
	// set, without re-implementing term.IsTerminal's own semantics,
	// which are out of scope for #120 to touch.
	t.Setenv("NO_COLOR", "1")
	fs := newSetupFlagSet(t)
	rt := &module.Runtime{}
	if err := setupRuntime(rt, fs); err != nil {
		t.Fatalf("setupRuntime: %v", err)
	}
	if rt.Output.Color {
		t.Error("Output.Color = true, want false with NO_COLOR set")
	}
}

func TestSetupRuntimeStdioDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("nil stdio fields get OS defaults", func(t *testing.T) {
		fs := newSetupFlagSet(t)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Stdin != os.Stdin || rt.Stdout != os.Stdout || rt.Stderr != os.Stderr {
			t.Error("setupRuntime did not default nil stdio fields")
		}
	})

	t.Run("pre-set stdio fields are not clobbered", func(t *testing.T) {
		fs := newSetupFlagSet(t)
		var out, errOut bytes.Buffer
		rt := &module.Runtime{Stdout: &out, Stderr: &errOut}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.Stdout != &out || rt.Stderr != &errOut {
			t.Error("setupRuntime overwrote pre-set stdio fields")
		}
	})
}

func TestSetupRuntimeVerboseAndDebug(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fs := newSetupFlagSet(t, "--verbose", "--debug")
	rt := &module.Runtime{}
	if err := setupRuntime(rt, fs); err != nil {
		t.Fatalf("setupRuntime: %v", err)
	}
	if !rt.Verbose {
		t.Error("Verbose = false, want true")
	}
	if !rt.Debug {
		t.Error("Debug = false, want true")
	}
}

// newLeafFlagSet is newSetupFlagSet plus the per-leaf --dry-run flag, so
// the mutating-verb case can be exercised. MarkMutating registers the
// flag on a command's own FlagSet; setupRuntime sees it because cobra
// hands it the executing command's MERGED set.
func newLeafFlagSet(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("leaf", pflag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("profile", "", "")
	fs.Bool("debug", false, "")
	fs.BoolP("verbose", "v", false, "")
	fs.StringP("output", "o", "text", "")
	fs.Bool("no-color", false, "")
	cmd := &cobra.Command{Use: "create"}
	MarkMutating(cmd)
	fs.AddFlagSet(cmd.Flags())
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs
}

func TestSetupRuntimeDryRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfgPath := writeSetupConfig(t, dir, "")

	t.Run("absent from the flag set leaves it off", func(t *testing.T) {
		// Every read-only verb: the flag is not registered at all, and
		// GetString would report an error indistinguishable from "no
		// value given".
		fs := newSetupFlagSet(t, "--config", cfgPath)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.DryRun != nil {
			t.Error("DryRun set on a command with no --dry-run flag")
		}
	})

	t.Run("registered but untouched leaves it off", func(t *testing.T) {
		fs := newLeafFlagSet(t, "--config", cfgPath)
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.DryRun != nil {
			t.Error("DryRun set without the flag being given")
		}
	})

	t.Run("bare --dry-run turns it on", func(t *testing.T) {
		fs := newLeafFlagSet(t, "--config", cfgPath, "--dry-run")
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.DryRun == nil {
			t.Fatal("DryRun not set by the bare --dry-run form")
		}
		if rt.DryRun.Intercepted() {
			t.Error("a fresh Run reports an interception")
		}
	})

	t.Run("explicit checks turns it on", func(t *testing.T) {
		fs := newLeafFlagSet(t, "--config", cfgPath, "--dry-run=checks")
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err != nil {
			t.Fatalf("setupRuntime: %v", err)
		}
		if rt.DryRun == nil {
			t.Fatal("DryRun not set by --dry-run=checks")
		}
	})

	t.Run("server is refused as a usage error", func(t *testing.T) {
		fs := newLeafFlagSet(t, "--config", cfgPath, "--dry-run=server")
		rt := &module.Runtime{}
		err := setupRuntime(rt, fs)
		if err == nil {
			t.Fatal("want an error for --dry-run=server")
		}
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("error is %T, want *UsageError", err)
		}
		if rt.DryRun != nil {
			t.Error("DryRun set despite a rejected value")
		}
	})

	t.Run("an invalid value is refused", func(t *testing.T) {
		fs := newLeafFlagSet(t, "--config", cfgPath, "--dry-run=true")
		rt := &module.Runtime{}
		if err := setupRuntime(rt, fs); err == nil {
			t.Fatal("want an error for --dry-run=true")
		}
	})
}
