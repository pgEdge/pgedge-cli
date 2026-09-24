package clitest

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
)

// --display-name was governed by two different rules and stated
// neither. `managed database create` took 40 characters while
// `managed database update` refused 26, so a database the CLI created
// could hold a name update would never accept back — including the one
// it already had.
//
// Both verbs, both products, one limit, checked before the request.
// byoc `database create` takes no --display-name, which is why there
// are three rows rather than four.
//
// TestEveryDisplayNameFlagIsChecked is what makes that count safe: it
// walks the tree and fails when any command carries the flag without
// appearing here. The API already accepts display_name on byoc create
// (openapi/byoc.yaml declares it with maxLength 25), so a fourth site
// is one small change away, and a list of three with nothing holding
// it would silently stop covering the tree.
var displayNameCommands = [][]string{
	{"starfleet", "managed", "database", "create",
		"--name", "dn1", "--size", "small", "--region", "us-east-2"},
	{"starfleet", "managed", "database", "update",
		"3f2a9c1e-0000-4000-8000-000000000000"},
	// The PLURAL alias, deliberately: Find() resolves it where the
	// hand-rolled path derivation this test used to carry did not.
	// The argument was `mydb` for the same reason -- byoc's own
	// documented example spelled it that way -- until every ID input
	// came to take a full UUID, which turned this row's control case
	// into an exit 2 that had nothing to do with --display-name.
	{"starfleet", "byoc", "databases", "update",
		"3f2a9c1e-0000-4000-8000-000000000000"},
	{"starfleet", "managed", "database", "branch", "create",
		"3f2a9c1e-0000-4000-8000-000000000000"},
}

func TestDisplayNameOverTheLimitIsAUsageError(t *testing.T) {
	tooLong := strings.Repeat("a", conn.DisplayNameMaxLen+1)
	atLimit := strings.Repeat("a", conn.DisplayNameMaxLen)

	for _, base := range displayNameCommands {
		name := strings.Join(base, " ")
		t.Run(name+" (over)", func(t *testing.T) {
			args := append(append([]string{}, base...),
				"--display-name", tooLong)
			if got := runForExit(t, args...); got != cli.ExitUsage {
				t.Errorf("exit = %d, want %d for a %d-character name",
					got, cli.ExitUsage, conn.DisplayNameMaxLen+1)
			}
		})
		// The control. Without it, a row whose other flags were wrong
		// would exit 2 from cobra and the case above would pass having
		// tested nothing — and "fixing" the limit to zero would look
		// like a fix.
		t.Run(name+" (at the limit)", func(t *testing.T) {
			args := append(append([]string{}, base...),
				"--display-name", atLimit)
			if got := runForExit(t, args...); got == cli.ExitUsage {
				t.Errorf("exit = 2 for a name AT the %d-character "+
					"limit; the CLI is refusing what the API accepts",
					conn.DisplayNameMaxLen)
			}
		})
	}
}

// TestEveryDisplayNameFlagIsChecked walks the whole command tree and
// fails when a command takes --display-name but is not in
// displayNameCommands. A count in a comment is not a guard; this is.
func TestEveryDisplayNameFlagIsChecked(t *testing.T) {
	root, err := FullTree()
	if err != nil {
		t.Fatal(err)
	}

	// cobra's own resolver, not a hand-rolled one. An earlier version
	// derived the path from a row's args by stopping at the first flag
	// or at a token that "looks like a UUID", which got six of eight
	// adversarial inputs wrong -- including this repo's mandated
	// plural aliases and byoc's own `databases update` alias.
	// It could not produce a vacuous PASS (arm 1 is an exact
	// membership test, so a mis-derived key can only fail to cover a
	// real command), but it produced a false FAILURE whose message
	// pointed at the list rather than the derivation -- and the
	// obvious "fix" for it trips arm 2 with a second false message.
	// Find() also gives a free extra signal: a row naming no real
	// command now says so.
	covered := map[string]bool{}
	for _, base := range displayNameCommands {
		c, _, ferr := root.Find(base)
		if ferr != nil {
			t.Fatalf("row %v names no command: %v", base, ferr)
		}
		covered[strings.TrimPrefix(c.CommandPath(), "pgedge ")] = true
	}

	found := 0
	walk(root, func(c *cobra.Command) {
		if c.Flags().Lookup("display-name") == nil {
			return
		}
		found++
		path := strings.TrimPrefix(c.CommandPath(), "pgedge ")
		if !covered[path] {
			t.Errorf("%q takes --display-name but is not in "+
				"displayNameCommands, so no test checks its limit",
				path)
		}
	})
	if found != len(displayNameCommands) {
		t.Errorf("the tree has %d commands taking --display-name and "+
			"displayNameCommands lists %d; the gate and the tree have "+
			"drifted", found, len(displayNameCommands))
	}
}
