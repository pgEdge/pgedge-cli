package cli

import (
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/spf13/cobra"
)

// Version, Commit, and BuildDate are injected at build time via
// ldflags (see the Makefile and .goreleaser.yaml). The defaults are
// what a plain `go build`/`go run` with no ldflags reports.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// GlobalFlags holds the values of the root persistent flags.
type GlobalFlags struct {
	Config  string
	Profile string
	Debug   bool
	Verbose bool
	Output  string
	NoColor bool
}

// NewRootCmd builds the root pgedge command. rt is the module
// runtime; it is nil in unit tests that only exercise flag
// registration.
func NewRootCmd(rt *module.Runtime) *cobra.Command {
	flags := &GlobalFlags{}
	root := &cobra.Command{
		Use:   "pgedge",
		Short: "Unified CLI for the pgEdge product suite",
		Long: `pgedge manages pgEdge products through a single CLI:
pgedge <module> <resource> <verb>.

AI agents: do not improvise from --help output alone. Run
'pgedge llms' for the complete command and workflow reference.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	pf := root.PersistentFlags()
	pf.StringVar(&flags.Config, "config", "",
		"Path to config file (default ~/.pgedge/cli/config.yaml)")
	pf.StringVar(&flags.Profile, "profile", "",
		"Use a configured profile (see 'pgedge profile list')")
	// "(implies --verbose)" states the ordering where users read it, in
	// the generated reference; internal/httplog.LevelFor enforces it.
	pf.BoolVar(&flags.Debug, "debug", false,
		"Dump HTTP requests and responses to stderr "+
			"(implies --verbose)")
	pf.BoolVarP(&flags.Verbose, "verbose", "v", false,
		"Log requests and responses to stderr, without bodies")
	pf.StringVarP(&flags.Output, "output", "o", "text",
		"Output format: text, json, yaml")
	pf.BoolVar(&flags.NoColor, "no-color", false,
		"Disable color in text output")
	_ = root.RegisterFlagCompletionFunc("output",
		func(*cobra.Command, []string, string) (
			[]string, cobra.ShellCompDirective,
		) {
			return []string{"text", "json", "yaml"},
				cobra.ShellCompDirectiveNoFileComp
		})

	// Cobra runs only the nearest ancestor's PersistentPreRun(E) by
	// default, so a module command defining its own would skip root's,
	// leave rt.Config nil and panic on first use. EnableTraverseRunHooks
	// runs every ancestor's hook, and internal/clitest's
	// TestRootOwnsTheOnlySetupHook keeps root the only command defining
	// one. No PersistentPostRun exists, so that chain is unaffected.
	cobra.EnableTraverseRunHooks = true

	// Populate rt from the executing command's merged, parsed flag set,
	// cmd.Flags(), rather than a pre-parse of os.Args. This is the one
	// seam through which every command gets a ready Runtime: it runs
	// before any RunE, and --help/-h short-circuits before it by design.
	//
	// rt is nil in internal/cli unit tests that build NewRootCmd(nil)
	// for flag registration or routing only. It is a zero-value Runtime
	// in internal/clitest's FullTree, which never calls Execute.
	// setupRuntime fills stdio fields only when nil, so it never
	// clobbers what main.go set.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if rt == nil {
			return nil
		}
		return setupRuntime(rt, cmd.Flags())
	}

	root.AddCommand(NewVersionCmd(rt, module.DescribeAll()))
	root.AddCommand(NewDoctorCmd(rt, nil))
	root.AddCommand(NewInspectCmd(rt, nil))
	root.AddCommand(NewEnvCmd(rt))
	root.AddCommand(NewProfileCmd(rt))
	root.AddCommand(NewSelfCmd(rt, module.DescribeAll(), nil))
	root.AddCommand(NewLLMSCmd())
	root.AddCommand(NewCompletionCmd())

	// Own the help command instead of letting cobra generate one; see
	// NewHelpCmd for why the built-in cannot report a bad topic.
	//
	// InitDefaultHelpCmd adds it to the tree now rather than lazily
	// during Execute, because main.go's markRan wraps only the hooks
	// present when it walks the tree, and the conformance gates, which
	// hold it to the stray-argument standard, see only commands present
	// before Execute. It must follow the AddCommand calls above:
	// InitDefaultHelpCmd returns early unless root has subcommands.
	root.SetHelpCommand(NewHelpCmd())
	root.InitDefaultHelpCmd()

	// Suppress the help dump for an invocation StrayArgsOnHelp objects
	// to. Otherwise `pgedge controlplane backup --help` prints
	// controlplane's help to stdout above the stderr diagnostic, which
	// reads as a successful lookup to anything parsing stdout. main.run
	// takes the diagnostic and exit code from the same predicate, so the
	// silence and the error always agree.
	//
	// Cobra's HelpFunc() walks up to the nearest ancestor that set one,
	// so this covers every command, including those modules add later.
	// The default is captured before SetHelpFunc, or the wrapper would
	// recurse; it renders the command it is passed, so one capture
	// serves every descendant.
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		if StrayArgsOnHelp(c) != nil {
			return
		}
		defaultHelp(c, args)
	})
	return root
}
