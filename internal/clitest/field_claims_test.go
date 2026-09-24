package clitest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// --- 3. the response-shape / field-name check --------------------------------
//
// The two checks above gate a doc's claims about a task envelope and
// about status vocabulary. Neither gates the broader class: a doc that
// names a RESPONSE FIELD the code does not have. That class has
// produced the worst documentation defects found here — most notably a
// doc instructing agents to capture a `task_id` from a BYOC response,
// when no BYOC response carries one.
//
// checkNoEnvelopeClaims closed that ONE case, but it closed it with a
// hardcoded three-string blocklist (noEnvelopeSentinels). That catches
// the field somebody already got wrong; it catches nothing about the
// next invented field. This check is the derived form: every field name
// a doc asserts is checked against the field names the code actually
// declares, so `job_id`, `operation_id` and whatever comes next fail
// the same way task_id would have, without anyone adding a string.
//
// WHY THE TRUTH UNIVERSE HAS THREE SOURCES AND NOT ONE. The obvious
// implementation reads the generated api packages and stops. Measured
// against the real docs, that produces immediate false positives,
// because two other kinds of struct reach a user's terminal:
//
//	generated api json tags   the bulk of it — internal/*/api
//	hand-written CLI structs  `starfleet auth status -o json` renders
//	                          internal/starfleet/account/cmd/auth.go, not
//	                          any generated type
//	config yaml tags          `current_profile` and `base_urls` exist
//	                          only in internal/config
//	                          (there is deliberately NO lowercased-Go-
//	                          field-name source — see collectFieldNames)
//
// WHY ONLY UNDERSCORE-BEARING NAMES. A doc's backticked tokens are
// dominated by things that are not field names: command verbs (`list`,
// `get`), module names (`byoc`), status values (`running`), Go keywords
// (`nil`, `omitempty`), file names and bare English words. Gating all
// of them was measured at 244 hits and is unusable. Requiring an
// underscore removes every one of those populations and keeps exactly
// the shape this project's field names take — `cluster_id`,
// `plan_expires_at`, `text_column`, and the `task_id` this exists to
// catch. It is a deliberately partial gate: a single-word invented
// field (`handle`) passes, and closing that needs the per-command
// binding described at the bottom of this comment, not a wider filter.
//
// WHAT THIS DOES NOT CATCH, STATED SO NOBODY ASSUMES OTHERWISE:
//
//   - a REAL field claimed on the WRONG command. `cluster_id` exists in
//     byoc scope, so an instruction to read it from `ssh-key get`
//     passes. Binding a claim to the one command whose section it sits
//     in is the fuller gate; parseCommandSections in llms_sections.go
//     already solves most of that anchoring problem, and the remaining
//     work is an AST walk from each command's RunE to the
//     `<Op>Response.JSON200` model it renders.
//   - wrong types and wrong nullability.
//   - a single-word invented field, per the underscore note above.
//
// One measured caution for whoever builds the per-command version: the
// nearest-preceding-fence heuristic is essential. A recon pass that
// bound "Capture `cluster_id`" to byoc.Cluster (which has `id`, not
// `cluster_id`) manufactured a confident false defect; the claim
// actually sits under a `database get` fence, and byoc.Database does
// carry cluster_id. A mis-anchored per-command gate invents exactly the
// kind of defect it was built to prevent.

// fieldClaimRe matches a backticked token that looks like a field name:
// lowercase, alphanumeric plus underscore, and containing at least one
// underscore. The backticks are required — an unbackticked word in
// prose is not a claim about a field.
var fieldClaimRe = regexp.MustCompile("`([a-z0-9]+(?:_[a-z0-9]+)+)`")

// jsonKeyClaimRe matches a JSON object key in a fenced block or inline
// prose: `"plan_expires_at":`. Quoted-and-colon is precise enough not
// to need the underscore filter, but the filter is applied anyway so
// this check and the one above share one namespace of allowance names.
var jsonKeyClaimRe = regexp.MustCompile(`"([a-z0-9_]+)"\s*:`)

// --- the truth universe ------------------------------------------------------

