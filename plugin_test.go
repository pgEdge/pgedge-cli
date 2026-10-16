package pgedgecli

import (
	"encoding/json"
	"os"
	"testing"
)

type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestPluginManifestsAgree holds the Claude and Cursor manifests to one
// plugin. A version is refused because Claude Code then pins installs to
// it until someone remembers to bump it; without one, they track main.
func TestPluginManifestsAgree(t *testing.T) {
	var claude, cursor pluginManifest
	readJSON(t, ".claude-plugin/plugin.json", &claude)
	readJSON(t, ".cursor-plugin/plugin.json", &cursor)

	var market struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSON(t, ".claude-plugin/marketplace.json", &market)

	if claude.Name != "pgedge" || cursor.Name != "pgedge" {
		t.Errorf("plugin names are %q (Claude) and %q (Cursor), want pgedge; "+
			"a published name must never change", claude.Name, cursor.Name)
	}
	if claude.Description == "" || claude.Description != cursor.Description {
		t.Errorf("descriptions differ or are empty:\nClaude: %q\nCursor: %q",
			claude.Description, cursor.Description)
	}
	if claude.Version != "" || cursor.Version != "" {
		t.Errorf("a manifest sets version (%q, %q)", claude.Version, cursor.Version)
	}
	if len(market.Plugins) != 1 || market.Plugins[0].Name != claude.Name ||
		market.Plugins[0].Source != "./" {
		t.Errorf("marketplace.json plugins = %+v, want one %q entry with source ./",
			market.Plugins, claude.Name)
	}
}
