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

// pagedByocVerbs is the ONE table. It was a local inside the test
// below, which made it a second, hand-maintained population that
// nothing derived: review dropped `ingress list`'s row from it, deleted
// `ingress list`'s whole paging claim from the reference, and `make
// test`, `make docs-check` and `make lint` all stayed green. managed's
// gates were fixed to read one table a round earlier; byoc kept the
// shape that made the bypass possible, so the changelog's claim about
// it was true of managed only.
//
// TestEveryPaginatedByocVerbHasAProseClaim derives its population from
// this map, so a verb cannot be dropped from here without the walk
// finding it in the tree with nowhere to point.
var pagedByocVerbs = map[string]cli.PageDefaults{
	"pgedge starfleet byoc cluster list":           clusterDefaults,
	"pgedge starfleet byoc database list":          databaseDefaults,
	"pgedge starfleet byoc ingress list":           ingressDefaults,
	"pgedge starfleet byoc backup-store list":      backupStoreDefaults,
	"pgedge starfleet byoc task list":              taskDefaults,
	"pgedge starfleet byoc backup-repository list": backupRepositoryDefaults,
	// The one endpoint with no cap, which is what makes the cap branch
	// below a live exemption rather than an unexercised one. It is also
	// the reason the walk keys on `--limit` rather than on a `list`
	// verb name: this is a `get`, and it pages.
	"pgedge starfleet byoc backup-repository get": backupInfoDefaults,
}

// TestPagingProseMatchesPageDefaults binds the reference's paging
// sentence to cli.PageDefaults, the table that actually drives
// printTruncationHint.
//
// Nothing tied the two together, and the reference drifted away from
// the binary while every gate stayed green: three sections were
// rewritten to say the default was unknown, while the table driving
// printTruncationHint recorded it for each endpoint. A doc claim about a number the CLI itself holds should
// not be checkable only by reading.
//
// Both fields are bound, because both drive the hint. An earlier
// version asserted the default as a phrase and the cap as the bare word
// "clamped", and review showed the cap half was inert: raising a cap to
// 200 left the prose saying 100 and the gate green.
//
// Scoped per COMMAND, via the `BEGIN GENERATED: <path>` marker each
// command's block carries, rather than per resource heading. The
// resource sections run to thousands of characters and hold more than
// one paging claim — `backup-repository` documents 10 for `list` and
// 100 for `get` — so a resource-scoped search could match a neighbour's
// number and pass while the verb's own prose was stale. Review
// constructed exactly that.
func TestPagingProseMatchesPageDefaults(t *testing.T) {
	for cmd, pd := range pagedByocVerbs {
		t.Run(cmd, func(t *testing.T) {
			ref := byocCommandProse(t, cmd)

			// Both numeric claims, in BOTH directions: the number the
			// table records present exactly once, and NO second claim
			// of either shape naming a different number. The inverse
			// half is the point — review put "above 200 is clamped"
			// beside the true "above 100 is clamped" and left this gate
			// green, because a Contains over a joined corpus can only
			// ever catch a MISSING quote. See docgen.CheckPagingClaims.
			if err := docgen.CheckPagingClaims(
				ref, pd.Def, pd.Cap,
			); err != nil {
				t.Errorf("%s: %v", cmd, err)
			}

			// The cap's WORDING is a separate claim that drifted
			// separately: the reference documented the number while
			// saying nothing about a larger --limit being clamped
			// rather than refused.
			if pd.Cap > 0 {
				if strings.Contains(ref, "no known cap") {
					t.Errorf("%s: prose claims no known cap, but "+
						"cli.PageDefaults records a cap of %d", cmd, pd.Cap)
				}
				return
			}
			// Asserted positively as well as negatively: a branch that
			// only checks a claim is ABSENT is satisfied by deleting the
			// paragraph. And the negative keys on the affirmative shape
			// "is clamped silently" rather than "is clamped", so a
			// truthful negation — "nothing here is clamped" — cannot
			// trip it.
			if !strings.Contains(ref, "no known cap") {
				t.Errorf("%s: prose does not state that this endpoint has "+
					"no known cap, which is what cli.PageDefaults records", cmd)
			}
			if strings.Contains(ref, "is clamped silently") {
				t.Errorf("%s: prose claims a silent clamp, but "+
					"cli.PageDefaults records no cap for this endpoint", cmd)
			}
		})
	}
}

