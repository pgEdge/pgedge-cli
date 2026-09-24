package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"gopkg.in/yaml.v3"
)

// schemaPropertyEnum reads one component schema property's enum out of
// the vendored managed spec.
//
// The generated enum types carry a Valid() method but no way to
// enumerate their members, so a Valid()-only check cannot notice a new
// member arriving: the CLI would go on refusing, client-side with exit
// 2, a value the API accepts. Reading the spec is the only assertion
// that fails in that direction — which is why an empty result is a
// hard failure here rather than a skip. A test that lost its truth
// source would otherwise pass against anything.
func schemaPropertyEnum(t *testing.T, schema, property string) []string {
	t.Helper()

	path := filepath.Join(
		"..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	s, ok := doc.Components.Schemas[schema]
	if !ok {
		t.Fatalf("no %s schema in %s; this test has lost its truth "+
			"source and would pass against anything", schema, path)
	}
	p, ok := s.Properties[property]
	if !ok {
		t.Fatalf("no %s.%s property in %s; this test has lost its "+
			"truth source", schema, property, path)
	}
	if len(p.Enum) == 0 {
		t.Fatalf("%s.%s carries no enum in %s; this test has lost "+
			"its truth source", schema, property, path)
	}
	return p.Enum
}

// TestMCPEmbeddingProvidersMatchTheSpecEnum pins the managed MCP
// embedding vocabulary to the vendored contract.
//
// The API removed ollama from managed while leaving it on byoc, so the
// two modules' lists are deliberately different and neither can be
// derived from the other. This asserts the managed one against
// managed's own document.
func TestMCPEmbeddingProvidersMatchTheSpecEnum(t *testing.T) {
	spec := schemaPropertyEnum(
		t, "MCPServiceConfig", "embedding_provider")

	sort.Strings(spec)
	got := append([]string(nil), mcpEmbeddingProviders...)
	sort.Strings(got)
	if !slices.Equal(spec, got) {
		t.Errorf("mcpEmbeddingProviders = %v, spec enum = %v — teach "+
			"mcpEmbeddingProviders the new provider (and check "+
			"whether it needs a credential flag of its own)",
			got, spec)
	}
	if slices.Contains(got, "ollama") {
		t.Error("ollama is back in the managed vocabulary; the API " +
			"rejects it there — check the spec before " +
			"restoring the flag")
	}
}

// backupLimitDefault reads the declared default for listBackups'
// `limit` out of the vendored managed spec. `/managed/v1/backups` is
// the only endpoint in any of the three Starfleet specs that declares a
// PAGING default, so there is nothing to generalise here yet. The only
// other declared defaults are `max_lines: 100` on the two logs
// endpoints, which cap one response rather than paging a collection —
// they are what a future generalisation would reach for next.
//
// A missing default is a hard failure, not a zero: the API removing the
// declaration would leave this test passing against anything.
func backupLimitDefault(t *testing.T) int {
	t.Helper()

	path := filepath.Join(
		"..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// Default is decoded as `any`, not `int`: the same parameter list
	// carries `descending`, whose default is a bool, and a typed field
	// would fail to parse the document rather than ignore it.
	var doc struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name   string `yaml:"name"`
					Schema struct {
						Default any `yaml:"default"`
					} `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, p := range doc.Paths["/managed/v1/backups"].Get.Parameters {
		if p.Name != "limit" {
			continue
		}
		n, ok := p.Schema.Default.(int)
		if !ok {
			t.Fatalf("listBackups' limit declares default %v (%T) in "+
				"%s, not an int; this test has lost its truth source",
				p.Schema.Default, p.Schema.Default, path)
		}
		return n
	}
	t.Fatalf("no limit parameter on /managed/v1/backups in %s; this "+
		"test has lost its truth source", path)
	return 0
}

// TestBackupPagingProseMatchesTheSpec pins the documented page size to
// the contract, in both places that state it.
//
// The API moved the default from 10 to 100 — and 100 is also the
// maximum, which inverts the advice. Both the cobra help and the
// reference said "10 rows unless --limit asks for more", so a
// server-side default change silently made the CLI's own documentation
// the opposite of true. Nothing in the build noticed: the default never
// reaches generated Go, so the compiler cannot see it, and `make
// docs-check` compares the reference against cobra's Short rather than
// against the spec.
func TestBackupPagingProseMatchesTheSpec(t *testing.T) {
	def := strconv.Itoa(backupLimitDefault(t))

	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	long := newBackupListCmd(rt).Long

	// The search is scoped to the backup list section, and the assertion
	// is a PHRASE rather than the bare number. Both matter: a first
	// version of this test looked for "100" anywhere in llms.txt, and
	// deleting the entire page-default paragraph left every gate green —
	// `1-10000` and "100x low" elsewhere in the file satisfied it. Even
	// its positive control was inert, because `--max-rows 500` matches a
	// mutated default of 50.
	ref := managedReferenceSection(t, "#### pgedge starfleet managed backup list")

	// Written as "<n> rows by default" in both texts so one substring
	// pins both, and so the number cannot be satisfied by an unrelated
	// figure that happens to share its digits.
	//
	// That is the SAME phrase TestManagedPagingProseMatchesPageDefaults
	// requires, deliberately: this test derives the number from
	// managed.yaml and that one from cli.PageDefaults, so one phrase
	// makes the two a cross-check. If the spec's declared default and
	// the measured page ever disagree, one of the two reddens rather
	// than both quietly reading a sentence of their own.
	want := def + " rows by default"

	for name, text := range map[string]string{
		"backup list --help":     long,
		"llms.txt (backup list)": ref,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not say %q; the prose and the contract "+
				"have diverged", name, want)
		}
		// The phrase the default change falsified. With the default
		// equal to the maximum, --limit cannot widen anything.
		if strings.Contains(text, "asks for more") {
			t.Errorf("%s still says --limit \"asks for more\", but the "+
				"default (%s) is the maximum — it can only narrow",
				name, def)
		}
	}
}

// TestManagedReferenceSectionSkipsGeneratedBlocks pins the helper the
// two prose gates depend on, using a command whose generated and
// hand-written headings sit at the SAME level.
//
// `database list` is one of the 21 such pairs (backup list is one of the
// 14 where the generated heading is deeper). Both earlier versions of
// the helper would have returned the generated block here: the first
// because an unanchored `#### x` matches inside `##### x`, the second
// because "take the deeper heading" does not apply. `**Usage:**` is the
// discriminator — docgen emits it, prose never does.
func TestManagedReferenceSectionSkipsGeneratedBlocks(t *testing.T) {
	const heading = "##### pgedge starfleet managed database list"

	got := managedReferenceSection(t, heading)

	if strings.Contains(got, "**Usage:**") {
		t.Errorf("section for %q is the GENERATED block, not the "+
			"hand-written prose; the gates that use this helper would "+
			"be asserting against docgen output\n%s", heading, got)
	}
	if !strings.Contains(got, "--region") {
		t.Errorf("section for %q does not look like the hand-written "+
			"prose (no --region mention); got:\n%s", heading, got)
	}
}

// generatedBlock matches one BEGIN/END GENERATED region in a reference.
var generatedBlock = regexp.MustCompile(
	`(?s)<!-- BEGIN GENERATED:.*?<!-- END GENERATED -->`)

// headingLine matches a Markdown ATX heading at the start of a line,
// capturing its hashes so a section can be terminated by any heading at
// the same level or shallower.
var headingLine = regexp.MustCompile(`(?m)^(#{1,6}) `)

// managedReferenceSection returns the HAND-WRITTEN section of the managed
// reference under the given heading, up to the next heading at the same
// level or shallower.
//
// Scoping matters more than it looks. The reference (index + pages) runs
// well past 1600 lines, so a whole-corpus substring search finds almost
// any short string somewhere, and a test that "passes" on an unrelated
// line pins nothing. An absent heading is a hard failure for the same
// reason.
//
// Generated blocks are REMOVED before the heading is located, which is
// the part that took two attempts to get right. Every command appears
// twice in its own page — once inside a `BEGIN GENERATED` block and once
// as hand-written prose — under headings whose levels are inconsistent:
// most pairs sit at the SAME level (the whole database subtree does),
// while some, backup list among them, have the generated one a level
// deeper. So neither "search for the deeper heading" nor "take the
// second match" is safe, and an earlier version of this helper relied on
// exactly that, silently scoping the assertion to a block that can never
// hold prose. Deleting the generated regions first is level-agnostic:
// what remains is only prose a human wrote.
func managedReferenceSection(t *testing.T, heading string) string {
	t.Helper()

	text := generatedBlock.ReplaceAllString(managedReferenceText(t), "")

	anchored := "\n" + heading + "\n"
	start := strings.Index(text, anchored)
	if start < 0 {
		t.Fatalf("no hand-written %q heading on a line of its own in "+
			"the module reference (index + pages); this test has lost "+
			"its truth source and would pass against anything", heading)
	}
	rest := text[start+len(anchored):]

	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	for _, m := range headingLine.FindAllStringSubmatchIndex(rest, -1) {
		if len(m[2:4]) == 2 && m[3]-m[2] <= level {
			return rest[:m[0]]
		}
	}
	return rest
}

// TestPgVersionsMatchTheSpecEnum pins --pg-version's vocabulary to the
// vendored contract. The API validates the value against the majors its
// Postgres image catalog publishes, and a new major reaching the spec
// must reach the flag rather than being refused locally.
func TestPgVersionsMatchTheSpecEnum(t *testing.T) {
	spec := schemaPropertyEnum(
		t, "CreateManagedDatabaseInput", "pg_version")

	sort.Strings(spec)
	got := append([]string(nil), pgVersions...)
	sort.Strings(got)
	if !slices.Equal(spec, got) {
		t.Errorf("pgVersions = %v, spec enum = %v — teach pgVersions "+
			"the new major", got, spec)
	}
}

// queryParameterEnum reads one path operation's query-parameter enum
// out of the vendored managed spec.
//
// The same argument as schemaPropertyEnum applies, one level over: the
// generated GetManagedDatabaseParamsUserType has Valid() but cannot
// enumerate itself, so only the spec can fail in the direction of a
// value the API accepts and the CLI refuses. An empty result is a hard
// failure rather than a skip.
func queryParameterEnum(
	t *testing.T, path, parameter string,
) []string {
	t.Helper()

	specPath := filepath.Join(
		"..", "..", "..", "..", "openapi", "managed.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var doc struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name   string `yaml:"name"`
					In     string `yaml:"in"`
					Schema struct {
						Enum []string `yaml:"enum"`
					} `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", specPath, err)
	}
	item, ok := doc.Paths[path]
	if !ok {
		t.Fatalf("no %s path in %s; this test has lost its truth "+
			"source and would pass against anything", path, specPath)
	}
	for _, p := range item.Get.Parameters {
		if p.Name != parameter || p.In != "query" {
			continue
		}
		if len(p.Schema.Enum) == 0 {
			t.Fatalf("%s's %s query parameter carries no enum in %s; "+
				"this test has lost its truth source",
				path, parameter, specPath)
		}
		return p.Schema.Enum
	}
	t.Fatalf("no %s query parameter on GET %s in %s; this test has "+
		"lost its truth source", parameter, path, specPath)
	return nil
}

// TestUserTypesMatchTheSpecEnum pins --user-type's WIRE vocabulary to
// the vendored contract.
//
// The API narrowed this parameter from three values to two while
// leaving rotate-password's role_name at three. Nothing in the build
// noticed until this test existed: the CLI went on sending
// application_read_only, and the server answered 400.
func TestUserTypesMatchTheSpecEnum(t *testing.T) {
	spec := queryParameterEnum(
		t, "/managed/v1/databases/{id}", "user_type")

	got := make([]string, 0, len(userTypeAliases))
	for _, wire := range userTypeAliases {
		if !slices.Contains(got, string(wire)) {
			got = append(got, string(wire))
		}
	}

	sort.Strings(spec)
	sort.Strings(got)
	if !slices.Equal(spec, got) {
		t.Errorf("--user-type sends %v, spec enum = %v — teach "+
			"userTypeAliases the change (and check whether "+
			"rotate-password's --role moved with it; the two "+
			"vocabularies are separate)", got, spec)
	}
}

// TestBranchUserTypesMatchTheSpecEnum is the same pin for branch get,
// whose user_type is a separate parameter in the contract.
func TestBranchUserTypesMatchTheSpecEnum(t *testing.T) {
	spec := queryParameterEnum(t,
		"/managed/v1/databases/{id}/branches/{branch_id}", "user_type")

	got := make([]string, 0, len(branchUserTypeAliases))
	for _, wire := range branchUserTypeAliases {
		if !slices.Contains(got, string(wire)) {
			got = append(got, string(wire))
		}
	}

	sort.Strings(spec)
	sort.Strings(got)
	if !slices.Equal(spec, got) {
		t.Errorf("branch get --user-type sends %v, spec enum = %v",
			got, spec)
	}
}

// TestVocabularyChecksRunBeforeCredentials pins the ORDERING, which is
// the whole value of a client-side vocabulary check and is invisible
// to a test that supplies credentials.
//
// Both checks were first written after clientFromCmd. Everything
// still passed — the value was refused, the exit code was 2, no write
// was sent — because every other test in this package runs through
// runAuthed, which supplies --client-id and --client-secret. The
// defect only shows with no credentials configured: credential
// resolution fails first and the user gets exit 5 "no credentials
// found" for a misspelled flag value, and the reference's "before any
// request" claim is false.
//
// runManaged is deliberate here. Unlike runAuthed it appends nothing,
// so the command runs exactly as it would for a user with no profile.
func TestVocabularyChecksRunBeforeCredentials(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"pg-version", []string{
			"database", "create", "--name", "mydb",
			"--region", "us-east-1", "--size", "small",
			"--pg-version", "15"}},
		{"embedding-provider", []string{
			"database", "mcp", "deploy", testDatabaseID,
			"--embedding-provider", "ollama"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")

			err := runManaged(t, rt, out, tc.args...)
			if err == nil {
				t.Fatal("the bad value was accepted")
			}
			var ee *ExitError
			if !asExitError(err, &ee) {
				t.Fatalf("not an ExitError: %v", err)
			}
			if ee.Code() != ExitUsage {
				t.Fatalf("exit %d, want %d (ExitUsage) — the check "+
					"has moved after clientFromCmd, so a malformed "+
					"value now reports a credential failure: %v",
					ee.Code(), ExitUsage, err)
			}
		})
	}
}
