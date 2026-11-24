package cli_test

import (
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/spf13/cobra"
)

func TestUsageHint(t *testing.T) {
	run := func(*cobra.Command, []string) {}
	leaf := func(use, long string) *cobra.Command {
		return &cobra.Command{Use: use, Long: long, Run: run}
	}
	tree := func(c *cobra.Command) *cobra.Command {
		root := &cobra.Command{Use: "pgedge"}
		group := &cobra.Command{Use: "thing", Run: run}
		root.AddCommand(group)
		group.AddCommand(c)
		return c
	}

	hybrid := leaf("restore <id>", "")
	hybrid.AddCommand(leaf("template", ""))
	tree(hybrid)

	group := &cobra.Command{Use: "grp", Run: run}
	group.AddCommand(leaf("list", ""), leaf("get", ""),
		&cobra.Command{Use: "secret", Hidden: true, Run: run})
	tree(group)

	cases := []struct {
		name string
		cmd  *cobra.Command
		want string
	}{{
		name: "nil command",
		want: "",
	}, {
		name: "leaf prints usage and its first example",
		cmd: tree(leaf("get <id>", `Get a thing.

Example:
  pgedge thing get abc
  pgedge thing get def`)),
		want: "Usage: pgedge thing get <id>\n" +
			"Example: pgedge thing get abc\n",
	}, {
		name: "an example of another command is skipped",
		cmd: tree(leaf("wait <id>", `Wait.

Example:
  pgedge thing create
  pgedge thing wait abc`)),
		want: "Usage: pgedge thing wait <id>\n" +
			"Example: pgedge thing wait abc\n",
	}, {
		name: "continuation lines join into one",
		cmd: tree(leaf("make", `Make.

Example:
  pgedge thing make \
    --size small
  pgedge thing make`)),
		want: "Usage: pgedge thing make\n" +
			"Example: pgedge thing make --size small\n",
	}, {
		name: "a prefix-sharing sibling is not this command",
		cmd: tree(leaf("get", `Get.

Example:
  pgedge thing get-all
  pgedge thing get`)),
		want: "Usage: pgedge thing get\n" +
			"Example: pgedge thing get\n",
	}, {
		name: "no example prints usage alone",
		cmd:  tree(leaf("bare", "No example here.")),
		want: "Usage: pgedge thing bare\n",
	}, {
		name: "group lists visible subcommands",
		cmd:  group,
		want: "Usage: pgedge thing grp <command>\n" +
			"Commands: get, list\n",
	}, {
		name: "group with a positional keeps its usage line",
		cmd:  hybrid,
		want: "Usage: pgedge thing restore <id>\n" +
			"Commands: template\n",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.UsageHint(tc.cmd); got != tc.want {
				t.Errorf("UsageHint =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}
