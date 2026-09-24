package cmd

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestGuardServiceIntentRecordsOnlyOnPass is byoc's half of the ledger
// contract. The managed twin is tested separately: they are deliberately
// duplicated implementations over different generated types, so a fix to
// one does not fix the other.
func TestGuardServiceIntentRecordsOnlyOnPass(t *testing.T) {
	mcp := api.ServiceServiceTypeMcp
	deployed := &api.Database{
		Id:       "7f3a",
		Services: &[]api.Service{{ServiceType: mcp}},
	}
	empty := &api.Database{Id: "7f3a"}

	tests := []struct {
		name    string
		db      *api.Database
		intent  serviceIntent
		wantErr bool
		want    string
	}{
		{
			name:   "deploy with nothing deployed",
			db:     empty,
			intent: intentDeploy,
			want:   `no "mcp" service already deployed (deploy intent)`,
		},
		{
			name:   "update with a service deployed",
			db:     deployed,
			intent: intentUpdate,
			want:   `a "mcp" service exists to update (update intent)`,
		},
		{
			// The guard case: a refused deploy must record nothing, or the
			// report would list the privilege-escalation guard as passed
			// on the run where it fired.
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
			err := guardServiceIntent(rt, tc.db, mcp, tc.intent,
				"pgedge starfleet byoc database mcp")
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

// TestGuardServiceIntentToleratesNoDryRun is the normal path: check sites
// call Pass unconditionally, so a nil Run must be inert.
func TestGuardServiceIntentToleratesNoDryRun(t *testing.T) {
	rt := &module.Runtime{}
	if err := guardServiceIntent(rt, &api.Database{Id: "7f3a"},
		api.ServiceServiceTypeMcp, intentDeploy,
		"pgedge starfleet byoc database mcp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDatabaseCreateNameCheckIsRecorded pins the ledger entry for the
// --name check. Without it the check runs, nothing records it, and
// --dry-run reports "none — this command has no client-side checks"
// for a verb that has one; deleting the Pass must fail a test.
//
// The negative case matters as much as the positive: a refused name
// must record nothing, or the report lists a check as passed on the
// run where it fired.
func TestDatabaseCreateNameCheckIsRecorded(t *testing.T) {
	// Driven through the COMMAND, not by calling Pass here: a subtest
	// that calls rt.DryRun.Pass itself tests nothing, since it passes
	// with the production call deleted.
	//
	// The ledger is installed by assigning rt.DryRun directly. These
	// harnesses build a bare cobra root with no PersistentPreRunE, so
	// a --dry-run ARGUMENT would never be read; the flag-to-Runtime
	// wiring is covered in internal/cli. What is under test here is
	// what the command records, given a ledger.
	t.Run("an accepted name records one entry", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"id":"db-1"}`))
		// The error is expected and not the subject: an intercepted
		// write surfaces as "dry run: POST ... was not sent", which is
		// the transport doing its job. internal/clitest's dry-run sweep
		// ignores it for the same reason. What is under test is the
		// ledger.
		_ = runAuthed(t, rt, out, url, "database", "create",
			"--name", "mydb", "--cluster-id", testClusterID)
		// TWO entries now: the name check and the --cluster-id parse
		// this verb carries. The count is asserted, not just
		// the membership, because the ledger's job is to be complete —
		// a check that runs and records nothing lets the report print
		// "none — this command has no client-side checks", which is what
		// made `ingress create --dry-run` false until review caught it.
		got := rt.DryRun.Checks()
		want := []string{
			`database name "mydb" accepted`,
			"cluster ID " + testClusterID + " well-formed",
		}
		if len(got) != len(want) {
			t.Fatalf("ledger = %q, want %d entries: %q",
				got, len(want), want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ledger[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	// Through the command, so the ORDER is covered too: a rejected name
	// must return before anything records or sends.
	t.Run("a refused name records nothing and sends nothing",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			url := testsupport.NewAuthedServer(t,
				testsupport.JSONHandler(http.StatusOK, `{"id":"db-1"}`))
			err := runAuthed(t, rt, out, url, "database", "create",
				"--name", "my-db", "--cluster-id", testClusterID)
			if err == nil {
				t.Fatal("want an error for a hyphenated name")
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

// TestClusterFlagChecksAreRecorded pins the ledger entries for the
// client-side checks on cluster create and update.
//
// It exists because deleting the whole rt.DryRun.Pass block once left
// every package green, so nothing asserted the string, the count, or
// the presence guard. Coverage did not help — the statements executed,
// nothing checked their effect.
//
// The failure this guards is a dropped Pass. For create that shows up
// as a missing line rather than the "none — this command has no
// client-side checks" sentence: create RECORDS its subnet check,
// so the report only goes empty if BOTH its entries go.
// update is the verb that printed "none" outright.
//
// It does NOT guard the ORDERING — it calls the builder directly, so
// it cannot see a caller that discards the ledger.
// TestClusterCreateRecordsItsChecksThroughTheCommand covers that.
func TestClusterFlagChecksAreRecorded(t *testing.T) {
	const rule = "name=https,port=443,sources=0.0.0.0/0"

	t.Run("create records the firewall parse and the subnet check",
		func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			_, err := buildClusterCreateBody(rt, &clusterCreateOpts{
				name:           "prod",
				cloudAccountID: testClusterID,
				regions:        []string{"us-east-1"},
				nodeLocation:   "public",
				firewallRules:  []string{rule, rule},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertLedgerHas(t, rt.DryRun.Checks(),
				"2 firewall rule(s) valid",
				"private-subnet configuration valid for public nodes")
		})

	t.Run("create with no rules records no firewall line",
		func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			if _, err := buildClusterCreateBody(rt, &clusterCreateOpts{
				name:           "prod",
				cloudAccountID: testClusterID,
				regions:        []string{"us-east-1"},
				nodeLocation:   "public",
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, c := range rt.DryRun.Checks() {
				if strings.Contains(c, "firewall rule(s) valid") {
					t.Errorf("recorded %q with no --firewall-rule given", c)
				}
			}
		})

	// A REJECTED value must record nothing: the report would otherwise
	// list a check as passed on the run where it fired.
	t.Run("create records nothing for a malformed rule",
		func(t *testing.T) {
			rt := &module.Runtime{DryRun: dryrun.New()}
			if _, err := buildClusterCreateBody(rt, &clusterCreateOpts{
				name:           "prod",
				cloudAccountID: testClusterID,
				regions:        []string{"us-east-1"},
				nodeLocation:   "public",
				firewallRules:  []string{"name=gopher,port=70"},
			}); err == nil {
				t.Fatal("want an error for an invalid rule name")
			}
			for _, c := range rt.DryRun.Checks() {
				if strings.Contains(c, "firewall rule(s) valid") {
					t.Errorf("a refused rule recorded %q", c)
				}
			}
		})
}

// assertLedgerHas fails unless every want appears in got.
func assertLedgerHas(t *testing.T, got []string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ledger %q is missing %q", got, want)
		}
	}
}

// TestClusterIDChecksAreRecorded pins the cluster ID ledger lines:
// without it, deleting all three rt.DryRun.Pass calls leaves build,
// vet and the whole suite green.
//
// The counts are asserted, not just membership, because the ledger's
// job is completeness. node_location and the private-subnet check
// record unconditionally, so without the ID lines the report is
// INCOMPLETE rather than false, which is why the want lists are exact.
func TestClusterIDChecksAreRecorded(t *testing.T) {
	t.Run("create records every check it runs", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, `{"id":"c-1"}`))
		_ = runAuthed(t, rt, out, url, "cluster", "create",
			"--name", "c1", "--cloud-account-id", testClusterID,
			"--regions", "us-east-1", "--node-location", "public",
			"--backup-store-id", testClusterID)
		// Five, and the fifth is the point of asserting a count
		// rather than membership: checkCloudAccountExists records a
		// network-backed check after the client is built, which a
		// membership assertion would never have surfaced.
		want := []string{
			`node location "public" accepted`,
			"cloud account ID is a UUID",
			"1 backup store ID(s) are UUIDs",
			"private-subnet configuration valid for public nodes",
			"cloud account " + testClusterID + " exists",
		}
		got := rt.DryRun.Checks()
		if len(got) != len(want) {
			t.Fatalf("ledger has %d entries, want %d:\n%v",
				len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("update records both of its ID checks", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		rt.DryRun = dryrun.New()
		url := testsupport.NewAuthedServer(t,
			testsupport.JSONHandler(http.StatusOK, clusterBody))
		_ = runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--backup-store-id", testClusterID)
		got := rt.DryRun.Checks()
		// The cluster ID parse is recorded too: it runs before the
		// client is built.
		want := []string{
			"at least one of --firewall-rule, --backup-store-id, " +
				"--regions given",
			"1 backup store ID(s) are UUIDs",
			"cluster ID is a UUID",
		}
		if len(got) != len(want) {
			t.Fatalf("ledger has %d entries, want %d:\n%v",
				len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// TestAnIDIsSentCanonically closes a hole in the ID checks:
// four sites parsed an ID, discarded the result and
// forwarded the raw flag value. uuid.Parse accepts `{uuid}` and
// `urn:uuid:uuid`, so both passed the check and reached the wire
// verbatim -- the same class of defect as forwarding a prefix, in a
// different spelling.
//
// Asserted on the REQUEST BODY the stub receives, because that is the
// only place the difference is observable: every one of these verbs
// exits 0 either way.
func TestAnIDIsSentCanonically(t *testing.T) {
	const canonical = testClusterID
	for _, spelling := range []string{
		"{" + canonical + "}",
		"urn:uuid:" + canonical,
		strings.ToUpper(canonical),
	} {
		t.Run(spelling, func(t *testing.T) {
			var bodies []string
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if b, err := io.ReadAll(r.Body); err == nil &&
						len(b) > 0 {
						bodies = append(bodies, string(b))
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(clusterBody))
				})
			_ = runAuthed(t, rt, out, url, "cluster", "create",
				"--name", "c1", "--cloud-account-id", spelling,
				"--regions", "us-east-1", "--node-location", "public",
				"--backup-store-id", spelling)
			if len(bodies) == 0 {
				t.Fatal("no request body reached the stub, so this " +
					"assertion is vacuous")
			}
			all := strings.Join(bodies, "\n")
			if strings.Contains(all, spelling) &&
				spelling != canonical {
				t.Errorf("the request body carries %q verbatim:\n%s",
					spelling, all)
			}
			if !strings.Contains(all, canonical) {
				t.Errorf("the request body does not carry the "+
					"canonical spelling %q:\n%s", canonical, all)
			}
		})
	}
}
