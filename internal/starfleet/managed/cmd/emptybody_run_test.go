package cmd

import (
	"net/http"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The empty-body-success contract.
//
// Every generated Parse*Response ends in a
// `strings.Contains(Content-Type, "json") && true` catch-all that
// unmarshals the body into the spec's Error model for ANY status, 2xx
// included. Where an operation also has no typed 2xx case, a success
// carrying an empty body has nothing else to match, so it lands on that
// catch-all and json.Unmarshal of 0 bytes fails — turning a call that
// SUCCEEDED into "unexpected end of JSON input" and a non-zero exit.
//
// Whether it fires depends only on the server sending a JSON
// Content-Type alongside the empty 2xx. The stub here answers WITH one,
// the hostile shape, so a command that cannot survive it fails here
// rather than in the field after a one-line server change.
//
// When this test goes red, fix the COMMAND, not the fixture: call the
// untyped operation instead of its *WithResponse wrapper and hand the
// StatusCode and body to checkEmptyBodyResponse. Do not "simplify" the
// stub to a bare w.WriteHeader(204) — that removes the Content-Type and
// with it the whole point of the test.

// managedEmptyBodySuccess names the managedCmdCases entries whose
// operation has no typed 2xx case in the generated client, so nothing
// can consume a body even if one arrived.
//
// Three, not two. Resize belongs here despite declaring 200 rather than
// 204: its spec gives that 200 no content schema, so the generated
// ResizeManagedDatabaseResponse has no JSON200 field and the catch-all
// is all that is left.
var managedEmptyBodySuccess = []string{
	"database delete",          // DeleteManagedDatabase, 204
	"database rotate-password", // RotateManagedDatabaseRolePassword, 204
	"database resize",          // ResizeManagedDatabase, 200 + no schema
	"database branch delete",   // DeleteBranch, 204
}

// managedEmptyBodyExempt names destructive cases deliberately NOT under
// the contract, each with the reason. A destructive command that is
// neither gated nor listed here fails
// TestEveryDestructiveManagedCaseIsAccountedFor.
var managedEmptyBodyExempt = map[string]string{
	// Not a delete endpoint: it PATCHes the database with the service
	// removed from the list, so success carries a ManagedDatabase body
	// and the parser has a typed 2xx case.
	"database service remove": "PATCHes the database; success has a body",
	// Not an empty-body case: the 202 Accepted carries the
	// ManagedDatabase being restored, so the parser has a typed 2xx
	// case (JSON202) and nothing lands on the catch-all.
	"backup restore": "202 carries a ManagedDatabase; typed 2xx case",
	// Both PATCH the database through applyAllowlist and receive the
	// database object back (UpdateManagedDatabaseResponse has a typed
	// JSON200); --force guards the lockout confirm, not a delete.
	"database allowlist remove": "PATCH returns the database " +
		"(typed 200); --force guards the lockout confirm, not a delete",
	"database allowlist clear": "PATCH returns the database " +
		"(typed 200); --force guards the lockout confirm, not a delete",
}

func findManagedCase(t *testing.T, name string) managedCmdCase {
	t.Helper()
	for _, tc := range managedCmdCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no managedCmdCases entry named %q — the gate and the "+
		"case table have drifted apart", name)
	return managedCmdCase{}
}

// TestManagedEmptyBodySuccessIsNotReportedAsFailure drives every
// empty-body-success command through a 204 carrying a JSON
// Content-Type and requires exit 0.
func TestManagedEmptyBodySuccessIsNotReportedAsFailure(t *testing.T) {
	for _, name := range managedEmptyBodySuccess {
		tc := findManagedCase(t, name)
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				withCatalog(testsupport.JSONHandler(
					http.StatusNoContent, "")))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("204 + JSON Content-Type reported as a "+
					"failure: %v", err)
			}
		})
	}
}

