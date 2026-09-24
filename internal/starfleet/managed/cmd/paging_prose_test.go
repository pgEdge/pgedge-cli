package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/docgen"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// pagedManagedVerbs is the ONE list, read by both gates below.
//
// It used to be two — this map keyed on the full path, and a separate
// `covered` map in the derived gate keyed without the "pgedge " prefix,
// with nothing tying them. Review added a fourth paginated verb, added
// one line to `covered`, and the whole suite stayed green: the verb had
// no cli.PageDefaults entry, no row here and no page-size claim, while
// the gate's failure message told you to add a row to a test nothing
// forced you to touch.
var pagedManagedVerbs = map[string]cli.PageDefaults{
	"pgedge starfleet managed task list":   taskPageDefaults,
	"pgedge starfleet managed backup list": backupPageDefaults,
	// Def 0, which is what makes the no-default branch below a live
	// exemption rather than an unexercised one.
	"pgedge starfleet managed database list":        databasePageDefaults,
	"pgedge starfleet managed database branch list": branchPageDefaults,
}

// TestManagedPagingProseMatchesPageDefaults is the managed half of
// byoc's TestPagingProseMatchesPageDefaults, and the reason managed
// needed one: the module reference carried NO page size for `task list`
// at all and none for `database list`, while the measured 25/100 pair
// sat in saas's source with nothing tying the two together. byoc's gate
// exists because that drift already happened once there (#198);
// managed's exists because the same claim was missing rather than
// stale.
//
// The span extraction is docgen.CommandProse, shared with byoc's gate —
// the anchoring rules and what each of them prevents live there.
func TestManagedPagingProseMatchesPageDefaults(t *testing.T) {
	for cmd, pd := range pagedManagedVerbs {
		t.Run(cmd, func(t *testing.T) {
			ref := managedCommandProse(t, cmd)

			// Both numeric claims, in BOTH directions: the number the
			// table records present exactly once, and NO second claim
			// of either shape naming a different number. A Def or Cap
			// of 0 requires the absence of that shape entirely, which
			// is what makes `database list` a live exercise of the
			// negative rather than an unreached branch. The inverse
			// half is the point — review added a contradictory second
			// page size and a contradictory second cap and left this
			// gate green both times, because Contains can only catch
			// a MISSING quote. See docgen.CheckPagingClaims.
			if err := docgen.CheckPagingClaims(
				ref, pd.Def, pd.Cap,
			); err != nil {
				t.Errorf("%s: %v", cmd, err)
			}

			// The WORDING of each absence, which no number can carry.
			// Def 0 is a real state here, not a gap: the endpoint
			// applies no default page, and "0 rows by default" would
			// be nonsense, so the prose has to state the absence.
			if pd.Def > 0 {
				if strings.Contains(ref, "no default page") {
					t.Errorf("%s: prose claims no default page, but "+
						"cli.PageDefaults records %d", cmd, pd.Def)
				}
			} else if !strings.Contains(ref, "no default page") {
				t.Errorf("%s: prose does not state that this "+
					"endpoint applies no default page, which is what "+
					"cli.PageDefaults records and what makes an "+
					"unbounded read complete rather than a first "+
					"page", cmd)
			}

			if pd.Cap > 0 {
				if strings.Contains(ref, "no known cap") {
					t.Errorf("%s: prose claims no known cap, but "+
						"cli.PageDefaults records a cap of %d",
						cmd, pd.Cap)
				}
				return
			}
			// Asserted positively as well as negatively, so that
			// deleting the paragraph cannot satisfy this branch.
			if !strings.Contains(ref, "no known cap") {
				t.Errorf("%s: prose does not state that this endpoint "+
					"has no known cap, which is what cli.PageDefaults "+
					"records", cmd)
			}
			if strings.Contains(ref, "is clamped silently") {
				t.Errorf("%s: prose claims a silent clamp, but "+
					"cli.PageDefaults records no cap", cmd)
			}
		})
	}
}

