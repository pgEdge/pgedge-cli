package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/metricfmt"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// metricsIntervalPattern is the managed spec's pattern for the metrics
// interval, checked client-side so a typo costs no round trip.
// TestMetricsIntervalPatternMatchesSpec pins the two, so a re-vendor
// that loosens the spec's cannot leave the CLI silently stricter.
const metricsIntervalPattern = `^[0-9]{1,4}[ ,](second|minute|hour|day)s?$`

//nolint:gocritic // regexpSimplify: the pattern is a verbatim copy of the spec's, and TestMetricsIntervalPatternMatchesSpec compares the two as strings
var metricsIntervalRE = regexp.MustCompile(metricsIntervalPattern)

// metricsIntervalMin is the smallest lookback the CLI sends. It is
// checked apart from the pattern, which is pinned to the spec's as a
// string. The spec and the API both accept `0,minutes`, and the API
// answers 200 with an empty series, which at exit 0 reads like a
// database with no metrics.
//
// There is no upper bound. The spec declares none, the pattern's four
// digits cap the value at 9999, and a lower cap would refuse values the
// API accepts.
const metricsIntervalMin = 1

// databaseMetricColumns are the table headers. Two columns, not one per
// metric: the series is ~36 columns wide, which no terminal renders.
var databaseMetricColumns = []string{"METRIC", "VALUE"}

// newestUsableSample returns the newest sample with a value in every
// column, or failing that the newest non-empty one, plus a note when
// any other row shares its timestamp, whether or not it could have
// been chosen.
//
// Newest means the largest `time`, not the last row. managed.yaml
// declares `values` as an array of arrays of `{}`, with no row order,
// column names or cell types. Rows have been seen oldest-first, but
// nothing promises it. With no `time` column, or no readable value in
// it, row position decides: a missing column is the API changing shape,
// not a caller error. See sampleTimeOutranks.
//
// Completeness comes first because the trailing bucket is still being
// scraped: 18 of 30 consecutive `--window 2,minutes` pulls carried a
// null, every one in the trailing row. A blank cell would read like an
// empty string value.
//
// During an instance replacement two samples share one timestamp, one
// per instance. Nothing published says which is incoming: the
// generation suffix in `instance_name` is undocumented, the column is
// not in the contract, and rows at one timestamp have no published
// order. So the note names the tie and the instance shown, and claims
// nothing about which is right. The transposed view prints the `time`
// row, so the sample shown is always identifiable.
func newestUsableSample(
	s api.MetricSeries,
) (row []interface{}, note string) {
	timeIdx := columnIndex(s.Columns, "time")

	// Pass one: the newest COMPLETE sample.
	if i := newestMatching(s, timeIdx, sampleIsComplete); i >= 0 {
		return s.Values[i], tieNote(s, timeIdx, i)
	}

	// Nothing complete, and a partial sample beats none. Skipping empty
	// rows survives a null row, which the spec rules out (the outer
	// items are `type: array`) and neither module has observed: the
	// nulls managed sends are cells, which `items: {}` permits. byoc's
	// endpoint has sent null where its spec declares an array, which is
	// why both guard against one.
	if i := newestMatching(s, timeIdx, sampleIsNonEmpty); i >= 0 {
		return s.Values[i], tieNote(s, timeIdx, i)
	}
	return nil, ""
}

// sampleIsComplete reports whether row has a value for every column.
//
// It cannot see a column the response never sent. Measured
// 2026-08-22T02:58-03:01Z: a window pinned on a single partly-scraped
// bucket drops the unfilled metrics from `columns` rather than sending
// them null, giving 36, 36, 34, 29, 36 and 34 columns across six
// attempts. Each time, the missing columns were exactly those a wide
// read showed null in that bucket. So the API omits a metric with no
// value anywhere in the window.
//
// Such a response is self-consistent, so this answers true, the table
// has no row for those metrics at exit 0, and the tie note's "none
// complete" clause cannot fire. Catching it would need an expected
// column list, which the spec does not give and hand-written lists
// have got wrong three times here. The command's Long and the module
// reference document it instead, and `-o json` shows what the API sent.
func sampleIsComplete(row []interface{}, columns int) bool {
	if len(row) != columns {
		return false
	}
	for _, v := range row {
		if v == nil {
			return false
		}
	}
	return true
}

