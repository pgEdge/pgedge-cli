package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// databaseMetricsBody mirrors a prod response, trimmed from 79 columns
// to six. Note what the real payload establishes and this fixture keeps:
// series names are EMPTY, the columns mix strings with numbers, "time"
// is one of the columns rather than a separate field, and there is one
// row per sample.
const databaseMetricsBody = `{"series":[{"columns":[` +
	`"availability_zone","node_name","pg_database_size_bytes",` +
	`"pg_container_cpu_ratio","pg_database_table_count","time"],` +
	`"name":"","values":[` +
	`["us-east-1a","n1",474355391,0.00031078824315297263,42,1785846721493],` +
	`["us-east-1a","n1",474362000,0.00033850786216125796,43,1785846781493]` +
	`]}]}`

// databaseMetricsNullSeries is what an unknown --node-name answers: a
// 200 whose series is JSON null, not an empty array. A renderer that
// only checks len() on a decoded slice copes; one that dereferences the
// container blindly does not.
const databaseMetricsNullSeries = `{"series":null}`

func TestDatabaseMetricsRun(t *testing.T) {
	// Text mode transposes the LATEST sample: 79 columns cannot be a
	// terminal table, and the newest value of each metric is the
	// question a terminal is actually being asked. json and yaml carry
	// every sample.
	t.Run("text shows the latest sample per metric", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"METRIC", "VALUE", "pg_database_size_bytes", "474362000",
			"pg_database_table_count", "43",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q: %q", want, got)
			}
		}
		// The older sample must not be shown: it is the previous value
		// of the same metric, and printing both rows unlabelled would
		// read as two different metrics.
		if strings.Contains(got, "474355391") {
			t.Errorf("text showed a superseded sample: %q", got)
		}
	})

	t.Run("text keeps string-valued columns", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics: %v", err)
		}
		if !strings.Contains(out.String(), "us-east-1a") {
			t.Errorf("dropped a string column: %q", out.String())
		}
	})

	t.Run("json carries every sample", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics json: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "474355391") ||
			!strings.Contains(got, "474362000") {
			t.Errorf("json dropped samples: %q", got)
		}
	})

	t.Run("null series reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, databaseMetricsNullSeries))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID, "--node-name", "zzz"); err != nil {
			t.Fatalf("database metrics null series: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("empty series reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, `{"series":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics empty series: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	// A series carrying columns but no rows has no latest sample to
	// transpose, which is a different shape from no series at all.
	t.Run("series with no samples reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200,
			`{"series":[{"columns":["time"],"name":"","values":[]}]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics no samples: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	t.Run("filters reach the API only when set", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(databaseMetricsBody))
			})
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID, "--interval", "5,minute",
			"--node-name", "n1",
			"--columns", "pg_database_size_bytes"); err != nil {
			t.Fatalf("database metrics with filters: %v", err)
		}
		for _, want := range []string{
			"interval=5%2Cminute", "node_name=n1",
			"columns=pg_database_size_bytes",
		} {
			if !strings.Contains(query, want) {
				t.Errorf("query %q missing %q", query, want)
			}
		}
	})

	t.Run("no filters means no query string", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var query string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(databaseMetricsBody))
			})
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics bare: %v", err)
		}
		if query != "" {
			t.Errorf("bare call sent query %q, want none", query)
		}
	})

	// A JSON null in `values` used to blank the whole table. The last
	// row is the one transposed, and a null row has zero length, so the
	// column loop broke immediately and every real sample beside it was
	// discarded — "No metrics found." at exit 0, which reads as a
	// database with no metrics rather than a rendering fault.
	//
	// The spec does not permit this — `values`' outer items declare
	// `type: array`. It is worth surviving anyway: the same endpoint
	// already answers `"series": null` against a required,
	// non-nullable `series`, which the null-series case above pins.
	t.Run("a null row does not blank the table", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200,
			`{"series":[{"columns":["alpha","beta"],`+
				`"values":[[1,2],null]}]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics null row: %v", err)
		}
		got := out.String()
		if strings.Contains(errb.String(), "No metrics found") {
			t.Fatalf("a null row blanked the table:\n%s", errb.String())
		}
		for _, want := range []string{"alpha", "beta", "1", "2"} {
			if !strings.Contains(got, want) {
				t.Errorf("output is missing %q:\n%s", want, got)
			}
		}
	})

	// byoc must NOT take a deliberately older sample the way managed
	// does. managed skips back to the newest COMPLETE row because its
	// trailing scrape bucket arrives with nulls about half the time;
	// byoc's collector is a different one and carried none in any
	// response measured (prod, 13 responses, zero null cells).
	// Skipping a merely-partial row here would report stale numbers
	// for no reason.
	t.Run("a partial newest row is still preferred", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200,
			`{"series":[{"columns":["alpha","beta"],`+
				`"values":[[1,2],[3,null]]}]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics partial row: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "3") {
			t.Errorf("did not render the newest row:\n%s", got)
		}
		if strings.Contains(got, "1") {
			t.Errorf("fell back to an older sample; byoc must not:\n%s",
				got)
		}
	})

	// A bad --interval or --columns answers 500, not 400: the CLI cannot
	// validate either without an allow-list it has no evidence for, so
	// the server's error is what the user must see.
	t.Run("api error surfaces", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500,
			`{"code":500,"message":"failed to read metrics"}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID, "--interval", "zzz"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}
