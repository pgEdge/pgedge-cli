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
// The spec forbids a null row, but this endpoint already answers
// `"series": null` against a required, non-nullable `series` (pinned
// by the null-series test), so a null row is worth surviving though
// never observed. Taking the last row blindly let one null row blank
// the table: "No metrics found." at exit 0.
//
// Not managed's newestUsableSample, which skips back to the newest
// complete row for a trailing scrape bucket that arrives with nulls
// about half the time. byoc's collector differs (79 columns against
// managed's 36, almost no overlap) and carried no null cell in any
// response measured: 13 on production, including a 59-row window, and
// a 30-row window re-measured. Skipping a partial row here would
// report an older sample for no reason.
//
// What it does share is the ordering: "newest" is the largest `time`,
// not the last row, because openapi/byoc.yaml publishes no row order.
// 30 consecutive production samples were ascending by position with no
// two sharing a timestamp, so today this changes nothing.
//
// The `time` match is exact because six other byoc columns contain
// "time": pg_container_cpu_throttled_time,
// pg_stat_database_blk_read_time and blk_write_time, and three
// pg_stat_replication_reply_time_nN.
//
// No tie note, unlike managed's, which exists for an instance
// replacement reporting two samples at one timestamp. Whether two byoc
// nodes can do that is unmeasured. On a tie this keeps the last
// matching row.
func newestNonEmptySample(s api.MetricSeries) []interface{} {
	timeIdx := columnIndex(s.Columns, "time")
	// Full-length rows first, short ones only if none is full length.
	// The column loop stops at a row's last cell, so a short row renders
	// fewer metrics silently, and ranking by time lets it win from
	// anywhere: a 3-cell row with the largest time hid
	// pg_container_cpu_ratio and pg_database_table_count while a 5-cell
	// row was available. This tests length, not null cells, so it is not
	// managed's completeness rule.
	if i := newestMatching(s, timeIdx, sampleIsFullLength); i >= 0 {
		return s.Values[i]
	}
	if i := newestMatching(s, timeIdx, sampleIsNonEmpty); i >= 0 {
		return s.Values[i]
	}
	return nil
}

// sampleIsFullLength reports whether row has a cell for every column;
// a null cell is fine. >= rather than == because an over-long row
// renders every column (extra cells are never read), so demoting it
// would only show an older sample. managed's equivalent uses != and
// carries the same asymmetry.
func sampleIsFullLength(row []interface{}, columns int) bool {
	return len(row) >= columns
}

// sampleIsNonEmpty is the fallback predicate: any row with a cell in
// it.
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
			// No `time` column: position is all there is.
			return i
		}
		if best < 0 || sampleTimeOutranks(s, timeIdx, i, best) {
			best = i
		}
	}
	return best
}

// columnIndex returns the position of name in columns, or -1. The
// comparison is exact; see newestNonEmptySample.
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
// A readable time outranks an unreadable one wherever it sits. Were
// either unreadable cell a veto, the backward walk's first `best` (the
// last row) could never be displaced if its time were unreadable,
// silently restoring the position walk for the whole series.
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
// gives a JSON number. Observed values are epoch milliseconds around
// 1.787e12, inside float64's exact-integer range, so the comparison is
// exact. Unlike managed's they are not bucket-aligned: 1787369521502
// and 1787368651493 carry sub-second parts.
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
			// null, so the nil slice is a real case.
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

// metricsIntervalWirePattern is the check the API runs on byoc's
// metrics interval after replacing every comma with a space. The spec
// declares no pattern, so this mirrors the server;
// TestMetricsIntervalMirrorsTheAPI records when it was read. A miss on
// the server reaches the client as a 500.
const metricsIntervalWirePattern = `^[0-9]+\s+(second|minute|hour|day|week|month|year)s?$`

//nolint:gocritic // regexpSimplify: a verbatim copy of the API's pattern, compared as a string by TestMetricsIntervalMirrorsTheAPI
var metricsIntervalWireRE = regexp.MustCompile(metricsIntervalWirePattern)

// validateMetricsInterval refuses what the server would, at exit 2
// before the request, plus a zero: the interval is a lookback window,
// so a zero-length one answers 200 with an empty series,
// indistinguishable from a database with no metrics.
func validateMetricsInterval(v string) error {
	wire := strings.ReplaceAll(v, ",", " ")
	if !metricsIntervalWireRE.MatchString(wire) {
		return newExitError(fmt.Sprintf(
			"invalid --interval value %q: expected value,unit — digits, "+
				"then second, minute, hour, day, week, month or year "+
				"(e.g. 15,minutes)", v), ExitUsage)
	}
	// After the pattern matched, the only Atoi error left is a number
	// too large for an int.
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