// scopeSourceDirs maps a docSection module scope to the packages whose
// structs a doc in that scope may name fields from. The shared entries
// are the ones any doc may reference regardless of module: the root
// CLI's own output structs and the config file's schema.
//
// cloud, byoc and managed partition the pgEdge Starfleet API's struct
// space in overlapping combinations, not into three disjoint sets:
//
//   - "starfleet" gets account + conn — the account-level resources
//     (auth, clients, invites, memberships, tenant) and the shared
//     connection, which is what starfleet's own docs (the former account
//     fold) actually name fields from.
//   - "byoc" additionally gets its own byoc structs and managed's —
//     the two infrastructure sub-trees share a vocabulary and
//     ROADMAP.md discusses both together, so byoc-scoped prose naming
//     a managed field is usually right rather than wrong.
//   - "managed" gets its own managed structs and account's, so
//     managed's OWN docs are checked against managed's structs rather
//     than borrowing byoc's.
//
// cp stays fully separate from all three: it is a different API, with
// its own struct universe and no shared connection.
var scopeSourceDirs = map[string][]string{
	"starfleet": {
		"../starfleet/account", "../starfleet/conn",
		"../cli", "../config", "../output", "../auth", "../module",
	},
	"byoc": {
		"../starfleet/byoc", "../starfleet/account", "../starfleet/managed",
		"../cli", "../config", "../output", "../auth", "../module",
	},
	"managed": {
		"../starfleet/managed", "../starfleet/account",
		"../cli", "../config", "../output", "../auth", "../module",
	},
	"controlplane": {
		"../controlplane",
		"../cli", "../config", "../output", "../auth", "../module",
	},
}

// collectFieldNames walks every .go file under dir and returns every
// name a struct field can be rendered as: its json tag and its yaml tag.
//
// NO LOWERCASED GO FIELD NAME, deliberately, and the reason is worth
// stating because an earlier version had one.
//
// It encoded a false claim. output.Print routes every yaml render
// through jsonShaped, which marshals via JSON first, so `-o yaml` emits
// the JSON tags — never yaml.v3's tagless lowercased fallback. The
// standing decision "a yaml key equals its json key" says the same
// thing, and TestProfileShowYAMLUsesTheSameKeysAsJSON pins
// it live. Prose written against the lowercased spelling would have been
// wrong, and this gate would have blessed it.
//
// It was also DEAD in practice: fieldClaimRe only matches names
// containing an underscore, and no field name in any scanned package
// lowercases to one — measured, 0 of 3064 named struct fields across the
// ten distinct scopeSourceDirs directories. Not a construction
// guarantee: a Go field name MAY legally carry an underscore, and
// golangci-lint does not forbid it here (ST1003 is off), so the claim is
// empirical rather than impossible.
//
// Removing it is safe for that measured reason ALONE, and the direction
// matters: a violation is recorded when a claimed name is NOT in the
// universe, so SHRINKING the universe can only ever produce MORE
// violations. Deleting a truth source is never safe in general — it is
// safe here only because the names this one contributed could not be
// matched by either regex in the first place.
func collectFieldNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", path, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, f := range st.Fields.List {
				// The Go field name contributes nothing: yaml renders
				// through the json tags. See collectFieldNames.
				if f.Tag == nil {
					continue
				}
				tag, uerr := strconv.Unquote(f.Tag.Value)
				if uerr != nil {
					continue
				}
				for _, key := range []string{"json", "yaml"} {
					v := reflectTagValue(tag, key)
					if v == "" || v == "-" {
						continue
					}
					names[strings.ToLower(
						strings.Split(v, ",")[0])] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("collecting field names from %s: %v", dir, err)
	}
	return names
}

// reflectTagValue pulls one key's value out of a struct tag without
// depending on reflect.StructTag, so the parsing stays visible here.
func reflectTagValue(tag, key string) string {
	for _, part := range strings.Fields(tag) {
		if !strings.HasPrefix(part, key+":\"") {
			continue
		}
		v := strings.TrimPrefix(part, key+":\"")
		if i := strings.Index(v, "\""); i >= 0 {
			return v[:i]
		}
	}
	return ""
}

// knownFieldNames builds and caches the truth universe for one scope.
var knownFieldNamesCache = map[string]map[string]bool{}

