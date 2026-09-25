package cmd

import (
	"fmt"
	"strconv"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// NewDatabaseAllowlistCmd builds `pgedge starfleet managed database
// allowlist`, the source-address rules for one endpoint of a managed
// database. The API stores the list as a field and replaces it whole on
// every write; these verbs read, splice and write it back so a caller
// never retypes the list.
func NewDatabaseAllowlistCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "allowlist",
		Aliases: []string{"allowlists"},
		Short:   "Manage which addresses may reach a database",
		Long: `allowlist manages the source-IP rules on one endpoint of a
managed database. Only addresses matching a rule can open a new
connection; a database with no rules is closed and reaches nobody.

A database has one list for its Postgres endpoint and one more for
each service deployed on it. Every verb edits the Postgres list unless
--service names a service type (mcp, rag or postgrest), in which case
it edits that service's own list. Nothing is shared between them.

A rule is an IPv4 address or CIDR block. The API stores a bare address
as a /32 and masks a block to its network address. IPv6 is refused.
At most 50 rules per endpoint.

Example:
  pgedge starfleet managed database allowlist get <database_id>
  pgedge starfleet managed database allowlist add <database_id> --my-ip
  pgedge starfleet managed database allowlist add <database_id> \
      203.0.113.0/24 --label office --service mcp`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newAllowlistGetCmd(rt),
		newAllowlistAddCmd(rt),
		newAllowlistRemoveCmd(rt),
		newAllowlistSetCmd(rt),
		newAllowlistOpenCmd(rt),
		newAllowlistClearCmd(rt),
	)
	return cmd
}

// addServiceFlag registers --service on an allowlist verb. Empty means
// the Postgres endpoint; the flag is parsed by resolveEndpoint.
func addServiceFlag(cmd *cobra.Command, svcType *string) {
	cmd.Flags().StringVar(svcType, "service", "",
		"Edit this service's own allowlist instead of the Postgres "+
			"endpoint's: mcp, rag or postgrest")
}

var allowlistRuleColumns = []string{"CIDR", "LABEL"}

type allowlistRuleRow struct{ cidr, label string }

func (r allowlistRuleRow) Columns() []string {
	return []string{r.cidr, r.label}
}

func allowlistRuleRows(rules []api.IPAllowlistRule) []output.Row {
	rows := make([]output.Row, 0, len(rules))
	for _, r := range rules {
		row := allowlistRuleRow{cidr: r.Cidr}
		if r.Label != nil {
			row.label = *r.Label
		}
		rows = append(rows, row)
	}
	return rows
}

func stateString(s *api.IPAllowlistState) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

var allowlistSummaryColumns = []string{"ENDPOINT", "STATE", "RULES"}

type allowlistSummaryRow struct{ endpoint, state, rules string }

func (r allowlistSummaryRow) Columns() []string {
	return []string{r.endpoint, output.ColorStatus(r.state), r.rules}
}

// allowlistSummaryRows is one row per endpoint: postgres first, then
// each deployed service. Always at least one row, so a fresh database
// shows that it is closed.
func allowlistSummaryRows(d *api.ManagedDatabase) []output.Row {
	pgState := stateString(d.IpAllowlist.State)
	if pgState == "" && len(d.IpAllowlist.Rules) == 0 {
		// A database whose allowlist has never been touched omits
		// state entirely; the contract derives closed from no rules,
		// same as newAllowlistGetCmd does for one endpoint.
		pgState = string(api.Closed)
	}
	rows := []output.Row{allowlistSummaryRow{
		endpoint: endpointPostgres,
		state:    pgState,
		rules:    strconv.Itoa(len(d.IpAllowlist.Rules)),
	}}
	if d.Services == nil {
		return rows
	}
	for _, svc := range *d.Services {
		row := allowlistSummaryRow{
			endpoint: string(svc.ServiceType), state: "closed", rules: "0"}
		if svc.IpAllowlist != nil {
			row.state = stateString(svc.IpAllowlist.State)
			row.rules = strconv.Itoa(len(svc.IpAllowlist.Rules))
		}
		rows = append(rows, row)
	}
	return rows
}

// --- get ---

