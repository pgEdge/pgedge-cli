package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/config"
)

func TestDoctorRun(t *testing.T) {
	t.Run("reachable", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if !strings.Contains(out.String(), "v0.9.0") {
			t.Errorf("missing server version: %q", out.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor json: %v", err)
		}
		if !strings.Contains(out.String(), "\"reachable\":true") {
			t.Errorf("want reachable true: %q", out.String())
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := deadServerURL(t)
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if !strings.Contains(out.String(), url) ||
			!strings.Contains(out.String(), "unreachable") {
			t.Errorf("want unreachable detail for %s: %q", url, out.String())
		}
	})

	t.Run("unreachable json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := deadServerURL(t)
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor json: %v", err)
		}
		if !strings.Contains(out.String(), "\"reachable\":false") {
			t.Errorf("want reachable false: %q", out.String())
		}
	})
}

// deadServerURL returns the base URL of an httptest server that has
// already been closed, so connections to it are refused immediately
// (no timeout wait) — a hermetic stand-in for an unreachable
// control-plane.
func deadServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(jsonHandler(200, versionBody))
	url := srv.URL
	srv.Close()
	return url
}

// versionBodyFor builds a minimal /v1/version JSON body carrying the
// given version string, for the below/at/above-floor doctor cases.
func versionBodyFor(version string) string {
	return `{"version":"` + version + `","revision":"abc123",` +
		`"revision_time":"2025-06-18T00:00:00Z","arch":"amd64"}`
}

// TestDoctorFloorWarning drives `controlplane doctor` against a single reachable
// server at each side of SupportFloor, plus an unparseable dev
// version, and checks the Reachable row's status and detail — this is
// item 2 of issue #106: below floor is a warning row naming both
// versions and the policy, at/above floor is unchanged, and an
// unparseable version never warns.
func TestDoctorFloorWarning(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantWarn bool
	}{
		{name: "below floor patch", version: "v0.9.1", wantWarn: true},
		{name: "below floor minor", version: "v0.8.0", wantWarn: true},
		{name: "at floor", version: "v0.10.0", wantWarn: false},
		{name: "above floor", version: "v0.11.0", wantWarn: false},
		{name: "unparseable dev version", version: "dev", wantWarn: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "text")
			url := newServer(t, jsonHandler(200, versionBodyFor(tc.version)))
			if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			got := out.String()
			if tc.wantWarn {
				if !strings.Contains(got, "warning") {
					t.Errorf("want a warning status, got: %q", got)
				}
				if !strings.Contains(got, tc.version) ||
					!strings.Contains(got, SupportFloor) {
					t.Errorf("warning must name both versions: %q", got)
				}
				if !strings.Contains(got, "supported: >= "+SupportFloor) {
					t.Errorf("warning must state the floor policy: %q", got)
				}
			} else if strings.Contains(got, "warning") {
				t.Errorf("want no warning, got: %q", got)
			}
			// doctor's own exit code never reflects a warning row — it
			// stays 0 whether the server is below floor or not, matching
			// how the starfleet module's doctor treats "warning" (auth
			// token expired) as informational, never a failing check.
		})
	}

	t.Run("below floor JSON carries a warning field", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, versionBodyFor("v0.9.1")))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor json: %v", err)
		}
		var parsed struct {
			Servers []struct {
				Warning string `json:"warning"`
			} `json:"servers"`
		}
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out.String())
		}
		if len(parsed.Servers) != 1 || parsed.Servers[0].Warning == "" {
			t.Fatalf("want a non-empty warning field: %+v", parsed.Servers)
		}
	})

	t.Run("at floor JSON warning field is empty", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, versionBodyFor("v0.10.0")))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor json: %v", err)
		}
		var parsed struct {
			Servers []struct {
				Warning string `json:"warning"`
			} `json:"servers"`
		}
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out.String())
		}
		if len(parsed.Servers) != 1 || parsed.Servers[0].Warning != "" {
			t.Fatalf("want an empty warning field: %+v", parsed.Servers)
		}
	})

	t.Run("doctor exit code stays 0 below floor", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBodyFor("v0.9.1")))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor should exit 0 with a warning row, got: %v", err)
		}
	})
}

