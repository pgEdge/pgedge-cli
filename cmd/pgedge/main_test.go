package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/keychain"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestRunHelperProcess is not a real test: it is re-invoked as a
// subprocess by runSubprocess below so each scenario gets a fresh
// process. run() calls module.Register, which panics on a second
// in-process call ("duplicate registration of byoc"), so exercising
// more than one run() outcome requires isolating each call in its
// own process rather than looping over run() directly.
func TestRunHelperProcess(t *testing.T) {
	raw := os.Getenv("PGEDGE_RUN_HELPER_ARGS")
	if raw == "" {
		t.Skip("helper process test; invoked via runSubprocess")
	}
	os.Args = strings.Split(raw, "\x1f")
	newKeychain = func() keychain.Store { return nil }
	os.Exit(run())
}

// runSubprocess re-executes this test binary, routing it into
// TestRunHelperProcess so run() is exercised for real (including its
// module.Register side effect) without corrupting the parent
// process's module registry for later cases.
//
// The child inherits os.Environ(), so any test whose outcome depends on
// a clean environment must call testsupport.ClearEnv(t) first: it
// neutralises the host variables (NO_COLOR, SHELL, XDG_CONFIG_HOME)
// that production code reads, and that list has one home, in
// internal/testsupport, because a local copy of it here was the fourth
// and would have been the next to drift. API URLs and the active
// profile read no environment variable, and ClearEnv clears the
// credential pair, so flags and the subprocess's own config file under
// HOME steer them.
func runSubprocess(t *testing.T, home string, args []string) (
	stdout, stderr string, exitCode int,
) {
	t.Helper()
	return runSubprocessStdin(t, home, "", args)
}

// runSubprocessStdin is runSubprocess with the child's stdin supplied.
// Commands that prompt — `starfleet auth login` reads a client ID and
// secret — otherwise block forever on an inherited terminal, or read EOF
// and fail for the wrong reason, which would make an exit-code
// assertion about them meaningless.
func runSubprocessStdin(t *testing.T, home, stdin string, args []string) (
	stdout, stderr string, exitCode int,
) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunHelperProcess$")
	cmd.Env = append(os.Environ(),
		"PGEDGE_RUN_HELPER_ARGS="+strings.Join(args, "\x1f"),
		"HOME="+home,
	)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	switch e := err.(type) {
	case nil:
		exitCode = 0
	case *exec.ExitError:
		exitCode = e.ExitCode()
	default:
		t.Fatalf("subprocess: %v", err)
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// writeStarfleetConfig writes a default-profile config.yaml at home's
// default path (~/.pgedge/cli/config.yaml) carrying the given cloud
// api_url/client_id/client_secret, for a subprocess test that needs to
// steer a profile's connection.
func writeStarfleetConfig(t *testing.T, home, apiURL, clientID, clientSecret string) {
	t.Helper()
	dir := filepath.Join(home, ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "current_profile: default\n" +
		"profiles:\n" +
		"  default:\n" +
		"    starfleet:\n" +
		"      api_url: " + apiURL + "\n" +
		"      client_id: " + clientID + "\n" +
		"      client_secret: " + clientSecret + "\n"
	if err := os.WriteFile(
		filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// writeProfilesConfig writes a config.yaml at home's default path
// naming the given profiles (each an empty section — no credentials
// needed for the unknown-profile guard tests) and current, which may
// be "" to omit current_profile entirely.
func writeProfilesConfig(t *testing.T, home, current string, profiles []string) {
	t.Helper()
	dir := filepath.Join(home, ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if current != "" {
		b.WriteString("current_profile: " + current + "\n")
	}
	b.WriteString("profiles:\n")
	for _, p := range profiles {
		b.WriteString("  " + p + ": {}\n")
	}
	if err := os.WriteFile(
		filepath.Join(dir, "config.yaml"), []byte(b.String()), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownProfileFlag is the behavioural matrix for one rule: an
// explicit --profile naming something that is not configured must fail
// exactly like `pgedge profile use <unknown>`, except for the commands
// that never read config or that create the named profile. Every case
// is hermetic: an isolated per-case HOME, no config or a fixture config
// this test writes itself, and no network beyond a local httptest stub
// or a closed port.
func TestUnknownProfileFlag(t *testing.T) {
	testsupport.ClearEnv(t)

	t.Run("unknown profile value on starfleet doctor is exit 1, no prod URL", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		stdout, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "starfleet", "doctor"})
		if code != 1 {
			t.Errorf("exit = %d, want 1; stderr=%q", code, stderr)
		}
		const want = `unknown profile "bogus" — configured profiles: alpha`
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want substring %q", stderr, want)
		}
		if strings.Contains(stdout, "api.pgedge.com") {
			t.Errorf("stdout mentions api.pgedge.com — a request was "+
				"rendered for a rejected profile:\n%s", stdout)
		}
	})

	t.Run("unknown profile value on profile list is exit 1, no phantom row", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		stdout, _, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "profile", "list"})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if strings.Contains(stdout, "bogus") {
			t.Errorf("stdout rendered a phantom row for bogus:\n%s", stdout)
		}
	})

	t.Run("unknown profile value on top-level doctor is exit 1", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, _, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "doctor"})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("known profile value on profile list is exit 0, one active row", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		stdout, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "alpha", "profile", "list"})
		if code != 0 {
			t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "alpha") ||
			!strings.Contains(stdout, "yes") {
			t.Errorf("stdout = %q, want an active alpha row", stdout)
		}
	})

	t.Run("exempt commands still work with an unknown profile value", func(t *testing.T) {
		cases := []struct {
			name string
			args []string
		}{
			{"version", []string{"pgedge", "--profile", "bogus", "version"}},
			{"llms", []string{"pgedge", "--profile", "bogus", "llms"}},
			{"completion zsh", []string{"pgedge", "--profile", "bogus",
				"completion", "zsh"}},
			{"help", []string{"pgedge", "--profile", "bogus", "help"}},
			{"--help", []string{"pgedge", "--profile", "bogus", "--help"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				writeProfilesConfig(t, home, "alpha", []string{"alpha"})
				_, stderr, code := runSubprocess(t, home, tc.args)
				if code != 0 {
					t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
				}
			})
		}
	})

	// The exemption above is from the UNKNOWN-profile rule only. An
	// EMPTY value is a usage error earlier than any of it, so the
	// same five commands split: only `--help` survives, because cobra
	// short-circuits it before setupRuntime runs.
	t.Run("exempt commands are not exempt from an EMPTY profile", func(t *testing.T) {
		cases := []struct {
			name string
			args []string
			want int
		}{
			{"version", []string{"pgedge", "--profile", "", "version"}, 2},
			{"llms", []string{"pgedge", "--profile", "", "llms"}, 2},
			{"completion zsh", []string{"pgedge", "--profile", "",
				"completion", "zsh"}, 2},
			{"help", []string{"pgedge", "--profile", "", "help"}, 2},
			{"--help", []string{"pgedge", "--profile", "", "--help"}, 0},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				writeProfilesConfig(t, home, "alpha", []string{"alpha"})
				_, stderr, code := runSubprocess(t, home, tc.args)
				if code != tc.want {
					t.Errorf("exit = %d, want %d; stderr=%q",
						code, tc.want, stderr)
				}
			})
		}
	})

	t.Run("cp config set creates a brand-new profile", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "brand-new", "controlplane", "config",
				"set", "--base-url", "http://127.0.0.1:1"})
		if code != 0 {
			t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".pgedge", "cli", "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "brand-new") {
			t.Errorf("config file does not carry the new profile:\n%s", raw)
		}
	})

	t.Run("starfleet auth login creates a brand-new profile", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		apiURL := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
			http.StatusOK, "{}"))
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "brand-new", "starfleet", "auth",
				"login", "--client-id", "id", "--client-secret", "s",
				"--api-url", apiURL})
		if code != 0 {
			t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".pgedge", "cli", "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "brand-new") {
			t.Errorf("config file does not carry the new profile:\n%s", raw)
		}
	})

	t.Run("zero-config machine: default and config-free commands work", func(t *testing.T) {
		// "doctor" belongs in this matrix too, but doctor's RunE
		// builds the production selfupdate.Source ladder (a live
		// api.github.com call with no flag/env override reachable from a
		// subprocess) whenever DoctorDeps.Source is nil, unconditionally
		// — running it here would violate "no test dials a real API".
		// "profile show" exercises the identical guard path (an
		// explicit --profile default reaching a non-exempt command's
		// RunE on a fresh machine) without touching the network.
		cases := [][]string{
			{"pgedge", "--profile", "default", "profile", "show"},
			{"pgedge", "--profile", "default", "profile", "list"},
			{"pgedge", "--profile", "default", "version"},
		}
		for _, args := range cases {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				home := t.TempDir() // no config file at all
				_, stderr, code := runSubprocess(t, home, args)
				if code != 0 {
					t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
				}
			})
		}
	})

	t.Run("zero-config machine: an unknown profile value still fails", func(t *testing.T) {
		home := t.TempDir() // no config file at all
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "prd", "doctor"})
		if code != 1 {
			t.Errorf("exit = %d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "no profiles are configured") {
			t.Errorf("stderr = %q, want the no-profiles message", stderr)
		}
	})

	// An empty value names no profile, and being handed the active one
	// instead means being handed another TENANT, so it is refused
	// the way an empty --config is. See TestUnknownCurrentProfile for
	// what that cost and why the repair route survives it.
	t.Run("explicit empty --profile is a usage error", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "", "profile", "list"})
		if code != 2 {
			t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "given an empty value") {
			t.Errorf("stderr = %q, want the empty-value message", stderr)
		}
	})

	// --profile exists, and "bogus" is simply not a configured
	// profile. This subtest is the standing citation for "a router
	// miss is exit 1", and says nothing about an unknown flag: that
	// case is pinned by its own sibling below.
	//
	// The behaviour here is right and worth keeping: an unknown
	// profile is a well-formed reference to something absent, which is
	// exit 1 rather than a usage error, per llms.txt's exception list.
	t.Run("unknown profile VALUE on a group router is exit 1, not a help dump", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		stdout, _, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "starfleet", "tenant"})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if strings.Contains(stdout, "Usage:") {
			t.Errorf("stdout printed a help dump instead of failing:\n%s",
				stdout)
		}
	})

	// The sibling the old name implied. An unknown FLAG on the same
	// group router is a usage error, exit 2 -- a different class from
	// the unknown profile value above, and the pair is what makes the
	// distinction readable rather than implied.
	//
	// The promotion itself is pinned in several places already:
	// TestRunGroupStrayArgumentIsUsageError drives it on four group
	// routers and TestRun covers an unknown global flag. What nothing
	// covered is an unknown FLAG at a NON-ROOT group router -- the
	// existing rows use the root and two leaves.
	//
	// It carries no --profile at all, deliberately: adding one would
	// make the exit code ambiguous between the two causes.
	t.Run("unknown FLAG on the same group router is exit 2", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "tenant", "--nosuchflag"})
		if code != 2 {
			t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "unknown flag") {
			t.Errorf("stderr does not name the unknown flag: %q",
				stderr)
		}
	})

	// The input is an unknown profile VALUE, not an unknown flag.
	t.Run("unknown profile value on profile use is exit 1: not exempt", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, _, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "profile", "use",
				"alpha"})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("profile use bogus: byte-identical parity message", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		_, flagStderr, flagCode := runSubprocess(t, home,
			[]string{"pgedge", "--profile", "bogus", "starfleet", "doctor"})
		if flagCode != 1 {
			t.Fatalf("--profile bogus exit = %d, want 1", flagCode)
		}
		_, useStderr, useCode := runSubprocess(t, home,
			[]string{"pgedge", "profile", "use", "bogus"})
		if useCode != 1 {
			t.Fatalf("profile use bogus exit = %d, want 1", useCode)
		}
		flagLine := unknownProfileLine(t, flagStderr)
		useLine := unknownProfileLine(t, useStderr)
		if flagLine != useLine {
			t.Errorf("message mismatch:\n--profile: %q\nprofile use: %q",
				flagLine, useLine)
		}
	})
}

