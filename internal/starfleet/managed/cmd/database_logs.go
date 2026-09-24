package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// The max_lines bounds the API enforces, mirrored client-side.
// TestLogsMaxLinesBoundsMatchSpec pins them to the spec.
const (
	logsMaxLinesMin = 1
	logsMaxLinesMax = 1000
)

// logRecordMissing is what a key the API did not send renders as.
const logRecordMissing = "-"

// logLevelWidth pads the level column so the messages line up. 7 fits
// every Postgres level up to WARNING; a longer one pushes its own line
// out rather than being truncated.
const logLevelWidth = 7

// databaseLogRecord is one log line lifted out of the generated bare
// object. The spec declares each item as `type: object` with no
// properties, so Logs is []map[string]interface{}. "level", "message"
// and "time" were observed on devapi 2026-08-17, not contracted, so a
// key that is missing, empty or wrongly typed renders a placeholder
// rather than an error; -o json carries whatever really arrived.
type databaseLogRecord struct {
	Time    string
	Level   string
	Message string
}

func databaseLogRecordsFrom(
	raw []map[string]interface{},
) []databaseLogRecord {
	out := make([]databaseLogRecord, 0, len(raw))
	for _, r := range raw {
		rec := databaseLogRecord{
			Time:    logRecordMissing,
			Level:   logRecordMissing,
			Message: logRecordMissing,
		}
		// encoding/json decodes every number into float64, so the
		// epoch never arrives as an int.
		if ms, ok := r["time"].(float64); ok {
			rec.Time = time.UnixMilli(int64(ms)).UTC().
				Format(time.RFC3339)
		}
		if level, ok := r["level"].(string); ok && level != "" {
			rec.Level = level
		}
		if msg, ok := r["message"].(string); ok && msg != "" {
			rec.Message = msg
		}
		out = append(out, rec)
	}
	return out
}

// validateLogsMaxLines refuses a --max-lines the API would refuse. It
// runs before clientFromCmd, so a bad value is exit 2 rather than a
// credential error.
func validateLogsMaxLines(rt *module.Runtime, n int) error {
	if n < logsMaxLinesMin || n > logsMaxLinesMax {
		return newExitError(fmt.Sprintf(
			"invalid --max-lines value %d: expected %d to %d",
			n, logsMaxLinesMin, logsMaxLinesMax), ExitUsage)
	}
	rt.DryRun.Pass("max-lines %d within %d..%d",
		n, logsMaxLinesMin, logsMaxLinesMax)
	return nil
}

// --- logs ---

func newDatabaseLogsCmd(rt *module.Runtime) *cobra.Command {
	var (
		maxLines           int
		startTime, endTime string
	)
	cmd := &cobra.Command{
		Use:   "logs <database_id>",
		Short: "Read a managed database's logs",
		Long: `logs reads Postgres log records from a managed database.

Records arrive newest first and this command does not re-order them.
Text output prints one line per record — timestamp, level, message —
so it pipes into grep unchanged. The API sends each timestamp as an
epoch in milliseconds; text output renders it as RFC3339 in UTC, while
json and yaml carry the raw epoch.

The API declares each record as an untyped object with no properties,
so none of the three fields is contracted. A record missing any of
them, or carrying one that is empty or of the wrong type, prints '-'
in that position rather than failing.

--max-lines takes 1 to 1000 and defaults, at the API, to 100. Bound an
absolute window with --start-time and --end-time.

An empty result means no records in the window, not a broken endpoint.

The argument takes a full UUID.

Example:
  pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 \
    --max-lines 500
  pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 -o json
  pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 \
    --start-time 2026-08-17T00:00:00Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			params := &api.GetManagedDatabaseLogsParams{}
			if f.Changed("max-lines") {
				if err := validateLogsMaxLines(rt, maxLines); err != nil {
					return err
				}
				params.MaxLines = &maxLines
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

			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetManagedDatabaseLogsWithResponse(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("get database logs: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			if resp.JSON200 == nil || len(resp.JSON200.Logs) == 0 {
				fmt.Fprintln(rt.Stderr, "No logs found.")
				return nil
			}
			// Escaped, unlike byoc's log lines, which print a bare body
			// under a `==> node <==` header. Here time, level and
			// message share one line, so a newline in any of them would
			// forge a second line that reads as a real record, the
			// hazard internal/controlplane/cmd/task.go guards against.
			// Level is a column padded with %-*s, not content.
			//
			// Time is CLI-generated today, but it is escaped too: the
			// keys are observed, not contracted, so a future passthrough
			// must not become the one unescaped column.
			for _, rec := range databaseLogRecordsFrom(resp.JSON200.Logs) {
				fmt.Fprintf(rt.Stdout, "%s  %-*s  %s\n",
					output.Sanitize(rec.Time), logLevelWidth,
					output.Sanitize(rec.Level),
					output.Sanitize(rec.Message))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&maxLines, "max-lines", 0,
		"Maximum log records to return, 1 to 1000 (API default 100)")
	f.StringVar(&startTime, "start-time", "",
		"Window start as an RFC3339 timestamp")
	f.StringVar(&endTime, "end-time", "",
		"Window end as an RFC3339 timestamp")
	return cmd
}
