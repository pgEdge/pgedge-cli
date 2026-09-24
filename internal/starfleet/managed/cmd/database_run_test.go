package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// validPipelines is a pipeline config file body that passes
// validatePipelines.
const validPipelines = `[{"name":"docs","tables":[
	{"table":"t","text_column":"c","vector_column":"v"}]}]`

// writePipelineConfig writes validPipelines to a temp file and returns
// its path.
func writePipelineConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pipelines.json")
	if err := os.WriteFile(path, []byte(validPipelines), 0o600); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}
	return path
}

// caseArgs resolves the PIPELINE_CONFIG placeholder in a case's args to
// a real temp file, so the table can stay hermetic and declarative.
func caseArgs(t *testing.T, tc managedCmdCase) []string {
	t.Helper()
	out := make([]string, len(tc.args))
	copy(out, tc.args)
	for i, a := range out {
		if a == "PIPELINE_CONFIG" {
			out[i] = writePipelineConfig(t)
		}
	}
	return out
}

// --- table-wide tests over the whole surface ---

// TestEveryManagedLeafHasACase pins managedCmdCases against the live
// tree. Without it a new verb could ship undriven by the transport,
// credential and empty-body tables that walk this list.
func TestEveryManagedLeafHasACase(t *testing.T) {
	covered := make(map[string]bool, len(managedCmdCases))
	for _, tc := range managedCmdCases {
		covered[tc.name] = true
	}

	root := NewManagedCmd(&module.Runtime{})
	var leaves []string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		kids := c.Commands()
		real := 0
		for _, k := range kids {
			if k.Name() == "help" || k.Hidden {
				continue
			}
			real++
			walk(k, append(path, k.Name()))
		}
		if real == 0 && len(path) > 0 {
			leaves = append(leaves, strings.Join(path, " "))
		}
	}
	walk(root, nil)

	// Positive control: the walk found the tree at all. A broken walk
	// returns zero leaves and looks exactly like full coverage.
	if len(leaves) < 16 {
		t.Fatalf("walk found only %d leaves; the tree has more — "+
			"has the walk broken?", len(leaves))
	}
	for _, leaf := range leaves {
		if !covered[leaf] {
			t.Errorf("leaf %q has no managedCmdCases entry", leaf)
		}
	}
}

// TestManagedTransportErrorsAreReported drives every command against a
// stub whose connection dies mid-response.
func TestManagedTransportErrorsAreReported(t *testing.T) {
	for _, tc := range managedCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.BrokenHandler())
			if err := runAuthed(t, rt, out, url,
				caseArgs(t, tc)...); err == nil {
				t.Fatal("a dead connection was reported as success")
			}
		})
	}
}

// TestManagedWithoutCredentialsFailsAuth drives every command with no
// credentials available from any source. managed borrows account's
// connection, so the failure must surface as an auth failure and not,
// say, a nil-pointer panic on a client that was never built.
func TestManagedWithoutCredentialsFailsAuth(t *testing.T) {
	for _, tc := range managedCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runManaged(t, rt, out, caseArgs(t, tc)...)
			if err == nil {
				t.Fatal("missing credentials reported as success")
			}
		})
	}
}

// TestManagedAPIErrorsAreReported drives every command against a stub
// that answers 500, and requires a non-zero exit.
func TestManagedAPIErrorsAreReported(t *testing.T) {
	for _, tc := range managedCmdCases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusInternalServerError,
					`{"code":500,"message":"boom"}`))
			if err := runAuthed(t, rt, out, url,
				caseArgs(t, tc)...); err == nil {
				t.Fatal("500 was reported as success")
			}
		})
	}
}

// --- list ---

func TestDatabaseListRendersRows(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	body := "[" + databaseJSON(testDatabaseID, "") + "]"
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))

	if err := runAuthed(t, rt, out, url, "database", "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	for _, want := range []string{"mydb", "available", "us-east-1", "small"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q; got:\n%s", want, got)
		}
	}
}

func TestDatabaseListEmptyIsNotAnError(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "[]"))

	if err := runAuthed(t, rt, out, url, "database", "list"); err != nil {
		t.Fatalf("empty list treated as an error: %v", err)
	}
}

