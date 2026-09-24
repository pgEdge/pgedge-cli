package cmd

import (
	"encoding/json"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// placeholderRE matches an OpenAPI path placeholder such as
// {database_id}.
var placeholderRE = regexp.MustCompile(`\{[^}]+\}`)

// specPath locates the vendored Control Plane description relative to
// this package.
func specPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "openapi", "control-plane.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("cannot find the vendored spec at %s: %v", p, err)
	}
	return p
}

// mutatingOperationWords are the operationId fragments that mark an
// operation as changing state. A GET carrying one of these is either in
// mutatingGETs or explicitly excused below.
var mutatingOperationWords = []string{
	"init", "cancel", "create", "delete", "update", "restart", "start",
	"stop", "failover", "switchover", "upgrade", "join", "deploy",
	"remove", "restore", "rotate", "resize", "accept", "register",
	"deregister", "apply", "promote",
}

// readOnlyGETs are GET operations whose operationId reads as mutating
// but which only read. Each needs a reason.
var readOnlyGETs = map[string]string{
	"get-join-token": "reads the token a node would use to join; " +
		"joining is a separate operation on the node itself",
}

// TestMutatingGETsCoverTheSpec is the gate that keeps mutatingGETs
// honest against the vendored description.
//
// It exists because trusting the HTTP method shipped a real bug: the
// first version of dry-run intercepted by method alone, so `controlplane task
// cancel --dry-run` sent GET .../cancel, really cancelled the task, and
// reported success. A re-vendor that adds another state-changing GET
// must fail here rather than quietly making --dry-run carry it out.
func TestMutatingGETsCoverTheSpec(t *testing.T) {
	raw, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("no paths parsed from the spec; the gate would pass " +
			"vacuously")
	}

	var suspicious, uncovered []string
	for path, ops := range doc.Paths {
		for method, op := range ops {
			if !strings.EqualFold(method, "get") {
				continue
			}
			lower := strings.ToLower(op.OperationID)
			mutating := false
			for _, w := range mutatingOperationWords {
				if strings.Contains(lower, w) {
					mutating = true
					break
				}
			}
			if !mutating {
				continue
			}
			suspicious = append(suspicious, op.OperationID)
			if _, excused := readOnlyGETs[op.OperationID]; excused {
				continue
			}
			if !matchesMutatingGET(path) {
				uncovered = append(uncovered,
					method+" "+path+" ("+op.OperationID+")")
			}
		}
	}

	// Positive control: if the keyword scan found nothing, the loop
	// above proved nothing either.
	if len(suspicious) == 0 {
		t.Fatal("no GET operation matched any mutating word; either the " +
			"spec changed shape or the scan is broken")
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("%d state-changing GET(s) are not covered by "+
			"mutatingGETs, so --dry-run would CARRY THEM OUT:\n  %s",
			len(uncovered), strings.Join(uncovered, "\n  "))
	}
}

// matchesMutatingGET reports whether path is covered by a mutatingGETs
// pattern, substituting a concrete value for each {placeholder} so a
// path template can be tested against a pattern written for real URLs.
func matchesMutatingGET(pathTemplate string) bool {
	concrete := placeholderRE.ReplaceAllString(pathTemplate, "abc123")
	// Both the bare path and the same path behind a base-URL prefix. A
	// pattern that only matches one of the two leaves the bug live for
	// half the deployments.
	for _, candidate := range []string{concrete, "/api" + concrete} {
		matched := false
		for _, re := range mutatingGETs {
			if re.MatchString(candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// TestReadOnlyGETExemptionsAllExist stops the exemption list from
// rotting: an entry that excuses nothing is an error, exactly as an
// unused doc-gate marker is.
func TestReadOnlyGETExemptionsAllExist(t *testing.T) {
	raw, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, ops := range doc.Paths {
		for method, op := range ops {
			if strings.EqualFold(method, "get") {
				present[op.OperationID] = true
			}
		}
	}
	for id, reason := range readOnlyGETs {
		if !present[id] {
			t.Errorf("exemption for %q excuses nothing: no GET "+
				"operation with that operationId", id)
		}
		if reason == "" {
			t.Errorf("%s: exemption carries no reason", id)
		}
	}
}

// TestMutatingGETsMatchRealURLs pins the patterns against the concrete
// paths the generated client builds, not just against templates.
func TestMutatingGETsMatchRealURLs(t *testing.T) {
	shouldStop := []string{
		"/v1/cluster/init",
		"/v1/databases/7f3a5c1e-0000-4000-8000-000000000000/tasks/" +
			"11111111-1111-4111-8111-111111111111/cancel",
		// A --base-url carrying a path prefix. A Control Plane behind a
		// reverse proxy at /api/ is an ordinary deployment, and the
		// first version of these patterns anchored on ^ — which left
		// the whole bug live for anyone configured that way.
		"/api/v1/cluster/init",
		"/api/v1/databases/7f3a/tasks/1111/cancel",
		"/cp/nested/prefix/v1/cluster/init",
		// A "/" inside a path parameter reaches the wire percent-encoded,
		// and url.URL.Path DECODES it — which splits the segment and
		// defeats the [^/]+ in the pattern. Only EscapedPath() still
		// matches, which is why reads() tests both spellings. Reachable
		// from a mistyped `--database "db1/"`.
		"/v1/databases/db1%2F/tasks/T/cancel",
		"/v1/databases/a%2Fb/tasks/T/cancel",
		// And the reverse: this DECODES to a covered path, so only
		// URL.Path matches it.
		"/v1/cluster%2Finit",
	}
	for _, p := range shouldStop {
		if !matchesConcrete(p) {
			t.Errorf("%s is not matched, so --dry-run would send it", p)
		}
	}
	// Paths that must still be readable. `join-token` is the trap: it
	// sits beside /cluster/init and its operationId contains "join".
	shouldPass := []string{
		"/v1/cluster/join-token",
		"/v1/cluster",
		"/v1/version",
		"/v1/hosts",
		"/v1/databases",
		"/v1/databases/7f3a/tasks",
		"/v1/databases/7f3a/tasks/1111",
		// The same reads under a path prefix.
		"/api/v1/cluster/join-token",
		"/api/v1/hosts",
		// A segment that merely ENDS with a covered name must not match:
		// the boundary is "/", not any character.
		"/v1/cluster/reinit",
		"/v1/notv1/cluster/init",
	}
	for _, p := range shouldPass {
		if matchesConcrete(p) {
			t.Errorf("%s is treated as state-changing, so a read that "+
				"a dry run needs would be blocked", p)
		}
	}
}

// matchesConcrete mirrors dryrun.Transport.reads: a pattern is tested
// against both the escaped path and its decoded form, and a match on
// either means "this writes". Testing only one spelling here would let
// the gate pass while the transport sent the request.
func matchesConcrete(path string) bool {
	decoded := path
	if u, err := neturl.Parse("http://h" + path); err == nil {
		decoded = u.Path
	}
	for _, re := range mutatingGETs {
		if re.MatchString(path) || re.MatchString(decoded) {
			return true
		}
	}
	return false
}