// unknownProfileLine extracts the `unknown profile "..." — ...` line
// from a captured stderr, for the byte-identical parity assertion.
func unknownProfileLine(t *testing.T, stderr string) string {
	t.Helper()
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, `unknown profile "bogus"`) {
			return line
		}
	}
	t.Fatalf("no 'unknown profile' line in stderr: %q", stderr)
	return ""
}

// TestCompleteIgnoresUnknownProfile pins the deliberate exemption
// noted at the wrapProfileGuard call site: cobra adds __complete
// lazily inside Execute, after the tree has been wrapped, so shell
// completion for a half-typed --profile can never hard-fail.
func TestCompleteIgnoresUnknownProfile(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	writeProfilesConfig(t, home, "alpha", []string{"alpha"})
	stdout, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "__complete", "--profile", "bogus",
			"profile", ""})
	if code != 0 {
		t.Errorf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"list", "show", "use"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("completion output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRun(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantExit int
		wantOut  string
		wantErr  string
	}{
		{
			name:     "version",
			args:     []string{"pgedge", "version"},
			wantExit: 0,
			wantOut:  "dev",
		},
		{
			name:     "output json version",
			args:     []string{"pgedge", "--output", "json", "version"},
			wantExit: 0,
			wantOut:  "dev",
		},
		{
			name:     "unknown command",
			args:     []string{"pgedge", "totally-bogus-command"},
			wantExit: 2,
			wantErr:  "unknown command",
		},
		{
			name:     "unknown global flag",
			args:     []string{"pgedge", "--nonexistent-flag"},
			wantExit: 2,
			wantErr:  "unknown flag",
		},
		{
			name:     "unknown subcommand flag",
			args:     []string{"pgedge", "controlplane", "config", "set", "--nope"},
			wantExit: 2,
			wantErr:  "unknown flag",
		},
		{
			name:     "missing required arg",
			args:     []string{"pgedge", "controlplane", "database", "get"},
			wantExit: 2,
			wantErr:  "arg",
		},
		{
			name:     "extra arg on no-arg command",
			args:     []string{"pgedge", "completion", "zsh", "extra"},
			wantExit: 2,
			wantErr:  "unknown command",
		},
		{
			name:     "invalid output format",
			args:     []string{"pgedge", "-o", "toml", "version"},
			wantExit: 2,
			wantErr:  "unsupported output format",
		},
		{
			name:     "valid format still succeeds",
			args:     []string{"pgedge", "-o", "yaml", "version"},
			wantExit: 0,
		},
	}
	testsupport.ClearEnv(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, stderr, code := runSubprocess(t, home, tc.args)
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d (stdout=%q stderr=%q)",
					code, tc.wantExit, stdout, stderr)
			}
			if tc.wantOut != "" && !strings.Contains(stdout, tc.wantOut) {
				t.Errorf("stdout = %q, want substring %q", stdout, tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tc.wantErr)
			}
		})
	}
}

