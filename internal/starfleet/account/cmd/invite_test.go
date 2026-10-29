package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestInviteVerbsNeedingUserSession covers accept, which refuses
// whatever arguments it is given. --token is deliberately not marked
// required: cobra would report the missing flag instead of the refusal.
func TestInviteVerbsNeedingUserSession(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
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

// TestInviteGuardedVerbsIssueNoRequest: the guard must refuse before
// any network call, not after one that fails.
func TestInviteGuardedVerbsIssueNoRequest(t *testing.T) {
	for _, args := range [][]string{
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

// TestInviteGuardPremiseStillHolds is the tripwire. create sends its
// request only for a browser sign-in and accept refuses every sign-in.
// It fails on a new auth verb or persistent flag, which could add a
// credential kind the create guard has not classified.
func TestInviteGuardPremiseStillHolds(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	// Reached through the real tree rather than newAuthCmd directly, so
	// the tripwire sees what a user sees.
	auth, _, err := newStarfleetRoot(rt).Find([]string{"auth"})
	if err != nil {
		t.Fatalf("starfleet auth not found: %v", err)
	}

	// None of these adds a kind of sign-in: login writes either a
	// browser session or an API client.
	wantVerbs := map[string]bool{"login": true, "status": true,
		"whoami": true, "logout": true}
	for _, sub := range auth.Commands() {
		name := strings.Fields(sub.Use)[0]
		if !wantVerbs[name] {
			t.Errorf("new `starfleet auth %s` verb: if it adds a kind "+
				"of sign-in, check invite create's session guard "+
				"classifies it", name)
		}
		delete(wantVerbs, name)
	}
	for name := range wantVerbs {
		t.Errorf("`starfleet auth %s` disappeared; this tripwire is now "+
			"watching the wrong surface", name)
	}

	// A browser session reaches the API for create and not for accept.
	for _, tc := range []struct {
		args     []string
		wantHits int
	}{
		{[]string{"invite", "create", "--email", "teammate@example.com"}, 1},
		{[]string{"invite", "accept", testInviteID, "--token", "tok"}, 0},
	} {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				hits++
				testsupport.JSONHandler(200, inviteBody)(w, r)
			}))
		srt, _, _ := testsupport.NewRuntime(t, "", "text")
		testsupport.SeedSession(t, srt, srv.URL, "u1",
			time.Now().Add(time.Hour), "t-a")
		err := runAccount(t, srt, &bytes.Buffer{}, tc.args...)
		srv.Close()
		if hits != tc.wantHits {
			t.Errorf("%v with a browser session: %d request(s), want "+
				"%d (err = %v); update this tripwire, the invite help "+
				"and invite.txt together", tc.args, hits, tc.wantHits,
				err)
		}
	}

	// The credentials themselves are persistent flags on the starfleet
	// root, so that is the surface to watch for another sign-in kind.
	wantFlags := map[string]bool{
		"api-url": true, "client-id": true, "client-secret": true,
		"tenant": true,
	}
	newStarfleetRoot(rt).PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !wantFlags[f.Name] {
			t.Errorf("new persistent `cloud --%s` flag: if it "+
				"carries a credential, check that invite create "+
				"still refuses every non-session one", f.Name)
		}
		delete(wantFlags, f.Name)
	})
	for name := range wantFlags {
		t.Errorf("`cloud --%s` disappeared; this tripwire is now "+
			"watching the wrong surface", name)
	}
}
