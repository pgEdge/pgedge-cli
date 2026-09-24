package cmd

import (
	"net/http"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The empty-body-success contract for account's own command tree
// (part of the starfleet module). See the long explanation on
// checkEmptyBodyResponse in client_conn.go, and the matching gate in
// internal/starfleet/byoc/cmd/emptybody_run_test.go.
//
// `client delete` is included deliberately even though it is already
// fixed: DeleteClient is the one saas handler that provably sends
// `ctx.JSON(http.StatusNoContent, …)`, so it is the case that fires in
// production rather than latently, and it is the one most worth a
// standing regression guard.
//
// When this goes red, fix the COMMAND, not the fixture. Do not
// "simplify" the stub to a bare w.WriteHeader(204) — that drops the
// Content-Type and with it the whole point of the test.

// accountEmptyBodySuccess names the accountCmdCases entries whose
// operation answers success with an empty body.
var accountEmptyBodySuccess = []string{
	"invite delete",     // DeleteInvite
	"membership delete", // DeleteMembership
	"client delete",     // DeleteClient — fires in production
}

// accountEmptyBodyExempt names destructive cases deliberately outside
// the contract, each with its reason.
var accountEmptyBodyExempt = map[string]string{
	// AcceptInvite does answer 204 with an empty body, so it belonged
	// here until the verb stopped calling it. It now refuses locally
	// with ExitAuth because the operation needs a signed-in user and
	// the CLI only ever holds a client credential — see
	// errUserSessionRequired in invite.go. With no request issued there
	// is no response for this contract to check; driving it through the
	// 204 stub would only re-test the guard, which
	// TestInviteVerbsNeedingUserSession already covers.
	//
	// Restoring it would take more than moving this line, and the
	// reason is worth recording: openapi/account.yaml declares no
	// acceptInvite operation at all — measured, and the generated
	// account client carries zero AcceptInvite symbols. saas has the
	// handler (its own api/invites.go), but the public filter drops it,
	// so a user login would need the operation vendored first. An
	// earlier version of this comment said the 204 was declared here
	// and unchanged, which was false in both halves.
	"invite accept": "refuses locally; issues no request",
}

func findAccountCase(t *testing.T, name string) accountCmdCase {
	t.Helper()
	for _, tc := range accountCmdCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no accountCmdCases entry named %q — the gate and the "+
		"case table have drifted apart", name)
	return accountCmdCase{}
}

// TestAccountEmptyBodySuccessIsNotReportedAsFailure drives every
// empty-body-success command through a 204 carrying a JSON
// Content-Type and requires exit 0.
func TestAccountEmptyBodySuccessIsNotReportedAsFailure(t *testing.T) {
	for _, name := range accountEmptyBodySuccess {
		tc := findAccountCase(t, name)
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusNoContent, ""))
			err := runAuthedAccount(t, rt, out, url, tc.args...)
			if err != nil {
				t.Fatalf("204 + JSON Content-Type reported as a "+
					"failure: %v", err)
			}
		})
	}
}

// TestEveryDestructiveAccountCaseIsAccountedFor pins the exemption set,
// so a new destructive verb cannot escape the contract unconsidered.
func TestEveryDestructiveAccountCaseIsAccountedFor(t *testing.T) {
	gated := make(map[string]bool, len(accountEmptyBodySuccess))
	for _, n := range accountEmptyBodySuccess {
		gated[n] = true
	}

	var destructive int
	for _, tc := range accountCmdCases {
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
		if gated[tc.name] {
			continue
		}
		if reason, ok := accountEmptyBodyExempt[tc.name]; ok {
			if reason == "" {
				t.Errorf("%s: exemption must carry a reason", tc.name)
			}
			continue
		}
		t.Errorf("destructive command %q is neither in "+
			"accountEmptyBodySuccess nor accountEmptyBodyExempt — "+
			"decide which and say why", tc.name)
	}

	// Positive control: without this, renaming --force would empty the
	// loop and the test would pass having checked nothing.
	if destructive < 3 {
		t.Errorf("found only %d destructive cases; expected at least 3 "+
			"— has --force been renamed, or the table truncated?",
			destructive)
	}
}
