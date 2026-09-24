package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

var versionColumns = []string{"VERSION", "REVISION", "ARCH"}

type versionRow struct{ version, revision, arch string }

func (r versionRow) Columns() []string {
	return []string{r.version, r.revision, r.arch}
}

// newVersionCmd builds `pgedge controlplane version`.
func newVersionCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the Control Plane server version",
		Long: `version reports the version of the control-plane server
that controlplane is pointed at (distinct from 'pgedge version', which reports
the CLI's own version). It doubles as a reachability check.

A server below this CLI's supported floor (>= ` + SupportFloor + `)
prints a one-time warning to stderr.

Example:
  pgedge controlplane version
  pgedge controlplane version -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetVersionWithResponse(
				context.Background())
			if err != nil {
				return networkError("get version", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if resp.JSON200 != nil {
				// With more than one --base-url, clientFromCmd's
				// selectBaseURL already probed every candidate's
				// version to pick a live server, and already emitted
				// this same warning if that server was below floor —
				// warning again here would double it (both read the
				// same server's version). Only the single-base-url
				// fast path — the common case, which never probes —
				// reaches this point without having warned already.
				// clientFromCmd above already resolved this
				// connection and returned any error, so this cannot
				// fail; checking again would add a branch no test
				// can reach.
				c, _ := resolveConnection(rt, cmd)
				if len(c.baseURLs) <= 1 {
					warnBelowFloor(rt, resp.JSON200.Version)
				}
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			v := resp.JSON200
			if v == nil {
				fmt.Fprintln(rt.Stderr, "No version data returned.")
				return nil
			}
			rows := []output.Row{versionRow{
				version: v.Version, revision: v.Revision, arch: v.Arch,
			}}
			return rt.Output.Print(rows, versionColumns)
		},
	}
}
