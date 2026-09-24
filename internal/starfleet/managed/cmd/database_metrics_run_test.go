package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// managedMetricsBody carries two samples whose values DISAGREE. A
// fixture whose samples match cannot prove the renderer picked the
// newest one — it would pass against a renderer that took the first.
// The API returns samples oldest-first, verified 2026-08-17.
const managedMetricsBody = `{"series":[{"name":"","columns":[` +
	`"cpu_seconds_total","instance_name","memory_used_bytes","time"],` +
	`"values":[` +
	`[270.734698,"db-1",931725312,1786991610000],` +
	`[283.303779,"db-1",930353152,1786992420000]]}]}`

func TestDatabaseMetricsRun(t *testing.T) {
	t.Run("text transposes the newest sample", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, managedMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"METRIC", "VALUE", "cpu_seconds_total", "283.303779",
			"instance_name", "db-1", "memory_used_bytes", "930353152",
			"time", "1786992420000",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		// The older sample's distinctive value must NOT appear.
		if strings.Contains(got, "270.734698") {
			t.Errorf("rendered the oldest sample, not the newest:\n%s", got)
		}
	})

	t.Run("json carries every sample", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, managedMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics json: %v", err)
		}
		var got api.MetricSeriesContainer
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("output is not the generated body: %v (%q)",
				err, out.String())
		}
		if len(got.Series) != 1 || len(got.Series[0].Values) != 2 {
			t.Errorf("json output = %+v, want one series of 2 samples",
				got)
		}
	})

	// A window with no samples answers 200 with an empty series, which
	// is what an environment without metrics read credentials also
	// returns. The notice must not imply the endpoint is broken.
	t.Run("empty reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"series":[]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	// A series present but carrying no samples is a separate branch
	// from an absent series.
	t.Run("valueless series reports none found", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK,
				`{"series":[{"name":"","columns":["a"],"values":[]}]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics valueless: %v", err)
		}
		if !strings.Contains(errb.String(), "No metrics found") {
			t.Errorf("missing empty notice: %q", errb.String())
		}
	})

	// More columns than the row has cells. Nothing in the spec forbids
	// it, and indexing past the row would panic.
	t.Run("short row stops at the last cell", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK,
				`{"series":[{"name":"","columns":["alpha","beta","gamma"],`+
					`"values":[[1,2]]}]}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("database metrics short row: %v", err)
		}
		if strings.Contains(out.String(), "gamma") {
			t.Errorf("rendered a column with no cell:\n%s", out.String())
		}
	})

	// The trailing bucket is still being scraped, so its container
	// metrics arrive null while the sample before it is whole.
	// Measured: 18 of 30 consecutive `--window 2,minutes` pulls carried
	// a null, every one in the trailing row. Rendering the last row
	// regardless blanks exactly the metrics the command exists to show.
	t.Run("a partial newest sample falls back to the last complete one",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK,
					`{"series":[{"name":"","columns":`+
						`["cpu_seconds_total","memory_used_bytes","time"],`+
						`"values":[`+
						`[111.5,222,1786991610000],`+
						`[333.5,444,1786992420000],`+
						`[null,null,1786992450000]]}]}`))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID); err != nil {
				t.Fatalf("database metrics: %v", err)
			}
			got := out.String()
			for _, want := range []string{"333.5", "444", "1786992420000"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q from the last complete "+
						"sample:\n%s", want, got)
				}
			}
			// The partial row's own timestamp must not be shown, or the
			// reader cannot tell which sample the values came from.
			if strings.Contains(got, "1786992450000") {
				t.Errorf("rendered the partial newest sample:\n%s", got)
			}
			// And the older complete sample must not win over the newer.
			if strings.Contains(got, "111.5") {
				t.Errorf("fell back further than necessary:\n%s", got)
			}
		})

	// Every sample partial: a partial answer still beats claiming none.
	t.Run("no complete sample still renders the newest",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK,
					`{"series":[{"name":"","columns":["a","b"],`+
						`"values":[[null,1],[null,2]]}]}`))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID); err != nil {
				t.Fatalf("database metrics: %v", err)
			}
			if !strings.Contains(out.String(), "2") {
				t.Errorf("dropped a partial sample entirely:\n%s",
					out.String())
			}
		})

	// A JSON null in `values` is legal — nothing constrains the array —
	// and must not empty the table when real samples sit beside it.
	t.Run("a null row does not hide the real samples",
		func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK,
					`{"series":[{"name":"","columns":["alpha","beta"],`+
						`"values":[[1,2],null]}]}`))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID); err != nil {
				t.Fatalf("database metrics: %v", err)
			}
			if strings.Contains(errb.String(), "No metrics found") {
				t.Errorf("a trailing null row emptied the table: %q",
					errb.String())
			}
			if !strings.Contains(out.String(), "alpha") {
				t.Errorf("missing the real sample:\n%s", out.String())
			}
		})

	t.Run("api error is reported", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusBadRequest,
				`{"code":400,"message":"invalid interval"}`))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err == nil {
			t.Fatal("a 400 was reported as success")
		}
	})
}

