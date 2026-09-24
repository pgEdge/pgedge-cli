package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testInviteID = "55555555-6666-7777-8888-99990000aaaa"

const inviteBody = `{"id":"` + testInviteID + `",` +
	`"email":"teammate@example.com","team_name":"team",` +
	`"expires_at":"2024-04-15T10:30:00Z",` +
	`"created_at":"2024-03-15T10:30:00Z",` +
	`"updated_at":"2024-03-15T10:30:00Z"}`

func TestInviteListRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotPath string
		url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testsupport.JSONHandler(200, `[`+inviteBody+`]`)(w, r)
		})
		if err := runAuthedAccount(t, rt, out, url, "invite", "list"); err != nil {
			t.Fatalf("invite list: %v", err)
		}
		if !strings.Contains(out.String(), "teammate@example.com") {
			t.Errorf("missing email: %q", out.String())
		}
		if gotPath != "/account/v1/invites" {
			t.Errorf("request path = %q, want /account/v1/invites", gotPath)
		}
	})

	t.Run("empty json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url, "invite", "list"); err != nil {
			t.Fatalf("invite list json: %v", err)
		}
	})

	// text empty covers the "No invites found" branch in text mode,
	// distinct from "empty json" above: the format check in
	// newInviteListCmd returns before this message on any non-table/text
	// format, so only a text-mode empty response reaches it.
	t.Run("text empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthedAccount(t, rt, out, url, "invite", "list"); err != nil {
			t.Fatalf("invite list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No invites found") {
			t.Errorf("missing empty-list message: %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthedAccount(t, rt, out, url, "invite", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

// TestInviteListSendsBearerToken verifies clientFromCmd resolves the
// account root's connection flags and attaches the bearer token from
// the token endpoint to an outgoing invite-list request. Formerly
// TestNewAccountClientSendsBearer in internal/byoc/cmd, back when
// invite/membership reached the accounts API through a second,
// byoc-local client (newAccountClient) layered on top of byoc's own.
// That duplication is gone now that invite lives in this package and
// calls the same clientFromCmd every other account command uses, but
// this end-to-end path is still not exercised elsewhere:
// TestClientFromCmd (client_conn_test.go) drives clientFromCmd through
// a synthetic probe command, not a real resource command's RunE, so it
// would not catch a bearer header dropped between clientFromCmd and an
// actual leaf command like `invite list`.
func TestInviteListSendsBearerToken(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")

	var gotAuth, gotPath string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			testsupport.JSONHandler(200, `[]`)(w, r)
		})

	if err := runAuthedAccount(t, rt, out, url, "invite", "list"); err != nil {
		t.Fatalf("invite list: %v", err)
	}

	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want \"Bearer tok\"", gotAuth)
	}
	if gotPath != "/account/v1/invites" {
		t.Errorf("request path = %q, want /account/v1/invites", gotPath)
	}
}

func TestInviteGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, inviteBody))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "get", testInviteID); err != nil {
			t.Fatalf("invite get: %v", err)
		}
		if !strings.Contains(out.String(), "teammate@example.com") {
			t.Errorf("missing email: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, inviteBody))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "get", testInviteID); err != nil {
			t.Fatalf("invite get json: %v", err)
		}
		if !strings.Contains(out.String(), testInviteID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, inviteBody))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "get", "bad"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	// nil body covers the "no invite data returned" branch: a 2xx
	// response that carries no parseable JSON200 body (the generated
	// parser only fills it for 200; 202 leaves it nil).
	t.Run("nil body", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(202, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "get", testInviteID); err != nil {
			t.Fatalf("invite get nil body: %v", err)
		}
		if !strings.Contains(errb.String(), "No invite data returned") {
			t.Errorf("missing nil-body message: %q", errb.String())
		}
	})
}

// invite create has no success path to exercise: the API refuses it
// for any client credential, so the CLI refuses it before the request.
// Its behaviour is covered by TestInviteVerbsNeedingUserSession and
// TestInviteGuardedVerbsIssueNoRequest in invite_test.go, which assert
// the exit code, the message and that no request is issued.
//
// Deliberately not a stub-server test that "passes" on any error — the
// previous "server error" case would now pass without the server being
// reached at all, which is the kind of test that looks like coverage
// and proves nothing.

func TestInviteDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "delete", testInviteID, "--force"); err != nil {
			t.Fatalf("invite delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "delete", "bad", "--force"); err == nil {
			t.Fatal("expected error on invalid id")
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthedAccount(t, rt, out, url,
			"invite", "delete", testInviteID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

// invite accept, like invite create, has no reachable success path —
// see the note above TestInviteDeleteRun's neighbour. Covered by
// TestInviteVerbsNeedingUserSession.
//
// Note the guard also removes the invite-ID validation that used to
// run here: it refuses before parsing the argument, since a valid UUID
// would not have helped. `invite delete` still validates its ID, and
// TestInviteDeleteRun/invalid_id still covers that.