// byocCommandProse reads this module's reference and delegates the
// span extraction to docgen.CommandProse, which managed's sibling gate
// also uses. The anchoring rules and the reasons for each of them live
// there; duplicating fifty lines of them here is how the two copies
// would drift.
func byocCommandProse(t *testing.T, command string) string {
	t.Helper()

	prose, err := docgen.CommandProse(byocReferenceText(t), command)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return prose
}

// byocReferenceText concatenates the module's llms.txt index with
// every resource page under ../llms/ (the reference used to be one
// file; it is now an index plus one page per resource). Concatenating
// rather than reading a single file keeps docgen.CommandProse's
// marker-uniqueness check meaningful: every command's generated block
// lives in exactly one page, so the marker still appears exactly once
// across the whole corpus.
//
// Sorted and walked rather than listed by hand, so a new page is
// covered the day it lands. A walk that finds zero pages is the split
// having broken, not an empty reference, and must fail loudly rather
// than let every subtest above pass against an empty string.
func byocReferenceText(t *testing.T) string {
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

// TestEveryPaginatedByocVerbHasAProseClaim derives its population from
// the flag, the same way managed's does. `--limit` is how a verb
// declares itself paged, so every leaf that carries one must have a row
// in pagedByocVerbs — and therefore a page-size claim in its own
// section of llms.txt, since TestPagingProseMatchesPageDefaults reads
// that same map.
//
// This is what review's bypass needed and did not have: dropping
// `ingress list` from the map and deleting its paging paragraph left
// every gate green, because the map WAS the population. Now the tree
// is.
//
// There is NO exemption map. One existed for a round and review
// defeated the gate with it in a single line: drop a verb from
// pagedByocVerbs, add it to the map with a false reason, delete its
// prose, and everything stayed green while the subtest count quietly
// fell. A waiver now costs re-adding the mechanism in the diff, where
// it can be argued about.
//
// THREE named blind spots, recorded rather than closed — widening the
// population is a separate change: the OTHER truncating flags
// (`--max-lines`, `--lines`), a `--limit` hoisted to a resource group's
// PersistentFlags(), and a verb that hints WITHOUT declaring `--limit`.
// They are the same three for both walks, so managed's
// TestEveryPaginatedManagedVerbHasAProseClaim carries the full entry for
// each, including the measurement behind the second; duplicating them
// here is how the two copies would drift.
func TestEveryPaginatedByocVerbHasAProseClaim(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "text")
	root := newStarfleetRoot(rt)

	covered := map[string]bool{}
	for cmd := range pagedByocVerbs {
		covered[strings.TrimPrefix(cmd, "pgedge ")] = true
	}

	found := 0
	walkByocCommands(root, func(c *cobra.Command) {
		if c.RunE == nil || c.Flags().Lookup("limit") == nil {
			return
		}
		found++
		path := c.CommandPath()
		if covered[path] {
			return
		}
		t.Errorf("%s takes --limit but has no row in pagedByocVerbs. A "+
			"paginated verb needs a cli.PageDefaults entry in "+
			"pagination.go with its provenance, a row in that map, and "+
			"a page-size claim in its section of llms.txt — a verb "+
			"that hints with no documented page size is the drift "+
			"this test exists to catch. If a verb genuinely cannot "+
			"carry one, say why in this file — there is deliberately "+
			"no exemption map to add a line to.", path)
	})
	// Tied to the table rather than to a literal: a floor sitting at
	// today's count stops discriminating the moment an eighth verb
	// lands, and a walk that has broken finds nothing and says so.
	if found < len(pagedByocVerbs) {
		t.Fatalf("only %d byoc leaves declare --limit, want at least "+
			"%d; the walk has broken and this gate would pass without "+
			"reading the tree", found, len(pagedByocVerbs))
	}
}

func walkByocCommands(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walkByocCommands(sub, fn)
	}
}