// TestValidateMetricsWindow covers the pattern's own edges. The space
// separator is real: the API answered 200 to `interval=15 minutes` on
// 2026-08-17.
func TestValidateMetricsWindow(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"comma separator", "15,minutes", true},
		{"space separator", "15 minutes", true},
		{"singular unit", "1,hour", true},
		{"smallest window", "1,second", true},
		{"four digits", "9999,seconds", true},
		{"largest window the pattern allows", "9999,days", true},
		{"day", "7,days", true},
		{"zero", "0,minutes", false},
		{"zero, space separator", "0 minutes", false},
		{"zero, singular unit", "0,second", false},
		{"zero padded to four digits", "0000,hours", false},
		{"five digits", "99999,minutes", false},
		{"unknown unit", "15,fortnights", false},
		{"no separator", "15minutes", false},
		{"no value", ",minutes", false},
		{"empty", "", false},
		{"trailing text", "15,minutes ago", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			err := validateMetricsWindow(rt, tc.in)
			if tc.ok {
				if err != nil {
					t.Fatalf("rejected %q: %v", tc.in, err)
				}
				if got := rt.DryRun.Checks(); len(got) != 1 {
					t.Errorf("a passing check recorded %q, want one entry",
						got)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %q", tc.in)
			}
			var ee *ExitError
			if !asExitError(err, &ee) || ee.Code() != ExitUsage {
				t.Errorf("error for %q = %v, want exit %d",
					tc.in, err, ExitUsage)
			}
			if got := rt.DryRun.Checks(); len(got) != 0 {
				t.Errorf("a failed check recorded %q", got)
			}
		})
	}
}

// TestMetricsWindowZeroFailsTheRangeCheckNotThePattern is the reason
// the zero check is separate code rather than a tighter regex. The
// pattern is a verbatim copy of the spec's and
// TestMetricsIntervalPatternMatchesSpec compares the two as strings, so
// a regex that refused zero would break that test by design. This
// asserts the split holds: the pattern still MATCHES a zero, and the
// rejection comes from the range check that runs after it.
func TestMetricsWindowZeroFailsTheRangeCheckNotThePattern(t *testing.T) {
	for _, in := range []string{"0,minutes", "0 minutes", "0000,hours"} {
		if !metricsIntervalRE.MatchString(in) {
			t.Errorf("the pattern refuses %q; it has been tightened "+
				"away from the spec's, which accepts a zero", in)
		}
		rt := &module.Runtime{DryRun: dryrun.New()}
		if err := validateMetricsWindow(rt, in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}

// TestMetricsWindowZeroMessageNamesTheBound pins the message shape to
// its sibling --max-lines, which reports "invalid --max-lines value 0:
// expected 1 to 1000". A zero interval used to answer 200 with an empty
// series at exit 0, indistinguishable from a database with no metrics,
// so the message has to say the value was the problem.
func TestMetricsWindowZeroMessageNamesTheBound(t *testing.T) {
	rt := &module.Runtime{DryRun: dryrun.New()}
	err := validateMetricsWindow(rt, "0,minutes")
	if err == nil {
		t.Fatal("accepted a zero-length window")
	}
	for _, want := range []string{"--window", "0,minutes", "zero"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
}

// TestManagedMetricsRejectsAZeroWindow runs with NO credentials, so
// exit 2 rather than exit 5 is the evidence that the zero check, like
// the pattern check, precedes the client and costs no round trip.
func TestManagedMetricsRejectsAZeroWindow(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "metrics",
		testDatabaseID, "--window", "0,minutes")
	if err == nil {
		t.Fatal("a zero --window was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d (got auth instead? then "+
			"the client is being built before the check)",
			err, ExitUsage)
	}
}

// TestManagedMetricsChecksPrecedeTheClient runs with NO credentials
// from any source. Without credentials the command can only fail at
// exit 5 if it built the client first, so exit 2 here is the only
// evidence that the check really does come first and really does save
// the round trip.
func TestManagedMetricsChecksPrecedeTheClient(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runManaged(t, rt, out, "database", "metrics",
		testDatabaseID, "--window", "15,fortnights")
	if err == nil {
		t.Fatal("a bad --window was reported as success")
	}
	var ee *ExitError
	if !asExitError(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("error = %v, want exit %d (got auth instead? then "+
			"the client is being built before the check)",
			err, ExitUsage)
	}
}

// TestManagedMetricsRejectsABadTimeFlag covers the two window flags,
// which share parseTimeFlag with the backup verbs.
func TestManagedMetricsRejectsABadTimeFlag(t *testing.T) {
	for _, flag := range []string{"--start-time", "--end-time"} {
		t.Run(flag, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runManaged(t, rt, out, "database", "metrics",
				testDatabaseID, flag, "yesterday")
			if err == nil {
				t.Fatalf("%s yesterday was reported as success", flag)
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want ExitUsage(%d); err=%v",
					got, ExitUsage, err)
			}
		})
	}
}