// sampleIsNonEmpty is the fallback predicate: any row with a cell in
// it. The column count is unused and named _ rather than dropped so
// both predicates share one signature.
func sampleIsNonEmpty(row []interface{}, _ int) bool {
	return len(row) > 0
}

// newestMatching returns the index of the newest row satisfying ok, or
// -1: the largest `time` when timeIdx names a column (see
// sampleTimeOutranks), and the highest index when it does not or no
// candidate's time is readable. On a tie at the newest time it keeps
// the last row, a deliberate non-choice (see newestUsableSample).
func newestMatching(
	s api.MetricSeries, timeIdx int,
	ok func(row []interface{}, columns int) bool,
) int {
	best := -1
	for i := len(s.Values) - 1; i >= 0; i-- {
		if !ok(s.Values[i], len(s.Columns)) {
			continue
		}
		if timeIdx < 0 {
			// No time column: the first match walking backwards IS
			// the last row, so stop.
			return i
		}
		if best < 0 || sampleTimeOutranks(s, timeIdx, i, best) {
			best = i
		}
	}
	return best
}

// sampleTimeOutranks reports whether row i should displace row j as the
// newest. A readable time outranks an unreadable one wherever it sits.
// If an unreadable cell merely blocked the comparison, one in the
// trailing row, which the backward walk reaches first, could never be
// displaced and would hand every row back to position. Only when no
// candidate has a readable time does position decide, and then the
// last row wins.
func sampleTimeOutranks(s api.MetricSeries, timeIdx, i, j int) bool {
	a, aok := sampleTime(s, timeIdx, i)
	b, bok := sampleTime(s, timeIdx, j)
	if !aok {
		return false
	}
	if !bok {
		return true
	}
	return a > b
}

// sampleTime reads a row's time cell as the float64 encoding/json
// decodes a number to. The values seen are epoch milliseconds near
// 1.787e12, well inside float64's exact-integer range, so comparing
// them is exact. Nothing needs a time.Time, and metricfmt.Value prints
// the column raw.
func sampleTime(s api.MetricSeries, timeIdx, i int) (float64, bool) {
	row := s.Values[i]
	if timeIdx >= len(row) {
		return 0, false
	}
	f, ok := row[timeIdx].(float64)
	return f, ok
}

// tieNote returns the advisory line when more than one row shares the
// chosen row's time, and "" otherwise.
//
// It counts every row at that time, not only rows that could have been
// chosen: an instance still coming up reports nulls, so its row is
// incomplete, and that is the tie a replacement produces. Counting
// every row also makes the number agree with `-o json`. instance_name
// is an opaque label, not parsed: its `-1-1`/`-2-1` generation suffix
// is undocumented, so it cannot say which instance is incoming.
func tieNote(
	s api.MetricSeries, timeIdx, chosen int,
) string {
	if timeIdx < 0 {
		return ""
	}
	want, wok := sampleTime(s, timeIdx, chosen)
	if !wok {
		return ""
	}
	tied, skipped := 0, 0
	for i := range s.Values {
		t, tok := sampleTime(s, timeIdx, i)
		if !tok || t != want {
			continue
		}
		tied++
		// Redundant with the switch below, which reads skipped only when
		// the chosen row is complete, and no test fails without it. It
		// stays so that removing either alone cannot bring back "(2
		// incomplete, so not shown)" beside one of those two rows.
		if i == chosen {
			continue
		}
		if !sampleIsComplete(s.Values[i], len(s.Columns)) {
			skipped++
		}
	}
	if tied < 2 {
		return ""
	}
	shown := ""
	if idx := columnIndex(s.Columns, "instance_name"); idx >= 0 {
		if v, ok := sampleCell(s, idx, chosen); ok && v != "" {
			shown = fmt.Sprintf(", showing instance_name=%s",
				output.Sanitize(v))
		}
	}
	// The skipped count tells a reader mid-handover that the other
	// instance had nothing usable at this time. When the row shown is
	// itself partial, that matters more: its blanks are the API's, not
	// the CLI's.
	incomplete := ""
	switch {
	case !sampleIsComplete(s.Values[chosen], len(s.Columns)):
		incomplete = " (none complete, so the one shown is partial)"
	case skipped > 0:
		incomplete = fmt.Sprintf(" (%d other incomplete, so not shown)",
			skipped)
	}
	return fmt.Sprintf(
		"%d samples share time %s%s%s; the API publishes no order "+
			"between them. Use -o json to read them all.\n",
		// Not sanitized: the note fires only when sampleTime read a
		// float64, which metricfmt.Value renders as digits.
		tied, metricfmt.Value(s.Values[chosen][timeIdx]), incomplete,
		shown)
}

