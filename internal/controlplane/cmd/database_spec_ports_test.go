package cmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostsCountingServer starts a server that answers GET /v1/hosts with
// hostsBody (200, JSON) and every other path with otherBody (200,
// JSON) -- create's and update's own success response. *hostsCalls is
// incremented once per /v1/hosts request, which is the positive
// control TestWarnSystemdPortsMissingSkipsListHostsWhenNothingMissing
// needs: asserting a count of zero is only meaningful if the same
// counter would show a nonzero count when the call does happen.
func hostsCountingServer(
	t *testing.T, hostsBody, otherBody string, hostsCalls *int,
) string {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			*hostsCalls++
			_, _ = io.WriteString(w, hostsBody)
			return
		}
		_, _ = io.WriteString(w, otherBody)
	})
}

// hostsErrorServer is hostsCountingServer's twin for the error-status
// and transport-failure rows: /v1/hosts answers with hostsStatus
// (non-2xx, or a hijacked/closed connection when hostsStatus is 0),
// everything else answers with otherBody (200).
func hostsErrorServer(
	t *testing.T, hostsStatus int, otherBody string,
) string {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/hosts" {
			if hostsStatus == 0 {
				hj, ok := w.(http.Hijacker)
				if ok {
					if c, _, err := hj.Hijack(); err == nil {
						_ = c.Close()
					}
					return
				}
			}
			w.WriteHeader(hostsStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, otherBody)
	})
}

// writeSpecFile writes contents to a temp file and returns its path.
func writeSpecFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

const specBothPortsSet = `database_name: storefront
port: 5432
patroni_port: 8008
nodes:
  - name: n1
    host_ids: [host-1]
`

const specMissingPatroniOnly = `database_name: storefront
port: 5432
nodes:
  - name: n1
    host_ids: [host-1]
`

func TestWarnSystemdPortsMissingSkipsListHostsWhenNothingMissing(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specBothPortsSet)
	var hostsCalls int
	url := hostsCountingServer(t, `{"hosts":[]}`, createResp, &hostsCalls)
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if hostsCalls != 0 {
		t.Errorf("ListHosts called %d times, want 0 -- a fully-specified "+
			"spec must never trigger the systemd-port check", hostsCalls)
	}
}

// TestWarnSystemdPortsMissingPositiveControl proves the counter above
// is real: the same counter, on a spec that DOES omit both fields,
// must show at least one call. Without this, a broken guard that never
// calls ListHosts at all would pass the zero-calls test above for the
// wrong reason.
func TestWarnSystemdPortsMissingPositiveControl(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML) // missing both port fields
	var hostsCalls int
	url := hostsCountingServer(t, `{"hosts":[]}`, createResp, &hostsCalls)
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if hostsCalls != 1 {
		t.Errorf("ListHosts called %d times, want exactly 1", hostsCalls)
	}
}

func TestWarnSystemdPortsMissingAllSwarmNoWarning(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"swarm"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(errb.String(), "systemd requires both") {
		t.Errorf("swarm host should not trigger the systemd warning: %q",
			errb.String())
	}
}

func TestWarnSystemdPortsMissingTargetedSystemdWarns(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"systemd"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := errb.String()
	if !strings.Contains(got, `node "n1"`) ||
		!strings.Contains(got, `host "host-1"`) {
		t.Errorf("warning should name the node and host: %q", got)
	}
	if !strings.Contains(got, "no port or patroni_port") {
		t.Errorf("warning should say both are missing: %q", got)
	}
}

func TestWarnSystemdPortsMissingNamesOnlyThePatroniPort(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specMissingPatroniOnly)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"systemd"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := errb.String()
	if !strings.Contains(got, "no patroni_port") {
		t.Errorf("warning should name patroni_port only: %q", got)
	}
	if strings.Contains(got, "no port or patroni_port") ||
		strings.Contains(got, "sets no port.") {
		t.Errorf("warning should not also claim port is missing: %q", got)
	}
}