// tiedMetricsBody is two COMPLETE samples at one timestamp, one per
// instance — the instance-replacement shape. No shipped fixture had it,
// which is why the defect shipped.
const tiedMetricsBody = `{"series":[{"name":"","columns":[` +
	`"cpu_seconds_total","instance_name","memory_used_bytes","time"],` +
	`"values":[` +
	`[270.734698,"db-1-1",931725312,1787168130000],` +
	`[283.303779,"db-2-1",930353152,1787168130000]]}]}`

// outOfOrderMetricsBody puts the NEWEST sample first. Rows have been
// observed oldest-first, but managed.yaml publishes no ordering at all,
// so a renderer that trusts position is trusting nothing.
const outOfOrderMetricsBody = `{"series":[{"name":"","columns":[` +
	`"cpu_seconds_total","instance_name","memory_used_bytes","time"],` +
	`"values":[` +
	`[283.303779,"db-1",930353152,1786992420000],` +
	`[270.734698,"db-1",931725312,1786991610000]]}]}`

func TestDatabaseMetricsTieAndOrdering(t *testing.T) {
	t.Run("a tie is noted on stderr, not in the table",
		func(t *testing.T) {
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, tiedMetricsBody))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID); err != nil {
				t.Fatalf("metrics: %v", err)
			}
			if !strings.Contains(errb.String(), "2 samples share time") {
				t.Errorf("stderr = %q, want the tie note", errb.String())
			}
			// stdout stays a clean two-column render: the note must
			// not become a row, or a caller parsing the table reads it
			// as a metric named after a sentence.
			if strings.Contains(out.String(), "samples share time") {
				t.Errorf("the note reached stdout: %q", out.String())
			}
		})

	t.Run("json mode is untouched", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(200, tiedMetricsBody))
		if err := runAuthed(t, rt, out, url, "database", "metrics",
			testDatabaseID); err != nil {
			t.Fatalf("metrics json: %v", err)
		}
		if errb.String() != "" {
			t.Errorf("json mode wrote to stderr: %q", errb.String())
		}
		// Both tied samples are still there: the CLI does not
		// deduplicate what the API sent.
		if strings.Count(out.String(), "1787168130000") != 2 {
			t.Errorf("json dropped a tied sample: %q", out.String())
		}
	})

	t.Run("the newest sample wins even when it is not last",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(200, outOfOrderMetricsBody))
			if err := runAuthed(t, rt, out, url, "database", "metrics",
				testDatabaseID); err != nil {
				t.Fatalf("metrics: %v", err)
			}
			// 283.303779 carries the LATER timestamp but the earlier
			// position. The older sample used to be rendered.
			if !strings.Contains(out.String(), "283.303779") {
				t.Errorf("stdout = %q, want the sample at the later "+
					"time regardless of its row position", out.String())
			}
			if strings.Contains(out.String(), "270.734698") {
				t.Errorf("stdout = %q, rendered the older sample",
					out.String())
			}
		})
}
