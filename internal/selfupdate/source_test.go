package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/httplog"
)

// --- stub Runner: re-execs this test binary as TestHelperProcess
// instead of the real gh, following os/exec's own test pattern
// (see stdlib os/exec/exec_test.go). No test in this file ever
// shells out to a real gh binary.

type stubResult struct {
	out  string
	code int
}

// stubRunner returns a Runner matching each call's "name arg1 arg2
// ..." against script by longest-prefix, so a key of "gh api" catches
// any query string appended after it. A call matching no key fails
// the test immediately — a test that expects a call must script it.
func stubRunner(t *testing.T, script map[string]stubResult) Runner {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := name + " " + strings.Join(args, " ")
		var (
			best    stubResult
			bestLen = -1
			found   bool
		)
		for k, v := range script {
			if strings.HasPrefix(key, k) && len(k) > bestLen {
				best, bestLen, found = v, len(k), true
			}
		}
		if !found {
			t.Fatalf("stubRunner: no script entry matches %q", key)
		}

		helperArgs := []string{"-test.run=TestHelperProcess", "--",
			best.out, strconv.Itoa(best.code)}
		helperArgs = append(helperArgs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GH_WANT_HELPER_PROCESS=1")
		return cmd
	}
}

// countingRunner wraps inner, counting invocations into n so a test
// can assert gh was never (or was exactly once) invoked.
func countingRunner(inner Runner, n *int32) Runner {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		atomic.AddInt32(n, 1)
		return inner(ctx, name, args...)
	}
}

// failRunner is a Runner that fails any test that calls it — used to
// prove a rung was never reached.
func failRunner(t *testing.T) Runner {
	return func(_ context.Context, name string, args ...string) *exec.Cmd {
		t.Fatalf("gh must not be invoked, got %q %v", name, args)
		return nil
	}
}

// TestHelperProcess is not a real test: it is the subprocess stubRunner
// re-execs in place of gh. Guarded by GH_WANT_HELPER_PROCESS so a
// normal `go test` run treats it as a no-op.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GH_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "stubRunner: too few helper args")
		os.Exit(2)
	}
	out := args[0]
	code, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "stubRunner: bad exit code")
		os.Exit(2)
	}
	ghArgs := args[2:] // the original "gh ..." argv, minus "gh" itself

	// A successful "release download" mimics what the real gh binary
	// would do: write the requested asset into --dir.
	if code == 0 && len(ghArgs) >= 2 && ghArgs[0] == "release" && ghArgs[1] == "download" {
		writeStubAsset(ghArgs)
	}

	if code == 0 {
		fmt.Fprint(os.Stdout, out)
	} else {
		fmt.Fprint(os.Stderr, out)
	}
	os.Exit(code)
}

func writeStubAsset(args []string) {
	var dir, pattern string
	for i, a := range args {
		switch a {
		case "--dir":
			if i+1 < len(args) {
				dir = args[i+1]
			}
		case "--pattern":
			if i+1 < len(args) {
				pattern = args[i+1]
			}
		}
	}
	if dir == "" || pattern == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, pattern), []byte("stub-asset\n"), 0o644)
}

// --- httptest helpers

func releasesListServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
}

const twoReleaseJSON = `[
  {"tag_name":"v1.1.0","assets":[{"name":"pgedge_linux_amd64.tar.gz"}]},
  {"tag_name":"v1.0.0","assets":[{"name":"pgedge_linux_amd64.tar.gz"}]}
]`

// --- Chain: rung 1 (http) wins, gh never invoked

func TestChainHTTPRungWinsGHNeverInvoked(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, twoReleaseJSON)
	defer srv.Close()

	var calls int32
	gh := NewGHSource(countingRunner(failRunner(t), &calls))
	chain := NewChain(NewHTTPSource(srv.URL, "unused"), gh)

	releases, err := chain.Releases(context.Background())
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("got %d releases, want 2", len(releases))
	}
	if releases[0].TagName != "v1.1.0" {
		t.Errorf("got tag %q, want v1.1.0", releases[0].TagName)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("gh invoked %d times, want 0", got)
	}
}

// --- Chain: rung 1 404s, falls through to a succeeding gh stub

