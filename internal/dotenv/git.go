package dotenv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

type ignoreState int

const (
	ignoreUnknown ignoreState = iota
	ignored
	notIgnored
	tracked
)

// WarnIfCommittable warns on stderr when git tracks path, or would
// commit it because it is not ignored. Ignoring a tracked file alone
// keeps committing it, so that case also names git rm --cached.
func WarnIfCommittable(rt *module.Runtime, path string) {
	switch gitIgnoreState(path) {
	case tracked:
		fmt.Fprintf(rt.Stderr, "Warning: git tracks %s, which now holds a "+
			"password. Run git rm --cached %s, then add it to .gitignore.\n",
			output.Sanitize(path), output.Sanitize(shellQuote(path)))
	case notIgnored:
		fmt.Fprintf(rt.Stderr, "Warning: git does not ignore %s, which now "+
			"holds a password. Add %s to .gitignore.\n",
			output.Sanitize(path), output.Sanitize(filepath.Base(path)))
	}
}

// gitIgnoreState asks git whether it tracks or ignores path. Anything
// but a clear answer, such as no git or no work tree, is ignoreUnknown
// and draws no warning.
func gitIgnoreState(path string) ignoreState {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ignoreUnknown
	}
	dir := filepath.Dir(abs)
	switch gitExit(dir, "ls-files", "--error-unmatch", "--", abs) {
	case 0:
		return tracked
	case 1:
	default:
		return ignoreUnknown
	}
	switch gitExit(dir, "check-ignore", "-q", "--", abs) {
	case 0:
		return ignored
	case 1:
		return notIgnored
	default:
		return ignoreUnknown
	}
}

// gitExit runs git in dir and returns its exit status, or -1 when git
// could not run or ran past the deadline.
func gitExit(dir string, args ...string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: fixed git subcommands, path is an argument
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		return exitErr.ExitCode()
	default:
		return -1
	}
}

// shellQuote returns path as one shell word. The path stays as given,
// relative to where the command ran, because a bare file name would
// untrack a different file from anywhere but that file's directory.
func shellQuote(path string) string {
	safe := path != ""
	for _, r := range path {
		word := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || strings.ContainsRune("._/-", r)
		if !word {
			safe = false
			break
		}
	}
	if safe {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
