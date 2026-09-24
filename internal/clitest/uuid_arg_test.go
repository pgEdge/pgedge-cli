package clitest

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
)

// A malformed UUID the caller typed is a bad invocation, so it exits 2
// (#277). Before this, cp exited 2 and byoc exited 1 for the same
// mistake, and 25 hand-rolled parses across byoc and account had each
// picked the general code.
//
// THIS LIST USED TO CARRY A SECOND, OPPOSITE HALF, and its removal is
// the substance of #194. The byoc and managed resolvers accepted an ID
// PREFIX, so there a failed parse was control flow into prefix
// matching and the right answer was 4 rather than 2 — which meant a
// sweep asserting 2 across the tree would have deleted a feature. That
// feature is gone: every ID-taking input takes a full UUID. So the
// verbs that used to be listed as prefix-resolved are now listed here,
// and TestNoIDInputResolvesAPrefix below is what stops the resolvers
// coming back.
//
// The whole list runs WITHOUT CREDENTIALS, which is what makes it
// cheap and what makes it worth having: the parse happens before any
// client is built, so exit 2 is reachable with no server. An entry
// that answered 5 instead would be one whose parse had drifted back
// behind clientFromCmd.
var directUUIDCommands = [][]string{
	{"starfleet", "client", "get", "not-a-uuid"},
	{"starfleet", "client", "delete", "not-a-uuid", "--force"},
	{"starfleet", "invite", "get", "not-a-uuid"},
	{"starfleet", "invite", "delete", "not-a-uuid", "--force"},
	{"starfleet", "membership", "delete", "not-a-uuid", "--force"},
	{"starfleet", "tenant", "get", "not-a-uuid"},
	{"starfleet", "byoc", "cloud-account", "get", "not-a-uuid"},
	{"starfleet", "byoc", "cloud-account", "delete", "not-a-uuid", "--force"},
	{"starfleet", "byoc", "ingress", "get", "not-a-uuid"},
	{"starfleet", "byoc", "ingress", "delete", "not-a-uuid", "--force"},
	{"starfleet", "byoc", "ssh-key", "get", "not-a-uuid"},
	{"starfleet", "byoc", "ssh-key", "delete", "not-a-uuid", "--force"},
	{"starfleet", "byoc", "ingress", "service", "list", "not-a-uuid"},
	{"starfleet", "byoc", "backup-store", "get", "not-a-uuid"},
	{"starfleet", "byoc", "cluster", "share", "list", "not-a-uuid"},
	{"controlplane", "task", "get", "not-a-uuid", "--database", "db"},

	// Added by #194, and these are the ones the old second half
	// covered. Each was previously a prefix candidate: the value went
	// to a list call, so with no credentials the answer was 5.
	{"starfleet", "byoc", "cluster", "get", "not-a-uuid"},
	{"starfleet", "byoc", "cluster", "delete", "not-a-uuid", "--force"},
	{"starfleet", "byoc", "cluster", "metrics", "not-a-uuid"},
	{"starfleet", "byoc", "database", "get", "not-a-uuid"},
	{"starfleet", "byoc", "database", "delete", "not-a-uuid", "--force"},
	{"starfleet", "byoc", "node", "list", "not-a-uuid"},
	{"starfleet", "managed", "database", "get", "not-a-uuid"},
	{"starfleet", "managed", "database", "delete", "not-a-uuid", "--force"},
	{"starfleet", "managed", "database", "logs", "not-a-uuid"},
	{"starfleet", "managed", "database", "metrics", "not-a-uuid"},
	{"starfleet", "managed", "database", "service", "list", "not-a-uuid"},
	{"starfleet", "managed", "backup", "get", "not-a-uuid"},
	{"starfleet", "managed", "backup", "restore", "not-a-uuid", "--force"},

	// #240: --subject-id was forwarded verbatim and the API answered a
	// malformed one with 500 "failed to list tasks".
	{"starfleet", "managed", "task", "list", "--subject-id", "not-a-uuid"},
	{"starfleet", "byoc", "task", "list", "--subject-id", "not-a-uuid"},

	// #257 and #274: --cluster-id reached a request body as a bare
	// string, so a prefix came back as "cluster not found or not
	// available" — which reads like the cluster is busy.
	{"starfleet", "byoc", "database", "create",
		"--name", "mydb", "--cluster-id", "not-a-uuid"},
	{"starfleet", "byoc", "ingress", "create", "--name", "web",
		"--cluster-id", "not-a-uuid", "--region", "us-east-1"},

	// Found by review, and every one of them was green while wrong.
	// `task get`/`task wait` had NO parse at all in either module and
	// forwarded the value into a 400 at exit ONE — not even the code
	// for a bad argument, in the same RunE family as the --subject-id
	// this change fixed. byoc's eight service verbs parsed INSIDE
	// fetchDatabaseWith, after the client, so they answered 5 with no
	// credentials while managed's identical verbs answered 2 — an
	// asymmetry this change introduced. `managed size get` parsed
	// after the client too.
	{"starfleet", "byoc", "task", "get", "not-a-uuid"},
	{"starfleet", "byoc", "task", "wait", "not-a-uuid"},
	{"starfleet", "managed", "task", "get", "not-a-uuid"},
	{"starfleet", "managed", "task", "wait", "not-a-uuid"},
	{"starfleet", "managed", "size", "get", "not-a-uuid"},
	{"starfleet", "byoc", "database", "service", "list", "not-a-uuid"},
	{"starfleet", "byoc", "database", "service", "get",
		"not-a-uuid", "abc12345"},
	{"starfleet", "byoc", "database", "mcp", "deploy", "not-a-uuid"},
	{"starfleet", "byoc", "database", "mcp", "update", "not-a-uuid"},
	{"starfleet", "byoc", "database", "rag", "update", "not-a-uuid"},
	{"starfleet", "byoc", "database", "postgrest", "update", "not-a-uuid"},
}

