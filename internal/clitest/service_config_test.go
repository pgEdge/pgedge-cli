package clitest

import (
	"regexp"
	"strings"
	"testing"

	cpcmd "github.com/pgEdge/pgedge-cli/internal/controlplane/cmd"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/svccfg"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// This file is issue #124's conformance gate: it ties `pgedge controlplane
// database init`'s blank template text to internal/controlplane/svccfg's
// hand-mirrored Control Plane key sets, so the two cannot silently
// drift apart. See internal/controlplane/svccfg's package doc comment for why
// the mirror exists (GATE, not generate) and
// internal/controlplane/cmd/database_spec_template.go's serviceConfigGuidance for
// the single-sourced per-type text this gate reads — the blank template
// and every populated service block render it from the same source, so
// gating the blank template gates both (held in place by
// TestServiceConfigGuidanceIsSingleSourced in internal/controlplane/cmd).
//
// Known blind spot, stated so it is a decision rather than an
// oversight: keyLineRe below is line-anchored, so a key named only in
// prose — a trailing inline aside, or a sentence inside a comment — is
// invisible to both gates. That makes this gate a constraint on the
// template's FORM as well as its content: every key the template means
// to document must sit at the start of its own line, in `key: value`
// shape. A key mentioned only in an aside is neither checked for
// validity nor counted as documenting a required key.
//
// Both directions are gated:
//   - TestInitTemplateNamesOnlyKnownServiceKeys: the template must
//     never invent a config key CP does not have (mutations M1, M2 in
//     the implementation plan).
//   - TestInitTemplateDocumentsEveryRequiredKey: the template must
//     document every key CP requires (mutation M3). Optional keys are
//     deliberately NOT required to all appear — the template's mcp
//     block, for instance, only shows one of the seven disable_*
//     toggles as a representative example. That is a documentation
//     decision (showing every optional key would make the template
//     unreadable), not something this gate polices.

// blankDatabaseInitTemplate runs `pgedge controlplane database init` with the
// default node count and returns the emitted YAML. --base-url is a
// non-routable placeholder: init makes no network call.
func blankDatabaseInitTemplate(t *testing.T) string {
	t.Helper()
	rt, stdout, _ := testsupport.NewRuntime(t, "", "text")
	cmd := cpcmd.NewControlplaneCmd(rt)
	cmd.SetArgs([]string{
		"database", "init", "--base-url", "http://127.0.0.1:0",
	})
	cmd.SetOut(stdout)
	cmd.SetErr(stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pgedge controlplane database init: %v", err)
	}
	return stdout.String()
}

// servicesStubStart/End bound the "services:" guidance block inside
// the blank template between two other stub headers that buildSpec
// always emits around it (writeRestoreConfig before, writePostgresqlConf
// after). If either header ever moves or is reworded, this test fails
// with a clear "could not locate" message rather than silently
// scanning the wrong text — a deliberate brittleness, since the
// alternative is a gate that can pass while looking at nothing.
const (
	servicesStubStart = "# services: extra services to run alongside Postgres."
	servicesStubEnd   = "# postgresql_conf: cluster-wide postgresql.conf settings"
)

// extractServicesStub returns the substring of tmpl holding the blank
// template's services guidance, bounded so later stub sections
// (postgresql_conf, pg_hba_conf, per-node overrides) can never leak
// into a service block's extracted key set.
func extractServicesStub(t *testing.T, tmpl string) string {
	t.Helper()
	start := strings.Index(tmpl, servicesStubStart)
	end := strings.Index(tmpl, servicesStubEnd)
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("could not locate the services stub in the template "+
			"(start=%d end=%d) -- did writeServices or "+
			"writePostgresqlConf's header text change? Update this "+
			"test's markers alongside it:\n%s", start, end, tmpl)
	}
	return tmpl[start:end]
}

// serviceBlock is one "- service_type: X" example inside the services
// stub, with its raw (still commented) lines.
type serviceBlock struct {
	serviceType string
	lines       []string
}

// serviceTypeMarkerRe matches a stripped line starting a new example,
// e.g. "- service_type: mcp". Only the type name is captured, so a
// trailing inline comment on that line would be ignored.
var serviceTypeMarkerRe = regexp.MustCompile(
	`^-\s*service_type:\s*([a-z]+)`)

// stripCommentPrefix removes every leading '#' (and surrounding
// whitespace) from a template line, so "#     config:" and "config:"
// scan identically. Template comment lines may nest a second '#' for
// an inline aside (e.g. "anthropic_api_key: CHANGE-ME   # required for
// provider anthropic") -- only LEADING '#'s are stripped, so that
// trailing aside stays part of the line and is simply ignored by the
// key-token regex below, which anchors at the start of the line.
func stripCommentPrefix(line string) string {
	s := strings.TrimLeft(line, " \t")
	for strings.HasPrefix(s, "#") {
		s = strings.TrimPrefix(s, "#")
		s = strings.TrimLeft(s, " \t")
	}
	return s
}

