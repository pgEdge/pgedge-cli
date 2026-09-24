package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestMetricsIntervalMirrorsTheAPI records where the byoc pattern comes
// from, since the vendored spec declares none to pin against: the
// API's own interval check, at openapi/SOURCE's pinned version
// (read 2026-08-29). The server replaces commas with spaces
// before matching, which is why the CLI does the same. Re-read the
// check when the pin moves; this test cannot see it.
func TestMetricsIntervalMirrorsTheAPI(t *testing.T) {
	const apiPattern = `^[0-9]+\s+(second|minute|hour|day|week|month|year)s?$`
	if metricsIntervalWirePattern != apiPattern {
		t.Errorf("CLI pattern %q drifted from the API's %q",
			metricsIntervalWirePattern, apiPattern)
	}
}

func TestValidateMetricsInterval(t *testing.T) {
	accept := []string{"5,minute", "15,minutes", "1,second", "2,hours",
		"1,day", "3,weeks", "1,month", "1,year", "5 minutes", "0005,minute"}
	for _, v := range accept {
		if err := validateMetricsInterval(v); err != nil {
			t.Errorf("%q refused: %v", v, err)
		}
	}
	reject := map[string]string{
		"0,minutes":                    "zero-length",
		"00,hours":                     "zero-length",
		"5minutes":                     "value,unit",
		"5,fortnight":                  "value,unit",
		"five,minutes":                 "value,unit",
		"-5,minutes":                   "value,unit",
		"5,minutes,":                   "value,unit",
		"":                             "value,unit",
		"5;minutes":                    "value,unit",
		"99999999999999999999,minutes": "too large",
	}
	for v, want := range reject {
		err := validateMetricsInterval(v)
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Errorf("%q: want exit %d, got %v", v, ExitUsage, err)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %q lacks %q", v, err, want)
		}
	}
}

// TestDatabaseMetricsRefusesIntervalBeforeTheRequest pins that a
// refused value must never reach the metrics endpoint. The stub sees
// only that request, so the pin is "before the request", not "before
// the client is built", which the code also does.
func TestDatabaseMetricsRefusesIntervalBeforeTheRequest(t *testing.T) {
	for _, v := range []string{"0,minutes", "5,fortnights"} {
		called := false
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID, "--interval", v)
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code() != ExitUsage {
			t.Fatalf("%q: want exit %d, got %v", v, ExitUsage, err)
		}
		if called {
			t.Errorf("%q: the refused request reached the server", v)
		}
	}
	// Control: a valid interval is sent, verbatim.
	var got string
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Query().Get("interval")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(databaseMetricsBody))
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	if err := runAuthed(t, rt, out, url, "database", "metrics",
		testDatabaseID, "--interval", "5,minutes"); err != nil {
		t.Fatal(err)
	}
	if got != "5,minutes" {
		t.Errorf("interval sent as %q, want it verbatim", got)
	}
}