// TestRunByocAuthErrorExitCode exercises the real failure path
// described by Finding 1: a byoc command that fails to authenticate
// must surface *byoccmd.ExitError's Code() (5, ExitAuth) as the
// process exit code, not the generic fallback of 1. The stub server's
// URL and a client ID/secret pair are written into the subprocess's
// own config file, so the conn.Resolve token exchange behind
// newAPIClient fails exactly the way a real auth failure would.
func TestRunByocAuthErrorExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		}))
	defer srv.Close()

	home := t.TempDir()
	testsupport.ClearEnv(t)
	writeStarfleetConfig(t, home, srv.URL, "test-id", "test-secret")

	_, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "starfleet", "byoc", "cluster", "list"})
	if code != 5 {
		t.Errorf("exit = %d, want 5 (ExitAuth); stderr=%q",
			code, stderr)
	}
	if !strings.Contains(stderr, "authentication failed") {
		t.Errorf("stderr = %q, want substring %q",
			stderr, "authentication failed")
	}
}

// TestExitCodeContract drives the whole exit-code vocabulary through
// real child processes. It is the gate that matters most for this
// contract, because every layer can be individually correct while the
// composition is not: internal/clitest checks that the four packages'
// constants agree, and this checks what a caller's `$?` actually is.
//
// The four usage rows are the point. They used to disagree with each
// other — a missing argument exited 2 while a missing required flag
// exited 1, because cobra validates arguments before its pre-run hooks
// and required flags after them, and main.go's sentinel sat in between.
// Any regression in markRan puts that split straight back, and only an
// end-to-end run can see it.
//
// Every case is hermetic: an isolated HOME with no config, and
// --api-url on a closed local port for the rows that get as far as a
// request. Any PGEDGE_* var inherited from the developer's shell is
// inert here — the CLI reads no PGEDGE_* environment variable at all,
// so nothing short of a config file or a flag can steer these cases.
func TestExitCodeContract(t *testing.T) {
	const closedPort = "http://127.0.0.1:1"
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  int
	}{
		// --- 0: it worked ---
		{name: "success", args: []string{"version"}, want: 0},
		{name: "bare help", args: []string{"help"}, want: 0},
		{
			// The whole point of tightening `help` is that it must still
			// resolve every real path, including a leaf several levels
			// down. A help command that rejected these would trade one
			// wrong exit code for a worse one.
			name: "help for a leaf command",
			args: []string{"help", "starfleet", "auth", "login"},
			want: 0,
		},
		{
			name: "help for a group",
			args: []string{"help", "starfleet", "byoc", "cluster"},
			want: 0,
		},

		// --- 2: the command was malformed ---
		{
			name: "missing required flag",
			args: []string{"starfleet", "client", "create"},
			want: 2,
		},
		{
			name: "missing positional argument",
			args: []string{"starfleet", "tenant", "get"},
			want: 2,
		},
		{
			name: "unknown flag",
			args: []string{"starfleet", "client", "create", "--nosuchflag"},
			want: 2,
		},
		{
			name: "unknown command",
			args: []string{"starfleet", "zzz-no-such-thing"},
			want: 2,
		},
		{
			name: "stray argument on a group",
			args: []string{"starfleet", "byoc", "cluster", "zzz-no-such-thing"},
			want: 2,
		},
		{
			name: "unsupported output format",
			args: []string{"--output", "nonsense", "version"},
			want: 2,
		},
		{
			// cobra's built-in help command uses Run, not RunE, and
			// prints "Unknown help topic" straight to output before
			// exiting 0 — a diagnostic with a success code, which is the
			// one combination a script cannot act on.
			name: "unknown help topic",
			args: []string{"help", "zzz-no-such-thing"},
			want: 2,
		},
		{
			// Worse than the row above, and the reason `help` needed its
			// own command rather than a wrapper: cobra's Find discards
			// the args it could not consume, so this printed `starfleet`'s
			// help with NO diagnostic at all and exited 0. A typo in the
			// second word was completely invisible.
			name: "stray argument after a valid help topic",
			args: []string{"help", "starfleet", "zzz-no-such-thing"},
			want: 2,
		},
		{
			// Half a credential pair is the operator mistyping, not a
			// credential the server refused. It reported ExitAuth until
			// this change and looked right only because ExitAuth was 2.
			name: "half a credential pair",
			args: []string{"starfleet", "tenant", "list",
				"--client-id", "only-an-id", "--api-url", closedPort},
			want: 2,
		},

		// --- 5: authentication ---
		{
			name: "no credentials anywhere",
			args: []string{"starfleet", "tenant", "list",
				"--api-url", closedPort},
			want: 5,
		},
		{
			// `auth login` wrapped this with fmt.Errorf, so nothing
			// implementing coder reached cli.ExitCode and it exited 1
			// while every resource command exited ExitAuth for the same
			// rejection.
			name:  "auth login cannot authenticate",
			args:  []string{"starfleet", "auth", "login", "--api-url", closedPort},
			stdin: "some-id\nsome-secret\n",
			want:  5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testsupport.ClearEnv(t)
			home := t.TempDir()
			_, stderr, code := runSubprocessStdin(t, home, tc.stdin,
				append([]string{"pgedge"}, tc.args...))
			if code != tc.want {
				t.Errorf("exit = %d, want %d; stderr=%q",
					code, tc.want, stderr)
			}
		})
	}
}

