package cmd

import (
	"net/http"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestRequestTimeoutFlagReachesTheClient proves the starfleet root's
// --timeout VALUE is what bounds the wire, not the compiled-in
// default. Nothing else can: TestStarfleetClientsAreBounded hands
// conn.HTTPClientFor an explicit duration, so rewiring newAPIClient
// back to conn.RequestTimeout would leave every test green while the
// flag silently did nothing. Here a hung resource endpoint under
// --timeout 100ms must fail at the timeout class in far less than
// the 30-second default — that regression turns this test into a
// 30-second hang, which the elapsed assertion refuses.
//
// managed has its own client.go and its own copy of this test;
// mutating one leaves the other green, so neither stands in for the
// other.
func TestRequestTimeoutFlagReachesTheClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})

	start := time.Now()
	err := runAuthed(t, rt, out, url,
		"cluster", "list", "--timeout", "100ms")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hung server produced a successful list")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %v: the --timeout flag did not bound the "+
			"request", elapsed)
	}
	if got := cli.ExitCode(err); got != cli.ExitTimeout {
		t.Errorf("exit = %d, want %d (timeout class)", got,
			cli.ExitTimeout)
	}
}

// TestTaskWaitPollFloorBoundsAnUnboundedClient kills the mutation
// review ran that nothing caught: deleting the pollDeadline floor
// from task wait's loop left every test green, because only
// --timeout 0 (an unbounded client) exposes the floor and no test
// ran that shape. Here the poll must end at the floor (one
// requestBound(), 30s when --timeout is 0) rather than never; the
// select guard turns a removed floor into a failure instead of a
// hung test run. Slow by construction (~30s): the floor under an
// unbounded client IS the 30-second default, and shrinking it would
// test a different rule.
func TestTaskWaitPollFloorBoundsAnUnboundedClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	// The handler frees itself at 90s so a REGRESSION fails at this
	// test's own guard instead of deadlocking srv.Close in cleanup
	// until the whole suite's timeout.
	url := testsupport.NewAuthedServer(t,
		func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(90 * time.Second):
			}
		})

	done := make(chan error, 1)
	go func() {
		done <- runAuthed(t, rt, out, url, "task", "wait",
			"e5f6a7b8-c9d0-1234-efab-567890123456",
			"--timeout", "0", "--wait-timeout", "1",
			"--wait-interval", "1")
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hung poll reported success")
		}
		if got := cli.ExitCode(err); got != cli.ExitTimeout {
			t.Errorf("exit = %d, want %d (timeout class)", got,
				cli.ExitTimeout)
		}
	case <-time.After(50 * time.Second):
		t.Fatal("the poll is unbounded: --timeout 0 left task wait " +
			"with no bound of its own, defeating --wait-timeout")
	}
}