func TestChain404FallsThroughToGH(t *testing.T) {
	srv := releasesListServer(t, http.StatusNotFound, "")
	defer srv.Close()

	run := stubRunner(t, map[string]stubResult{
		"gh api": {out: twoReleaseJSON, code: 0},
	})
	chain := NewChain(NewHTTPSource(srv.URL, "unused"), NewGHSource(run))

	releases, err := chain.Releases(context.Background())
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("got %d releases, want 2", len(releases))
	}
	if releases[0].TagName != "v1.1.0" {
		t.Errorf("got tag %q, want v1.1.0", releases[0].TagName)
	}
}

// --- Chain: both rungs fail, single error naming both, errors.Is
// still answers through the fallback's cause

func TestChainBothFailAggregatesErrors(t *testing.T) {
	srv := releasesListServer(t, http.StatusNotFound, "")
	defer srv.Close()

	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "boom", code: 1},
		"gh auth status": {out: "not logged in", code: 1},
	})
	chain := NewChain(NewHTTPSource(srv.URL, "unused"), NewGHSource(run))

	_, err := chain.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "repository not found or private") {
		t.Errorf("error %q missing primary (http) message", msg)
	}
	if !strings.Contains(msg, "gh is not authenticated") {
		t.Errorf("error %q missing fallback (gh) message", msg)
	}
	if !errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("errors.Is(err, ErrGHUnauthenticated) = false, want true")
	}
}

// --- Chain.Download: rung 1 (http) wins, gh never invoked

func TestChainDownloadHTTPRungWins(t *testing.T) {
	const tag = "v1.0.0"
	const asset = "pgedge_linux_amd64.tar.gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("archive"))
	}))
	defer srv.Close()

	var calls int32
	gh := NewGHSource(countingRunner(failRunner(t), &calls))
	chain := NewChain(NewHTTPSource("unused", srv.URL), gh)

	dst := t.TempDir()
	path, err := chain.Download(context.Background(), tag, asset, dst)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if want := filepath.Join(dst, asset); path != want {
		t.Errorf("got path %q, want %q", path, want)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("gh invoked %d times, want 0", got)
	}
}

// --- Chain.Download: both rungs fail, aggregated the same way

func TestChainDownloadBothFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	run := stubRunner(t, map[string]stubResult{
		"gh release download": {out: "boom", code: 1},
		"gh auth status":      {out: "ok", code: 0},
	})
	chain := NewChain(NewHTTPSource("unused", srv.URL), NewGHSource(run))

	dst := t.TempDir()
	_, err := chain.Download(context.Background(), "v1.0.0", "pgedge_linux_amd64.tar.gz", dst)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("gh auth status succeeded; error should not classify as unauthenticated: %v", err)
	}
}

// --- GHSource: auth failure classification

func TestGHSourceAuthFailureClassification(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "boom", code: 1},
		"gh auth status": {out: "not logged in", code: 1},
	})
	src := NewGHSource(run)

	_, err := src.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("errors.Is(err, ErrGHUnauthenticated) = false, want true; err = %v", err)
	}
}

// TestGHSourceGenericFailureIsNotAuth covers the other branch of the
// classifier: gh fails, but gh auth status succeeds, so the failure
// is reported as-is, not misclassified as unauthenticated.
func TestGHSourceGenericFailureIsNotAuth(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "rate limited", code: 1},
		"gh auth status": {out: "logged in", code: 0},
	})
	src := NewGHSource(run)

	_, err := src.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("gh auth status succeeded; error should not be ErrGHUnauthenticated: %v", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error %q should carry gh's own stderr", err.Error())
	}
}

// TestGHSourceMissingBinary covers exec.ErrNotFound: a gh that isn't
// installed at all is diagnosed without ever running gh auth status
// (there'd be nothing to run it with).
func TestGHSourceMissingBinary(t *testing.T) {
	run := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(
			ctx, "pgedge-selfupdate-test-nonexistent-binary")
	}
	src := NewGHSource(run)

	_, err := src.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("errors.Is(err, exec.ErrNotFound) = false, want true; err = %v", err)
	}
	if !strings.Contains(err.Error(), "install") {
		t.Errorf("error %q should name the install docs", err.Error())
	}
}

