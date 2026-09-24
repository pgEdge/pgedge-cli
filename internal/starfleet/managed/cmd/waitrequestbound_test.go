package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// hungTaskServer accepts and then never answers, so every poll the
// wait loop makes runs into whichever bound is shorter.
func hungTaskServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func hungTaskClient(t *testing.T, timeout time.Duration) *api.ClientWithResponses {
	t.Helper()
	client, err := api.NewClientWithResponses(hungTaskServer(t),
		api.WithHTTPClient(&http.Client{Timeout: timeout}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// TestSlowPollDoesNotForgeAWaitExpiry is the managed copy of the
// discrimination #348 forced. Bounding the client (conn.RequestTimeout)
// makes a single slow poll fail with the same error a whole wait
// expiring raises, so `errors.Is(err, context.DeadlineExceeded)` on its
// own would report a 300-second wait as timed out 200ms in, with
// essentially all of --wait-timeout unspent.
//
// byoc has its own copy of this loop and its own copy of this test.
// Mutating one leaves the other green, so neither stands in for the
// other.
func TestSlowPollDoesNotForgeAWaitExpiry(t *testing.T) {
	t.Run("a bounded request is not the wait expiring", func(t *testing.T) {
		rt := &module.Runtime{Stderr: io.Discard}
		start := time.Now()
		err := waitForSubjectTask(rt,
			hungTaskClient(t, 200*time.Millisecond),
			testDatabaseID, taskBaseline{}, 300, 1, false)
		if err == nil {
			t.Fatal("a hung server produced a successful wait")
		}
		if elapsed := time.Since(start); elapsed > 30*time.Second {
			t.Fatalf("took %v: the request was not bounded", elapsed)
		}
		var ee *ExitError
		if asExitError(err, &ee) && ee.Code() == ExitTimeout {
			t.Fatalf("one slow poll reported the whole --wait-timeout as "+
				"expired: %v", err)
		}
	})

	// The converse: when the wait really does run out mid-poll, it
	// must still report ExitTimeout. An unbounded client leaves the
	// loop's own deadline as the only bound, which is what expires.
	t.Run("the wait expiring is still ExitTimeout", func(t *testing.T) {
		rt := &module.Runtime{Stderr: io.Discard}
		err := waitForSubjectTask(rt, hungTaskClient(t, 0),
			testDatabaseID, taskBaseline{}, 1, 1, false)
		if err == nil {
			t.Fatal("a hung server produced a successful wait")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitTimeout {
			t.Fatalf("want ExitTimeout, got %v (%T)", err, err)
		}
	})
}

// TestCaptureTaskBaselineIsBounded is #340: the pre-mutation read used
// context.Background() with no deadline, so a hung read blocked the
// command before the write was even submitted. It inherits the
// client's bound now rather than getting a flag of its own, which
// leaves --wait-timeout meaning only what it is documented to mean.
//
// What a timed-out capture leaves behind is #340's second question,
// and bounding the client answers it without a new rule: a bounded
// read that expires IS a failed read, so it takes the age floor #335
// already built for one.
func TestCaptureTaskBaselineIsBounded(t *testing.T) {
	prevWait := waitFlag
	waitFlag = true
	t.Cleanup(func() { waitFlag = prevWait })

	client := hungTaskClient(t, 200*time.Millisecond)
	start := time.Now()
	base := captureTaskBaseline(client, testDatabaseID)
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("took %v: the capture is still unbounded", elapsed)
	}
	if base.notBefore.IsZero() {
		t.Error("a timed-out capture set no age floor, so the next " +
			"task of any age would be accepted as this mutation's")
	}
	if base.priorID != "" {
		t.Errorf("a timed-out capture recorded a prior id: %q",
			base.priorID)
	}
}

// TestManagedBaselineReadIsBounded is byoc's
// TestByocPriorTaskReadIsBounded for managed's own copy of the
// helper: the client is deliberately UNBOUNDED (--timeout 0's shape),
// so only newestSubjectTask's requestBound() floor can end the read.
// Review deleted byoc's floor with every test green, which is why
// both copies carry their own guard — mutating one must leave the
// other's test red on its own.
func TestManagedBaselineReadIsBounded(t *testing.T) {
	old := requestTimeoutFlag
	requestTimeoutFlag = 200 * time.Millisecond
	t.Cleanup(func() { requestTimeoutFlag = old })

	client := hungTaskClient(t, 0)
	done := make(chan error, 1)
	go func() {
		_, err := newestSubjectTask(
			context.Background(), client, testDatabaseID)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hung read reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the baseline read is unbounded: an unbounded client " +
			"and a bare context left it with no deadline at all")
	}
}
