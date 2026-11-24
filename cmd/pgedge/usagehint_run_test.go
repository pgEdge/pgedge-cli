package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestUsageErrorShowsTheForm proves run() prints cli.UsageHint under
// every class of usage error, whichever layer raised it, and only on
// stderr.
func TestUsageErrorShowsTheForm(t *testing.T) {
	const id = "e5f6a7b8-c9d0-1234-efab-567890123456"
	cases := []struct {
		name string
		args []string
		want []string
	}{{
		name: "missing argument",
		args: []string{"controlplane", "database", "get"},
		want: []string{
			"Usage: pgedge controlplane database get <database_id>",
			"Example: pgedge controlplane database get ",
		},
	}, {
		name: "extra argument",
		args: []string{"starfleet", "managed", "database", "get", id, "x"},
		want: []string{"Usage: pgedge starfleet managed database get "},
	}, {
		name: "unknown flag",
		args: []string{"starfleet", "managed", "database", "list", "--bogus"},
		want: []string{
			"Usage: pgedge starfleet managed database list [flags]",
			"Example: pgedge starfleet managed database list",
		},
	}, {
		name: "missing required flag",
		args: []string{"starfleet", "managed", "database", "create"},
		want: []string{"Example: pgedge starfleet managed database " +
			"create --name "},
	}, {
		name: "mutually exclusive flags",
		args: []string{"starfleet", "managed", "database", "create",
			"--name", "x", "--size", "y", "--open", "--allow", "1.2.3.4"},
		want: []string{"Usage: pgedge starfleet managed database create"},
	}, {
		name: "invalid choice",
		args: []string{"version", "-o", "xml"},
		want: []string{"Usage: pgedge version"},
	}, {
		name: "malformed ID",
		args: []string{"starfleet", "managed", "database", "get", "nope"},
		want: []string{"Usage: pgedge starfleet managed database get "},
	}, {
		name: "unknown subcommand",
		args: []string{"starfleet", "managed", "database", "xyz"},
		want: []string{
			"Usage: pgedge starfleet managed database <command>",
			"Commands: allowlist, ",
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testsupport.ClearEnv(t)
			home := t.TempDir()
			writeProfilesConfig(t, home, "alpha", []string{"alpha"})
			stdout, stderr, code := runSubprocess(t, home,
				append([]string{"pgedge"}, tc.args...))
			if code != 2 {
				t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
		})
	}
}

// TestStateErrorShowsNoForm: a spec file that does not parse is exit 2,
// but the command line was right, so no form is printed under it.
func TestStateErrorShowsNoForm(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	writeProfilesConfig(t, home, "alpha", []string{"alpha"})
	spec := filepath.Join(home, "spec.yaml")
	if err := os.WriteFile(spec, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runSubprocess(t, home, []string{"pgedge",
		"controlplane", "database", "create", "storefront", "-f", spec})
	if code != 2 {
		t.Errorf("exit = %d, want 2; stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "parse spec file") {
		t.Errorf("not the spec error: %q", stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("state error printed the form:\n%s", stderr)
	}
}

// TestRuntimeErrorShowsNoForm is the inverse: an exit-1 error is not
// the caller's typing, so the form would only add noise.
func TestRuntimeErrorShowsNoForm(t *testing.T) {
	testsupport.ClearEnv(t)
	home := t.TempDir()
	bad := filepath.Join(home, "bad.yaml")
	if err := os.WriteFile(bad, []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runSubprocess(t, home,
		[]string{"pgedge", "--config", bad, "version"})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("exit-1 error printed the form:\n%s", stderr)
	}
}
