package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/dryrun"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

const testClusterID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

// clusterBody is a minimal valid cluster JSON object.
const clusterBody = `{"id":"` + testClusterID + `","name":"prod",` +
	`"status":"available","regions":["us-east-1"],` +
	`"created_at":"2024-03-15T10:30:00Z"}`

func TestClusterListRun(t *testing.T) {
	body := `[{"id":"` + testClusterID + `","name":"prod",` +
		`"status":"available","regions":["us-east-1"],` +
		`"created_at":"2024-03-15T10:30:00Z"}]`

	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "cluster", "list"); err != nil {
			t.Fatalf("cluster list: %v", err)
		}
		if !strings.Contains(out.String(), "prod") {
			t.Errorf("output missing cluster name: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, body))
		if err := runAuthed(t, rt, out, url, "cluster", "list"); err != nil {
			t.Fatalf("cluster list json: %v", err)
		}
		if !strings.Contains(out.String(), testClusterID) {
			t.Errorf("json missing id: %q", out.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `[]`))
		if err := runAuthed(t, rt, out, url, "cluster", "list"); err != nil {
			t.Fatalf("cluster list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No clusters found") {
			t.Errorf("want 'No clusters found', got %q", errb.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		err := runAuthed(t, rt, out, url, "cluster", "list")
		if err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestClusterGetRun(t *testing.T) {
	t.Run("text success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url,
			"cluster", "get", testClusterID); err != nil {
			t.Fatalf("cluster get: %v", err)
		}
		if !strings.Contains(out.String(), "prod") {
			t.Errorf("missing name: %q", out.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url,
			"cluster", "get", testClusterID); err != nil {
			t.Fatalf("cluster get json: %v", err)
		}
		if !strings.Contains(out.String(), testClusterID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "get", testClusterID); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestClusterCreateRun(t *testing.T) {
	args := []string{"cluster", "create", "--name", "prod",
		"--cloud-account-id", testClusterID, "--regions", "us-east-1",
		"--node-location", "public"}

	t.Run("text success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("cluster create: %v", err)
		}
		if !strings.Contains(errb.String(), "created") {
			t.Errorf("missing created message: %q", errb.String())
		}
	})

	t.Run("json success", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "json")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url, args...); err != nil {
			t.Fatalf("cluster create json: %v", err)
		}
		if !strings.Contains(out.String(), testClusterID) {
			t.Errorf("missing id: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, args...); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

// TestClusterCreatePrivateRejectsMissingSubnets pins that a private
// cluster whose --network carries no private-subnets is rejected with
// exit 2 before the request reaches the server.
func TestClusterCreatePrivateRejectsMissingSubnets(t *testing.T) {
	called := false
	url := testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	err := runAuthed(t, rt, out, url, "cluster", "create",
		"--name", "prod", "--cloud-account-id", testClusterID,
		"--regions", "us-east-1", "--node-location", "private",
		"--network",
		"region=us-east-1,cidr=10.4.0.0/16,public-subnets=10.4.1.0/24")
	if err == nil {
		t.Fatal("expected error for private cluster without private-subnets")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code() != ExitUsage {
		t.Errorf("want exit %d, got %v", ExitUsage, err)
	}
	if called {
		t.Error("missing private-subnets should not reach the server")
	}
}

// TestStructuredFlagTypoIsRejectedBeforeAnyRequest is the end-to-end
// half of TestStructuredFlagRejectionsAreUsageErrors, and exists
// because that test could not have caught the bug this one does.
//
// The unit test calls the three parsers DIRECTLY, so it is structurally
// blind to a caller that catches the parser's *ExitError and re-wraps
// it. `cluster update` did exactly that — it flattened the code back to
// ExitGeneral — so the same malformed --firewall-rule value exited 2 on
// create and 1 on update while every unit test passed.
//
// It also pins the second half of the promise, which the unit test
// cannot see at all: nothing is SENT. `cluster update` used to parse
// its rules after clientFromCmd and resolveClusterID, so a typo still
// cost a token exchange and, for a cluster named rather than given by
// UUID, a GET /clusters as well.
//
// Every verb that parses a structured flag is covered. Adding one
// without a case here leaves that verb free to re-acquire either half
// of the bug.
func TestStructuredFlagTypoIsRejectedBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"create --firewall-rule", []string{
			"cluster", "create", "--name", "prod",
			"--cloud-account-id", testClusterID,
			"--regions", "us-east-1", "--node-location", "public",
			"--firewall-rule", "name=gopher,port=70"}},
		{"create --network", []string{
			"cluster", "create", "--name", "prod",
			"--cloud-account-id", testClusterID,
			"--regions", "us-east-1", "--node-location", "public",
			"--network", "region=us-east-1,nope=1"}},
		{"create --node", []string{
			"cluster", "create", "--name", "prod",
			"--cloud-account-id", testClusterID,
			"--regions", "us-east-1", "--node-location", "public",
			"--node", "name=n1,volume-size=big"}},
		{"create --node with the shorthand", []string{
			"cluster", "create", "--name", "prod",
			"--cloud-account-id", testClusterID,
			"--regions", "us-east-1", "--node-location", "public",
			"--node", "name=n1", "--instance-type", "r7g.medium"}},
		// The regression case. Both halves failed here: exit 1, and a
		// token exchange plus a cluster lookup already sent.
		{"update --firewall-rule", []string{
			"cluster", "update", testClusterID,
			"--firewall-rule", "name=gopher,port=70"}},
		{"update --firewall-rule, cluster named not UUID", []string{
			"cluster", "update", "prod-west",
			"--firewall-rule", "name=gopher,port=70"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately NOT testsupport.NewAuthedServer: that helper
			// answers the token path itself, before the caller's handler
			// runs, so a token exchange is invisible to it. The token
			// exchange is the request this test most needs to see —
			// `cluster update` used to make one before it parsed. This
			// server records every path, auth included, and still serves
			// a usable token so that reaching auth is a test FAILURE
			// rather than a different error masking the real one.
			var mu sync.Mutex
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					paths = append(paths, r.URL.Path)
					mu.Unlock()
					if r.URL.Path == testsupport.TokenPath {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, testsupport.TokenBody)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
				}))
			defer srv.Close()

			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, srv.URL, tt.args...)
			if err == nil {
				t.Fatal("expected an error for a malformed flag value")
			}
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want *ExitError, got %T: %v", err, err)
			}
			if ee.Code() != ExitUsage {
				t.Errorf("exit code = %d, want ExitUsage (%d): %v",
					ee.Code(), ExitUsage, err)
			}
			mu.Lock()
			sent := append([]string(nil), paths...)
			mu.Unlock()
			if len(sent) != 0 {
				t.Errorf("a malformed flag value caused %d request(s) "+
					"%v; it must be rejected before anything is sent, "+
					"including the token exchange", len(sent), sent)
			}
		})
	}
}

