// Command npmpack stages the npm packages for a release from that
// release's published archives: one package per platform holding that
// platform's binary, and @pgedge/cli, whose launcher finds and runs
// it. npm installs only the platform package whose os and cpu match,
// the layout esbuild, Biome and Supabase use.
//
//	go run ./cmd/npmpack -assets assets -version 0.5.0 -out npm
//
// -assets holds the release's archives and its checksums.txt, whose
// signature the caller has already verified. Every archive is checked
// against it before anything is read from it.
//
// It also writes out/dist-tag, the tag to publish under: beta for a
// pre-release, so `npm install @pgedge/cli` never resolves to one.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/selfupdate"
)

const mainPackage = "@pgedge/cli"

//go:embed launcher.js
var launcher []byte

//go:embed README.npm.md
var readme []byte

// licenceFiles ship in every package, as they do in every archive.
var licenceFiles = []string{"LICENSE.md", "NOTICE.txt", "THIRD_PARTY_LICENSES.txt"}

// platform is one GoReleaser build target and its npm names.
type platform struct {
	goos, goarch, os, cpu string
}

// platforms is .goreleaser.yaml's build matrix. A release missing one
// is refused rather than published without it.
var platforms = []platform{
	{"darwin", "amd64", "darwin", "x64"},
	{"darwin", "arm64", "darwin", "arm64"},
	{"linux", "amd64", "linux", "x64"},
	{"linux", "arm64", "linux", "arm64"},
	{"windows", "amd64", "win32", "x64"},
	{"windows", "arm64", "win32", "arm64"},
}

func (p platform) pkgName() string { return mainPackage + "-" + p.os + "-" + p.cpu }

func (p platform) dirName() string { return "cli-" + p.os + "-" + p.cpu }

func (p platform) exe() string {
	if p.goos == "windows" {
		return "pgedge.exe"
	}
	return "pgedge"
}

func (p platform) archive(version string) string {
	ext := ".tar.gz"
	if p.goos == "windows" {
		ext = ".zip"
	}
	return "pgedge_" + version + "_" + p.goos + "_" + p.goarch + ext
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
	assets := flag.String("assets", "assets", "directory holding the release archives and checksums.txt")
	version := flag.String("version", "", "release version, without the leading v")
	out := flag.String("out", "npm", "directory to stage the packages in")
	flag.Parse()

	if err := run(*assets, *version, *out); err != nil {
		fmt.Fprintln(os.Stderr, "npmpack:", err)
		os.Exit(1)
	}
}

func run(assets, version, out string) error {
	if version == "" || strings.HasPrefix(version, "v") {
		return fmt.Errorf("-version %q must be a version without the leading v", version)
	}
	checksums, err := os.ReadFile(filepath.Join(assets, selfupdate.ChecksumsAsset)) //nolint:gosec // G304: path is under -assets
	if err != nil {
		return err
	}

	optional := map[string]string{}
	for _, p := range platforms {
		name := p.archive(version)
		src := filepath.Join(assets, name)
		if err := selfupdate.VerifyChecksum(src, name, checksums); err != nil {
			return err
		}
		m := base(p.pkgName(), version,
			fmt.Sprintf("The pgedge binary for %s %s", p.os, p.cpu))
		m.OS = []string{p.os}
		m.CPU = []string{p.cpu}
		// Yarn Plug'n'Play otherwise keeps the package zipped, and a
		// zipped binary cannot be executed.
		m.PreferUnplugged = true
		dir := filepath.Join(out, p.dirName())
		if err := writeManifest(dir, m); err != nil {
			return err
		}
		if err := extract(src, p.exe(), dir); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		optional[p.pkgName()] = version
	}

	m := base(mainPackage, version, "Unified CLI for the pgEdge product suite")
	m.Bin = map[string]string{"pgedge": "bin/pgedge.js"}
	m.Engines = map[string]string{"node": ">=18"}
	m.OptionalDependencies = optional
	dir := filepath.Join(out, "cli")
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	// Every archive carries the same licence files; take the first
	// platform package's.
	for _, name := range licenceFiles {
		if err := copyFile(filepath.Join(out, platforms[0].dirName(), name), filepath.Join(dir, name)); err != nil {
			return err
		}
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

// extract writes the archive's binary to dir/bin and its licence files
// to dir, and fails unless it found all of them.
func extract(archive, exe, dir string) error {
	want := map[string]string{exe: filepath.Join(dir, "bin", exe)}
	for _, name := range licenceFiles {
		want[name] = filepath.Join(dir, name)
	}
	put := func(name string, r io.Reader) error {
		dst, ok := want[name]
		if !ok {
			return nil
		}
		delete(want, name)
		body, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if name == exe {
			mode = 0o755
		}
		return writeFile(dst, body, mode)
	}

	var err error
	if strings.HasSuffix(archive, ".zip") {
		err = walkZip(archive, put)
	} else {
		err = walkTarGz(archive, put)
	}
	if err != nil {
		return err
	}
	for name := range want {
		return fmt.Errorf("archive has no %s", name)
	}
	return nil
}

func walkTarGz(path string, put func(string, io.Reader) error) error {
	f, err := os.Open(path) //nolint:gosec // G304: path is under -assets
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if err := put(h.Name, tr); err != nil {
			return err
		}
	}
}

func walkZip(path string, put func(string, io.Reader) error) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close() //nolint:errcheck // read-only
	for _, zf := range zr.File {
		if !zf.Mode().IsRegular() {
			continue
		}
		r, err := zf.Open()
		if err != nil {
			return err
		}
		err = put(zf.Name, r)
		_ = r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeManifest(dir string, m manifest) error {
	// Without SetEscapeHTML(false), ">=18" is written as ">=18".
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "package.json"), buf.Bytes(), 0o644)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode) //nolint:gosec // G703: every path is -out joined with a fixed name; archive entry names only select which
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // G304: src is under -out
	if err != nil {
		return err
	}
	return writeFile(dst, data, 0o644)
}
