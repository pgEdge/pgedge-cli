package pgedgecli

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"testing"
)

const pluginDir = "plugins/pgedge"

type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Icon        string `json:"icon"`
	Logo        string `json:"logo"`
	Skills      string `json:"skills"`
}

func readJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}

func mustExist(t *testing.T, what, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("%s %q: %v", what, p, err)
	}
}

// TestPluginManifestsAgree holds the Claude and Cursor manifests to one
// plugin. A version is refused because Claude Code then pins installs to
// it until someone remembers to bump it; without one, they track main.
func TestPluginManifestsAgree(t *testing.T) {
	var claude, cursor pluginManifest
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &claude)
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
		market.Plugins[0].Source != "./"+pluginDir {
		t.Errorf("marketplace.json plugins = %+v, want one %q entry with source ./%s",
			market.Plugins, claude.Name, pluginDir)
	}

	// Claude resolves the icon from the plugin folder, Cursor resolves its
	// paths from the repository root.
	mustExist(t, "Claude icon", filepath.Join(pluginDir, claude.Icon))
	mustExist(t, "Cursor logo", cursor.Logo)
	if path.Clean(cursor.Skills) != pluginDir+"/skills" {
		t.Errorf("Cursor skills = %q, want ./%s/skills", cursor.Skills, pluginDir)
	}
	mustExist(t, "README the Anthropic directory requires",
		filepath.Join(pluginDir, "README.md"))
}
