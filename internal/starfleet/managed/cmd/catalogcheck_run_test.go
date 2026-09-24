package cmd

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// Two checks: --size and --region measured against what the API
// publishes, rather than left for the API to refuse after a clean dry
// run had said the run would succeed.

// catalogServer answers the two catalog reads from withCatalog's
// fixture and records whether anything was WRITTEN.
func catalogServer(t *testing.T, wrote *bool) string {
	t.Helper()
	return testsupport.NewAuthedServer(t, withCatalog(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.Method != http.MethodGet {
			*wrote = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(databaseJSON(testDatabaseID, "")))
	}))
}

func TestDatabaseCreateChecksTheCatalog(t *testing.T) {
	cases := map[string]struct {
		flag, value string
		wantExit    int
		wantIn      string
	}{
		// The two values measured passing a dry run and then
		// failing the real call.
		"unknown size": {
			"--size", "enormous", ExitUsage, "large, small, xl"},
		"unknown region": {
			"--region", "mars-1", ExitUsage, "us-east-1, us-east-2"},
		// An unset shell variable, which is how both arrive in a
		// script. Refused before the client is built, so this holds
		// with no credentials at all.
		"empty size":   {"--size", "", ExitUsage, "empty value"},
		"empty region": {"--region", "", ExitUsage, "empty value"},
		// Positive controls. A check that refused every value would
		// pass every case above.
		"published size":   {"--size", "xl", 0, ""},
		"published region": {"--region", "us-east-2", 0, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wrote := false
			url := catalogServer(t, &wrote)
			rt, out, errb := testsupport.NewRuntime(t, "", "text")

			args := []string{"database", "create", "--name", "mydb",
				"--region", "us-east-1", "--size", "small"}
			for i := 0; i < len(args); i++ {
				if args[i] == tc.flag {
					args[i+1] = tc.value
				}
			}
			err := runAuthed(t, rt, out, url, args...)

			if tc.wantExit == 0 {
				if err != nil {
					t.Fatalf("%s %q was refused: %v",
						tc.flag, tc.value, err)
				}
				if !wrote {
					t.Errorf("%s %q never reached the create, so "+
						"this control proves nothing",
						tc.flag, tc.value)
				}
				// The other half of the uncheckable-catalog notice.
				// Without this, a spurious "not checked" warning on the
				// SUCCESS path -- a check that ran, reported, and
				// warned anyway -- passes every test in the repo. The
				// ledger half is pinned; this pins the stderr half.
				if strings.Contains(errb.String(), "not checked") {
					t.Errorf("%s %q was checked, yet stderr warns it "+
						"was not: %q", tc.flag, tc.value,
						errb.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("%s %q was accepted", tc.flag, tc.value)
			}
			if got := cli.ExitCode(err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d (err: %v)",
					got, tc.wantExit, err)
			}
			// The message has to name what IS accepted, or the caller
			// is left running `size list` to find out.
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not carry %q",
					err.Error(), tc.wantIn)
			}
			if wrote {
				t.Errorf("%s %q reached the create anyway",
					tc.flag, tc.value)
			}
		})
	}
}

// resize takes the same --size and had the same gap. The check sits
// AFTER the confirmation prompt, so --force is part of every case:
// without it the destructive-verb refusal fires first and the case
// would pass on the wrong error.
func TestDatabaseResizeChecksTheCatalog(t *testing.T) {
	t.Run("an unknown size is refused", func(t *testing.T) {
		wrote := false
		url := catalogServer(t, &wrote)
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, "database", "resize",
			testDatabaseID, "--size", "enormous", "--force")
		if err == nil {
			t.Fatal("resize to an unpublished size was accepted")
		}
		if got := cli.ExitCode(err); got != ExitUsage {
			t.Errorf("exit = %d, want ExitUsage(%d) (err: %v)",
				got, ExitUsage, err)
		}
		if wrote {
			t.Error("the resize was sent anyway")
		}
	})

	// The control, and it is doing real work: the check runs after the
	// prompt, so a mistake in that ordering would show up here as a
	// resize that never happens.
	t.Run("a published size still resizes", func(t *testing.T) {
		wrote := false
		url := catalogServer(t, &wrote)
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, url, "database", "resize",
			testDatabaseID, "--size", "large", "--force"); err != nil {
			t.Fatalf("resize to a published size: %v", err)
		}
		if !wrote {
			t.Error("the resize never reached the API")
		}
	})

	// A blank --size is refused BEFORE the prompt, so it does not need
	// --force. That ordering is the reason the blank check is separate
	// from the catalog check at all.
	t.Run("a blank size needs no --force to be refused",
		func(t *testing.T) {
			wrote := false
			url := catalogServer(t, &wrote)
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, "database", "resize",
				testDatabaseID, "--size", "")
			if err == nil {
				t.Fatal("a blank --size was accepted")
			}
			if got := cli.ExitCode(err); got != ExitUsage {
				t.Errorf("exit = %d, want ExitUsage(%d)",
					got, ExitUsage)
			}
			if !strings.Contains(err.Error(), "empty value") {
				t.Errorf("error %q is the prompt refusal, not the "+
					"blank-value one -- the check has moved below "+
					"cli.Confirm", err.Error())
			}
		})
}

