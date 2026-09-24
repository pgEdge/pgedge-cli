package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/svccfg"
)

// This file closes the hole issue #124's review found in the original
// gate: internal/clitest's conformance tests read only the BLANK
// template's services stub, while the populated (interviewed) path had
// its own separately-maintained copy of the same per-type guidance,
// gated by nothing. The fix was to single-source the text
// (serviceConfigGuidance in database_spec_template.go); these tests
// hold that dedup in place and additionally gate the one part of the
// populated path that is NOT guidance text -- the live config keys an
// interviewed mcp service writes.

// guidancePrefixWidth is the width of the fixed prefix
// writeServiceConfigGuidance puts in front of every guidance line, the
// same in both renderings by construction ("#     " and "    " + "# ").
const guidancePrefixWidth = 6

// normalizeGuidanceLine strips a rendered guidance line back to its
// source form in serviceConfigGuidance: drop the 6-column prefix, then
// drop the extra "# " the blank template's rendering leaves on the
// leading note line (the populated rendering folds that '#' into its
// own prefix). Every other line -- YAML or a nested aside -- is
// unchanged by the TrimPrefix, since it starts with a space.
func normalizeGuidanceLine(t *testing.T, line string) string {
	t.Helper()
	if len(line) < guidancePrefixWidth {
		t.Fatalf("guidance line %q is shorter than the %d-column prefix "+
			"every rendering must add", line, guidancePrefixWidth)
	}
	return strings.TrimPrefix(line[guidancePrefixWidth:], "# ")
}

// blankTemplateGuidance returns the normalized guidance lines the BLANK
// template emits for serviceType: everything after that example's
// "version:" line that still carries the "#     " prefix.
func blankTemplateGuidance(t *testing.T, serviceType string) []string {
	t.Helper()
	tmpl := buildSpec(specValues{databaseName: "db"})
	marker := "#   - service_type: " + serviceType + "\n"
	idx := strings.Index(tmpl, marker)
	if idx < 0 {
		t.Fatalf("blank template has no %s service example:\n%s",
			serviceType, tmpl)
	}
	var out []string
	seenVersion := false
	for _, line := range strings.Split(tmpl[idx:], "\n") {
		if !seenVersion {
			if strings.HasPrefix(line, "#     version:") {
				seenVersion = true
			}
			continue
		}
		if !strings.HasPrefix(line, "#     ") {
			break
		}
		out = append(out, normalizeGuidanceLine(t, line))
	}
	return out
}

// populatedServiceGuidance returns the normalized guidance lines a
// POPULATED service block of serviceType emits -- the path an
// interviewed rag/postgrest service, or an mcp service that configured
// nothing, actually takes.
func populatedServiceGuidance(t *testing.T, serviceType string) []string {
	t.Helper()
	spec := buildSpec(specValues{
		databaseName: "db",
		nodes:        []nodeValue{{name: "n1", hostIDs: []string{"h-1"}}},
		services: []serviceValue{{
			serviceType: serviceType,
			serviceID:   serviceType + "-1",
			connectAs:   "admin",
			hostIDs:     []string{"h-1"},
		}},
	})
	var out []string
	seenVersion := false
	for _, line := range strings.Split(spec, "\n") {
		if !seenVersion {
			if strings.HasPrefix(line, "    version:") {
				seenVersion = true
			}
			continue
		}
		if !strings.HasPrefix(line, "    # ") {
			break
		}
		out = append(out, normalizeGuidanceLine(t, line))
	}
	return out
}

// TestServiceConfigGuidanceIsSingleSourced proves the blank template
// and a populated service block emit the SAME per-type config guidance,
// which is what makes internal/clitest's gate over the blank template
// cover the populated path too. Before the #124 review the two were
// independent copies that had already diverged: the populated mcp copy
// showed 5 of the blank copy's 18 keys, and the populated rag copy
// omitted token_budget, top_n, search, hybrid_enabled and
// vector_weight -- all of it invisible to the gate.
func TestServiceConfigGuidanceIsSingleSourced(t *testing.T) {
	for _, styp := range serviceStubTypes {
		t.Run(styp, func(t *testing.T) {
			blank := blankTemplateGuidance(t, styp)
			populated := populatedServiceGuidance(t, styp)
			// Positive control: a comparison of two empty slices
			// passes while proving nothing.
			if len(blank) < 8 {
				t.Fatalf("extracted only %d guidance lines for %s from "+
					"the blank template -- the extractor is looking at "+
					"the wrong text: %q", len(blank), styp, blank)
			}
			if len(blank) != len(populated) {
				t.Fatalf("%s guidance is %d lines in the blank template "+
					"but %d in a populated block -- the two copies have "+
					"drifted:\nblank:\n%s\npopulated:\n%s", styp,
					len(blank), len(populated),
					strings.Join(blank, "\n"),
					strings.Join(populated, "\n"))
			}
			for i := range blank {
				if blank[i] != populated[i] {
					t.Errorf("%s guidance line %d differs:\n"+
						"  blank:     %q\n  populated: %q",
						styp, i+1, blank[i], populated[i])
				}
			}
		})
	}
}

