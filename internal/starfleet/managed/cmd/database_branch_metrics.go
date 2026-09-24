package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/metricfmt"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// newDatabaseBranchMetricsCmd is database_metrics.go's command scoped to
// a branch instead of a database. It shares that file's window
// validation, sample-selection and rendering logic verbatim -- a
// branch's metrics are the same shape on the wire (MetricSeriesContainer),
// just at a path with a second ID segment -- so only the argument count,
// the API call and the user-facing text differ.
func newDatabaseBranchMetricsCmd(rt *module.Runtime) *cobra.Command {
	var window, startTime, endTime string
	cmd := &cobra.Command{
		Use:   "metrics <database_id> <branch_id>",
		Short: "Read a branch's metrics",
		Long: `metrics reads the Postgres and container metrics
collected for a branch. A branch's series never appear under its
source database's metrics, and the source's series never appear here.

Every other behaviour is shared with 'database metrics': --window,
--start-time and --end-time mean the same thing, the newest COMPLETE
sample is what text mode shows, and the same notes fire on stderr for
a tie between instances or a window narrow enough to hold only one
sample. See 'pgedge llms starfleet managed database metrics' for the
full detail on sample selection and the publication lag.

Both arguments take a full UUID.

Example:
  pgedge starfleet managed database branch metrics e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901
  pgedge starfleet managed database branch metrics e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901 \
    --window 5,minutes`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			params := &api.GetBranchMetricsParams{}
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

			databaseID, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			branchID, err := parseUUIDArg(args[1], "branch ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetBranchMetricsWithResponse(
				context.Background(), databaseID, branchID, params)
			if err != nil {
				return fmt.Errorf("get branch metrics: %w", err)
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