// TestRunGroupStrayArgumentIsUsageError is the one end-to-end proof,
// complementing internal/clitest's structural gate
// (TestCommandTreeConformance), which asserts every group command's
// Args+RunE pair without ever executing a real binary. That gate is
// cheap and names ~30 commands individually, which is exactly what
// makes it the wrong tool to prove the mapping below: it cannot see
// past internal/cli.ExitCode into main.go's ranRunE promotion
// (cli.go:96-122) that turns a plain cobra parse-error (exit 1) into
// exit 2. A regression there would leave the structural gate green
// while every one of these commands silently regressed to exit 1 (or
// worse, cobra's flag.ErrHelp path regressed and it silently returned
// to exit 0) — so this one subprocess case is what actually exercises
// that mapping, for a representative sample rather than all ~30.
//
// testsupport.ClearEnv is required, not optional: runSubprocess hands
// the child os.Environ(), and a developer's shell commonly carries
// NO_COLOR or a non-default SHELL — without clearing first, either
// could change this subprocess's output or behavior.
// Each depth is also run with each help flag appended, which is the
// other half of the contract: without it every `--help` row below
// exits 0 having printed the PARENT's help, so `<cmd> --help` — the
// conventional way to probe whether a command exists — answers yes
// for commands that do not. Why cobra skipped the group's
// Args: cobra.NoArgs is stated once, in cli.StrayArgsOnHelp, and not
// restated here.
func TestRunGroupStrayArgumentIsUsageError(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "starfleet module root",
			args: []string{"pgedge", "starfleet", "zzz-no-such-thing"},
		},
		{
			name: "controlplane module root",
			args: []string{"pgedge", "controlplane", "zzz-no-such-thing"},
		},
		{
			name: "byoc resource group",
			args: []string{"pgedge", "starfleet", "byoc", "cluster",
				"zzz-no-such-thing"},
		},
		{
			name: "managed resource group",
			args: []string{"pgedge", "starfleet", "managed", "database",
				"zzz-no-such-thing"},
		},
	}
	trailing := []struct {
		name string
		args []string
	}{
		{name: "bare"},
		{name: "--help", args: []string{"--help"}},
		{name: "-h", args: []string{"-h"}},
	}
	testsupport.ClearEnv(t)
	for _, tc := range cases {
		for _, tr := range trailing {
			t.Run(tc.name+" "+tr.name, func(t *testing.T) {
				home := t.TempDir()
				args := append(append([]string{}, tc.args...), tr.args...)
				stdout, stderr, code := runSubprocess(t, home, args)
				if code != 2 {
					t.Errorf("exit = %d, want 2 (stdout=%q stderr=%q)",
						code, stdout, stderr)
				}
				if !strings.Contains(stderr, "unknown command") {
					t.Errorf("stderr = %q, want substring "+
						"%q", stderr, "unknown command")
				}
				// The exit code alone is not the whole fix. A caller
				// reading stdout must not be handed the parent's help
				// as though the lookup had succeeded.
				if strings.Contains(stdout, "Usage:") {
					t.Errorf("stdout printed a help dump:\n%s", stdout)
				}
			})
		}
	}
}

// TestRunHelpStillWorks is the negative-control set for the --help
// fix. It sits on cobra's --help path, which is also the path every legitimate
// help lookup and every shell completion takes, and it is reached
// before the Runtime exists — so nothing here may start failing.
func TestRunHelpStillWorks(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "root --help",
			args: []string{"pgedge", "--help"},
			want: "Usage:",
		},
		{
			name: "module root --help",
			args: []string{"pgedge", "controlplane", "--help"},
			want: "Usage:",
		},
		{
			name: "resource group --help",
			args: []string{"pgedge", "starfleet", "byoc", "cluster", "--help"},
			want: "Usage:",
		},
		{
			name: "leaf --help",
			args: []string{"pgedge", "starfleet", "auth", "login", "--help"},
			want: "Usage:",
		},
		{
			name: "leaf -h",
			args: []string{"pgedge", "starfleet", "auth", "login", "-h"},
			want: "Usage:",
		},
		{
			name: "help command",
			args: []string{"pgedge", "help"},
			want: "Usage:",
		},
		{
			name: "help command with a path",
			args: []string{"pgedge", "help", "starfleet", "byoc", "cluster"},
			want: "Usage:",
		},
		{
			// A leaf that takes a positional argument: its ExactArgs(1)
			// must not fire on the help path, where the positional is
			// absent by design.
			name: "help for a leaf that takes an argument",
			args: []string{"pgedge", "help", "starfleet", "tenant", "get"},
			want: "Usage:",
		},
		{
			name: "leaf that takes an argument, --help",
			args: []string{"pgedge", "starfleet", "tenant", "get", "--help"},
			want: "Usage:",
		},
		{
			name: "shell completion of a group",
			args: []string{"pgedge", "__complete", "starfleet", ""},
			want: "byoc",
		},
		{
			// __complete's own trailing word is a stray positional to
			// every Args validator in the tree, so a fix that fired
			// outside the help path would break completion outright.
			name: "shell completion of a partial word",
			args: []string{"pgedge", "__complete", "starfleet", "by"},
			want: "byoc",
		},
		{
			name: "version",
			args: []string{"pgedge", "version"},
			want: "dev",
		},
	}
	testsupport.ClearEnv(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, stderr, code := runSubprocess(t, home, tc.args)
			if code != 0 {
				t.Errorf("exit = %d, want 0 (stderr=%q)", code, stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout = %q, want substring %q",
					stdout, tc.want)
			}
		})
	}
}

func TestRunBadConfigFile(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	badConfig := filepath.Join(home, "bad.yaml")
	if err := os.WriteFile(
		badConfig, []byte("not: [valid: yaml"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "--config", badConfig, "version"})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "Error:") {
		t.Errorf("stderr = %q, want Error: prefix", stderr)
	}
}

// TestEnvCredentialsAreNeverResolved is the end-to-end guard for the
// profile-only-config change, driven through a real child process so it
// covers the wiring internal/auth's own tests cannot: credential
// resolution runs inside run(), and a revert there would be invisible to
// a package-level test.
//
// The CLI reads no PGEDGE_* environment variable at all — not even the
// real, once-live names. Credentials, API URLs and the active profile
// come only from flags and the config file under HOME. This sets the
// removed names to junk values and asserts the command fails with the
// local "no credentials found" message rather than an authentication
// attempt against the bogus API URL: if the env pair were still being
// resolved into credentials, the CLI would instead try to dial
// never.example and fail differently (or hang), not report no
// credentials at all.
//
// `starfleet tenant list` reaches credential resolution and fails there,
// before any request is built, so a clean revert of both env reads
// needs no stub server and no closed-port trick — the bogus
// --api-url is never dialled if this test passes.
//
// But a PARTIAL revert is the failure mode this guards against:
// internal/auth's credential resolution and internal/*/conn's
// ResolveAPIURL are different packages, restorable independently. If
// only the credential env read came back (PGEDGE_ACCOUNT_CLIENT_ID/
// _SECRET), ResolveAPIURL would still ignore PGEDGE_ACCOUNT_API_URL
// and return the real DefaultAPIURL — and this test, with no --api-url
// flag of its own, would let junk-id/junk-secret sail past credential
// resolution and dial the real https://api.pgedge.com. --api-url on a
// closed local port is the fuse: it fails the request instantly and
// locally regardless of which revert half landed, so a partial revert
// still cannot reach the network, and the assertions below still tell
// the two outcomes apart (no-credentials vs. a dial attempt).
func TestEnvCredentialsAreNeverResolved(t *testing.T) {
	const closedPort = "http://127.0.0.1:1"
	testsupport.ClearEnv(t)
	t.Setenv("PGEDGE_ACCOUNT_CLIENT_ID", "junk-id")
	t.Setenv("PGEDGE_ACCOUNT_CLIENT_SECRET", "junk-secret")
	t.Setenv("PGEDGE_ACCOUNT_API_URL", "https://never.example")
	home := t.TempDir()

	_, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "starfleet", "tenant", "list",
			"--api-url", closedPort})
	if code == 0 {
		t.Fatalf("exit = 0 with only PGEDGE_ACCOUNT_* set; stderr=%q",
			stderr)
	}
	if !strings.Contains(stderr, "no credentials found") {
		t.Errorf("stderr = %q, want the no-credentials message", stderr)
	}
}