// TestClusterCreatePrivateAcceptsSubnets is the positive control for
// TestClusterCreatePrivateRejectsMissingSubnets: the same private
// cluster WITH private-subnets passes validation and reaches the
// server.
func TestClusterCreatePrivateAcceptsSubnets(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
	err := runAuthed(t, rt, out, url, "cluster", "create",
		"--name", "prod", "--cloud-account-id", testClusterID,
		"--regions", "us-east-1", "--node-location", "private",
		"--network",
		"region=us-east-1,cidr=10.4.0.0/16,public-subnets=10.4.1.0/24,"+
			"private-subnets=10.4.128.0/24")
	if err != nil {
		t.Fatalf("private cluster with private-subnets: %v", err)
	}
}

// TestClusterCreatePrivateWithoutNetworkPassesThrough is the GCP
// regression case for the private-subnets check: a private cluster with NO --network
// flag at all is a valid GCP shape (the server synthesizes host
// groups per region and GCP's own validator never requires
// private-subnets — it forbids the key outright). The client-side
// check must not block this: it should reach the server rather than
// exit 2 locally.
func TestClusterCreatePrivateWithoutNetworkPassesThrough(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
	err := runAuthed(t, rt, out, url, "cluster", "create",
		"--name", "prod", "--cloud-account-id", testClusterID,
		"--regions", "us-east-1", "--node-location", "private")
	if err != nil {
		t.Fatalf("private cluster without --network: %v", err)
	}
}

func TestClusterDeleteRun(t *testing.T) {
	t.Run("force success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "delete", testClusterID, "--force"); err != nil {
			t.Fatalf("cluster delete: %v", err)
		}
		if !strings.Contains(errb.String(), "deleted") {
			t.Errorf("missing deleted message: %q", errb.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, `{}`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "delete", testClusterID); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(404, `nope`))
		if err := runAuthed(t, rt, out, url,
			"cluster", "delete", testClusterID, "--force"); err == nil {
			t.Fatal("expected error on 404")
		}
	})
}

func TestClusterUpdateRun(t *testing.T) {
	t.Run("firewall rule success", func(t *testing.T) {
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, func(w http.ResponseWriter, r *http.Request) {
			// GET returns the cluster; PUT/PATCH just needs a 2xx.
			testsupport.JSONHandler(200, clusterBody)(w, r)
		})
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--firewall-rule",
			"name=https,port=443,sources=0.0.0.0/0"); err != nil {
			t.Fatalf("cluster update: %v", err)
		}
		if !strings.Contains(errb.String(), "updated") {
			t.Errorf("missing updated message: %q", errb.String())
		}
	})

	t.Run("nothing to update", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(200, clusterBody))
		if err := runAuthed(t, rt, out, url,
			"cluster", "update", testClusterID); err == nil {
			t.Fatal("expected error when no changes given")
		}
	})

	t.Run("get error", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t, testsupport.JSONHandler(500, `boom`))
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "eu-west-1"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

