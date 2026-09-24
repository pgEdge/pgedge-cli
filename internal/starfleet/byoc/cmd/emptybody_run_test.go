package cmd

import (
	"net/http"
	"strings"
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
// Content-Type alongside the empty 2xx. saas's RespondNoContent ->
// ctx.NoContent(204) sets no Content-Type, which is why these are
// latent rather than broken today; `ctx.JSON(http.StatusNoContent, …)`
// sets one, which is why `starfleet client delete` had to be fixed.
//
// These tests hold the contract independent of which of those two a
// handler happens to use: the stub answers 204 WITH a JSON
// Content-Type, the hostile shape, so a command that cannot survive it
// fails here rather than in the field after a one-line server change.
//
// When this test goes red, fix the COMMAND, not the fixture. The fix is
// the bypass precedent in internal/starfleet/account/cmd/apiclient.go's
// delete verb: keep the generated request builder, call the untyped operation
// instead of its *WithResponse wrapper, and hand StatusCode and body to
// checkResponse, which already accepts any 2xx. Do not "simplify" the
// stub to a bare w.WriteHeader(204) — that removes the Content-Type and
// with it the whole point of the test.

// byocEmptyBodySuccess names the byocCmdCases entries whose operation
// answers success with an empty body: the generated parser carries the
// catch-all and has no typed 2xx case, so nothing can consume a body
// even if one arrived.
var byocEmptyBodySuccess = []string{
	"cluster delete",             // DeleteCluster
	"cluster share delete",       // DeleteClusterShare
	"database delete",            // DeleteDatabase
	"backup create",              // BackupDatabase
	"backup-store delete",        // DeleteBackupStore
	"cloud-account delete",       // DeleteCloudAccount
	"ingress delete",             // DeleteIngress
	"ingress service deregister", // DeleteIngressService
	"ssh-key delete",             // DeleteSshKey

	// Neither is a delete, but both answer success with no usable
	// body and have no typed 2xx case. byoc's handlers return
	// RespondOK(ctx, nil) against specs declaring no content, so
	// success arrives as 200 carrying the JSON literal `null` —
	// verified live on 2026-08-04 — where managed's rotate answers
	// 204. Both shapes land on the same catch-all, so both need the
	// bypass.
	"database rotate-password", // RotateDatabaseRolePassword
	"database restore",         // RestoreDatabase
}

// byocEmptyBodyExempt names destructive cases that are deliberately NOT
// under the contract, each with the reason. A destructive command that
// is neither gated nor listed here fails
// TestEveryDestructiveByocCaseIsAccountedFor, so a new one cannot slip
// past unconsidered.
var byocEmptyBodyExempt = map[string]string{
	// Not a delete endpoint: it PATCHes the database with the service
	// removed from the list, so success carries a Database body and the
	// parser has a typed 2xx case. UpdateDatabaseWithResponse,
	// database_service.go.
	"database service remove": "PATCHes the database; success has a body",
}

func findByocCase(t *testing.T, name string) byocCmdCase {
	t.Helper()
	for _, tc := range byocCmdCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no byocCmdCases entry named %q — the gate and the case "+
		"table have drifted apart", name)
	return byocCmdCase{}
}

// TestEmptyBodySuccessIsNotReportedAsFailure drives every
// empty-body-success command through a 204 carrying a JSON
// Content-Type and requires exit 0.
func TestEmptyBodySuccessIsNotReportedAsFailure(t *testing.T) {
	for _, name := range byocEmptyBodySuccess {
		tc := findByocCase(t, name)
		t.Run(name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusNoContent, ""))
			if err := runAuthed(t, rt, out, url, tc.args...); err != nil {
				t.Fatalf("204 + JSON Content-Type reported as a "+
					"failure: %v", err)
			}
		})
	}
}

// TestEveryDestructiveByocCaseIsAccountedFor pins the exemption set. A
// --force flag marks a destructive verb, and every destructive verb is
// either under the empty-body contract or explicitly exempt with a
// reason. Without this, adding a delete verb would silently escape the
// contract.
func TestEveryDestructiveByocCaseIsAccountedFor(t *testing.T) {
	gated := make(map[string]bool, len(byocEmptyBodySuccess))
	for _, n := range byocEmptyBodySuccess {
		gated[n] = true
	}

	var destructive int
	for _, tc := range byocCmdCases {
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
		if reason, ok := byocEmptyBodyExempt[tc.name]; ok {
			if reason == "" {
				t.Errorf("%s: exemption must carry a reason", tc.name)
			}
			continue
		}
		t.Errorf("destructive command %q is neither in "+
			"byocEmptyBodySuccess nor byocEmptyBodyExempt — decide "+
			"which and say why", tc.name)
	}

	// Positive control: the scan found the destructive commands at all.
	// Without this, a rename of --force would empty the loop and the
	// test would pass having checked nothing.
	if destructive < 9 {
		t.Errorf("found only %d destructive cases; expected at least 9 "+
			"— has --force been renamed, or the table truncated?",
			destructive)
	}
}

