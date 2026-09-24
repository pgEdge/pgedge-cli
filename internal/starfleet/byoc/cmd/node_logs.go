package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// --- logs ---

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

The log name is a journald selector, and the API does not validate it:
every name answers 200, and an unrecognised one comes back carrying the
same '-- No entries --' as a real log with nothing in it. The CLI passes
that through rather than translating it, because it cannot tell the two
apart. Verified live on a BYOC node, 'system', 'docker' and
'containerd' return entries; 'postgresql', 'pgedge', 'patroni',
'messages', 'syslog', 'journal', 'kern', 'daemon' and 'spock' all
return '-- No entries --', and 'postgres' answers 500.
For Postgres's own log use 'database logs' instead.

--priority, --reverse and --dmesg work. --grep, --since and --until
are currently refused server-side with '500 failed to read log', even
against a log that returns entries without them; they are sent
unchanged so they start working when the API does. --since and --until
take RFC3339 timestamps — the API rejects a bare date outright.

Filters are sent only when you set them.

Text output prints each entry's raw text, one per line, and drops the
blank entry the API appends. json and yaml also carry level, message
and time, but the API leaves all three empty today — only raw_text is
populated.

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

			// The API pads the array with an all-empty element, so text
			// mode skips entries with nothing to print. json and yaml
			// carry the response verbatim.
			var printed int
			for _, e := range *entries {
				if e.RawText == "" {
					continue
				}
				// Verbatim, by the same decision byoc's database logs
				// record: a journald line is CONTENT, and a log reader
				// wants the bytes the node emitted. Nothing is
				// interpolated around it -- one entry, one Fprintln,
				// no header and no columns -- so an embedded newline
				// splits a line the server already controlled rather
				// than forging a field the CLI supplies. managed's
				// renderer IS escaped because it puts a time and a
				// level on the same line (#323, #349).
				fmt.Fprintln(rt.Stdout, e.RawText)
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