// TestClusterUpdateRecordsItsChecks is the update half of
// TestClusterFlagChecksAreRecorded. It goes through the command rather
// than a builder because update's two checks live in its RunE, and
// because the ordering matters as much as the content: both must be
// recorded BEFORE clientFromCmd, so a --dry-run that never reaches the
// server still reports them.
func TestClusterUpdateRecordsItsChecks(t *testing.T) {
	const rule = "name=https,port=443,sources=0.0.0.0/0"

	tests := []struct {
		name  string
		args  []string
		wants []string
		// absent is a substring that must NOT appear.
		absent string
		// unreachable points the command at a closed port, so nothing
		// recorded after clientFromCmd can survive.
		unreachable bool
	}{
		{
			name: "rules given records both checks",
			args: []string{"cluster", "update", testClusterID,
				"--firewall-rule", rule},
			wants: []string{
				"at least one of --firewall-rule, --backup-store-id, " +
					"--regions given",
				"1 firewall rule(s) valid",
			},
		},
		{
			// With no rules, the ledger must
			// still be non-empty, or the report claims the verb has no
			// client-side checks while one just passed.
			name: "no rules still records the at-least-one check",
			args: []string{"cluster", "update", testClusterID,
				"--backup-store-id", "bs-2"},
			wants: []string{
				"at least one of --firewall-rule, --backup-store-id, " +
					"--regions given",
			},
			absent: "firewall rule(s) valid",
		},
		{
			// The ordering gate. Both Pass calls sit before
			// clientFromCmd, so an unreachable API must not cost the
			// ledger anything. Move either one below clientFromCmd and
			// this case loses its entry while every other case here
			// still passes.
			name: "checks survive an unreachable API",
			args: []string{"cluster", "update", testClusterID,
				"--firewall-rule", rule},
			wants: []string{
				"at least one of --firewall-rule, --backup-store-id, " +
					"--regions given",
				"1 firewall rule(s) valid",
			},
			unreachable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			rt.DryRun = dryrun.New()
			// A closed port is the shape used here to make the command
			// die inside clientFromCmd. Any failure at or before that
			// point would do; what matters is that a WORKING server
			// cannot distinguish "recorded before the client was built"
			// from "recorded after", because both orderings leave the
			// same ledger.
			url := deadAddr(t)
			if !tt.unreachable {
				url = testsupport.NewAuthedServer(t,
					testsupport.JSONHandler(http.StatusOK, clusterBody))
			}
			// Errors are not the subject: the stub does not model a full
			// update round trip. What matters is what the ledger holds.
			_ = runAuthed(t, rt, out, url, tt.args...)

			got := rt.DryRun.Checks()
			for _, want := range tt.wants {
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
			if tt.absent != "" {
				for _, g := range got {
					if strings.Contains(g, tt.absent) {
						t.Errorf("ledger %q should not contain %q",
							got, tt.absent)
					}
				}
			}
		})
	}
}

// TestClusterCreateRecordsItsChecksThroughTheCommand is the create-side
// ordering gate, and exists because TestClusterFlagChecksAreRecorded
// cannot be one: it calls buildClusterCreateBody directly, so it is
// blind to a create RunE that passes the builder a different Runtime
// and discards the ledger — the same structural blindness that let
// `cluster update` re-wrap its exit code past a unit test.
//
// The unreachable address is what makes it an ORDERING gate rather
// than a repeat: the builder runs before clientFromCmd, so a command
// that never reaches the server must still have recorded both checks.
func TestClusterCreateRecordsItsChecksThroughTheCommand(t *testing.T) {
	rt, out, _ := testsupport.NewRuntime(t, "", "text")
	rt.DryRun = dryrun.New()
	_ = runAuthed(t, rt, out, deadAddr(t), "cluster", "create",
		"--name", "prod", "--cloud-account-id", testClusterID,
		"--regions", "us-east-1", "--node-location", "public",
		"--volume-size", "30",
		"--firewall-rule", "name=https,port=443,sources=0.0.0.0/0")

	got := rt.DryRun.Checks()
	for _, want := range []string{
		"1 firewall rule(s) valid",
		"private-subnet configuration valid for public nodes",
		// The volume-size check is recorded from RunE's own
		// Flags().Changed reading, so a create that built its body from
		// a different Runtime — or never set volumeSizeSet at all —
		// would lose this line while the other two survived.
		"volume size 30 GB is 1 or more",
	} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ledger %q is missing %q; create's checks must be "+
				"recorded on the Runtime the command was given, before "+
				"anything is dialled", got, want)
		}
	}
}

