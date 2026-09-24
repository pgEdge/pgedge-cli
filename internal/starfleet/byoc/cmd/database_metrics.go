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
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// databaseMetricColumns are the table headers for database metrics.
// Two columns, not one per metric: see newDatabaseMetricsCmd.
var databaseMetricColumns = []string{"METRIC", "VALUE"}

// newestNonEmptySample returns the newest sample carrying any values,
// skipping rows that are empty or a JSON null.
//
// The spec does not permit a null row — `values`' outer items declare
// `type: array`, and only the cells inside are unconstrained. The API
// sends JSON null where its own spec declares an array anyway: this
// same endpoint answers `"series": null` for an unknown --node-name,
// against a `series` that is required and non-nullable — recorded in
// the comment below and pinned by the null-series test. A null row is
// the same deviation one level down, so it is worth surviving rather
// than trusting the schema about. It has not itself been observed.
//
// Taking the last row blindly made one blank the whole table:
// a null decodes to a zero-length row, the column loop broke on its
// first iteration, and every real sample beside it was discarded —
// "No metrics found." at exit 0, indistinguishable from a database
// that reported nothing.
//
// This is deliberately NOT managed's newestUsableSample, which skips
// back to the newest COMPLETE row. That exists for managed's trailing
// scrape bucket, which is still being written when the request lands
// and so arrives with nulls about half the time. byoc's collector is a
// different one — 79 columns against managed's 36, with almost no
// overlap — and carried no nulls in any response measured: 13 on
// production, including a 59-row window, had zero null cells, and a
// 30-row window re-measured had zero as well. Skipping a
// merely-partial row here would report an older sample for no reason.
// Identical generated types are what made the two look alike; the data
// behind them is not.
//
// WHAT IT DOES SHARE WITH managed IS THE ORDERING. "Newest"
// means the largest `time`, not the last row, because the ORDERING IS
// NOT PUBLISHED: openapi/byoc.yaml declares `values` byte-identically
// to openapi/managed.yaml — an array of arrays of `{}`, no row order,
// no column vocabulary, not even cell types. The completeness argument
// above genuinely does not transfer between the two collectors, and
// none of its reasons is about position-versus-time, so for byoc that
// question was never rebutted; it was only never asked.
//
// Reading the column is free and removes the dependency. Today it
// changes nothing: 30 consecutive samples on production were ascending
// by position, the maximum was the last row, and no two shared a
// timestamp. That is exactly why this is worth doing now rather than
// after a collector change makes it a defect.
//
// The `time` match is EXACT, not a substring, and that is not
// incidental: byoc's series carries six other columns whose names
// contain "time" — pg_container_cpu_throttled_time,
// pg_stat_database_blk_read_time and blk_write_time, and three
// pg_stat_replication_reply_time_nN — any of which a loose match would
// pick up in preference. See columnIndex.
//
// There is no tie note here, unlike managed's. managed's exists for a
// specific mechanism, an instance replacement reporting two samples at
// one timestamp, and byoc's series carries `node_name` rather than
// `instance_name`. Whether two byoc nodes can report at one timestamp
// is UNMEASURED, so inventing a note for it would be asserting a
// mechanism rather than reporting one. On a tie this keeps the last
// matching row, which is what the position walk it replaced would have
// chosen.
func newestNonEmptySample(s api.MetricSeries) []interface{} {
	timeIdx := columnIndex(s.Columns, "time")
	// Full-length rows first, short ones only if none is full length.
	//
	// THIS TIER EXISTS BECAUSE READING `time` CREATED THE NEED FOR IT.
	// The column loop stops at the last cell a row has, so a row with
	// fewer cells than there are columns renders fewer metrics --
	// silently. Under the position walk this replaced, such a row
	// could only win by being LAST; ranking by time lets it win from
	// anywhere, so a partial sample can now beat a complete one
	// sitting beside it in the same series. Review reproduced that end
	// to end: a 3-cell row with the largest time hid
	// pg_container_cpu_ratio and pg_database_table_count while a
	// 5-cell row was available.
	//
	// It is NOT managed's completeness rule. That skips a row with a
	// null CELL, for a collector whose trailing bucket arrives half
	// scraped. This skips a row that is the wrong LENGTH, which is the
	// API changing shape rather than a bucket in progress -- byoc has
	// carried no null cell in any response measured, and that argument
	// is untouched.
	if i := newestMatching(s, timeIdx, sampleIsFullLength); i >= 0 {
		return s.Values[i]
	}
	if i := newestMatching(s, timeIdx, sampleIsNonEmpty); i >= 0 {
		return s.Values[i]
	}
	return nil
}