func knownFieldNames(t *testing.T, module string) map[string]bool {
	t.Helper()
	if cached, ok := knownFieldNamesCache[module]; ok {
		return cached
	}

	dirs, ok := scopeSourceDirs[module]
	if !ok {
		t.Fatalf("no source dirs registered for module scope %q — a new "+
			"docSection module must declare where its field names come "+
			"from, or this check silently flags every claim in it",
			module)
	}

	names := make(map[string]bool)
	for _, dir := range dirs {
		for name := range collectFieldNames(t, dir) {
			names[name] = true
		}
	}

	// A truth universe that came back tiny means the walk found nothing
	// and every claim is about to be reported. Fail loudly instead.
	if len(names) < 100 {
		t.Fatalf("only %d field names collected for scope %q — the "+
			"source walk is broken and this check would flag correct "+
			"docs wholesale", len(names), module)
	}

	knownFieldNamesCache[module] = names
	return names
}

// --- the check ---------------------------------------------------------------

// isKnownStatusValue reports whether name is in any module's verified
// status/state allowlist. It exists only to keep the allowance
// namespaces disjoint: `backing_up` is a real byoc status value AND
// carries an underscore, so without this it would be claimed by both
// checkStatusStateValues and this one, and whichever spent the marker
// first would make the other report it as excusing nothing.
func isKnownStatusValue(name string) bool {
	for _, set := range []map[string]bool{
		byocStatusFieldValues, byocStateFieldValues,
		controlplaneStatusFieldValues, controlplaneStateFieldValues,
		healthCheckValues} {
		if set[name] {
			return true
		}
	}
	return false
}

// ownsFieldNames is the third arm of the allowance partition described
// on annotatedSentence.spend. It must stay disjoint from
// ownsSentinelNames, ownsValueNames and ownsFlagNames, and
// ownsValueNames is defined in terms of it so the arms cannot drift
// apart. The flag exclusion matters for a marker naming something like
// `--foo_bar`: kebab-case is gate-enforced for real flags, but the
// marker operand is author-written, and without the exclusion two arms
// would own that name at once.
func ownsFieldNames(name string) bool {
	return strings.Contains(name, "_") && !ownsFlagNames(name) &&
		!isNoEnvelopeSentinel(name) && !isKnownStatusValue(name)
}

// checkFieldNameClaims returns one violation per field name sec asserts
// that no struct in sec.module's scope declares.
func checkFieldNameClaims(t *testing.T, path string,
	sec docSection) []string {
	t.Helper()

	known := knownFieldNames(t, sec.module)

	var violations []string
	for _, sentence := range annotateSentences(sec.module,
		normalizeWhitespace(sec.text)) {

		seen := make(map[string]bool)
		for _, re := range []*regexp.Regexp{fieldClaimRe, jsonKeyClaimRe} {
			for _, m := range re.FindAllStringSubmatch(sentence.text, -1) {
				name := strings.ToLower(m[1])
				if !ownsFieldNames(name) || known[name] || seen[name] {
					continue
				}
				if sentence.spend(name, ownsFieldNames) {
					seen[name] = true
					continue
				}
				seen[name] = true
				violations = append(violations, fmt.Sprintf(
					"%s: names field %q, which no struct in %q scope "+
						"declares — as a json tag or a yaml tag. Note "+
						"that `-o yaml` emits the JSON tags (Print "+
						"renders yaml through jsonShaped), so a "+
						"lowercased Go field name is never the right "+
						"spelling. Correct the field, or "+
						"if the sentence names it in order to document "+
						"its absence, annotate it: `<!-- doc-gate: "+
						"deliberately-wrong %s — <reason> -->`. If it is "+
						"a real field of another module, use `<!-- "+
						"doc-gate: correct-for <module> %s — <reason> "+
						"-->`. Sentence: %q",
					path, name, sec.module, name, name,
					strings.TrimSpace(sentence.text)))
			}
		}

		violations = append(violations,
			unusedMarkerViolations(path, &sentence, ownsFieldNames)...)
	}
	return violations
}

// TestSkillDocsFieldClaimsMatchStructs runs the field-name check over
// the same doc set the other doc gates cover.
func TestSkillDocsFieldClaimsMatchStructs(t *testing.T) {
	files := scanDocFiles(t)

	scanned := make(map[string]bool, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanned[path] = true

		for _, sec := range moduleSections(path, string(content)) {
			for _, msg := range checkFieldNameClaims(t, path, sec) {
				t.Error(msg)
			}
		}
	}

	assertAllNamedDocsScanned(t, scanned)
}