func newAllowlistGetCmd(rt *module.Runtime) *cobra.Command {
	var svcType string
	cmd := &cobra.Command{
		Use:   "get [<database_id>]",
		Short: "Show an endpoint's allowlist and its state",
		Long: `get prints one endpoint's rules and the posture they add up
to: closed (no rules, nobody connects), restricted, or open (a rule
admits every address).

Text output is an Endpoint line, a State line and a CIDR/LABEL table.
-o json prints the endpoint's own {"rules", "state"} object, with
state "closed" filled in for an endpoint the API has never written.
In a folder linked with 'database link', the ID can be left out.

Example:
  pgedge starfleet managed database allowlist get <database_id>
  pgedge starfleet managed database allowlist get <database_id> --service mcp`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := databaseArg(rt, args, 0)
			if err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, rules, err := resolveEndpoint(db, svcType, id.String())
			if err != nil {
				return err
			}
			var list api.IPAllowlist
			if svc == nil {
				list = db.IpAllowlist
			} else if svc.IpAllowlist != nil {
				list = *svc.IpAllowlist
			}
			list.Rules = rules
			// A service that has never had its allowlist touched omits
			// the field entirely; the contract derives closed from no
			// rules, so state is never left unset for a caller to see.
			if list.State == nil {
				closed := api.Closed
				list.State = &closed
			}
			if rt.Output.Structured() {
				return rt.Output.Print(list, nil)
			}
			fmt.Fprintf(rt.Output.Out, "Endpoint: %s\nState: %s\n",
				output.Sanitize(endpointName(typ)),
				output.Sanitize(stateString(list.State)))
			if len(rules) == 0 {
				fmt.Fprintln(rt.Stderr, "No rules; the endpoint is closed.")
				return nil
			}
			fmt.Fprintln(rt.Output.Out)
			return rt.Output.Print(allowlistRuleRows(rules),
				allowlistRuleColumns)
		},
	}
	addServiceFlag(cmd, &svcType)
	return cmd
}

// --- add ---

func newAllowlistAddCmd(rt *module.Runtime) *cobra.Command {
	var (
		svcType string
		label   string
		myIP    bool
	)
	cmd := &cobra.Command{
		Use:   "add <database_id> [<cidr>...]",
		Short: "Allow addresses to reach an endpoint",
		Long: `add appends rules to one endpoint's allowlist and leaves the
existing rules alone. Give one or more IPv4 addresses or CIDR blocks,
or --my-ip to add the address the API sees this command arriving from,
or both.

A rule already present is skipped and named on stderr. If every input
is already present, nothing is sent and the command exits 0. Inputs are
sent as typed; the API stores a bare address as a /32 and masks a block
to its network address, and refuses IPv6 and malformed input. More than
50 rules, or a --label over 64 characters, is refused here with exit 2
before anything is written.

--my-ip is a convenience, not a guarantee: the address is the one this
request arrived from, which may differ from the one your Postgres
client connects from. The caveat prints every time. Exit 1 when the API
reports no address or an IPv6 one.

The change moves the database through modifying and needs it to be
available first. Postgres is not restarted and its open sessions are
not cut; the rules govern new connections.

Example:
  pgedge starfleet managed database allowlist add <database_id> --my-ip
  pgedge starfleet managed database allowlist add <database_id> \
      203.0.113.0/24 198.51.100.9 --label office
  pgedge starfleet managed database allowlist add <database_id> \
      203.0.113.7 --service mcp --wait`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The ID first, so `allowlist add 203.0.113.7` names the
			// missing ID rather than asking for an address it was given.
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			inputs := args[1:]
			if len(inputs) == 0 && !myIP {
				return newExitError(
					"give at least one address, or --my-ip", ExitUsage)
			}
			if err := checkAllowlistBounds(nil, label); err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			if myIP {
				mine, err := fetchClientIP(rt, client)
				if err != nil {
					return err
				}
				inputs = append(inputs, mine)
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, current, err := resolveEndpoint(db, svcType, args[0])
			if err != nil {
				return err
			}
			merged, skipped := mergeRules(current, inputs, label)
			for _, s := range skipped {
				fmt.Fprintf(rt.Stderr, "Already allowed: %s\n",
					output.Sanitize(s))
			}
			if len(merged) == len(current) {
				fmt.Fprintln(rt.Stderr, "Nothing to add.")
				return nil
			}
			if err := checkAllowlistBounds(merged, label); err != nil {
				return err
			}
			if err := applyAllowlist(rt, client, db, svc, merged,
				fmt.Sprintf("%d rule(s) added to the %s allowlist",
					len(merged)-len(current), endpointName(typ)),
				args[0]); err != nil {
				return err
			}
			warnIfOpen(rt, merged, typ, args[0])
			return nil
		},
	}
	addServiceFlag(cmd, &svcType)
	cmd.Flags().StringVar(&label, "label", "",
		"Note stored on each rule added by this call, at most 64 characters")
	cmd.Flags().BoolVar(&myIP, "my-ip", false,
		"Also allow the address the API sees this command arriving from")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)
	return cmd
}

// --- remove ---

func newAllowlistRemoveCmd(rt *module.Runtime) *cobra.Command {
	var (
		svcType string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "remove <database_id> <cidr>...",
		Short: "Stop addresses reaching an endpoint",
		Long: `remove drops rules from one endpoint's allowlist and leaves
the rest alone. Name each rule as stored, or as you typed it when you
added it: a bare address matches its stored /32, and a block matches
its masked network address.

A rule that is not present is exit 4 and nothing is sent. Removing the
last rule closes the endpoint, so that case asks for confirmation
unless --force is given.

The change moves the database through modifying and needs it to be
available first. Open sessions are not cut.

Example:
  pgedge starfleet managed database allowlist remove <database_id> 203.0.113.7
  pgedge starfleet managed database allowlist remove <database_id> \
      198.51.100.0/24 --service rag`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, current, err := resolveEndpoint(db, svcType, args[0])
			if err != nil {
				return err
			}
			drop := make(map[int]bool, len(args)-1)
			for _, in := range args[1:] {
				i := ruleIndex(current, in)
				if i < 0 {
					return newExitError(fmt.Sprintf(
						"no rule %q on the %s endpoint of database %s",
						in, endpointName(typ), args[0]), ExitNotFound)
				}
				drop[i] = true
			}
			remaining := make([]api.IPAllowlistRule, 0, len(current))
			for i, r := range current {
				if !drop[i] {
					remaining = append(remaining, r)
				}
			}
			if len(remaining) == 0 {
				if err := confirmClose(rt, typ, args[0], force); err != nil {
					return err
				}
			}
			return applyAllowlist(rt, client, db, svc, remaining,
				fmt.Sprintf("%d rule(s) removed from the %s allowlist",
					len(drop), endpointName(typ)), args[0])
		},
	}
	addServiceFlag(cmd, &svcType)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt when removing the last rule")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)
	return cmd
}