// TestPopulatedServiceGuidanceNamesOnlyKnownKeys re-runs internal/
// clitest's forward assertion directly over the POPULATED path's
// rendering, so the gate does not depend solely on the two renderings
// staying identical. Every "key:" token the guidance names must be a
// key internal/controlplane/svccfg says Control Plane accepts for that type.
func TestPopulatedServiceGuidanceNamesOnlyKnownKeys(t *testing.T) {
	for _, styp := range serviceStubTypes {
		t.Run(styp, func(t *testing.T) {
			known := svccfg.KnownKeyNames(styp)
			if len(known) == 0 {
				t.Fatalf("svccfg knows no keys for %s", styp)
			}
			tokens := guidanceKeyTokens(populatedServiceGuidance(t, styp))
			if len(tokens) == 0 {
				t.Fatalf("extracted no config key tokens for %s", styp)
			}
			for _, tok := range tokens {
				if !known[tok] {
					t.Errorf("populated %s block's guidance names config "+
						"key %q, which is not in internal/controlplane/svccfg's "+
						"mirror of Control Plane's known keys", styp, tok)
				}
			}
		})
	}
}

// TestPopulatedServiceGuidanceDocumentsEveryRequiredKey is the reverse
// direction over the populated path, matching internal/clitest's
// TestInitTemplateDocumentsEveryRequiredKey. Optional keys deliberately
// need not all appear (the mcp block shows one of seven disable_*
// toggles); required ones must.
func TestPopulatedServiceGuidanceDocumentsEveryRequiredKey(t *testing.T) {
	for _, styp := range serviceStubTypes {
		t.Run(styp, func(t *testing.T) {
			got := map[string]bool{}
			for _, tok := range guidanceKeyTokens(
				populatedServiceGuidance(t, styp)) {
				got[tok] = true
			}
			req := svccfg.RequiredKeyNames(styp)
			if len(req) == 0 {
				t.Fatalf("svccfg reports no required keys for %s", styp)
			}
			for _, r := range req {
				if !got[r] {
					t.Errorf("populated %s block's guidance never "+
						"documents required key %q", styp, r)
				}
			}
		})
	}
}

// guidanceKeyTokens pulls the "key:" token off the start of each
// normalized guidance line, skipping the "config:" wrapper and any line
// that is prose rather than a key. It mirrors internal/clitest's
// keyLineRe: line-anchored, so a key named only inside a trailing aside
// is invisible here too.
func guidanceKeyTokens(lines []string) []string {
	var out []string
	for _, line := range lines {
		s := strings.TrimLeft(line, " ")
		s = strings.TrimPrefix(s, "- ")
		s = strings.TrimLeft(s, " ")
		if strings.HasPrefix(s, "#") {
			continue
		}
		name, _, ok := strings.Cut(s, ":")
		if !ok || name == "" || name == "config" ||
			strings.ContainsAny(name, " \t") {
			continue
		}
		out = append(out, name)
	}
	return out
}

// TestInterviewedMCPConfigNamesOnlyKnownKeys gates the one part of the
// populated path that is not guidance text: the LIVE config keys an
// interviewed mcp service writes. Those come from interviewMCPConfig's
// own string literals (llm_enabled/llm_provider/llm_model, the
// provider-resolved credential key, and init_token), so nothing in the
// guidance-text gates above would catch a typo or a rename there --
// and a key Control Plane does not know is a guaranteed 400.
func TestInterviewedMCPConfigNamesOnlyKnownKeys(t *testing.T) {
	known := svccfg.KnownKeyNames("mcp")
	for _, provider := range []string{"anthropic", "openai", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			// scaffold -> backups? n -> services? y -> mcp -> defaults
			// -> LLM? y -> provider -> model -> init token? y ->
			// another? n -> restore? n -> per-node overrides? n
			in := "db\n1\nn1\nhost-1\nadmin\n\nn\n" +
				"y\nmcp\n\n\nh-1\n\ny\n" + provider +
				"\nclaude-sonnet-4-5\ny\nn\nn\n"
			var errBuf bytes.Buffer
			got, err := runInterview(strings.NewReader(in), &errBuf, 1,
				detectionResult{})
			if err != nil {
				t.Fatalf("runInterview: %v", err)
			}
			spec := decodeSpec(t, got)
			if spec.Services == nil || len(*spec.Services) != 1 {
				t.Fatalf("want 1 service, got %+v", spec.Services)
			}
			cfg := (*spec.Services)[0].Config
			if cfg == nil || len(*cfg) == 0 {
				t.Fatalf("interviewed mcp service wrote no live config; "+
					"this test would then assert nothing:\n%s", got)
			}
			for k := range *cfg {
				if !known[k] {
					t.Errorf("interviewed mcp service writes config key "+
						"%q, which is not in internal/controlplane/svccfg's mirror "+
						"of Control Plane's known mcp keys", k)
				}
			}
			// Positive control that both interview branches ran, so the
			// loop above saw the credential key AND init_token rather
			// than just llm_enabled.
			for _, want := range []string{
				"llm_enabled", "llm_provider", "llm_model", "init_token",
			} {
				if _, ok := (*cfg)[want]; !ok {
					t.Errorf("interviewed config is missing %q, so this "+
						"test is gating less than it claims: %+v",
						want, *cfg)
				}
			}
		})
	}
}