func TestDatabaseListSendsFilters(t *testing.T) {
	var query string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "list",
		"--region", "eu-west-1", "--limit", "5", "--offset", "10",
		"--descending")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{
		"region=eu-west-1", "limit=5", "offset=10", "descending=true",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
}

// --- get ---

func TestDatabaseGetRendersRow(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, "")))

	err := runAuthed(t, rt, out, url, "database", "get", testDatabaseID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out.String(), "mydb") {
		t.Errorf("get output missing the name; got:\n%s", out.String())
	}
	// databaseJSON sends no ip_allowlist field at all, so State is nil
	// and Rules is empty; the summary row must still derive "closed"
	// rather than printing a blank STATE cell.
	if !strings.Contains(out.String(), "closed") {
		t.Errorf("a database with no ip_allowlist field must show closed; got:\n%s",
			out.String())
	}
}

// TestDatabaseGetRendersServicesSection pins the services block on the
// text path. `database get` is where a user asks "how do I call this",
// and uri is the locator it prints rather than leaving the answer only
// in `-o json`.
//
// The endpoint is asserted as a whole string. Asserting the domain
// alone would pass on a bare hostname, which is precisely the
// undiscoverable state the service uri fixed.
func TestDatabaseGetRendersServicesSection(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, databaseJSON(testDatabaseID, mcpServiceJSON)))

	err := runAuthed(t, rt, out, url, "database", "get", testDatabaseID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"mydb",
		"Services",
		"abc12345",
		"https://demo-db.use2.example.com/mcp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("get output missing %q; got:\n%s", want, got)
		}
	}
	// The secrets that come back on this path must not reach a terminal.
	for _, secret := range []string{"tok-stored", "sk-stored"} {
		if strings.Contains(got, secret) {
			t.Errorf("get output leaked %q; got:\n%s", secret, got)
		}
	}
}

// TestDatabaseGetOmitsServicesSectionWhenNone pins that a database with
// no services prints no heading. A bare "Services" over an empty table
// reads as a rendering fault.
//
// Both spellings of "none" are exercised. The API sends null rather
// than an empty array, so the absent case is the real one — but the
// guard covers an empty array too, and a guard nothing reaches is a
// guard nothing keeps honest.
func TestDatabaseGetOmitsServicesSectionWhenNone(t *testing.T) {
	for _, tc := range []struct {
		name, services string
	}{
		{"field absent", ""},
		{"empty array", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
				http.StatusOK, databaseJSON(testDatabaseID, tc.services)))

			err := runAuthed(t, rt, out, url,
				"database", "get", testDatabaseID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if strings.Contains(out.String(), "Services") {
				t.Errorf("services heading printed with no services; "+
					"got:\n%s", out.String())
			}
		})
	}
}

// TestDatabaseGetSendsUserType pins the --user-type normalisation:
// the CLI's canonical short forms (and their long-form aliases) all
// resolve to the wire value the get endpoint actually accepts. "app"
// alone used to round-trip verbatim and the live API rejected it with
// a 400.
func TestDatabaseGetSendsUserType(t *testing.T) {
	cases := []struct {
		flag, wantQuery string
	}{
		{"admin", "user_type=admin"},
		{"app", "user_type=application"},
		{"application", "user_type=application"},
		{"app_read_only", "user_type=application_read_only"},
		{"application_read_only", "user_type=application_read_only"},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			var query string
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					query = r.URL.RawQuery
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
				})

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "get",
				testDatabaseID, "--user-type", tc.flag)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if !strings.Contains(query, tc.wantQuery) {
				t.Errorf("query %q missing %s", query, tc.wantQuery)
			}
		})
	}
}

// TestDatabaseGetRejectsUnknownUserType pins that an unrecognised
// --user-type value never reaches the network: it fails client-side
// the same way rotate-password's --role does.
func TestDatabaseGetRejectsUnknownUserType(t *testing.T) {
	called := false
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "get",
		testDatabaseID, "--user-type", "superuser")
	if err == nil {
		t.Fatal("expected error on unknown user-type")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
	if called {
		t.Error("unknown user-type should not reach the server")
	}
}

