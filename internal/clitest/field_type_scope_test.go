package clitest

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// --- 5. the repository field/type-scope check --------------------------------
//
// #507: TestSkillDocsFieldClaimsMatchStructs (field_claims_test.go) checks
// that a claimed field NAME exists somewhere in a module's struct scope;
// it has no notion of WHICH repository type (s3, gcs, azure, posix,
// cifs) a field belongs to. Swapping s3_bucket and base_path between
// their type groups in docs/controlplane/backup-restore.md passed every
// existing gate, because both are real BackupRepositorySpec fields —
// only their pairing with a type was wrong. That swap is the shape of
// the real defect #504's review caught: base_path was described as
// posix/cifs-exclusive when the spec says it is REQUIRED for those two
// types, not restricted to them.
//
// THIS IS A NARROW GATE, NOT THE GENERAL ONE. field_claims_test.go's
// package comment already names the fuller version as future work: bind
// every claim to the one command whose section it sits in, via an AST
// walk from RunE to the response model. That would let a per-type
// scoping check ride the same anchoring. Building that here to close one
// mutation would be the wrong trade. Instead, this covers exactly the
// shape backup-restore.md uses to associate a type with its fields —
// `` `type[, type]` takes `field[, field]` `` — checked against
// BackupRepositorySpec's own OpenAPI descriptions, which already carry
// the scoping in prose ("Only applies when type = 's3'") and need no
// hand-written table. It does not generalize to another spec, another
// struct, or another claim shape, and it does not catch a field wrongly
// claimed as type-exclusive when the spec never restricts it (base_path
// is such a field: nothing here would flag calling it "s3-only", because
// the spec's absence of an "Only applies" clause is not itself a
// positive claim this check can compare against).

// exclusiveTypeRe extracts the one type an "Only applies when type =
// 'X'" field description restricts to. The character class must allow
// digits as well as letters — "s3" is a repository type, not just
// "gcs" and "azure" — and missed that silently on the first pass: the
// two alphabetic types matched, s3_bucket/s3_region/s3_endpoint did
// not, and the truth table came back short by exactly the three fields
// the swapped-fields mutation needs. BackupRepositorySpec's "Required
// for type = 'X' and 'Y'" fields (base_path) do not match this and are
// deliberately left out of the truth table: "required for" is not
// "restricted to", and a table that could not tell the two apart would
// be the false claim #504 shipped, encoded into the gate instead of
// the prose.
var exclusiveTypeRe = regexp.MustCompile(`Only applies when type = '([a-z0-9]+)'`)

// repoTypeNames is BackupRepositorySpec.type's enum. A doc claim
// associates a field with one of these, never with a field name, so
// checkRepositoryTypeScope only treats a backticked token as a type
// when it is one of these five.
var repoTypeNames = map[string]bool{
	"s3": true, "gcs": true, "azure": true, "posix": true, "cifs": true,
}

// repositoryExclusiveFields reads specPath (a vendored OpenAPI document)
// and returns, for every BackupRepositorySpec field whose description
// names exactly one type it applies to, that type.
func repositoryExclusiveFields(t *testing.T, specPath string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading %s: %v", specPath, err)
	}

	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parsing %s: %v", specPath, err)
	}

	schema, ok := spec.Components.Schemas["BackupRepositorySpec"]
	if !ok {
		t.Fatalf("%s carries no BackupRepositorySpec schema — the "+
			"type-scope truth source is gone, or the spec was "+
			"recaptured under a different schema name", specPath)
	}

	exclusive := make(map[string]string)
	for field, prop := range schema.Properties {
		if m := exclusiveTypeRe.FindStringSubmatch(prop.Description); m != nil {
			exclusive[field] = m[1]
		}
	}

	// s3, gcs and azure each contribute at least one exclusive field
	// (eight total, measured against openapi/control-plane.json on
	// 2026-09-22). Fewer means the description wording changed on
	// recapture and this check would silently stop checking anything.
	if len(exclusive) < 5 {
		t.Fatalf("only %d exclusive-type fields found in %s's "+
			"BackupRepositorySpec — the description format changed and "+
			"this check would flag nothing", len(exclusive), specPath)
	}
	return exclusive
}

// typeFieldAssocRe matches this page's one bullet shape for associating
// a repository type with the fields that belong to it: one or more
// backticked, comma/"and"-joined type tokens, then "takes" or "take",
// then the rest of the sentence up to its closing period. See the
// package comment for why the shape stays this narrow.
var typeFieldAssocRe = regexp.MustCompile(
	"((?:`[a-z0-9_]+`[a-z, ]*)+) takes? ([^.]*)\\.")

// backtickedNameRe matches one backticked lowercase token.
var backtickedNameRe = regexp.MustCompile("`([a-z0-9_]+)`")