// sampleIsFullLength reports whether row has a cell for every column.
// It says nothing about the cells' contents: a null is fine here, which
// is what keeps this from becoming managed's completeness rule.
//
// >= rather than ==, so the rule matches its own reason. The whole
// argument is about rows that render FEWER metrics, and an over-long
// row renders every column perfectly -- the loop iterates s.Columns and
// breaks at i >= len(latest), so extra cells are simply never read.
// Demoting such a row would buy nothing and show an older sample.
// Review measured that: with an over-long newest row, == chose the
// older one. managed's equivalent uses != and inherits the same
// asymmetry; byoc is where it is cheap to fix, because byoc's rule is
// about length and nothing else.
func sampleIsFullLength(row []interface{}, columns int) bool {
	return len(row) >= columns
}

// sampleIsNonEmpty is the fallback predicate: any row with a cell in
// it. The column count is unused and named _ rather than dropped so
// both predicates share one signature.
func sampleIsNonEmpty(row []interface{}, _ int) bool {
	return len(row) > 0
}

// newestMatching returns the index of the newest row satisfying ok, or
// -1. "Newest" is the largest `time` when timeIdx names a column, and
// the highest index otherwise -- including when the column is there but
// no candidate row holds a readable value in it.
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
			// No `time` column: the first match walking backwards IS
			// the last row, so position is all there is. A missing
			// column is the API changing shape, not the caller doing
			// anything wrong.
			return i
		}
		if best < 0 || sampleTimeOutranks(s, timeIdx, i, best) {
			best = i
		}
	}
	return best
}

// columnIndex returns the position of name in columns, or -1. The
// comparison is exact; see newestNonEmptySample for why that matters
// here specifically.
func columnIndex(columns []string, name string) int {
	for i, c := range columns {
		if c == name {
			return i
		}
	}
	return -1
}

// sampleTimeOutranks reports whether row i should displace row j as the
// newest.
//
// A READABLE time outranks an unreadable one wherever it sits, and the
// asymmetry is deliberate — it is the fix managed's equivalent needed.
// Comparing both cells and answering "not later" whenever EITHER was
// unreadable vetoed time comparison for the whole series on one bad
// cell: walking backwards, the last row becomes `best` first, and if
// its time were unreadable nothing could displace it, silently
// restoring the position walk for every row behind it. So the
// degradation is scoped to the rows that earn it.
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

// sampleTime reads a row's time cell as a float64.
//
// float64 is what encoding/json gives for a JSON number, and the values
// observed are epoch MILLISECONDS around 1.787e12 — well inside
// float64's exact-integer range, so the comparison is exact. Unlike
// managed's, byoc's are not bucket-aligned: 1787369521502 and
// 1787368651493 carry real sub-second parts, which is another reason
// not to reason about them as a sequence.
func sampleTime(s api.MetricSeries, timeIdx, i int) (float64, bool) {
	row := s.Values[i]
	if timeIdx >= len(row) {
		return 0, false
	}
	f, ok := row[timeIdx].(float64)
	return f, ok
}

// --- metrics ---

