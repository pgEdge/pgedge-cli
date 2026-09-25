// Command npmpack stages the npm packages for a release from
// GoReleaser's dist/ output: one package per platform holding that
// platform's binary, and @pgedge/cli, whose launcher finds and runs
// it. npm installs only the platform package whose os and cpu match,
// the layout esbuild, Biome and Supabase use.
//
//	go run ./cmd/npmpack -dist dist -out dist/npm
//
// It also writes out/dist-tag, the tag to publish under: beta for a
// pre-release, so `npm install @pgedge/cli` never resolves to one.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const mainPackage = "@pgedge/cli"

//go:embed launcher.js
var launcher []byte

//go:embed README.npm.md
var readme []byte

// licenceFiles ship in every package, as they do in every archive.
var licenceFiles = []string{"LICENSE.md", "NOTICE.txt", "THIRD_PARTY_LICENSES.txt"}

var npmOS = map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}

var npmCPU = map[string]string{"amd64": "x64", "arm64": "arm64"}

type artifact struct {
	Path   string `json:"path"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Type   string `json:"type"`
}

type binary struct {
	src, os, cpu string
}

func (b binary) pkgName() string { return mainPackage + "-" + b.os + "-" + b.cpu }

func (b binary) dirName() string { return "cli-" + b.os + "-" + b.cpu }

func (b binary) exe() string {
	if b.os == "win32" {
		return "pgedge.exe"
	}
	return "pgedge"
}

type manifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Description          string            `json:"description"`
	License              string            `json:"license"`
	Homepage             string            `json:"homepage"`
	Repository           repository        `json:"repository"`
	Bin                  map[string]string `json:"bin,omitempty"`
	OS                   []string          `json:"os,omitempty"`
	CPU                  []string          `json:"cpu,omitempty"`
	Engines              map[string]string `json:"engines,omitempty"`
	PreferUnplugged      bool              `json:"preferUnplugged,omitempty"`
	OptionalDependencies map[string]string `json:"optionalDependencies,omitempty"`
}

// repository must name this repo: npm provenance rejects a publish
// whose package.json points anywhere other than the building repo.
type repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

func base(name, version, description string) manifest {
	return manifest{
		Name:        name,
		Version:     version,
		Description: description,
		License:     "PostgreSQL",
		Homepage:    "https://github.com/pgEdge/pgedge-cli",
		Repository: repository{
			Type: "git",
			URL:  "git+https://github.com/pgEdge/pgedge-cli.git",
		},
	}
}

func main() {
	dist := flag.String("dist", "dist", "GoReleaser output directory")
	out := flag.String("out", "dist/npm", "directory to stage the packages in")
	root := flag.String("root", ".", "repository root holding the licence files")
	flag.Parse()

	if err := run(*dist, *out, *root); err != nil {
		fmt.Fprintln(os.Stderr, "npmpack:", err)
		os.Exit(1)
	}
}

func run(dist, out, root string) error {
	version, err := readVersion(filepath.Join(dist, "metadata.json"))
	if err != nil {
		return err
	}
	bins, err := readBinaries(filepath.Join(dist, "artifacts.json"), dist)
	if err != nil {
		return err
	}

	optional := map[string]string{}
	for _, b := range bins {
		m := base(b.pkgName(), version,
			fmt.Sprintf("The pgedge binary for %s %s", b.os, b.cpu))
		m.OS = []string{b.os}
		m.CPU = []string{b.cpu}
		// Yarn Plug'n'Play otherwise keeps the package zipped, and a
		// zipped binary cannot be executed.
		m.PreferUnplugged = true
		dir := filepath.Join(out, b.dirName())
		if err := stage(dir, m, root); err != nil {
			return err
		}
		if err := copyFile(b.src, filepath.Join(dir, "bin", b.exe()), 0o755); err != nil {
			return err
		}
		optional[b.pkgName()] = version
	}

	m := base(mainPackage, version, "Unified CLI for the pgEdge product suite")
	m.Bin = map[string]string{"pgedge": "bin/pgedge.js"}
	m.Engines = map[string]string{"node": ">=18"}
	m.OptionalDependencies = optional
	dir := filepath.Join(out, "cli")
	if err := stage(dir, m, root); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "bin", "pgedge.js"), launcher, 0o755); err != nil {
		return err
	}
	tag := distTag(version)
	spec := mainPackage
	if tag != "latest" {
		spec += "@" + tag
	}
	page := strings.ReplaceAll(string(readme), "{{spec}}", spec)
	if err := writeFile(filepath.Join(dir, "README.md"), []byte(page), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(out, "dist-tag"), []byte(tag+"\n"), 0o644)
}

func distTag(version string) string {
	if strings.Contains(version, "-") {
		return "beta"
	}
	return "latest"
}

func readVersion(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is -dist's metadata.json
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if meta.Version == "" {
		return "", fmt.Errorf("%s has no version", path)
	}
	return meta.Version, nil
}

// readBinaries resolves artifact paths against dist's parent, because
// GoReleaser records them relative to the directory it ran in.
func readBinaries(path, dist string) ([]binary, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is -dist's artifacts.json
	if err != nil {
		return nil, err
	}
	var arts []artifact
	if err := json.Unmarshal(raw, &arts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	seen := map[string]bool{}
	var bins []binary
	for _, a := range arts {
		if a.Type != "Binary" {
			continue
		}
		b := binary{
			src: filepath.Join(filepath.Dir(dist), a.Path),
			os:  npmOS[a.GOOS],
			cpu: npmCPU[a.GOARCH],
		}
		if b.os == "" || b.cpu == "" {
			return nil, fmt.Errorf("no npm platform for %s/%s", a.GOOS, a.GOARCH)
		}
		if seen[b.pkgName()] {
			return nil, fmt.Errorf("two binaries for %s", b.pkgName())
		}
		seen[b.pkgName()] = true
		bins = append(bins, b)
	}
	if len(bins) == 0 {
		return nil, fmt.Errorf("%s lists no binaries", path)
	}
	sort.Slice(bins, func(i, j int) bool { return bins[i].pkgName() < bins[j].pkgName() })
	return bins, nil
}

func stage(dir string, m manifest, root string) error {
	// Without SetEscapeHTML(false), ">=18" is written as "\u003e=18".
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "package.json"), buf.Bytes(), 0o644); err != nil {
		return err
	}
	for _, name := range licenceFiles {
		if err := copyFile(filepath.Join(root, name), filepath.Join(dir, name), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // G304: sources come from GoReleaser's artifact list and -root
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // G304: dst is under -out
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
