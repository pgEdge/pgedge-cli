package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

func newDatabaseWaitCmd(rt *module.Runtime) *cobra.Command {
	var (
		status   string
		timeout  int
		interval int
	)
	cmd := &cobra.Command{
		Use:   "wait <database_id>",
		Short: "Wait for a database to reach a status",
		Long: `wait polls a BYOC database until its status is --status,
available by default.

Use it to chain work after a write whose --wait was not passed, or
after a change this CLI did not start. While the status is creating,
modifying, restoring, maintenance, deleting or archiving it keeps
polling; any other status is final.

Exit codes:
  0  the database reached --status
  1  it settled on another status, which the error names
  3  --wait-timeout passed first
  4  no database has that ID

The argument takes a full UUID.

Example:
  pgedge starfleet byoc database wait f6a7b8c9-d0e1-2345-fabc-456789012345
  pgedge starfleet byoc database wait f6a7b8c9-d0e1-2345-fabc-456789012345 \
    --status degraded --wait-timeout 900`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !slices.Contains(subjectStatuses, status) {
				return newExitError(fmt.Sprintf(
					"unknown status %q (expected one of: %s)", status,
					strings.Join(subjectStatuses, ", ")), ExitUsage)
			}
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			deadline := time.Now().Add(time.Duration(timeout) * time.Second)
			return awaitSettled(rt,
				databaseSettle(client, id.String(), status),
				deadline, timeout,
				time.Duration(max(interval, 1))*time.Second)
		},
	}
	f := cmd.Flags()
	f.StringVar(&status, "status", "available",
		"Status to wait for: "+strings.Join(subjectStatuses, ", "))
	f.IntVar(&timeout, "wait-timeout", 600, "Max seconds to wait")
	f.IntVar(&interval, "wait-interval", 5, "Polling interval in seconds")
	return cmd
}