// TestBackupRepositoryGetSurvivesItsDeclared204 covers the one READ in
// the tree whose spec declares an empty success.
//
// `getBackupRepositoryInfo` in byoc.yaml declares `204: Empty response
// indicating the repository contains no data for the specified node` —
// a normal outcome for a node with no backups yet, not an error. It
// sits outside every other empty-body gate in the repo, all of which
// filter on cli.IsMutating, and it was broken: the *WithResponse
// wrapper has no typed 2xx case for 204, so the success landed on the
// generated catch-all and exited 1 with "unexpected end of JSON input".
//
// Both output paths are asserted, because they answer differently and
// only one of them was ever going to be noticed by hand: text narrates
// on stderr, and json/yaml print NOTHING rather than the `null` that
// rendering a nil pointer would produce (#141).
func TestBackupRepositoryGetSurvivesItsDeclared204(t *testing.T) {
	// Every shape this endpoint can answer with nothing, because one
	// of them is not obvious and cost a review round: `null` decodes
	// into a struct WITHOUT error and leaves it zero, so treating a
	// non-empty body as an object printed three blank fields at exit 0
	// -- a fabricated repository, which is the one thing #141 forbids.
	// `null` is byoc's own idiom for nothing, so it belongs here even
	// though this operation declares a 204 as well.
	shapes := []struct {
		name   string
		status int
		body   string
	}{
		{"204 with no body", http.StatusNoContent, ""},
		{"204 with a null body", http.StatusNoContent, "null"},
		{"200 with no body", http.StatusOK, ""},
		{"200 with a null body", http.StatusOK, "null"},
		{"200 with a trailing newline", http.StatusOK, "null\n"},
	}
	for _, shape := range shapes {
		for _, format := range []string{"text", "json", "yaml"} {
			t.Run(shape.name+"/"+format, func(t *testing.T) {
				rt, out, errOut := testsupport.NewRuntime(t, "", format)
				url := testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(shape.status, shape.body))
				if err := runAuthed(t, rt, out, url,
					"backup-repository", "get", testClusterID,
					"n1"); err != nil {
					t.Fatalf("an empty success was reported as a "+
						"failure: %v", err)
				}
				if got := out.String(); got != "" {
					t.Errorf("stdout = %q, want empty: nothing came "+
						"back, so there is nothing to print", got)
				}
				// The EXACT sentence of the nil branch. "No backup"
				// also matches the populated branch's "No backups
				// found.", so the loose form passed while the verb
				// narrated a fabricated repository with a blank id --
				// measured: 5 of 15 subtests were blind, and removing
				// the null guard reddened 4 rather than 6.
				const want = "No backup repository data returned."
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr = %q, want %q: the operator is "+
						"not being told the repository holds no data "+
						"for that node", errOut.String(), want)
				}
			})
		}
	}
}

// TestBackupRepositoryGetStillRejectsAMalformedBody is the negative
// control for the decode above. Skipping the wrapper must not also skip
// error detection: a body that is PRESENT and malformed is still a
// failure, or the test above would pass just as well against a verb
// that ignored the body entirely.
func TestBackupRepositoryGetStillRejectsAMalformedBody(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, `{"backups":`))
	if err := runAuthed(t, rt, out, url, "backup-repository", "get",
		testClusterID, "n1"); err == nil {
		t.Fatal("a truncated body was reported as success")
	}
}

// TestBackupRepositoryGetPrintsABodyCarryingANull is the positive
// control for the emptiness guard, and it exists because the guard is
// hand-rolled.
//
// The shape table above feeds only EMPTY bodies and asserts only
// silence, so it cannot tell "correctly silent" from "silent about
// everything". Widen `bytes.Equal(trimmed, []byte("null"))` to
// `bytes.Contains` -- the classic slip -- and a repository holding a
// real backup whose JSON carries one null-valued optional field is
// reported as holding none, at exit 0, with the whole suite green.
// Measured. That is worse than the fabrication the guard fixes,
// because a fabrication is visible and this is not.
//
// No other fixture in this package contains the substring `null`, so
// nothing else in the tree could have caught it.
func TestBackupRepositoryGetPrintsABodyCarryingANull(t *testing.T) {
	const body = `{"backup_repository_id":"` + testBackupRepoID + `",` +
		`"database_id":"` + testBackupRepoID + `","node_name":"n1",` +
		`"pg_version":null,"status":null,` +
		`"backups":[{"label":"20240619-195803F","type":"full"}]}`
	rt, out, _ := testsupport.NewRuntime(t, "", "json")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))
	if err := runAuthed(t, rt, out, url, "backup-repository", "get",
		testBackupRepoID, "n1"); err != nil {
		t.Fatalf("a real body was reported as a failure: %v", err)
	}
	if !strings.Contains(out.String(), "20240619-195803F") {
		t.Errorf("stdout = %q: a repository holding backups was "+
			"reported as holding none, because its JSON happens to "+
			"contain the substring `null`", out.String())
	}
}