// singleSampleNote warns that a window holding one sample cannot tell a
// metric with no value from one the API did not send. The API omits a
// metric that is null across the whole window, and with one sample
// "null across the window" and "null in that row" are the same, so the
// response's shape alone is enough to warn on, with no column list. It
// counts distinct times, not rows: two rows at one time are one bucket
// reported by two instances.
//
// It fires on a two-minute window about half the time, by arithmetic. A
// window longer than the lag holds floor((W-lag)/30) + 1 samples, so
// with W=120 and a lag of 72 to 101 seconds it holds two at a lag of 90
// or under, one above, and never zero. Measured 6 of 12; the run's
// apparent period came from the sampling cadence, so only the rate is a
// fact about the API. The note is right to fire, so the reference
// recommends three minutes rather than two: 3 minutes measured 4
// distinct samples, 5 measured 8 and 10 measured 18.
//
// One sample is the riskiest shape, not the only one: a metric null
// across N samples is omitted as silently. That is unobserved, as the
// fixture's column sets were 36 at 2 minutes, 10 minutes, 1 hour, 6
// hours and 2 days, so the note covers only the shape it can identify.
func singleSampleNote(s api.MetricSeries) string {
	timeIdx := columnIndex(s.Columns, "time")
	if timeIdx < 0 {
		return ""
	}
	seen := map[float64]bool{}
	for i := range s.Values {
		t, ok := sampleTime(s, timeIdx, i)
		if !ok {
			// An unreadable time cannot be counted as a distinct
			// bucket, and cannot be assumed to duplicate one either,
			// so it disables the note rather than skewing it.
			return ""
		}
		seen[t] = true
	}
	if len(seen) != 1 {
		return ""
	}
	return "This window holds one sample, so a metric with no value " +
		"in it is absent from this table rather than blank. Widen the " +
		"window, or use -o json to see which metrics the API sent.\n"
}

// sampleCell renders one cell as a string, for the tie note's label.
func sampleCell(s api.MetricSeries, idx, i int) (string, bool) {
	row := s.Values[i]
	if idx >= len(row) || row[idx] == nil {
		return "", false
	}
	return metricfmt.Value(row[idx]), true
}

// columnIndex returns the position of name in columns, or -1.
func columnIndex(columns []string, name string) int {
	for i, c := range columns {
		if c == name {
			return i
		}
	}
	return -1
}