// TestFieldClaimCheckCatchesInventedField is the acceptance test: a
// field the code does not have, caught from the structs rather than
// from a memorised string. This is the whole difference between this
// check and the hardcoded noEnvelopeSentinels list — nobody had to
// predict `job_id`.
//
// NOTE ON task_id, WHICH IS DELIBERATELY NOT ONE OF THESE CASES. The
// obvious case to write here is the historical defect itself, and it
// does not belong: task_id is a noEnvelopeSentinel, so ownsFieldNames
// excludes it and this check never sees it. That is the partition
// working, not a gap — but it does mean this check cannot be described
// as "the one that would have caught task_id". The accurate claim is
// narrower and is pinned by TestTaskIDRemainsCaughtBySomeGate below:
// task_id stays caught, by its sibling check, which also catches the
// "task output" and "task's output" phrasings that carry no underscore
// and are therefore invisible here. Moving task_id into this check
// would trade those two phrasings for nothing.
func TestFieldClaimCheckCatchesInventedField(t *testing.T) {
	cases := map[string]string{
		"an invented field nobody has written yet": "Read `job_id` " +
			"from the response.",
		"a json key in a fenced response": "```json\n" +
			"{\"operation_handle_id\": \"abc\"}\n```",
		"a plausible-looking envelope field": "The response carries " +
			"`request_id` for correlation.",
	}

	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkFieldNameClaims(t, "fixture.md",
				docSection{module: "byoc", text: fixture})
			if len(got) == 0 {
				t.Fatalf("checkFieldNameClaims did not flag %q", fixture)
			}
		})
	}
}

// TestTaskIDRemainsCaughtBySomeGate is the seam test between this check
// and checkNoEnvelopeClaims. The three allowance predicates partition
// the namespace, so it is possible to move a name from one check to
// another and leave it owned by NEITHER — a name that no check claims
// is a name no check reports, and the partition tests would still pass
// because "owned by exactly one" is false in the direction nobody
// asserts. This asserts the union, for the one name whose absence
// started all of this.
func TestTaskIDRemainsCaughtBySomeGate(t *testing.T) {
	sec := docSection{module: "byoc",
		text: "Capture the returned `task_id` and poll it."}

	total := len(checkNoEnvelopeClaims("fixture.md", sec)) +
		len(checkStatusStateValues("fixture.md", sec)) +
		len(checkFieldNameClaims(t, "fixture.md", sec))

	if total == 0 {
		t.Fatal("a byoc task_id claim is now caught by NO doc gate — " +
			"the three checks partition the allowance namespace, so a " +
			"name can be moved out of one without landing in another")
	}
}

// TestFieldClaimCheckAcceptsRealFields is the negative control the
// check needs to be worth anything: a gate that flags everything and a
// gate that flags nothing both look like a clean run on a clean doc.
// These are real fields, verified present on real structs.
func TestFieldClaimCheckAcceptsRealFields(t *testing.T) {
	cases := map[string]struct {
		module string
		text   string
	}{
		"byoc Database.cluster_id": {"byoc",
			"Capture `cluster_id` from the database response."},
		"account Tenant.plan_expires_at": {"byoc",
			"The tenant carries `plan_expires_at`."},
		"cp Task.task_id": {"controlplane",
			"Read `task_id` from the controlplane task response."},
		"config current_profile": {"byoc",
			"The config file's `current_profile` key selects a profile."},
		// Named for what it actually is, which is a yaml key equalling
		// its json key. The gate has no notion of output format — it
		// scans backticked underscore-bearing tokens — and cluster_id
		// resolves from a json tag, so this cannot fail if yaml/json
		// equality ever breaks; that is pinned live by
		// TestProfileShowYAMLUsesTheSameKeysAsJSON instead. It replaced
		// a `hascreds` case that was vacuous: fieldClaimRe requires an
		// underscore, so `hascreds` was never a claim this gate checks
		// and it passed regardless of the truth universe.
		"a json tag claimed in yaml prose": {"byoc",
			"Under -o yaml the same object carries `cluster_id`."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkFieldNameClaims(t, "fixture.md",
				docSection{module: tc.module, text: tc.text})
			if len(got) != 0 {
				t.Fatalf("checkFieldNameClaims flagged a real field: %v",
					got)
			}
		})
	}
}

