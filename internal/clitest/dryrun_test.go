package clitest

import (
	"sort"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
	"github.com/spf13/cobra"
)

// mutatingVerbNames are final path elements that change server state.
//
// This list is the independent half of the gate. Without it, the check
// would only ever be "the annotation and the flag agree", which a verb
// carrying neither satisfies trivially — the exact way a new mutating
// verb would ship with no dry-run support and nothing would notice.
var mutatingVerbNames = map[string]bool{
	"accept":          true,
	"add":             true,
	"api":             true,
	"backup":          true,
	"cancel":          true,
	"clear":           true,
	"create":          true,
	"delete":          true,
	"deploy":          true,
	"deregister":      true,
	"failover":        true,
	"init":            true,
	"join":            true,
	"open":            true,
	"register":        true,
	"remove":          true,
	"resize":          true,
	"restart":         true,
	"restore":         true,
	"rotate-password": true,
	"set":             true,
	"start":           true,
	"stop":            true,
	"switchover":      true,
	"update":          true,
	"upgrade":         true,
}

// nonMutatingExceptions are commands whose final path element is in
// mutatingVerbNames but which send no write. Each would carry a reason,
// because an exemption is a decision on the record rather than an
// omission — the same rule the doc-gate markers follow.
//
// Five of the six local-state writers that are out of scope by
// decision — `profile use`, `starfleet auth login`/`logout` and
// `completion install`/`uninstall` — need no entry, because none of
// them ends in one of these names and so nothing flags them in the
// first place. Nor does `controlplane database restore template`, which
// prints a template and ends in "template". The sixth, `cp config
// set`, does need an entry below, because "set" is in
// mutatingVerbNames.
//
// TestDryRunExceptionsAllExist holds every entry to being both real and
// necessary.
var nonMutatingExceptions = map[string]string{
	"pgedge controlplane database init": "prints a commented starter spec to " +
		"stdout and issues no write at all; its only network traffic " +
		"is the GET-based orchestrator detection, so --dry-run there " +
		"would be an accepted flag that does nothing",
	"pgedge self update": "swaps the local pgedge binary; it never " +
		"writes to a pgEdge API, so --dry-run's meaning here (client-" +
		"side checks against the API run, only the write is stopped) " +
		"has nothing to attach to — the same reason the local-state " +
		"writers named above this map need no entry, except this " +
		"one's final path element happens to collide with a " +
		"monitored verb name",
	"pgedge controlplane config set": "writes only the local " +
		"~/.pgedge/cli/config.yaml profile and reaches no pgEdge API, " +
		"the same reason the other local-state writers above need no " +
		"entry — except this one's final path element now collides " +
		"with the monitored \"set\" verb name",
}

// actionCommands returns every command a user can actually invoke,
// keyed by path.
//
// "Has no children" would be the obvious test and is the wrong one:
// `pgedge controlplane database restore <database_id>` is a real mutating action
// that ALSO carries a `template` subcommand, so a leaves-only walk drops
// precisely one of the verbs this gate exists to cover. Runnable-and-not-
// a-pure-router is the same predicate internal/testsupport already uses
// to tell that hybrid from a group.
func actionCommands(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*cobra.Command{}
	walk(root, func(c *cobra.Command) {
		if !c.HasParent() {
			return // root
		}
		if testsupport.IsPureRouter(c) {
			return
		}
		if c.RunE == nil && c.Run == nil {
			return
		}
		out[c.CommandPath()] = c
	})
	return out
}