// noMetricsMessage explains an empty series in terms of the window that
// produced it. `--window 1,minute` answers 200 with an empty series,
// which at exit 0 reads like a database with no metrics: fifteen
// consecutive runs across two fixtures on 2026-08-22T02:56Z, on top of
// nine earlier ones, while `--window 2,minutes` on the same database in
// the same minute returned rows every time.
//
// The cause is publication lag, so the emptiness is deterministic, not
// a race. The newest sample was 98, 99, 100 and 100 seconds behind the
// wall clock on four consecutive reads, with buckets every 30 seconds
// aligned to :00 and :30, so a window shorter than the lag ends before
// the newest sample exists. Misaligned scrape buckets would have made
// it intermittent, and it is not.
//
// The message names the window rather than refusing it. Raising
// metricsIntervalMin past the lag would refuse a value the API accepts
// and hard-code a server-side number the CLI cannot see change, which
// is also why the lag is described rather than quoted. The lag
// sentence appears only for windows that end now, those without
// --end-time.
func noMetricsMessage(
	f *pflag.FlagSet, window, startTime, endTime string,
) string {
	span := ""
	switch {
	case f.Changed("start-time") && f.Changed("end-time"):
		span = fmt.Sprintf(" between %s and %s", startTime, endTime)
	case f.Changed("start-time"):
		span = fmt.Sprintf(" since %s", startTime)
	case f.Changed("end-time"):
		span = fmt.Sprintf(" up to %s", endTime)
	case f.Changed("window"):
		span = fmt.Sprintf(" for the last %s", window)
	}
	msg := "No metrics found" + span + "."
	if f.Changed("end-time") {
		// A window with an explicit end is a window in the past, and
		// lag has nothing to do with it holding no samples.
		return msg
	}
	// Name the flag that decides the window. openapi/managed.yaml says
	// interval "determines the window only when neither start_time nor
	// end_time is supplied", and adding --window to a --start-time call
	// returned the identical empty answer, so a longer --window there is
	// advice the API ignores.
	fix := "a longer --window may reach one"
	if f.Changed("start-time") {
		fix = "an earlier --start-time may reach one"
	}
	return msg + " The newest published sample lags behind now, so a " +
		"short window can end before it; " + fix + "."
}

// validateMetricsWindow refuses a --window the API would refuse,
// and a zero, which it accepts but cannot answer usefully (see
// metricsIntervalMin). It runs before clientFromCmd, so a typo costs no
// round trip; TestManagedMetricsChecksPrecedeTheClient runs with no
// credentials because a test that supplies them cannot see the order.
func validateMetricsWindow(rt *module.Runtime, v string) error {
	if !metricsIntervalRE.MatchString(v) {
		return newExitError(fmt.Sprintf(
			"invalid --window value %q: expected value,unit — up to "+
				"four digits, then second, minute, hour or day "+
				"(e.g. 15,minutes)", v), ExitUsage)
	}
	// The match guarantees one to four digits then a separator, so the
	// slice is safe and the Atoi error unreachable; it is folded into
	// the bound rather than dropped.
	n, err := strconv.Atoi(v[:strings.IndexAny(v, " ,")])
	if err != nil || n < metricsIntervalMin {
		return newExitError(fmt.Sprintf(
			"invalid --window value %q: expected %d or more — a "+
				"zero-length window holds no sample",
			v, metricsIntervalMin), ExitUsage)
	}
	rt.DryRun.Pass("window %q accepted", v)
	return nil
}

// --- metrics ---