// TestGHSourceReleasesDecodeError covers malformed gh api output.
func TestGHSourceReleasesDecodeError(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api": {out: "not json", code: 0},
	})
	src := NewGHSource(run)

	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("want decode error, got nil")
	}
}

// --- GHSource.Download: success path returns the path gh would have
// written to, and failure path is classified the same as Releases.

func TestGHSourceDownloadReturnsPath(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh release download": {out: "", code: 0},
	})
	src := NewGHSource(run)

	dst := t.TempDir()
	path, err := src.Download(context.Background(), "v1.0.0", "pgedge_linux_amd64.tar.gz", dst)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	want := filepath.Join(dst, "pgedge_linux_amd64.tar.gz")
	if path != want {
		t.Errorf("got path %q, want %q", path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("asset file not written: %v", err)
	}
}

func TestGHSourceDownloadFailureClassification(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh release download": {out: "boom", code: 1},
		"gh auth status":      {out: "not logged in", code: 1},
	})
	src := NewGHSource(run)

	_, err := src.Download(context.Background(), "v1.0.0", "pgedge_linux_amd64.tar.gz", t.TempDir())
	if !errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("errors.Is(err, ErrGHUnauthenticated) = false, want true; err = %v", err)
	}
}

// --- HTTPSource: 404 message wording

func TestHTTPSource404Message(t *testing.T) {
	srv := releasesListServer(t, http.StatusNotFound, "")
	defer srv.Close()

	src := NewHTTPSource(srv.URL, "unused")
	_, err := src.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "repository not found or private") {
		t.Errorf("error %q should read \"repository not found or private\"", msg)
	}
	if strings.Contains(msg, "no releases") {
		t.Errorf("error %q must never read \"no releases\" for a 404", msg)
	}
}

func TestHTTPSourceOtherStatusIsError(t *testing.T) {
	srv := releasesListServer(t, http.StatusInternalServerError, "")
	defer srv.Close()

	src := NewHTTPSource(srv.URL, "unused")
	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("want error for 500, got nil")
	}
}

func TestHTTPSourceReleasesSuccess(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, twoReleaseJSON)
	defer srv.Close()

	src := NewHTTPSource(srv.URL, "unused")
	releases, err := src.Releases(context.Background())
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("got %d releases, want 2", len(releases))
	}
	if releases[0].Assets[0].Name != "pgedge_linux_amd64.tar.gz" {
		t.Errorf("got asset %q, want pgedge_linux_amd64.tar.gz", releases[0].Assets[0].Name)
	}
}

func TestHTTPSourceReleasesDecodeError(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, "not json")
	defer srv.Close()

	src := NewHTTPSource(srv.URL, "unused")
	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("want decode error, got nil")
	}
}

// --- HTTPSource: download writes the asset file into dstDir

