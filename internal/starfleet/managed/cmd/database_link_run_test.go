package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/projectlink"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testBranchHost = "br.example.pgedge.cloud"

// linkStub answers the database and branch GETs and records each path
// asked for. Either can be switched to a 404.
type linkStub struct {
	mu            sync.Mutex
	paths         []string
	userTypes     []string
	dbMissing     bool
	branchMissing bool
	noHost        bool
}

func (s *linkStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	s.userTypes = append(s.userTypes, r.URL.Query().Get("user_type"))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	branchPath := "/managed/v1/databases/" + testDatabaseID + "/branches/" + testBranchID
	switch r.URL.Path {
	case "/managed/v1/databases/" + testDatabaseID:
		if s.dbMissing {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
			return
		}
		if s.noHost {
			_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
			return
		}
		_, _ = w.Write([]byte(databaseWithConnectionJSON(testDatabaseID)))
	case branchPath:
		if s.branchMissing {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"message":"not found"}`))
			return
		}
		pw, _ := json.Marshal(testConnPassword)
		b := strings.TrimSuffix(branchJSON(testDatabaseID, testBranchID), "}")
		_, _ = fmt.Fprintf(w, `%s,"connection":{"host":%q,"port":5432,"database":"mydb","username":"app","password":%s}}`,
			b, testBranchHost, pw)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"unexpected path"}`))
	}
}

// inProject moves the test into a fresh project folder and returns it.
func inProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func writeLink(t *testing.T, dir, branch string) {
	t.Helper()
	if _, err := projectlink.Write(dir, projectlink.Link{
		Module: projectlink.ModuleManaged, DatabaseID: testDatabaseID, BranchID: branch,
	}); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func wantExit(t *testing.T, err error, code int, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want exit %d, got success", code)
	}
	var ue *cli.UsageError
	switch {
	case code == ExitUsage && asUsageError(err, &ue):
	default:
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != code {
			t.Fatalf("want exit %d, got %v (%T)", code, err, err)
		}
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("error %q does not contain %q", err, contains)
	}
}

func asUsageError(err error, target **cli.UsageError) bool {
	ue, ok := err.(*cli.UsageError)
	if ok {
		*target = ue
	}
	return ok
}

// wantEnvURI is the value env pull writes for testConnPassword on
// host: EnvFileURI's encoding, which leaves no $ or # for a loader.
func wantEnvURI(host string) string {
	return "postgresql://app:p%40ss%3Aw%2Frd%3F%23%26%27x%5Cy%C2%A0z@" +
		host + ":5432/mydb?sslmode=require"
}

func TestDatabaseLinkWritesTheLink(t *testing.T) {
	tests := []struct {
		name, branch string
		extra        []string
		wantPaths    int
	}{
		{"database", "", nil, 1},
		{"branch", testBranchID, []string{"--branch", testBranchID}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := inProject(t)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			stub := &linkStub{}
			url := testsupport.NewAuthedServer(t, stub.handler)

			args := append([]string{"database", "link", testDatabaseID}, tt.extra...)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("link: %v", err)
			}
			got, err := projectlink.Read(dir)
			if err != nil || got == nil {
				t.Fatalf("Read = %v, %v", got, err)
			}
			if got.DatabaseID != testDatabaseID || got.BranchID != tt.branch {
				t.Errorf("link = %+v", got.Link)
			}
			if out.Len() != 0 {
				t.Errorf("stdout not empty: %q", out.String())
			}
			if !strings.Contains(errb.String(), "Linked "+dir) {
				t.Errorf("stderr = %q", errb.String())
			}
			if len(stub.paths) != tt.wantPaths {
				t.Errorf("requests = %v, want %d", stub.paths, tt.wantPaths)
			}
		})
	}
}

func TestDatabaseLinkWritesNothingForAnUnseenTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		stub *linkStub
		args []string
	}{
		{"database 404", &linkStub{dbMissing: true}, nil},
		{"branch 404", &linkStub{branchMissing: true}, []string{"--branch", testBranchID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := inProject(t)
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, tc.stub.handler)
			args := append([]string{"database", "link", testDatabaseID}, tc.args...)
			wantExit(t, runAuthed(t, rt, out, url, args...), ExitNotFound, "404")
			if _, err := os.Stat(filepath.Join(dir, projectlink.Dir)); !os.IsNotExist(err) {
				t.Errorf("%s was created: %v", projectlink.Dir, err)
			}
		})
	}
}

