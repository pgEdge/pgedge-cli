package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// clusterMetricsBody mirrors a prod response, trimmed to three of the
// seven resources. It keeps two things the real payload establishes:
// "resources" arrives as JSON null, and "status" is a real key carrying
// an EMPTY items list with no type and no unit — a renderer that only
// walks items drops it silently.
const clusterMetricsBody = `{` +
	`"cpu":{"end":1785846878,"name":"CPU","start":1785843278,` +
	`"type":"gauge","unit":"pct","items":[{"name":"n1",` +
	`"resources":null,"value":5.106809077670058,` +
	`"values":[[1785843338,4.62],[1785843398,5.11]]}]},` +
	`"memory":{"end":1785846878,"name":"Memory","start":1785843278,` +
	`"type":"gauge","unit":"pct","items":[{"name":"n1",` +
	`"resources":null,"value":19.721560190853978,` +
	`"values":[[1785843338,19.5],[1785843398,19.72]]}]},` +
	`"status":{"end":1785846878,"name":"Status","start":1785843278,` +
	`"type":"","unit":"","items":[]}}`

func TestClusterMetricsRun(t *testing.T) {
	t.Run("text shows a row per node per metric", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, clusterMetricsBody))
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID); err != nil {
			t.Fatalf("cluster metrics: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"METRIC", "NODE", "VALUE", "UNIT", "SAMPLES",
			"CPU", "Memory", "n1", "pct",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q: %q", want, got)
			}
		}
		if !strings.Contains(got, "5.1") {
			t.Errorf("missing the CPU value: %q", got)
		}
		if !strings.Contains(got, "2") {
			t.Errorf("missing the sample count: %q", got)
		}
	})

	// Map iteration order is random in Go, so an unsorted renderer
	// passes and fails at random. Rows are ordered by resource key.
	t.Run("rows are ordered by resource key", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, clusterMetricsBody))
			if err := runAuthed(t, rt, out, url, "cluster", "metrics",
				testClusterID); err != nil {
				t.Fatalf("cluster metrics: %v", err)
			}
			got := out.String()
			cpu := strings.Index(got, "CPU")
			mem := strings.Index(got, "Memory")
			status := strings.Index(got, "Status")
			if cpu < 0 || mem < 0 || status < 0 {
				t.Fatalf("run %d: missing a row: %q", i, got)
			}
			if cpu >= mem || mem >= status {
				t.Fatalf("run %d: rows out of order (cpu=%d memory=%d "+
					"status=%d): %q", i, cpu, mem, status, got)
			}
		}
	})

	// "status" carries no items on a healthy cluster. It must still be
	// reported: a resource silently missing from the table reads as a
	// resource the cluster does not have.
	t.Run("a resource with no items still appears", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, clusterMetricsBody))
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID); err != nil {
			t.Fatalf("cluster metrics: %v", err)
		}
		if !strings.Contains(out.String(), "Status") {
			t.Errorf("dropped the itemless resource: %q", out.String())
		}
	})

	t.Run("json carries the time series", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, clusterMetricsBody))
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID); err != nil {
			t.Fatalf("cluster metrics json: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "1785843338") {
			t.Errorf("json dropped the sample timestamps: %q", got)
		}
	})

	t.Run("empty object reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID); err != nil {
			t.Fatalf("cluster metrics empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("window flags reach the API only when set", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(clusterMetricsBody))
			})
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID, "--start-time", "2026-08-04T00:00:00Z",
			"--end-time", "2026-08-04T01:00:00Z"); err != nil {
			t.Fatalf("cluster metrics with window: %v", err)
		}
		for _, want := range []string{
			"start_time=2026-08-04T00%3A00%3A00Z",
			"end_time=2026-08-04T01%3A00%3A00Z",
		} {
			if !strings.Contains(query, want) {
				t.Errorf("query %q missing %q", query, want)
			}
		}
	})

	t.Run("no window means no query string", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(clusterMetricsBody))
			})
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID); err != nil {
			t.Fatalf("cluster metrics bare: %v", err)
		}
		if query != "" {
			t.Errorf("bare call sent query %q, want none", query)
		}
	})

	// An unparseable --start-time answers 400 "failed to parse
	// start_time"; the CLI does not pre-validate the format.
	t.Run("api error surfaces", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(400,
			`{"code":400,"message":"failed to parse start_time"}`))
		if err := runAuthed(t, rt, out, url, "cluster", "metrics",
			testClusterID, "--start-time", "zzz"); err == nil {
			t.Fatal("expected an error on 400")
		}
	})
}
