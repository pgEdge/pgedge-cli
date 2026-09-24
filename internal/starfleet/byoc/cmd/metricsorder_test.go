package cmd

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
)

func series(columns []string, rows ...[]interface{}) api.MetricSeries {
	return api.MetricSeries{Columns: columns, Values: rows}
}

// TestByocNewestSampleReadsTheTimeColumn is the gate for #322.
//
// TEN cases covering TWO different rule pairs -- worth stating,
// because a single count invites over-reading the coverage.
//
// POSITION vs TIME: three cases distinguish those, and seven
// deliberately expect the last row because that is what the fallbacks
// are for. Measured with the position walk reinstated: 3 fail, 7 pass.
// Those three matter because every response measured on production has
// its newest row LAST, so a test built from real data would have passed
// against the walk this replaced without exercising the change at all.
//
// TIER vs NO TIER: the two length cases distinguish those instead, and
// neither shows up in the 3 above -- "a short row with the largest time
// loses to a complete one" PASSES under the position walk, because the
// walk happens to pick the last row, which is the complete one. So the
// two rule pairs are measured separately and a count of one does not
// speak for the other.
func TestByocNewestSampleReadsTheTimeColumn(t *testing.T) {
	cols := []string{"time", "pg_up"}
	cases := []struct {
		name string
		s    api.MetricSeries
		want interface{}
	}{{
		// The plain inversion.
		name: "newest is the first row",
		s: series(cols,
			[]interface{}{3.0, "newest"},
			[]interface{}{1.0, "oldest"}),
		want: "newest",
	}, {
		// The newest by time sits in the middle, so BOTH a
		// last-row walk and a first-row rule get it wrong.
		name: "newest is in the middle",
		s: series(cols,
			[]interface{}{1.0, "oldest"},
			[]interface{}{9.0, "newest"},
			[]interface{}{2.0, "middling"}),
		want: "newest",
	}, {
		// An empty row must not become the answer, and must not stop
		// the walk either -- that combination is #202, which made one
		// null row blank the whole table.
		name: "an empty trailing row does not win",
		s: series(cols,
			[]interface{}{5.0, "newest"},
			[]interface{}{}),
		want: "newest",
	}, {
		// The asymmetry. An unreadable time in the LAST row must not
		// veto time comparison for the rows behind it: walking
		// backwards it becomes the incumbent first, and a rule that
		// answered "not later" whenever either cell was unreadable
		// would leave it unbeatable, silently restoring the position
		// walk.
		name: "an unreadable time in the last row loses to a readable one",
		s: series(cols,
			[]interface{}{7.0, "newest"},
			[]interface{}{1.0, "older"},
			[]interface{}{"not a number", "unreadable"}),
		want: "newest",
	}, {
		// With NO row carrying a readable time, position is all there
		// is and the last non-empty row wins -- which is what the walk
		// this replaced would have chosen.
		name: "no readable time anywhere falls back to position",
		s: series(cols,
			[]interface{}{"x", "first"},
			[]interface{}{"y", "last"}),
		want: "last",
	}, {
		// No time column at all: the API changed shape, and position
		// is the only rule left.
		name: "no time column falls back to position",
		s: series([]string{"a", "pg_up"},
			[]interface{}{1.0, "first"},
			[]interface{}{2.0, "last"}),
		want: "last",
	}, {
		// byoc has no tie note -- whether two nodes can report at one
		// timestamp is unmeasured -- so this pins the non-choice
		// rather than a mechanism.
		name: "a tie keeps the last of the tied rows",
		s: series(cols,
			[]interface{}{4.0, "first at 4"},
			[]interface{}{4.0, "last at 4"}),
		want: "last at 4",
	}, {
		// A row too short to hold the time cell must not panic and
		// must not win on a time it does not have.
		//
		// GENUINELY short, not empty. A first version passed a
		// zero-length row here, which the non-empty check skips before
		// sampleTime is ever called -- so it was a duplicate of the
		// empty-row case above and sampleTime's bounds guard had NO
		// coverage at all. Review deleted that guard and the whole
		// package still passed; with this row the mutated tree panics
		// with "index out of range [1] with length 1".
		//
		// byoc reaches sampleTime's bounds guard through the ranking
		// loop. managed reaches it too -- through tieNote, which
		// counts EVERY row at the chosen timestamp regardless of
		// completeness, and through its non-empty fallback tier -- so
		// the difference is the ROUTE, not whether it is reachable.
		// That matters because byoc's route is the one byoc's own
		// original fixture failed to exercise, while managed's is the
		// one #320 found.
		name: "a row shorter than the time index does not win",
		s: series([]string{"pg_up", "time"},
			[]interface{}{"newest", 6.0},
			[]interface{}{"short"}),
		want: "newest",
	}, {
		// THE EXPOSURE READING `time` CREATED. A short row used to be
		// able to win only by being LAST; ranking by time lets it win
		// from anywhere, so a partial sample could beat a complete one
		// beside it and silently render fewer metrics. Review
		// reproduced it end to end. The length tier is what stops it,
		// and this is the case that pins the tier.
		name: "a short row with the largest time loses to a complete one",
		s: series([]string{"time", "pg_up", "pg_extra"},
			[]interface{}{9.0, "short"},
			[]interface{}{1.0, "complete", "also"}),
		want: "complete",
	}, {
		// But a short row is still better than nothing: with no
		// full-length row anywhere, the newest short one is the answer
		// rather than an empty table (#202's lesson).
		name: "with no full-length row the newest short one still wins",
		s: series([]string{"time", "pg_up", "pg_extra"},
			[]interface{}{1.0, "older"},
			[]interface{}{9.0, "newest"}),
		want: "newest",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newestNonEmptySample(tc.s)
			// The label is found BY COLUMN NAME, not at a fixed index.
			// One case has to put `time` after the label to make a row
			// genuinely short with respect to the time index, and a
			// hardcoded index silently read the wrong cell there --
			// which is the column-index trap this repo has been caught
			// by before.
			idx := columnIndex(tc.s.Columns, "pg_up")
			if idx < 0 {
				t.Fatalf("fixture has no pg_up column: %v",
					tc.s.Columns)
			}
			if idx >= len(got) {
				t.Fatalf("chose a row too short to hold pg_up: %v",
					got)
			}
			if got[idx] != tc.want {
				t.Errorf("chose %v, want the row marked %v",
					got[idx], tc.want)
			}
		})
	}
}