func TestDatabaseLinkRefusesToReplaceWithoutForce(t *testing.T) {
	dir := inProject(t)
	writeLink(t, dir, testBranchID)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	stub := &linkStub{}
	url := testsupport.NewAuthedServer(t, stub.handler)

	wantExit(t, runAuthed(t, rt, out, url, "database", "link", testDatabaseID),
		ExitGeneral, "pass --force")
	if len(stub.paths) != 0 {
		t.Errorf("refusal sent requests: %v", stub.paths)
	}

	rt, out, _ = testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "link", testDatabaseID, "--force"); err != nil {
		t.Fatalf("link --force: %v", err)
	}
	if got, _ := projectlink.Read(dir); got.BranchID != "" {
		t.Errorf("--force kept the old branch: %+v", got.Link)
	}

	// The same link again is not a replacement.
	rt, out, _ = testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "link", testDatabaseID); err != nil {
		t.Errorf("relinking the same database: %v", err)
	}
}

func TestDatabaseLinkRefusesABadBranchID(t *testing.T) {
	inProject(t)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "link", testDatabaseID, "--branch", "nope"),
		ExitUsage, "invalid branch ID")
}

func TestDatabaseUnlink(t *testing.T) {
	dir := inProject(t)
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	wantExit(t, runManaged(t, rt, out, "database", "unlink"), ExitGeneral, "no .pgedge/link.yaml")

	writeLink(t, dir, "")
	if err := runManaged(t, rt, out, "database", "unlink"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, projectlink.Dir)); !os.IsNotExist(err) {
		t.Errorf("%s left behind: %v", projectlink.Dir, err)
	}
	if !strings.Contains(errb.String(), "Removed the database link") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestEnvPull(t *testing.T) {
	tests := []struct {
		name     string
		branch   string // link's branch; "" for a database link
		args     []string
		fromSub  bool // run from a subfolder of the project
		noLink   bool
		wantFile string // relative to the project folder
		wantHost string
		wantVar  string
		wantUser string
	}{
		{name: "linked database from a subfolder", fromSub: true,
			wantFile: ".env", wantHost: testConnHost, wantVar: "DATABASE_URL"},
		{name: "linked branch", branch: testBranchID,
			wantFile: ".env", wantHost: testBranchHost, wantVar: "DATABASE_URL"},
		{name: "explicit ID writes in the current folder", noLink: true, fromSub: true,
			args: []string{testDatabaseID}, wantFile: "sub/.env",
			wantHost: testConnHost, wantVar: "DATABASE_URL"},
		{name: "--branch pulls a branch past a database link",
			args: []string{"--branch", testBranchID}, wantFile: ".env",
			wantHost: testBranchHost, wantVar: "DATABASE_URL"},
		{name: "--branch wins over a branch link", branch: testOtherBranchID,
			args: []string{"--branch", testBranchID}, wantFile: ".env",
			wantHost: testBranchHost, wantVar: "DATABASE_URL"},
		{name: "--branch with an explicit ID", noLink: true,
			args: []string{testDatabaseID, "--branch", testBranchID}, wantFile: ".env",
			wantHost: testBranchHost, wantVar: "DATABASE_URL"},
		{name: "--file, --var and --user-type",
			args:     []string{"--file", "custom.env", "--var", "PG_URL", "--user-type", "app_read_only"},
			wantFile: "custom.env", wantHost: testConnHost, wantVar: "PG_URL",
			wantUser: "application_read_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := inProject(t)
			if !tt.noLink {
				writeLink(t, dir, tt.branch)
			}
			if tt.fromSub {
				sub := filepath.Join(dir, "sub")
				if err := os.Mkdir(sub, 0o750); err != nil {
					t.Fatal(err)
				}
				t.Chdir(sub)
			}
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			stub := &linkStub{}
			url := testsupport.NewAuthedServer(t, stub.handler)

			args := append([]string{"database", "env", "pull"}, tt.args...)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("env pull: %v", err)
			}
			path := filepath.Join(dir, tt.wantFile)
			if tt.wantFile == "custom.env" {
				path = filepath.Join(dir, "custom.env")
			}
			want := tt.wantVar + "=" + wantEnvURI(tt.wantHost) + "\n"
			if got := readFile(t, path); got != want {
				t.Errorf("%s:\n got %q\nwant %q", tt.wantFile, got, want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout not empty: %q", out.String())
			}
			if !strings.Contains(errb.String(), "Set "+tt.wantVar) {
				t.Errorf("no acknowledgement on stderr: %q", errb.String())
			}
			if !tt.noLink && !strings.Contains(errb.String(), "Using database "+testDatabaseID) {
				t.Errorf("no note naming the linked database: %q", errb.String())
			}
			if !tt.noLink {
				if got, _ := projectlink.Read(dir); got == nil || got.BranchID != tt.branch {
					t.Errorf("env pull changed the link: %+v", got)
				}
			}
			last := stub.userTypes[len(stub.userTypes)-1]
			if last != tt.wantUser {
				t.Errorf("user_type = %q, want %q", last, tt.wantUser)
			}
		})
	}
}