// checkRepositoryTypeScope returns one violation per doc claim that
// associates a repository type with a field the spec restricts to a
// different type.
func checkRepositoryTypeScope(path, content string,
	exclusive map[string]string) []string {

	var violations []string
	for _, m := range typeFieldAssocRe.FindAllStringSubmatch(content, -1) {
		var types []string
		for _, tok := range backtickedNameRe.FindAllStringSubmatch(m[1], -1) {
			if repoTypeNames[tok[1]] {
				types = append(types, tok[1])
			}
		}
		if len(types) == 0 {
			continue // not a type association; "takes"/"take" is common English
		}

		for _, tok := range backtickedNameRe.FindAllStringSubmatch(m[2], -1) {
			field := tok[1]
			want, ok := exclusive[field]
			if !ok {
				continue
			}
			claimed := false
			for _, typ := range types {
				if typ == want {
					claimed = true
				}
			}
			if !claimed {
				violations = append(violations, fmt.Sprintf(
					"%s: claims %s takes `%s`, but the spec says `%s` "+
						"only applies when type = %q",
					path, strings.Join(types, "/"), field, field, want))
			}
		}
	}
	return violations
}

// TestControlPlaneDocRepositoryFieldsMatchTypeScope runs the type-scope
// check over docs/controlplane/backup-restore.md, the one page that
// uses the associative shape checkRepositoryTypeScope understands.
func TestControlPlaneDocRepositoryFieldsMatchTypeScope(t *testing.T) {
	const path = "../../docs/controlplane/backup-restore.md"

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	exclusive := repositoryExclusiveFields(t, "../../openapi/control-plane.json")
	for _, msg := range checkRepositoryTypeScope(path,
		normalizeWhitespace(string(content)), exclusive) {
		t.Error(msg)
	}
}

// TestRepositoryTypeScopeCheckCatchesSwappedFields is the acceptance
// test: the exact mutation #507 reports, planted directly rather than
// through the doc file. Swapping s3_bucket and base_path between their
// type groups produces "`posix` and `cifs` take `s3_bucket`", and the
// spec restricts s3_bucket to type = 's3'.
func TestRepositoryTypeScopeCheckCatchesSwappedFields(t *testing.T) {
	exclusive := repositoryExclusiveFields(t, "../../openapi/control-plane.json")

	mutated := "- `s3` takes `base_path`, `s3_region` and an optional " +
		"`s3_endpoint`. - `posix` and `cifs` take `s3_bucket`."

	got := checkRepositoryTypeScope("fixture.md",
		normalizeWhitespace(mutated), exclusive)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation for the swapped-fields "+
			"mutation, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "s3_bucket") ||
		!strings.Contains(got[0], `"s3"`) {
		t.Fatalf("violation did not name the swapped field and its real "+
			"type: %v", got[0])
	}
}

// TestRepositoryTypeScopeCheckAcceptsRealAssociations is the negative
// control: the current, correct bullets from backup-restore.md's
// "Backup configuration" section, none of which should be flagged.
func TestRepositoryTypeScopeCheckAcceptsRealAssociations(t *testing.T) {
	exclusive := repositoryExclusiveFields(t, "../../openapi/control-plane.json")

	fixture := "- `s3` takes `s3_bucket`, `s3_region` and an optional " +
		"`s3_endpoint`. - `gcs` takes `gcs_bucket` and an optional " +
		"`gcs_endpoint`. - `azure` takes `azure_account`, " +
		"`azure_container` and an optional `azure_endpoint`. - `posix` " +
		"and `cifs` take `base_path`."

	if got := checkRepositoryTypeScope("fixture.md",
		normalizeWhitespace(fixture), exclusive); len(got) != 0 {
		t.Fatalf("flagged a correct type/field association: %v", got)
	}
}

// TestRepositoryExclusiveFieldsExcludesRequiredFor pins the distinction
// the package comment draws: base_path is "required for" posix and
// cifs, not "only applies when type =", so it must not appear in the
// exclusive-fields truth table. If a future recapture reworded the spec
// to phrase base_path exclusively, this would start failing as a
// prompt to reconsider the check, not as a defect in it.
func TestRepositoryExclusiveFieldsExcludesRequiredFor(t *testing.T) {
	exclusive := repositoryExclusiveFields(t, "../../openapi/control-plane.json")
	if typ, ok := exclusive["base_path"]; ok {
		t.Fatalf("base_path is scoped as exclusive to %q, but the spec "+
			"describes it as required-for, not exclusive-to", typ)
	}
	if exclusive["s3_bucket"] != "s3" {
		t.Fatalf("s3_bucket's exclusive type = %q, want \"s3\"",
			exclusive["s3_bucket"])
	}
}