// writeTwoProfileStarfleetConfig writes a config.yaml with two profiles,
// "default" (current) and "other", each pointing at its own cloud
// api_url. It is the fixture for the tenant-retarget regression: two
// profiles must resolve to two distinguishable destinations so a test
// can tell which one an outgoing request actually reached.
func writeTwoProfileStarfleetConfig(t *testing.T, home, defaultURL, otherURL string) {
	t.Helper()
	dir := filepath.Join(home, ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "current_profile: default\n" +
		"profiles:\n" +
		"  default:\n" +
		"    starfleet:\n" +
		"      api_url: " + defaultURL + "\n" +
		"      client_id: default-id\n" +
		"      client_secret: default-secret\n" +
		"  other:\n" +
		"    starfleet:\n" +
		"      api_url: " + otherURL + "\n" +
		"      client_id: other-id\n" +
		"      client_secret: other-secret\n"
	if err := os.WriteFile(
		filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// newFlaggingAuthedServer is testsupport.NewAuthedServer with a hit
// counter on the resource handler, so a test can tell which of two
// otherwise-identical stub servers an outgoing request actually
// reached.
func newFlaggingAuthedServer(t *testing.T, hit *int32) string {
	t.Helper()
	return testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hit, 1)
		testsupport.JSONHandler(http.StatusOK, "[]")(w, r)
	})
}

// TestGlobalFlagAsAnotherFlagsValue is the regression matrix for one
// rule: a global flag token that cobra binds as the VALUE of another flag —
// one registered on a leaf command, elsewhere in the tree — must have
// no effect on global behaviour. Deriving the Runtime from cobra's own
// parsed flags (rather than a pre-cobra scan of os.Args) is what makes
// this true: main.go's peekFlag/hasFlag used to scan raw argv and
// would match these tokens regardless of what cobra itself did with
// them.
func TestGlobalFlagAsAnotherFlagsValue(t *testing.T) {
	testsupport.ClearEnv(t)

	t.Run("--config consumed as --user-type's value", func(t *testing.T) {
		home := t.TempDir()
		badConfig := filepath.Join(home, "bad.yaml")
		if err := os.WriteFile(
			badConfig, []byte("not: [valid: yaml"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "managed", "database", "get", "X",
				"--user-type", "--config=" + badConfig})
		// parseUserType validates --user-type client-side,
		// before any config or client is touched, so this is a usage
		// error (2) naming the whole swallowed token — not a config
		// parse error (1), which is what the old pre-parse produced by
		// separately recognising "--config=..." in raw argv.
		if code != 2 {
			t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "unknown user type") {
			t.Errorf("stderr = %q, want an unknown-user-type error",
				stderr)
		}
		if !strings.Contains(stderr, "--config="+badConfig) {
			t.Errorf("stderr = %q, want it to name the swallowed token %q",
				stderr, "--config="+badConfig)
		}
	})

	t.Run("--profile consumed as --client-id's value never retargets the tenant", func(t *testing.T) {
		home := t.TempDir()
		var hitDefault, hitOther int32
		defaultURL := newFlaggingAuthedServer(t, &hitDefault)
		otherURL := newFlaggingAuthedServer(t, &hitOther)
		writeTwoProfileStarfleetConfig(t, home, defaultURL, otherURL)

		// "--profile=other" is ONE token here, entirely consumed as
		// --client-id's value: cobra never parses "--profile" as the
		// global flag at all. The old peek scanned raw argv for a
		// "--profile=" prefix independently of what cobra did with the
		// token, and would retarget the request at the "other"
		// tenant's server regardless.
		_, stderr, _ := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "tenant", "list",
				"--client-id", "--profile=other", "--client-secret", "s"})
		if atomic.LoadInt32(&hitOther) != 0 {
			t.Errorf("request reached the 'other' tenant's server; "+
				"--profile=other, consumed as --client-id's value, "+
				"still retargeted the tenant; stderr=%q", stderr)
		}
		if atomic.LoadInt32(&hitDefault) == 0 {
			t.Errorf("request never reached the default tenant's "+
				"server; stderr=%q", stderr)
		}
	})

	t.Run("-o consumed as --client-id's value does not gate output format", func(t *testing.T) {
		home := t.TempDir()
		srv := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, "[]"))
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "tenant", "list",
				"--client-id", "-o", "--client-secret", "s",
				"--api-url", srv})
		if code == 2 && strings.Contains(stderr, "unsupported output format") {
			t.Errorf("stderr = %q, -o's swallowed value was still "+
				"gated as the format flag", stderr)
		}
	})

	t.Run("--debug consumed as --client-id's value produces no wire dump", func(t *testing.T) {
		home := t.TempDir()
		srv := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, "[]"))
		_, stderr, _ := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "tenant", "list",
				"--client-id", "--debug", "--client-secret", "s",
				"--api-url", srv})
		if strings.Contains(stderr, "> POST") || strings.Contains(stderr, "> GET") {
			t.Errorf("stderr = %q, want no wire dump: --debug's token "+
				"was consumed as --client-id's value, not parsed as "+
				"the global flag", stderr)
		}
	})

	t.Run("--no-color and --verbose consumed as another flag's value", func(t *testing.T) {
		home := t.TempDir()
		srv := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, "[]"))
		_, stderr, _ := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "tenant", "list",
				"--client-id", "--verbose", "--client-secret", "--no-color",
				"--api-url", srv})
		if strings.Contains(stderr, "> GET") || strings.Contains(stderr, "> POST") {
			t.Errorf("stderr = %q, --verbose's swallowed token must "+
				"not have enabled wire logging", stderr)
		}
	})
}

// TestTokensAfterTerminator pins the other half of that matrix: every
// global flag appearing after a `--` terminator is a positional argument, not
// a flag, and must have no effect on global behaviour. The old
// os.Args scan did not stop at `--`, so it would still see these
// tokens and act on them.
func TestTokensAfterTerminator(t *testing.T) {
	testsupport.ClearEnv(t)

	t.Run("--debug after -- produces no wire dump", func(t *testing.T) {
		home := t.TempDir()
		_, stderr, _ := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "managed", "database", "get",
				"--client-id", "id", "--client-secret", "sec",
				"--api-url", "http://127.0.0.1:1", "--", "--debug"})
		if strings.Contains(stderr, "> POST") || strings.Contains(stderr, "> GET") {
			t.Errorf("stderr = %q, want no wire dump: --debug appeared "+
				"after -- and must be treated as a positional argument",
				stderr)
		}
	})

	t.Run("global flags after -- are stray positionals, not flags", func(t *testing.T) {
		cases := []struct {
			name string
			args []string
		}{
			{"--debug", []string{"pgedge", "version", "--", "--debug"}},
			{"--verbose", []string{"pgedge", "version", "--", "--verbose"}},
			{"-v", []string{"pgedge", "version", "--", "-v"}},
			{"--no-color", []string{"pgedge", "version", "--", "--no-color"}},
			{"--profile", []string{"pgedge", "version", "--", "--profile", "x"}},
			{"--output", []string{"pgedge", "version", "--", "--output", "json"}},
			{"--config", []string{"pgedge", "version", "--", "--config", "/nope"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				_, stderr, code := runSubprocess(t, home, tc.args)
				// version is cobra.NoArgs: a real global flag would be
				// consumed by cobra's parser and version would exit 0
				// (dev). Exit 2, "not enough/too many args" or "unknown
				// command", is what proves the token landed as a bare
				// positional instead.
				if code != 2 {
					t.Errorf("exit = %d, want 2 (stray positional, not "+
						"a parsed global flag); stderr=%q", code, stderr)
				}
			})
		}
	})
}

