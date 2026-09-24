package conn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"gopkg.in/yaml.v3"
)

func TestValidateDisplayName(t *testing.T) {
	t.Run("at the limit is accepted", func(t *testing.T) {
		if err := ValidateDisplayName(
			strings.Repeat("a", DisplayNameMaxLen)); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("one over is a usage error", func(t *testing.T) {
		err := ValidateDisplayName(
			strings.Repeat("a", DisplayNameMaxLen+1))
		if err == nil {
			t.Fatal("want an error")
		}
		if got := cli.ExitCode(err); got != cli.ExitUsage {
			t.Errorf("exit = %d, want %d", got, cli.ExitUsage)
		}
		for _, want := range []string{"--display-name", "25", "26"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q does not name %q",
					err.Error(), want)
			}
		}
	})

	// OpenAPI maxLength counts CHARACTERS, not bytes. Counting bytes
	// would refuse a 25-character name the API accepts as soon as one
	// character is non-ASCII, which is the direction that turns a
	// helpful check into a bug report.
	t.Run("counts characters, not bytes", func(t *testing.T) {
		v := strings.Repeat("é", DisplayNameMaxLen)
		if len(v) <= DisplayNameMaxLen {
			t.Fatalf("fixture is not multi-byte: %d bytes", len(v))
		}
		if err := ValidateDisplayName(v); err != nil {
			t.Errorf("err = %v, want nil for %d characters in %d bytes",
				err, DisplayNameMaxLen, len(v))
		}
	})

	// Empty stays legal: it is how the flag is spelled when a caller
	// means "no display name", and rejecting it here would be a
	// separate decision from the length limit.
	t.Run("empty is accepted", func(t *testing.T) {
		if err := ValidateDisplayName(""); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

// TestDisplayNameMaxLenMatchesBothSpecs pins the client-side limit to
// the one the API validates against, in EVERY schema that declares it,
// in BOTH products.
//
// The limit is declared in both vendored specs, and the CLI reads
// it from there. Finding no declaration at all is a
// hard failure rather than a skip: a pin that lost its truth source
// passes against anything, and would let a re-vendor loosen the
// contract while the CLI went on refusing values the API accepts.
func TestDisplayNameMaxLenMatchesBothSpecs(t *testing.T) {
	type schema struct {
		Properties map[string]struct {
			MaxLength *int `yaml:"maxLength"`
		} `yaml:"properties"`
	}

	total := 0
	for _, name := range []string{"managed.yaml", "byoc.yaml"} {
		path := filepath.Join("..", "..", "..", "openapi", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// DECLARED INSIDE THE LOOP, and that is the whole of it.
		// yaml.v3 only allocates a map when the target is nil, so a
		// doc reused across iterations MERGES the second file into the
		// first rather than replacing it. Hoisting this one line out
		// made managed.yaml's three declarations count again under
		// byoc.yaml's name: the total read nine instead of six, the
		// failure message named the wrong file, and byoc.yaml could
		// lose its entire half of the truth source while the
		// per-file guard below still passed on managed's leftovers.
		var doc struct {
			Components struct {
				Schemas map[string]schema `yaml:"schemas"`
			} `yaml:"components"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		found := 0
		for schemaName, s := range doc.Components.Schemas {
			p, ok := s.Properties["display_name"]
			if !ok || p.MaxLength == nil {
				continue
			}
			found++
			if *p.MaxLength != DisplayNameMaxLen {
				t.Errorf("%s: %s.display_name declares maxLength %d, "+
					"CLI limit is %d — the CLI would refuse values "+
					"the API accepts, or accept ones it does not",
					name, schemaName, *p.MaxLength, DisplayNameMaxLen)
			}
		}
		// PER FILE, not in total: one spec losing its declarations is
		// exactly the drift this pin exists to catch, and a combined
		// count lets the other spec cover for it.
		if found == 0 {
			t.Fatalf("%s declares maxLength on no display_name; this "+
				"pin has lost half its truth source and would pass "+
				"against anything for that product", name)
		}
		t.Logf("%s: pinned against %d declarations", name, found)
		total += found
	}
	t.Logf("pinned against %d declarations in total", total)
}