func TestHTTPSourceDownloadWritesFile(t *testing.T) {
	const tag = "v1.0.0"
	const asset = "pgedge_linux_amd64.tar.gz"
	const content = "fake binary archive contents"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := fmt.Sprintf("/%s/%s/releases/download/%s/%s", repoOwner, repoName, tag, asset)
		if r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()

	src := NewHTTPSource("unused", srv.URL)
	dst := t.TempDir()
	path, err := src.Download(context.Background(), tag, asset, dst)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	want := filepath.Join(dst, asset)
	if path != want {
		t.Errorf("got path %q, want %q", path, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != content {
		t.Errorf("got content %q, want %q", got, content)
	}
}

func TestHTTPSourceDownloadNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := NewHTTPSource("unused", srv.URL)
	if _, err := src.Download(context.Background(), "v1.0.0", "asset.tar.gz", t.TempDir()); err == nil {
		t.Fatal("want error for 404 download, got nil")
	}
}

// --- deadlines

// TestGHSleepHelper is not a real test: it is the subprocess the
// deadline test execs in place of gh, and it outlives any budget the
// test gives it. Guarded by GH_WANT_SLEEP_HELPER so a normal `go
// test` run treats it as a no-op.
func TestGHSleepHelper(t *testing.T) {
	if os.Getenv("GH_WANT_SLEEP_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

// sleepingRunner returns a Runner whose every invocation outlives the
// caller's budget, so only the context can end it.
func sleepingRunner() Runner {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestGHSleepHelper$")
		cmd.Env = append(os.Environ(), "GH_WANT_SLEEP_HELPER=1")
		return cmd
	}
}

// TestGHSourceReleasesHonoursTheDeadline pins the reason Runner takes
// a context at all: gh is a child process with no timeout of its own,
// so before this seam existed a gh that never answered hung the whole
// command. The runner here never returns on its own.
func TestGHSourceReleasesHonoursTheDeadline(t *testing.T) {
	src := NewGHSource(sleepingRunner())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := src.Releases(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want a deadline error, got nil")
	}
	if elapsed > 10*time.Second {
		t.Errorf("Releases took %s; the deadline did not reach the exec", elapsed)
	}
}

// TestGHSourceDeadlineIsNotAnAuthFailure guards the exit code: our own
// budget expiring must stay a plain error (exit 1). Classification
// would otherwise run `gh auth status` under the same dead context,
// watch it fail, and report an unauthenticated session — exit 5 for a
// timeout. The scripted gh here WOULD be classified that way: both
// calls fail, which is exactly the auth-failure script.
func TestGHSourceDeadlineIsNotAnAuthFailure(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "boom", code: 1},
		"gh auth status": {out: "not logged in", code: 1},
	})
	src := NewGHSource(run)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Releases(ctx)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("a dead context was classified as unauthenticated: %v", err)
	}
}

// TestHTTPSourceDownloadRemovesAPartialFile pins the M6 fix: the gh
// rung is tried on this same path next, and `gh release download`
// refuses to write over an existing file, so a truncated first rung
// must not leave one.
func TestHTTPSourceDownloadRemovesAPartialFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			// Promise more than is delivered, then hang up: io.Copy
			// fails mid-stream with the file already created.
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("half a bin"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
		}))
	defer srv.Close()

	dst := t.TempDir()
	src := NewHTTPSource(srv.URL, srv.URL)
	if _, err := src.Download(
		context.Background(), "v1.0.0", "asset.tar.gz", dst); err == nil {
		t.Fatal("want a mid-stream error, got nil")
	}

	if _, err := os.Stat(filepath.Join(dst, "asset.tar.gz")); !os.IsNotExist(err) {
		t.Errorf("stat partial file: %v, want IsNotExist", err)
	}
}

// TestChainUnreachablePrimaryDemotesGHAuthClassification covers an outage.
// With no route to GitHub, `gh auth status` fails too — gh 2.95.0
// reports "The token in keyring is invalid" offline — so the gh rung
// reads the outage as an unauthenticated session and the command
// exits 5 for a network failure. The HTTP rung ran first and failed
// at the TRANSPORT, not with a status, and that is the ladder's only
// evidence of reachability: the Chain must not let the fallback's
// auth classification survive it. Both rungs' messages must.
func TestChainUnreachablePrimaryDemotesGHAuthClassification(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // the URL now refuses connections

	run := stubRunner(t, map[string]stubResult{
		"gh api":              {out: "error connecting to api.github.com", code: 1},
		"gh release download": {out: "error connecting to api.github.com", code: 1},
		"gh auth status":      {out: "The token in keyring is invalid.", code: 1},
	})
	chain := NewChain(NewHTTPSource(srv.URL, srv.URL), NewGHSource(run))

	_, err := chain.Releases(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("offline failure classified as unauthenticated: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("error %q missing the primary's transport failure", msg)
	}
	if !strings.Contains(msg, "error connecting to api.github.com") {
		t.Errorf("error %q missing gh's own message", msg)
	}
	if strings.Contains(msg, "not authenticated") {
		t.Errorf("error %q still names authentication as the cause", msg)
	}

	_, err = chain.Download(context.Background(), "v0.6.0", "a.tar.gz", t.TempDir())
	if err == nil {
		t.Fatal("Download: want error, got nil")
	}
	if errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("Download: offline failure classified as unauthenticated: %v", err)
	}
}