// TestResizeAcceptsBothResponseShapes pins the shape mismatch that
// makes resize the dangerous one.
//
// The spec declares 200 with no content, and the API answers with a
// full ManagedDatabase.
// The two disagree, so the command has to survive either. An empty body
// is what the spec promises; a populated one is what the server sends
// today. Both must exit 0.
func TestResizeAcceptsBothResponseShapes(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"empty body, per the spec": {http.StatusOK, ""},
		"full ManagedDatabase, per the handler": {
			http.StatusOK, databaseJSON(testDatabaseID, "")},
		"204 with a JSON content type": {http.StatusNoContent, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				withCatalog(testsupport.JSONHandler(
					tc.status, tc.body)))
			err := runAuthed(t, rt, out, url, "database", "resize",
				testDatabaseID, "--size", "large", "--force")
			if err != nil {
				t.Fatalf("resize reported success as failure: %v", err)
			}
		})
	}
}

// TestEmptyBodyOpsStillReportRealFailures is the negative control for
// the bypass. Skipping the parser must not also skip error handling: a
// 4xx still has to fail, or the contract test above would pass just as
// well against a command that ignores the status entirely.
func TestEmptyBodyOpsStillReportRealFailures(t *testing.T) {
	for _, name := range managedEmptyBodySuccess {
		tc := findManagedCase(t, name)
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusNotFound,
					`{"code":404,"message":"not found"}`))
			if err := runAuthed(t, rt, out, url, tc.args...); err == nil {
				t.Fatal("404 was reported as success")
			}
		})
	}
}

// TestEveryDestructiveManagedCaseIsAccountedFor pins the exemption set.
// A --force flag marks a destructive verb, and every destructive verb
// is either under the empty-body contract or explicitly exempt with a
// reason.
func TestEveryDestructiveManagedCaseIsAccountedFor(t *testing.T) {
	gated := make(map[string]bool, len(managedEmptyBodySuccess))
	for _, n := range managedEmptyBodySuccess {
		gated[n] = true
	}

	var destructive int
	seenDestructive := map[string]bool{}
	for _, tc := range managedCmdCases {
		forced := false
		for _, a := range tc.args {
			if a == "--force" {
				forced = true
				break
			}
		}
		if !forced {
			continue
		}
		destructive++
		seenDestructive[tc.name] = true
		if gated[tc.name] {
			continue
		}
		if reason, ok := managedEmptyBodyExempt[tc.name]; ok {
			if reason == "" {
				t.Errorf("%s: exemption must carry a reason", tc.name)
			}
			continue
		}
		t.Errorf("destructive command %q is neither in "+
			"managedEmptyBodySuccess nor managedEmptyBodyExempt — "+
			"decide which and say why", tc.name)
	}

	// Positive control, pinned to a NAMED LIST rather than a floor.
	//
	// It used to assert `destructive >= 3` against five real entries,
	// which guarded against the loop being EMPTIED and against
	// nothing else. That is not the failure that happens: deleting
	// `--force` from one row of managedCmdCases leaves the count at
	// four and silently turns four other table-driven tests into
	// no-ops, because each takes its args from this same shared table
	// and a command that stops at the destructive refusal never
	// reaches the 500, the 404, the dead connection or the auth
	// failure it was meant to exercise. One of those four is an
	// explicitly labelled negative control. Measured: dropping the
	// token from the resize row left all five green.
	//
	// An exact set makes adding or removing a destructive command a
	// deliberate edit here.
	wantDestructive := map[string]bool{
		"database delete":           true,
		"database resize":           true,
		"database rotate-password":  true,
		"database service remove":   true,
		"backup restore":            true,
		"database allowlist remove": true,
		"database allowlist clear":  true,
		"database branch delete":    true,
	}
	if destructive != len(wantDestructive) {
		t.Errorf("found %d destructive cases, expected %d — has "+
			"--force been renamed or removed from a row, or has a "+
			"destructive command been added? Update wantDestructive "+
			"deliberately; do not relax the count.",
			destructive, len(wantDestructive))
	}
	for name := range wantDestructive {
		if !seenDestructive[name] {
			t.Errorf("%q carries no --force in managedCmdCases; every "+
				"table test taking its args now skips the error path "+
				"it exists to check", name)
		}
	}
}