// TestByocTimeColumnMatchIsExact is the second dimension, and it is
// specific to byoc rather than a copy of managed's concern.
//
// byoc's series carries six columns whose names CONTAIN "time" --
// pg_container_cpu_throttled_time, pg_stat_database_blk_read_time and
// blk_write_time, and three pg_stat_replication_reply_time_nN --
// measured on a 79-column production response. A loose match would
// pick one of those as the ordering key, and because they are real
// numbers it would produce a plausible wrong answer rather than an
// error.
func TestByocTimeColumnMatchIsExact(t *testing.T) {
	decoys := []string{
		"pg_container_cpu_throttled_time",
		"pg_stat_database_blk_read_time",
		"pg_stat_database_blk_write_time",
		"pg_stat_replication_reply_time_n1",
		"pg_stat_replication_reply_time_n2",
		"pg_stat_replication_reply_time_n3",
	}
	for _, d := range decoys {
		if columnIndex([]string{d, "time"}, "time") != 1 {
			t.Errorf("columnIndex matched the decoy %q", d)
		}
	}
	// And the decoys alone must find nothing, so the command falls
	// back to position rather than ordering by a throttled-time
	// counter.
	if got := columnIndex(decoys, "time"); got != -1 {
		t.Errorf("columnIndex(decoys only) = %d, want -1", got)
	}
	// The real column is found wherever it sits. On production it is
	// index 76 of 79 -- followed by wait_event and wait_event_type --
	// so neither first nor last.
	all := append(append([]string{}, decoys...), "time")
	if got := columnIndex(all, "time"); got != len(decoys) {
		t.Errorf("columnIndex = %d, want %d", got, len(decoys))
	}
}

// TestOverLongRowIsNotDemoted pins the >= in sampleIsFullLength.
//
// An over-long row renders every column — the loop iterates the columns
// and breaks at the row's length, so extra cells are never read. With
// `==` such a row was demoted and an OLDER sample shown for no benefit,
// which review measured. The rule has to match its own reason.
func TestOverLongRowIsNotDemoted(t *testing.T) {
	s := series([]string{"time", "pg_up"},
		[]interface{}{9.0, "newest", "extra"},
		[]interface{}{1.0, "older"})
	got := newestNonEmptySample(s)
	if len(got) < 2 || got[1] != "newest" {
		t.Errorf("chose %v, want the over-long newest row", got)
	}
}