func TestEnvPullValueRoundTrips(t *testing.T) {
	u, err := neturl.Parse(wantEnvURI(testConnHost))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := u.User.Password(); p != testConnPassword {
		t.Errorf("password parses back as %q, want %q", p, testConnPassword)
	}
}

func TestEnvPullRefuses(t *testing.T) {
	tests := []struct {
		name  string
		stub  *linkStub
		args  []string
		link  bool
		code  int
		match string
	}{
		{"no link and no ID", &linkStub{}, nil, false, ExitUsage, "no database ID given"},
		{"empty --user-type", &linkStub{}, []string{testDatabaseID, "--user-type", ""}, false, ExitUsage, "--user-type given an empty value"},
		{"unknown --user-type", &linkStub{}, []string{testDatabaseID, "--user-type", "root"}, false, ExitUsage, "unknown user type"},
		{"empty --file", &linkStub{}, []string{testDatabaseID, "--file", ""}, false, ExitUsage, "--file given an empty value"},
		{"bad --var", &linkStub{}, []string{testDatabaseID, "--var", "1X"}, false, ExitUsage, "is not a variable name"},
		{"empty --var", &linkStub{}, []string{testDatabaseID, "--var", ""}, false, ExitUsage, "is not a variable name"},
		{"bad --branch", &linkStub{}, []string{testDatabaseID, "--branch", "nope"}, false, ExitUsage, "invalid branch ID"},
		{"no host yet", &linkStub{noHost: true}, nil, true, ExitGeneral, "no connection host yet"},
		{"database gone", &linkStub{dbMissing: true}, nil, true, ExitNotFound, "404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := inProject(t)
			if tt.link {
				writeLink(t, dir, "")
			}
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, tt.stub.handler)
			args := append([]string{"database", "env", "pull"}, tt.args...)
			wantExit(t, runAuthed(t, rt, out, url, args...), tt.code, tt.match)
			if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
				t.Errorf(".env was written: %v", err)
			}
		})
	}
}

func TestEnvPullWarnsWhenGitDoesNotIgnoreTheFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, tc := range []struct {
		name, gitignore string
		track           bool
		want            string // "" for no warning
	}{
		{"not ignored", "", false, "git does not ignore"},
		{"ignored", ".env\n", false, ""},
		{"tracked, even though ignored", ".env\n", true, "git rm --cached .env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := inProject(t)
			if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
				t.Fatalf("git init: %v", err)
			}
			if tc.gitignore != "" {
				if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(tc.gitignore), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.track {
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := exec.Command("git", "-C", dir, "add", "-f", ".env").Run(); err != nil {
					t.Fatalf("git add: %v", err)
				}
			}
			writeLink(t, dir, "")
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
			if err := runAuthed(t, rt, out, url, "database", "env", "pull"); err != nil {
				t.Fatalf("env pull: %v", err)
			}
			warned := strings.Contains(errb.String(), "Warning:")
			switch {
			case tc.want == "" && warned:
				t.Errorf("unexpected warning: %q", errb.String())
			case tc.want != "" && !strings.Contains(errb.String(), tc.want):
				t.Errorf("stderr %q does not contain %q", errb.String(), tc.want)
			}
		})
	}
}

// TestReadVerbsTakeTheLinkedDatabase drives every read verb with its
// ID left out. Each must reach the API for the linked database; the
// stub answers only the database GET, so a verb needing more fails
// after that, which is fine. A verb that still reads args[0] panics.
func TestReadVerbsTakeTheLinkedDatabase(t *testing.T) {
	verbs := [][]string{
		{"database", "get"},
		{"database", "connection-string"},
		{"database", "inspect", "table-sizes"},
		{"database", "logs"},
		{"database", "metrics"},
		{"database", "allowlist", "get"},
		{"database", "allowlist", "get", "--service", "mcp"},
		{"database", "branch", "list"},
	}
	for _, args := range verbs {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := inProject(t)
			writeLink(t, dir, "")
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			stub := &linkStub{}
			url := testsupport.NewAuthedServer(t, stub.handler)
			err := runAuthed(t, rt, out, url, args...)
			var ue *cli.UsageError
			if asUsageError(err, &ue) {
				t.Fatalf("usage error despite the link: %v", err)
			}
			if len(stub.paths) == 0 || !strings.Contains(stub.paths[0], testDatabaseID) {
				t.Errorf("requests = %v, want the linked database first", stub.paths)
			}
			if !strings.Contains(errb.String(), "Using database "+testDatabaseID) {
				t.Errorf("stderr = %q", errb.String())
			}
		})
	}
}

