package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func runEnvPull(t *testing.T, body string, args ...string,
) (stdout, stderr string, err error) {
	t.Helper()
	rt, out, errOut := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK, body))
	full := append([]string{"database", "env", "pull", testDatabaseID},
		args...)
	err = runAuthed(t, rt, out, url, full...)
	return out.String(), errOut.String(), err
}

func TestByocEnvPullWritesAPrivateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env")
	out, errOut, err := runEnvPull(t,
		byocDatabaseWithNodesJSON(publicNode, privateNode),
		"--node", "east", "--internal", "--file", file)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "node east") {
		t.Errorf("stderr does not name the node: %q", errOut)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	got, _ := os.ReadFile(file) //nolint:gosec // G304: the test's own file
	want := "DATABASE_URL=postgresql://admin:" +
		"p%40ss%3Aw%2Frd%3F%23%26%27x%5Cy%C2%A0z@" + testPrivateHost +
		":5432/defaultdb?sslmode=require\n"
	if string(got) != want {
		t.Errorf("file:\n got %q\nwant %q", got, want)
	}
}

// Exit 2 proves the refusal came before the fetch: this body, fetched,
// is a database with no nodes, which is exit 1.
func TestByocEnvPullRefusesBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--var", "1BAD"},
		{"--file", ""},
		{"--node", ""},
	} {
		t.Run(args[0], func(t *testing.T) {
			_, _, err := runEnvPull(t, "{}", args...)
			requireExitCode(t, err, ExitUsage)
		})
	}
}

func TestByocEnvPullSeveralNodesWritesNothing(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".env")
	out, _, err := runEnvPull(t,
		byocDatabaseWithNodesJSON(publicNode, privateNode), "--file", file)
	requireExitCode(t, err, ExitUsage)
	if !strings.Contains(out, testPrivateHost) {
		t.Errorf("node listing missing:\n%s", out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file written despite no --node: %v", err)
	}
}