// TestDatabaseGetRejectsAnEmptyUserType pins that an explicitly empty
// --user-type is a usage error rather than a silent no-op. It used to
// take the "not set" branch, so `--user-type ""` sent no user_type at
// all and answered 200 for the default role, while `--user-type bogus`
// was refused at exit 2. `--user-type "$ROLE"` with the variable unset
// reaches the CLI as "" and fell through that way.
func TestDatabaseGetRejectsAnEmptyUserType(t *testing.T) {
	called := false
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "get",
		testDatabaseID, "--user-type", "")
	if err == nil {
		t.Fatal("an empty --user-type was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
	if called {
		t.Error("an empty --user-type should not reach the server")
	}
	// The message is asserted, not just the code. A review found that
	// substituting --config's message verbatim passed every assertion
	// there, so each of these names something only THIS message can
	// supply: the flag, the three roles, and the way out.
	for _, want := range []string{
		"--user-type", "empty value", "admin, app or app_read_only",
		"omit the flag",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
	// Not the enum message: `unknown role ""` was what this used to
	// report, and it says nothing about what the caller did wrong.
	if strings.Contains(err.Error(), "unknown role") {
		t.Errorf("message %q is the enum message, not the empty-value "+
			"one", err.Error())
	}
}

// TestDatabaseGetOmitsUserTypeWhenUnset is the control for the test
// above: not passing the flag must still send no user_type. Without it,
// rejecting "" could be "fixed" by making the flag mandatory.
func TestDatabaseGetOmitsUserTypeWhenUnset(t *testing.T) {
	var query string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "get",
		testDatabaseID); err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(query, "user_type") {
		t.Errorf("query %q carries user_type with the flag unset", query)
	}
}

// --- create ---

func TestDatabaseCreateSendsRequiredFields(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
	}))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
		"--display-name", "My DB", "--pg-version", "16")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		t.Fatalf("request body was not JSON: %v (%q)", err, rec.Body)
	}
	for k, want := range map[string]any{
		"name": "mydb", "region": "us-east-1", "size": "small",
		"display_name": "My DB", "pg_version": "16",
	} {
		if body[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, body[k], want)
		}
	}
}

// TestDatabaseCreateRejectsAnUnknownPgVersion pins the client-side
// check. The API fixed --pg-version being ignored and closed the value
// set at the same time, so a typo that used to be dropped silently is
// now a 400 — and the version cannot be changed afterwards, so learning
// it locally for exit 2 is worth more here than on a flag that could be
// corrected later.
func TestDatabaseCreateRejectsAnUnknownPgVersion(t *testing.T) {
	for _, bad := range []string{"15", "17.2", "latest", "'18'"} {
		t.Run(bad, func(t *testing.T) {
			called := 0
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					called++
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(
						databaseJSON(testDatabaseID, "")))
				})

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "create",
				"--name", "mydb", "--region", "us-east-1",
				"--size", "small", "--pg-version", bad)
			if err == nil {
				t.Fatalf("--pg-version %q was accepted", bad)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Fatalf("want ExitUsage, got %v", err)
			}
			if called != 0 {
				t.Errorf("a rejected version still reached the API "+
					"(%d calls)", called)
			}
		})
	}
}

// TestDatabaseCreateRejectsAnEmptyPgVersion is the higher-stakes twin of
// the --user-type fix. validatePgVersion used to return nil for "", so
// `--pg-version ""` skipped the enum check entirely and created the
// database on the newest major. pg_version is fixed for the life of the
// database — the spec's own description says so and `database update`
// has no such field — so `--pg-version "$PGV"` with the variable unset
// produced a database on a version the caller neither chose nor can
// change.
func TestDatabaseCreateRejectsAnEmptyPgVersion(t *testing.T) {
	called := 0
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			called++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1",
		"--size", "small", "--pg-version", "")
	if err == nil {
		t.Fatal("an empty --pg-version was accepted; a database would " +
			"have been created on a version nobody chose")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Fatalf("want ExitUsage, got %v", err)
	}
	if called != 0 {
		t.Errorf("an empty --pg-version still reached the API "+
			"(%d calls)", called)
	}
	for _, want := range []string{
		"--pg-version", "empty value", "omit the flag",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
}

