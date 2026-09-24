package inspectlive_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const lagRef = `SELECT COALESCE(host(client_addr), ''), application_name, state,
  sent_lsn::text, replay_lsn::text, sync_state
  FROM pg_stat_replication WHERE application_name = '` + subscription + `'`

func TestReplicationLagWhileStreaming(t *testing.T) {
	requireLive(t)
	got, want := stable(t, pub, "replication-lag", lagRef)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("want one subscriber row, got %v against reference %v", got, want)
	}
	r, w := got[0], want[0]
	if r["client"] != w[0] || r["application"] != w[1] || r["state"] != w[2] ||
		r["sent_lsn"] != w[3] || r["replay_lsn"] != w[4] || r["sync_state"] != w[5] {
		t.Errorf("row %v, reference %v", r, w)
	}
	if net.ParseIP(r["client"]) == nil {
		t.Errorf("client %q is not an address", r["client"])
	}
	if r["application"] != subscription || r["state"] != "streaming" || r["sync_state"] != "async" {
		t.Errorf("application %q state %q sync_state %q", r["application"], r["state"], r["sync_state"])
	}
	if !isLSN(r["sent_lsn"]) || !isLSN(r["replay_lsn"]) {
		t.Errorf("sent_lsn %q replay_lsn %q", r["sent_lsn"], r["replay_lsn"])
	}
	// The lag columns are intervals while WAL flows and empty once the
	// subscriber is level; either is a value, anything else a wrong
	// binding.
	for _, col := range []string{"write_lag", "flush_lag", "replay_lag"} {
		if r[col] != "" {
			parseInterval(t, r[col])
		}
	}
}

func TestSubscriptionsOnTheSubscriber(t *testing.T) {
	requireLive(t)
	got, want := stable(t, sub, "subscriptions", `
		SELECT subname, pid::text, received_lsn::text, latest_end_lsn::text
		FROM pg_stat_subscription WHERE relid IS NULL`)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("want one apply worker row, got %v against reference %v", got, want)
	}
	r, w := got[0], want[0]
	if r["subscription"] != w[0] || r["pid"] != w[1] || r["received_lsn"] != w[2] || r["latest_end_lsn"] != w[3] {
		t.Errorf("row %v, reference %v", r, w)
	}
	if r["subscription"] != subscription {
		t.Errorf("subscription %q", r["subscription"])
	}
	if _, err := strconv.Atoi(r["pid"]); err != nil {
		t.Errorf("pid %q", r["pid"])
	}
	if !isLSN(r["received_lsn"]) || !isLSN(r["latest_end_lsn"]) {
		t.Errorf("received_lsn %q latest_end_lsn %q", r["received_lsn"], r["latest_end_lsn"])
	}
	sent := parseTimestamp(t, r["last_msg_sent"])
	received := parseTimestamp(t, r["last_msg_received"])
	if received.Before(sent.Add(-time.Second)) {
		t.Errorf("last_msg_received %s precedes last_msg_sent %s", received, sent)
	}
}

// TestReplicationEmptyOnTheOtherSide checks the empty-result contract
// on the side of the pair each analysis has nothing to say about: no
// rows is exit 0, one sentence on stderr, [] under -o json and nothing
// on stdout in text.
func TestReplicationEmptyOnTheOtherSide(t *testing.T) {
	requireLive(t)
	cases := []struct {
		s        *server
		analysis string
	}{
		{pub, "subscriptions"},
		{sub, "replication-slots"},
		{sub, "replication-lag"},
	}
	for _, tc := range cases {
		j := run(t, tc.s, tc.analysis, "-o", "json")
		if j.code != 0 || j.stdout != "[]\n" || j.stderr != tc.analysis+": no rows.\n" {
			t.Errorf("%s %s -o json: exit %d stdout %q stderr %q", tc.s.name, tc.analysis, j.code, j.stdout, j.stderr)
		}
		x := run(t, tc.s, tc.analysis)
		if x.code != 0 || x.stdout != "" || x.stderr != tc.analysis+": no rows.\n" {
			t.Errorf("%s %s: exit %d stdout %q stderr %q", tc.s.name, tc.analysis, x.code, x.stdout, x.stderr)
		}
	}
}

