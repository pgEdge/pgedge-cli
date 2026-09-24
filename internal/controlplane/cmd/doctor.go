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
			// Not propagated: doctor is what to run when a command
			// cannot connect, so a profile field that will not parse
			// is reported as a row instead.
			c, cerr := resolveConnection(rt, cmd)
			problem := ""
			if cerr != nil {
				problem = cerr.Error()
			}
			type probe struct {
				url       string
				reachable bool
				version   string
				warning   string
				// clusterKnown false (could not ask) must stay distinct
				// from "no cluster yet": only the second names a remedy.
				clusterInitialized, clusterKnown bool
			}
			// Concurrent so one dead host does not stall the rest
			// behind the 30s default timeout. Each result lands at its
			// base_urls index, keeping the configured order.
			//
			// Each probe builds its own client (newAPIClient), so only
			// rt.Stderr is shared: --verbose/--debug lines from
			// httplog can interleave. That is cosmetic, not a race.
			probes := make([]probe, len(c.baseURLs))
			var wg sync.WaitGroup
			for i, u := range c.baseURLs {
				wg.Add(1)
				go func(i int, u string) {
					defer wg.Done()
					// Both probes run concurrently: sequencing them puts
					// doctor behind the slowest server twice over
					// (TestDoctorProbesConcurrently).
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
			// The collapsed verdict needs every server that answered
			// to agree. Letting the first speak for the rest sends an
			// operator to cluster init when a later server already has
			// a cluster.
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
				// Absent rather than false when unknown, here and per
				// server: false would read "could not tell" as "needs
				// init".
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
					// Both remedies: on a host joining an existing
					// cluster, init is the wrong one.
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
