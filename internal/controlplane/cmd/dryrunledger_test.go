package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
)

// specWithPlaceholders is a minimal DatabaseSpec carrying an unfilled
// template placeholder, so checkUnfilledPlaceholders refuses it.
const specWithPlaceholders = `database_name: storefront
postgres_version: "16.4"
database_users:
  - username: admin
    password: CHANGE-ME
    db_owner: true
nodes:
  - name: n1
    host_ids: [CHANGE-ME]
`

// specValid is the same spec with the placeholder filled.
const specValid = `database_name: storefront
postgres_version: "16.4"
database_users:
  - username: admin
    password: sup3rs3cret
    db_owner: true
nodes:
  - name: n1
    host_ids: [7f3a5c1e-0000-4000-8000-000000000000]
`

func writeSpec(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestControlplaneSpecLedgerRecordsAtTheCallSites covers the four ledger lines in
// database_spec.go, at the level that matters: the create path under
// --dry-run.
//
// Testing the validators directly would prove nothing — they take no
// Runtime, so "the validator did not record" is structurally true and
// cannot fail. The behaviour worth pinning is that the CALLER records
// after each check passes, and records NOTHING when one refuses.
func TestControlplaneSpecLedgerRecordsAtTheCallSites(t *testing.T) {
	t.Run("a valid spec records both checks", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := newServer(t, jsonHandler(200, `{}`))
		// The write is intercepted, so an error here is the abort.
		// The write is intercepted, so an error here is the abort.
		_ = runControlplane(t, rt, out, url, "database", "create",
			"7f3a5c1e-0000-4000-8000-000000000000",
			"-f", writeSpec(t, specValid))

		got := rt.DryRun.Checks()
		if len(got) != 2 {
			t.Fatalf("recorded %q, want two entries", got)
		}
		joined := strings.Join(got, "\n")
		for _, want := range []string{
			"postgres versions well-formed",
			"no unfilled template placeholders",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("ledger missing %q:\n%s", want, joined)
			}
		}
		if !rt.DryRun.Intercepted() {
			t.Error("the create write was not intercepted")
		}
	})

	t.Run("a refused check records nothing after it", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := newServer(t, jsonHandler(200, `{}`))
		err := runControlplane(t, rt, out, url, "database", "create",
			"7f3a5c1e-0000-4000-8000-000000000000",
			"-f", writeSpec(t, specWithPlaceholders))
		if err == nil {
			t.Fatal("want an error for an unfilled placeholder")
		}
		got := rt.DryRun.Checks()
		// The version check runs first and passes; the placeholder check
		// then refuses, so exactly one entry may exist and the
		// placeholder line must NOT.
		for _, entry := range got {
			if strings.Contains(entry, "placeholder") {
				t.Errorf("recorded a check that refused the command: %q",
					entry)
			}
		}
		if rt.DryRun.Intercepted() {
			t.Error("a refused spec still reached the write")
		}
	})
}

// TestTaskCancelRejectsDotSegmentDatabase covers the one value that made
// a dry run lie: `.` and `..` survive path escaping, so url.Parse
// resolves them away and the built path no longer matches the cancel
// operation — the request went out and no report was printed.
//
// Exhaustive substitution against every Control Plane path template
// showed a dot segment cannot land on another state-changing operation,
// so this is about dry-run truthfulness, not a write escape.
func TestTaskCancelRejectsDotSegmentDatabase(t *testing.T) {
	for _, bad := range []string{".", ".."} {
		t.Run(bad, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			err := runControlplane(t, rt, out, "http://127.0.0.1:9",
				"task", "cancel",
				"11111111-1111-4111-8111-111111111111",
				"--database", bad, "--force")
			if err == nil {
				t.Fatalf("--database %q was accepted", bad)
			}
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code() != ExitUsage {
				t.Errorf("error = %v (%T), want a usage error", err, err)
			}
		})
	}

	// A normal value must still be accepted this far — otherwise the
	// check above passes by rejecting everything.
	rt, out, _ := newTestRuntime(t, "", "text")
	err := runControlplane(t, rt, out, "http://127.0.0.1:9", "task", "cancel",
		"11111111-1111-4111-8111-111111111111",
		"--database", "storefront", "--force")
	if err == nil {
		t.Fatal("precondition: expected a transport error against a " +
			"closed port")
	}
	if strings.Contains(err.Error(), "cannot be a path segment") {
		t.Errorf("an ordinary database id was rejected: %v", err)
	}
}
