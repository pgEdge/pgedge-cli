package clitest

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	cpcmd "github.com/pgEdge/pgedge-cli/internal/controlplane/cmd"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/svccfg"
)

// controlplaneTagFromSource parses the `CP_TAG=` pin out of openapi/SOURCE (repo
// root relative to this package), the file `make vendor-spec` and its
// hand-written re-vendor instructions both point at as the Control
// Plane revision this repo is built against. It fails the test if the
// file is unreadable or the pin is missing — the sync check has
// nothing to compare against otherwise, and a silent skip would let
// the floor and the pin drift with no signal.
func controlplaneTagFromSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "openapi", "SOURCE")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "CP_TAG=") {
			continue
		}
		return strings.TrimPrefix(line, "CP_TAG=")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	t.Fatalf("%s has no CP_TAG= line", path)
	return ""
}

// controlplaneFloorProseRe matches a floor statement in hand-written prose: a
// `>=` followed by a three-component version, optionally "v"-prefixed.
// Every one of the controlplane reference's floor mentions takes that
// shape today ("supports Control Plane >= 0.10.0", "`supported: >=
// 0.10.0`", "supported floor (>= 0.10.0)" twice), which is why the
// pattern can be this narrow: it never sees an unrelated version
// literal, so a future "Postgres 17.2.1" cannot make this gate cry
// wolf.
//
// The narrowness is also why the caller asserts a non-zero match count.
// A rewrite to "supports Control Plane 0.10.0 and later" would drop out
// of this pattern entirely, and a regex-based prose gate that matches
// nothing is indistinguishable from prose that agrees.
var controlplaneFloorProseRe = regexp.MustCompile(`>=\s*v?(\d+\.\d+\.\d+)`)

// controlplaneLLMSProse returns the controlplane reference — its index
// AND every resource page — with every docgen block blanked out. The
// generated blocks derive from the cobra tree, so a floor version
// inside one is already correct by construction and checking it is
// checking the generator against itself; only the hand-written prose
// can drift.
//
// Reading the index alone was right while the module shipped one file.
// The split moved two of the four floor sentences onto llms/doctor.txt
// and llms/version.txt, and an index-only read would have gone on
// checking two of them while reporting green.
func controlplaneLLMSProse(t *testing.T) string {
	t.Helper()
	files := ReferenceFilesFor("../../internal/controlplane")
	if len(files) == 0 {
		t.Fatal("no controlplane reference files — the module " +
			"derivation is broken and this gate has nothing to read")
	}
	var out strings.Builder
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		out.WriteString(stripGeneratedBlocks(string(content)))
		out.WriteString("\n")
	}
	return out.String()
}

// TestControlplaneSupportFloorMatchesSource guards against the CLI's enforced
// support floor (cpcmd.SupportFloor) silently drifting from the two
// places that must agree with it:
//
//   - openapi/SOURCE's CP_TAG pin — the revision the vendored
//     control-plane.json (and so internal/controlplane/api) actually comes from.
//     Bumping one without the other would leave the CLI enforcing a
//     floor that no longer matches what it was built against, in either
//     direction (see internal/controlplane/cmd/floor.go's doc comment on
//     SupportFloor for why this must never happen silently).
//   - the controlplane reference's hand-written prose, which states
//     the floor as a literal version four times — twice in the index,
//     once each on llms/doctor.txt and llms/version.txt. Those
//     sentences are what an agent reads to decide whether a server is
//     supported at all, and they sit OUTSIDE the generated blocks, so
//     nothing regenerates them: `make docs` would leave every one of
//     them stating the old floor after a bump, and the reference would
//     be confidently wrong while every other gate stayed green. This is
//     the half PR #138's merge-gate review flagged as unpinned.
func TestControlplaneSupportFloorMatchesSource(t *testing.T) {
	tag := controlplaneTagFromSource(t)
	wantFloor := strings.TrimPrefix(strings.TrimPrefix(tag, "v"), "V")
	if wantFloor != cpcmd.SupportFloor {
		t.Errorf("openapi/SOURCE CP_TAG=%s (floor %s) but "+
			"cpcmd.SupportFloor = %s — bump both together",
			tag, wantFloor, cpcmd.SupportFloor)
	}

	matches := controlplaneFloorProseRe.FindAllStringSubmatch(controlplaneLLMSProse(t), -1)
	if len(matches) == 0 {
		t.Fatalf("the controlplane reference's hand-written prose no longer "+
			"states the support floor in a `>= <version>` form, so this "+
			"gate has nothing to compare against cpcmd.SupportFloor "+
			"(%s) — restore the wording, or widen controlplaneFloorProseRe to "+
			"whatever shape replaced it", cpcmd.SupportFloor)
	}
	for _, m := range matches {
		if m[1] == cpcmd.SupportFloor {
			continue
		}
		t.Errorf("the controlplane reference states a support floor of %s in "+
			"hand-written prose (%q) but cpcmd.SupportFloor = %s — that "+
			"prose is outside the generated blocks, so `make docs` will "+
			"not fix it; edit the sentence", m[1], m[0],
			cpcmd.SupportFloor)
	}
}

// TestServiceConfigKeysFloorMatchesSupportFloor guards the OTHER half
// of the CP_TAG pin: internal/controlplane/svccfg is a hand-written mirror of
// CP's service-config key sets read at a specific tag (its package doc
// comment states the manual re-read discipline), and svccfg.KeysFloor
// records which tag that was. If cpcmd.SupportFloor moves without
// svccfg being re-read and KeysFloor bumped alongside it, the CLI would
// silently gate `database init` templates against a stale key
// inventory for whatever version gap opened up — modelled on
// TestControlplaneSupportFloorMatchesSource above, which guards the sibling case
// (the vendored OpenAPI spec vs. the enforced floor).
func TestServiceConfigKeysFloorMatchesSupportFloor(t *testing.T) {
	if svccfg.KeysFloor != cpcmd.SupportFloor {
		t.Errorf("svccfg.KeysFloor = %s but cpcmd.SupportFloor = %s — "+
			"re-read internal/controlplane/svccfg's three CP source files at "+
			"the new tag (see its package doc comment), reconcile any "+
			"drift with scripts/cp-service-keys.sh, then bump both",
			svccfg.KeysFloor, cpcmd.SupportFloor)
	}
}
