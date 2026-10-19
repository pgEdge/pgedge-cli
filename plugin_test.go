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
// plugin, pinned to one release: the marketplace installs the skills from
// the tag of that release, so they describe the binary users have. The
// release workflow refuses a tag that doesn't match the version.
func TestPluginManifestsAgree(t *testing.T) {
	var claude, cursor pluginManifest
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &claude)
	readJSON(t, ".cursor-plugin/plugin.json", &cursor)

	var market struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				URL    string `json:"url"`
				Path   string `json:"path"`
				Ref    string `json:"ref"`
			} `json:"source"`
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
	if claude.Version == "" || claude.Version != cursor.Version {
		t.Errorf("versions are %q (Claude) and %q (Cursor), want one release version",
			claude.Version, cursor.Version)
	}
	if len(market.Plugins) != 1 {
		t.Fatalf("marketplace.json has %d plugins, want 1", len(market.Plugins))
	}
	entry := market.Plugins[0]
	if entry.Name != claude.Name || entry.Source.Source != "git-subdir" ||
		entry.Source.URL != "https://github.com/pgEdge/pgedge-cli.git" || entry.Source.Path != pluginDir ||
		entry.Source.Ref != "v"+claude.Version {
		t.Errorf("marketplace.json entry = %+v, want %q from git-subdir "+
			"https://github.com/pgEdge/pgedge-cli.git path %s ref v%s",
			entry, claude.Name, pluginDir, claude.Version)
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
