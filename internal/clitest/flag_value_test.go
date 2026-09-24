package clitest

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// A flag value the caller got wrong is a bad invocation, so it exits 2
// and nothing is sent. Three shapes were
// reaching the API instead:
//
//   - byoc parsed --created-after inline and returned the GENERAL code,
//     while managed's twin returned 2 for the identical flag;
//   - an optional string gated on `!= ""` could not tell an omitted
//     flag from `--region "$UNSET"`, so a backup store was created in
//     whatever region the API picked, permanently;
//   - --capacity guarded on `!= 0`, so a negative went to the server.
//
// EVERY ROW IS RUN TWICE, and the second run is what makes the first
// mean anything. A row whose other required flags are wrong would exit
// 2 from cobra, and the bad-value assertion would pass having tested
// nothing. So each row also runs with a VALID value for the flag under
// test and asserts the result is NOT 2 — reaching the auth failure or
// the network instead, which only happens once the command is
// otherwise well-formed.
//
// The runs need no credentials because every validation here now
// happens BEFORE the client is built. That ordering is part of the
// fix, not an accident of the test: nothing is sent, so there is no
// reason to resolve a connection first.
var flagValueCases = []struct {
	// args is the command path plus every OTHER required flag.
	args []string
	flag string
	// bad values must each exit 2; good must not.
	bad  []string
	good string
}{
	// byoc's four inline time parses, now cli.ParseTimeFlag.
	{
		args: []string{"starfleet", "byoc", "ingress", "list"},
		flag: "--created-after",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	{
		args: []string{"starfleet", "byoc", "ingress", "list"},
		flag: "--created-before",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	{
		args: []string{"starfleet", "byoc", "backup-store", "list"},
		flag: "--created-after",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	{
		args: []string{"starfleet", "byoc", "backup-store", "list"},
		flag: "--created-before",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	// The managed twin, which already exited 2. It is here so that
	// "make byoc match managed" cannot later be satisfied by moving
	// managed to byoc's behaviour.
	{
		args: []string{"starfleet", "managed", "backup", "list"},
		flag: "--created-after",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	{
		args: []string{"starfleet", "managed", "backup", "list"},
		flag: "--created-before",
		bad:  []string{"notatime", ""},
		good: "2026-08-01T00:00:00Z",
	},
	// A backup store's region has no update path anywhere in
	// the spec, the generated client or the CLI, so a dropped --region
	// is permanent.
	{
		args: []string{"starfleet", "byoc", "backup-store", "create",
			"--name", "store1",
			"--cloud-account-id", "3f2a9c1e-0000-4000-8000-000000000000"},
		flag: "--region",
		bad:  []string{""},
		good: "us-east-1",
	},
	// A blank region here does not send an empty value: URL
	// resolution collapses the empty path segment, so the request
	// addresses /regions/availability-zones at exit 0.
	{
		args: []string{"starfleet", "byoc", "cloud-account",
			"availability-zones",
			"3f2a9c1e-0000-4000-8000-000000000000"},
		flag: "--region",
		bad:  []string{"", "   "},
		good: "us-east-1",
	},
	// The third behaviour for a negative numeric flag in one
	// module. --volume-size refuses, --node volume-size= refuses,
	// and this one forwarded.
	{
		args: []string{"starfleet", "byoc", "cluster", "share", "create",
			"3f2a9c1e-0000-4000-8000-000000000000"},
		flag: "--capacity",
		bad:  []string{"-5", "0"},
		good: "2",
	},
}

// TestAMalformedFlagValueIsAUsageError is the mechanical gate over the
// whole set: it fails when a row regresses.
//
// What it cannot do is find a site nobody enumerated, and it cannot
// hold a PROPERTY either. A reviewer proved the second half: substitute
// every row's empty-value case for another malformed string and both
// the row count and the case count below still pass while the
// empty-is-absent mutation survives. TestApplyCreatedRange in
// internal/starfleet/conn is where that property actually lives, and it
// killed the mutation with this table thinned.
func TestAMalformedFlagValueIsAUsageError(t *testing.T) {
	// Both counts, because a row count alone does not pin coverage:
	// dropping "" from a row's bad slice leaves the row count
	// untouched and lets the empty-value mutation live. A reviewer
	// found that hole by mutating ApplyCreatedRange, which only the
	// "" cases kill.
	if len(flagValueCases) < 9 {
		t.Fatalf("flagValueCases has %d rows; a shrinking list is how "+
			"this gate stops covering the sweep it pins",
			len(flagValueCases))
	}
	cases := 0
	for _, c := range flagValueCases {
		cases += len(c.bad)
	}
	if cases < 17 {
		t.Fatalf("flagValueCases carries %d bad values across %d rows; "+
			"a row whose cases were thinned still counts as a row",
			cases, len(flagValueCases))
	}
	for _, c := range flagValueCases {
		name := strings.Join(c.args, " ") + " " + c.flag
		for _, bad := range c.bad {
			t.Run(name+"="+bad, func(t *testing.T) {
				args := append(append([]string{}, c.args...), c.flag, bad)
				if got := runForExit(t, args...); got != cli.ExitUsage {
					t.Errorf("exit = %d, want %d for %s %q",
						got, cli.ExitUsage, c.flag, bad)
				}
			})
		}
		t.Run(name+" (valid value is not a usage error)", func(t *testing.T) {
			args := append(append([]string{}, c.args...), c.flag, c.good)
			if got := runForExit(t, args...); got == cli.ExitUsage {
				t.Errorf("exit = 2 for a VALID %s %q — the row's other "+
					"flags are wrong, so its bad-value cases prove "+
					"nothing", c.flag, c.good)
			}
		})
	}
}

// The profile timeout and cert checks live in cp, whose validation
// runs with no server and no credentials for a different reason:
// these are read before any connection is resolved at all. They are
// here so the gate covers the set it claims, not only internal/controlplane/cmd's unit tests.
func TestControlplaneProfileAndCertValuesAreUsageErrors(t *testing.T) {
	t.Run("a malformed profile timeout", func(t *testing.T) {
		home := t.TempDir()
		writeControlplaneTimeoutProfile(t, home, "10 minutes")
		if got := runForExitInHome(t, home,
			"controlplane", "cluster", "info"); got != cli.ExitUsage {
			t.Errorf("exit = %d, want %d", got, cli.ExitUsage)
		}
	})
	// The escape hatch, and the precedence rule it rests on: a
	// --timeout on the command line has already replaced whatever the
	// profile says, so the bad value must not still be fatal.
	t.Run("--timeout overrides it", func(t *testing.T) {
		home := t.TempDir()
		writeControlplaneTimeoutProfile(t, home, "10 minutes")
		if got := runForExitInHome(t, home, "controlplane", "config", "view",
			"--timeout", "30s"); got == cli.ExitUsage {
			t.Error("exit = 2 with --timeout given; the flag must " +
				"beat the profile, or a bad profile cannot be worked " +
				"around")
		}
	})
	// doctor and config view diagnose the profile, so they report the
	// bad value rather than refusing to print anything.
	for _, args := range [][]string{
		{"controlplane", "doctor"}, {"controlplane", "config", "view"},
	} {
		t.Run(strings.Join(args, " ")+" still reports", func(t *testing.T) {
			home := t.TempDir()
			writeControlplaneTimeoutProfile(t, home, "10 minutes")
			if got := runForExitInHome(t, home, args...); got != 0 {
				t.Errorf("exit = %d, want 0: this is the command an "+
					"operator runs to find out what is wrong", got)
			}
		})
	}
	t.Run("an unreadable cert path", func(t *testing.T) {
		home := t.TempDir()
		got := runForExitInHome(t, home, "controlplane", "config", "set",
			"--base-url", "https://cp:3000",
			"--ca-cert", home+"/not-there.pem")
		if got != cli.ExitUsage {
			t.Errorf("exit = %d, want %d", got, cli.ExitUsage)
		}
	})
}

// An SSH key that is not a key used to be stored and reported
// as created. It is discovered when someone cannot reach a node, which
// is the furthest point from the cause, and the value is copied into
// node configuration so it outlives the mistake.
func TestSSHKeyCreateRejectsAValueThatIsNotAPublicKey(t *testing.T) {
	good := authorizedKey(t)
	for name, bad := range map[string]string{
		"not a key at all":  "not-a-key",
		"empty":             "",
		"type with garbage": "ssh-ed25519 nonsense",
		// All of these parse cleanly through ParseAuthorizedKey and
		// are refused deliberately. The CLI is stricter than the
		// Starfleet UI's validator for this field, not aligned with it --
		// the UI accepts a two-key value.
		"authorized_keys options": `no-pty,command="x" ` + good,
		// Three multi-key shapes, and only the first was caught by
		// the check as first written. The space-separated form parses
		// with an EMPTY rest, because ParseAuthorizedKey hands
		// everything after the base64 field back as the comment; the
		// CR form truncates the line and hides the remainder. Both
		// registered key one and discarded the rest at exit 0, which
		// is the silent drop this test is about.
		// ssh-keygen -l on this line exits 255.
		"type token disagrees with the blob": "ssh-rsa " +
			strings.Fields(good)[1],
		"type token is nonsense":    "zzz-nope " + strings.Fields(good)[1],
		"two keys, newline":         good + "\n" + good,
		"two keys, space":           good + " " + good,
		"two keys, carriage return": good + "\r" + good,
		"trailing comment line":     good + "\n# note",
		"leading comment line":      "# note\n" + good,
		// Parses cleanly through x/crypto; OpenSSH refuses it.
		"DSA key": dsaAuthorizedKey,
	} {
		t.Run(name, func(t *testing.T) {
			got := runForExit(t, "starfleet", "byoc", "ssh-key", "create",
				"--name", "k1", "--public-key", bad)
			if got != cli.ExitUsage {
				t.Errorf("exit = %d, want %d", got, cli.ExitUsage)
			}
		})
	}
}

// dsaAuthorizedKey is a fixed 1024-bit `ssh-keygen -t dsa` public key.
// Fixed rather than generated because DSA parameter generation is slow.
const dsaAuthorizedKey = "ssh-dss " +
	"AAAAB3NzaC1kc3MAAACBALusrjN1fTJXRank448dW4RTZ0MhbMlw" +
	"9Q+ipTrg5/9QHpfZ5LJovs0ZFN1pqvS2Eqe2/PAARGqOxmAZuJM34UPKPxZt" +
	"eICIkgLTsS2IrH48i8GaVC+fCOjeJjpsbIhvhbZJZhsyIelmuQheJu+SKCAU" +
	"EOfgC3ovXCMSqOzGGdrdAAAAFQDafbmfWw1/O3EMKvsft5PFH0gFlwAAAIA2" +
	"va0fNctiQYcATJlmqO6pT7uVhZitNWfZb9rqw4VwphXX9nTaHYwPXdIr9gFg" +
	"sPa5v56/zFKCq5/SCerfccFynVCSQFtxxIoH1vA74ut+adDUTjmW4WX0uqok" +
	"Y3x814JtSq9PGUWouopNKIruYipypTGKVtsMCJl1Q/SSxuTMcwAAAIAw2PkG" +
	"L/KzrwbvKrC0mP2CTtJNU6hKRsxFz3kSkPPzTLpaxWjEkJEiD09sQcwfvU/n" +
	"vs1C2I3uMKMugin4c4PPqS0m6ByqPq2NWzsxgARFjVRChZTxq9w8sV1q+R5F" +
	"Tg0PeC8Z38V93OSdxpIsFn34HF69LEnEm0SJVNUpgsBX3g=="

// authorizedKey returns a freshly generated ed25519 public key in
// authorized_keys form.
func authorizedKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// The control: a real public key still passes validation and the
// command goes on to fail for want of credentials, not for its input.
// Without this, marking --public-key impossible to satisfy would look
// like a fix.
func TestSSHKeyCreateAcceptsARealPublicKey(t *testing.T) {
	authorized := authorizedKey(t)
	for _, v := range []string{authorized, authorized + " me@example"} {
		got := runForExit(t, "starfleet", "byoc", "ssh-key", "create",
			"--name", "k1", "--public-key", v)
		if got == cli.ExitUsage {
			t.Errorf("exit = 2 for a real ed25519 public key %q", v)
		}
	}
}

// runForExitInHome is runForExit with a caller-supplied HOME, so a
// test can plant a config file the command will read. runForExit makes
// its own temp HOME, which is right for every case that needs no
// config and wrong for the ones that do.
func runForExitInHome(t *testing.T, home string, args ...string) int {
	t.Helper()
	t.Setenv("HOME", home)

	rt := &module.Runtime{Stdin: strings.NewReader("")}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			t.Fatal(err)
		}
		root.AddCommand(c)
	}
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return cli.ExitCode(root.Execute())
}

// writeControlplaneTimeoutProfile plants a config whose controlplane profile carries the
// given timeout: value, and a base URL that refuses instantly so no
// case here can reach a real Control Plane -- one may be running on
// localhost:3000 on a developer's machine.
func writeControlplaneTimeoutProfile(t *testing.T, home, timeout string) {
	t.Helper()
	dir := filepath.Join(home, ".pgedge", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "current_profile: default\nprofiles:\n  default:\n" +
		"    controlplane:\n      base_url: http://127.0.0.1:1\n" +
		"      timeout: \"" + timeout + "\"\n"
	if err := os.WriteFile(
		filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