func TestConnectionStringFollowsABranchLink(t *testing.T) {
	dir := inProject(t)
	writeLink(t, dir, testBranchID)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	if err := runAuthed(t, rt, out, url, "database", "connection-string", "--no-password"); err != nil {
		t.Fatal(err)
	}
	want := "postgresql://app@" + testBranchHost + ":5432/mydb?sslmode=require\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestInspectWithOneArgumentNeedsALink(t *testing.T) {
	inProject(t)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "inspect", "table-sizes"),
		ExitUsage, "no database ID given")
}

func TestDatabaseArgRefusesALinkFromAnotherModule(t *testing.T) {
	dir := inProject(t)
	if err := os.MkdirAll(filepath.Join(dir, projectlink.Dir), 0o750); err != nil {
		t.Fatal(err)
	}
	// Validate refuses unknown modules, so a foreign link can only be
	// one projectlink knows but managed does not own. Simulate it by
	// registering a second module for the duration of the test.
	projectlink.CommandPaths["starfleet/other"] = []string{"x", "pull"}
	t.Cleanup(func() { delete(projectlink.CommandPaths, "starfleet/other") })
	body := "module: starfleet/other\ndatabase_id: " + testDatabaseID + "\n"
	if err := os.WriteFile(filepath.Join(dir, projectlink.Dir, projectlink.File), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "get"), ExitGeneral, "not a managed one")
}

func TestCreateLink(t *testing.T) {
	createArgs := []string{"database", "create", "--name", "d",
		"--region", "us-east-2", "--size", "small", "--link"}

	t.Run("links after a successful wait", func(t *testing.T) {
		dir := inProject(t)
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, withCatalog(waitFlowHandler("succeeded", "")))
		args := append(append([]string{}, createArgs...),
			"--wait", "--wait-interval", "1", "--wait-timeout", "30")
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("create --link: %v", err)
		}
		got, err := projectlink.Read(dir)
		if err != nil || got == nil || got.DatabaseID != testDatabaseID {
			t.Fatalf("link = %+v, %v", got, err)
		}
		if !strings.Contains(errb.String(), "Linked "+dir) {
			t.Errorf("stderr = %q", errb.String())
		}
	})

	t.Run("a failed task writes no link", func(t *testing.T) {
		dir := inProject(t)
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, withCatalog(waitFlowHandler("failed", "quota exceeded")))
		args := append(append([]string{}, createArgs...),
			"--wait", "--wait-interval", "1", "--wait-timeout", "30")
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("want an error")
		}
		if got, _ := projectlink.Read(dir); got != nil {
			t.Errorf("a failed create wrote a link: %+v", got.Link)
		}
	})

	t.Run("needs --wait or --follow", func(t *testing.T) {
		inProject(t)
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.BrokenHandler())
		wantExit(t, runAuthed(t, rt, out, url, createArgs...), ExitUsage, "--link needs --wait")
	})

	t.Run("an existing link is refused before the create", func(t *testing.T) {
		dir := inProject(t)
		writeLink(t, dir, "")
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var mu sync.Mutex
		requests := 0
		url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests++
			mu.Unlock()
			withCatalog(waitFlowHandler("succeeded", ""))(w, r)
		})
		args := append(append([]string{}, createArgs...), "--wait")
		wantExit(t, runAuthed(t, rt, out, url, args...), ExitGeneral, "already links database")
		if requests != 0 {
			t.Errorf("%d requests sent before the refusal", requests)
		}
	})
}