func runForExit(t *testing.T, args ...string) int {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	rt := &module.Runtime{Stdin: strings.NewReader("")}
	root := cli.NewRootCmd(rt)
	for _, m := range Modules() {
		c, err := m.Command(rt)
		if err != nil {
			t.Fatal(err)
		}
		root.AddCommand(c)
	}
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return cli.ExitCode(root.Execute())
}

// discardedParseRe matches the fast path: a parse whose error is
// tested for SUCCESS and otherwise thrown away.
//
// The error variable's NAME is not part of the contract, and neither
// is a bare argument: `id, parseErr := uuid.Parse(input); parseErr ==
// nil` and `uuid.Parse(strings.TrimSpace(input))` are both the same
// fast path. A first version keyed on the literal `err` and on
// `[^)]*`, so either refactor false-failed.
var discardedParseRe = regexp.MustCompile(
	`uuid\.Parse\(.*\);\s*\w+ == nil`)

// A destructive verb parses its ID BEFORE prompting, so a malformed one
// is refused rather than confirmed and then refused. Review proved the
// other order on a real pty: `Delete database "not-a-uuid"? This cannot
// be undone. [y/N]: y` followed by `invalid database ID`, which asks an
// operator to authorise an operation that could never run.
//
// It reuses example_runs_test.go's runForError rather than a second
// runner: the MESSAGE matters as well as the code here, because
// cli.Confirm's own refusal is a UsageError too.
//
// It cannot be tested by exit code. cli.Confirm's own refusal is a
// UsageError too, so both orders answer 2 — every entry here would pass
// against either. The MESSAGE is the discriminator, and these run
// WITHOUT --force precisely so the confirm path is live: with --force
// the prompt is skipped and the ordering is unobservable, which is why
// directUUIDCommands could not see this.
//
// It also matters beyond tidiness. TestShippedExamplesAreNotMalformed
// waives the destructive-verb refusal as environmental, so with the
// prompt first a shipped example carrying a BAD id passes that gate —
// review demonstrated it by reverting one example. The ordering is what
// puts prompting verbs back inside the example gate's reach.
var destructiveIDCommands = [][]string{
	{"starfleet", "byoc", "cluster", "delete", "not-a-uuid"},
	{"starfleet", "byoc", "cluster", "delete", "not-a-uuid", "--cascade"},
	{"starfleet", "byoc", "database", "delete", "not-a-uuid"},
	{"starfleet", "byoc", "database", "rotate-password", "not-a-uuid",
		"--role", "admin"},
	{"starfleet", "byoc", "database", "restore", "not-a-uuid",
		"--node-name", "n1",
		"--repository", "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"},
	{"starfleet", "managed", "database", "delete", "not-a-uuid"},
	{"starfleet", "managed", "database", "rotate-password", "not-a-uuid",
		"--role", "admin"},
	{"starfleet", "managed", "database", "resize", "not-a-uuid",
		"--size", "large"},
	{"starfleet", "managed", "backup", "restore", "not-a-uuid"},
	{"starfleet", "managed", "database", "service", "remove", "not-a-uuid",
		"mcp"},
}

