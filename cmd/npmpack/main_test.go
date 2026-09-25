package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeAssets lays out a release's assets: one archive per platform in
// GoReleaser's layout and a checksums.txt listing them. edit, when not
// nil, may change a platform's archive entries before it is written.
func fakeAssets(t *testing.T, version string, edit func(p platform, entries map[string]string)) string {
	t.Helper()
	dir := t.TempDir()
	var sums strings.Builder
	for _, p := range platforms {
		entries := map[string]string{
			p.exe():                   "binary " + p.goos + "/" + p.goarch,
			"completions/pgedge.bash": "completion",
		}
		for _, name := range licenceFiles {
			entries[name] = name + " " + p.goos
		}
		if edit != nil {
			edit(p, entries)
		}
		var body []byte
		if p.goos == "windows" {
			body = zipOf(t, entries)
		} else {
			body = tarGzOf(t, entries)
		}
		name := p.archive(version)
		mustWrite(t, filepath.Join(dir, name), string(body))
		sum := sha256.Sum256(body)
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
		sums.WriteString(strings.Repeat("0", 64) + "  " + name + ".sbom.json\n")
	}
	mustWrite(t, filepath.Join(dir, "checksums.txt"), sums.String())
	return dir
}

func tarGzOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := writeFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestRunStagesEveryPlatform(t *testing.T) {
	assets := fakeAssets(t, "0.5.0", nil)
	out := filepath.Join(t.TempDir(), "npm")
	if err := run(assets, "0.5.0", out); err != nil {
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
			goos := strings.Split(tt.src, "/")[0]
			for _, name := range licenceFiles {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != name+" "+goos {
					t.Errorf("%s = %q, %v; want the %s archive's copy", name, got, err, goos)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "completions")); err == nil {
				t.Error("completions staged; only the binary and licence files belong")
			}
		})
		wantOptional[tt.name] = "0.5.0"
	}

	dir := filepath.Join(out, "cli")
	m := readManifest(t, filepath.Join(dir, "package.json"))
	if m.Name != "@pgedge/cli" || m.Bin["pgedge"] != "bin/pgedge.js" {
		t.Errorf("main package = %s, bin %v", m.Name, m.Bin)
	}
	if !reflect.DeepEqual(m.OptionalDependencies, wantOptional) {
		t.Errorf("optionalDependencies = %v, want %v", m.OptionalDependencies, wantOptional)
	}
	if m.Repository.URL != "git+https://github.com/pgEdge/pgedge-cli.git" {
		t.Errorf("repository.url = %q", m.Repository.URL)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "bin", "pgedge.js")); string(got) != string(launcher) {
		t.Error("launcher not staged verbatim")
	}
	for _, name := range licenceFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("main package missing %s: %v", name, err)
		}
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
			out := t.TempDir()
			if err := run(fakeAssets(t, tt.version, nil), tt.version, out); err != nil {
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
	linuxArm := platform{"linux", "arm64", "linux", "arm64"}
	winX64 := platform{"windows", "amd64", "win32", "x64"}
	tests := []struct {
		name    string
		version string
		assets  func(t *testing.T) string
		want    string
	}{
		{"leading v", "v1.0.0", func(t *testing.T) string { return fakeAssets(t, "1.0.0", nil) }, "without the leading v"},
		{"no version", "", func(t *testing.T) string { return fakeAssets(t, "1.0.0", nil) }, "without the leading v"},
		{"other version", "1.0.1", func(t *testing.T) string { return fakeAssets(t, "1.0.0", nil) }, "no checksum entry for pgedge_1.0.1_darwin_amd64.tar.gz"},
		{"no checksums.txt", "1.0.0", func(t *testing.T) string {
			dir := fakeAssets(t, "1.0.0", nil)
			if err := os.Remove(filepath.Join(dir, "checksums.txt")); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "checksums.txt"},
		{"missing archive", "1.0.0", func(t *testing.T) string {
			dir := fakeAssets(t, "1.0.0", nil)
			if err := os.Remove(filepath.Join(dir, linuxArm.archive("1.0.0"))); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "pgedge_1.0.0_linux_arm64.tar.gz"},
		{"tampered archive", "1.0.0", func(t *testing.T) string {
			dir := fakeAssets(t, "1.0.0", nil)
			mustWrite(t, filepath.Join(dir, winX64.archive("1.0.0")), "not the archive")
			return dir
		}, "checksum mismatch for pgedge_1.0.0_windows_amd64.zip"},
		{"unlisted archive", "1.0.0", func(t *testing.T) string {
			dir := fakeAssets(t, "1.0.0", nil)
			mustWrite(t, filepath.Join(dir, "checksums.txt"), "")
			return dir
		}, "no checksum entry"},
		{"no binary", "1.0.0", func(t *testing.T) string {
			return fakeAssets(t, "1.0.0", func(p platform, e map[string]string) {
				if p == linuxArm {
					delete(e, "pgedge")
				}
			})
		}, "pgedge_1.0.0_linux_arm64.tar.gz: archive has no pgedge"},
		{"no licence", "1.0.0", func(t *testing.T) string {
			return fakeAssets(t, "1.0.0", func(p platform, e map[string]string) {
				if p == winX64 {
					delete(e, "NOTICE.txt")
				}
			})
		}, "pgedge_1.0.0_windows_amd64.zip: archive has no NOTICE.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.assets(t), tt.version, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestRunRefusesACorruptArchive(t *testing.T) {
	for _, p := range []platform{platforms[0], platforms[len(platforms)-1]} {
		t.Run(p.goos, func(t *testing.T) {
			dir := fakeAssets(t, "1.0.0", nil)
			name := p.archive("1.0.0")
			body := "not an archive"
			mustWrite(t, filepath.Join(dir, name), body)
			sum := sha256.Sum256([]byte(body))
			mustWrite(t, filepath.Join(dir, "checksums.txt"), hexOf(sum)+"  "+name+"\n")
			// Only this platform's archive is listed, so it must be
			// the first one read.
			platformsBefore := platforms
			platforms = []platform{p}
			t.Cleanup(func() { platforms = platformsBefore })
			if err := run(dir, "1.0.0", t.TempDir()); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("run() = %v, want an error naming %s", err, name)
			}
		})
	}
}

func hexOf(sum [32]byte) string { return hex.EncodeToString(sum[:]) }