// TestReplicationSlotsThroughAnOutage disables the subscription, writes
// past it, and reads what a stopped consumer looks like from the
// publisher: no replication-lag row at all, and a slot whose retained
// WAL grows. A physical slot created after the writes gives the same
// analysis a second row with a different value in every column.
func TestReplicationSlotsThroughAnOutage(t *testing.T) {
	requireLive(t)
	exec1(t, sub, `ALTER SUBSCRIPTION `+subscription+` DISABLE`)
	t.Cleanup(func() {
		exec1(t, sub, `ALTER SUBSCRIPTION `+subscription+` ENABLE`)
		exec1(t, pub, `SELECT pg_drop_replication_slot('`+physicalSlot+`')
		  WHERE EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = '`+physicalSlot+`')`)
	})
	waitFor(t, "walsender gone", func() (bool, error) {
		return refInt(t, pub, `SELECT count(*) FROM pg_stat_replication`) == 0, nil
	})
	exec1(t, pub, `INSERT INTO `+schema+`.replicated
	  SELECT g, repeat('w', 40) FROM generate_series(11, 400010) g`)
	// An immediately reserved physical slot starts at the last
	// checkpoint's redo pointer, not at the write position (measured:
	// 93 MB retained on creation, more than the stopped logical slot).
	// A checkpoint first puts that pointer here.
	exec1(t, pub, `CHECKPOINT`)
	exec1(t, pub, `SELECT pg_create_physical_replication_slot('`+physicalSlot+`', true)`)

	if lag := rows(t, pub, "replication-lag"); len(lag) != 0 {
		t.Errorf("a disabled subscriber must leave no replication-lag row, got %v", lag)
	}

	got, want := stable(t, pub, "replication-slots", `
		SELECT slot_name, slot_type, COALESCE(plugin, ''), COALESCE(database, ''),
		  active::text, COALESCE(wal_status, '')
		FROM pg_replication_slots ORDER BY slot_name`)
	if len(got) != 2 {
		t.Fatalf("want the logical and the physical slot, got %v", got)
	}
	for _, w := range want {
		r := find(t, got, "slot", w[0])
		if r["type"] != w[1] || r["plugin"] != w[2] || r["database"] != w[3] ||
			r["active"] != w[4] || r["wal_status"] != w[5] {
			t.Errorf("%s: row %v, reference %v", w[0], r, w)
		}
	}
	logical := find(t, got, "slot", subscription)
	physical := find(t, got, "slot", physicalSlot)
	if logical["type"] != "logical" || logical["plugin"] != "pgoutput" ||
		logical["database"] != currentDB || logical["active"] != "false" {
		t.Errorf("logical slot %v", logical)
	}
	if physical["type"] != "physical" || physical["plugin"] != "" ||
		physical["database"] != "" || physical["active"] != "false" {
		t.Errorf("physical slot %v", physical)
	}
	// 400000 rows of WAL (measured: 65 MB) went past the stopped
	// logical slot; the physical slot was reserved after them.
	if parseSize(t, logical["retained_wal"]) <= 16<<20 {
		t.Errorf("logical slot retains %q, want more than one 16 MB segment", logical["retained_wal"])
	}
	if parseSize(t, physical["retained_wal"]) >= 1<<20 {
		t.Errorf("physical slot retains %q, want under 1 MB", physical["retained_wal"])
	}
	if got[0]["slot"] != physicalSlot {
		t.Errorf("first row %q, want %s (slot-name order)", got[0]["slot"], physicalSlot)
	}

	// Re-enable with the subscriber's table locked, so the apply worker
	// stalls on the first change while the publisher sends. Level,
	// sent_lsn and replay_lsn are equal and a swap between them is
	// invisible; stalled, they must differ. The stalled worker reports
	// nothing back, so the sender sits in catchup and the three lag
	// columns are empty (measured on 18: a 63 MB gap, every lag "").
	ctx := context.Background()
	blocker, err := pgx.Connect(ctx, sub.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close(ctx) }()
	btx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = btx.Rollback(ctx) }()
	if _, err := btx.Exec(ctx, `LOCK TABLE `+schema+`.replicated IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	exec1(t, sub, `ALTER SUBSCRIPTION `+subscription+` ENABLE`)
	waitFor(t, "publisher sent past the stalled replay", func() (bool, error) {
		rs := ref(t, pub, `SELECT sent_lsn::text, replay_lsn::text
		  FROM pg_stat_replication WHERE application_name = $1`, subscription)
		return len(rs) == 1 && rs[0][0] != "" && rs[0][1] != "" &&
			lsnValue(t, rs[0][0]) > lsnValue(t, rs[0][1]), nil
	})
	// The stall pins replay_lsn; sent_lsn keeps creeping as the kernel
	// accepts more of the stream (on a CI runner it moved through five
	// bracketed reads), so it is checked as ahead and monotonic, not
	// equal to a reference.
	before := ref(t, pub, `SELECT sent_lsn::text, replay_lsn::text
	  FROM pg_stat_replication WHERE application_name = $1`, subscription)
	stalled := rows(t, pub, "replication-lag")
	if len(stalled) != 1 || len(before) != 1 {
		t.Fatalf("stalled: want one row, got %v against reference %v", stalled, before)
	}
	t.Logf("stalled row: %v", stalled[0])
	if stalled[0]["replay_lsn"] != before[0][1] {
		t.Errorf("stalled: replay_lsn %q, reference %q", stalled[0]["replay_lsn"], before[0][1])
	}
	if sent := lsnValue(t, stalled[0]["sent_lsn"]); sent <= lsnValue(t, stalled[0]["replay_lsn"]) ||
		sent < lsnValue(t, before[0][0]) {
		t.Errorf("stalled: sent_lsn %q against replay_lsn %q and the earlier %q",
			stalled[0]["sent_lsn"], stalled[0]["replay_lsn"], before[0][0])
	}
	if stalled[0]["application"] != subscription || stalled[0]["sync_state"] != "async" {
		t.Errorf("stalled: application %q sync_state %q", stalled[0]["application"], stalled[0]["sync_state"])
	}
	switch stalled[0]["state"] {
	case "catchup":
		for _, col := range []string{"write_lag", "flush_lag", "replay_lag"} {
			if stalled[0][col] != "" {
				t.Errorf("stalled in catchup: %s %q, want empty", col, stalled[0][col])
			}
		}
	case "streaming":
		for _, col := range []string{"write_lag", "flush_lag", "replay_lag"} {
			if stalled[0][col] != "" {
				parseInterval(t, stalled[0][col])
			}
		}
	default:
		t.Errorf("stalled: state %q", stalled[0]["state"])
	}
	if err := btx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	// The subscriber catches up, the slot is active again and its
	// retained WAL falls back.
	waitFor(t, "subscriber caught up", func() (bool, error) {
		return refInt(t, sub, `SELECT count(*) FROM `+schema+`.replicated`) == 400010, nil
	})
	waitFor(t, "logical slot released its WAL", func() (bool, error) {
		n := refInt(t, pub, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)
		  FROM pg_replication_slots WHERE slot_name = $1`, subscription)
		return n < 1<<20, nil
	})
	after := find(t, rows(t, pub, "replication-slots"), "slot", subscription)
	if after["active"] != "true" || parseSize(t, after["retained_wal"]) >= 1<<20 {
		t.Errorf("after catch-up: %v", after)
	}
	lag, _ := stable(t, pub, "replication-lag", lagRef)
	if len(lag) != 1 || lag[0]["state"] != "streaming" {
		t.Errorf("after catch-up: %v", lag)
	}
}
