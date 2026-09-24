package clitest

import (
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file exists because per-module fixtures could not see the
// modules they were not in. The paging sweep (#280) landed with tests
// in internal/starfleet/managed/cmd only, and a reviewer showed that
// deleting the whole validation block from `byoc ingress list`, or
// inventing a ceiling in `controlplane task list`, left the entire suite green.
// Fifteen of the twenty-one call sites were unprotected.
//
// The gate walks the LIVE command tree, so a list verb added tomorrow
// is covered the day it lands rather than the day someone remembers to
// add a fixture for it.

// pagingCeilings names the only verbs allowed to refuse a large value,
// and the maximum each may enforce.
//
// An allowlist rather than a per-verb assertion, because the property
// being protected is a negative one: a ceiling must exist ONLY where
// the vendored spec declares a maximum. managed.yaml declares one for
// exactly two operations; byoc.yaml, account.yaml and
// control-plane.json declare none anywhere.
//
// This map does not certify itself, and an earlier version of this
// comment wrongly implied it did. TestPagingCeilingsAreSpecDerived
// (paging_ceiling_spec_test.go) walks all four specs and fails if the
// allowlist and the contracts describe different sets of bounds — a
// row added here for an endpoint whose spec is silent is exactly how
// an invented ceiling reached the binary behind a message claiming
// the contract declared it.
//
// Keyed on the SPEC PATH as well as the number. Keying on the value
// alone let a reviewer wire a real declaration to the wrong verb: a
// maximum of 100 published on /byoc/v1/ingresses satisfied a
// `controlplane task list` row of 100, so cp shipped a fabricated ceiling while
// byoc's genuine bound went unenforced, with every gate green.
type pagingCeiling struct {
	specPath string
	max      int
}

var pagingCeilings = map[string]pagingCeiling{
	"pgedge starfleet managed backup list":   {"/managed/v1/backups", 100},
	"pgedge starfleet managed database list": {"/managed/v1/databases", 1000},
	"pgedge starfleet managed database branch list": {
		"/managed/v1/databases/{id}/branches", 100},
}

// pagingFloorVerbs is the count of leaves carrying --limit that the
// sweep must find.
//
// It catches ONE thing: the walk going blind — a changed tree shape,
// or a FullTree regression that stops returning verbs. It cannot see
// a synthesis failure, because it counts pflag introspection and
// never runs a command; that case is caught by the positive gate's
// message match, where an unfilled required flag yields "required
// flag(s) not set" and refusedTheFlag returns false.
//
// Twelve is the tally at the time of writing (7 byoc, 4 managed,
// 1 cp) -- `database branch list` is the fourth managed one. A new
// paging verb raises it; nothing may lower it without saying why.
const pagingFloorVerbs = 12

// pagingArgOverrides replace the generic argument synthesis for verbs
// it cannot satisfy.
//
// Scoped to this file rather than added to the dry-run sweep's own
// override map: the two sweeps drive the tree for different reasons,
// and a shared override silently changes what the other one tests.
//
// `controlplane task list` needs one: cp marks nothing required and validates by
// hand, so the generic synthesis fills every selector flag it finds --
// including both --database and --host, which the verb refuses as
// mutually exclusive before it ever looks at --limit. That refusal is
// also exit 2, which is exactly why refusedTheFlag matches the MESSAGE
// rather than the code; a code-only assertion would have scored this
// as the paging check working.
var pagingArgOverrides = map[string][]string{
	"pgedge controlplane task list": {"--database", sweepUUID},
}

// pagingLeaf is one command carrying paging flags.
type pagingLeaf struct {
	path      string
	cmd       *cobra.Command
	hasLimit  bool
	hasOffset bool
}

// pagingLeaves finds every leaf declaring an int --limit.
func pagingLeaves(t *testing.T) []pagingLeaf {
	t.Helper()
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	var out []pagingLeaf
	walk(root, func(c *cobra.Command) {
		if c.HasSubCommands() {
			return
		}
		var limit, offset bool
		c.Flags().VisitAll(func(f *pflag.Flag) {
			switch f.Name {
			case "limit":
				limit = f.Value.Type() == "int"
			case "offset":
				offset = f.Value.Type() == "int"
			}
		})
		// limit OR offset: keying on limit alone would make a future
		// verb carrying only --offset invisible to every gate here,
		// and to the floor meant to notice verbs going missing. None
		// exists today.
		if limit || offset {
			out = append(out, pagingLeaf{
				path: c.CommandPath(), cmd: c,
				hasLimit: limit, hasOffset: offset})
		}
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i].path < out[j].path
	})
	return out
}

