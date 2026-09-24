package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// paramSchema is the slice of a query parameter's schema the
// client-side checks mirror.
type paramSchema struct {
	Pattern string `yaml:"pattern"`
	Minimum *int   `yaml:"minimum"`
	Maximum *int   `yaml:"maximum"`
}

// queryParamSchema reads one GET query parameter's schema out of the
// vendored managed spec.
//
// oapi-codegen emits neither patterns nor numeric bounds into Go, so
// unlike the enum checks in specenums_test.go there is nothing
// generated to borrow. Reading the document is the only assertion that
// fails when a re-vendor LOOSENS the contract — without it the CLI
// would go on refusing, client-side with exit 2, values the API
// accepts. A missing parameter is a hard failure, not a skip: a test
// that lost its truth source passes against anything.
func queryParamSchema(t *testing.T, path, name string) paramSchema {
	t.Helper()

	specPath := filepath.Join(
		"..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	var doc struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name   string      `yaml:"name"`
					In     string      `yaml:"in"`
					Schema paramSchema `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}

	op, ok := doc.Paths[path]
	if !ok {
		t.Fatalf("no GET %s in %s; this test has lost its truth "+
			"source and would pass against anything", path, specPath)
	}
	for _, p := range op.Get.Parameters {
		if p.Name == name && p.In == "query" {
			return p.Schema
		}
	}
	t.Fatalf("GET %s declares no query parameter %q in %s",
		path, name, specPath)
	return paramSchema{}
}

// TestMetricsIntervalPatternMatchesSpec pins the client-side --window
// check to the pattern the API validates against.
func TestMetricsIntervalPatternMatchesSpec(t *testing.T) {
	got := queryParamSchema(
		t, "/managed/v1/databases/{id}/metrics", "interval")
	if got.Pattern == "" {
		t.Fatal("the spec declares no pattern for interval; the " +
			"client-side check has nothing to mirror")
	}
	if got.Pattern != metricsIntervalPattern {
		t.Errorf("spec pattern %q, CLI pattern %q; the CLI would "+
			"refuse values the API accepts, or accept ones it does not",
			got.Pattern, metricsIntervalPattern)
	}
}

// TestLogsMaxLinesBoundsMatchSpec pins the client-side --max-lines
// bounds to the spec's own minimum and maximum.
func TestLogsMaxLinesBoundsMatchSpec(t *testing.T) {
	got := queryParamSchema(
		t, "/managed/v1/databases/{id}/logs", "max_lines")
	if got.Minimum == nil || got.Maximum == nil {
		t.Fatal("the spec declares no bounds for max_lines; the " +
			"client-side check has nothing to mirror")
	}
	if *got.Minimum != logsMaxLinesMin {
		t.Errorf("spec minimum %d, CLI minimum %d",
			*got.Minimum, logsMaxLinesMin)
	}
	if *got.Maximum != logsMaxLinesMax {
		t.Errorf("spec maximum %d, CLI maximum %d",
			*got.Maximum, logsMaxLinesMax)
	}
}
