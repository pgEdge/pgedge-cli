package inspectlive_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestLocks(t *testing.T) {
	requireLive(t)
	ctx := context.Background()
	holder, err := pgx.Connect(ctx, pub.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	waiter, err := pgx.Connect(ctx, pub.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = waiter.Close(ctx) }()
	var holderPID, waiterPID int
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if err := waiter.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&waiterPID); err != nil {
		t.Fatal(err)
	}

	htx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = htx.Rollback(ctx) }()
	if _, err := htx.Exec(ctx, `UPDATE `+schema+`.locked SET v = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	wtx, err := waiter.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wtx.Rollback(ctx) }()
	blocked := make(chan error, 1)
	go func() {
		_, err := wtx.Exec(ctx, `UPDATE `+schema+`.locked SET v = 2 WHERE id = 1 -- inspectlive-blocked`)
		blocked <- err
	}()
	waitFor(t, "waiter blocked", func() (bool, error) {
		return refInt(t, pub, `SELECT cardinality(pg_blocking_pids($1))`, waiterPID) > 0, nil
	})

	got := rows(t, pub, "locks")
	// The waiter is the only blocked session; the holder, the sleeper
	// and the binary's own session are all active and unblocked.
	if len(got) != 1 {
		t.Errorf("want the one blocked session, got %d rows: %v", len(got), got)
	}
	r := find(t, got, "blocked_pid", strconv.Itoa(waiterPID))
	if r["blocked_by"] != strconv.Itoa(holderPID) {
		t.Errorf("blocked_by %q, want the holder %d", r["blocked_by"], holderPID)
	}
	if d := parseInterval(t, r["blocked_duration"]); d < 0 || d > time.Minute {
		t.Errorf("blocked_duration %q", r["blocked_duration"])
	}
	if !strings.Contains(r["blocked_query"], "inspectlive-blocked") {
		t.Errorf("blocked_query %q", r["blocked_query"])
	}

	if err := htx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-blocked; err != nil {
		t.Fatalf("waiter after release: %v", err)
	}
}

// TestLongRunningQueries reads the sleeper TestMain started. The
// analysis needs five minutes of query_start, which nothing can forge,
// so the test waits for whatever of that the other tests have not
// already used; -short skips it.
func TestLongRunningQueries(t *testing.T) {
	requireLive(t)
	if testing.Short() {
		t.Skip("needs a query five minutes old; -short skips the wait")
	}
	if remaining := 5*time.Minute + 2*time.Second - time.Since(sleeper.started); remaining > 0 {
		t.Logf("waiting %s for the sleeper to pass five minutes", remaining.Round(time.Second))
		time.Sleep(remaining)
	}
	r := find(t, rows(t, pub, "long-running-queries"), "pid", strconv.Itoa(sleeper.pid))
	if d := parseInterval(t, r["duration"]); d < 5*time.Minute {
		t.Errorf("duration %q, want at least five minutes", r["duration"])
	}
	if r["user"] != currentUser || r["state"] != "active" {
		t.Errorf("user %q state %q, want %s active", r["user"], r["state"], currentUser)
	}
	if !strings.Contains(r["query"], "inspectlive_long") {
		t.Errorf("query %q", r["query"])
	}
}
