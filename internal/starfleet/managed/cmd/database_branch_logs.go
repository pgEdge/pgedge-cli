package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// newDatabaseBranchLogsCmd is database_logs.go's command scoped to a
// branch instead of a database. The record shape on the wire
// (DatabaseLogsResponse) and every rendering rule are shared with that
// file; only the argument count, the API call and the user-facing text
// differ.
func newDatabaseBranchLogsCmd(rt *module.Runtime) *cobra.Command {
	var (
		maxLines           int
		startTime, endTime string
	)
	cmd := &cobra.Command{
		Use:   "logs <database_id> <branch_id>",
		Short: "Read a branch's logs",
		Long: `logs reads Postgres log records from a branch. A
branch's lines never appear under its source database's logs, and the
source's lines never appear here.

Every other behaviour is shared with 'database logs': records arrive
newest first and are not re-ordered, --max-lines takes 1 to 1000
(API default 100), and --start-time/--end-time bound an absolute
window.

Both arguments take a full UUID.

Example:
  pgedge starfleet managed database branch logs e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901
  pgedge starfleet managed database branch logs e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901 \
    --max-lines 500`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			params := &api.GetBranchLogsParams{}
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

			resp, err := client.GetBranchLogsWithResponse(
				context.Background(), databaseID, branchID, params)
			if err != nil {
				return fmt.Errorf("get branch logs: %w", err)
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
