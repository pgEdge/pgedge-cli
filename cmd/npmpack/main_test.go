package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeDist lays out a GoReleaser dist/ under a temp root: metadata,
// an artifacts list, one fake binary per target, and licence files.
func fakeDist(t *testing.T, version string, targets [][2]string) (root string) {
	t.Helper()
	root = t.TempDir()
	dist := filepath.Join(root, "dist")
	var arts []map[string]string
	for _, tg := range targets {
		name := "pgedge"
		if tg[0] == "windows" {
			name += ".exe"
		}
		rel := filepath.Join("dist", "pgedge_"+tg[0]+"_"+tg[1], name)
		mustWrite(t, filepath.Join(root, rel), "binary "+tg[0]+"/"+tg[1])
		arts = append(arts, map[string]string{
			"path": rel, "goos": tg[0], "goarch": tg[1], "type": "Binary",
		})
	}
	arts = append(arts, map[string]string{"path": "dist/x.tar.gz", "type": "Archive"})
	mustWriteJSON(t, filepath.Join(dist, "artifacts.json"), arts)
	mustWriteJSON(t, filepath.Join(dist, "metadata.json"), map[string]string{"version": version})
	for _, name := range licenceFiles {
		mustWrite(t, filepath.Join(root, name), name)
	}
	return root
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := writeFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(raw))
}

func readManifest(t *testing.T, path string) manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var allTargets = [][2]string{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

func TestRunStagesEveryPlatform(t *testing.T) {
	root := fakeDist(t, "0.5.0", allTargets)
	out := filepath.Join(root, "dist", "npm")
	if err := run(filepath.Join(root, "dist"), out, root); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		dir, name, os, cpu, exe, src string
	}{
		{"cli-linux-x64", "@pgedge/cli-linux-x64", "linux", "x64", "pgedge", "linux/amd64"},
		{"cli-linux-arm64", "@pgedge/cli-linux-arm64", "linux", "arm64", "pgedge", "linux/arm64"},
		{"cli-darwin-x64", "@pgedge/cli-darwin-x64", "darwin", "x64", "pgedge", "darwin/amd64"},
		{"cli-darwin-arm64", "@pgedge/cli-darwin-arm64", "darwin", "arm64", "pgedge", "darwin/arm64"},
		{"cli-win32-x64", "@pgedge/cli-win32-x64", "win32", "x64", "pgedge.exe", "windows/amd64"},
		{"cli-win32-arm64", "@pgedge/cli-win32-arm64", "win32", "arm64", "pgedge.exe", "windows/arm64"},
	}
	wantOptional := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			dir := filepath.Join(out, tt.dir)
			m := readManifest(t, filepath.Join(dir, "package.json"))
			if m.Name != tt.name || m.Version != "0.5.0" {
				t.Errorf("name@version = %s@%s, want %s@0.5.0", m.Name, m.Version, tt.name)
			}
			if !reflect.DeepEqual(m.OS, []string{tt.os}) || !reflect.DeepEqual(m.CPU, []string{tt.cpu}) {
				t.Errorf("os/cpu = %v/%v, want [%s]/[%s]", m.OS, m.CPU, tt.os, tt.cpu)
			}
			if !m.PreferUnplugged {
				t.Error("preferUnplugged is false")
			}
			bin := filepath.Join(dir, "bin", tt.exe)
			got, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "binary "+tt.src {
				t.Errorf("%s holds %q, want the %s binary", bin, got, tt.src)
			}
			if info, _ := os.Stat(bin); info.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s is not executable: %v", bin, info.Mode())
			}
			for _, name := range licenceFiles {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Errorf("missing %s: %v", name, err)
				}
			}
		})
		wantOptional[tt.name] = "0.5.0"
	}

	m := readManifest(t, filepath.Join(out, "cli", "package.json"))
	if m.Name != "@pgedge/cli" || m.Bin["pgedge"] != "bin/pgedge.js" {
		t.Errorf("main package = %s, bin %v", m.Name, m.Bin)
	}
	if !reflect.DeepEqual(m.OptionalDependencies, wantOptional) {
		t.Errorf("optionalDependencies = %v, want %v", m.OptionalDependencies, wantOptional)
	}
	if m.Repository.URL != "git+https://github.com/pgEdge/pgedge-cli.git" {
		t.Errorf("repository.url = %q", m.Repository.URL)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "cli", "bin", "pgedge.js")); string(got) != string(launcher) {
		t.Error("launcher not staged verbatim")
	}
}

func TestRunDistTag(t *testing.T) {
	tests := []struct {
		version, tag, spec string
	}{
		{"0.5.0-beta.2", "beta", "npm install -g @pgedge/cli@beta\n"},
		{"1.0.0", "latest", "npm install -g @pgedge/cli\n"},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			root := fakeDist(t, tt.version, allTargets[:1])
			out := filepath.Join(root, "out")
			if err := run(filepath.Join(root, "dist"), out, root); err != nil {
				t.Fatal(err)
			}
			tag, _ := os.ReadFile(filepath.Join(out, "dist-tag"))
			if string(tag) != tt.tag+"\n" {
				t.Errorf("dist-tag = %q, want %q", tag, tt.tag)
			}
			page, _ := os.ReadFile(filepath.Join(out, "cli", "README.md"))
			if !strings.Contains(string(page), tt.spec) || strings.Contains(string(page), "{{") {
				t.Errorf("README does not carry %q:\n%s", tt.spec, page)
			}
		})
	}
}

func TestRunRefuses(t *testing.T) {
	tests := []struct {
		name    string
		targets [][2]string
		version string
		want    string
	}{
		{"unknown platform", [][2]string{{"freebsd", "amd64"}}, "1.0.0", "no npm platform for freebsd/amd64"},
		{"unknown arch", [][2]string{{"linux", "386"}}, "1.0.0", "no npm platform for linux/386"},
		{"duplicate", [][2]string{{"linux", "amd64"}, {"linux", "amd64"}}, "1.0.0", "two binaries for @pgedge/cli-linux-x64"},
		{"no binaries", nil, "1.0.0", "lists no binaries"},
		{"no version", allTargets, "", "has no version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fakeDist(t, tt.version, tt.targets)
			err := run(filepath.Join(root, "dist"), filepath.Join(root, "out"), root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestRunMissingInputs(t *testing.T) {
	root := fakeDist(t, "1.0.0", allTargets)
	dist := filepath.Join(root, "dist")
	mustWrite(t, filepath.Join(dist, "metadata.json"), "{")
	if err := run(dist, filepath.Join(root, "out"), root); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("bad metadata: run() = %v, want a parse error", err)
	}

	root = fakeDist(t, "1.0.0", allTargets)
	dist = filepath.Join(root, "dist")
	mustWrite(t, filepath.Join(dist, "artifacts.json"), "[")
	if err := run(dist, filepath.Join(root, "out"), root); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("bad artifacts: run() = %v, want a parse error", err)
	}

	if err := run(filepath.Join(t.TempDir(), "dist"), t.TempDir(), root); err == nil {
		t.Error("missing dist: run() = nil, want an error")
	}

	root = fakeDist(t, "1.0.0", allTargets)
	if err := os.Remove(filepath.Join(root, "NOTICE.txt")); err != nil {
		t.Fatal(err)
	}
	if err := run(filepath.Join(root, "dist"), filepath.Join(root, "out"), root); err == nil {
		t.Error("missing licence file: run() = nil, want an error")
	}
}