// TestFieldClaimCheckIsModuleScoped proves the scope actually bites:
// controlplane's genuine task_id must fail in a byoc-scoped section and pass in a
// controlplane-scoped one. A check whose scoping did nothing would pass both.
// patroni_port is the right example rather than task_id: it is a real
// field on controlplane's DatabaseNodeSpec, absent from every byoc struct, and —
// unlike task_id — it is not a noEnvelopeSentinel, so this check
// genuinely owns it. It is also one of the two ROADMAP.md rows that
// made this scoping visible in the first place.
func TestFieldClaimCheckIsModuleScoped(t *testing.T) {
	claim := "Each node needs a `patroni_port`."

	if got := checkFieldNameClaims(t, "fixture.md",
		docSection{module: "controlplane", text: claim}); len(got) != 0 {
		t.Fatalf("controlplane's real patroni_port was flagged in controlplane scope: %v", got)
	}
	if got := checkFieldNameClaims(t, "fixture.md",
		docSection{module: "byoc", text: claim}); len(got) == 0 {
		t.Fatal("controlplane's patroni_port was accepted in byoc scope — the " +
			"module scoping is not doing anything")
	}
}

// TestFieldClaimCheckStarfleetScopeExists proves the new "starfleet" scope is
// real rather than merely accepted by moduleSections: it has its own
// struct universe (scopeSourceDirs' "starfleet" entry), inherited from the
// deleted account fold rather than borrowed from byoc's. `client_id`
// is a real field on the generated account API's ClientSecretJSONBody
// (internal/starfleet/account/api/client.gen.go) and must pass in cloud
// scope; controlplane's patroni_port, absent from starfleet's struct universe, must
// still fail there — the same asymmetry
// TestFieldClaimCheckIsModuleScoped proves for byoc vs cp. Until
// scopeSourceDirs gains a "starfleet" key this fails with "no source dirs
// registered for module scope", not a false negative.
//
// WHY auth0_secret IS PINNED HERE AND client_id CANNOT CARRY THE CASE
// ALONE. client_id looks like the account API's field and is described
// that way above, but it is ALSO a yaml tag on config.StarfleetProfile
// (internal/config/config.go), and `../config` is in every scope's
// source dirs. So the client_id assertion passes even if
// `../starfleet/account` is deleted from scopeSourceDirs' "starfleet" entry:
// it proves the scope resolves, not that the account package is wired
// into it. auth0_secret is the field that closes that: it exists in
// exactly one struct in the tree, the generated account client's
// CreateApiClientResponse (json tag `auth0_secret`), so it can only
// pass while cloud scope actually reaches internal/starfleet/account. That
// matters because internal/starfleet/llms.txt's api-client sections lean
// on auth0_secret harder than on any other field name — twelve
// occurrences, all of them hand-written prose asserting the
// create-only-secret rule — and every one of those claims goes
// unchecked the moment the package drops out of scope.
func TestFieldClaimCheckStarfleetScopeExists(t *testing.T) {
	if got := checkFieldNameClaims(t, "fixture.md",
		docSection{module: "starfleet",
			text: "The token exchange takes `client_id`."}); len(got) != 0 {
		t.Fatalf("starfleet's real client_id was flagged in cloud scope: %v",
			got)
	}
	if got := checkFieldNameClaims(t, "fixture.md",
		docSection{module: "starfleet",
			text: "`create` returns `auth0_secret` once."}); len(got) != 0 {
		t.Fatalf("starfleet's real auth0_secret was flagged in cloud scope "+
			"— internal/starfleet/account is no longer in scopeSourceDirs' "+
			"\"cloud\" entry, or the field was renamed in the generated "+
			"account client: %v", got)
	}
	if got := checkFieldNameClaims(t, "fixture.md",
		docSection{module: "starfleet",
			text: "Each node needs a `patroni_port`."}); len(got) == 0 {
		t.Fatal("controlplane's patroni_port was accepted in cloud scope — the " +
			"cloud scope is not doing anything")
	}
}

