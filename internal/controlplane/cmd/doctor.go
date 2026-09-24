package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// newDoctorCmd builds `pgedge controlplane doctor`.
func newDoctorCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the Control Plane connection",
		Long: `doctor shows the resolved connection and probes each
configured server twice: /v1/version to confirm reachability, and
/v1/cluster to see whether a cluster exists. Use it first when a controlplane
command cannot connect.

The Cluster row is omitted when no server could be asked, reports
"disagree" when the configured servers give different answers, and
otherwise says initialized or uninitialized.

-o json carries cluster_initialized at the top level, summarising
only the servers that answered and absent when none answered or when
those that answered disagree -- there is no "disagree" value, so the
row and the key do not say the same thing. Each servers[] entry
carries its own cluster_initialized, absent for a server that could
not be asked.

A Profile row appears when the active profile carries a field that
will not parse -- a timeout: that is not a Go duration, say -- with
the offending value named, and -o json carries the same as problem.
doctor reports it rather than refusing to run, because a profile you
cannot resolve is the thing you came here to diagnose.

doctor exits 0 whatever it finds, including an unreachable server or
one with no cluster, so it is safe to run when the thing it diagnoses
is broken. That means it is NOT a gate: read reachable and
cluster_initialized from -o json rather than testing $?.

This CLI supports Control Plane >= ` + SupportFloor + `. A reachable
server below that floor reports as a warning naming both versions,
not as an error.

Example:
  pgedge controlplane doctor
  pgedge controlplane doctor -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Not propagated: doctor's own help says to run it first
			// when a controlplane command cannot connect, so it must not be the
			// first command to refuse. A profile field that will not
			// parse is reported as a row -- which is the diagnosis the
			// operator came for.
			c, cerr := resolveConnection(rt, cmd)
			problem := ""
			if cerr != nil {
				problem = cerr.Error()
			}
			type probe struct {
				url       string
				reachable bool
				version   string
				// warning is floorWarning's message when this server's
				// version is below SupportFloor, or "" otherwise
				// (unreachable, unparseable version, or at/above floor).
				warning string
				// clusterKnown is false when this server could not be
				// asked at all, which must stay distinct from a
				// server that answered "no cluster yet": one names a
				// remedy and the other must not (#255).
				clusterInitialized, clusterKnown bool
			}
			// Probe every base_url concurrently rather than walking
			// the list serially: with several configured servers and
			// the 30s default timeout, one dead or slow host used to
			// stall every probe behind it. Results land in probes at
			// their base_urls index, so the reported order stays
			// exactly the configured order regardless of which probe
			// answers first — tests and users depend on that
			// stability. A WaitGroup over a pre-sized slice keeps this
			// deterministic without a channel-into-map race.
			//
			// probeVersion builds a fresh *http.Client per call (see
			// newAPIClient in client.go), so there is no client or
			// transport state shared across goroutines. The one thing
			// they do share is rt.Stderr: when --verbose or --debug is
			// set, httplog.Transport.RoundTrip writes several separate
			// lines per request straight to it. Those individual
			// Fprintf calls are not coordinated across goroutines, so
			// concurrent probes under --verbose/--debug can interleave
			// their diagnostic lines. That is a cosmetic ordering
			// issue in opt-in diagnostic output, not a data race
			// (os.File.Write has no unsynchronized shared Go state for
			// -race to catch) and not a correctness issue in the
			// reported results, which is why it is left as-is here
			// rather than restructuring the shared httplog transport.
			probes := make([]probe, len(c.baseURLs))
			var wg sync.WaitGroup
			for i, u := range c.baseURLs {
				wg.Add(1)
				go func(i int, u string) {
					defer wg.Done()
					// The two probes run CONCURRENTLY, not one after
					// the other. Sequencing them costs a whole extra
					// round trip per server and puts doctor behind the
					// slowest one twice over -- reintroducing the
					// stall the outer concurrency exists to remove.
					// TestDoctorProbesConcurrently catches it, and did.
					//
					// probeCluster runs even against a server whose
					// version probe fails: it cannot know that yet,
					// and a server that answers /v1/cluster at all is
					// reachable by definition. Each goroutine writes
					// its own variables, so there is nothing shared
					// to race on.
					var (
						v           string
						ok          bool
						init, known bool
						inner       sync.WaitGroup
					)
					inner.Add(2)
					go func() {
						defer inner.Done()
						v, ok = probeVersion(
							context.Background(), rt, c, u)
					}()
					go func() {
						defer inner.Done()
						init, known = probeCluster(
							context.Background(), rt, c, u)
					}()
					inner.Wait()

					w := ""
					if ok {
						w = floorWarning(v)
					}
					probes[i] = probe{
						url: u, reachable: ok, version: v, warning: w,
						clusterInitialized: init, clusterKnown: known,
					}
				}(i, u)
			}
			wg.Wait()
			anyReachable := false
			for _, p := range probes {
				if p.reachable {
					anyReachable = true
					break
				}
			}
			// The collapsed row is emitted only when every server
			// that could be asked AGREES.
			//
			// Letting the first one speak for the rest was wrong,
			// and measurably so: with an uninitialized server
			// listed before an initialized one -- both reachable --
			// doctor told the operator to run cluster init while a
			// cluster existed, and reversing the order flipped the
			// verdict silently. That is the second-cluster outcome
			// the three-valued handling exists to prevent, produced
			// by the collapse rather than by the unknown case.
			//
			// Per-server state is reported either way, so a caller
			// can see the disagreement rather than infer it.
			clusterInitialized, clusterKnown := false, false
			clusterAgrees := true
			for _, p := range probes {
				if !p.clusterKnown {
					continue
				}
				if !clusterKnown {
					clusterInitialized, clusterKnown =
						p.clusterInitialized, true
					continue
				}
				if p.clusterInitialized != clusterInitialized {
					clusterAgrees = false
				}
			}

			if rt.Output.Structured() {
				servers := make([]map[string]any, len(probes))
				for i, p := range probes {
					servers[i] = map[string]any{
						"base_url":       p.url,
						"reachable":      p.reachable,
						"server_version": p.version,
						"warning":        p.warning,
					}
					// Absent when this server could not be asked, for
					// the same reason the top-level key is.
					if p.clusterKnown {
						servers[i]["cluster_initialized"] =
							p.clusterInitialized
					}
				}
				out := map[string]any{
					"base_urls": c.baseURLs,
					"mtls":      c.caCert != "" || c.clientCert != "",
					"insecure":  c.insecure,
					"reachable": anyReachable,
					"servers":   servers,
				}
				if problem != "" {
					out["problem"] = problem
				}
				// Absent rather than false when unknown: a consumer
				// keying on cluster_initialized == false would
				// otherwise read "could not tell" as "needs init".
				if clusterKnown && clusterAgrees {
					out["cluster_initialized"] = clusterInitialized
				}
				return rt.Output.Print(out, nil)
			}
			mtls := "disabled"
			if c.caCert != "" || c.clientCert != "" {
				mtls = "enabled"
			}
			rows := []output.Row{output.CheckRow{
				Check: "mTLS", Status: "ok", Details: mtls,
			}}
			if problem != "" {
				rows = append(rows, output.CheckRow{
					Check: "Profile", Status: "error", Details: problem,
				})
			}
			for _, p := range probes {
				status := "error"
				detail := p.url + " (unreachable)"
				if p.reachable {
					status = "ok"
					detail = fmt.Sprintf("%s (%s)", p.url, p.version)
					if p.warning != "" {
						status = "warning"
						detail = fmt.Sprintf("%s (%s) -- %s",
							p.url, p.version, p.warning)
					}
				}
				rows = append(rows, output.CheckRow{
					Check: "Reachable", Status: status, Details: detail,
				})
			}
			if clusterKnown {
				status, detail := "ok", "initialized"
				switch {
				case !clusterAgrees:
					status = "warning"
					detail = "the configured servers disagree — see " +
						"each server's cluster_initialized in -o json"
				case !clusterInitialized:
					status = "error"
					// Both remedies, matching the 409 prose: on a
					// host being added to an existing cluster, init
					// is the wrong one.
					detail = "uninitialized — run 'pgedge controlplane cluster " +
						"init', or 'pgedge controlplane cluster join' to join an " +
						"existing cluster"
				}
				rows = append(rows, output.CheckRow{
					Check: "Cluster", Status: status, Details: detail,
				})
			}
			return rt.Output.Print(rows, output.CheckHeaders())
		},
	}
}