func TestWarnSystemdPortsMissingHostNotTargetedNoWarning(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML) // targets host-1 only
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			// host-2 is systemd, but no node targets it.
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"swarm"},`+
					`{"id":"host-2","orchestrator":"systemd"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(errb.String(), "systemd requires both") {
		t.Errorf("an untargeted systemd host must not trigger a "+
			"warning: %q", errb.String())
	}
}

func TestWarnSystemdPortsMissingListHostsErrorSkipsWarningSilently(t *testing.T) {
	for name, status := range map[string]int{
		"500 status": http.StatusInternalServerError,
		"transport":  0,
	} {
		t.Run(name, func(t *testing.T) {
			rt, out, errb := newTestRuntime(t, "", "text")
			specPath := writeSpecFile(t, specYAML)
			url := hostsErrorServer(t, status, createResp)
			if err := runControlplane(t, rt, out, url,
				"database", "create", "storefront", "-f", specPath); err != nil {
				t.Fatalf("create should still succeed: %v", err)
			}
			if strings.Contains(errb.String(), "systemd requires both") {
				t.Errorf("a failed ListHosts must skip the warning "+
					"silently: %q", errb.String())
			}
			if !strings.Contains(errb.String(), createTaskID) {
				t.Errorf("create should still report its accepted "+
					"task: %q", errb.String())
			}
		})
	}
}

func TestWarnSystemdPortsMissingCasingSystemDWarns(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"SystemD"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(errb.String(), "systemd requires both") {
		t.Errorf("casing variant should still warn: %q", errb.String())
	}
}

func TestWarnSystemdPortsMissingUnknownOrchestratorNoWarning(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"nomad"}]}`)
			return
		}
		_, _ = io.WriteString(w, createResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "create", "storefront", "-f", specPath); err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(errb.String(), "systemd requires both") {
		t.Errorf("an unknown orchestrator must not warn: %q",
			errb.String())
	}
}

// TestWarnSystemdPortsMissingUpdateAlsoWarns proves update shares the
// same check as create, not a copy that has drifted.
func TestWarnSystemdPortsMissingUpdateAlsoWarns(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	specPath := writeSpecFile(t, specYAML)
	url := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hosts" {
			_, _ = io.WriteString(w,
				`{"hosts":[{"id":"host-1","orchestrator":"systemd"}]}`)
			return
		}
		_, _ = io.WriteString(w, updateResp)
	})
	if err := runControlplane(t, rt, out, url,
		"database", "update", "storefront", "-f", specPath, "--force"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(errb.String(), "systemd requires both") {
		t.Errorf("update should warn like create does: %q", errb.String())
	}
}

// TestSystemdPortWarningAdvisesPerNodePorts pins the remedy the
// warning names. The Control Plane folds a top-level port into every
// node that does not override it and then requires per-host
// uniqueness, so on a multi-node host the top-level pair is the one
// remedy that cannot work, and the message advised exactly that until
// this test landed. Nothing else pins the sentence, so a re-word
// reintroducing the wrong advice would otherwise pass every gate.
func TestSystemdPortWarningAdvisesPerNodePorts(t *testing.T) {
	got := systemdPortWarning(nodePorts{
		name:           "n1",
		missingPort:    true,
		missingPatroni: true,
	}, "host-1")

	for _, want := range []string{
		`node "n1"`,
		`host "host-1"`,
		"no port or patroni_port",
		"systemd requires both",
		"on every node",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q: %q", want, got)
		}
	}
	// Both spellings: the advice this replaced read "set them at the
	// top level", and a re-word that only hyphenated it would be the
	// same wrong remedy.
	for _, bad := range []string{"at the top level", "at the top-level"} {
		if strings.Contains(got, bad) {
			t.Errorf("warning must not advise the top-level pair: %q", got)
		}
	}
}
