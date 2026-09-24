package conn

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mutatingOperationWords are operationId fragments that mark an
// operation as changing state.
var mutatingOperationWords = []string{
	"init", "cancel", "create", "delete", "update", "restart", "start",
	"stop", "failover", "switchover", "upgrade", "join", "deploy",
	"remove", "restore", "rotate", "resize", "accept", "register",
	"deregister", "apply", "promote",
}

// readOnlySafeOps are safe-method Starfleet operations whose operationId
// reads as mutating but which only read. Each would need a reason.
//
// EMPTY, and verified so: no safe-method operation in any of the three
// vendored Starfleet specs has an operationId containing a mutating word, so
// nothing needs excusing. (`getCloudFormationTemplate` retrieves a
// template for the caller to apply and creates nothing, but it is not
// flagged either, so an entry for it would excuse nothing.) The map
// stays so a future one has to be justified here rather than skipped.
var readOnlySafeOps = map[string]string{}

// TestStarfleetSpecsHaveNoStateChangingReads is why HTTPClientFor passes no
// mutating-GET patterns.
//
// That is a claim about the three vendored Starfleet specs, not a property of
// the code, so it needs a gate: `make vendor-spec` could introduce a
// state-changing GET at any time, and without this the starfleet module would
// silently start sending it under --dry-run. The controlplane module has the same
// gate over control-plane.json, where two such operations really exist.
func TestStarfleetSpecsHaveNoStateChangingReads(t *testing.T) {
	var checked int
	for _, name := range []string{
		"byoc.yaml", "managed.yaml", "account.yaml",
	} {
		path := filepath.Join("..", "..", "..", "openapi", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			Paths map[string]map[string]struct {
				OperationID string `yaml:"operationId"`
				Summary     string `yaml:"summary"`
			} `yaml:"paths"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if len(doc.Paths) == 0 {
			t.Fatalf("%s: no paths parsed; the gate would pass "+
				"vacuously", name)
		}

		var suspicious []string
		for p, ops := range doc.Paths {
			for method, op := range ops {
				switch strings.ToLower(method) {
				case "get", "head", "options":
				default:
					continue
				}
				checked++
				lower := strings.ToLower(op.OperationID)
				for _, w := range mutatingOperationWords {
					if !strings.Contains(lower, w) {
						continue
					}
					if _, ok := readOnlySafeOps[op.OperationID]; ok {
						break
					}
					suspicious = append(suspicious,
						strings.ToUpper(method)+" "+p+
							" ("+op.OperationID+")")
					break
				}
			}
		}
		sort.Strings(suspicious)
		if len(suspicious) > 0 {
			t.Errorf("%s declares %d safe-method operation(s) that read "+
				"as state-changing. If any of them really writes, "+
				"HTTPClientFor must pass a pattern for it — otherwise "+
				"--dry-run will SEND it. If it only reads, add it to "+
				"readOnlySafeOps with a reason:\n  %s",
				name, len(suspicious), strings.Join(suspicious, "\n  "))
		}
	}
	// Positive control: an empty scan would pass while proving nothing.
	if checked < 20 {
		t.Errorf("only %d safe-method operations scanned across the "+
			"three specs; the scan is not finding them", checked)
	}
	t.Logf("scanned %d safe-method Starfleet operations", checked)
}

// TestReadOnlySafeOpExemptionsAllExist stops the exemption list rotting.
func TestReadOnlySafeOpExemptionsAllExist(t *testing.T) {
	present := map[string]bool{}
	for _, name := range []string{
		"byoc.yaml", "managed.yaml", "account.yaml",
	} {
		raw, err := os.ReadFile(
			filepath.Join("..", "..", "..", "openapi", name))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Paths map[string]map[string]struct {
				OperationID string `yaml:"operationId"`
			} `yaml:"paths"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, ops := range doc.Paths {
			for _, op := range ops {
				present[op.OperationID] = true
			}
		}
	}
	for id, reason := range readOnlySafeOps {
		if !present[id] {
			t.Errorf("exemption for %q excuses nothing: no such "+
				"operation in the vendored Starfleet specs", id)
		}
		if reason == "" {
			t.Errorf("%s: exemption carries no reason", id)
		}
		// And it must actually be needed: an operationId carrying no
		// mutating word is never flagged, so an entry for it is dead
		// weight — the same rule the doc-gate markers follow.
		lower := strings.ToLower(id)
		needed := false
		for _, w := range mutatingOperationWords {
			if strings.Contains(lower, w) {
				needed = true
				break
			}
		}
		if !needed {
			t.Errorf("exemption for %q is unnecessary: its operationId "+
				"contains no mutating word, so nothing would flag it",
				id)
		}
	}
}