func TestMalformedIDIsRefusedBeforeThePrompt(t *testing.T) {
	if len(destructiveIDCommands) < 10 {
		t.Fatalf("destructiveIDCommands has %d entries; a shrinking "+
			"list is how this gate stops covering the prompting verbs",
			len(destructiveIDCommands))
	}
	for _, args := range destructiveIDCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runForError(t, args...)
			if err == nil {
				t.Fatalf("`pgedge %s` succeeded",
					strings.Join(args, " "))
			}
			if got := cli.ExitCode(err); got != cli.ExitUsage {
				t.Errorf("exit %d, want %d", got, cli.ExitUsage)
			}
			// The positive assertion. Without it, the confirm
			// refusal satisfies the exit code and this test says
			// nothing about the order.
			if !strings.Contains(err.Error(), "not-a-uuid") {
				t.Errorf("`pgedge %s` answered %q, which does not name "+
					"the bad ID.\nThe parse must precede cli.Confirm: "+
					"otherwise the operator is asked to authorise an "+
					"operation that cannot run, and a shipped example "+
					"with a bad ID slips past "+
					"TestShippedExamplesAreNotMalformed, which waives "+
					"the destructive refusal.",
					strings.Join(args, " "), err.Error())
			}
			if strings.Contains(err.Error(), "destructive") {
				t.Errorf("`pgedge %s` refused as destructive before "+
					"reading its ID: %q", strings.Join(args, " "),
					err.Error())
			}
		})
	}
}

func TestMalformedUUIDArgumentIsAUsageError(t *testing.T) {
	if len(directUUIDCommands) < 44 {
		t.Fatalf("directUUIDCommands has %d entries; a shrinking list "+
			"is how this gate stops covering the sweep it pins",
			len(directUUIDCommands))
	}
	for _, args := range directUUIDCommands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if got := runForExit(t, args...); got != cli.ExitUsage {
				t.Errorf("`pgedge %s` exited %d, want %d.\nA malformed "+
					"UUID the caller typed is a bad invocation. Route "+
					"the parse through cli.ParseUUIDArg rather than "+
					"building a general error (#277).",
					strings.Join(args, " "), got, cli.ExitUsage)
			}
		})
	}
}

