package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

// errTimeout is a net.Error that reports a timeout, standing in for the
// shapes Go produces for a deadline. The end-to-end test below pins the
// real one; this is for the table.
type errTimeout struct{}

func (errTimeout) Error() string   { return "deadline" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return false }

// TestNetworkErrorDiagnosesATimeoutSeparately: a timeout must not
// arrive with the reachability/mTLS hint, sending the reader to
// --ca-cert while the server is answering fine.
func TestNetworkErrorDiagnosesATimeoutSeparately(t *testing.T) {
	// The CODE varies with the row, and that is the point: a timeout
	// is 3 CLI-wide, while a server that cannot be reached
	// at all is an ordinary failure. Asserting one code for every row
	// would hide either half.
	tests := []struct {
		name     string
		err      error
		wantCode int
		want     []string
		notWant  []string
	}{
		{
			name:     "unreachable keeps the reachability hint",
			err:      errNet{},
			wantCode: ExitGeneral,
			want:     []string{"control-plane reachable", "--ca-cert"},
			notWant:  []string{"--timeout"},
		},
		{
			name:     "timeout names --timeout",
			err:      errTimeout{},
			wantCode: ExitTimeout,
			want: []string{
				"timed out", "--timeout", "--timeout 0"},
			notWant: []string{"control-plane reachable", "--ca-cert"},
		},
		{
			name:     "a wrapped timeout is still a timeout",
			err:      fmtWrap(errTimeout{}),
			wantCode: ExitTimeout,
			want:     []string{"--timeout"},
			notWant:  []string{"--ca-cert"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := networkError("list databases", tc.err)
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want *ExitError, got %T", err)
			}
			if ee.Code() != tc.wantCode {
				t.Errorf("code = %d, want %d",
					ee.Code(), tc.wantCode)
			}
			msg := ee.Error()
			if !strings.HasPrefix(msg, "list databases: ") {
				t.Errorf("want the action first: %q", msg)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("missing %q: %q", w, msg)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(msg, w) {
					t.Errorf("must not mention %q: %q", w, msg)
				}
			}
		})
	}
}

// fmtWrap hides an error one level down, the way a caller that adds
// context would.
func fmtWrap(err error) error {
	return &wrapped{err}
}

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "get: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }

// TestClientTimeoutIsDiagnosedEndToEnd runs the reproduction:
// a healthy server that is merely slow, and a --timeout that cannot
// wait for it. The classification must hold for the error Go actually
// produces, not only for the stub above — the filed report and the
// re-verification saw two different wordings of that error, which is
// why the code reads net.Error rather than matching its text.
func TestClientTimeoutIsDiagnosedEndToEnd(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	err := runControlplane(t, rt, out, srv, "database", "list", "--timeout", "20ms")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--timeout") {
		t.Errorf("a client timeout must name --timeout: %q", msg)
	}
	if strings.Contains(msg, "control-plane reachable") ||
		strings.Contains(msg, "--ca-cert") {
		t.Errorf("a timeout must not be reported as a reachability "+
			"or mTLS problem: %q", msg)
	}
}

// TestWaitDeadlineDuringAPollReportsTheWait covers the same
// misdiagnosis wearing another flag: the deadline that expired is
// --wait-timeout's, so the message must name the wait rather than
// --timeout, and the code must be the ExitTimeout the skill documents
// for a timed-out --wait.
func TestWaitDeadlineDuringAPollReportsTheWait(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	tid := uuid.MustParse(taskUUID)
	err := waitForTask(rt, databaseTaskSource(client, "db1", tid), 1, 1)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitTimeout {
		t.Fatalf("want ExitTimeout(%d), got %v", ExitTimeout, err)
	}
	if !strings.Contains(ee.Error(), "timed out after 1s waiting for task") {
		t.Errorf("want the wait named: %q", ee.Error())
	}
	if strings.Contains(ee.Error(), "--timeout") ||
		strings.Contains(ee.Error(), "control-plane reachable") {
		t.Errorf("an expired --wait-timeout is neither a per-request "+
			"timeout nor a reachability problem: %q", ee.Error())
	}
}

