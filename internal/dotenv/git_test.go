package dotenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
)

func TestWarnIfCommittable(t *testing.T) {
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
			dir := t.TempDir()
			if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
				t.Fatalf("git init: %v", err)
			}
			if tc.gitignore != "" {
				if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(tc.gitignore), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(dir, ".env")
			if err := os.WriteFile(file, []byte("A=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.track {
				if err := exec.Command("git", "-C", dir, "add", "-f", ".env").Run(); err != nil {
					t.Fatalf("git add: %v", err)
				}
			}
			var errb bytes.Buffer
			WarnIfCommittable(&module.Runtime{Stderr: &errb}, file)
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