// TestStarfleetScopeReachesTheAccountPackage is the other half of the pin
// above, and the half that does not depend on anybody reading a
// comment. It asserts auth0_secret is in starfleet's collected truth
// universe AND absent from the shared source dirs every scope gets, so
// a future edit that keeps the field but stops walking the account
// package fails here with the reason named, rather than silently
// widening what cloud-scoped prose may claim.
func TestStarfleetScopeReachesTheAccountPackage(t *testing.T) {
	const field = "auth0_secret"

	if !knownFieldNames(t, "starfleet")[field] {
		t.Errorf("%q is not in starfleet's truth universe — cloud-scoped "+
			"prose naming it now fails, and internal/starfleet/llms.txt "+
			"names it repeatedly", field)
	}

	shared := make(map[string]bool)
	for _, dir := range []string{
		"../cli", "../config", "../output", "../auth", "../module"} {
		for name := range collectFieldNames(t, dir) {
			shared[name] = true
		}
	}
	if shared[field] {
		t.Errorf("%q now exists in the shared source dirs too, so it no "+
			"longer proves cloud scope reaches internal/starfleet/account — "+
			"pick another account-only field for the pin in "+
			"TestFieldClaimCheckStarfleetScopeExists", field)
	}
}

// TestFieldClaimCheckHonoursBothMarkerVerbs pins that this check shares
// the one opt-out rather than growing its own. Both verbs must work:
// deliberately-wrong for a sentence documenting a field's absence, and
// correct-for where the field is real under another module's scope —
// which is the ROADMAP.md case, a byoc-scoped planning file naming
// genuine cp fields.
func TestFieldClaimCheckHonoursBothMarkerVerbs(t *testing.T) {
	t.Run("deliberately-wrong", func(t *testing.T) {
		fixture := "There is no `token_exists` field. " +
			"<!-- doc-gate: deliberately-wrong token_exists — this " +
			"sentence documents the field's absence -->"
		if got := checkFieldNameClaims(t, "fixture.md",
			docSection{module: "byoc", text: fixture}); len(got) != 0 {
			t.Fatalf("the marker did not clear the claim: %v", got)
		}
	})

	t.Run("correct-for", func(t *testing.T) {
		fixture := "The node spec carries `patroni_port`. " +
			"<!-- doc-gate: correct-for controlplane patroni_port — a real field " +
			"on controlplane's DatabaseNodeSpec -->"
		if got := checkFieldNameClaims(t, "fixture.md",
			docSection{module: "byoc", text: fixture}); len(got) != 0 {
			t.Fatalf("the marker did not clear the claim: %v", got)
		}
	})

	t.Run("unannotated control", func(t *testing.T) {
		if got := checkFieldNameClaims(t, "fixture.md",
			docSection{module: "byoc",
				text: "The node spec carries `patroni_port`."},
		); len(got) != 1 {
			t.Fatalf("want exactly 1 violation without a marker, got "+
				"%d: %v", len(got), got)
		}
	})
}

// TestFieldClaimAllowanceNamespaceIsDisjoint pins the four-way
// partition. `backing_up` is a real byoc status value that also
// contains an underscore, so it is the one name that could be claimed
// by two checks at once; `--cp-url` and `--foo_bar` are the flag
// shapes (the second is what an author-written marker could name even
// though the kebab gate keeps real flags underscore-free). If any arm
// stops excluding another's names, one check spends the marker and
// the other reports it as excusing nothing — a correct doc with no
// remedy, which is the exact failure class the marker design exists
// to end.
func TestFieldClaimAllowanceNamespaceIsDisjoint(t *testing.T) {
	names := []string{"task_id", "backing_up", "cluster_id", "active",
		"--cp-url", "--foo_bar"}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			owners := 0
			for _, own := range []func(string) bool{
				ownsSentinelNames, ownsValueNames, ownsFieldNames,
				ownsFlagNames} {
				if own(name) {
					owners++
				}
			}
			if owners != 1 {
				t.Errorf("%q is owned by %d of the four allowance "+
					"predicates, want exactly 1 — the partition must "+
					"stay total and disjoint", name, owners)
			}
		})
	}
}