// TestDatabaseCreateOmitsPgVersionWhenUnset is the control: not passing
// the flag must still send no pg_version, so the API applies its own
// default. Without it, rejecting "" could be "fixed" by making the flag
// mandatory, which would break every create that wants the newest
// major.
func TestDatabaseCreateOmitsPgVersionWhenUnset(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t,
		withCatalog(func(w http.ResponseWriter, r *http.Request) {
			buf, _ := io.ReadAll(r.Body)
			rec.Body = string(buf)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
		}))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1",
		"--size", "small"); err != nil {
		t.Fatalf("create without --pg-version: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		t.Fatalf("request body was not JSON: %v (%q)", err, rec.Body)
	}
	if _, ok := body["pg_version"]; ok {
		t.Errorf("pg_version %v sent with the flag unset; the API's "+
			"own default is what an omitted flag must get",
			body["pg_version"])
	}
}

// TestDatabaseCreateAcceptsEverySpecPgVersion is the other direction:
// every major the contract publishes must reach the wire. Driving the
// command rather than the slice is what makes this catch a validation
// path that rejects a value pgVersions contains.
func TestDatabaseCreateAcceptsEverySpecPgVersion(t *testing.T) {
	for _, v := range pgVersions {
		t.Run(v, func(t *testing.T) {
			rec := &captureRequest{}
			url := testsupport.NewAuthedServer(t,
				withCatalog(func(
					w http.ResponseWriter, r *http.Request,
				) {
					buf, _ := io.ReadAll(r.Body)
					rec.Body = string(buf)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(
						databaseJSON(testDatabaseID, "")))
				}))

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, url, "database", "create",
				"--name", "mydb", "--region", "us-east-1",
				"--size", "small", "--pg-version", v); err != nil {
				t.Fatalf("--pg-version %s: %v", v, err)
			}

			var body map[string]any
			if err := json.Unmarshal(
				[]byte(rec.Body), &body); err != nil {
				t.Fatalf("request body was not JSON: %v (%q)",
					err, rec.Body)
			}
			if body["pg_version"] != v {
				t.Errorf("pg_version = %v, want %q",
					body["pg_version"], v)
			}
		})
	}
}

func TestDatabaseCreateRequiresNameRegionSize(t *testing.T) {
	for _, missing := range []string{"--name", "--region", "--size"} {
		t.Run(missing, func(t *testing.T) {
			args := []string{"database", "create",
				"--name", "d", "--region", "r", "--size", "s"}
			// Drop the flag under test and its value.
			var kept []string
			for i := 0; i < len(args); i++ {
				if args[i] == missing {
					i++
					continue
				}
				kept = append(kept, args[i])
			}
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runManaged(t, rt, out, kept...); err == nil {
				t.Fatalf("%s was not required", missing)
			}
		})
	}
}

// TestDatabaseCreateRejectsBadName pins that a --name that violates
// the documented rules (skills/pgedge-managed/SKILL.md) is rejected
// with ExitUsage before any request reaches the server.
func TestDatabaseCreateRejectsBadName(t *testing.T) {
	for _, bad := range []string{"My-DB", "my_db", "café", "1mydb"} {
		t.Run(bad, func(t *testing.T) {
			called := false
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				})
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "create",
				"--name", bad, "--region", "us-east-1", "--size", "small")
			if err == nil {
				t.Fatalf("%q was accepted, want rejected", bad)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Errorf("want exit %d, got %v", ExitUsage, err)
			}
			if called {
				t.Errorf("bad name %q should not reach the server", bad)
			}
		})
	}
}

// --- update ---

