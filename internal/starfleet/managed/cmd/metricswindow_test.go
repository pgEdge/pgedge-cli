package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// lagClause is the sentence noMetricsMessage adds for a window that
// ends at now. Held once, because every case below asserts either its
// presence or its ABSENCE -- a test that only looked for it would pass
// against a version that printed it unconditionally, which is the
// mistake worth guarding: it would tell a caller asking about last
// Tuesday that the collector is behind.
const lagClause = "newest published sample lags behind now"

// TestEmptyMetricsNamesTheWindow is the gate for an empty window.
//
// `--window 1,minute` answers 200 with an empty series at exit 0,
// measurably and repeatably -- fifteen consecutive runs on two
// fixtures -- because the newest published sample sits 98 to 100
// seconds behind the wall clock, so a shorter window ends before any
// sample exists. The value is not refused (see noMetricsMessage), so
// the whole fix is that the message distinguishes "the window you
// asked for held nothing" from "this database has no metrics".
//
// Every case pins the window text AND whether the lag clause appears,
// because the two are decided separately: the window comes from which
// flags were given, the clause from whether the window ends at now.
func TestEmptyMetricsNamesTheWindow(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantLag bool
	}{{
		name:    "window names the lookback",
		args:    []string{"--window", "1,minute"},
		want:    "No metrics found for the last 1,minute.",
		wantLag: true,
	}, {
		// A window with an explicit end is a window in the past, and
		// lag has nothing to do with it holding no samples.
		name: "an explicit window is not blamed on lag",
		args: []string{
			"--start-time", "2026-01-01T00:00:00Z",
			"--end-time", "2026-01-01T00:05:00Z"},
		want: "No metrics found between 2026-01-01T00:00:00Z and " +
			"2026-01-01T00:05:00Z.",
		wantLag: false,
	}, {
		name:    "start-time alone still ends at now",
		args:    []string{"--start-time", "2026-01-01T00:00:00Z"},
		want:    "No metrics found since 2026-01-01T00:00:00Z.",
		wantLag: true,
	}, {
		// --end-time alone is the other half of the pair, and it is
		// the case that separates "an end was given" from "both were
		// given" in the switch.
		name:    "end-time alone names the end and is not blamed on lag",
		args:    []string{"--end-time", "2026-01-01T00:05:00Z"},
		want:    "No metrics found up to 2026-01-01T00:05:00Z.",
		wantLag: false,
	}, {
		// No window flag: there is no window to name, because the API
		// picks the default and does not report it.
		name:    "no window flag names no window",
		args:    nil,
		want:    "No metrics found.",
		wantLag: true,
	}, {
		// PRECEDENCE. The spec says interval decides the window only
		// when neither time flag is given, and the API honours that, so
		// naming the interval here would name a window the API did not
		// use. Nothing pinned the switch's arm order until this row:
		// review hoisted the interval arm above the three time arms and
		// the whole package -- 645 subtests -- stayed green.
		name: "start-time wins over window, which the API ignores",
		args: []string{
			"--window", "5,minutes",
			"--start-time", "2026-01-01T00:00:00Z"},
		want:    "No metrics found since 2026-01-01T00:00:00Z.",
		wantLag: true,
	}, {
		name: "end-time wins over window too",
		args: []string{
			"--window", "5,minutes",
			"--end-time", "2026-01-01T00:05:00Z"},
		want:    "No metrics found up to 2026-01-01T00:05:00Z.",
		wantLag: false,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, `{"series":[]}`))
			args := append([]string{"database", "metrics",
				testDatabaseID}, tc.args...)
			if err := runAuthed(t, rt, out, url, args...); err != nil {
				t.Fatalf("database metrics: %v", err)
			}
			got := errb.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("stderr does not name the window\n got: %q\nwant: %q",
					got, tc.want)
			}
			if strings.Contains(got, lagClause) != tc.wantLag {
				t.Errorf("lag clause present=%v, want %v: %q",
					!tc.wantLag, tc.wantLag, got)
			}
			// The REMEDY has to name the flag that decides the window.
			// Recommending a longer --window where --start-time is
			// set is advice the API provably ignores, so the clause
			// being present is not enough -- it has to point at
			// something that works.
			if tc.wantLag {
				wantFix := "a longer --window"
				if strings.Contains(strings.Join(tc.args, " "),
					"--start-time") {
					wantFix = "an earlier --start-time"
				}
				if !strings.Contains(got, wantFix) {
					t.Errorf("remedy is not %q: %q", wantFix, got)
				}
			}
			// The acknowledgement is exit 0 and a sentence on stderr;
			// an empty series carries no body, so stdout stays
			// byte-empty.
			if out.Len() != 0 {
				t.Errorf("stdout is not empty: %q", out.String())
			}
		})
	}
}