func newDatabaseMetricsCmd(rt *module.Runtime) *cobra.Command {
	var (
		interval string
		nodeName string
		columns  string
	)
	cmd := &cobra.Command{
		Use:   "metrics <database_id>",
		Short: "Read a database's metrics",
		Long: `metrics reads the Postgres and container metrics
collected for a database.

The API returns a wide series: one column per metric — around 80 of
them, from pg_database_size_bytes to the Spock subscription counts —
and one row per sample.

Text output cannot be that table, so it shows the MOST RECENT sample
transposed: one row per metric, carrying its newest value. Narrow it
with --columns, or choose json or yaml to get every sample.

"Most recent" means the largest 'time', not the last row. Rows have
been observed oldest-first, but the contract publishes no ordering, so
this command reads the column rather than trusting the position. A row
whose 'time' cell is not a number loses to any row whose is, wherever
it sits; row position decides only where no row carries a readable
'time'.

Values are printed as the API sends them, unrounded and unscaled. In
particular the series' own 'time' column is a raw epoch, and this
command does not reinterpret it.

--interval is how far back to read, not a grouping: the API returns
every sample newer than now minus the interval, 15 minutes when the
flag is omitted. It takes value,unit form, such as 5,minute or
2,hours, with second, minute, hour, day, week, month or year as the
unit. The CLI refuses a malformed value with a usage error before the
request, because the API answers one with a 500 rather than a 400. It
refuses a zero the same way, because the API accepts one and answers
200 with an empty series, which reads as a database with no metrics.
--columns is not checked: the spec publishes no vocabulary for it, and
a name the API does not recognise is a 500.

The argument takes a full UUID.

Example:
  pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --columns pg_database_size_bytes
  pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --interval 5,minute -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("interval") {
				if err := validateMetricsInterval(interval); err != nil {
					return err
				}
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.GetDatabaseMetricsParams{}
			f := cmd.Flags()
			if f.Changed("interval") {
				params.Interval = &interval
			}
			if f.Changed("node-name") {
				params.NodeName = &nodeName
			}
			if f.Changed("columns") {
				params.Columns = &columns
			}

			resp, err := client.GetDatabaseMetricsWithResponse(
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

			// An unknown --node-name answers 200 with series set to JSON
			// null rather than to an empty array, so the nil slice is a
			// real case and not just an absent field.
			var rows []output.Row
			if resp.JSON200 != nil {
				for _, s := range resp.JSON200.Series {
					latest := newestNonEmptySample(s)
					if len(latest) == 0 {
						continue
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
				fmt.Fprintln(rt.Stderr, "No metrics found.")
				return nil
			}
			return rt.Output.Print(rows, databaseMetricColumns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&interval, "interval", "",
		"How far back to read, as value,unit; omitted, the API reads 15 minutes")
	f.StringVar(&nodeName, "node-name", "",
		"Only metrics for this node name")
	f.StringVar(&columns, "columns", "",
		"Comma-separated metric names to return")
	return cmd
}

// --- row adapter ---

type metricRow struct {
	name, value string
}

func (r metricRow) Columns() []string {
	return []string{r.name, r.value}
}

// metricsIntervalWirePattern is the check the API runs on byoc's metrics
// interval, after the server has replaced every comma with a space.
// The vendored spec declares no pattern, so this is mirrored from the
// server rather than the contract, and TestMetricsIntervalMirrorsTheAPI
// records when it was read. A miss on the server is a plain error, which
// reaches the client as a 500.
const metricsIntervalWirePattern = `^[0-9]+\s+(second|minute|hour|day|week|month|year)s?$`

//nolint:gocritic // regexpSimplify: a verbatim copy of the API's pattern, compared as a string by TestMetricsIntervalMirrorsTheAPI
var metricsIntervalWireRE = regexp.MustCompile(metricsIntervalWirePattern)

// validateMetricsInterval refuses what the server would, at exit 2
// before the request, plus a zero: the interval is a lookback window
// (`time >= now() - interval`), so a zero-length one holds no sample
// and answers 200 with an empty series, indistinguishable from a
// database with no metrics at all.
func validateMetricsInterval(v string) error {
	wire := strings.ReplaceAll(v, ",", " ")
	if !metricsIntervalWireRE.MatchString(wire) {
		return newExitError(fmt.Sprintf(
			"invalid --interval value %q: expected value,unit — digits, "+
				"then second, minute, hour, day, week, month or year "+
				"(e.g. 15,minutes)", v), ExitUsage)
	}
	// The pattern matched, so the value opens with digits; the one Atoi
	// error left is a number too large for an int, which Postgres would
	// refuse as an interval too.
	n, err := strconv.Atoi(strings.Fields(wire)[0])
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid --interval value %q: the number is too large", v),
			ExitUsage)
	}
	if n < 1 {
		return newExitError(fmt.Sprintf(
			"invalid --interval value %q: expected 1 or more — a "+
				"zero-length window holds no sample", v), ExitUsage)
	}
	return nil
}