func newDatabaseMetricsCmd(rt *module.Runtime) *cobra.Command {
	var window, startTime, endTime string
	cmd := &cobra.Command{
		Use:   "metrics [<database_id>]",
		Short: "Read a managed database's metrics",
		Long: `metrics reads the Postgres and container metrics
collected for a managed database.

The API returns a wide series: one column per metric — around 36 of
them, from pg_database_size_bytes to the replication slot counts — and
one row per sample. Rows have been observed oldest-first, but the
contract publishes no ordering, so this command reads the 'time'
column rather than trusting the row position. A row whose 'time' cell
is not a number loses to any row whose is, wherever it sits; row
position decides only where no row carries a readable 'time'.

Text output cannot be that table, so it shows ONE sample transposed:
one row per metric. Choose json or yaml to get every sample.

The sample shown is the newest COMPLETE one, not simply the newest.
The trailing bucket is often still being scraped, so its container
metrics — CPU and memory among them — can arrive empty while every
earlier sample is whole. The 'time' row says which sample you were
given. If no sample is complete, the newest is shown as it is.

During an instance replacement two samples can share one timestamp,
one per instance. Text mode shows one and notes the tie on stderr. The
note names the instance_name it showed when the series carries that
column, and adds how many OTHER rows at that timestamp were incomplete
-- or, when none of them was complete, that the sample shown is itself
partial. It cannot say which instance is the incoming one, because
nothing published lets it. Use -o json to read them all.

Values are printed as the API sends them, unrounded and unscaled. The
series' own 'time' column is a raw epoch in milliseconds, and this
command does not reinterpret it.

--window sets a relative lookback in 'value,unit' form and applies
only when neither --start-time nor --end-time is given, though it is
validated either way. A zero value is refused: the API accepts it and
answers with an empty series, which reads exactly like a database with
no metrics. It is unrelated to --wait-interval, the polling period
of the commands that take --wait.

A window shorter than the collector's publication lag is empty, and
that is the collector rather than the database. The newest published
sample lags behind the wall clock and the lag varies, so a one-minute
window is reliably empty. Ask for three minutes or more: a two-minute
window often holds a SINGLE sample, the shape the note above warns
about. The value is not refused, because the API accepts it and the
lag is not something this CLI can see change; the message names the
window it asked for so the two cases can be told apart.

A metric with no value anywhere in the window is OMITTED from the
response rather than shown blank, so a narrow window returns a SHORTER
table. A window pinned on a single partly-scraped bucket has been
measured at 29 and 34 columns against the usual 36, losing the CPU and
memory rows among others. The response is self-consistent, so nothing
here can identify WHICH metrics are missing.

What it can identify is the shape in which they vanish. With exactly
one sample in the window, "no value anywhere in the window" and "no
value in that row" are the same thing, so text mode notes a one-sample
window on stderr, beside the tie note. Widen the window to include a
fully scraped bucket, and use -o json to see exactly which metrics the
API sent.

An empty result means no samples in the window, not a broken endpoint.

The argument takes a full UUID. In a folder linked with 'database link', the ID can be left out.

Example:
  pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 \
    --window 5,minutes
  pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 -o json
  pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 \
    --start-time 2026-08-17T00:00:00Z`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			params := &api.GetManagedDatabaseMetricsParams{}
			if f.Changed("window") {
				if err := validateMetricsWindow(rt, window); err != nil {
					return err
				}
				// The wire parameter is the spec's own name, interval;
				// only the flag spells it --window.
				params.Interval = &window
			}
			if f.Changed("start-time") {
				at, err := parseTimeFlag("--start-time", startTime)
				if err != nil {
					return err
				}
				params.StartTime = &at
			}
			if f.Changed("end-time") {
				at, err := parseTimeFlag("--end-time", endTime)
				if err != nil {
					return err
				}
				params.EndTime = &at
			}

			id, _, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetManagedDatabaseMetricsWithResponse(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("get database metrics: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			var rows []output.Row
			var notes []string
			if resp.JSON200 != nil {
				for _, s := range resp.JSON200.Series {
					latest, note := newestUsableSample(s)
					if len(latest) == 0 {
						continue
					}
					if note != "" {
						notes = append(notes, note)
					}
					if n := singleSampleNote(s); n != "" {
						notes = append(notes, n)
					}
					for i, name := range s.Columns {
						if i >= len(latest) {
							break
						}
						rows = append(rows, metricRow{
							name:  name,
							value: metricfmt.Value(latest[i]),
						})
					}
				}
			}
			if len(rows) == 0 {
				fmt.Fprintln(rt.Stderr,
					noMetricsMessage(f, window, startTime, endTime))
				return nil
			}
			if err := rt.Output.Print(
				rows, databaseMetricColumns); err != nil {
				return err
			}
			// After the table and on STDERR, so stdout stays a clean
			// two-column render and the note cannot be mistaken for a
			// metric. Collected rather than printed inline because a
			// container may carry more than one series.
			for _, n := range notes {
				fmt.Fprint(rt.Stderr, n)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&window, "window", "",
		"Relative lookback window, in value,unit form (1 or more)")
	f.StringVar(&startTime, "start-time", "",
		"Window start as an RFC3339 timestamp")
	f.StringVar(&endTime, "end-time", "",
		"Window end as an RFC3339 timestamp")
	return cmd
}

// --- row adapter ---

type metricRow struct {
	name, value string
}

func (r metricRow) Columns() []string {
	return []string{r.name, r.value}
}