// runPagingVerb runs one verb with extra arguments and returns the
// error, under a HOME with no configuration at all.
//
// The empty HOME is the instrument, not a convenience. A check that
// runs BEFORE the API client is built needs no credentials, so it
// answers exit 2; anything after it answers exit 5 for the credentials
// it could not find. That difference is the evidence that the
// validation sits where it should.
//
// cp needs no credentials, so that discriminator alone does not
// reach `controlplane task list`. TestCPPagingCheckPrecedesTheClientBuild below
// supplies the missing one.
func runPagingVerb(t *testing.T, leaf pagingLeaf, extra ...string) error {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	rt := &module.Runtime{}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			t.Fatal(err)
		}
		root.AddCommand(c)
	}

	args := strings.Fields(strings.TrimPrefix(leaf.path, "pgedge "))
	target, _, err := root.Find(args)
	if err != nil {
		t.Fatalf("%s: %v", leaf.path, err)
	}

	full := make([]string, 0, len(args)+10)
	full = append(full, args...)
	if override, ok := pagingArgOverrides[leaf.path]; ok {
		full = append(full, override...)
	} else {
		full = append(full, synthesizeArgs(target)...)
	}
	full = append(full, extra...)
	root.SetArgs(full)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root.Execute()
}

// refusedTheFlag reports whether err is this CLI's own refusal of the
// named paging flag, rather than any other exit 2.
//
// Matching the MESSAGE, not the code, is the point. A verb whose
// required flags the synthesis failed to fill also exits 2 — with
// "required flag(s) not set" — and a code-only assertion would score
// that as the refusal working. It would pass just as happily on a verb
// where the check had been deleted outright.
func refusedTheFlag(err error, flag string) bool {
	if err == nil || cli.ExitCode(err) != cli.ExitUsage {
		return false
	}
	return strings.Contains(err.Error(), "--"+flag+" must be at ")
}

// TestEveryPagingVerbRefusesAValueItCannotMean is the sweep.
//
// --limit 0 and --limit -1 must be refused everywhere, and a negative
// --offset wherever --offset exists.
func TestEveryPagingVerbRefusesAValueItCannotMean(t *testing.T) {
	leaves := pagingLeaves(t)
	if len(leaves) < pagingFloorVerbs {
		t.Fatalf("found %d verbs carrying --limit, floor is %d; the "+
			"walk has stopped seeing verbs and every assertion below "+
			"would pass vacuously", len(leaves), pagingFloorVerbs)
	}

	for _, leaf := range leaves {
		t.Run(leaf.path, func(t *testing.T) {
			if leaf.hasLimit {
				for _, bad := range []string{"0", "-1"} {
					err := runPagingVerb(t, leaf, "--limit", bad)
					if !refusedTheFlag(err, "limit") {
						t.Errorf("--limit %s was not refused: %v",
							bad, err)
					}
				}
			}
			if !leaf.hasOffset {
				return
			}
			err := runPagingVerb(t, leaf, "--offset", "-1")
			if !refusedTheFlag(err, "offset") {
				t.Errorf("--offset -1 was not refused: %v", err)
			}
		})
	}
}