// TestRootEnvPullRunsTheModuleVerb drives `pgedge env pull` through a
// root holding both it and the starfleet tree, as the binary does.
func TestRootEnvPullRunsTheModuleVerb(t *testing.T) {
	dir := inProject(t)
	writeLink(t, dir, "")
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	stub := &linkStub{}
	url := testsupport.NewAuthedServer(t, stub.handler)

	root := &cobra.Command{Use: "pgedge", SilenceUsage: true, SilenceErrors: true}
	starfleet := newStarfleetRoot(rt)
	root.AddCommand(cli.NewEnvCmd(rt), starfleet)
	// Root `env pull` has no connection flags, so the stub is reached
	// the way a profile would reach the API: through the module's own.
	for name, v := range map[string]string{"api-url": url, "client-id": "id", "client-secret": "secret"} {
		if err := starfleet.PersistentFlags().Set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	root.SetArgs([]string{"env", "pull", "--var", "APP_DB", "--user-type", "admin"})
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatalf("env pull: %v", err)
	}
	want := "APP_DB=" + wantEnvURI(testConnHost) + "\n"
	if got := readFile(t, filepath.Join(dir, ".env")); got != want {
		t.Errorf(".env = %q, want %q", got, want)
	}
	if got := stub.userTypes[len(stub.userTypes)-1]; got != "admin" {
		t.Errorf("--user-type did not reach the module verb: %q", got)
	}
	if !strings.Contains(errb.String(), "Set APP_DB") {
		t.Errorf("stderr = %q", errb.String())
	}

	t.Run("--branch", func(t *testing.T) {
		root.SetArgs([]string{"env", "pull", "--branch", testBranchID})
		if err := root.Execute(); err != nil {
			t.Fatalf("env pull --branch: %v", err)
		}
		// The root keeps --var APP_DB from the run above.
		want := "APP_DB=" + wantEnvURI(testBranchHost) + "\n"
		if got := readFile(t, filepath.Join(dir, ".env")); got != want {
			t.Errorf(".env = %q, want %q", got, want)
		}
	})

	t.Run("no link", func(t *testing.T) {
		inProject(t)
		root.SetArgs([]string{"env", "pull"})
		err := root.Execute()
		var ue *cli.UsageError
		if !asUsageError(err, &ue) || !strings.Contains(err.Error(), "no .pgedge/link.yaml") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestEnvPullRefusesBadFlagsBeforeAnyRequest(t *testing.T) {
	inProject(t)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	stub := &linkStub{}
	url := testsupport.NewAuthedServer(t, stub.handler)
	_ = runAuthed(t, rt, out, url, "database", "env", "pull", testDatabaseID, "--var", "1X")
	if len(stub.paths) != 0 {
		t.Errorf("a bad --var still fetched the password: %v", stub.paths)
	}
}

func TestEnvPullRefusesASymlinkedEnvFile(t *testing.T) {
	dir := inProject(t)
	writeLink(t, dir, "")
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "env", "pull"), ExitGeneral, "symbolic link")
	if got := readFile(t, target); got != "A=1\n" {
		t.Errorf("the symlink's target was written: %q", got)
	}
}

func TestTheHomeFolderIsNeverLinked(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"link", []string{"database", "link", testDatabaseID}},
		{"unlink", []string{"database", "unlink"}},
		{"create --link", []string{"database", "create", "--name", "d",
			"--region", "us-east-2", "--size", "small", "--link", "--wait"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			home := os.Getenv("HOME")
			t.Chdir(home)
			stub := &linkStub{}
			url := testsupport.NewAuthedServer(t, stub.handler)
			wantExit(t, runAuthed(t, rt, out, url, tt.args...), ExitUsage, "home folder cannot be linked")
			if len(stub.paths) != 0 {
				t.Errorf("requests sent: %v", stub.paths)
			}
			if _, err := os.Stat(filepath.Join(home, projectlink.Dir)); !os.IsNotExist(err) {
				t.Errorf("~/%s was created: %v", projectlink.Dir, err)
			}
		})
	}
}

func TestLinkForceReplacesABrokenLink(t *testing.T) {
	dir := inProject(t)
	if err := os.MkdirAll(filepath.Join(dir, projectlink.Dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projectlink.Dir, projectlink.File), []byte("module: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, (&linkStub{}).handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "link", testDatabaseID), ExitGeneral, "pass --force")

	rt, out, _ = testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "link", testDatabaseID, "--force"); err != nil {
		t.Fatalf("link --force over a broken link: %v", err)
	}
	if got, err := projectlink.Read(dir); err != nil || got == nil || got.DatabaseID != testDatabaseID {
		t.Errorf("link = %+v, %v", got, err)
	}
}

func TestInspectChecksTheAnalysisBeforeTheLink(t *testing.T) {
	dir := inProject(t)
	writeLink(t, dir, "")
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	stub := &linkStub{}
	url := testsupport.NewAuthedServer(t, stub.handler)
	wantExit(t, runAuthed(t, rt, out, url, "database", "inspect", testDatabaseID), ExitUsage, "unknown analysis")
	if strings.Contains(errb.String(), "Using database") {
		t.Errorf("named the linked database before refusing the analysis: %q", errb.String())
	}
	if len(stub.paths) != 0 {
		t.Errorf("requests sent: %v", stub.paths)
	}
}