// TestGlobalFlagPositions is the happy-path complement to the
// regression matrix above: every global flag, in every position cobra
// allows, must still work exactly as before. This also pins the two
// bonus fixes deriving the Runtime from cobra makes free: combined
// shorthand blocks (-vo=json) now apply both flags, where the old
// os.Args scan silently dropped them both.
func TestGlobalFlagPositions(t *testing.T) {
	testsupport.ClearEnv(t)

	cases := []struct {
		name    string
		args    []string
		wantOut string
	}{
		{
			name:    "--output before the module word",
			args:    []string{"pgedge", "--output", "json", "version"},
			wantOut: `"version"`,
		},
		{
			name:    "--output=value before the module word",
			args:    []string{"pgedge", "--output=json", "version"},
			wantOut: `"version"`,
		},
		{
			name:    "-o json shorthand",
			args:    []string{"pgedge", "-o", "json", "version"},
			wantOut: `"version"`,
		},
		{
			name:    "-o=json shorthand",
			args:    []string{"pgedge", "-o=json", "version"},
			wantOut: `"version"`,
		},
		{
			name:    "--output after the leaf command",
			args:    []string{"pgedge", "version", "--output", "json"},
			wantOut: `"version"`,
		},
		{
			name:    "repeated --output: last wins",
			args:    []string{"pgedge", "--output", "yaml", "--output", "json", "version"},
			wantOut: `"version"`,
		},
		{
			name:    "combined shorthand -vo=json (bonus fix)",
			args:    []string{"pgedge", "-vo=json", "version"},
			wantOut: `"version"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, stderr, code := runSubprocess(t, home, tc.args)
			if code != 0 {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			if !strings.Contains(stdout, tc.wantOut) {
				t.Errorf("stdout = %q, want substring %q", stdout, tc.wantOut)
			}
		})
	}

	// pflag's combined-shorthand parsing does not accept a
	// space-separated value for a block ending in a value-taking
	// flag ("-vo json" fails to route at all, on both the old and new
	// implementations — this is pflag's own syntax rule, not a
	// regression). Only the "=" form is valid combined-shorthand
	// syntax, which is what the case above exercises.
	t.Run("bool flags: bare, =true, =false", func(t *testing.T) {
		boolCases := []struct {
			name string
			args []string
		}{
			{"--verbose bare", []string{"pgedge", "--verbose", "version"}},
			{"-v bare", []string{"pgedge", "-v", "version"}},
			{"--verbose=false", []string{"pgedge", "--verbose=false", "version"}},
			{"--no-color=true", []string{"pgedge", "--no-color=true", "version"}},
			{"--debug=false", []string{"pgedge", "--debug=false", "version"}},
			{"--debug between module and leaf", []string{"pgedge", "--debug", "version"}},
		}
		for _, tc := range boolCases {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				stdout, stderr, code := runSubprocess(t, home, tc.args)
				if code != 0 {
					t.Errorf("exit = %d, want 0; stdout=%q stderr=%q",
						code, stdout, stderr)
				}
				if !strings.Contains(stdout, "dev") {
					t.Errorf("stdout = %q, want version output", stdout)
				}
			})
		}
	})
}

// TestSetupErrorIsNotPromoted restates TestRunBadConfigFile's contract
// at the new seam: a bad --config file is a runtime setup failure
// (cli.SetupError), not a cobra parse error, so it must stay exit 1
// and must not be promoted to exit 2 by the !ranRunE fallback in
// run()'s error branch.
func TestSetupErrorIsNotPromoted(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	badConfig := filepath.Join(home, "bad.yaml")
	if err := os.WriteFile(
		badConfig, []byte("not: [valid: yaml"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "--config", badConfig, "version"})
	if code != 1 {
		t.Errorf("exit = %d, want 1 (SetupError, not promoted to a "+
			"usage error); stderr=%q", code, stderr)
	}
}

// TestDryRunReportsAndExitsZero is the end-to-end proof of the whole
// mechanism: the report reaches stdout, the exit code is 0 even though
// the transport aborted the request with an error, and the write never
// arrived at the server.
//
// It runs through the real binary path (run(), the module registry, the
// generated client) because the risk it covers only exists there: the
// report is recovered from the Run rather than the error chain, and
// nothing between the transport and main() is required to preserve an
// error for it to work.
func TestDryRunReportsAndExitsZero(t *testing.T) {
	testsupport.ClearEnv(t)

	var writes atomic.Int64
	var tokenPosts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testsupport.TokenPath {
				tokenPosts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testsupport.TokenBody))
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writes.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c1","name":"ci"}`))
		}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	writeStarfleetConfig(t, home, srv.URL, "id", "secret")

	stdout, stderr, code := runSubprocess(t, home, []string{
		"pgedge", "starfleet", "client", "create",
		"--name", "ci", "--description", "CI runner", "--dry-run",
	})

	if code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s",
			code, stdout, stderr)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("server saw %d write(s); a dry run must send none", n)
	}
	// The reads-allowed half of the contract: authentication still
	// happens, so the dry run exercises the credential.
	if n := tokenPosts.Load(); n != 1 {
		t.Errorf("token endpoint hit %d times, want 1 — the carve-out "+
			"must let the OAuth POST through", n)
	}
	for _, want := range []string{
		"would send:", "POST ", "/account/v1/clients",
		"nothing was written; the server was not consulted and may " +
			"still reject this request.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\nstdout: %s\nstderr: %s",
				want, stdout, stderr)
		}
	}
	if !strings.Contains(stdout, `"name": "ci"`) {
		t.Errorf("report did not preview the body:\n%s", stdout)
	}
}

// TestDryRunOnReadOnlyVerbIsUsageError pins the scope decision: the flag
// is registered per mutating leaf, so on a read verb it is simply not a
// flag — exit 2, like any other typo.
func TestDryRunOnReadOnlyVerbIsUsageError(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	writeStarfleetConfig(t, home, "https://127.0.0.1:1", "id", "secret")

	_, stderr, code := runSubprocess(t, home, []string{
		"pgedge", "starfleet", "client", "list", "--dry-run",
	})
	if code != 2 {
		t.Errorf("exit = %d, want 2 (unknown flag)\nstderr: %s",
			code, stderr)
	}
	if !strings.Contains(stderr, "dry-run") {
		t.Errorf("stderr does not name the flag:\n%s", stderr)
	}
}