// TestFollowPollDeadlineNamesItsOwnBound pins the third site. --follow
// bounds each log poll itself, so --timeout is not the knob there —
// --timeout 0 still stops at followPollTimeout.
func TestFollowPollDeadlineNamesItsOwnBound(t *testing.T) {
	old := followPollTimeout
	followPollTimeout = 50 * time.Millisecond
	t.Cleanup(func() { followPollTimeout = old })

	rt, _, _ := newTestRuntime(t, "", "text")
	client := newRawClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	tid := uuid.MustParse(taskUUID)
	err := followTask(rt, databaseTaskSource(client, "db1", tid))
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitTimeout {
		t.Fatalf("want ExitTimeout(%d), got %v", ExitTimeout, err)
	}
	if !strings.Contains(ee.Error(), "--follow bounds each log poll") {
		t.Errorf("want the poll's own bound named: %q", ee.Error())
	}
	if strings.Contains(ee.Error(), "control-plane reachable") {
		t.Errorf("not a reachability problem: %q", ee.Error())
	}
}

// TestIsTimeout guards the classifier itself, including the direction
// that matters: a connection refused is NOT a timeout, or every
// unreachable server would be told to raise --timeout.
func TestIsTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errNet{}, false},
		{"timeout", errTimeout{}, true},
		{"wrapped timeout", fmtWrap(errTimeout{}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTimeout(tc.err); got != tc.want {
				t.Errorf("isTimeout(%v) = %v, want %v",
					tc.err, got, tc.want)
			}
		})
	}
}

// TestPerRequestTimeoutUnderWaitStillNamesTimeout is the direction the
// reference has to state: with the shipped
// defaults (--timeout 30s, --wait-timeout 600) the bound that expires
// first is the PER-REQUEST one, and then the wait does not report
// itself — the error names --timeout and the wait aborts at exit 1
// rather than retrying on the next interval. The wait's own bound
// reports itself only when the wait's own bound is what ran out.
func TestPerRequestTimeoutUnderWaitStillNamesTimeout(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	slow := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	client, err := api.NewClientWithResponses(slow,
		api.WithHTTPClient(&http.Client{Timeout: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	tid := uuid.MustParse(taskUUID)
	// A wait window nowhere near expiring: 600s is the shipped default.
	werr := waitForTask(rt, databaseTaskSource(client, "db1", tid), 600, 1)
	if werr == nil {
		t.Fatal("expected an error, got nil")
	}
	var ee *ExitError
	// ExitTimeout: a timeout is 3 whichever bound produced
	// it. So the CODE does not separate a per-request timeout from
	// the wait window expiring — the MESSAGE does, which is what the
	// two assertions below pin, and they are the whole guarantee.
	if !errors.As(werr, &ee) || ee.Code() != ExitTimeout {
		t.Fatalf("want ExitTimeout(%d): %v", ExitTimeout, werr)
	}
	if !strings.Contains(ee.Error(), "--timeout") {
		t.Errorf("want --timeout named: %q", ee.Error())
	}
	if strings.Contains(ee.Error(), "waiting for task") {
		t.Errorf("must not be reported as the wait timing out: %q",
			ee.Error())
	}
}

// TestFollowPollThatCannotConnectKeepsTheReachabilityHint is the
// missing direction on followTask's own branch. pollExpired is read
// before cancel() on purpose; read it after and ctx.Err() is Canceled
// for every outcome, so an unreachable server would be reported as a
// fabricated 30s timeout at exit 3 — the misdiagnosis in reverse, and every
// mutation of that ordering used to stay green.
func TestFollowPollThatCannotConnectKeepsTheReachabilityHint(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "", "text")
	client, err := api.NewClientWithResponses("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	tid := uuid.MustParse(taskUUID)
	ferr := followTask(rt, databaseTaskSource(client, "db1", tid))
	if ferr == nil {
		t.Fatal("expected an error, got nil")
	}
	var ee *ExitError
	if !errors.As(ferr, &ee) || ee.Code() != ExitGeneral {
		t.Fatalf("want ExitGeneral(%d), got %v", ExitGeneral, ferr)
	}
	if !strings.Contains(ee.Error(), "control-plane reachable") {
		t.Errorf("a refused connection keeps the reachability hint: %q",
			ee.Error())
	}
	if strings.Contains(ee.Error(), "timed out") {
		t.Errorf("a refused connection is not a timeout: %q", ee.Error())
	}
}

// TestFollowPollTimeoutIsTheDocumentedThirtySeconds binds the "30s"
// both the controlplane reference and the changelog print to the value the code
// uses. followPollTimeout is a var so a test can shorten it, which
// left the documented number pinned to nothing.
func TestFollowPollTimeoutIsTheDocumentedThirtySeconds(t *testing.T) {
	if followPollTimeout != 30*time.Second {
		t.Errorf("followPollTimeout = %v, but internal/controlplane/llms.txt "+
			"and docs/changelog.md both say 30s; change the documents "+
			"in the same commit or restore the value",
			followPollTimeout)
	}
}
