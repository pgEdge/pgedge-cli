package cmd

import (
	"fmt"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// createAllowOpts is --allow, --my-ip and --open: the first rules of an
// endpoint, given when the endpoint is created. database create takes
// them for the Postgres endpoint, and a service deploy for the
// service's.
type createAllowOpts struct {
	allow []string
	myIP  bool
	open  bool
}

// bindCreateAllowFlags registers the three flags; endpoint completes
// "allowed to reach the <endpoint> endpoint".
func bindCreateAllowFlags(
	cmd *cobra.Command, o *createAllowOpts, endpoint string,
) {
	f := cmd.Flags()
	f.StringArrayVar(&o.allow, "allow", nil,
		"IPv4 address or CIDR block allowed to reach the "+endpoint+
			" endpoint (repeatable)")
	f.BoolVar(&o.myIP, "my-ip", false,
		"Also allow the address the API sees this command arriving from")
	f.BoolVar(&o.open, "open", false,
		"Admit every address with one 0.0.0.0/0 rule, never the default")
	cmd.MarkFlagsMutuallyExclusive("open", "allow")
	cmd.MarkFlagsMutuallyExclusive("open", "my-ip")
}

func (o *createAllowOpts) given() bool {
	return o != nil && (len(o.allow) > 0 || o.myIP || o.open)
}

// precheck sizes --allow before the client is built; --my-ip cannot be
// sized until its read, so rules checks again.
func (o *createAllowOpts) precheck() error {
	if o == nil {
		return nil
	}
	return checkAllowlistBounds(newRules(o.allow, ""), "")
}

// rules returns the rules to send, or nil when no flag was given, which
// leaves the endpoint closed.
func (o *createAllowOpts) rules(
	rt *module.Runtime, client *api.ClientWithResponses,
) ([]api.IPAllowlistRule, error) {
	switch {
	case !o.given():
		return nil, nil
	case o.open:
		return []api.IPAllowlistRule{{Cidr: allowlistOpenCIDR}}, nil
	}
	inputs := o.allow
	if o.myIP {
		mine, err := fetchClientIP(rt, client)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, mine)
	}
	rules := newRules(inputs, "")
	if err := checkAllowlistBounds(rules, ""); err != nil {
		return nil, err
	}
	return rules, nil
}

// serviceCreateRules resolves the allowlist flags for a service write.
// They describe a new endpoint, so on a service that already exists
// they are refused, naming the verb that edits its rules.
func serviceCreateRules(
	rt *module.Runtime, client *api.ClientWithResponses,
	db *api.ManagedDatabase, t api.ServiceConfigServiceType,
	intent serviceIntent, o *createAllowOpts, group string,
) ([]api.IPAllowlistRule, error) {
	if !o.given() {
		return nil, nil
	}
	if intent != intentDeploy {
		return nil, newExitError(fmt.Sprintf(
			"--allow, --my-ip and --open set a new service's "+
				"allowlist, and database %s already has a %q service; "+
				"change its rules with %q",
			db.Id, t, fmt.Sprintf("%s allowlist add %s --service %s",
				strings.TrimSuffix(group, " "+string(t)), db.Id, t)),
			ExitUsage)
	}
	return o.rules(rt, client)
}

// applyNewService writes svc with rules as its allowlist when rules is
// non-nil, and names an open endpoint once the write is accepted.
func applyNewService(
	rt *module.Runtime, client *api.ClientWithResponses,
	db *api.ManagedDatabase, svc api.ServiceConfig,
	rules []api.IPAllowlistRule, what, dbID string,
) error {
	if rules != nil {
		svc.IpAllowlist = &api.IPAllowlist{Rules: rules}
	}
	err := applyServices(rt, client, db, buildServiceList(db, svc),
		what, dbID)
	if err == nil {
		warnIfOpen(rt, rules, svc.ServiceType, db.Id)
	}
	return err
}