// TestDryRunServerValueNamesTheMissingCapability covers the reserved
// value: a kubectl or helm user typing --dry-run=server must learn why
// it cannot work here, not just that it is invalid.
func TestDryRunServerValueNamesTheMissingCapability(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	writeStarfleetConfig(t, home, "https://127.0.0.1:1", "id", "secret")

	_, stderr, code := runSubprocess(t, home, []string{
		"pgedge", "starfleet", "client", "create",
		"--name", "ci", "--description", "d", "--dry-run=server",
	})
	if code != 2 {
		t.Errorf("exit = %d, want 2\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "validate endpoint") {
		t.Errorf("stderr does not explain why:\n%s", stderr)
	}
}

// dryRunStub answers every path with a permissive JSON body and counts
// non-safe requests. It is deliberately generous: these tests assert
// that NOTHING was written, so a stub that could not have served the
// write would prove nothing.
func dryRunStub(t *testing.T, writes *atomic.Int64) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead &&
				r.URL.Path != testsupport.TokenPath {
				writes.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case testsupport.TokenPath:
				_, _ = w.Write([]byte(testsupport.TokenBody))
			case "/v1/version":
				_, _ = w.Write([]byte(`{"version":"0.10.0"}`))
			default:
				_, _ = w.Write([]byte(
					`{"id":"7f3a5c1e-0000-4000-8000-000000000000",` +
						`"name":"mydb","services":[]}`))
			}
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDryRunSurvivesEveryErrorMappingFamily is the coverage that matters
// for the report reaching the user.
//
// The risk is not per-verb, it is per error-mapping helper: the report is
// recovered from the Run, but a command whose helper ALSO swallowed the
// abort error could still print a plausible result of its own before
// main() got there. Each row below is a different such helper —
// internal/controlplane/cmd.networkError (the one that formats its cause with %v
// into an ExitError carrying no Unwrap), and each cloud sub-tree's own
// fmt.Errorf/checkResponse pair — so the families are covered rather
// than sixty-five near-identical argv rows being maintained.
func TestDryRunSurvivesEveryErrorMappingFamily(t *testing.T) {
	const dbID = "7f3a5c1e-0000-4000-8000-000000000000"
	tests := []struct {
		name           string
		args           func(url string) []string
		starfleetCreds bool
		wantMethod     string
		wantPath       string
	}{
		{
			// internal/controlplane/cmd.networkError: the helper that destroys an
			// error chain, and the reason the Run is the channel.
			name: "cp delete (networkError family)",
			args: func(url string) []string {
				return []string{
					"pgedge", "controlplane", "database", "delete", dbID,
					"--force", "--base-url", url, "--dry-run",
				}
			},
			wantMethod: "DELETE",
			wantPath:   "/v1/databases/" + dbID,
		},
		{
			name: "byoc ssh-key create (byoc family)",
			args: func(string) []string {
				return []string{
					"pgedge", "starfleet", "byoc", "ssh-key", "create",
					"--name", "laptop", "--public-key", fixtureSSHPublicKey,
					"--dry-run",
				}
			},
			starfleetCreds: true,
			wantMethod:     "POST",
			wantPath:       "/byoc/v1/ssh-keys",
		},
		{
			name: "managed delete (managed family)",
			args: func(string) []string {
				return []string{
					"pgedge", "starfleet", "managed", "database", "delete",
					dbID, "--force", "--dry-run",
				}
			},
			starfleetCreds: true,
			wantMethod:     "DELETE",
			wantPath:       "/managed/v1/databases/" + dbID,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsupport.ClearEnv(t)
			var writes atomic.Int64
			url := dryRunStub(t, &writes)
			home := t.TempDir()
			if tc.starfleetCreds {
				writeStarfleetConfig(t, home, url, "id", "secret")
			}

			stdout, stderr, code := runSubprocess(t, home, tc.args(url))

			if code != 0 {
				t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s",
					code, stdout, stderr)
			}
			if n := writes.Load(); n != 0 {
				t.Errorf("server saw %d write(s); a dry run sends none",
					n)
			}
			for _, want := range []string{
				tc.wantMethod, tc.wantPath,
				"nothing was written; the server was not " +
					"consulted and may still reject this request.",
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q:\nstdout: %s\nstderr: %s",
						want, stdout, stderr)
				}
			}
		})
	}
}

// TestDryRunReportsChecksFromReads is the reads-allowed half of the
// semantics, end to end: a service deploy fetches the database first,
// and both the resolution and the create-vs-reconfigure intent guard
// must appear as passed checks in the report.
func TestDryRunReportsChecksFromReads(t *testing.T) {
	testsupport.ClearEnv(t)
	const dbID = "7f3a5c1e-0000-4000-8000-000000000000"
	var writes atomic.Int64
	url := dryRunStub(t, &writes)
	home := t.TempDir()
	writeStarfleetConfig(t, home, url, "id", "secret")

	stdout, stderr, code := runSubprocess(t, home, []string{
		"pgedge", "starfleet", "managed", "database", "mcp", "deploy", dbID,
		"--dry-run",
	})

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
			code, stdout, stderr)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("server saw %d write(s)", n)
	}
	for _, want := range []string{
		"checks passed:",
		// The argument IS the id here, so the note carries no
		// parenthetical — see databaseResolvedNote.
		"database " + dbID + " resolved",
		`no "mcp" service already deployed (deploy intent)`,
		"would send:",
		"nothing was written; the server was not consulted and may " +
			"still reject this request.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report missing %q:\nstdout: %s\nstderr: %s",
				want, stdout, stderr)
		}
	}
}

// TestDryRunOnFailedCheckShowsProgressAndRealExitCode covers the half of
// the contract that gating on Intercepted() alone made unreachable.
//
// When a client-side check refuses the command, the dry run never
// intercepts anything. It must still say how far it got — otherwise the
// ledger is collected and thrown away — and it must exit exactly what the
// real run would, which for a failed intent guard is 1.
//
// The partial report goes to STDERR: the command is exiting non-zero, and
// a report on stdout would read as a result.
func TestDryRunOnFailedCheckShowsProgressAndRealExitCode(t *testing.T) {
	testsupport.ClearEnv(t)
	const dbID = "7f3a5c1e-0000-4000-8000-000000000000"
	var writes atomic.Int64
	// A database that ALREADY has an mcp service, so `mcp deploy` is
	// refused by the create-vs-reconfigure guard.
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead &&
				r.URL.Path != testsupport.TokenPath {
				writes.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == testsupport.TokenPath {
				_, _ = w.Write([]byte(testsupport.TokenBody))
				return
			}
			_, _ = w.Write([]byte(`{"id":"` + dbID + `","name":"mydb",` +
				`"services":[{"service_id":"d751b884",` +
				`"service_type":"mcp","state":"running"}]}`))
		}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	writeStarfleetConfig(t, home, srv.URL, "id", "secret")

	stdout, stderr, code := runSubprocess(t, home, []string{
		"pgedge", "starfleet", "managed", "database", "mcp", "deploy", dbID,
		"--dry-run",
	})

	// Exactly what the real run would return for a refused intent guard.
	if code != 1 {
		t.Errorf("exit = %d, want 1 (the code the real run would "+
			"return)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("server saw %d write(s)", n)
	}
	// How far it got, on stderr.
	if !strings.Contains(stderr, "checks passed:") ||
		!strings.Contains(stderr, "resolved") {
		t.Errorf("stderr does not say how far the dry run got:\n%s",
			stderr)
	}
	// The guard's own error is still reported.
	if !strings.Contains(stderr, "already deployed") {
		t.Errorf("stderr lost the check's own error:\n%s", stderr)
	}
	// Nothing on stdout: this run produced no result.
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a failed dry run wrote to stdout, where it reads as "+
			"a result:\n%s", stdout)
	}
	// And it must claim NEITHER outcome line. It did not stop a write
	// (it never built one), and it is not a verb that sends nothing —
	// this one would have sent a PATCH had the guard passed. Both lines
	// would be false here.
	for _, wrong := range []string{
		"nothing was written; the server was not consulted and may " +
			"still reject this request.",
		"nothing would be sent; the server was not consulted.",
	} {
		if strings.Contains(stderr, wrong) {
			t.Errorf("partial report claims %q, which is not what "+
				"happened:\n%s", wrong, stderr)
		}
	}
}