// deadAddr returns an address that refuses connections, so a command
// pointed at it fails at the first request it makes.
func deadAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()
	return addr
}

// TestClusterCreateVolumeSizeBounds pins the whole --volume-size
// boundary — the accepted values as well as the refused ones — so a
// later change cannot tighten or loosen the rule without a test saying
// so.
//
// A `volumeSize > 0` guard OMITTED any non-positive size from the
// request instead of refusing it. The API then applied its own
// undocumented default of 100 GB and answered 200, so
// `--volume-size -5` provisioned a 100 GB volume at exit 0 and the
// operator was never told the size they asked for had been discarded.
//
// Both halves are asserted, because the exit code alone cannot tell
// "refused" from "silently sent as omitted": the refused cases must
// reach no request at all, and the accepted cases are checked against
// what actually went out on the wire. The last case is the one that
// keeps "omitted" and "explicitly zero" distinguishable — omitting the
// flag must still send no volume_size and leave the choice to the API.
func TestClusterCreateVolumeSizeBounds(t *testing.T) {
	common := []string{
		"cluster", "create", "--name", "prod",
		"--cloud-account-id", testClusterID,
		"--regions", "us-east-1", "--node-location", "public",
	}
	tests := []struct {
		name          string
		flag          []string
		wantUsage     bool
		wantInBody    string
		wantNotInBody string
	}{
		{
			name:      "a negative size is a usage error",
			flag:      []string{"--volume-size", "-5"},
			wantUsage: true,
		},
		{
			name:      "an explicit zero is a usage error",
			flag:      []string{"--volume-size", "0"},
			wantUsage: true,
		},
		{
			// The literal 1 is deliberate and must not be replaced by
			// volumeSizeMin: a case written against the constant moves
			// with it, so raising the floor would pass. Verified — with
			// volumeSizeMin bumped to 2 this case fails and the unit
			// test's symbolic twin does not.
			name:       "the smallest accepted size is sent",
			flag:       []string{"--volume-size", "1"},
			wantInBody: `"volume_size":1`,
		},
		{
			name:       "an ordinary size is sent unchanged",
			flag:       []string{"--volume-size", "30"},
			wantInBody: `"volume_size":30`,
		},
		{
			name:          "omitting the flag sends no volume_size",
			wantNotInBody: "volume_size",
		},
		// The structured spelling, end to end. It carries the same floor
		// because it is the same field, and because every worked create
		// example in llms.txt uses it — an agent following the reference
		// writes this form, not the shorthand.
		{
			name:      "a non-positive --node volume-size is a usage error",
			flag:      []string{"--node", "name=n1,volume-size=-5"},
			wantUsage: true,
		},
		{
			// Hardcoded 1 for the same reason as the shorthand's case
			// above: a case written against volumeSizeMin would survive
			// a raised floor.
			name:       "the smallest --node volume-size is sent",
			flag:       []string{"--node", "name=n1,volume-size=1"},
			wantInBody: `"volume_size":1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Records every path, the token exchange included, so that
			// "nothing was sent" means nothing at all rather than
			// "nothing after auth".
			var mu sync.Mutex
			var paths, bodies []string
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					mu.Lock()
					paths = append(paths, r.URL.Path)
					bodies = append(bodies, string(b))
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == testsupport.TokenPath {
						_, _ = io.WriteString(w, testsupport.TokenBody)
						return
					}
					_, _ = io.WriteString(w, clusterBody)
				}))
			defer srv.Close()

			args := append(append([]string{}, common...), tt.flag...)
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, srv.URL, args...)

			mu.Lock()
			gotPaths := append([]string(nil), paths...)
			gotBodies := append([]string(nil), bodies...)
			mu.Unlock()

			if tt.wantUsage {
				var ee *ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("want *ExitError, got %T: %v", err, err)
				}
				if ee.Code() != ExitUsage {
					t.Errorf("exit code = %d, want ExitUsage (%d): %v",
						ee.Code(), ExitUsage, err)
				}
				if len(gotPaths) != 0 {
					t.Errorf("a non-positive --volume-size caused %d "+
						"request(s) %v; it must be refused before "+
						"anything is sent, the token exchange included",
						len(gotPaths), gotPaths)
				}
				return
			}

			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if len(gotBodies) == 0 {
				t.Fatal("no request reached the server")
			}
			// The create is the last request; the token exchange is the
			// first.
			body := gotBodies[len(gotBodies)-1]
			if tt.wantInBody != "" &&
				!strings.Contains(body, tt.wantInBody) {
				t.Errorf("request body = %s, want it to contain %s",
					body, tt.wantInBody)
			}
			if tt.wantNotInBody != "" &&
				strings.Contains(body, tt.wantNotInBody) {
				t.Errorf("request body = %s, want no %q in it at all",
					body, tt.wantNotInBody)
			}
		})
	}
}