// TestEmptyMetricsMessageSurvivesEveryFormat pins the one thing the
// window text must not do: reach stdout. The message is an
// explanation, not data, so json and yaml callers get a byte-empty
// stdout and the same sentence on stderr.
func TestEmptyMetricsMessageSurvivesEveryFormat(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", format)
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, `{"series":[]}`))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID, "--window", "1,minute"); err != nil {
				t.Fatalf("database metrics -o %s: %v", format, err)
			}
			if strings.Contains(out.String(), "No metrics found") {
				t.Errorf("the notice reached stdout: %q", out.String())
			}
			if format != "text" {
				// json and yaml render the empty body the API sent
				// rather than the notice, so there is nothing to
				// assert about stderr here beyond the notice not
				// having been swallowed in text mode.
				return
			}
			if !strings.Contains(errb.String(), "1,minute") {
				t.Errorf("stderr does not name the window: %q",
					errb.String())
			}
		})
	}
}

// TestSingleSampleNote covers the one detection that turned out to
// be allowed, and the reason the other three were rejected.
//
// The API omits a metric that is null across the whole requested
// window. With exactly ONE sample in the window, "null across the
// window" and "null in that row" are the same statement — so the
// omission is undetectable from the response, and the response's own
// shape is nonetheless enough to warn about it. No column vocabulary,
// no second round trip, no cross-row comparison.
func TestSingleSampleNote(t *testing.T) {
	cols := []string{"time", "pg_up"}
	cases := []struct {
		name string
		s    api.MetricSeries
		want bool
	}{{
		name: "one sample warns",
		s: api.MetricSeries{Columns: cols, Values: [][]interface{}{
			{1.0, "a"}}},
		want: true,
	}, {
		// Two rows at ONE timestamp is one bucket reported by two
		// instances — the tie case — so it is still a single sample
		// and still at risk. Counting rows rather than distinct times
		// would miss it.
		name: "two rows sharing a timestamp is still one sample",
		s: api.MetricSeries{Columns: cols, Values: [][]interface{}{
			{1.0, "a"}, {1.0, "b"}}},
		want: true,
	}, {
		name: "two distinct samples do not warn",
		s: api.MetricSeries{Columns: cols, Values: [][]interface{}{
			{1.0, "a"}, {2.0, "b"}}},
		want: false,
	}, {
		// An unreadable time cannot be counted as a distinct bucket
		// and cannot be assumed to duplicate one, so it disables the
		// note rather than skewing it in either direction.
		name: "an unreadable time disables the note",
		s: api.MetricSeries{Columns: cols, Values: [][]interface{}{
			{"nope", "a"}}},
		want: false,
	}, {
		// MIXED is the case that distinguishes "disable" from "skip".
		// With a single unreadable row, `seen` ends empty either way
		// and the count check answers "" -- so review changed the
		// early return to a `continue` and the whole package stayed
		// green. Here, skipping would count ONE readable bucket and
		// warn that the window holds one sample, when it may hold two.
		name: "one readable and one unreadable time disables the note",
		s: api.MetricSeries{Columns: cols, Values: [][]interface{}{
			{1.0, "a"}, {"nope", "b"}}},
		want: false,
	}, {
		name: "no time column disables the note",
		s: api.MetricSeries{Columns: []string{"a", "b"},
			Values: [][]interface{}{{1.0, 2.0}}},
		want: false,
	}, {
		name: "an empty series does not warn",
		s:    api.MetricSeries{Columns: cols},
		want: false,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := singleSampleNote(tc.s) != ""
			if got != tc.want {
				t.Errorf("singleSampleNote warned=%v, want %v",
					got, tc.want)
			}
		})
	}
}

