package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/pgEdge/pgedge-cli/internal/metricfmt"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// clusterMetricColumns are the table headers for cluster metrics.
var clusterMetricColumns = []string{
	"METRIC", "NODE", "VALUE", "UNIT", "SAMPLES",
}

// --- metrics ---

func newClusterMetricsCmd(rt *module.Runtime) *cobra.Command {
	var (
		startTime string
		endTime   string
	)
	cmd := &cobra.Command{
		Use:   "metrics <cluster_id>",
		Short: "Read a cluster's host metrics",
		Long: `metrics reads the host metrics collected for a cluster's
nodes: CPU, memory, disk, network and process counts.

Each resource carries a current value per node plus the time series
behind it. Text output shows the current value, its unit, and how many
samples the series holds; choose json or yaml for the samples
themselves.

--start-time and --end-time take RFC3339 timestamps and narrow the
window; the API refuses any other format. Values are printed as the API
sends them, unrounded.

The argument takes a cluster's full UUID.

Example:
  pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890
  pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o json
  pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
    --start-time 2026-08-04T00:00:00Z --end-time 2026-08-04T01:00:00Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ReadClusterMetricsParams{}
			f := cmd.Flags()
			if f.Changed("start-time") {
				params.StartTime = &startTime
			}
			if f.Changed("end-time") {
				params.EndTime = &endTime
			}

			resp, err := client.ReadClusterMetricsWithResponse(
				context.Background(), clusterID, params)
			if err != nil {
				return fmt.Errorf("get cluster metrics: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			metrics := resp.JSON200
			if metrics == nil || len(*metrics) == 0 {
				fmt.Fprintln(rt.Stderr, "No metrics found.")
				return nil
			}
			return rt.Output.Print(
				clusterMetricRows(*metrics), clusterMetricColumns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&startTime, "start-time", "",
		"Start of the window, as an RFC3339 timestamp")
	f.StringVar(&endTime, "end-time", "",
		"End of the window, as an RFC3339 timestamp")
	return cmd
}

// clusterMetricRows flattens a Metrics map into table rows, one per node
// per resource.
//
// Keys are sorted so row order is stable across runs. A resource with
// no items still gets a row: on a healthy cluster "status" arrives with
// an empty items list, and dropping it would read as no status at all.
func clusterMetricRows(metrics api.Metrics) []output.Row {
	keys := make([]string, 0, len(metrics))
	for k := range metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([]output.Row, 0, len(keys))
	for _, k := range keys {
		m := metrics[k]
		label := m.Name
		if label == "" {
			label = k
		}
		if len(m.Items) == 0 {
			rows = append(rows, clusterMetricRow{
				metric: label, node: "-", value: "-",
				unit: m.Unit, samples: "0",
			})
			continue
		}
		for _, it := range m.Items {
			rows = append(rows, clusterMetricRow{
				metric:  label,
				node:    it.Name,
				value:   metricfmt.Value(it.Value),
				unit:    m.Unit,
				samples: strconv.Itoa(len(it.Values)),
			})
		}
	}
	return rows
}

// --- row adapter ---

type clusterMetricRow struct {
	metric, node, value, unit, samples string
}

func (r clusterMetricRow) Columns() []string {
	return []string{r.metric, r.node, r.value, r.unit, r.samples}
}