func TestDoctorMultiServer(t *testing.T) {
	t.Run("json reports both servers independently", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		live := newServer(t, jsonHandler(200, versionBody))
		dead := deadServerURL(t)
		err := runControlplaneURLs(t, rt, out, []string{"doctor"}, live, dead)
		if err != nil {
			t.Fatalf("doctor multi-server json: %v", err)
		}
		got := out.String()

		var parsed struct {
			BaseURLs  []string `json:"base_urls"`
			Reachable bool     `json:"reachable"`
			Servers   []struct {
				BaseURL       string `json:"base_url"`
				Reachable     bool   `json:"reachable"`
				ServerVersion string `json:"server_version"`
			} `json:"servers"`
		}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal doctor output: %v\n%s", err, got)
		}

		if len(parsed.Servers) != 2 {
			t.Fatalf("servers len = %d, want 2: %+v",
				len(parsed.Servers), parsed.Servers)
		}
		if !parsed.Reachable {
			t.Errorf("top-level reachable = false, want true " +
				"(one live server)")
		}

		var liveEntry, deadEntry *struct {
			BaseURL       string `json:"base_url"`
			Reachable     bool   `json:"reachable"`
			ServerVersion string `json:"server_version"`
		}
		for i := range parsed.Servers {
			s := &parsed.Servers[i]
			switch s.BaseURL {
			case live:
				liveEntry = s
			case dead:
				deadEntry = s
			}
		}
		if liveEntry == nil || deadEntry == nil {
			t.Fatalf("expected entries for both %s and %s: %+v",
				live, dead, parsed.Servers)
		}
		if !liveEntry.Reachable {
			t.Errorf("live server %s: reachable = false, want true", live)
		}
		if liveEntry.ServerVersion != "v0.9.0" {
			t.Errorf("live server_version = %q, want %q",
				liveEntry.ServerVersion, "v0.9.0")
		}
		if deadEntry.Reachable {
			t.Errorf("dead server %s: reachable = true, want false", dead)
		}
	})

	t.Run("text shows one Reachable row per server", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		live := newServer(t, jsonHandler(200, versionBody))
		dead := deadServerURL(t)
		err := runControlplaneURLs(t, rt, out, []string{"doctor"}, live, dead)
		if err != nil {
			t.Fatalf("doctor multi-server text: %v", err)
		}
		got := out.String()

		if !strings.Contains(got, live+" (v0.9.0)") {
			t.Errorf("missing live row for %s: %q", live, got)
		}
		if !strings.Contains(got, dead+" (unreachable)") {
			t.Errorf("missing dead row for %s: %q", dead, got)
		}
		if strings.Count(got, "Reachable") != 2 {
			t.Errorf("want 2 Reachable rows, got %d: %q",
				strings.Count(got, "Reachable"), got)
		}
	})
}

// slowServer starts a server whose handler sleeps for delay before
// answering with a 200 version body, standing in for a control-plane
// server whose network path is merely slow rather than dead.
func slowServer(t *testing.T, delay time.Duration) string {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		jsonHandler(200, versionBody)(w, r)
	})
}

