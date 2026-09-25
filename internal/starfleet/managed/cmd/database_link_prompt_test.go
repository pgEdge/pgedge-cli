package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/projectlink"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testOtherBranchID = "0a1b2c3d-4e5f-6789-abcd-ef0123456789"

// pickStub answers the two list calls the prompt makes and hands every
// other request to linkStub.
type pickStub struct {
	linkStub
	databases string // the database list body
	branches  bool   // whether the database has branches
	listed    int    // list requests seen
}

func (s *pickStub) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/managed/v1/databases":
		s.listed++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.databases))
	case "/managed/v1/databases/" + testDatabaseID + "/branches":
		s.listed++
		w.Header().Set("Content-Type", "application/json")
		if !s.branches {
			_, _ = w.Write([]byte("[]"))
			return
		}
		creating := strings.Replace(branchJSON(testDatabaseID, testOtherBranchID),
			`"status":"available"`, `"status":"creating"`, 1)
		_, _ = fmt.Fprintf(w, "[%s,%s]", creating, branchJSON(testDatabaseID, testBranchID))
	default:
		s.linkStub.handler(w, r)
	}
}

// listOf is a database list holding testDatabaseID. It carries no
// branch_count, as the live list does not.
func listOf() string {
	return "[" + databaseJSON(testDatabaseID, "") + "]"
}

// asATerminal makes the command see a person at a terminal.
func asATerminal(t *testing.T, yes bool) {
	t.Helper()
	was := stdinIsTerminal
	stdinIsTerminal = func() bool { return yes }
	t.Cleanup(func() { stdinIsTerminal = was })
}

func TestDatabaseLinkPrompts(t *testing.T) {
	tests := []struct {
		name       string
		branches   bool
		stdin      string
		wantBranch string
		wantEnv    string // host in .env, "" for no .env
		wantErr    []string
	}{
		{name: "database without branches, .env declined", stdin: "1\nn\n",
			wantErr: []string{"Database [1-1]", "Run 'pgedge env pull'"}},
		{name: "bad answers asked again, .env by default", stdin: "x\n5\n1\n1\n\n",
			wantEnv: testConnHost, wantErr: []string{"Enter a number from 1 to 1.", "Answer y or n."}},
		{name: "Enter keeps the database", branches: true, stdin: "1\n\nn\n",
			wantErr: []string{"Enter) the database itself", "(not ready)"}},
		{name: "a branch, after refusing one not ready", branches: true, stdin: "1\n1\n2\nyes\n",
			wantBranch: testBranchID, wantEnv: testBranchHost,
			wantErr: []string{"That branch is creating, not available; choose another."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asATerminal(t, true)
			dir := inProject(t)
			rt, out, errb := testsupport.NewRuntime(t, tt.stdin, "text")
			stub := &pickStub{databases: listOf(), branches: tt.branches}
			url := testsupport.NewAuthedServer(t, stub.handler)

			if err := runAuthed(t, rt, out, url, "database", "link"); err != nil {
				t.Fatalf("link: %v\nstderr: %s", err, errb.String())
			}
			got, err := projectlink.Read(dir)
			if err != nil || got == nil || got.DatabaseID != testDatabaseID || got.BranchID != tt.wantBranch {
				t.Fatalf("link = %+v, %v; want database %s branch %q", got, err, testDatabaseID, tt.wantBranch)
			}
			env := filepath.Join(dir, ".env")
			if tt.wantEnv == "" {
				if _, err := os.Stat(env); !os.IsNotExist(err) {
					t.Errorf(".env written after a no: %v", err)
				}
			} else if got := readFile(t, env); got != "DATABASE_URL="+wantEnvURI(tt.wantEnv)+"\n" {
				t.Errorf(".env = %q", got)
			}
			if stub.listed != 2 {
				t.Errorf("list requests = %d, want the databases and the branches", stub.listed)
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(errb.String(), w) {
					t.Errorf("stderr lacks %q:\n%s", w, errb.String())
				}
			}
			if out.Len() != 0 {
				t.Errorf("stdout not empty: %q", out.String())
			}
		})
	}
}

func TestDatabaseLinkPromptRefuses(t *testing.T) {
	tests := []struct {
		name      string
		terminal  bool
		args      []string
		databases string
		stdin     string
		existing  bool
		code      int
		match     string
		wantLists int
	}{
		{name: "no terminal", args: nil, code: ExitUsage,
			match: "run this in a terminal to choose from a list"},
		{name: "--branch without the database", terminal: true,
			args: []string{"--branch", testBranchID}, code: ExitUsage, match: "--branch needs the database ID"},
		{name: "no databases", terminal: true, databases: "[]", code: ExitGeneral,
			match: "create --name <db-name> --link", wantLists: 1},
		{name: "input ends", terminal: true, databases: listOf(), stdin: "", code: ExitUsage,
			match: "aborted: nothing chosen", wantLists: 1},
		{name: "folder linked elsewhere", terminal: true, databases: listOf(), stdin: "1\n",
			existing: true, code: ExitGeneral, match: "pass --force", wantLists: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asATerminal(t, tt.terminal)
			dir := inProject(t)
			if tt.existing {
				writeLink(t, dir, testBranchID)
			}
			rt, out, _ := testsupport.NewRuntime(t, tt.stdin, "text")
			stub := &pickStub{databases: tt.databases}
			url := testsupport.NewAuthedServer(t, stub.handler)
			args := append([]string{"database", "link"}, tt.args...)
			wantExit(t, runAuthed(t, rt, out, url, args...), tt.code, tt.match)
			if stub.listed != tt.wantLists || len(stub.paths) != 0 {
				t.Errorf("requests: %d lists, others %v; want %d lists", stub.listed, stub.paths, tt.wantLists)
			}
			if got, _ := projectlink.Read(dir); (got != nil) != tt.existing {
				t.Errorf("link after refusal = %+v", got)
			}
			if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
				t.Errorf(".env written: %v", err)
			}
		})
	}
}

// An ID given on a terminal links as before: no list, no question.
func TestDatabaseLinkWithAnIDAsksNothing(t *testing.T) {
	asATerminal(t, true)
	inProject(t)
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	stub := &pickStub{}
	url := testsupport.NewAuthedServer(t, stub.handler)
	if err := runAuthed(t, rt, out, url, "database", "link", testDatabaseID); err != nil {
		t.Fatalf("link: %v", err)
	}
	if stub.listed != 0 || strings.Contains(errb.String(), "[Y/n]") {
		t.Errorf("asked with an ID: %d lists, stderr %q", stub.listed, errb.String())
	}
}
