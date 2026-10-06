package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// --- logs ---

// nodeLogLevelWidth fits journald's longest level name, WARNING.
const nodeLogLevelWidth = 7

func newNodeLogsCmd(rt *module.Runtime) *cobra.Command {
	var (
		lines    int
		since    string
		until    string
		priority string
		grep     string
		caseSens bool
		reverse  bool
		dmesg    bool
	)
	cmd := &cobra.Command{
		Use:   "logs <cluster_id> <node> <log_name>",
		Short: "Read a journald log from a cluster node",
		Long: `logs reads one of the journald logs on a single cluster
node.

The node argument accepts a node name as shown by 'node list', or a
full node UUID. A name is resolved through the cluster's node list,
because the API's log path takes a UUID and 'cluster get' does not
report one. The CLUSTER argument takes a full UUID.

The log name is 'system', 'docker' or 'containerd'. The API refuses
any other name with '400 invalid log_name'. For Postgres's own log,
use 'database logs' instead.

--priority, --grep, --reverse and --dmesg work. --since and --until
are currently refused server-side with '500 failed to read log', even
against a log that returns entries without them; they are sent
unchanged so they start working when the API does. --since and --until
take RFC3339 timestamps — the API rejects a bare date outright.

Filters are sent only when you set them.

Text output prints each entry's time, level and message, one per line.
json and yaml also carry raw_text, the entry's full journald record.

Example:
  pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 system
  pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 docker \
    --lines 200 --reverse
  pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 system \
    --priority err`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			nodeID, err := resolveNodeID(
				context.Background(), client, clusterID, args[1])
			if err != nil {
				return err
			}

			// Optional filters are sent only when set: an empty --grep or
			// a zero --lines would change what the API returns, and on a
			// real node an unwanted grep answers 500.
			params := &api.GetClusterLogsParams{}
			f := cmd.Flags()
			if f.Changed("lines") {
				params.Lines = &lines
			}
			if f.Changed("since") {
				params.Since = &since
			}
			if f.Changed("until") {
				params.Until = &until
			}
			if f.Changed("priority") {
				params.Priority = &priority
			}
			if f.Changed("grep") {
				params.Grep = &grep
			}
			if f.Changed("case-sensitive") {
				params.CaseSensitive = &caseSens
			}
			if f.Changed("reverse") {
				params.Reverse = &reverse
			}
			if f.Changed("dmesg") {
				params.Dmesg = &dmesg
			}

			resp, err := client.GetClusterLogsWithResponse(
				context.Background(), clusterID, nodeID, args[2], params)
			if err != nil {
				return fmt.Errorf("get node logs: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			entries := resp.JSON200
			if entries == nil || len(*entries) == 0 {
				fmt.Fprintln(rt.Stderr, "No log entries found.")
				return nil
			}

			// raw_text is the whole journald JSON record, unreadable as
			// text, so print the parsed fields. An older API fills only
			// raw_text and pads the array with an all-empty element.
			var printed int
			for _, e := range *entries {
				switch {
				case e.Message != "":
					// Escaped, as in managed's renderer: time, level and
					// message share a line, so an embedded newline would
					// forge a record.
					fmt.Fprintf(rt.Stdout, "%s  %-*s  %s\n",
						output.Sanitize(e.Time), nodeLogLevelWidth,
						output.Sanitize(e.Level),
						output.Sanitize(e.Message))
				case e.RawText != "":
					// Verbatim: nothing the CLI supplies shares the line.
					fmt.Fprintln(rt.Stdout, e.RawText)
				default:
					continue
				}
				printed++
			}
			if printed == 0 {
				fmt.Fprintln(rt.Stderr, "No log entries found.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&lines, "lines", 0,
		"Maximum number of log lines to return")
	f.StringVar(&since, "since", "",
		"Only entries at or after this RFC3339 timestamp")
	f.StringVar(&until, "until", "",
		"Only entries at or before this RFC3339 timestamp")
	f.StringVar(&priority, "priority", "",
		"Only entries at this journald priority, such as err")
	f.StringVar(&grep, "grep", "",
		"Only entries whose message matches this regular expression")
	f.BoolVar(&caseSens, "case-sensitive", false,
		"Make --grep case-sensitive")
	f.BoolVar(&reverse, "reverse", false,
		"Show the newest entries first")
	f.BoolVar(&dmesg, "dmesg", false,
		"Show only kernel entries")
	return cmd
}
