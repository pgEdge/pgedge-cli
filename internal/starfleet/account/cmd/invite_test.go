package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/pflag"
)

func TestNewInviteCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewInviteCmd(rt)

	if cmd.Use != "invite" {
		t.Errorf("Use = %q, want \"invite\"", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "invites" {
		t.Errorf("Aliases = %v, want first alias \"invites\"", cmd.Aliases)
	}
	if !cmd.HasSubCommands() {
		t.Error("no verbs registered")
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false,
		"delete": false, "accept": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("invite command missing subcommand %q", name)
		}
	}
}

// TestInviteVerbsNeedingUserSession covers both verbs that cannot work
// with a client credential, whatever arguments they are given. Measured
// live before the guard existed: create answered `400 cannot create
// invites from an api client` and accept answered 401.
//
// The flags are deliberately no longer marked required. cobra validates
// required flags before RunE, so marking them would replace this
// explanation with "required flag(s) \"email\" not set" — technically
// true and completely unhelpful, since supplying it changes nothing.
// The "with flags supplied" cases below are what pin that.
func TestInviteVerbsNeedingUserSession(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "create bare", args: []string{"invite", "create"}},
		{name: "create with email", args: []string{"invite", "create",
			"--email", "teammate@example.com"}},
		{name: "create with every flag", args: []string{"invite", "create",
			"--email", "teammate@example.com", "--expiration", "48"}},
		{name: "accept bare", args: []string{"invite", "accept",
			testInviteID}},
		{name: "accept with token", args: []string{"invite", "accept",
			testInviteID, "--token", "tok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, _, _ := testsupport.NewRuntime(t, "", "table")
			var out bytes.Buffer
			err := runAccount(t, rt, &out, tt.args...)
			if err == nil {
				t.Fatal("expected the user-session guard to refuse")
			}
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want an ExitError, got %T: %v", err, err)
			}
			if ee.Code() != ExitAuth {
				t.Errorf("exit code = %d, want ExitAuth (%d)",
					ee.Code(), ExitAuth)
			}
			// The message must say what to do instead, not just refuse.
			for _, want := range []string{"signed-in user", "UI"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q missing %q", err.Error(), want)
				}
			}
			// And it must not pretend a flag was the problem.
			if strings.Contains(err.Error(), "required flag") {
				t.Errorf("guard was masked by flag validation: %q",
					err.Error())
			}
		})
	}
}

// TestInviteGuardedVerbsIssueNoRequest is the half that matters for a
// destructive-adjacent verb: the guard must refuse before any network
// call, not after one that fails. The server fails the test if reached,
// so a guard that regressed into a post-request check would be caught.
func TestInviteGuardedVerbsIssueNoRequest(t *testing.T) {
	for _, args := range [][]string{
		{"invite", "create", "--email", "teammate@example.com"},
		{"invite", "accept", testInviteID, "--token", "tok"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "table")
			var hits int
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					// The token endpoint is part of the harness, not
					// the verb under test.
					if r.URL.Path != testsupport.TokenPath {
						hits++
					}
					testsupport.JSONHandler(200, "{}")(w, r)
				})
			if err := runAuthedAccount(
				t, rt, out, url, args...); err == nil {
				t.Fatal("expected the guard to refuse")
			}
			if hits != 0 {
				t.Errorf("verb issued %d API request(s); the guard must "+
					"refuse before any network call", hits)
			}
		})
	}
}

// TestInviteGuardPremiseStillHolds is the tripwire. The guard exists
// only because every credential the CLI accepts is a client ID and
// secret, which saas maps to X-Client-ID and never X-User-ID. If an
// interactive or device login ever lands, the premise is false and both
// verbs should be restored rather than left refusing.
//
// This is weaker than the spec-divergence pattern used for
// TestDatabaseLogsSpecShapeDivergesFromAPI, because there is no
// vendored artefact to key off — it watches the auth surface instead.
// It fails on a new auth verb or a new login flag, which is the
// cheapest available signal that the premise moved.
func TestInviteGuardPremiseStillHolds(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	// Reached through the real tree rather than newAuthCmd directly, so
	// the tripwire sees what a user sees.
	auth, _, err := newStarfleetRoot(rt).Find([]string{"auth"})
	if err != nil {
		t.Fatalf("starfleet auth not found: %v", err)
	}

	// whoami is listed here having been checked against the premise
	// rather than waved through. It reads the client and tenant lists
	// with the same client credential and mints no user session, and
	// the API's own current-user route refuses that credential with 401
	// on every profile tried, dev and prod alike, because a client
	// principal carries no user id. That refusal is evidence FOR the
	// premise, not against it.
	wantVerbs := map[string]bool{"login": true, "status": true,
		"whoami": true, "logout": true}
	for _, sub := range auth.Commands() {
		name := strings.Fields(sub.Use)[0]
		if !wantVerbs[name] {
			t.Errorf("new `starfleet auth %s` verb: if it signs a *user* "+
				"in, the invite guard's premise no longer holds — "+
				"revisit errUserSessionRequired", name)
		}
		delete(wantVerbs, name)
	}
	for name := range wantVerbs {
		t.Errorf("`starfleet auth %s` disappeared; this tripwire is now "+
			"watching the wrong surface", name)
	}

	// The credentials themselves are persistent flags on the cloud
	// root, not local to login, so that is the surface to watch: a
	// device-code or browser login would add one here.
	wantFlags := map[string]bool{
		"api-url": true, "client-id": true, "client-secret": true,
	}
	newStarfleetRoot(rt).PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !wantFlags[f.Name] {
			t.Errorf("new persistent `cloud --%s` flag: if it "+
				"authenticates a *user*, revisit "+
				"errUserSessionRequired and the two guarded invite "+
				"verbs", f.Name)
		}
		delete(wantFlags, f.Name)
	})
	for name := range wantFlags {
		t.Errorf("`cloud --%s` disappeared; this tripwire is now "+
			"watching the wrong surface", name)
	}
}
