package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"gopkg.in/yaml.v3"
)

// runVersion executes the version command with the given format and
// returns captured stdout. The renderer and cobra share one buffer so
// both the text path (cmd.OutOrStdout) and the structured path
// (rt.Output.Out) land in the same place.
func runVersion(t *testing.T, format string,
	modules []module.ModuleInfo) string {
	t.Helper()
	var out bytes.Buffer
	rt := &module.Runtime{
		Output: &output.Renderer{Format: format, Out: &out},
	}
	cmd := NewVersionCmd(rt, modules)
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

func TestVersionText(t *testing.T) {
	prev := Version
	prevC, prevD := Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = prev, prevC, prevD })
	Version, Commit, BuildDate = "v9.9.9-test", "abc1234", "2026-07-23T00:00:00Z"

	got := runVersion(t, "text", nil)
	// First line is the bare version (grep-stable), then metadata.
	firstLine := strings.SplitN(strings.TrimSpace(got), "\n", 2)[0]
	if firstLine != Version {
		t.Errorf("first line = %q, want %q", firstLine, Version)
	}
	for _, want := range []string{Version, Commit, BuildDate} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q: %q", want, got)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	prev := Version
	prevC, prevD := Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = prev, prevC, prevD })
	Version, Commit, BuildDate = "v1.2.3", "deadbee", "2026-01-01T00:00:00Z"

	got := runVersion(t, "json", nil)
	var info versionInfo
	if err := json.Unmarshal([]byte(got), &info); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, got)
	}
	if info.Version != Version || info.Commit != Commit ||
		info.BuildDate != BuildDate {
		t.Errorf("json = %+v, want version/commit/date %q/%q/%q",
			info, Version, Commit, BuildDate)
	}
}

func TestVersionYAML(t *testing.T) {
	prev := Version
	prevC, prevD := Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = prev, prevC, prevD })
	Version, Commit, BuildDate = "v4.5.6", "cafef00", "2026-02-02T00:00:00Z"

	got := runVersion(t, "yaml", nil)
	var info versionInfo
	if err := yaml.Unmarshal([]byte(got), &info); err != nil {
		t.Fatalf("output is not valid YAML: %v (%q)", err, got)
	}
	if info.Version != Version || info.Commit != Commit ||
		info.BuildDate != BuildDate {
		t.Errorf("yaml = %+v, want version/commit/date %q/%q/%q",
			info, Version, Commit, BuildDate)
	}
}

func TestVersionTextModules(t *testing.T) {
	prev := Version
	t.Cleanup(func() { Version = prev })
	Version = "v1.0.0"
	mods := []module.ModuleInfo{
		{Name: "byoc", Version: "v1.0.0"},
		{Name: "controlplane", Version: "v0.4.1"},
	}
	got := runVersion(t, "text", mods)
	firstLine := strings.SplitN(strings.TrimSpace(got), "\n", 2)[0]
	if firstLine != Version {
		t.Errorf("first line = %q, want bare %q", firstLine, Version)
	}
	for _, want := range []string{"modules:", "byoc", "controlplane", "v0.4.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("text missing %q:\n%s", want, got)
		}
	}
}

func TestVersionJSONModules(t *testing.T) {
	mods := []module.ModuleInfo{{Name: "controlplane", Version: "v0.4.1"}}
	got := runVersion(t, "json", mods)
	var info versionInfo
	if err := json.Unmarshal([]byte(got), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(info.Modules) != 1 || info.Modules[0].Name != "controlplane" ||
		info.Modules[0].Version != "v0.4.1" {
		t.Errorf("json modules = %+v", info.Modules)
	}
}