// TestChainStatusFailurePreservesGHAuthClassification is the guard on
// the demotion above: a primary that REACHED GitHub and got a status
// back (404 while the repository is private) is not a reachability
// failure, so the fallback's classification stands and exit 5 is
// still reachable at all.
func TestChainStatusFailurePreservesGHAuthClassification(t *testing.T) {
	srv := releasesListServer(t, http.StatusNotFound, "")
	defer srv.Close()

	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "boom", code: 1},
		"gh auth status": {out: "not logged in", code: 1},
	})
	chain := NewChain(NewHTTPSource(srv.URL, "unused"), NewGHSource(run))

	_, err := chain.Releases(context.Background())
	if !errors.Is(err, ErrGHUnauthenticated) {
		t.Errorf("errors.Is(err, ErrGHUnauthenticated) = false, want true: %v", err)
	}
}

// --- diagnostics: --verbose/--debug reach the ladder

func TestHTTPSourceLogsListTrafficAtVerbose(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, `[{"tag_name":"v1.0.0","assets":[]}]`)
	defer srv.Close()

	var diag bytes.Buffer
	src := NewHTTPSource(srv.URL, "unused").Logging(&diag, httplog.Verbose)
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	out := diag.String()
	if !strings.Contains(out, "> GET "+srv.URL+"/repos/pgEdge/pgedge-cli/releases") {
		t.Errorf("diagnostics missing the request line:\n%s", out)
	}
	if !strings.Contains(out, "< 200 OK") {
		t.Errorf("diagnostics missing the response line:\n%s", out)
	}
	if strings.Contains(out, "tag_name") {
		t.Errorf("verbose must not dump bodies:\n%s", out)
	}
}

func TestHTTPSourceLogsListBodyAtDebug(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, `[{"tag_name":"v1.0.0","assets":[]}]`)
	defer srv.Close()

	var diag bytes.Buffer
	src := NewHTTPSource(srv.URL, "unused").Logging(&diag, httplog.Debug)
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if !strings.Contains(diag.String(), "tag_name") {
		t.Errorf("debug should dump the release list body:\n%s", diag.String())
	}
}

// TestHTTPSourceNeverDumpsADownloadBody pins the cap: --debug dumps
// bodies, but a release archive is megabytes of binary, so the
// download client logs at most the request and response lines.
func TestHTTPSourceNeverDumpsADownloadBody(t *testing.T) {
	const content = "ARCHIVE-BYTES-THAT-MUST-NOT-BE-ECHOED"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()

	var diag bytes.Buffer
	src := NewHTTPSource("unused", srv.URL).Logging(&diag, httplog.Debug)
	path, err := src.Download(context.Background(), "v1.0.0", "a.tar.gz", t.TempDir())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Fatalf("downloaded content = %q, %v; want %q", got, err, content)
	}
	out := diag.String()
	if !strings.Contains(out, "> GET ") || !strings.Contains(out, "< 200 OK") {
		t.Errorf("download diagnostics missing request/response lines:\n%s", out)
	}
	if strings.Contains(out, content) {
		t.Errorf("download body was dumped:\n%s", out)
	}
}

func TestHTTPSourceLoggingOffIsSilent(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, `[]`)
	defer srv.Close()

	var diag bytes.Buffer
	src := NewHTTPSource(srv.URL, "unused").Logging(&diag, httplog.Off)
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if diag.Len() != 0 {
		t.Errorf("Off level wrote diagnostics:\n%s", diag.String())
	}
}

func TestGHSourceLogsEachExecAtVerbose(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api":         {out: "boom", code: 1},
		"gh auth status": {out: "logged in", code: 0},
	})
	var diag bytes.Buffer
	src := NewGHSource(run).Logging(&diag, httplog.Verbose)
	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("want error, got nil")
	}
	out := diag.String()
	for _, want := range []string{
		"> gh api repos/pgEdge/pgedge-cli/releases?per_page=30\n",
		"< exit status 1 (",
		"> gh auth status\n",
		"< exit 0 (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnostics missing %q:\n%s", want, out)
		}
	}
}

func TestGHSourceLoggingOffIsSilent(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh api": {out: `[]`, code: 0},
	})
	var diag bytes.Buffer
	src := NewGHSource(run).Logging(&diag, httplog.Off)
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if diag.Len() != 0 {
		t.Errorf("Off level wrote diagnostics:\n%s", diag.String())
	}
}

