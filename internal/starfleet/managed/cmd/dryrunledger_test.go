package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestGuardServiceIntentRecordsOnlyOnPass covers both halves of the
// ledger contract at the check that matters most (#117): a passing check
// says what it proved, and a failing one records nothing — a report
// listing a check as passed when it refused the command would be the
// worst possible lie for this feature to tell.
func TestGuardServiceIntentRecordsOnlyOnPass(t *testing.T) {
	mcp := api.Mcp
	deployed := &api.ManagedDatabase{
		Id: "7f3a",
		Services: &[]api.ServiceConfig{
			{ServiceType: mcp},
		},
	}
	empty := &api.ManagedDatabase{Id: "7f3a"}

	tests := []struct {
		name    string
		db      *api.ManagedDatabase
		intent  serviceIntent
		wantErr bool
		want    string
	}{
		{
			name:   "deploy with nothing deployed records the intent",
			db:     empty,
			intent: intentDeploy,
			want:   `no "mcp" service already deployed (deploy intent)`,
		},
		{
			name:   "update with a service deployed records the intent",
			db:     deployed,
			intent: intentUpdate,
			want:   `a "mcp" service exists to update (update intent)`,
		},
		{
			name:    "deploy over an existing service records nothing",
			db:      deployed,
			intent:  intentDeploy,
			wantErr: true,
		},
		{
			name:    "update with nothing deployed records nothing",
			db:      empty,
			intent:  intentUpdate,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			err := guardServiceIntent(rt, tc.db, api.Mcp, tc.intent,
				"pgedge starfleet managed database mcp")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				if got := rt.DryRun.Checks(); len(got) != 0 {
					t.Errorf("a failed check recorded %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := rt.DryRun.Checks()
			if len(got) != 1 {
				t.Fatalf("recorded %q, want one entry", got)
			}
			if got[0] != tc.want {
				t.Errorf("recorded %q, want %q", got[0], tc.want)
			}
		})
	}
}

// TestGuardServiceIntentToleratesNoDryRun is the normal path: every
// check site calls Pass unconditionally, so a nil Run must be inert.
func TestGuardServiceIntentToleratesNoDryRun(t *testing.T) {
	rt := &module.Runtime{} // DryRun nil
	if err := guardServiceIntent(rt, &api.ManagedDatabase{Id: "7f3a"},
		api.Mcp, intentDeploy,
		"pgedge starfleet managed database mcp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLedgerEntriesNameTheirSubject holds every entry this module can
// record to the wording rule: past tense, naming the concrete thing it
// proved, so a reader can tell which database or service was checked.
func TestLedgerEntriesNameTheirSubject(t *testing.T) {
	rt := &module.Runtime{DryRun: dryrun.New()}
	if err := guardServiceIntent(rt, &api.ManagedDatabase{Id: "7f3a"},
		api.Mcp, intentDeploy,
		"pgedge starfleet managed database mcp"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range rt.DryRun.Checks() {
		if strings.HasPrefix(entry, "check") ||
			strings.HasPrefix(entry, "validate") {
			t.Errorf("entry %q describes the action, not what it "+
				"proved", entry)
		}
		if !strings.Contains(entry, "mcp") {
			t.Errorf("entry %q names no concrete subject", entry)
		}
	}
}

// TestDatabaseResolvedNote pins the one ledger line that names a
// database, now that IDs are full UUIDs (#194).
//
// It used to take the argument as well and suppress a parenthetical
// when the two matched, because a name or an ID prefix resolved to a
// different string. Neither resolves any more, so the caller's
// spelling carries nothing the server's own id does not.
func TestDatabaseResolvedNote(t *testing.T) {
	const id = "7f3a5c1e-0000-4000-8000-000000000000"
	want := "database " + id + " resolved"
	if got := databaseResolvedNote(id); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestManagedRegionResolutionIsRecorded pins the ledger entry for the
// --region resolution (#195).
//
// The standing rule is that every client-side check is RECORDED, and
// the test above exists because a managed check once shipped recording
// an entry that nothing asserted. This is the same shape one release
// later, so it gets its test in the same commit rather than the next
// review.
//
// The regions read has to answer an ARRAY: the generated parser
// unmarshals into []ManagedRegion, and an object kills the verb before
// it records anything — which would make this test pass for the wrong
// reason if it asserted only "nothing recorded".
func TestManagedRegionResolutionIsRecorded(t *testing.T) {
	// /sizes is answered as well as /regions, and it has to be: --size
	// is now checked against the catalog, so a handler that returned
	// the shared object for it would fail the create before the ledger
	// reached the entries under test -- and every case here would pass
	// for the wrong reason.
	regionsHandler := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/regions") {
				_, _ = w.Write([]byte(body))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/sizes") {
				_, _ = w.Write([]byte(catalogSizesJSON))
				return
			}
			_, _ = w.Write([]byte(`{"id":"db-1"}`))
		}
	}

	t.Run("a resolved region is recorded", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := testsupport.NewAuthedServer(t,
			regionsHandler(`[{"region":"us-east-2"}]`))

		_ = runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "large")

		got := rt.DryRun.Checks()
		if len(got) != 3 {
			t.Fatalf("ledger = %q, want the name, the region and "+
				"the size", got)
		}
		if !strings.Contains(got[1], "us-east-2") {
			t.Errorf("the region entry does not name it: %q", got[1])
		}
	})

	// An explicit flag skips the RESOLUTION, and must not record it —
	// otherwise the entry would claim a check that never ran.
	t.Run("an explicit region is checked, not resolved",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			url := testsupport.NewAuthedServer(t,
				regionsHandler(`[{"region":"us-east-2"}]`))

			_ = runAuthed(t, rt, out, url, "database", "create",
				"--name", "mydb", "--size", "large",
				"--region", "us-east-2")

			// Three entries, none of them the resolution: an explicit
			// region is CHECKED against the list (#242) but not resolved
			// from it, and the two read differently. "is the only one
			// published" is a claim about the catalog having one entry;
			// "is published" is a claim about this value being in it.
			got := rt.DryRun.Checks()
			if len(got) != 3 {
				t.Fatalf("ledger = %q, want the name, the region check "+
					"and the size", got)
			}
			for _, c := range got {
				if strings.Contains(c, "the only one published") {
					t.Errorf("an explicit region was resolved: %q", c)
				}
			}
		})

	t.Run("a refusal records nothing and sends nothing", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := testsupport.NewAuthedServer(t, regionsHandler(
			`[{"region":"us-east-2"},{"region":"us-west-1"}]`))

		err := runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--size", "large")
		if err == nil {
			t.Fatal("two regions resolved instead of refusing")
		}
		// The name check still records; the region one must not, and
		// no write may have been prepared.
		for _, c := range rt.DryRun.Checks() {
			if strings.Contains(c, "region") {
				t.Errorf("a refused resolution recorded %q", c)
			}
		}
		if rt.DryRun.Request() != nil {
			t.Error("a refusal still prepared a write")
		}
	})
}