// The two fail-open branches, one case each. Both send the value
// rather than refusing it, which keeps the property the checks are
// built on -- they never refuse what the API would have accepted --
// and both were untested, which is how a branch that silently
// disables a check survives a review.
//
// The content types are the whole reason there are two cases. The
// generated parser sets `JSON200 = &dest` UNCONDITIONALLY on a 200
// carrying a JSON content type, so a `null` body yields a non-nil
// pointer to a nil slice and lands on `len(names) == 0` -- the same
// branch as `[]`, not the nil-JSON200 one it was named for. A 200
// whose content type is not JSON falls past every case in that switch
// and is what actually reaches `resp.JSON200 == nil`.
func TestAnUncheckableCatalogFailsOpen(t *testing.T) {
	for name, tc := range map[string]struct {
		contentType, body string
	}{
		"an empty list":       {"application/json", `[]`},
		"a non-JSON 200 body": {"text/plain", `not json`},
	} {
		t.Run(name, func(t *testing.T) {
			url := testsupport.NewAuthedServer(t, func(
				w http.ResponseWriter, r *http.Request,
			) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/sizes"),
					strings.HasSuffix(r.URL.Path, "/regions"):
					w.Header().Set("Content-Type", tc.contentType)
					_, _ = w.Write([]byte(tc.body))
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(
						databaseJSON(testDatabaseID, "")))
				}
			})
			rt, out, errb := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()

			// A value no real catalog would carry. It must still be
			// sent: with nothing to check against, refusing it would
			// ground the create on an upstream blip.
			_ = runAuthed(t, rt, out, url, "database", "create",
				"--name", "mydb", "--region", "nowhere-1",
				"--size", "enormous")

			// Request(), not the returned error: the dry-run transport
			// intercepts the write and that interception surfaces AS an
			// error, so a nil-error assertion here would fail on the
			// success path. Request() is non-nil only if the checks let
			// the create be built, which is the fail-open property
			// under test.
			req := rt.DryRun.Request()
			if req == nil {
				t.Fatal("an uncheckable catalog refused the create; " +
					"it should have sent the value and let the API " +
					"judge it")
			}
			// BOTH values: asserting only one lets a mutation that
			// drops Region from the body survive.
			for _, want := range []string{"enormous", "nowhere-1"} {
				if !strings.Contains(string(req.Body), want) {
					t.Errorf("the prepared body dropped %q: %s",
						want, req.Body)
				}
			}

			// And it must SAY it did not check, rather than recording a
			// pass that examined nothing. Only reachable under a dry
			// run; on a real run there is no signal, which the comment
			// in catalog.go states as the limit of the design.
			// The exact entries, not a count of the phrase. A count
			// passes when validateRegion's branch names the SIZE,
			// which is a one-word copy-paste away.
			checks := rt.DryRun.Checks()
			for _, want := range []string{
				`size "enormous" not checked`,
				`region "nowhere-1" not checked`,
			} {
				var found bool
				for _, c := range checks {
					if strings.Contains(c, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("ledger %q carries no entry matching %q",
						checks, want)
				}
			}

			// And it must not be ledger-only: on a real run rt.DryRun
			// is nil, so stderr is the operator's only signal that a
			// check did not happen.
			for _, want := range []string{"enormous", "nowhere-1"} {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("stderr does not warn about %q: %q",
						want, errb.String())
				}
			}
		})
	}
}

// A dry run has to SAY it checked, or the ledger goes on implying the
// verb has no catalog checks -- which is the reading that made a clean
// dry run mean more than it did.
func TestCatalogChecksAreRecorded(t *testing.T) {
	// rt.DryRun set directly, not --dry-run: this package's synthetic
	// starfleet root carries only the three connection flags, so the real
	// --dry-run (registered by internal/cli's root) does not exist
	// here. Every other ledger test in this suite does the same.
	wrote := false
	url := catalogServer(t, &wrote)
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()

	// The intercepted write surfaces as an error; the ledger is what is
	// under test.
	_ = runAuthed(t, rt, out, url, "database", "create",
		"--name", "mydb", "--region", "us-east-2", "--size", "xl")

	got := rt.DryRun.Checks()
	for _, want := range []string{
		`database name "mydb" accepted`,
		`region "us-east-2" is published`,
		`size "xl" is published`,
	} {
		if !slices.Contains(got, want) {
			t.Errorf("ledger %q does not carry %q", got, want)
		}
	}
}

// A check that REFUSES must record nothing. A report listing a catalog
// check as passed when it refused the command is the worst lie this
// feature can tell, and it is the contract the service-intent ledger
// test states for its own checks.
func TestARefusedCatalogCheckRecordsNothing(t *testing.T) {
	for _, tc := range []struct{ flag, value, word string }{
		{"--size", "enormous", "size"},
		{"--region", "mars-1", "region"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			wrote := false
			url := catalogServer(t, &wrote)
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()

			args := []string{"database", "create", "--name", "mydb",
				"--region", "us-east-1", "--size", "small"}
			for i := range args {
				if args[i] == tc.flag {
					args[i+1] = tc.value
				}
			}
			if err := runAuthed(t, rt, out, url, args...); err == nil {
				t.Fatalf("%s %q was accepted", tc.flag, tc.value)
			}
			for _, c := range rt.DryRun.Checks() {
				if strings.Contains(c, tc.value) {
					t.Errorf("a refused check recorded %q", c)
				}
			}
			// The ledger's own record of a write ATTEMPT. A stub
			// counter cannot serve here: the dry-run transport
			// intercepts every write, so it would measure exactly what
			// the dry run prevents.
			if req := rt.DryRun.Request(); req != nil {
				t.Errorf("a refused check still attempted %s %s",
					req.Method, req.URL)
			}
		})
	}
}
