package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
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

// TestSlowPollDoesNotForgeAWaitExpiry is byoc's copy of the
// timeout discrimination. Bounding the client (conn.RequestTimeout)
// makes a single slow poll fail with the same error a whole wait
// expiring raises, so `errors.Is(err, context.DeadlineExceeded)` on its
// own would report a 300-second wait as timed out 200ms in, with
// essentially all of --wait-timeout unspent.
//
// managed has its own copy of this loop and its own copy of this test.
// Mutating one leaves the other green, so neither stands in for the
// other.
func TestSlowPollDoesNotForgeAWaitExpiry(t *testing.T) {
	t.Run("a bounded request is not the wait expiring", func(t *testing.T) {
		rt := &module.Runtime{Stderr: io.Discard}
		start := time.Now()
		err := waitForSubjectTask(rt,
			hungTaskClient(t, 200*time.Millisecond),
			testDatabaseID, "", 300, 1, false)
		if err == nil {
			t.Fatal("a hung server produced a successful wait")
		}
		if elapsed := time.Since(start); elapsed > 30*time.Second {
			t.Fatalf("took %v: the request was not bounded", elapsed)
		}
		if ee, ok := err.(*ExitError); ok && ee.Code() == ExitTimeout {
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
			testDatabaseID, "", 1, 1, false)
		if err == nil {
			t.Fatal("a hung server produced a successful wait")
		}
		ee, ok := err.(*ExitError)
		if !ok || ee.Code() != ExitTimeout {
			t.Fatalf("want ExitTimeout, got %v (%T)", err, err)
		}
	})
}

// TestByocPriorTaskReadIsBounded pins the prior-task read. byoc captures
// the prior task id with a bare context.Background() at thirteen call
// sites, so a hung read blocked the mutation before it was submitted.
// It inherits the client's bound now.
//
// byoc needs no age floor for the timed-out case: unlike managed it
// propagates a failed pre-mutation read and refuses the write, and a
// bounded read that expires is a failed read, so it refuses on exactly
// the path it already had.
func TestByocPriorTaskReadIsBounded(t *testing.T) {
	// The client is deliberately UNBOUNDED (--timeout 0's shape), so
	// only newestSubjectTaskID's own requestBound() floor can end the
	// read — an earlier version bounded the client at 200ms, which
	// dominated, and review deleted the floor with every test green.
	// requestTimeoutFlag stands in for a --timeout 200ms so the floor
	// is fast to observe; the select guard turns a removed floor into
	// a failure instead of a hung test run.
	old := requestTimeoutFlag
	requestTimeoutFlag = 200 * time.Millisecond
	t.Cleanup(func() { requestTimeoutFlag = old })

	client := hungTaskClient(t, 0)
	done := make(chan error, 1)
	go func() {
		_, err := newestSubjectTaskID(
			context.Background(), client, testDatabaseID)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hung read reported success, so the mutation " +
				"would proceed with no baseline")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the capture is unbounded: an unbounded client and a " +
			"bare context left the read with no deadline at all")
	}
}