// TestDatabaseUpdateSendsOnlyChangedFields pins the partial-update
// contract: a flag the caller did not pass must not appear in the
// request at all, or the server would read it as an instruction.
func TestDatabaseUpdateSendsOnlyChangedFields(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, stubGetThenWrite(
		rec, databaseJSON(testDatabaseID, ""), http.StatusOK,
		databaseJSON(testDatabaseID, "")))

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "update",
		testDatabaseID, "--display-name", "renamed")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		t.Fatalf("request body was not JSON: %v (%q)", err, rec.Body)
	}
	if body["display_name"] != "renamed" {
		t.Errorf("display_name = %v, want renamed", body["display_name"])
	}
	if _, present := body["options"]; present {
		t.Error("options was sent despite not being passed")
	}
	if _, present := body["services"]; present {
		t.Error("services was sent by update; it would replace the list")
	}
}

// --- delete ---

func TestDatabaseDeleteRequiresConfirmation(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "n\n", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusNoContent, ""))

	// Without --force and with a declining answer the command must not
	// proceed.
	err := runAuthed(t, rt, out, url, "database", "delete", testDatabaseID)
	if err == nil {
		t.Fatal("delete proceeded without confirmation")
	}
}

// --force only skips the prompt; it must never imply the API's
// cascading force=true, or a script written before branches existed
// would start deleting them.
func TestDatabaseDeleteOmitsForceParamByDefault(t *testing.T) {
	var query string
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		})

	err := runAuthed(t, rt, out, url,
		"database", "delete", testDatabaseID, "--force")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if strings.Contains(query, "force") {
		t.Errorf("query = %q, must not send force without --delete-branches",
			query)
	}
}

// --delete-branches is the explicit, separate opt-in for the API's
// cascading delete, since branch data is unrecoverable.
func TestDatabaseDeleteBranchesSendsForceParam(t *testing.T) {
	var query string
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		})

	err := runAuthed(t, rt, out, url, "database", "delete", testDatabaseID,
		"--force", "--delete-branches")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(query, "force=true") {
		t.Errorf("query = %q, want force=true", query)
	}
}

// --delete-branches without --force must still stop for confirmation,
// same as any other destructive verb.
func TestDatabaseDeleteBranchesRequiresConfirmation(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "n\n", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusNoContent, ""))

	err := runAuthed(t, rt, out, url,
		"database", "delete", testDatabaseID, "--delete-branches")
	if err == nil {
		t.Fatal("delete --delete-branches proceeded without confirmation")
	}
}

// A resize is irreversible by the command's own documentation -- a
// database can grow but not shrink -- moves the database onto
// different infrastructure, and on a per-vCPU plan raises the bill.
// It ran unattended with no confirmation until a reviewer found it.
func TestDatabaseResizeRefusesWithoutForce(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, ""))
	err := runAuthed(t, rt, out, srv,
		"database", "resize", testDatabaseID, "--size", "large")
	if err == nil {
		t.Fatal("resize ran unattended with no --force")
	}
	if got := cli.ExitCode(err); got != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage(%d)", got, ExitUsage)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want the destructive-verb refusal", err)
	}
}

// display_name is on the response and in -o json, but the shared
// list/get table has no column for it, so text callers could not read
// back what they set. The detail view prints it when set.
func TestDatabaseGetShowsDisplayName(t *testing.T) {
	const body = `{"id":"` + testDatabaseID + `","name":"db1",` +
		`"status":"available","region":"us-east-2","size":"small",` +
		`"display_name":"My Database",` +
		`"created_at":"2024-03-15T10:30:00Z"}`
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))
	if err := runAuthed(t, rt, out, srv,
		"database", "get", testDatabaseID); err != nil {
		t.Fatalf("database get: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "My Database") {
		t.Errorf("detail view does not show the display name:\n%s", got)
	}
}

// The control: an absent display name prints no label. A blank
// "Display name:" line on every database is noise, and it is also how
// a reader would mistake "unset" for "set to empty".
func TestDatabaseGetOmitsAnAbsentDisplayName(t *testing.T) {
	const body = `{"id":"` + testDatabaseID + `","name":"db1",` +
		`"status":"available","region":"us-east-2","size":"small",` +
		`"created_at":"2024-03-15T10:30:00Z"}`
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srv := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))
	if err := runAuthed(t, rt, out, srv,
		"database", "get", testDatabaseID); err != nil {
		t.Fatalf("database get: %v", err)
	}
	if got := out.String(); strings.Contains(got, "Display name") {
		t.Errorf("printed a display-name label with nothing set:\n%s",
			got)
	}
}