// The other half of the contract. Prefixes are WITHDRAWN (#194): every
// ID-taking input takes a full UUID, so a value that is not one is a
// usage error rather than control flow into prefix matching.
//
// This file used to say the opposite, and the reversal is the point.
// The old gate asserted that the resolvers KEPT a bare uuid.Parse
// whose error was discarded, because that discard routed a prefix on
// to matching. Deleting that assertion without adding this one would
// leave nothing at all: a resolver reinstated tomorrow would list, see
// one page, and treat a single match on it as unique -- the false
// unique the ruling was made to remove.
//
// It reads the source, and that is deliberate rather than lazy. A
// runtime assertion is already covered above for the verbs whose parse
// is reachable without credentials; what a runtime test canNOT see is
// a resolver reintroduced BEHIND a client build, where the answer with
// no credentials is exit 5 either way. A first version of the old gate
// made exactly that mistake and passed whatever the code did.
//
// resolveNodeID is the sanctioned exception and is listed as such. A
// node can be NAMED -- `cluster get`'s node objects carry no id field,
// so a name is the only thing a human has -- and names are a separate
// question the ruling left open. So there a failed parse is still
// control flow, and byoc's TestResolveNodeIDTakesAUUIDOrAName pins
// both that a name resolves and that a prefix no longer does.
// prefixMatchRe is how a resolver is spelt: a case-folded HasPrefix
// over a list of ids. Both deleted resolvers were exactly this line,
// and it is what a reinstatement would be.
var prefixMatchRe = regexp.MustCompile(
	`strings\.HasPrefix\(strings\.ToLower\(`)

// TestNoIDInputResolvesAPrefix WALKS the two starfleet modules rather than
// reading a list of files, and that is not tidiness. A first version
// named six paths, and review reinstated a full resolver — with the
// exact spelling this regex keys on — in a SEVENTH file
// (byoc/cmd/database_metrics.go), wired into a live verb, and every
// gate in the repo stayed green while `byoc database metrics <prefix>`
// resolved and sent. A gate over a hand-written file list stops
// covering the tree the moment someone adds a file, which is the
// weakest possible property for a tripwire whose whole job is to stop
// something coming back.
func TestNoIDInputResolvesAPrefix(t *testing.T) {
	roots := []string{"../starfleet/byoc/cmd", "../starfleet/managed/cmd"}
	scanned := 0
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
		for _, e := range entries {
			// Test files are excluded on purpose: a fixture may
			// legitimately spell a prefix match to prove one is
			// REFUSED, and the module resolver tests do.
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
				strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			scanned++
			if loc := prefixMatchRe.FindStringIndex(
				string(raw)); loc != nil {
				t.Errorf("%s: case-folded prefix matching at byte %d. "+
					"ID prefixes are withdrawn: every ID-taking input "+
					"takes a full UUID, checked locally, exit 2 "+
					"(#194). A resolver lists ONE page and calls a "+
					"single match on it unique, so a resource on page "+
					"two is a false unique -- on `database delete` "+
					"that is a destructive act on a resource the "+
					"caller never named. If this is a deliberate "+
					"reversal, the ROADMAP row carries what doing it "+
					"properly requires.", path, loc[0])
			}
		}
	}
	// A walk that found nothing looks exactly like a walk that found
	// no violations. Both module directories are dozens of files.
	if scanned < 40 {
		t.Fatalf("scanned only %d non-test files across %v — the walk "+
			"has broken and this gate would pass without reading the "+
			"code it exists to read", scanned, roots)
	}
}

// The exception, asserted rather than assumed. A gate that only forbids
// prefix matching says nothing about the one resolver that must SURVIVE,
// and deleting resolveNodeID outright would leave this file green --
// which is how the ID-prefix fallback was once deleted with every test
// in the repo still passing.
func TestNodeNameResolutionSurvives(t *testing.T) {
	const path = "../starfleet/byoc/cmd/resolve.go"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(raw)
	if !strings.Contains(src, "func resolveNodeID(") {
		t.Fatalf("%s: resolveNodeID is gone. A node can only be named "+
			"-- `cluster get`'s node objects carry no id -- so its "+
			"name resolution is not part of the prefix withdrawal "+
			"(#194). If it was renamed, update this gate.", path)
	}
	// The discarded parse is what routes a name on to matching, and
	// the byoc package's own test proves the matching still works.
	if !discardedParseRe.MatchString(src) {
		t.Errorf("%s: resolveNodeID no longer contains the "+
			"discarded-parse form that sends a NAME on to matching. "+
			"If you refactored the fast path and names still resolve "+
			"-- TestResolveNodeIDTakesAUUIDOrAName is what would tell "+
			"you -- update the pattern rather than the code.", path)
	}
}
