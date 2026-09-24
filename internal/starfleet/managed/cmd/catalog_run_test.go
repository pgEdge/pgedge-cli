package cmd

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// --- fixtures ---

const regionsJSON = `[{"region":"us-east-2"},{"region":"eu-west-1"}]`

// sizesJSON carries one priced size and one with no price at all, so
// the blank-vs-zero distinction is exercised rather than assumed.
const sizesJSON = `[
	{"id":"01952c3a-0000-7000-8000-000000000001",
	 "name":"small","display_name":"Small",
	 "cpu_limit":"1000m","cpu_request":"250m",
	 "memory_limit":"2Gi","memory_request":"1Gi",
	 "storage_size":"25Gi","status":"active",
	 "qos_class":"burstable","version":1,
	 "postgres_settings":{"max_connections":"25"},
	 "created_at":"2026-06-01T20:48:06Z",
	 "pricing":{"billing_interval":"month","currency":"usd",
	            "price_amount":2500}},
	{"id":"01952c3a-0000-7000-8000-000000000002",
	 "name":"large","display_name":"Large",
	 "cpu_limit":"2000m","cpu_request":"500m",
	 "memory_limit":"8Gi","memory_request":"4Gi",
	 "storage_size":"50Gi","status":"deprecated",
	 "qos_class":"burstable","version":1,
	 "postgres_settings":{"max_connections":"50"},
	 "created_at":"2026-06-01T20:48:06Z"}
]`

const oneSizeJSON = `{"id":"01952c3a-0000-7000-8000-000000000001",
	"name":"small","display_name":"Small",
	"cpu_limit":"1000m","cpu_request":"250m",
	"memory_limit":"2Gi","memory_request":"1Gi",
	"storage_size":"25Gi","status":"active",
	"qos_class":"burstable","version":1,
	"postgres_settings":{"max_connections":"25"},
	"created_at":"2026-06-01T20:48:06Z"}`

const tasksJSON = `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"create-managed",
	"status":"succeeded","subject_id":"db-1","subject_kind":"database",
	"messages":[],"created_at":"2026-08-03T18:22:38Z",
	"updated_at":"2026-08-03T18:24:00Z"}]`

// pricingFixture builds the price object the API embeds when a size
// list is asked to include pricing.
func pricingFixture(
	amount int64, currency, interval string,
) *api.SizePricing {
	return &api.SizePricing{
		PriceAmount:     amount,
		Currency:        currency,
		BillingInterval: interval,
	}
}

func jsonServer(t *testing.T, body string) string {
	t.Helper()
	return testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
}

// --- pg-version ---

// pgVersionsJSON carries the default and a non-default, so the
// yes/no rendering is exercised in both directions rather than
// assumed. Captured 2026-08-28.
const pgVersionsJSON = `[{"version":"18","default":true},
	{"version":"17","default":false},
	{"version":"16","default":false}]`

func TestPgVersionListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, pgVersionsJSON)
		if err := runAuthed(t, rt, out, url,
			"pg-version", "list"); err != nil {
			t.Fatalf("pg-version list: %v", err)
		}
		for _, want := range []string{
			"VERSION", "DEFAULT", "18", "17", "16", "yes", "no",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q: %q", want, out.String())
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[]`)
		if err := runAuthed(t, rt, out, url,
			"pg-version", "list"); err != nil {
			t.Fatalf("pg-version list empty: %v", err)
		}
		if !strings.Contains(
			errb.String(), "No Postgres versions found") {
			t.Errorf("want the empty notice: %q", errb.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := jsonServer(t, pgVersionsJSON)
		if err := runAuthed(t, rt, out, url,
			"pg-version", "list"); err != nil {
			t.Fatalf("pg-version list json: %v", err)
		}
		// UNMARSHALLED, not substring-matched. Wrapping the array in
		// an object passes a Contains check: every substring below
		// still appears inside {"versions": [...]}, and
		// `jq '.[].version'` would then fail against output this test
		// called correct. Decoding into a slice is what pins
		// the top level as an array, and the typed Default field is
		// what pins the boolean as a boolean rather than the table's
		// yes/no.
		var got []struct {
			Version string `json:"version"`
			Default bool   `json:"default"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not a top-level JSON array of "+
				"{version, default}: %v\noutput: %q",
				err, out.String())
		}
		want := []struct {
			Version string `json:"version"`
			Default bool   `json:"default"`
		}{
			{"18", true}, {"17", false}, {"16", false},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("decoded %+v, want %+v (order included: the API "+
				"answers newest first and the array passes through "+
				"unchanged)", got, want)
		}
	})

	t.Run("plural alias", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, pgVersionsJSON)
		if err := runAuthed(t, rt, out, url,
			"pg-versions", "list"); err != nil {
			t.Fatalf("pg-versions list: %v", err)
		}
		if !strings.Contains(out.String(), "18") {
			t.Errorf("plural alias printed nothing: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(500)
			})
		if err := runAuthed(t, rt, out, url,
			"pg-version", "list"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// --- region ---

func TestRegionListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, regionsJSON)
		if err := runAuthed(t, rt, out, url,
			"region", "list"); err != nil {
			t.Fatalf("region list: %v", err)
		}
		for _, want := range []string{"REGION", "us-east-2", "eu-west-1"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q: %q", want, out.String())
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[]`)
		if err := runAuthed(t, rt, out, url,
			"region", "list"); err != nil {
			t.Fatalf("region list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No regions found") {
			t.Errorf("want the empty notice: %q", errb.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := jsonServer(t, regionsJSON)
		if err := runAuthed(t, rt, out, url,
			"region", "list"); err != nil {
			t.Fatalf("region list json: %v", err)
		}
		// The API answers with objects, not bare strings, so a json
		// consumer reads .[].region — pin that shape.
		if !strings.Contains(out.String(), `"region":"us-east-2"`) {
			t.Errorf("json is not the raw API shape: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(500)
			})
		if err := runAuthed(t, rt, out, url,
			"region", "list"); err == nil {
			t.Fatal("expected an error on 500")
		}
	})
}

// --- size ---

func TestSizeListRun(t *testing.T) {
	t.Run("text without pricing", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, sizesJSON)
		if err := runAuthed(t, rt, out, url,
			"size", "list"); err != nil {
			t.Fatalf("size list: %v", err)
		}
		s := out.String()
		for _, want := range []string{
			"NAME", "small", "Small", "1000m", "2Gi", "25Gi", "active",
			"large", "deprecated",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q: %q", want, s)
			}
		}
		// The price column costs a server-side lookup, so it must not
		// appear unless it was asked for.
		if strings.Contains(s, "PRICE") {
			t.Errorf("PRICE column without --pricing: %q", s)
		}
	})

	t.Run("text with pricing", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotQuery string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sizes") {
					gotQuery = r.URL.RawQuery
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(sizesJSON))
			})
		if err := runAuthed(t, rt, out, url,
			"size", "list", "--pricing"); err != nil {
			t.Fatalf("size list --pricing: %v", err)
		}
		if !strings.Contains(gotQuery, "include=pricing") {
			t.Errorf("query %q did not ask for pricing", gotQuery)
		}
		s := out.String()
		if !strings.Contains(s, "PRICE") {
			t.Errorf("missing PRICE column: %q", s)
		}
		// 2500 minor units of usd is $25.00, not 2500.
		if !strings.Contains(s, "25.00 USD/month") {
			t.Errorf("price not rendered in major units: %q", s)
		}
		// The unpriced size must show a blank cell, never a zero —
		// "0.00" would read as free.
		if strings.Contains(s, "0.00") {
			t.Errorf("unpriced size rendered as zero: %q", s)
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[]`)
		if err := runAuthed(t, rt, out, url,
			"size", "list"); err != nil {
			t.Fatalf("size list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No sizes found") {
			t.Errorf("want the empty notice: %q", errb.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := jsonServer(t, sizesJSON)
		if err := runAuthed(t, rt, out, url,
			"size", "list"); err != nil {
			t.Fatalf("size list json: %v", err)
		}
		// postgres_settings never reaches text output, so json is the
		// only way to read it.
		if !strings.Contains(out.String(), "postgres_settings") {
			t.Errorf("json dropped postgres_settings: %q", out.String())
		}
	})
}

func TestSizeGetRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, oneSizeJSON)
		if err := runAuthed(t, rt, out, url,
			"size", "get", testSizeID); err != nil {
			t.Fatalf("size get: %v", err)
		}
		s := out.String()
		if !strings.Contains(s, "small") {
			t.Errorf("missing the size: %q", s)
		}
		// This endpoint takes no `include`, so pricing can never be
		// populated — a PRICE column here would be blank on every row.
		if strings.Contains(s, "PRICE") {
			t.Errorf("PRICE column on an endpoint that cannot fill "+
				"it: %q", s)
		}
	})

	// A size name is a usage error, not a request: the API addresses
	// sizes by UUID, so sending "small" would 404 with nothing useful.
	t.Run("rejects a size name locally", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var called bool
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sizes") {
					called = true
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(oneSizeJSON))
			})
		err := runAuthed(t, rt, out, url, "size", "get", "small")
		if err == nil {
			t.Fatal("expected an error for a size name")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitUsage {
			t.Fatalf("want ExitUsage, got %v", err)
		}
		if !strings.Contains(err.Error(), "size list -o json") {
			t.Errorf("error does not say where to get an id: %v", err)
		}
		if called {
			t.Error("a request was issued for an argument that " +
				"could never be a size id")
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := jsonServer(t, oneSizeJSON)
		if err := runAuthed(t, rt, out, url,
			"size", "get", testSizeID); err != nil {
			t.Fatalf("size get json: %v", err)
		}
		if !strings.Contains(out.String(), "postgres_settings") {
			t.Errorf("json dropped postgres_settings: %q", out.String())
		}
	})
}

// TestFormatPricing pins the arithmetic, including the currencies
// whose smallest unit is the unit. Dividing one of those by 100 would
// report a price 100x too low and look entirely plausible.
func TestFormatPricing(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		interval string
		want     string
	}{
		{"usd whole", 9900, "usd", "month", "99.00 USD/month"},
		{"usd with cents", 2599, "usd", "month", "25.99 USD/month"},
		{"usd under a unit", 5, "usd", "month", "0.05 USD/month"},
		{"usd zero", 0, "usd", "month", "0.00 USD/month"},
		{"eur", 1000, "eur", "year", "10.00 EUR/year"},
		// No subdivision: 500 JPY is 500 yen, not 5.
		{"jpy has no subunit", 500, "jpy", "month", "500 JPY/month"},
		{"krw has no subunit", 12000, "krw", "month", "12000 KRW/month"},
		{"case is normalised", 100, "USD", "month", "1.00 USD/month"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatPricing(pricingFixture(
				tc.amount, tc.currency, tc.interval))
			if got != tc.want {
				t.Errorf("formatPricing = %q, want %q", got, tc.want)
			}
		})
	}

	// No price configured is a blank cell, never a zero: a zero would
	// read as free.
	if got := formatPricing(nil); got != "" {
		t.Errorf("formatPricing(nil) = %q, want an empty cell", got)
	}
}

// --- task ---

func TestManagedTaskListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, tasksJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "list"); err != nil {
			t.Fatalf("task list: %v", err)
		}
		s := out.String()
		for _, want := range []string{
			"7db8f401-0d8d-4daf-889b-f721e395df61", "create-managed", "succeeded",
			// SUBJECT is <kind>/<id>, so one column answers "what was
			// this task about".
			"database/db-1",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q: %q", want, s)
			}
		}
	})

	t.Run("filters reach the query", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		var gotQuery string
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tasks") {
					gotQuery = r.URL.RawQuery
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tasksJSON))
			})
		if err := runAuthed(t, rt, out, url, "task", "list",
			"--subject-id", testDatabaseID, "--subject-kind", "database",
			"--status", "failed", "--name", "rotate-password-managed",
			"--limit", "20", "--offset", "5"); err != nil {
			t.Fatalf("task list filters: %v", err)
		}
		for _, want := range []string{
			"subject_id=" + testDatabaseID, "subject_kind=database",
			"status=failed", "name=rotate-password-managed",
			"limit=20", "offset=5",
		} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q missing %q", gotQuery, want)
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[]`)
		if err := runAuthed(t, rt, out, url,
			"task", "list"); err != nil {
			t.Fatalf("task list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No tasks found") {
			t.Errorf("want the empty notice: %q", errb.String())
		}
	})
}

func TestManagedTaskGetRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, tasksJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get: %v", err)
		}
		if !strings.Contains(out.String(), "7db8f401-0d8d-4daf-889b-f721e395df61") {
			t.Errorf("missing the task: %q", out.String())
		}
	})

	t.Run("not found exits 4", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[]`)
		err := runAuthed(t, rt, out, url, "task", "get", testTaskID)
		if err == nil {
			t.Fatal("expected an error for a missing task")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitNotFound {
			t.Fatalf("want ExitNotFound, got %v", err)
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := jsonServer(t, tasksJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "get", testTaskID); err != nil {
			t.Fatalf("task get json: %v", err)
		}
		// Text output shows only the LATEST message, so json
		// must still carry the whole per-step trace.
		if !strings.Contains(out.String(), "messages") {
			t.Errorf("json dropped the message trace: %q", out.String())
		}
	})
}

func TestManagedTaskWaitRun(t *testing.T) {
	t.Run("succeeded exits 0", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, tasksJSON)
		if err := runAuthed(t, rt, out, url,
			"task", "wait", testTaskID); err != nil {
			t.Fatalf("task wait: %v", err)
		}
		if !strings.Contains(errb.String(), "succeeded") {
			t.Errorf("want a success line: %q", errb.String())
		}
	})

	t.Run("failed exits 1 and names the reason", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"create-managed",
			"status":"failed","subject_id":"db-1",
			"subject_kind":"database","messages":[],
			"error":"unsupported version",
			"created_at":"2026-08-03T18:22:38Z",
			"updated_at":"2026-08-03T18:24:00Z"}]`)
		err := runAuthed(t, rt, out, url,
			"task", "wait", testTaskID)
		if err == nil {
			t.Fatal("expected an error for a failed task")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitGeneral {
			t.Fatalf("want ExitGeneral, got %v", err)
		}
		// A script should not need a second call to learn why.
		if !strings.Contains(err.Error(), "unsupported version") {
			t.Errorf("error does not carry the reason: %v", err)
		}
	})

	t.Run("timeout exits 3", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := jsonServer(t, `[{"id":"7db8f401-0d8d-4daf-889b-f721e395df61","name":"create-managed",
			"status":"running","subject_id":"db-1",
			"subject_kind":"database","messages":[],
			"created_at":"2026-08-03T18:22:38Z",
			"updated_at":"2026-08-03T18:24:00Z"}]`)
		err := runAuthed(t, rt, out, url, "task", "wait", testTaskID,
			"--wait-timeout", "0", "--wait-interval", "1")
		if err == nil {
			t.Fatal("expected a timeout")
		}
		var ee *ExitError
		if !asExitError(err, &ee) || ee.Code() != ExitTimeout {
			t.Fatalf("want ExitTimeout, got %v", err)
		}
		if !strings.Contains(err.Error(), "running") {
			t.Errorf("timeout does not report the last status: %v", err)
		}
	})
}