// finalElement is the verb: the last space-separated token of the path.
func finalElement(path string) string {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// TestEveryMutatingVerbIsAnnotated is the verb-name-implies-annotation
// direction. It is what catches a mutating verb added without
// cli.MarkMutating.
func TestEveryMutatingVerbIsAnnotated(t *testing.T) {
	var missing []string
	for path, c := range actionCommands(t) {
		if !mutatingVerbNames[finalElement(path)] {
			continue
		}
		if _, excused := nonMutatingExceptions[path]; excused {
			continue
		}
		if !cli.IsMutating(c) {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d mutating verb(s) are not marked with "+
			"cli.MarkMutating, so they have no --dry-run:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestEveryAnnotatedLeafCarriesDryRun is the annotation-implies-flag
// direction. MarkMutating does both at once, so this can only fail if
// somebody sets the annotation by hand.
func TestEveryAnnotatedLeafCarriesDryRun(t *testing.T) {
	for path, c := range actionCommands(t) {
		if !cli.IsMutating(c) {
			continue
		}
		f := c.Flags().Lookup(cli.DryRunFlag)
		if f == nil {
			t.Errorf("%s is annotated as mutating but has no --%s; "+
				"set the annotation through cli.MarkMutating, which "+
				"registers both", path, cli.DryRunFlag)
			continue
		}
		if f.NoOptDefVal == "" {
			t.Errorf("%s: --%s has no NoOptDefVal, so the bare form "+
				"demands a value", path, cli.DryRunFlag)
		}
		if f.Value.Type() != "string" {
			t.Errorf("%s: --%s is a %s; it must be a string so "+
				"--dry-run=true is never a valid spelling",
				path, cli.DryRunFlag, f.Value.Type())
		}
	}
}

// TestNoUnrecognisedVerbIsAnnotated is the reverse of
// TestEveryMutatingVerbIsAnnotated, and it is what catches
// OVER-annotation: a router or a read verb given --dry-run by mistake.
//
// Without it the three other directions are all satisfied by marking
// everything, since MarkMutating sets the annotation and the flag
// together and nothing else would object.
//
// A genuinely mutating verb with a name not yet in mutatingVerbNames
// fails here, and the fix is to add the name — the list is the contract
// for what "mutating" means, so extending it is a deliberate act.
//
// THE LIMIT, STATED PLAINLY: this gate keys on the verb NAME, so a new
// mutating verb called something not in the list (`promote`, `sync`,
// `apply`, `attach`, `scale`, `revoke`, …) satisfies every direction
// here while carrying no --dry-run. Verified by mutation: adding a
// `controlplane cluster promote` that really writes, with no MarkMutating, passes
// all of these.
//
// TestNoMutatingRequestEscapesADryRun does not depend on naming, but it
// is NOT a backstop for this: it iterates the ANNOTATED verbs, so an
// unannotated one is invisible to it too. Closing the hole properly
// means either extending that sweep to every runnable command — which
// needs a way to tell a read verb's request from a write verb's without
// asking the annotation — or adding the new verb name here. Adding the
// name is the cheap, deliberate act; the list is the contract for what
// "mutating" means.
func TestNoUnrecognisedVerbIsAnnotated(t *testing.T) {
	var unexpected []string
	for path, c := range actionCommands(t) {
		if !cli.IsMutating(c) {
			continue
		}
		if !mutatingVerbNames[finalElement(path)] {
			unexpected = append(unexpected, path)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("%d command(s) are marked mutating but their verb is "+
			"not in mutatingVerbNames — either they do not write, or "+
			"the name belongs in the list:\n  %s",
			len(unexpected), strings.Join(unexpected, "\n  "))
	}
}

// TestNoReadOnlyLeafCarriesDryRun is the flag-implies-annotation
// direction, and it is what enforces the scope decision: on a read verb
// --dry-run must be an unknown flag (exit 2), not an accepted no-op.
func TestNoReadOnlyLeafCarriesDryRun(t *testing.T) {
	for path, c := range actionCommands(t) {
		if cli.IsMutating(c) {
			continue
		}
		if f := c.Flags().Lookup(cli.DryRunFlag); f != nil {
			t.Errorf("%s is not annotated as mutating but registers "+
				"--%s; an accepted-but-inert flag is worse than an "+
				"unknown one", path, cli.DryRunFlag)
		}
	}
}

// TestNoPureRouterIsAnnotated closes the blind spot the other four
// checks share: they all iterate actionCommands, which skips pure
// routers, so a GROUP marked mutating would be invisible to every one of
// them while still carrying a --dry-run flag in its help.
//
// This is not hypothetical. `starfleet managed backup` and `starfleet byoc
// backup` are routers whose Use is "backup" — a name that reads exactly
// like the mutating verb `controlplane database node backup` — and both were
// marked by a first pass that matched on the name alone.
func TestNoPureRouterIsAnnotated(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}
	var marked []string
	walk(root, func(c *cobra.Command) {
		if !testsupport.IsPureRouter(c) {
			return
		}
		if cli.IsMutating(c) || c.Flags().Lookup(cli.DryRunFlag) != nil {
			marked = append(marked, c.CommandPath())
		}
	})
	sort.Strings(marked)
	if len(marked) > 0 {
		t.Errorf("%d router(s) are marked mutating; a group sends "+
			"nothing, so --dry-run there is a flag that cannot "+
			"work:\n  %s", len(marked), strings.Join(marked, "\n  "))
	}
}

// TestDryRunExceptionsAllExist stops the exemption table from rotting.
// An entry that excuses nothing is an error, exactly as an unused
// doc-gate marker is.
func TestDryRunExceptionsAllExist(t *testing.T) {
	leaves := actionCommands(t)
	for path, reason := range nonMutatingExceptions {
		c, ok := leaves[path]
		if !ok {
			t.Errorf("exemption for %q excuses nothing: no such leaf "+
				"in the tree", path)
			continue
		}
		if reason == "" {
			t.Errorf("%s: exemption carries no reason", path)
		}
		if !mutatingVerbNames[finalElement(path)] {
			t.Errorf("%s: exemption is unnecessary — %q is not in "+
				"mutatingVerbNames, so nothing would have flagged it",
				path, finalElement(path))
		}
		// An exempt leaf must actually be unannotated: an entry here
		// beside a MarkMutating call is two sources disagreeing.
		if cli.IsMutating(c) {
			t.Errorf("%s is both exempted and annotated as mutating; "+
				"remove one", path)
		}
	}
}

// TestMutatingVerbCountIsSane is a positive control. Every check above
// is satisfied by an empty tree, and a FullTree() that silently returned
// a root with no children would make the whole file pass while testing
// nothing.
func TestMutatingVerbCountIsSane(t *testing.T) {
	annotated := 0
	for _, c := range actionCommands(t) {
		if cli.IsMutating(c) {
			annotated++
		}
	}
	// An EQUALITY, deliberately, not a floor. The first version used a
	// floor of 60 against a census of 65, which would have let five
	// annotations vanish silently — and the whole point of this check is
	// to be the positive control the other five cannot be, since every
	// one of them passes against an empty tree.
	//
	// Adding or removing a verb is expected to fail here. Bump the
	// number in the same commit: that makes the count a reviewed fact
	// rather than a range nobody looks at.
	//
	// 67 -> 72: the five mutating `managed database allowlist` verbs
	// (add, remove, set, open, clear); `get` is read-only and
	// unannotated.
	// 72 -> 74: `managed database branch create` and `... delete`;
	// `list` and `get` are read-only and unannotated.
	const census = 74
	if annotated != census {
		t.Errorf("%d annotated mutating commands, want exactly %d. If "+
			"you added or removed a verb, update this number in the "+
			"same commit; if you did not, an annotation moved on its "+
			"own", annotated, census)
	}
}