// TestDoctorProbesConcurrently proves the doctor loop fans its probes
// out rather than walking base_urls one at a time. Two of the three
// servers each sleep 800ms; run serially that alone is >= 1.6s, so a
// wall time comfortably under that (1.4s, a generous CI margin) is only
// possible if both slow probes ran concurrently. The first server in
// the list is one of the slow ones specifically to prove a slow LEADER
// cannot stall the servers behind it in the result order.
func TestDoctorProbesConcurrently(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	slow1 := slowServer(t, 800*time.Millisecond)
	slow2 := slowServer(t, 800*time.Millisecond)
	fast := newServer(t, jsonHandler(200, versionBody))

	start := time.Now()
	err := runControlplaneURLs(t, rt, out,
		[]string{"doctor"}, slow1, slow2, fast)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	const budget = 1400 * time.Millisecond
	if elapsed >= budget {
		t.Errorf("doctor took %s, want < %s (probes should run "+
			"concurrently, not serially)", elapsed, budget)
	}

	var parsed struct {
		BaseURLs []string `json:"base_urls"`
		Servers  []struct {
			BaseURL string `json:"base_url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal doctor output: %v\n%s", err, out.String())
	}
	wantOrder := []string{slow1, slow2, fast}
	if len(parsed.Servers) != len(wantOrder) {
		t.Fatalf("servers len = %d, want %d: %+v",
			len(parsed.Servers), len(wantOrder), parsed.Servers)
	}
	for i, want := range wantOrder {
		if parsed.Servers[i].BaseURL != want {
			t.Errorf("servers[%d].base_url = %q, want %q (base_urls "+
				"order must stay stable regardless of probe timing)",
				i, parsed.Servers[i].BaseURL, want)
		}
	}
}

// clusterStateHandler answers /v1/version with a version and
// /v1/cluster with clusterStatus, so a test can drive doctor's two
// probes independently.
func clusterStateHandler(
	clusterStatus int, clusterBody string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/cluster") {
			w.WriteHeader(clusterStatus)
			_, _ = io.WriteString(w, clusterBody)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, versionBody)
	}
}

// uninitializedBody is what a Control Plane with no cluster answers
// with. Measured against control-plane v0.10.1 on 2026-08-20: GET
// /v1/{cluster,hosts,databases,tasks} each return HTTP 409 with
// exactly this body.
const uninitializedBody = `{"name":"cluster_not_initialized",` +
	`"message":"this operation is invalid on an uninitialized cluster"}`

// doctor gave a clean bill of health to a Control Plane on which
// nothing worked: reachable, right version, and every other verb
// answering 409 (#255). The state is one call away.
func TestDoctorReportsAnUninitializedCluster(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, clusterStateHandler(
			http.StatusConflict, uninitializedBody))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"Cluster", "uninitialized", "cluster init",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output does not mention %q:\n%s", want, got)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, clusterStateHandler(
			http.StatusConflict, uninitializedBody))
		if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
			t.Fatalf("doctor: %v", err)
		}
		if !strings.Contains(out.String(), `"cluster_initialized":false`) {
			t.Errorf("want cluster_initialized false:\n%s", out.String())
		}
	})
}

func TestDoctorReportsAnInitializedCluster(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, clusterStateHandler(
		http.StatusOK, `{"cluster":{"id":"c1"}}`))
	if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), `"cluster_initialized":true`) {
		t.Errorf("want cluster_initialized true:\n%s", out.String())
	}
}

// Three-valued, and this is the arm that matters most: an unreachable
// server means the cluster state is UNKNOWN, not "needs init". The key
// is absent rather than false, so a consumer keying on
// cluster_initialized == false cannot read "could not tell" as a
// remedy — and doctor must not tell an operator to create a second
// cluster.
func TestDoctorOmitsClusterStateWhenItCannotTell(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	if err := runControlplane(t, rt, out, deadServerURL(t), "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out.String(), "cluster_initialized") {
		t.Errorf("reported a cluster state for an unreachable "+
			"server:\n%s", out.String())
	}
}

// A 409 that is NOT cluster_not_initialized must not be read as one:
// the discriminator keys on the name field, not the status.
func TestDoctorDoesNotReadEveryConflictAsUninitialized(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, clusterStateHandler(http.StatusConflict,
		`{"name":"something_else","message":"unrelated"}`))
	if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out.String(), "cluster_initialized") {
		t.Errorf("an unrelated 409 was classified:\n%s", out.String())
	}
}

// The 409 body was dumped raw as JSON and never named the remedy,
// which is a command in the same tree (#255).
func TestUninitializedClusterIsRenderedAsProse(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(
		http.StatusConflict, uninitializedBody))
	err := runControlplane(t, rt, out, url, "host", "list")
	if err == nil {
		t.Fatal("an uninitialized cluster was reported as success")
	}
	if !strings.Contains(err.Error(), "cluster init") {
		t.Errorf("error does not name the remedy: %v", err)
	}
	// The raw body must not be dumped alongside the prose.
	if strings.Contains(err.Error(), `{"name"`) {
		t.Errorf("raw JSON body leaked into the message: %v", err)
	}
}

// An unrelated 409 keeps the generic rendering, so the prose above
// cannot be reached by any conflict at all.
func TestAnUnrelatedConflictKeepsTheGenericMessage(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(http.StatusConflict,
		`{"name":"resource_busy","message":"try again"}`))
	err := runControlplane(t, rt, out, url, "host", "list")
	if err == nil {
		t.Fatal("a 409 was reported as success")
	}
	if strings.Contains(err.Error(), "cluster init") {
		t.Errorf("an unrelated 409 was given the init remedy: %v", err)
	}
}

// Letting the first reachable server speak for the fleet was wrong,
// and measurably so: with an uninitialized server listed before an
// initialized one — both reachable — doctor told the operator to run
// 'cluster init' while a cluster existed, and reversing the order
// flipped the verdict silently. That is the second-cluster outcome the
// three-valued handling exists to prevent, produced by the collapse
// rather than by the unknown case.
func TestDoctorReportsDisagreementRatherThanTheFirstAnswer(t *testing.T) {
	uninit := newServer(t, clusterStateHandler(
		http.StatusConflict, uninitializedBody))
	init := newServer(t, clusterStateHandler(
		http.StatusOK, `{"cluster":{"id":"c1"}}`))

	// Both orders, because the defect was order-dependence itself.
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{"uninitialized first", []string{uninit, init}},
		{"initialized first", []string{init, uninit}},
	} {
		order := tc.order
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", "json")
			if err := runControlplaneURLs(t, rt, out,
				[]string{"doctor"}, order...); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			// Parsed, not substring-matched: the per-server entries
			// carry the same key, so a Contains check on the whole
			// document cannot tell a top-level verdict from a
			// server's own.
			var parsed struct {
				ClusterInitialized *bool `json:"cluster_initialized"`
				Servers            []struct {
					ClusterInitialized *bool `json:"cluster_initialized"`
				} `json:"servers"`
			}
			if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, out.String())
			}
			if parsed.ClusterInitialized != nil {
				t.Errorf("a top-level verdict (%v) was reported for "+
					"servers that disagree",
					*parsed.ClusterInitialized)
			}
			// The per-server state must still be there, or the
			// operator has no way to see WHICH server disagrees.
			var seen int
			for _, sv := range parsed.Servers {
				if sv.ClusterInitialized != nil {
					seen++
				}
			}
			if seen != 2 {
				t.Errorf("per-server cluster state present on %d of 2 "+
					"servers:\n%s", seen, out.String())
			}
		})
	}
}

// probeCluster keys on resp.JSON200, not on any 2xx: a 200 carrying a
// non-JSON body leaves the typed field nil, and reporting
// "initialized" from a body never parsed claims knowledge the probe
// does not have. Without this test the fix is invisible — the whole
// package passes with it reverted, because both existing 200 cases
// send valid JSON.
func TestDoctorTreatsANonJSONClusterReplyAsUnknown(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cluster") {
			// 200, but not a Control Plane's answer — a proxy
			// interstitial reaches this function in practice, which is
			// why checkResponse already special-cases a router miss.
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "<html>hello</html>")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, versionBody)
	})
	if err := runControlplane(t, rt, out, url, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out.String(), "cluster_initialized") {
		t.Errorf("a 200 whose body never parsed was reported as a "+
			"cluster state:\n%s", out.String())
	}
}

// The diagnostic verbs must SURFACE a broken profile, not merely
// survive it. The gate in internal/clitest asserts they exit 0, which
// a version that silently swallowed the error would also satisfy —
// and a merge resolution put the row and the json key in by hand, so
// nothing was checking either.
func TestDoctorSurfacesABrokenProfile(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			rt, out, _ := newTestRuntime(t, "", format)
			rt.Config.SetControlplaneProfile("default", &config.ControlplaneProfile{
				BaseURL: deadServerURL(t),
				Timeout: "10 minutes",
			})
			cmd := NewControlplaneCmd(rt)
			cmd.SetArgs([]string{"doctor"})
			cmd.SetOut(out)
			cmd.SetErr(out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			got := out.String()
			// The offending VALUE, so a row that merely said
			// "something is wrong" would not satisfy this.
			if !strings.Contains(got, "10 minutes") {
				t.Errorf("doctor does not surface the bad timeout:\n%s",
					got)
			}
			want := "Profile"
			if format == "json" {
				want = "problem"
			}
			if !strings.Contains(got, want) {
				t.Errorf("output carries no %q:\n%s", want, got)
			}
		})
	}
}