// TestEveryPagingVerbAcceptsOffsetZero is the boundary a
// "non-positive is a usage error" rule gets wrong if it is applied to
// both flags alike.
//
// The zeroth row is the first page — an ordinary value the API
// accepts. Refusing it would refuse something the contract permits.
func TestEveryPagingVerbAcceptsOffsetZero(t *testing.T) {
	var checked int
	for _, leaf := range pagingLeaves(t) {
		if !leaf.hasOffset {
			continue
		}
		checked++
		t.Run(leaf.path, func(t *testing.T) {
			err := runPagingVerb(t, leaf, "--offset", "0")
			if refusedTheFlag(err, "offset") {
				t.Errorf("--offset 0 was refused: %v", err)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no verb declares --offset; this gate proved nothing")
	}
}

// TestOnlySpecBoundedVerbsEnforceACeiling is the negative half, and
// the one that stops a guessed maximum spreading.
//
// A ceiling may exist only where the vendored spec declares a
// maximum. Everywhere else a large --limit must reach the wire, even
// though the server is known to clamp it: refusing a value the
// contract permits would still be refused the day the cap moves.
func TestOnlySpecBoundedVerbsEnforceACeiling(t *testing.T) {
	const huge = "100000"

	var bounded int
	for _, leaf := range pagingLeaves(t) {
		if !leaf.hasLimit {
			continue
		}
		t.Run(leaf.path, func(t *testing.T) {
			row, wantCeiling := pagingCeilings[leaf.path]
			ceiling := row.max
			err := runPagingVerb(t, leaf, "--limit", huge)
			got := refusedTheFlag(err, "limit")

			switch {
			case wantCeiling && !got:
				t.Errorf("--limit %s was accepted, but the spec "+
					"declares a maximum of %d for this endpoint",
					huge, ceiling)
			case !wantCeiling && got:
				t.Errorf("--limit %s was refused, but no spec "+
					"declares a maximum for this endpoint — the CLI "+
					"must not invent one, or it will refuse a value "+
					"the API accepts once the cap moves: %v", huge, err)
			}
			if wantCeiling {
				bounded++
				// The boundary itself, so the ceiling cannot drift
				// off the declared number in either direction.
				if err := runPagingVerb(
					t, leaf, "--limit", strconv.Itoa(ceiling)); refusedTheFlag(
					err, "limit") {
					t.Errorf("--limit %d is the declared maximum and "+
						"was refused: %v", ceiling, err)
				}
			}
		})
	}
	if bounded != len(pagingCeilings) {
		t.Errorf("checked %d bounded verbs, allowlist has %d; a path "+
			"in pagingCeilings no longer names a real verb and is "+
			"excusing nothing", bounded, len(pagingCeilings))
	}
}

// TestNoPagingVerbEnforcesAnOffsetCeiling closes the hole a reviewer
// found: nothing asserted that --offset is unbounded.
//
// No spec declares a maximum for offset anywhere, so a ceiling on it
// is always invented. On `managed backup list`, where --limit maxes at
// 100, --offset is the ONLY way to reach the 101st row — so a ceiling
// there makes later rows unreachable, with a message asserting a clamp
// the contract never declares. Every paging test passed with exactly
// that mutation in place.
func TestNoPagingVerbEnforcesAnOffsetCeiling(t *testing.T) {
	var checked int
	for _, leaf := range pagingLeaves(t) {
		if !leaf.hasOffset {
			continue
		}
		checked++
		t.Run(leaf.path, func(t *testing.T) {
			err := runPagingVerb(t, leaf, "--offset", "100000")
			if refusedTheFlag(err, "offset") {
				t.Errorf("a large --offset was refused: %v", err)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no verb declares --offset; this gate proved nothing")
	}
}

// TestCPPagingCheckPrecedesTheClientBuild is controlplane's ordering assertion,
// which the empty-HOME trick cannot make.
//
// Every cloud verb proves its check precedes the client build by
// answering 2 rather than 5 with no credentials. cp needs none, so its
// client builds either way and both orderings look identical — a
// reviewer moved the check below clientFromCmd and the whole suite
// stayed green.
//
// The discriminator is a client build that MUST fail: a --client-cert
// pointing at a file that does not exist. With the check first the
// paging refusal wins; with it second the caller is told their
// certificate is missing when their flag is wrong.
func TestCPPagingCheckPrecedesTheClientBuild(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pem")
	leaf := pagingLeaf{
		path:     "pgedge controlplane task list",
		hasLimit: true,
	}
	err := runPagingVerb(t, leaf, "--limit", "0",
		"--client-cert", missing, "--client-key", missing)
	if !refusedTheFlag(err, "limit") {
		t.Errorf("--limit 0 lost to a failing client build (%v); the "+
			"paging check must run BEFORE clientFromCmd, or a bad "+
			"flag is reported as a certificate problem", err)
	}
}
