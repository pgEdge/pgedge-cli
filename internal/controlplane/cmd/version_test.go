package cmd

import (
	"strings"
	"testing"
)

const versionBody = `{"version":"v0.9.0","revision":"abc123",` +
	`"revision_time":"2025-06-18T00:00:00Z","arch":"amd64"}`

func TestVersionRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version: %v", err)
		}
		if !strings.Contains(out.String(), "v0.9.0") {
			t.Errorf("missing version: %q", out.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version json: %v", err)
		}
		if !strings.Contains(out.String(), "abc123") {
			t.Errorf("missing revision: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(500, "boom"))
		if err := runControlplane(t, rt, out, url, "version"); err == nil {
			t.Fatal("expected error on 500")
		}
	})

	t.Run("yaml", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "yaml")
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version yaml: %v", err)
		}
		if !strings.Contains(out.String(), "amd64") {
			t.Errorf("missing arch: %q", out.String())
		}
	})

	t.Run("verbose logs request and response", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		rt.Verbose = true
		url := newServer(t, jsonHandler(200, versionBody))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version verbose: %v", err)
		}
		log := stderr.String()
		if !strings.Contains(log, "> GET") ||
			!strings.Contains(log, "< 200") {
			t.Errorf("verbose log missing entries: %q", log)
		}
	})

	t.Run("empty body reports no data", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		// 200 with a non-JSON body leaves the typed JSON200 nil.
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version empty: %v", err)
		}
		if !strings.Contains(stderr.String(), "No version data returned") {
			t.Errorf("missing no-data notice: %q", stderr.String())
		}
	})

	// TestVersionFloorWarning covers item 3 of issue #106: `cp version`
	// is one of the two places a controlplane command already has the server's
	// version in hand (it IS the command's job), so it is one of the
	// two call sites of warnBelowFloor. Below floor prints one stderr
	// line naming both versions and the policy; at/above floor and an
	// unparseable version stay silent.
	t.Run("below floor warns once on stderr", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBodyFor("v0.9.1")))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version: %v", err)
		}
		got := stderr.String()
		if strings.Count(got, "below the supported") != 1 {
			t.Fatalf("want exactly one below-floor warning, got: %q", got)
		}
		if !strings.Contains(got, "v0.9.1") || !strings.Contains(got, SupportFloor) {
			t.Errorf("warning must name both versions: %q", got)
		}
		if !strings.Contains(got, "supported: >= "+SupportFloor) {
			t.Errorf("warning must state the floor policy: %q", got)
		}
	})

	t.Run("at floor is silent on stderr", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBodyFor(SupportFloor)))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version: %v", err)
		}
		if got := stderr.String(); strings.Contains(got, "below the supported") {
			t.Errorf("want no warning at the floor, got: %q", got)
		}
	})

	t.Run("above floor is silent on stderr", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBodyFor("v1.2.3")))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version: %v", err)
		}
		if got := stderr.String(); strings.Contains(got, "below the supported") {
			t.Errorf("want no warning above the floor, got: %q", got)
		}
	})

	t.Run("unparseable version never warns", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, versionBodyFor("dev")))
		if err := runControlplane(t, rt, out, url, "version"); err != nil {
			t.Fatalf("version: %v", err)
		}
		if got := stderr.String(); strings.Contains(got, "below the supported") {
			t.Errorf("want no warning for an unparseable version, got: %q", got)
		}
	})

	t.Run("unreachable server yields network error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		rt.Verbose = true
		// Reserved TEST-NET / unroutable port: connection refused.
		err := runControlplane(t, rt, out, "http://127.0.0.1:1", "version")
		if err == nil {
			t.Fatal("expected network error")
		}
		ee, ok := err.(*ExitError)
		if !ok {
			t.Fatalf("want *ExitError, got %T", err)
		}
		if ee.Code() != ExitGeneral {
			t.Errorf("code = %d, want ExitGeneral", ee.Code())
		}
	})
}
