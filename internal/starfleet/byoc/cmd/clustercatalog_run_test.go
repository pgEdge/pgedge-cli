package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// The checks #256 asked for on `cluster create`, and the one it asked
// for that cannot be built.
//
// cluster create is the most expensive verb in the CLI: it provisions
// real cloud infrastructure and takes minutes. A dry run that passed
// every one of these values gave the strongest possible false
// confidence at exactly the wrong moment.

// clusterCreateArgs is the valid vector every case below mutates one
// flag of. Kept whole rather than spelled per case so a positive
// control is a copy with one substitution, and so a new required flag
// breaks every case loudly instead of turning them into no-ops.
func clusterCreateArgs(replace map[string]string) []string {
	pairs := [][2]string{
		{"--name", "prod"},
		{"--cloud-account-id", testClusterID},
		{"--regions", "us-east-1"},
		{"--node-location", "public"},
	}
	out := make([]string, 0, 2+len(pairs)*2)
	out = append(out, "cluster", "create")
	for _, p := range pairs {
		v := p[1]
		if sub, ok := replace[p[0]]; ok {
			v = sub
		}
		out = append(out, p[0], v)
	}
	return out
}

func TestClusterCreateChecksNodeLocation(t *testing.T) {
	cases := map[string]struct {
		value    string
		wantExit int
	}{
		// The value #256 measured sailing through a dry run and
		// reaching the API.
		"outside the enum": {"sideways", ExitUsage},
		// How an unset shell variable arrives. MarkFlagRequired is
		// satisfied by the flag being present, so this reaches the
		// body.
		"empty":           {"", ExitUsage},
		"whitespace only": {"   ", ExitUsage},
		// Both halves of the enum, as positive controls. Without them
		// a check that refused everything would look like a fix.
		"public":  {"public", 0},
		"private": {"private", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reached := false
			url := testsupport.NewAuthedServer(t, func(
				w http.ResponseWriter, r *http.Request,
			) {
				if r.Method != http.MethodGet {
					reached = true
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(clusterBody))
			})
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, url, clusterCreateArgs(
				map[string]string{"--node-location": tc.value})...)

			if tc.wantExit == 0 {
				if err != nil {
					t.Fatalf("%q was refused: %v", tc.value, err)
				}
				if !reached {
					t.Errorf("%q never reached the write, so this "+
						"control proves nothing", tc.value)
				}
				return
			}
			if err == nil {
				t.Fatalf("node location %q was accepted", tc.value)
			}
			if got := cli.ExitCode(err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d (err: %v)",
					got, tc.wantExit, err)
			}
			// Nothing sent: the whole point is that the refusal costs
			// no request, not merely that it happens.
			if reached {
				t.Errorf("node location %q reached the API", tc.value)
			}
		})
	}
}

func TestClusterCreateChecksTheCloudAccount(t *testing.T) {
	t.Run("a malformed id is a usage error", func(t *testing.T) {
		reached := false
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request,
		) {
			if r.Method != http.MethodGet {
				reached = true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(clusterBody))
		})
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, clusterCreateArgs(
			map[string]string{"--cloud-account-id": "acc-1"})...)
		if err == nil {
			t.Fatal("a non-UUID cloud account id was accepted")
		}
		if got := cli.ExitCode(err); got != ExitUsage {
			t.Errorf("exit = %d, want ExitUsage(%d) (err: %v)",
				got, ExitUsage, err)
		}
		if reached {
			t.Error("a malformed cloud account id reached the API")
		}
	})

	// A well-formed id naming nothing. The value is not malformed, so
	// this is the unknown-resource code rather than a usage error --
	// and the create must not be attempted.
	t.Run("a missing account stops the create", func(t *testing.T) {
		var paths []string
		url := testsupport.NewAuthedServer(t, func(
			w http.ResponseWriter, r *http.Request,
		) {
			paths = append(paths, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "/cloud-accounts/") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(
					`{"code":404,"message":"cloud account not found"}`))
				return
			}
			_, _ = w.Write([]byte(clusterBody))
		})
		rt, out, errb := testsupport.NewRuntime(t, "", "text")
		err := runAuthed(t, rt, out, url, clusterCreateArgs(nil)...)
		if err == nil {
			t.Fatal("a nonexistent cloud account was accepted")
		}
		if got := cli.ExitCode(err); got != ExitNotFound {
			t.Errorf("exit = %d, want ExitNotFound(%d) (err: %v)",
				got, ExitNotFound, err)
		}
		for _, p := range paths {
			if strings.HasPrefix(p, "POST") {
				t.Errorf("the create was attempted anyway: %v", paths)
			}
		}
		// The no-backup-store warning must not print for a cluster
		// that was never created. It reads as though the create had
		// happened and only the store were missing. Adding this check
		// put the warning above it once, and only a live run showed it.
		if strings.Contains(errb.String(), "no backup store") {
			t.Errorf("warned about the backup store of a cluster that "+
				"was never created: %q", errb.String())
		}
	})
}