func TestDatabaseCreateWithoutAllowlistSaysClosed(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID, "[]", "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(rec.Body, "ip_allowlist") {
		t.Errorf("no flag given, yet ip_allowlist was sent: %s", rec.Body)
	}
	if !strings.Contains(errOut.String(), "Created closed") ||
		!strings.Contains(errOut.String(), "allowlist add") {
		t.Errorf("closed note missing: %q", errOut.String())
	}
}

func TestDatabaseCreateAllowSendsRules(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID,
			`[{"cidr":"203.0.113.7/32"},{"cidr":"198.51.100.0/24"}]`, "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
		"--allow", "203.0.113.7", "--allow", "198.51.100.0/24"); err != nil {
		t.Fatalf("create --allow: %v", err)
	}
	rules, present, _ := allowlistBody(t, rec.Body)
	if !present || len(rules) != 2 || rules[0]["cidr"] != "203.0.113.7" {
		t.Errorf("rules = %v (present=%v)", rules, present)
	}
	if strings.Contains(errOut.String(), "Created closed") {
		t.Errorf("closed note printed although rules were given")
	}
}

// The controller ruling: a dead URL alone would also make this test
// pass on a connection failure, so the error text must name both
// flags cobra's mutually-exclusive check reports.
func TestDatabaseCreateMyIPAndOpenAreExclusive(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, "http://127.0.0.1:9", "database",
		"create", "--name", "mydb", "--size", "small", "--open", "--my-ip")
	if err == nil {
		t.Fatal("--open with --my-ip must be refused")
	}
	if !strings.Contains(err.Error(), "open") ||
		!strings.Contains(err.Error(), "my-ip") {
		t.Errorf("error does not name the conflicting flags: %v", err)
	}
}

func TestDatabaseCreateOpenSendsOpenRuleAndWarns(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID,
			`[{"cidr":"0.0.0.0/0"}]`, "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
		"--open"); err != nil {
		t.Fatalf("create --open: %v", err)
	}
	rules, _, _ := allowlistBody(t, rec.Body)
	if len(rules) != 1 || rules[0]["cidr"] != "0.0.0.0/0" {
		t.Errorf("rules = %v", rules)
	}
	if !strings.Contains(errOut.String(), "open to every address") {
		t.Errorf("no warning: %q", errOut.String())
	}
}

// TestDatabaseCreateOpenWarnsUnderJSON pins the fix for the bug the
// reviewer found: the open warning was nested inside the text-output
// branch, so `-o json` created a wide-open database and printed
// nothing. Stdout must stay pure JSON while stderr still carries the
// warning.
func TestDatabaseCreateOpenWarnsUnderJSON(t *testing.T) {
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID,
			`[{"cidr":"0.0.0.0/0"}]`, "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "json")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
		"--open"); err != nil {
		t.Fatalf("create --open -o json: %v", err)
	}
	if !strings.Contains(errOut.String(), "open to every address") {
		t.Errorf("no warning on stderr: %q", errOut.String())
	}
	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if body["id"] != testDatabaseID {
		t.Errorf("stdout id = %v, want %q", body["id"], testDatabaseID)
	}
}

// TestDatabaseCreateWithoutAllowlistSaysClosedUnderJSON is the
// no-flags twin of TestDatabaseCreateOpenWarnsUnderJSON: the closed
// note was equally suppressed under `-o json` before the hoist.
func TestDatabaseCreateWithoutAllowlistSaysClosedUnderJSON(t *testing.T) {
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID, "[]", "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "json")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
	); err != nil {
		t.Fatalf("create -o json: %v", err)
	}
	if !strings.Contains(errOut.String(), "Created closed") ||
		!strings.Contains(errOut.String(), "allowlist add") {
		t.Errorf("closed note missing on stderr: %q", errOut.String())
	}
	var body map[string]any
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if body["id"] != testDatabaseID {
		t.Errorf("stdout id = %v, want %q", body["id"], testDatabaseID)
	}
}

