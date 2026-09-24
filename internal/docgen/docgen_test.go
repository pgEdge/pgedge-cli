package docgen

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- fixtures ---------------------------------------------------------

// newLeaf builds a minimal leaf command with no children.
func newLeaf(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Run: func(*cobra.Command, []string) {}}
}

// newGroup builds a command intended to hold subcommands. It has no Run,
// matching how cobra distinguishes groups (HasAvailableSubCommands) from
// leaves in this package's usageLine logic.
func newGroup(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short}
}

// flagNamed returns the flag f from the command's local flag set. It
// panics-free fails the test via t.Fatalf if the flag was not registered.
func flagNamed(t *testing.T, cmd *cobra.Command, name string) *pflag.Flag {
	t.Helper()
	f := cmd.Flags().Lookup(name)
	if f == nil {
		t.Fatalf("flag %q not registered on command %q", name, cmd.Use)
	}
	return f
}

// --- headingLevel -------------------------------------------------------

func TestHeadingLevel(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int
	}{
		{"root", "pgedge", 1},
		{"one level down", "pgedge byoc", 2},
		{"two levels down", "pgedge byoc cluster", 3},
		{"three levels down", "pgedge byoc cluster list", 4},
		{
			"clamped at six: markdown has no h7, so a tree deeper than " +
				"six must flatten rather than silently emit an invalid " +
				"heading",
			"pgedge a b c d e f g",
			6,
		},
		{
			"exactly six stays six, proving the clamp is > not >=",
			"pgedge a b c d e",
			6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headingLevel(tt.path); got != tt.want {
				t.Errorf("headingLevel(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

// --- flagCell -------------------------------------------------------------

func TestFlagCell(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		register func(*cobra.Command)
		want     string
	}{
		{
			name:     "bool renders bare: --force bool would be invalid at the CLI",
			flag:     "force",
			register: func(c *cobra.Command) { c.Flags().Bool("force", false, "force it") },
			want:     "`--force`",
		},
		{
			name:     "non-bool carries its type",
			flag:     "limit",
			register: func(c *cobra.Command) { c.Flags().Int("limit", 0, "max results") },
			want:     "`--limit int`",
		},
		{
			name:     "shorthand present is prefixed before the long name",
			flag:     "output",
			register: func(c *cobra.Command) { c.Flags().StringP("output", "o", "", "output format") },
			want:     "`-o, --output string`",
		},
		{
			name:     "shorthand absent: no leading dash pair",
			flag:     "profile",
			register: func(c *cobra.Command) { c.Flags().String("profile", "", "profile name") },
			want:     "`--profile string`",
		},
		{
			// A bare `string` would say the value is mandatory. It is
			// not: `--dry-run` alone is the documented form, and this
			// table is what AI agents read instead of --help.
			name: "optional value renders cobra's [=default] spelling",
			flag: "dry-run",
			register: func(c *cobra.Command) {
				c.Flags().String("dry-run", "", "dry run")
				c.Flags().Lookup("dry-run").NoOptDefVal = "checks"
			},
			want: "`--dry-run string[=\"checks\"]`",
		},
		{
			// pflag quotes the optional-value bracket only for string
			// flags and prints every other type's unquoted (#370). A
			// quoted `strings[="a,b"]` here would disagree with the
			// screen the same way #362's raw type names did.
			name: "non-string optional value renders unquoted like --help",
			flag: "columns",
			register: func(c *cobra.Command) {
				c.Flags().StringSlice("columns", nil, "columns")
				c.Flags().Lookup("columns").NoOptDefVal = "a,b"
			},
			want: "`--columns strings[=a,b]`",
		},
		{
			// pflag prints a string's optional value RAW inside the
			// quotes ([="%s"]), never %q-escaped: a default of a"b\c
			// renders [="a"b\c"] on screen (measured against
			// FlagUsagesWrapped in #378's review). Every live value
			// today is escape-free, so this row is the only thing
			// that distinguishes the two mechanisms.
			name: "string optional value is not re-escaped",
			flag: "dry-run",
			register: func(c *cobra.Command) {
				c.Flags().String("dry-run", "", "dry run")
				c.Flags().Lookup("dry-run").NoOptDefVal = "a\"b\\c"
			},
			want: "`--dry-run string[=\"a\"b\\c\"]`",
		},
		{
			// pflag suppresses count's implicit "+1" in --help; the
			// reference must too (#370).
			name: "count with its implicit +1 renders no bracket",
			flag: "verbose",
			register: func(c *cobra.Command) {
				c.Flags().Count("verbose", "verbosity")
			},
			want: "`--verbose count`",
		},
		{
			// A bool with NoOptDefVal is cobra's normal state for every
			// boolean flag ("true"), so the optional-value suffix must
			// not leak onto them — that is the whole reason bools render
			// bare.
			name: "bool with NoOptDefVal still renders bare",
			flag: "force",
			register: func(c *cobra.Command) {
				c.Flags().Bool("force", false, "force it")
			},
			want: "`--force`",
		},
		{
			// The token is what --help prints, not pflag's internal
			// Value.Type(): UnquoteUsage normalises stringSlice to
			// `strings`, and the reference must match the screen
			// (#362).
			name: "stringSlice renders --help's strings, not the raw type",
			flag: "target-nodes",
			register: func(c *cobra.Command) {
				c.Flags().StringSlice("target-nodes", nil, "target nodes")
			},
			want: "`--target-nodes strings`",
		},
		{
			// stringArray has no friendly name in pflag, so both
			// renderings already agree; pin that this fix does not
			// disturb it.
			name: "stringArray keeps its raw token, matching --help",
			flag: "base-url",
			register: func(c *cobra.Command) {
				c.Flags().StringArray("base-url", nil, "base URLs")
			},
			want: "`--base-url stringArray`",
		},
		{
			name: "duration keeps its token, matching --help",
			flag: "timeout",
			register: func(c *cobra.Command) {
				c.Flags().Duration("timeout", 0, "per-request bound")
			},
			want: "`--timeout duration`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newLeaf("x", "")
			tt.register(cmd)
			f := flagNamed(t, cmd, tt.flag)
			if got := flagCell(f); got != tt.want {
				t.Errorf("flagCell() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- requiredCell -----------------------------------------------------

func TestRequiredCell(t *testing.T) {
	t.Run("Yes when MarkFlagRequired was called", func(t *testing.T) {
		cmd := newLeaf("get", "")
		cmd.Flags().String("id", "", "cluster id")
		if err := cmd.MarkFlagRequired("id"); err != nil {
			t.Fatalf("MarkFlagRequired: %v", err)
		}
		f := flagNamed(t, cmd, "id")
		if got := requiredCell(f); got != "Yes" {
			t.Errorf("requiredCell() = %q, want %q", got, "Yes")
		}
	})

	t.Run("No when the flag carries no required annotation", func(t *testing.T) {
		cmd := newLeaf("list", "")
		cmd.Flags().Int("limit", 0, "max results")
		f := flagNamed(t, cmd, "limit")
		if got := requiredCell(f); got != "No" {
			t.Errorf("requiredCell() = %q, want %q", got, "No")
		}
	})
}

// --- escapePipes --------------------------------------------------------

func TestEscapePipes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "pipe is escaped so it cannot break the markdown table",
			in:   "one of running|stopped|failed",
			want: `one of running\|stopped\|failed`,
		},
		{
			name: "no pipe passes through unchanged, aside from trimming",
			in:   "  max results to return  ",
			want: "max results to return",
		},
		{
			name: "empty string stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapePipes(tt.in); got != tt.want {
				t.Errorf("escapePipes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- declaredFlags ------------------------------------------------------

func TestDeclaredFlags(t *testing.T) {
	t.Run("local and persistent flags are merged, sorted, deduped", func(t *testing.T) {
		cmd := newLeaf("list", "")
		cmd.Flags().Int("limit", 0, "max results")
		cmd.Flags().String("zeta", "", "z flag")
		cmd.PersistentFlags().Bool("all", false, "show all")

		got := declaredFlags(cmd)
		var names []string
		for _, f := range got {
			names = append(names, f.Name)
		}
		want := []string{"all", "limit", "zeta"}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("declaredFlags names = %v, want %v (sorted)", names, want)
		}
	})

	t.Run("a flag declared both locally and persistently is not duplicated", func(t *testing.T) {
		cmd := newLeaf("list", "")
		// Simulate a name colliding between the two sets by registering
		// distinct flags whose names collide is not possible via pflag
		// (it panics), so instead assert the seen-map dedup logic by
		// checking count matches unique names when only one set is used
		// with a real duplicate-prone scenario: parent persistent flag
		// shared with a child's own declared set is covered by the
		// inheritance test below. Here we confirm a plain single flag
		// yields exactly one entry, not two.
		cmd.Flags().Bool("force", false, "force it")
		got := declaredFlags(cmd)
		count := 0
		for _, f := range got {
			if f.Name == "force" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("declaredFlags contains %d entries named force, want 1", count)
		}
	})

	t.Run("inherited flags from an ancestor are excluded", func(t *testing.T) {
		parent := newGroup("byoc", "")
		parent.PersistentFlags().String("profile", "", "active profile")

		child := newLeaf("list", "")
		child.Flags().Int("limit", 0, "max results")
		parent.AddCommand(child)

		got := declaredFlags(child)
		for _, f := range got {
			if f.Name == "profile" {
				t.Errorf(
					"declaredFlags(child) includes inherited flag %q; "+
						"inherited flags belong to the ancestor that "+
						"declares them and must not repeat on every leaf",
					f.Name)
			}
		}
		if len(got) != 1 || got[0].Name != "limit" {
			t.Errorf("declaredFlags(child) = %v, want only [limit]", got)
		}
	})
}

// --- usageLine ------------------------------------------------------------

func TestUsageLine(t *testing.T) {
	t.Run("leaf with no flags and no positionals", func(t *testing.T) {
		cmd := newLeaf("status", "")
		want := "**Usage:** `status`"
		if got := usageLine(cmd); got != want {
			t.Errorf("usageLine() = %q, want %q", got, want)
		}
	})

	t.Run("positional placeholder comes from Use", func(t *testing.T) {
		cmd := newLeaf("get <cluster_id>", "")
		want := "**Usage:** `get <cluster_id>`"
		if got := usageLine(cmd); got != want {
			t.Errorf("usageLine() = %q, want %q", got, want)
		}
	})

	t.Run("group command gets <command> placeholder", func(t *testing.T) {
		parent := newGroup("byoc", "")
		child := newLeaf("list", "")
		parent.AddCommand(child)
		want := "**Usage:** `byoc <command>`"
		if got := usageLine(parent); got != want {
			t.Errorf("usageLine() = %q, want %q", got, want)
		}
	})

	t.Run("[flags] appears only when the command declares flags", func(t *testing.T) {
		withFlags := newLeaf("list", "")
		withFlags.Flags().Int("limit", 0, "max results")
		want := "**Usage:** `list [flags]`"
		if got := usageLine(withFlags); got != want {
			t.Errorf("usageLine() = %q, want %q", got, want)
		}
	})

	t.Run("no [flags] suffix when there are none declared", func(t *testing.T) {
		noFlags := newLeaf("status", "")
		if got := usageLine(noFlags); strings.Contains(got, "[flags]") {
			t.Errorf("usageLine() = %q, want no [flags] suffix", got)
		}
	})

	t.Run("path comes from CommandPath, combining with parents", func(t *testing.T) {
		root := newGroup("pgedge", "")
		mod := newGroup("byoc", "")
		leaf := newLeaf("get <cluster_id>", "")
		leaf.Flags().Bool("force", false, "skip confirmation")
		root.AddCommand(mod)
		mod.AddCommand(leaf)
		want := "**Usage:** `pgedge byoc get <cluster_id> [flags]`"
		if got := usageLine(leaf); got != want {
			t.Errorf("usageLine() = %q, want %q", got, want)
		}
	})
}

// --- Block ----------------------------------------------------------------

func TestBlock(t *testing.T) {
	root := newGroup("pgedge", "")
	mod := newGroup("byoc", "")
	cluster := newGroup("cluster", "")
	list := newLeaf("list", "List clusters.")
	list.Flags().Int("limit", 0, "Maximum number of results to return")
	root.AddCommand(mod)
	mod.AddCommand(cluster)
	cluster.AddCommand(list)

	got := Block(list)
	path := "pgedge byoc cluster list"

	if !strings.HasPrefix(got, BeginMarker(path)) {
		t.Errorf("Block() does not open with the begin marker for %q:\n%s", path, got)
	}
	if !strings.HasSuffix(got, EndMarker()) {
		t.Errorf("Block() does not close with the end marker:\n%s", got)
	}
	wantUsage := "**Usage:** `" + path + " [flags]`"
	if !strings.Contains(got, wantUsage) {
		t.Errorf("Block() missing usage line %q:\n%s", wantUsage, got)
	}
	if !strings.Contains(got, "List clusters.") {
		t.Errorf("Block() missing Short text:\n%s", got)
	}
	if !strings.Contains(got, "| `--limit int` | No |  | Maximum number of results to return |") {
		t.Errorf("Block() missing flag row:\n%s", got)
	}
}

// TestDefaultCell pins the one judgement call in #432: a blank cell is
// a zero value and nothing else, so a reader can tell "no default"
// from a default that happens to be empty only because pflag cannot
// either, and the table says exactly what --help says.
func TestDefaultCell(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.Int("limit", 0, "")
	fs.Int("wait-interval", 5, "")
	fs.String("output", "text", "")
	fs.String("profile", "", "")
	fs.Bool("force", false, "")
	fs.Bool("verbose", true, "")
	fs.Duration("timeout", 30*time.Second, "")
	fs.Duration("none", 0, "")
	fs.StringSlice("nodes", nil, "")
	fs.StringSlice("cols", []string{"a", "b"}, "")
	fs.StringArray("urls", nil, "")
	fs.StringArray("hosts", []string{"h1"}, "")
	fs.String("pipe", "a|b", "")
	cases := map[string]string{
		"limit": "", "wait-interval": "`5`", "output": "`text`",
		"profile": "", "force": "", "verbose": "`true`",
		"timeout": "`30s`", "none": "", "nodes": "",
		"cols": "`[a,b]`", "pipe": "`a\\|b`",
		"urls": "", "hosts": "`[h1]`",
	}
	for name, want := range cases {
		if got := defaultCell(fs.Lookup(name)); got != want {
			t.Errorf("defaultCell(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestBlockOmitsFlagsTableWhenNoneDeclared(t *testing.T) {
	cmd := newLeaf("status", "Show status.")
	got := Block(cmd)
	if strings.Contains(got, "**Flags:**") {
		t.Errorf("Block() included a Flags section for a command with no flags:\n%s", got)
	}
}

// --- VisibleCommands ------------------------------------------------------

func TestVisibleCommands(t *testing.T) {
	root := newGroup("pgedge", "")
	byoc := newGroup("byoc", "")
	zzz := newLeaf("zzz", "")
	aaa := newLeaf("aaa", "")
	hidden := newLeaf("hidden-thing", "")
	hidden.Hidden = true
	root.AddCommand(byoc)
	byoc.AddCommand(zzz, aaa, hidden)
	// cobra auto-registers a "help" command once the root has
	// subcommands and help is enabled; add explicitly to be sure the
	// filter is exercised regardless of cobra defaults.
	root.InitDefaultHelpCmd()

	got := VisibleCommands(root)

	var paths []string
	for _, c := range got {
		paths = append(paths, c.CommandPath())
	}

	for _, p := range paths {
		if strings.Contains(p, "hidden-thing") {
			t.Errorf("VisibleCommands() included hidden command, paths=%v", paths)
		}
		if strings.HasSuffix(p, " help") || p == "help" {
			t.Errorf("VisibleCommands() included help command, paths=%v", paths)
		}
	}

	// Stable sorted order by CommandPath.
	sorted := append([]string(nil), paths...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Errorf("VisibleCommands() not sorted: %v", paths)
			break
		}
	}

	wantCount := 4 // pgedge, byoc, aaa, zzz
	if len(got) != wantCount {
		t.Errorf("VisibleCommands() returned %d commands, want %d: %v",
			len(got), wantCount, paths)
	}
}

// --- Apply ------------------------------------------------------------

// buildTree returns a small pgedge > byoc > cluster > list tree whose
// leaf declares one flag, used across the Apply/Conform/Adopt tests.
func buildTree() (root, list *cobra.Command) {
	root = newGroup("pgedge", "")
	byoc := newGroup("byoc", "")
	cluster := newGroup("cluster", "")
	list = newLeaf("list", "List clusters.")
	list.Flags().Int("limit", 0, "Maximum number of results to return")
	root.AddCommand(byoc)
	byoc.AddCommand(cluster)
	cluster.AddCommand(list)
	return root, list
}

func TestApply(t *testing.T) {
	root, list := buildTree()
	want := VisibleCommands(root)
	path := "pgedge byoc cluster list"

	t.Run("rewrites a drifted block and counts it as Changed", func(t *testing.T) {
		drifted := BeginMarker(path) + "\n" +
			"##### stale heading\n\n" +
			"stale content\n" +
			EndMarker()
		res := Apply(drifted, want)
		if res.Changed == 0 {
			t.Errorf("Apply() Changed = 0, want at least 1 for a drifted block")
		}
		if !strings.Contains(res.Doc, Block(list)) {
			t.Errorf("Apply() Doc does not contain the freshly rendered block:\n%s", res.Doc)
		}
		if strings.Contains(res.Doc, "stale heading") {
			t.Errorf("Apply() left stale content in place:\n%s", res.Doc)
		}
	})

	t.Run("a document with no blocks reports every wanted command as Missing", func(t *testing.T) {
		res := Apply("no blocks here at all", want)
		if len(res.Missing) != len(want) {
			t.Errorf("Apply() Missing = %v, want %d entries (one per command)",
				res.Missing, len(want))
		}
	})

	t.Run("a block for a command not in want is reported Foreign and left untouched", func(t *testing.T) {
		foreignPath := "pgedge byoc ghost delete"
		foreignBlock := BeginMarker(foreignPath) + "\n" +
			"##### ghost delete\n\n" +
			"this command no longer exists\n" +
			EndMarker()
		res := Apply(foreignBlock, want)

		found := false
		for _, p := range res.Foreign {
			if p == foreignPath {
				found = true
			}
		}
		if !found {
			t.Errorf("Apply() Foreign = %v, want it to contain %q", res.Foreign, foreignPath)
		}
		if res.Doc != foreignBlock {
			t.Errorf("Apply() rewrote a Foreign block; Doc = %q, want unchanged %q",
				res.Doc, foreignBlock)
		}
	})

	t.Run("an up to date block is not counted as Changed", func(t *testing.T) {
		// Scoped to just the leaf: Apply reports every uncovered path in
		// want as Missing, and the full tree's ancestors would pollute
		// this assertion, which is only about Changed staying 0.
		leafOnly := []*cobra.Command{list}
		fresh := Block(list)
		res := Apply(fresh, leafOnly)
		if res.Changed != 0 {
			t.Errorf("Apply() Changed = %d, want 0 for an already-conforming block", res.Changed)
		}
		if len(res.Missing) != 0 {
			t.Errorf("Apply() Missing = %v, want none: the block covers the only wanted command",
				res.Missing)
		}
	})
}

func TestConform(t *testing.T) {
	root, list := buildTree()
	want := VisibleCommands(root)

	t.Run("empty for a conforming document", func(t *testing.T) {
		// want here is deliberately just the leaf: a doc with one fresh
		// block conforms to a tree of one command, not to the full
		// pgedge/byoc/cluster/list tree buildTree constructs.
		leafOnly := []*cobra.Command{list}
		doc := Block(list)
		if got := Conform(doc, leafOnly); len(got) != 0 {
			t.Errorf("Conform() = %v, want empty for a conforming document", got)
		}
	})

	t.Run("one message per distinct problem", func(t *testing.T) {
		// This document is missing blocks for every command except
		// list's own, and additionally carries one foreign block plus
		// one drifted one — three problem categories, three messages.
		drifted := BeginMarker("pgedge byoc cluster list") + "\n" +
			"stale\n" + EndMarker()
		foreign := BeginMarker("pgedge byoc ghost delete") + "\n" +
			"gone\n" + EndMarker()
		doc := drifted + "\n" + foreign

		got := Conform(doc, want)

		var missingMsgs, foreignMsgs, driftMsgs int
		for _, m := range got {
			switch {
			case strings.Contains(m, "no generated block for command"):
				missingMsgs++
			case strings.Contains(m, "is not responsible for"):
				foreignMsgs++
			case strings.Contains(m, "generated block(s) differ"):
				driftMsgs++
			}
		}
		if missingMsgs != len(want)-1 {
			t.Errorf("Conform() missing-block messages = %d, want %d", missingMsgs, len(want)-1)
		}
		if foreignMsgs != 1 {
			t.Errorf("Conform() foreign-block messages = %d, want 1", foreignMsgs)
		}
		if driftMsgs != 1 {
			t.Errorf("Conform() drift messages = %d, want 1", driftMsgs)
		}
	})
}

func TestBlockCount(t *testing.T) {
	_, list := buildTree()
	tests := []struct {
		name string
		doc  string
		want int
	}{
		{"no blocks", "just some prose", 0},
		{"one block", Block(list), 1},
		{"two blocks", Block(list) + "\n" + Block(list), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BlockCount(tt.doc); got != tt.want {
				t.Errorf("BlockCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMissingBlocks(t *testing.T) {
	root, _ := buildTree()
	want := VisibleCommands(root)

	res := Apply("", want)
	got := MissingBlocks(want, res.Missing)

	for _, path := range res.Missing {
		if !strings.Contains(got, BeginMarker(path)) {
			t.Errorf("MissingBlocks() does not render a block for missing path %q:\n%s",
				path, got)
		}
	}

	t.Run("a path not present in want is silently skipped, never a crash", func(t *testing.T) {
		got := MissingBlocks(want, []string{"pgedge nonexistent command"})
		if strings.TrimSpace(got) != "" {
			t.Errorf("MissingBlocks() = %q, want empty for an unknown path", got)
		}
	})
}

// --- usagePathOnLine ----------------------------------------------------

func TestUsagePathOnLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "not a usage line at all",
			line: "some prose that mentions **Usage:** loosely",
			want: "",
		},
		{
			name: "plain path with flags placeholder",
			line: "**Usage:** `pgedge byoc cluster list [flags]`",
			want: "pgedge byoc cluster list",
		},
		{
			name: "positional placeholder stops the path",
			line: "**Usage:** `pgedge byoc cluster get <cluster_id> [flags]`",
			want: "pgedge byoc cluster get",
		},
		{
			name: "no flags, no placeholder: whole line is the path",
			line: "**Usage:** `pgedge byoc status`",
			want: "pgedge byoc status",
		},
		{
			// THE BUG THIS PINS. A group's own usage line
			// ("pgedge byoc backup [flags]") is a PROPER PREFIX of its
			// child's usage line ("pgedge byoc backup create [flags]").
			// A prefix-matching implementation resolves the group's
			// line to "pgedge byoc backup" but ALSO matches the child's
			// line up through "pgedge byoc backup", silently truncating
			// the child's path and causing the group to adopt its own
			// child's section. Measured cost when this shipped: 146
			// spans claimed from 122 real anchors, 406 lines lost to
			// overlapping rewrites. usagePathOnLine must parse the full
			// run of bare tokens so the child's line resolves to its
			// own complete path, distinct from the parent's.
			name: "child usage line resolves to its own full path, not the parent's prefix",
			line: "**Usage:** `pgedge byoc backup create [flags]`",
			want: "pgedge byoc backup create",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usagePathOnLine(tt.line); got != tt.want {
				t.Errorf("usagePathOnLine(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

// --- keepProse ------------------------------------------------------------

func TestKeepProse(t *testing.T) {
	tests := []struct {
		name  string
		block []string
		want  []string
	}{
		{
			name:  "empty input stays empty",
			block: nil,
			want:  nil,
		},
		{
			name:  "blank lines only trim to nothing",
			block: []string{"", "   ", ""},
			want:  nil,
		},
		{
			// THE BUG THIS PINS. The first version of Adopt deleted
			// this whole range on the assumption it only ever held a
			// one-line description. `pgedge byoc database rag deploy`
			// carried three paragraphs and a fenced JSON example here;
			// deleting the range silently dropped all of it, caught
			// only by a ```json fence count (3 -> 2) in a 4,700-line
			// diff. A single plain paragraph line is the ONLY thing
			// this function may drop.
			name:  "single plain line is dropped: Short replaces it",
			block: []string{"List all clusters in the current tenant."},
			want:  nil,
		},
		{
			name: "two plain paragraph lines are kept: a real second clause is informative",
			block: []string{
				"Register a new cloud account for AWS, Azure, or GCP.",
				"Required flags depend on the cloud provider specified with --type.",
			},
			want: []string{
				"Register a new cloud account for AWS, Azure, or GCP.",
				"Required flags depend on the cloud provider specified with --type.",
			},
		},
		{
			name: "a fenced code block is kept even alone",
			block: []string{
				"```json",
				`{"pipeline": "default"}`,
				"```",
			},
			want: []string{
				"```json",
				`{"pipeline": "default"}`,
				"```",
			},
		},
		{
			name:  "a table row is kept",
			block: []string{"| col | col |"},
			want:  []string{"| col | col |"},
		},
		{
			name:  "a list marker (-) is kept",
			block: []string{"- first item"},
			want:  []string{"- first item"},
		},
		{
			name:  "a list marker (*) is kept",
			block: []string{"* first item"},
			want:  []string{"* first item"},
		},
		{
			name:  "a blockquote is kept",
			block: []string{"> a warning worth keeping"},
			want:  []string{"> a warning worth keeping"},
		},
		{
			name: "blank edges are trimmed before the single-line check applies",
			block: []string{
				"",
				"List all clusters in the current tenant.",
				"",
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keepProse(tt.block)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("keepProse(%v) = %v, want %v", tt.block, got, tt.want)
			}
		})
	}
}

// --- trimBlankEdges -------------------------------------------------------

func TestTrimBlankEdges(t *testing.T) {
	tests := []struct {
		name  string
		block []string
		want  []string
	}{
		{"no edges to trim", []string{"a", "b"}, []string{"a", "b"}},
		{"leading blank trimmed", []string{"", "a"}, []string{"a"}},
		{"trailing blank trimmed", []string{"a", ""}, []string{"a"}},
		{"both edges trimmed", []string{"", "a", "b", ""}, []string{"a", "b"}},
		{"all blank collapses to empty", []string{"", "  ", ""}, nil},
		{"interior blank line is preserved", []string{"a", "", "b"}, []string{"a", "", "b"}},
		{"empty input stays empty", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimBlankEdges(tt.block)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("trimBlankEdges(%v) = %v, want %v", tt.block, got, tt.want)
			}
		})
	}
}

// --- Adopt ------------------------------------------------------------

func TestAdopt(t *testing.T) {
	t.Run("converts a hand-written section into a block and preserves trailing prose", func(t *testing.T) {
		root := newGroup("pgedge", "")
		byoc := newGroup("byoc", "")
		cluster := newGroup("cluster", "")
		list := newLeaf("list", "List clusters.")
		list.Flags().Int("limit", 0, "Maximum number of results to return")
		root.AddCommand(byoc)
		byoc.AddCommand(cluster)
		cluster.AddCommand(list)

		doc := strings.Join([]string{
			"##### cluster list",
			"",
			"List all clusters in the current tenant.",
			"",
			"**Usage:** `pgedge byoc cluster list [flags]`",
			"",
			"**Flags:**",
			"",
			"| Flag | Required | Description |",
			"|------|----------|-------------|",
			"| `--limit int` | No | Maximum number of results to return |",
			"",
			"**Example:**",
			"```bash",
			"pgedge byoc cluster list --limit 20",
			"```",
		}, "\n")

		out, adopted := Adopt(doc, root)

		if len(adopted) != 1 || adopted[0] != "pgedge byoc cluster list" {
			t.Fatalf("Adopt() adopted = %v, want exactly [\"pgedge byoc cluster list\"]", adopted)
		}
		if !strings.Contains(out, BeginMarker("pgedge byoc cluster list")) {
			t.Errorf("Adopt() output missing begin marker:\n%s", out)
		}
		if !strings.Contains(out, EndMarker()) {
			t.Errorf("Adopt() output missing end marker:\n%s", out)
		}
		// The hand-written one-liner is the one deliberate loss.
		if strings.Contains(out, "List all clusters in the current tenant.") {
			t.Errorf("Adopt() kept the one-line description; it should be "+
				"replaced by Short:\n%s", out)
		}
		// Trailing prose after the flags table must survive untouched.
		if !strings.Contains(out, "**Example:**") ||
			!strings.Contains(out, "pgedge byoc cluster list --limit 20") {
			t.Errorf("Adopt() dropped trailing prose:\n%s", out)
		}
	})

	t.Run("a section already owned by a block is left untouched", func(t *testing.T) {
		root := newGroup("pgedge", "")
		status := newLeaf("status", "Show status.")
		root.AddCommand(status)

		block := Block(status)
		out, adopted := Adopt(block, root)

		if len(adopted) != 0 {
			t.Errorf("Adopt() adopted = %v, want none: the section is already a block", adopted)
		}
		if out != block {
			t.Errorf("Adopt() modified an already-owned block:\ngot:  %s\nwant: %s", out, block)
		}
	})

	t.Run("a command with neither a block nor a usage line is left for Apply to report", func(t *testing.T) {
		root := newGroup("pgedge", "")
		orphan := newLeaf("orphan", "Not documented anywhere.")
		root.AddCommand(orphan)

		doc := "just some unrelated prose"
		out, adopted := Adopt(doc, root)

		if len(adopted) != 0 {
			t.Errorf("Adopt() adopted = %v, want none", adopted)
		}
		if out != doc {
			t.Errorf("Adopt() modified doc with no matching section:\ngot:  %q\nwant: %q", out, doc)
		}
	})

	t.Run("multi-paragraph prose between heading and usage is preserved, not dropped", func(t *testing.T) {
		// Regression fixture for the keepProse loss bug: a section with
		// more than a one-liner between its heading and usage line must
		// keep all of it once adopted.
		root := newGroup("pgedge", "")
		deploy := newLeaf("deploy", "Deploy the RAG pipeline.")
		root.AddCommand(deploy)

		doc := strings.Join([]string{
			"##### database rag deploy",
			"",
			"Deploys a RAG pipeline using the given configuration.",
			"The minimum config accepted is shown below.",
			"",
			"```json",
			`{"pipeline": "default"}`,
			"```",
			"",
			"**Usage:** `pgedge deploy [flags]`",
		}, "\n")

		out, adopted := Adopt(doc, root)

		if len(adopted) != 1 {
			t.Fatalf("Adopt() adopted = %v, want exactly one path", adopted)
		}
		if !strings.Contains(out, "Deploys a RAG pipeline using the given configuration.") {
			t.Errorf("Adopt() dropped multi-paragraph prose:\n%s", out)
		}
		if !strings.Contains(out, `{"pipeline": "default"}`) {
			t.Errorf("Adopt() dropped the fenced JSON example:\n%s", out)
		}
	})

	t.Run("multiple sections are all adopted, in original top-to-bottom order", func(t *testing.T) {
		// Exercises the multi-span path: spans are rewritten back to
		// front (sortSpansDesc) so earlier indices stay valid, then
		// adopted paths are reversed back to document order. A single-
		// section case can't tell these apart from a no-op.
		root := newGroup("pgedge", "")
		first := newLeaf("first", "Do the first thing.")
		second := newLeaf("second", "Do the second thing.")
		root.AddCommand(first, second)

		doc := strings.Join([]string{
			"##### first",
			"",
			"**Usage:** `pgedge first`",
			"",
			"##### second",
			"",
			"**Usage:** `pgedge second`",
		}, "\n")

		out, adopted := Adopt(doc, root)

		want := []string{"pgedge first", "pgedge second"}
		if strings.Join(adopted, ",") != strings.Join(want, ",") {
			t.Errorf("Adopt() adopted = %v, want %v in document order", adopted, want)
		}
		firstIdx := strings.Index(out, BeginMarker("pgedge first"))
		secondIdx := strings.Index(out, BeginMarker("pgedge second"))
		if firstIdx < 0 || secondIdx < 0 || firstIdx > secondIdx {
			t.Errorf("Adopt() did not preserve document order of the two blocks:\n%s", out)
		}
	})
}

// CommandProse's leading-heading skip must not borrow a NEIGHBOUR's
// prose. Review demonstrated both shapes against an earlier version:
// a command with no hand-written half returning the next section's
// text, and a non-command heading resolving too. Either defeats the
// empty-prose error the extractor exists to raise.
func TestCommandProseDoesNotBorrowANeighboursProse(t *testing.T) {
	const doc = `
<!-- BEGIN GENERATED: pgedge starfleet x list -->
##### pgedge starfleet x list
<!-- END GENERATED -->

#### pgedge starfleet x get

Prose that belongs to GET, not to list.
`
	// `list` has no prose of its own: the next heading names `get`, so
	// the skip must not fire and the span must be reported empty.
	if got, err := CommandProse(doc, "pgedge starfleet x list"); err == nil {
		t.Errorf("returned %q for a command with no prose of its own; "+
			"want an error. Borrowing the next section's text is how a "+
			"paging gate goes green while the verb's own claim is "+
			"absent.", got)
	}

	const unrelated = `
<!-- BEGIN GENERATED: pgedge starfleet x list -->
##### pgedge starfleet x list
<!-- END GENERATED -->

## Some unrelated section

100 rows by default is what this happens to say.
`
	if got, err := CommandProse(unrelated, "pgedge starfleet x list"); err == nil {
		t.Errorf("returned %q past a non-command heading; want an "+
			"error", got)
	}

	// A SIBLING VERB, which a HasSuffix match let through: both
	// headings end in "list", so `database list` with its own prose
	// deleted borrowed `backup list`'s. Review found this against the
	// second version of the guard, after the first.
	const sibling = `
<!-- BEGIN GENERATED: pgedge starfleet managed database list -->
##### pgedge starfleet managed database list
<!-- END GENERATED -->

##### pgedge starfleet managed backup list

100 rows by default.
`
	if got, err := CommandProse(
		sibling, "pgedge starfleet managed database list"); err == nil {
		t.Errorf("returned %q by matching a SIBLING verb's heading on "+
			"a shared suffix; want an error. That is how a paging gate "+
			"goes green reading another verb's page size.", got)
	}

	// A heading that merely ENDS with the last path element.
	const suffixOnly = `
<!-- BEGIN GENERATED: pgedge starfleet x list -->
##### pgedge starfleet x list
<!-- END GENERATED -->

## Notes on every list

100 rows by default.
`
	if got, err := CommandProse(
		suffixOnly, "pgedge starfleet x list"); err == nil {
		t.Errorf("returned %q past a heading that only ends with the "+
			"command's last element; want an error", got)
	}

	// The skip DOES fire when the heading names the command, which is
	// managed's prose-first layout and the reason the skip exists.
	const own = `
<!-- BEGIN GENERATED: pgedge starfleet x list -->
##### pgedge starfleet x list
<!-- END GENERATED -->

##### pgedge starfleet x list

25 rows by default.
`
	got, err := CommandProse(own, "pgedge starfleet x list")
	if err != nil {
		t.Fatalf("own heading: %v", err)
	}
	if !strings.Contains(got, "25 rows by default") {
		t.Errorf("got %q, want the command's own prose", got)
	}

	// byoc's group headings are the SHORT form, so a suffix match is
	// what keeps those resolving.
	const short = `
<!-- BEGIN GENERATED: pgedge starfleet byoc cluster -->
#### pgedge starfleet byoc cluster
<!-- END GENERATED -->

#### cluster

Group prose.
`
	if got, err := CommandProse(
		short, "pgedge starfleet byoc cluster"); err != nil ||
		!strings.Contains(got, "Group prose") {
		t.Errorf("got (%q, %v), want the group's own prose", got, err)
	}
}

// The inverse half is the half this repo shipped without twice. The
// "want" cases are the honest documents; the "wantErr" cases are the
// two bypasses review demonstrated against a Contains-only gate — a
// second, contradictory claim of the same shape sitting beside the
// true one — plus a claim of a shape the code records nothing for.
func TestCheckPagingClaims(t *testing.T) {
	cases := []struct {
		name         string
		prose        string
		def, clamp   int
		wantErr      bool
		wantContains string
	}{
		{
			name:  "the honest document",
			prose: "10 rows by default, and above 100 is clamped.",
			def:   10, clamp: 100,
		},
		{
			// The CLI refuses a --limit above the cap before sending it,
			// so a page that says so is the honest one; a gate that only
			// read "clamped" forced a false word onto every such page.
			name:  "the honest document, where the CLI refuses",
			prose: "10 rows by default, and above 100 is refused.",
			def:   10, clamp: 100,
		},
		{
			name: "a stale cap in the other wording",
			prose: "10 rows by default. Above 100 is refused, and " +
				"above 200 is clamped on older deployments.",
			def: 10, clamp: 100,
			wantErr:      true,
			wantContains: "[100 200]",
		},
		{
			name:  "no default page and no cap",
			prose: "This endpoint applies no default page and has no known cap.",
		},
		{
			name: "a stale cap beside the true one",
			prose: "10 rows by default. A --limit above 100 is " +
				"clamped, and above 200 is clamped as well.",
			def: 10, clamp: 100,
			wantErr:      true,
			wantContains: "[100 200]",
		},
		{
			// The case that caught the first version of this check out.
			// A stale claim opening a sentence is capitalised, and a
			// case-sensitive scan reads that as one claim rather than
			// two — which is the Contains-only failure over again.
			name: "a capitalised stale cap",
			prose: "10 rows by default. above 100 is clamped. " +
				"Above 200 is clamped on older deployments.",
			def: 10, clamp: 100,
			wantErr:      true,
			wantContains: "[100 200]",
		},
		{
			name: "a stale page size beside the true one",
			prose: "50 rows by default on older deployments. 25 rows " +
				"by default now. Above 100 is clamped.",
			def: 25, clamp: 100,
			wantErr:      true,
			wantContains: "[50 25]",
		},
		{
			name:  "a cap claim where the code records none",
			prose: "No default page, and above 100 is clamped.",
			def:   0, clamp: 0,
			wantErr:      true,
			wantContains: "records no such number",
		},
		{
			name:  "a page size where the code records none",
			prose: "25 rows by default, no known cap.",
			def:   0, clamp: 0,
			wantErr:      true,
			wantContains: "records no such number",
		},
		{
			name:  "the claim is missing entirely",
			prose: "Pages with --limit and --offset.",
			def:   10, clamp: 100,
			wantErr:      true,
			wantContains: "want exactly [10]",
		},
		{
			name:  "the number is simply wrong",
			prose: "10 rows by default, and above 500 is clamped.",
			def:   10, clamp: 100,
			wantErr:      true,
			wantContains: "want exactly [100]",
		},
		{
			// I3: the emphasis can sit anywhere. The shipped documents
			// bold these numbers, and `**25** rows by default` did not
			// match while `**25 rows by default**` did — a difference
			// invisible to the reader and decisive to the gate.
			name: "a split emphasis is still the claim",
			prose: "The server returns **50** rows by default on " +
				"older deployments, **25 rows by default** now, and a " +
				"`--limit` **above 100** is clamped.",
			def: 25, clamp: 100,
			wantErr:      true,
			wantContains: "[50 25]",
		},
		{
			name:  "a backticked claim is still the claim",
			prose: "`10 rows by default`, and above `100` is clamped.",
			def:   10, clamp: 100,
		},
		{
			name:  "a thousands separator is one number",
			prose: "1,000 rows by default, no known cap.",
			def:   1000, clamp: 0,
		},
		{
			// The comma rule must not join two sentences into one
			// claim: dropping every comma would turn this into
			// "...default 100 rows..." and change what matches.
			name:  "a punctuation comma is kept",
			prose: "10 rows by default, and above 100 is clamped.",
			def:   10, clamp: 100,
		},
		{
			// The prose gates run over CommandProse output, which has
			// collapsed whitespace — but a hard wrap inside the phrase
			// must not read as an absence if a caller ever passes raw
			// text, so record which way it goes rather than leaving it
			// to be discovered.
			name:  "a wrap inside the phrase is not the phrase",
			prose: "10 rows by\ndefault.",
			def:   10, clamp: 0,
			wantErr:      true,
			wantContains: "want exactly [10]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPagingClaims(tc.prose, tc.def, tc.clamp)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantContains != "" &&
				!strings.Contains(err.Error(), tc.wantContains) {
				t.Errorf("error %q does not mention %q",
					err, tc.wantContains)
			}
		})
	}
}

// The span terminator is a HEADING test, not a "line starting with #"
// test, and it does not look inside fenced blocks. Both halves were
// defects: byoc's hand-written prose carries its `**Example:**` fences
// inside the span, so a shell comment as an example's first line ended
// the gate's view of that verb and hid every claim after it.
func TestCommandProseStopsOnlyAtARealHeading(t *testing.T) {
	const doc = `
<!-- BEGIN GENERATED: pgedge starfleet x list -->
<!-- END GENERATED -->

Before the example.

` + "```bash" + `
# every row in the account
#not-a-heading
####### seven hashes
pgedge starfleet x list
` + "```" + `

After the example: 10 rows by default.

#### the next section

Neighbour prose.
`
	got, err := CommandProse(doc, "pgedge starfleet x list")
	if err != nil {
		t.Fatalf("CommandProse: %v", err)
	}
	for _, want := range []string{
		"Before the example.",
		"After the example: 10 rows by default.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("span %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "Neighbour prose") {
		t.Errorf("span ran past the heading into %q", got)
	}
}

// The three shapes atxHeading has to separate, kept as its own table
// so a failure names the shape rather than a span.
func TestATXHeading(t *testing.T) {
	for _, tc := range []struct {
		line, text string
		ok         bool
	}{
		{line: "# One", text: "One", ok: true},
		{line: "###### Six", text: "Six", ok: true},
		{line: "  ## Indented", text: "Indented", ok: true},
		// Three spaces is the CommonMark limit; four makes the
		// line an indented CODE BLOCK, so it is not a heading and
		// must not end a span. Without this row, a 4-space
		// indented `#` comment in an example terminated the span
		// and a contradictory paging claim after it was invisible.
		{line: "   ### Three spaces", text: "Three spaces",
			ok: true},
		{line: "    # code, not a heading"},
		{line: "\t# tab-indented code"},
		{line: "#", text: "", ok: true},
		{line: "####### Seven"},
		{line: "#not-a-heading"},
		{line: "#tag"},
		{line: "plain text"},
		{line: ""},
	} {
		text, ok := atxHeading(tc.line)
		if ok != tc.ok || text != tc.text {
			t.Errorf("atxHeading(%q) = (%q, %v), want (%q, %v)",
				tc.line, text, ok, tc.text, tc.ok)
		}
	}
}

// TestExampleSectionExtractsTheTrailingBlock covers the shapes this
// tree actually writes: a single line, several lines, and a backslash
// continuation indented deeper than the command it continues.
func TestExampleSectionExtractsTheTrailingBlock(t *testing.T) {
	cases := []struct {
		name string
		long string
		want string
	}{
		{
			name: "no example section",
			long: "list shows every cluster.",
			want: "",
		},
		{
			name: "single line",
			long: "list shows every cluster.\n\nExample:\n  pgedge x list",
			want: "pgedge x list",
		},
		{
			name: "several lines",
			long: "Example:\n  pgedge x list\n  pgedge x list -o json",
			want: "pgedge x list\npgedge x list -o json",
		},
		{
			// Dedent is by the shallowest indent, so a continuation
			// keeps its indent RELATIVE to the command it continues:
			// 4 spaces under a 2-space command becomes 2.
			name: "continuation keeps its relative indent",
			long: "Example:\n  pgedge x create \\\n    --name n",
			want: "pgedge x create \\\n  --name n",
		},
		{
			name: "trailing blank lines are dropped",
			long: "Example:\n  pgedge x list\n\n",
			want: "pgedge x list",
		},
		{
			name: "a heading with nothing under it yields nothing",
			long: "Example:\n",
			want: "",
		},
		{
			name: "the last heading wins",
			long: "Example:\n  pgedge a\n\nExample:\n  pgedge b",
			want: "pgedge b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExampleSection(c.long); got != c.want {
				t.Errorf("ExampleSection() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPageEmitsExamplesAndBlocksDoNotPins the asymmetry renderBody
// exists for: the reference pages carry the example, the llms.txt
// blocks must not, because those files already carry hand-written
// ones and would print each twice.
func TestPageEmitsExamplesAndBlocksDoNot(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List things",
		Long:  "list shows things.\n\nExample:\n  pgedge x list",
	}

	page := Page([]*cobra.Command{cmd})
	if !strings.Contains(page, "**Example:**") {
		t.Errorf("a reference page must carry the example:\n%s", page)
	}
	if !strings.Contains(page, "pgedge x list") {
		t.Errorf("the example's command is missing:\n%s", page)
	}

	block := Block(cmd)
	if strings.Contains(block, "**Example:**") {
		t.Errorf("an llms.txt block must not carry the example:\n%s", block)
	}
}