// TestSingleSampleNoteReachesStderrNotStdout pins where it goes. It is
// advice, not data, so stdout stays the clean two-column render a
// caller may be parsing.
func TestSingleSampleNoteReachesStderrNotStdout(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK,
			`{"series":[{"name":"","columns":["time","pg_up"],`+
				`"values":[[1787369521502,1]]}]}`))
	if err := runAuthed(t, rt, out, url, "database", "metrics",
		testDatabaseID); err != nil {
		t.Fatalf("database metrics: %v", err)
	}
	if !strings.Contains(errb.String(), "one sample") {
		t.Errorf("no single-sample warning on stderr: %q",
			errb.String())
	}
	if strings.Contains(out.String(), "one sample") {
		t.Errorf("the warning reached stdout: %q", out.String())
	}
	if !strings.Contains(out.String(), "pg_up") {
		t.Errorf("the table is missing: %q", out.String())
	}
}

// TestTieNoteAndSingleSampleNoteBothReachStderr pins that the two
// stderr notes coexist.
//
// They can fire on the same response, and that response is the one a
// reader most needs both for: an instance handover puts two rows at
// ONE timestamp, which is a tie AND a single sample. Review suppressed
// the single-sample note whenever the tie note had fired and the whole
// package stayed green — the unit table calls singleSampleNote
// directly, and the one end-to-end case used a single row with no tie,
// so nothing proved both reach the user.
func TestTieNoteAndSingleSampleNoteBothReachStderr(t *testing.T) {
	rt, out, errb := testsupport.NewRuntime(t, "", "text")
	// Two rows, one timestamp, one of them incomplete: a tie, and a
	// single distinct sample.
	url := testsupport.NewAuthedServer(t,
		testsupport.JSONHandler(http.StatusOK,
			`{"series":[{"name":"","columns":`+
				`["time","instance_name","pg_up"],"values":[`+
				`[1787369521502,"db-1-1",1],`+
				`[1787369521502,"db-2-1",null]]}]}`))
	if err := runAuthed(t, rt, out, url, "database", "metrics",
		testDatabaseID); err != nil {
		t.Fatalf("database metrics: %v", err)
	}
	got := errb.String()
	if !strings.Contains(got, "samples share time") {
		t.Errorf("no tie note: %q", got)
	}
	if !strings.Contains(got, "one sample") {
		t.Errorf("no single-sample note: %q", got)
	}
	// Neither is data.
	if strings.Contains(out.String(), "share time") ||
		strings.Contains(out.String(), "one sample") {
		t.Errorf("a note reached stdout: %q", out.String())
	}
}

// TestMetricsWindowKeepsTheWireName pins the flag/wire seam
// deliberately created: the flag spells --window, the managed spec's
// query parameter is still `interval`. Without this, a generated client
// emitting `window=` survived every test in the tree (the query string
// was asserted by nothing).
func TestMetricsWindowKeepsTheWireName(t *testing.T) {
	var query string
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"series":[]}`))
		})
	if err := runAuthed(t, rt, out, url, "database", "metrics",
		testDatabaseID, "--window", "5,minutes"); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if !strings.Contains(query, "interval=5%2Cminutes") {
		t.Errorf("query = %q, want the spec's interval parameter "+
			"carrying the --window value", query)
	}
	if strings.Contains(query, "window") {
		t.Errorf("query = %q must not carry a window parameter", query)
	}
}
