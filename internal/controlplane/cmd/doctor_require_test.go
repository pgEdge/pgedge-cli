package cmd

import (
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

func TestUnmetConditions(t *testing.T) {
	all := []string{"reachable", "cluster", "supported"}
	for _, tc := range []struct {
		name string
		v    doctorVerdict
		want []string
	}{
		{"all hold", doctorVerdict{true, true, true}, nil},
		{"unreachable", doctorVerdict{false, false, false}, all},
		{"no cluster", doctorVerdict{true, false, true},
			[]string{"cluster"}},
		{"below floor", doctorVerdict{true, true, false},
			[]string{"supported"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unmetConditions(all, tc.v); !slices.Equal(got, tc.want) {
				t.Errorf("unmet = %v, want %v", got, tc.want)
			}
		})
	}
	if got := unmetConditions([]string{"reachable"},
		doctorVerdict{reachable: true}); got != nil {
		t.Errorf("an unrequested condition failed the gate: %v", got)
	}
}

// versionHandler answers /v1/cluster with a cluster and /v1/version
// with version.
func versionHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/cluster") {
			_, _ = io.WriteString(w, clusterBody)
			return
		}
		_, _ = io.WriteString(w, versionBodyFor(version))
	}
}

func TestDoctorRequire(t *testing.T) {
	cluster := clusterStateHandler(http.StatusOK, clusterBody)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		require string
		want    int
	}{
		{"reachable and cluster hold", cluster, "reachable,cluster", ExitOK},
		{"no cluster", clusterStateHandler(http.StatusConflict,
			uninitializedBody), "reachable,cluster", ExitGeneral},
		{"cluster unknown fails closed", clusterStateHandler(
			http.StatusInternalServerError, "boom"), "cluster", ExitGeneral},
		{"below floor", versionHandler("v0.9.1"), "supported", ExitGeneral},
		{"unparseable version", versionHandler("dev"), "supported",
			ExitGeneral},
		{"trailing comma is ignored", cluster, "reachable,", ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "json")
			url := newServer(t, tc.handler)
			err := runControlplane(t, rt, out, url, "doctor",
				"--require", tc.require)
			if got := cli.ExitCode(err); got != tc.want {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.want)
			}
			if !strings.Contains(out.String(), `"reachable"`) {
				t.Errorf("the report was not printed: %q", out.String())
			}
		})
	}

	t.Run("servers disagree on the cluster", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		err := runControlplaneURLs(t, rt, out, []string{"doctor",
			"--require", "cluster"},
			newServer(t, cluster),
			newServer(t, clusterStateHandler(http.StatusConflict,
				uninitializedBody)))
		if got := cli.ExitCode(err); got != ExitGeneral {
			t.Fatalf("exit = %d (%v), want %d", got, err, ExitGeneral)
		}
	})

	for _, empty := range []string{",", ""} {
		t.Run("no condition: "+strconv.Quote(empty), func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			requireUsageError(t, runControlplane(t, rt, out,
				newServer(t, cluster), "doctor", "--require", empty))
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		err := runControlplane(t, rt, out, deadServerURL(t), "doctor",
			"--require", "reachable")
		if got := cli.ExitCode(err); got != ExitGeneral {
			t.Fatalf("exit = %d (%v), want %d", got, err, ExitGeneral)
		}
	})

	t.Run("unknown condition probes nothing", func(t *testing.T) {
		var hits atomic.Int32
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			cluster(w, r)
		})
		err := runControlplane(t, rt, out, url, "doctor",
			"--require", "reachable,ready")
		requireUsageError(t, err)
		if !strings.Contains(err.Error(), `"ready"`) {
			t.Errorf("error does not name the condition: %v", err)
		}
		if hits.Load() != 0 {
			t.Errorf("doctor probed %d times before refusing", hits.Load())
		}
	})
}