// splitServiceBlocks partitions a services stub into one serviceBlock
// per "- service_type: X" example.
func splitServiceBlocks(t *testing.T, stub string) []serviceBlock {
	t.Helper()
	var blocks []serviceBlock
	var cur *serviceBlock
	for _, line := range strings.Split(stub, "\n") {
		stripped := stripCommentPrefix(line)
		if m := serviceTypeMarkerRe.FindStringSubmatch(stripped); m != nil {
			if cur != nil {
				blocks = append(blocks, *cur)
			}
			cur = &serviceBlock{serviceType: m[1]}
			continue
		}
		if cur != nil {
			cur.lines = append(cur.lines, line)
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	if len(blocks) != 3 {
		t.Fatalf("services stub has %d service_type examples, want 3 "+
			"(mcp, rag, postgrest):\n%s", len(blocks), stub)
	}
	return blocks
}

// keyLineRe matches a "key:" token anchored at the start of an
// (already comment-stripped) line, with an optional YAML list-item
// dash before it -- "- name: docs" and "config:" both match, but a
// value containing a colon later in the line (e.g.
// server_cors_allowed_origins: "https://app.example.com") does not
// produce a second, spurious match, since the search is anchored to
// the start of the line rather than run globally over it.
var keyLineRe = regexp.MustCompile(`^-?\s*([a-z][a-z0-9_]*):`)

// serviceIdentityKeys are ServiceSpec2's own top-level fields, common
// to every service type and never part of a service's `config` key
// set -- internal/controlplane/svccfg only mirrors what CP validates inside
// config, so these must be excluded before checking tokens against it.
var serviceIdentityKeys = map[string]bool{
	"service_type": true,
	"service_id":   true,
	"connect_as":   true,
	"host_ids":     true,
	"version":      true,
	"config":       true,
}

// extractConfigKeyTokens returns every "key:" token found in a
// service block's lines, excluding ServiceSpec2's own identity fields.
func extractConfigKeyTokens(lines []string) []string {
	var tokens []string
	for _, line := range lines {
		stripped := stripCommentPrefix(line)
		m := keyLineRe.FindStringSubmatch(stripped)
		if m == nil {
			continue
		}
		if serviceIdentityKeys[m[1]] {
			continue
		}
		tokens = append(tokens, m[1])
	}
	return tokens
}

// TestInitTemplateNamesOnlyKnownServiceKeys is the forward direction:
// every config key token the blank template names, for every service
// type, must be a key internal/controlplane/svccfg says CP actually accepts.
// This is what catches the #109 defect class: the template inventing
// a key like llm_api_key that CP rejects outright.
func TestInitTemplateNamesOnlyKnownServiceKeys(t *testing.T) {
	tmpl := blankDatabaseInitTemplate(t)
	stub := extractServicesStub(t, tmpl)
	for _, blk := range splitServiceBlocks(t, stub) {
		known := svccfg.KnownKeyNames(blk.serviceType)
		if len(known) == 0 {
			t.Fatalf("services stub names unrecognised service_type %q",
				blk.serviceType)
		}
		for _, tok := range extractConfigKeyTokens(blk.lines) {
			if !known[tok] {
				t.Errorf("template's %s example names config key %q, "+
					"which is not in internal/controlplane/svccfg's mirror of "+
					"Control Plane's known keys for that service type",
					blk.serviceType, tok)
			}
		}
	}
}

// TestInitTemplateDocumentsEveryRequiredKey is the reverse direction:
// every key internal/controlplane/svccfg marks Required (unconditionally, or
// conditionally via ConditionalOn) for a service type must appear
// somewhere in that type's example block. Optional keys deliberately
// need not all appear -- see this file's header comment.
func TestInitTemplateDocumentsEveryRequiredKey(t *testing.T) {
	tmpl := blankDatabaseInitTemplate(t)
	stub := extractServicesStub(t, tmpl)
	for _, blk := range splitServiceBlocks(t, stub) {
		got := map[string]bool{}
		for _, tok := range extractConfigKeyTokens(blk.lines) {
			got[tok] = true
		}
		for _, req := range svccfg.RequiredKeyNames(blk.serviceType) {
			if !got[req] {
				t.Errorf("template's %s example never documents "+
					"required key %q", blk.serviceType, req)
			}
		}
	}
}

// TestInitTemplateHasThreeServiceExamples is a positive control for
// the two gates above: it fails loudly if the blank template ever
// stops showing all three service types (rather than the two tests
// above silently checking zero blocks and passing vacuously — see
// splitServiceBlocks' own len(blocks) != 3 guard, asserted here too so
// a reader of this file sees the invariant stated directly).
func TestInitTemplateHasThreeServiceExamples(t *testing.T) {
	tmpl := blankDatabaseInitTemplate(t)
	stub := extractServicesStub(t, tmpl)
	blocks := splitServiceBlocks(t, stub)
	seen := map[string]bool{}
	for _, b := range blocks {
		seen[b.serviceType] = true
	}
	for _, want := range []string{"mcp", "rag", "postgrest"} {
		if !seen[want] {
			t.Errorf("services stub is missing a %s example", want)
		}
	}
}
