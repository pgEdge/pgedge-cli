package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// --- list ---

func TestDatabaseBranchListRendersRows(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, "["+branchJSON(testDatabaseID, testBranchID)+"]"))

	err := runAuthed(t, rt, out, url,
		"database", "branch", "list", testDatabaseID)
	if err != nil {
		t.Fatalf("branch list: %v", err)
	}
	for _, want := range []string{testBranchID, "br-1", "available"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output missing %q; got:\n%s", want, out.String())
		}
	}
}

func TestDatabaseBranchListEmptyIsNotAnError(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, "[]"))

	if err := runAuthed(t, rt, out, url,
		"database", "branch", "list", testDatabaseID); err != nil {
		t.Fatalf("empty branch list treated as an error: %v", err)
	}
}

func TestDatabaseBranchListSendsFilters(t *testing.T) {
	var query string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "branch", "list",
		testDatabaseID, "--limit", "5", "--offset", "10",
		"--descending", "--include-deleted")
	if err != nil {
		t.Fatalf("branch list: %v", err)
	}
	for _, want := range []string{
		"limit=5", "offset=10", "descending=true", "include_deleted=true",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %q", query, want)
		}
	}
}

// --- get ---

func TestDatabaseBranchGetRendersRow(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(
		http.StatusOK, branchJSON(testDatabaseID, testBranchID)))

	err := runAuthed(t, rt, out, url,
		"database", "branch", "get", testDatabaseID, testBranchID)
	if err != nil {
		t.Fatalf("branch get: %v", err)
	}
	for _, want := range []string{testBranchID, "br-1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get output missing %q; got:\n%s", want, out.String())
		}
	}
}

// --- create ---

func TestDatabaseBranchCreateSendsDisplayName(t *testing.T) {
	var body string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			body = string(buf)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(branchJSON(testDatabaseID, testBranchID)))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "branch", "create",
		testDatabaseID, "--display-name", "dev-copy")
	if err != nil {
		t.Fatalf("branch create: %v", err)
	}
	if !strings.Contains(body, `"display_name":"dev-copy"`) {
		t.Errorf("body = %s, want display_name sent", body)
	}
}

func TestDatabaseBranchCreateOmitsDisplayNameByDefault(t *testing.T) {
	var body string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			body = string(buf)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(branchJSON(testDatabaseID, testBranchID)))
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url,
		"database", "branch", "create", testDatabaseID)
	if err != nil {
		t.Fatalf("branch create: %v", err)
	}
	if strings.Contains(body, "display_name") {
		t.Errorf("body = %s, must not send display_name when not given",
			body)
	}
}

// --- delete ---

func TestDatabaseBranchDeleteRequiresConfirmation(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "n\n", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusNoContent, ""))

	err := runAuthed(t, rt, out, url,
		"database", "branch", "delete", testDatabaseID, testBranchID)
	if err == nil {
		t.Fatal("branch delete proceeded without confirmation")
	}
}

func TestDatabaseBranchDeleteSendsRequest(t *testing.T) {
	var path, method string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			method = r.Method
			w.WriteHeader(http.StatusNoContent)
		})

	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "database", "branch", "delete",
		testDatabaseID, testBranchID, "--force")
	if err != nil {
		t.Fatalf("branch delete: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", method)
	}
	if !strings.Contains(path, testDatabaseID) ||
		!strings.Contains(path, testBranchID) {
		t.Errorf("path = %q, want both %s and %s",
			path, testDatabaseID, testBranchID)
	}
}
