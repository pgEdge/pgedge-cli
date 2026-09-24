package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// goreleaserConfig is the slice of .goreleaser.yaml this package's
// extraction assumptions rest on. Fields it does not read are ignored
// by the decoder, so a config edit elsewhere never breaks the test.
type goreleaserConfig struct {
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		Binary string `yaml:"binary"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate    string   `yaml:"name_template"`
		WrapInDirectory any      `yaml:"wrap_in_directory"`
		Formats         []string `yaml:"formats"`
		FormatOverrides []struct {
			GOOS    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
	Signs []struct {
		Signature string `yaml:"signature"`
		Artifacts string `yaml:"artifacts"`
		Output    bool   `yaml:"output"`
	} `yaml:"signs"`
}

// loadGoreleaserConfig decodes the repo's .goreleaser.yaml, found
// relative to this package's directory.
func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode .goreleaser.yaml: %v", err)
	}
	if len(cfg.Builds) != 1 || len(cfg.Archives) != 1 {
		t.Fatalf(".goreleaser.yaml has %d builds and %d archives; "+
			"this test assumes exactly one of each",
			len(cfg.Builds), len(cfg.Archives))
	}
	return cfg
}

// TestExtractBinaryMemberNameMatchesGoreleaser binds ExtractBinary's
// exact-member-name assumption to the release config that lays the
// archives out. Two edits there would break `pgedge self update` for
// every user with no other CI signal — surfacing only as "archive
// does not contain the pgedge binary" after a release shipped:
// renaming the build's binary, or setting wrap_in_directory so the
// member becomes "<dir>/pgedge".
func TestExtractBinaryMemberNameMatchesGoreleaser(t *testing.T) {
	cfg := loadGoreleaserConfig(t)

	if got := cfg.Builds[0].Binary; got != unixBinaryName {
		t.Errorf("builds[0].binary = %q; ExtractBinary extracts only %q",
			got, unixBinaryName)
	}
	// goreleaser appends .exe to the Windows build on its own, so the
	// zip member is derived, not configured; the check is that the
	// two constants agree with the one configured stem.
	if windowsBinaryName != cfg.Builds[0].Binary+".exe" {
		t.Errorf("windowsBinaryName = %q, want %q",
			windowsBinaryName, cfg.Builds[0].Binary+".exe")
	}

	switch wrap := cfg.Archives[0].WrapInDirectory; wrap {
	case nil, false, "false", "":
	default:
		t.Errorf("archives[0].wrap_in_directory = %v; ExtractBinary "+
			"only accepts the binary at the archive root", wrap)
	}
}

// TestSignatureAssetNamesMatchGoreleaser binds the two asset names
// self update downloads to the names the release publishes. A rename
// on either side passes every other test and surfaces only as a 404
// on the first real update.
func TestSignatureAssetNamesMatchGoreleaser(t *testing.T) {
	cfg := loadGoreleaserConfig(t)

	if got := cfg.Checksum.NameTemplate; got != ChecksumsAsset {
		t.Errorf("checksum.name_template = %q, want %q", got, ChecksumsAsset)
	}

	if len(cfg.Signs) != 1 {
		t.Fatalf(".goreleaser.yaml has %d signs entries, want 1", len(cfg.Signs))
	}

	sign := cfg.Signs[0]
	if sign.Artifacts != "checksum" || !sign.Output {
		t.Errorf("signs[0] artifacts=%q output=%v, want checksum and true",
			sign.Artifacts, sign.Output)
	}

	got := strings.ReplaceAll(sign.Signature, "${artifact}", ChecksumsAsset)
	if got != BundleAsset {
		t.Errorf("signs[0].signature publishes %q, self update reads %q",
			got, BundleAsset)
	}
}
