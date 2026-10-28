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
		Use:   "wait [<database_id>]",
		Short: "Wait for a database to reach a status",
		Long: `wait polls a managed database until its status is --status,
available by default.

Use it to chain work after a write whose --wait was not passed, or
after a change this CLI did not start. While the status is creating,
modifying, deleting, suspending or resuming it keeps polling; any
other status is final.

Exit codes:
  0  the database reached --status
  1  it settled on another status, which the error names
  3  --wait-timeout passed first
  4  no database has that ID

The argument takes a full UUID. In a folder linked with
'database link', the ID can be left out.

Example:
  pgedge starfleet managed database wait e5f6a7b8-c9d0-1234-efab-567890123456
  pgedge starfleet managed database wait e5f6a7b8-c9d0-1234-efab-567890123456 \
    --status suspended --wait-timeout 900`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !slices.Contains(subjectStatuses, status) {
				return newExitError(fmt.Sprintf(
					"unknown status %q (expected one of: %s)", status,
					strings.Join(subjectStatuses, ", ")), ExitUsage)
			}
			id, _, err := databaseArg(rt, args, 0)
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