// TestEveryPaginatedManagedVerbHasAProseClaim derives its population
// from the flag, not from a count. A first version grepped for
// `cli.PrintTruncationHint(` and asserted the count was 3; review
// defeated it in one file with an aliased import (`import platform
// "…/internal/cli"`), added a fourth hinting verb, and left every gate
// green — including a hint on `size list`, which takes no `--limit` at
// all and so told the user to pass a flag that does not exist.
//
// A count pins the table, not the property. What actually needs
// covering is "a paginated list verb has a documented page size", and
// `--limit` is how a verb declares itself paginated. So this walks the
// managed tree: every leaf carrying `--limit` must have a row in the
// map above, or a reason here.
//
// There is NO exemption map. One existed for a round and review
// defeated the gate with it in a single line: drop a verb from
// pagedManagedVerbs, add it to the map with a false reason, delete its
// prose, and everything stayed green while the subtest count quietly
// fell where nothing was looking. A waiver now costs re-adding the
// mechanism in the diff, where it can be argued about.
//
// THREE named blind spots, shared with byoc's walk and recorded rather
// than closed — widening the population is a separate change:
//
//   - the walk keys on `--limit`, so the OTHER truncating flags are
//     outside it: `--max-lines` on `database logs` in both byoc and
//     managed, and `--lines` on byoc's `node logs`. Each documents a
//     server default of 100, each truncates silently, and none prints
//     a hint. `--max-lines` keeps TWO unbound copies of its 100 — one
//     in each module's flag help — plus a third in managed's reference
//     prose, which is the same configuration the derived `--limit` help
//     was written to remove, not merely a gap in this walk's
//     population.
//   - `c.Flags()` before execution does NOT carry a parent's
//     persistent flags — measured: on `starfleet managed task list`,
//     `Flags().Lookup("limit")` is true while
//     `Flags().Lookup("api-url")` is false and only
//     `InheritedFlags()` sees it. So a `--limit` hoisted to a resource
//     group's PersistentFlags() would be invisible here.
//   - a verb that hints WITHOUT declaring `--limit` is still
//     invisible. That is a deliberate act rather than drift, since the
//     hint's own message names a flag the verb lacks.
//
// Closing the third would need a behavioural sweep with a decodable
// stub body per verb, which is a bigger instrument than this file. The
// count check below is kept as a cheap tripwire for the ordinary case,
// with no pretence that it is more.
func TestEveryPaginatedManagedVerbHasAProseClaim(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	root := newStarfleetRoot(rt)
	// DERIVED from the prose gate's own table, so a verb cannot be
	// waved through here without acquiring a row there.
	covered := map[string]bool{}
	for cmd := range pagedManagedVerbs {
		covered[strings.TrimPrefix(cmd, "pgedge ")] = true
	}

	found := 0
	walkLeaves(root, func(c *cobra.Command) {
		if c.RunE == nil || c.Flags().Lookup("limit") == nil {
			return
		}
		found++
		path := c.CommandPath()
		if covered[path] {
			return
		}
		t.Errorf("%s takes --limit but has no row in "+
			"TestManagedPagingProseMatchesPageDefaults. A paginated "+
			"list verb needs a cli.PageDefaults entry in paging.go "+
			"with its provenance, a row in that test, and a page-size "+
			"claim in its section of llms.txt — a verb that hints with "+
			"no documented page size is the drift #198 and #269 were "+
			"both about. If a verb genuinely cannot carry one, say why "+
			"in this file — there is deliberately no exemption map to "+
			"add a line to.", path)
	})
	// Tied to the table, not a literal: a floor sitting exactly at
	// today's count stops being able to detect a walk that finds three
	// of four the moment a fourth verb lands.
	if found < len(pagedManagedVerbs) {
		t.Fatalf("only %d managed leaves declare --limit, want at "+
			"least %d; the walk has broken and this gate would pass "+
			"without reading the tree", found, len(pagedManagedVerbs))
	}
}

// TestEveryManagedHintCallSiteHasAProseClaim is that cheap tripwire;
// its limits are in the comment above.
func TestEveryManagedHintCallSiteHasAProseClaim(t *testing.T) {
	dir, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range dir {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		found += strings.Count(string(raw), "cli.PrintTruncationHint(")
	}
	if found != 4 {
		t.Errorf("%d direct cli.PrintTruncationHint call sites in this "+
			"package; TestManagedPagingProseMatchesPageDefaults covers "+
			"4. This counts DIRECT calls only — an aliased import or a "+
			"local wrapper is invisible to it, which review "+
			"demonstrated. TestEveryPaginatedManagedVerbHasAProseClaim "+
			"is the derived check; this one just catches the ordinary "+
			"case early.", found)
	}
}

func managedCommandProse(t *testing.T, command string) string {
	t.Helper()

	prose, err := docgen.CommandProse(managedReferenceText(t), command)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return prose
}

// managedReferenceText concatenates the module's llms.txt index with
// every resource page under ../llms/ (the reference used to be one
// file; it is now an index plus one page per resource). Shared by
// every test in this package that used to read ../llms.txt alone —
// metricsample_test.go's quote gates and specenums_test.go's section
// finder among them — so the walk and its failure mode live once.
//
// Concatenating rather than reading one file keeps
// docgen.CommandProse's marker-uniqueness check meaningful: every
// command's generated block lives in exactly one page, so the marker
// still appears exactly once across the whole corpus.
//
// Sorted and walked rather than listed by hand, so a new page is
// covered the day it lands. A walk that finds zero pages is the split
// having broken, not an empty reference, and must fail loudly rather
// than let every caller pass against an empty string.
func managedReferenceText(t *testing.T) string {
	t.Helper()

	idxPath := filepath.Join("..", "llms.txt")
	idx, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read %s: %v", idxPath, err)
	}

	root := filepath.Join("..", "llms")
	var pages []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".txt") {
			pages = append(pages, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(pages) == 0 {
		t.Fatalf("no reference pages found under %s; the walk has "+
			"broken and every test reading this text would pass "+
			"vacuously", root)
	}
	sort.Strings(pages)

	var b strings.Builder
	b.Write(idx)
	for _, p := range pages {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		b.WriteByte('\n')
		b.Write(raw)
	}
	return b.String()
}

func walkLeaves(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walkLeaves(sub, fn)
	}
}
