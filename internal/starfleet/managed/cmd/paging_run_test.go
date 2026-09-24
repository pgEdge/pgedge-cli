package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// recordingServer answers every resource request with an empty JSON
// array and records the query string it was asked for.
//
// The query is what these tests assert on, not the rendered table. A
// paging flag's whole job is to reach the wire, and a table assertion
// would pass just as happily against a flag that was parsed and then
// dropped — which is precisely the defect being closed.
//
// asked counts RESOURCE requests only. The OAuth exchange is excluded
// because it is sent before any flag is looked at, so counting it would
// make "nothing was sent" unassertable.
func recordingServer(t *testing.T) (srvURL string, queries func() []url.Values) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == testsupport.TokenPath {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testsupport.TokenBody)
				return
			}
			mu.Lock()
			seen = append(seen, r.URL.Query())
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
		}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), seen...)
	}
}

// TestPagingFlagsRefuseNonPositiveValues is the ruling's core: an
// optional flag given an explicitly empty or non-positive value is a
// usage error, refused locally with NOTHING sent.
//
// The second half is the one that matters and the one a rendered-output
// assertion cannot make. Before this change `--limit 0` was silently
// dropped and the command ran anyway, returning a full default page —
// so a caller who wrote `--limit "$N"` with N unset got MORE rows than
// they asked for and exit 0. Asserting only the exit code would still
// pass if the request went out and the error came back from the server.
func TestPagingFlagsRefuseNonPositiveValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"task limit zero", []string{"task", "list", "--limit", "0"}},
		{"task limit negative", []string{"task", "list", "--limit", "-1"}},
		{"task offset negative", []string{"task", "list", "--offset", "-5"}},
		{"backup limit zero", []string{"backup", "list", "--limit", "0"}},
		{"backup offset negative",
			[]string{"backup", "list", "--offset", "-5"}},
		{"database limit zero",
			[]string{"database", "list", "--limit", "0"}},
		{"database offset negative",
			[]string{"database", "list", "--offset", "-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			srvURL, queries := recordingServer(t)

			err := runAuthedSilent(t, rt, out, srvURL, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted; expected exit %d",
					tc.args, ExitUsage)
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want %d (%v)",
					got, ExitUsage, err)
			}
			if n := len(queries()); n != 0 {
				t.Errorf("%d request(s) reached the server; a value "+
					"refused locally must send nothing", n)
			}
		})
	}
}

// TestPagingFlagsRefuseAboveTheDeclaredMaximum pins the two endpoints
// managed.yaml actually bounds.
//
// The server clamps a larger value SILENTLY, so `--limit 500` came back
// with 100 rows and no indication it had been reduced. A caller cannot
// tell that from a complete result, which is why this is a refusal
// rather than a warning.
func TestPagingFlagsRefuseAboveTheDeclaredMaximum(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		max  int
	}{
		{"backup", []string{"backup", "list", "--limit", "101"},
			backupLimitMax},
		{"database", []string{"database", "list", "--limit", "1001"},
			databaseLimitMax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			srvURL, queries := recordingServer(t)

			err := runAuthedSilent(t, rt, out, srvURL, tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted; expected exit %d",
					tc.args, ExitUsage)
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want %d (%v)", got, ExitUsage, err)
			}
			if n := len(queries()); n != 0 {
				t.Errorf("%d request(s) reached the server", n)
			}
			// The message must name the bound. "invalid value" would
			// leave the caller guessing which of two maxima applies to
			// the flag they just typed.
			if !strings.Contains(err.Error(), strconv.Itoa(tc.max)) {
				t.Errorf("error does not name the maximum %d: %v",
					tc.max, err)
			}
		})
	}

	// The two maxima must not have collapsed into one shared number.
	// A single constant would be wrong for whichever endpoint it did
	// not come from, and that is the defect this file exists to stop.
	if backupLimitMax == databaseLimitMax {
		t.Errorf("backupLimitMax and databaseLimitMax are both %d; "+
			"the specs declare 100 and 1000 and a shared constant "+
			"cannot be right for both", backupLimitMax)
	}
}

// TestUnboundedPagingFlagAcceptsALargeValue is the negative control,
// and it is the most important test here.
//
// /managed/v1/tasks publishes no maximum. The server was MEASURED
// clamping --limit 500 to 100 rows, and the tempting fix is to refuse
// above 100 everywhere. That would refuse a value the API accepts, in
// the direction this repo treats as the serious error: the CLI would
// be enforcing a bound the contract never promised, and would keep
// enforcing it after the API raised the cap.
//
// If someone "tidies" the three NoUpperBound call sites into one shared
// ceiling, this test is what fails.
func TestUnboundedPagingFlagAcceptsALargeValue(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	srvURL, queries := recordingServer(t)

	if err := runAuthedSilent(t, rt, out, srvURL,
		"task", "list", "--limit", "500"); err != nil {
		t.Fatalf("task list --limit 500: %v", err)
	}
	q := queries()
	if len(q) != 1 {
		t.Fatalf("%d requests reached the server, want 1", len(q))
	}
	if got := q[0].Get("limit"); got != "500" {
		t.Errorf("limit reached the wire as %q, want \"500\"", got)
	}
}

// TestPagingFlagsSendWhatWasAsked covers the two boundaries the refusal
// must not swallow: an OMITTED flag still sends nothing, and --offset 0
// is a real value that must be sent.
//
// --offset 0 is the case a "non-positive is a usage error" rule gets
// wrong if it is applied to both flags alike. Zero rows is meaningless;
// the zeroth row is the first page.
func TestPagingFlagsSendWhatWasAsked(t *testing.T) {
	t.Run("omitted sends neither", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		srvURL, queries := recordingServer(t)

		if err := runAuthedSilent(
			t, rt, out, srvURL, "task", "list"); err != nil {
			t.Fatalf("task list: %v", err)
		}
		q := queries()
		if len(q) != 1 {
			t.Fatalf("%d requests, want 1", len(q))
		}
		for _, name := range []string{"limit", "offset"} {
			if _, ok := q[0][name]; ok {
				t.Errorf("%s was sent as %q despite being omitted; "+
					"omitting the flag is how a caller asks for the "+
					"server's default", name, q[0].Get(name))
			}
		}
	})

	t.Run("offset zero is sent", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		srvURL, queries := recordingServer(t)

		if err := runAuthedSilent(t, rt, out, srvURL,
			"task", "list", "--offset", "0"); err != nil {
			t.Fatalf("task list --offset 0: %v", err)
		}
		q := queries()
		if len(q) != 1 {
			t.Fatalf("%d requests, want 1", len(q))
		}
		if got := q[0].Get("offset"); got != "0" {
			t.Errorf("offset = %q, want \"0\" — zero is the first "+
				"page, not an empty value", got)
		}
	})
}