// TestHTTPSourceLoggingTwiceDoesNotStack pins that Logging wraps the
// pristine base transport, not whatever is installed: a second call
// must not double every diagnostic line.
func TestHTTPSourceLoggingTwiceDoesNotStack(t *testing.T) {
	srv := releasesListServer(t, http.StatusOK, `[]`)
	defer srv.Close()

	var diag bytes.Buffer
	src := NewHTTPSource(srv.URL, "unused").
		Logging(&diag, httplog.Verbose).
		Logging(&diag, httplog.Verbose)
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("Releases: %v", err)
	}
	if n := strings.Count(diag.String(), "> GET "); n != 1 {
		t.Errorf("request line logged %d times after two Logging calls, want 1:\n%s", n, diag.String())
	}
}

// TestGHSourceSanitizesTheLoggedTag guards the diag line against a
// release feed that carries control characters in a tag.
func TestGHSourceSanitizesTheLoggedTag(t *testing.T) {
	run := stubRunner(t, map[string]stubResult{
		"gh release download": {out: "", code: 0},
	})
	var diag bytes.Buffer
	src := NewGHSource(run).Logging(&diag, httplog.Verbose)
	_, _ = src.Download(context.Background(), "v9.9.9\nforged line\x1b[2J", "a.tar.gz", t.TempDir())
	out := diag.String()
	if strings.Contains(out, "\nforged line") || strings.Contains(out, "\x1b") {
		t.Errorf("control characters reached the diagnostic stream: %q", out)
	}
}

// TestDownloadTransportKeepsTheDefaultsItNeeds pins the transport's
// shape against the bare http.Transport it replaced.
//
// The download client used to be built as
// &http.Transport{ResponseHeaderTimeout: ...}, which sets that one
// field and zeroes every other default: no Proxy, so an operator
// behind HTTPS_PROXY reached the API through DefaultTransport and
// then could not download an update, and no dial or TLS-handshake
// bound, so the ResponseHeaderTimeout it did set could be reached
// only after an unbounded connect.
//
// It asserts on the transport the request actually TRAVELS THROUGH,
// reached the way production reaches it, rather than on the field the
// constructor happened to set. Two mutations walk through the weaker
// version: one that installs a Proxy function returning nil (present
// but dead), and one where Logging reinstalls a bare transport on
// s.dl, leaving the constructor's clone pristine and unused. Logging
// is not optional in production -- internal/cli/self.go calls it
// unconditionally -- so s.dl.Transport is the honest subject.
//
// Comparing the Proxy func by pointer rather than driving a live
// proxy is deliberate: http.ProxyFromEnvironment caches the
// environment on first use, so a t.Setenv here would be read or
// ignored depending on what ran before it in the package, which is a
// test that passes for the wrong reason.
func TestDownloadTransportKeepsTheDefaultsItNeeds(t *testing.T) {
	s := NewHTTPSource("https://api.example.com", "https://dl.example.com")
	// Production always logs; Off returns the base unwrapped, which is
	// the same object either way.
	s.Logging(io.Discard, httplog.Verbose)

	rt := s.dl.Transport
	if lt, ok := rt.(*httplog.Transport); ok {
		rt = lt.Base
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("the download transport is %T, want *http.Transport", rt)
	}

	if tr.ResponseHeaderTimeout != 10*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 10s",
			tr.ResponseHeaderTimeout)
	}

	// Identity, not merely non-nil: a Proxy that returns nil for every
	// request is as broken as no Proxy at all, and reads the same to a
	// nil check.
	want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
	if tr.Proxy == nil {
		t.Error("Proxy is nil: a download will ignore HTTPS_PROXY")
	} else if got := reflect.ValueOf(tr.Proxy).Pointer(); got != want {
		t.Error("Proxy is not ProxyFromEnvironment: a download will " +
			"not follow HTTPS_PROXY")
	}

	def := http.DefaultTransport.(*http.Transport)
	if tr.TLSHandshakeTimeout != def.TLSHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want DefaultTransport's %v",
			tr.TLSHandshakeTimeout, def.TLSHandshakeTimeout)
	}
	if tr.DialContext == nil {
		t.Error("DialContext is nil: the dial is unbounded")
	}
}
