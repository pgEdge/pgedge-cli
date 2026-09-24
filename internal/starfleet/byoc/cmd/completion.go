package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// completeFixed returns a Cobra flag-completion function offering a
// fixed value set and disabling filename fallback. Use it for flags
// whose value is one of a small, known set.
func completeFixed(values ...string) func(
	*cobra.Command, []string, string,
) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) (
		[]string, cobra.ShellCompDirective,
	) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeFirewallRuleName completes the name= portion of a
// --firewall-rule value. The flag takes a structured key=value list
// (name=https,port=443,sources=...), so we offer name=<type> for each
// valid rule type and keep the cursor attached (NoSpace) so the user
// can continue typing the rest of the rule.
func completeFirewallRuleName(
	_ *cobra.Command, _ []string, toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if strings.Contains(toComplete, "=") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{
			"name=http", "name=https", "name=postgres", "name=ssh",
		},
		cobra.ShellCompDirectiveNoSpace |
			cobra.ShellCompDirectiveNoFileComp
}