// TestUnknownCurrentProfile is a behavioural matrix, and the
// counterpart to TestUnknownProfileFlag above: a hand-edited
// current_profile naming a profile that does not exist must be
// rejected the same way, everywhere, rather than silently resolving
// to the default production URL. Nothing in the CLI can write such a
// value — there is no `profile delete` — so a hand edit is the only
// way in, and until now it was the last remaining silent-prod-dial
// path.
//
// The two commands that keep working are the way back out. Without
// them the CLI would diagnose a state it gave the operator no way to
// leave except by editing YAML again.
func TestUnknownCurrentProfile(t *testing.T) {
	testsupport.ClearEnv(t)

	t.Run("unknown current_profile on starfleet doctor is exit 1", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		stdout, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "starfleet", "doctor"})
		if code != 1 {
			t.Errorf("exit = %d, want 1; stderr=%q", code, stderr)
		}
		// Byte-identical to the --profile path's text. The two are one
		// validator precisely so this assertion can be the same string.
		const want = `unknown profile "ghost" — configured profiles: alpha`
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want substring %q", stderr, want)
		}
		if strings.Contains(stdout, "api.pgedge.com") {
			t.Errorf("stdout mentions api.pgedge.com — the silent prod "+
				"dial this matrix guards:\n%s", stdout)
		}
	})

	t.Run("unknown current_profile on top-level doctor is exit 1", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		_, _, code := runSubprocess(t, home,
			[]string{"pgedge", "doctor"})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("profile list still runs and marks the row unresolved", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		stdout, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "profile", "list"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "alpha") {
			t.Errorf("profile list did not name the profile that DOES "+
				"exist, which is what makes it a repair route:\n%s",
				stdout)
		}
		// Scoped to the ghost ROW, not the whole table. `alpha: {}`
		// carries no api_url of its own, so it falls back to the
		// module-wide default and legitimately prints that same URL —
		// a table-wide assertion here fails on correct output.
		var ghostRow string
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "ghost") {
				ghostRow = line
			}
		}
		if ghostRow == "" {
			t.Fatalf("no row for the active profile:\n%s", stdout)
		}
		if !strings.Contains(ghostRow, "unresolved") {
			t.Errorf("row does not mark the broken profile: %q", ghostRow)
		}
		if strings.Contains(ghostRow, "https://api.pgedge.com") {
			t.Errorf("row still advertises the production URL for a "+
				"profile nothing can connect with: %q", ghostRow)
		}
	})

	t.Run("profile use repairs it", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "profile", "use", "alpha"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		// The repair has to actually land on disk, or the carve-out
		// buys nothing: the next command must succeed too.
		_, stderr2, code2 := runSubprocess(t, home,
			[]string{"pgedge", "profile", "list"})
		if code2 != 0 {
			t.Fatalf("exit = %d after repair, want 0; stderr=%q",
				code2, stderr2)
		}
		raw, err := os.ReadFile(
			filepath.Join(home, ".pgedge", "cli", "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "current_profile: alpha") {
			t.Errorf("config still does not name alpha:\n%s", raw)
		}
	})

	// This is the cost of refusing an empty --profile: a wrapper doing
	// `pgedge --profile="$VAR" ...` with VAR unset sends exactly this
	// and names no profile, so it is refused rather than treated as if
	// the flag were absent, and THAT wrapper does not repair — it gets
	// exit 2 and a message naming the fix.
	//
	// What matters is the repair being REACHABLE, and
	// the case below is that guarantee restated: with the flag omitted
	// the route is open, because the carve-out keys on no profile
	// having been named and omitting the flag still does that.
	t.Run("empty --profile= no longer repairs", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile=", "profile", "use", "alpha"})
		if code != 2 {
			t.Fatalf("exit = %d, want 2; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "omit the flag") {
			t.Errorf("stderr = %q, want the message to name the fix",
				stderr)
		}
	})

	t.Run("omitting --profile still repairs", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "profile", "use", "alpha"})
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
		}
		_, stderr2, code2 := runSubprocess(t, home,
			[]string{"pgedge", "profile", "list"})
		if code2 != 0 {
			t.Errorf("profile list: exit = %d, want 0; stderr=%q",
				code2, stderr2)
		}
	})

	// The other half, still true and now for a different reason:
	// --profile= is not a way to SKIP the unresolvable-current_profile
	// check. It used to resolve to current_profile and be rejected at
	// exit 1; it is now rejected earlier, at exit 2. Either way a
	// non-repair command under a ghost config does not succeed.
	t.Run("empty --profile= does not bypass the check", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "ghost", []string{"alpha"})
		_, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "--profile=", "starfleet", "doctor"})
		if code != 2 {
			t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
		}
	})

	// The carve-out waives the config path, never the flag path. If it
	// ever widened to a full exemption this is the case that catches
	// it: `profile use` would stop rejecting a typo'd --profile.
	t.Run("explicit unknown --profile is still rejected on repair commands", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		for _, args := range [][]string{
			{"pgedge", "--profile", "bogus", "profile", "list"},
			{"pgedge", "--profile", "bogus", "profile", "use", "alpha"},
		} {
			_, stderr, code := runSubprocess(t, home, args)
			if code != 1 {
				t.Errorf("%v: exit = %d, want 1; stderr=%q",
					args, code, stderr)
			}
		}
	})

	// The argument path, end to end.
	t.Run("profile show <unknown> is exit 1 with no report", func(t *testing.T) {
		home := t.TempDir()
		writeProfilesConfig(t, home, "alpha", []string{"alpha"})
		stdout, stderr, code := runSubprocess(t, home,
			[]string{"pgedge", "profile", "show", "ghost"})
		if code != 1 {
			t.Errorf("exit = %d, want 1; stderr=%q", code, stderr)
		}
		const want = `unknown profile "ghost" — configured profiles: alpha`
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want substring %q", stderr, want)
		}
		if strings.Contains(stdout, "api.pgedge.com") {
			t.Errorf("profile show fabricated a prod-pointing report:\n%s",
				stdout)
		}
	})
}

// TestRemoveStaleSwapBackupIsWindowsOnly pins both halves of the
// gate. Only the Windows swap ever writes a "<binary>.old"; on every
// other platform such a file is one the user made, so the cleanup
// must leave it exactly where it is.
func TestRemoveStaleSwapBackupIsWindowsOnly(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	old := exe + ".old"
	if err := os.WriteFile(old, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed stale .old file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(old) })

	removeStaleSwapBackup()

	_, statErr := os.Stat(old)
	if runtime.GOOS == "windows" {
		if !os.IsNotExist(statErr) {
			t.Errorf("stat %s after removeStaleSwapBackup: %v, want IsNotExist",
				old, statErr)
		}
		return
	}
	if statErr != nil {
		t.Errorf("removeStaleSwapBackup deleted a file it never wrote: %v",
			statErr)
	}
}

func TestRemoveStaleSwapBackupIsSilentWithNothingToRemove(t *testing.T) {
	// No ".old" file exists beside the test binary; this must not
	// panic or otherwise fail the test — it is a best-effort no-op.
	removeStaleSwapBackup()
}

// fixtureSSHPublicKey is a real, throwaway ed25519 public key. ssh-key
// create parses --public-key before sending, so a placeholder
// no longer reaches the request this case asserts on. A public key is
// not a secret, and this one has no counterpart private half anywhere.
const fixtureSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAII6SfQktsEUrCGgH1nfvXdjb3/W69viNpwNu+XIBjRLH fixture@pgedge"