// TestManagedDatabaseCreateNameCheckIsRecorded is the managed twin of
// the byoc test of the same shape.
//
// It is here because #170's review pointed out that the byoc fix had
// restored parity with an UNTESTED sibling: the managed --name check
// has recorded its ledger entry since #115, and nothing asserted it.
// Two implementations over different generated types are deliberately
// duplicated in this repo, so a fix to one does not fix the other —
// which cuts both ways for their tests.
func TestManagedDatabaseCreateNameCheckIsRecorded(t *testing.T) {
	t.Run("an accepted name records its entry first",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			// withCatalog, so the --region and --size checks reach
			// their own entries. Without it the catalog read fails and
			// this case passes with a one-entry ledger for a reason
			// that has nothing to do with the name check.
			url := testsupport.NewAuthedServer(t, withCatalog(
				testsupport.JSONHandler(
					http.StatusOK, `{"id":"db-1"}`)))
			// The intercepted write surfaces as an error; the ledger
			// is what is under test.
			_ = runAuthed(t, rt, out, url, "database", "create",
				"--name", "mydb", "--region", "us-east-1",
				"--size", "large")
			got := rt.DryRun.Checks()
			// FIRST, not only: the name is checked before the client
			// is built and the catalog checks cannot be, so their
			// entries follow. Asserting position rather than count
			// keeps this test about the name.
			if len(got) == 0 ||
				got[0] != `database name "mydb" accepted` {
				t.Errorf("ledger = %q, want the accepted name first",
					got)
			}
		})

	t.Run("a refused name records nothing and sends nothing",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, `{"id":"db-1"}`))
			// Underscores are legal for byoc and refused here — the
			// divergence internal/clitest pins from above.
			err := runAuthed(t, rt, out, url, "database", "create",
				"--name", "my_db", "--region", "us-east-1",
				"--size", "large")
			if err == nil {
				t.Fatal("want an error for an underscored managed name")
			}
			if got := rt.DryRun.Checks(); len(got) != 0 {
				t.Errorf("a refused name recorded %q", got)
			}
			// rt.DryRun.Request(), not a flag set by the stub
			// handler: the dry-run transport intercepts every write,
			// so a server-side counter can never fire here whatever
			// production does — it would measure exactly what the
			// dry run prevents. Request() is the ledger's own record
			// of a write ATTEMPT, so it stays nil only if validation
			// really did return first.
			if req := rt.DryRun.Request(); req != nil {
				t.Errorf("a refused name still attempted %s %s",
					req.Method, req.URL)
			}
		})
}