// --- set ---

func newAllowlistSetCmd(rt *module.Runtime) *cobra.Command {
	var (
		svcType string
		label   string
	)
	cmd := &cobra.Command{
		Use:   "set <database_id> <cidr>...",
		Short: "Replace an endpoint's allowlist",
		Long: `set replaces one endpoint's rules with exactly the addresses
given, in one request. Use it from scripts and configuration that state
the whole list; use add and remove to change one rule at a time.

Giving no address is exit 2: to close an endpoint, use clear. More than
50 rules, or a --label over 64 characters, is refused with exit 2
before any request. Inputs are sent as typed and normalised by the API.

The change moves the database through modifying and needs it to be
available first. Open sessions are not cut.

Example:
  pgedge starfleet managed database allowlist set <database_id> \
      203.0.113.0/24 198.51.100.9 --label ci`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return newExitError(
					"set needs at least one address; use clear to close "+
						"the endpoint", ExitUsage)
			}
			rules := newRules(args[1:], label)
			if err := checkAllowlistBounds(rules, label); err != nil {
				return err
			}
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, _, err := resolveEndpoint(db, svcType, args[0])
			if err != nil {
				return err
			}
			if err := applyAllowlist(rt, client, db, svc, rules,
				fmt.Sprintf("%s allowlist set to %d rule(s)",
					endpointName(typ), len(rules)), args[0]); err != nil {
				return err
			}
			warnIfOpen(rt, rules, typ, args[0])
			return nil
		},
	}
	addServiceFlag(cmd, &svcType)
	cmd.Flags().StringVar(&label, "label", "",
		"Note stored on every rule set by this call, at most 64 characters")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)
	return cmd
}

// --- open ---

func newAllowlistOpenCmd(rt *module.Runtime) *cobra.Command {
	var svcType string
	cmd := &cobra.Command{
		Use:   "open <database_id>",
		Short: "Let every address reach an endpoint",
		Long: `open replaces one endpoint's rules with the single rule
0.0.0.0/0, so the API reports its state as open and any address can
connect. This is as close as the API comes to switching the allowlist
off; there is no separate off switch.

It is never a default. A new database starts closed.

The change moves the database through modifying and needs it to be
available first.

Example:
  pgedge starfleet managed database allowlist open <database_id>
  pgedge starfleet managed database allowlist open <database_id> --service mcp`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, _, err := resolveEndpoint(db, svcType, args[0])
			if err != nil {
				return err
			}
			rules := []api.IPAllowlistRule{{Cidr: allowlistOpenCIDR}}
			if err := applyAllowlist(rt, client, db, svc, rules,
				fmt.Sprintf("%s allowlist opened", endpointName(typ)),
				args[0]); err != nil {
				return err
			}
			warnIfOpen(rt, rules, typ, args[0])
			return nil
		},
	}
	addServiceFlag(cmd, &svcType)
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)
	return cmd
}

// --- clear ---

func newAllowlistClearCmd(rt *module.Runtime) *cobra.Command {
	var (
		svcType string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "clear <database_id>",
		Short: "Close an endpoint to every address",
		Long: `clear removes every rule from one endpoint, so no address
can open a new connection to it. Open sessions stay up. It asks for
confirmation unless --force is given.

The change moves the database through modifying and needs it to be
available first.

Example:
  pgedge starfleet managed database allowlist clear <database_id>
  pgedge starfleet managed database allowlist clear <database_id> \
      --service rag --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "database ID")
			if err != nil {
				return err
			}
			if svcType != "" {
				if _, err := parseServiceType(svcType); err != nil {
					return err
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			db, err := fetchDatabaseWith(rt, client, id)
			if err != nil {
				return err
			}
			typ, svc, _, err := resolveEndpoint(db, svcType, args[0])
			if err != nil {
				return err
			}
			if err := confirmClose(rt, typ, args[0], force); err != nil {
				return err
			}
			return applyAllowlist(rt, client, db, svc,
				[]api.IPAllowlistRule{},
				fmt.Sprintf("%s allowlist cleared", endpointName(typ)),
				args[0])
		},
	}
	addServiceFlag(cmd, &svcType)
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)
	return cmd
}