// TestDatabaseCreateMyIPAloneSendsFetchedAddress covers the branch
// TestDatabaseCreateMyIPAndOpenAreExclusive cannot reach: --my-ip on
// its own runs RunE's fetchClientIP call and folds the result into the
// rules sent on create. withCatalog's inner handler only routes
// /sizes and /regions, so this stub adds a third route for the
// client-ip GET the way stubAllowlist does for the allowlist verbs,
// then falls through to the create POST for everything else.
func TestDatabaseCreateMyIPAloneSendsFetchedAddress(t *testing.T) {
	rec := &captureRequest{}
	url := testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/client-ip") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ip_address":"203.0.113.9"}`))
			return
		}
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.Body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(allowlistDatabaseJSON(testDatabaseID,
			`[{"cidr":"203.0.113.9/32"}]`, "")))
	}))
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-1", "--size", "small",
		"--my-ip"); err != nil {
		t.Fatalf("create --my-ip: %v", err)
	}
	// fetchClientIP -> observedIPv4 returns the address with a /32
	// suffix (see client_ip.go); newRules stores inputs verbatim, so
	// the wire value carries the suffix too.
	rules, present, _ := allowlistBody(t, rec.Body)
	if !present || len(rules) != 1 ||
		rules[0]["cidr"] != "203.0.113.9/32" {
		t.Errorf("rules = %v (present=%v)", rules, present)
	}
	if !strings.Contains(errOut.String(), "not necessarily") {
		t.Errorf("client-ip caveat missing: %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "Created closed") {
		t.Errorf("closed note printed although --my-ip was given")
	}
}

func TestDatabaseGetPrintsAllowlistsSection(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, twoRulesJSON,
			mcpWithAllowlistJSON),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "get",
		testDatabaseID); err != nil {
		t.Fatalf("database get: %v", err)
	}
	text := out.String()
	for _, want := range []string{"Allowlists", "ENDPOINT", "STATE",
		"RULES", "postgres", "restricted", "mcp"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	// The Postgres row carries the count 2, the mcp row the count 1.
	idx := strings.Index(text, "postgres")
	if idx < 0 {
		t.Fatalf("output lacks a postgres row:\n%s", text)
	}
	pg := text[idx:]
	if !strings.Contains(strings.SplitN(pg, "\n", 2)[0], "2") {
		t.Errorf("postgres row lacks its rule count:\n%s", text)
	}
}

func TestDatabaseGetAllowlistsSectionAlwaysPrints(t *testing.T) {
	url := testsupport.NewAuthedServer(t, stubAllowlist(
		&captureRequest{},
		allowlistDatabaseJSON(testDatabaseID, "[]", ""),
		"203.0.113.9", http.StatusOK, ""))
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "get",
		testDatabaseID); err != nil {
		t.Fatalf("database get: %v", err)
	}
	if !strings.Contains(out.String(), "closed") {
		t.Errorf("a closed database must still show its posture:\n%s",
			out.String())
	}
}

// TestAllowlistSummaryColumnsMatchRow pins header and cell order
// together; they are two lists and have drifted before.
func TestAllowlistSummaryColumnsMatchRow(t *testing.T) {
	row := allowlistSummaryRow{endpoint: "E", state: "S", rules: "R"}
	cols := row.Columns()
	if len(cols) != len(allowlistSummaryColumns) {
		t.Fatalf("row has %d cells, header %d", len(cols),
			len(allowlistSummaryColumns))
	}
	want := map[string]string{"ENDPOINT": "E", "STATE": "S", "RULES": "R"}
	for i, h := range allowlistSummaryColumns {
		if !strings.Contains(cols[i], want[h]) {
			t.Errorf("column %d %q holds %q", i, h, cols[i])
		}
	}
}

func TestAllowlistRuleColumnsMatchRow(t *testing.T) {
	row := allowlistRuleRow{cidr: "C", label: "L"}
	cols := row.Columns()
	if len(cols) != 2 || cols[0] != "C" || cols[1] != "L" {
		t.Errorf("CIDR/LABEL order broken: %v", cols)
	}
}
