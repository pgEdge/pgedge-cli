package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/httplog"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// ErrGHUnauthenticated marks a gh failure classified as an
// unauthenticated session (gh auth status itself failing after a
// fetch failed), distinct from any other reason gh might fail. The
// caller uses errors.Is to pick exit 5 over the generic exit 1.
var ErrGHUnauthenticated = errors.New("gh is not authenticated")

// ghAuthError is the concrete unauthenticated classification: it
// matches ErrGHUnauthenticated under errors.Is and keeps gh's own
// stderr separately, so Chain can re-report the same failure WITHOUT
// the classification when the HTTP rung has already shown GitHub to
// be unreachable.
type ghAuthError struct{ stderr string }

func (e *ghAuthError) Error() string {
	if e.stderr == "" {
		return ErrGHUnauthenticated.Error()
	}
	return ErrGHUnauthenticated.Error() + ": " + e.stderr
}

func (e *ghAuthError) Is(target error) bool { return target == ErrGHUnauthenticated }

// unclassified is the same failure reported as a plain gh error.
func (e *ghAuthError) unclassified() error {
	if e.stderr == "" {
		return errors.New("gh: exited without a message")
	}
	return fmt.Errorf("gh: %s", e.stderr)
}

// Runner constructs the *exec.Cmd for one invocation of an external
// command. Production code passes exec.CommandContext; tests inject a
// stub that re-execs the test binary instead of the real gh, so no
// test ever shells out to a real gh.
//
// The context is the deadline seam: a child process has no timeout of
// its own, so without it an unauthenticated `gh` sitting on a prompt
// hangs the command with nothing to stop it.
type Runner func(ctx context.Context, name string, args ...string) *exec.Cmd

// GHSource fetches releases and downloads assets by shelling out to
// the gh CLI — the ladder's fallback rung, used when the
// unauthenticated HTTP rung fails.
type GHSource struct {
	run  Runner
	diag io.Writer
	lvl  httplog.Level
}

// NewGHSource builds a GHSource that invokes commands through run.
func NewGHSource(run Runner) *GHSource {
	return &GHSource{run: run}
}

// Logging renders each gh invocation to out at Verbose or above — the
// command line, then its exit status and elapsed time — so --verbose
// and --debug show which rung a failure came from. gh's own
// stderr is already carried in the returned error, so it is not
// repeated here. It returns s for chaining.
func (s *GHSource) Logging(out io.Writer, lvl httplog.Level) *GHSource {
	s.diag = out
	s.lvl = lvl
	return s
}

// exec runs cmd, rendering it to the diagnostic stream when one is
// set as `gh <args>` — the invocation as a user would type it, not
// cmd.Args, which a test's Runner points at a stub binary.
func (s *GHSource) exec(cmd *exec.Cmd, args ...string) error {
	if s.diag == nil || s.lvl < httplog.Verbose {
		return cmd.Run()
	}
	fmt.Fprintf(s.diag, "> gh %s\n", output.Sanitize(strings.Join(args, " ")))
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Fprintf(s.diag, "< %v (%s)\n", err, elapsed)
	} else {
		fmt.Fprintf(s.diag, "< exit 0 (%s)\n", elapsed)
	}
	return err
}

// Releases execs `gh api repos/pgEdge/pgedge-cli/releases?per_page=30`,
// which returns the same JSON shape as the plain REST API.
func (s *GHSource) Releases(ctx context.Context) ([]Release, error) {
	args := []string{"api", fmt.Sprintf("repos/%s/%s/releases?per_page=30", repoOwner, repoName)}
	cmd := s.run(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := s.exec(cmd, args...); err != nil {
		return nil, s.classify(ctx, err, &stderr)
	}

	var raw []apiRelease
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("decoding gh api output: %w", err)
	}
	return releasesFromAPI(raw), nil
}

// Download execs `gh release download <tag> --repo pgEdge/pgedge-cli
// --pattern <assetName> --dir <dstDir>`; gh itself writes the file
// named assetName into dstDir, so success returns that path without
// this package touching the filesystem.
func (s *GHSource) Download(
	ctx context.Context, tag, assetName, dstDir string,
) (string, error) {
	args := []string{"release", "download", tag,
		"--repo", repoOwner + "/" + repoName,
		"--pattern", assetName,
		"--dir", dstDir}
	cmd := s.run(ctx, "gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := s.exec(cmd, args...); err != nil {
		return "", s.classify(ctx, err, &stderr)
	}
	return filepath.Join(dstDir, assetName), nil
}

// classify turns a failed gh invocation into an actionable error. A
// missing gh binary is diagnosed straight from the exec error — no
// point running gh auth status when gh itself cannot be found — and
// names the install docs. Otherwise it runs `gh auth status` (never
// as a preflight; only after a fetch has already failed) to tell an
// unauthenticated session, wrapped as ErrGHUnauthenticated, from any
// other failure (network, rate limit, ...), which is reported as-is
// with gh's own stderr.
//
// An expired deadline short-circuits that: `gh auth status` would be
// killed by the same expired context and its failure would be read as
// an unauthenticated session, turning our own timeout into exit 5.
func (s *GHSource) classify(
	ctx context.Context, err error, stderr *bytes.Buffer,
) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("gh is not installed; see https://cli.github.com for install docs: %w", err)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("gh: %w", ctxErr)
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return fmt.Errorf("gh: %w", err)
	}

	authCmd := s.run(ctx, "gh", "auth", "status")
	if authErr := s.exec(authCmd, "auth", "status"); authErr != nil {
		return &ghAuthError{stderr: strings.TrimSpace(stderr.String())}
	}

	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("gh: %s", msg)
}
