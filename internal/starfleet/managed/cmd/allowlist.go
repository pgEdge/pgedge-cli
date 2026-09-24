package cmd

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
)

// The contract's declared bounds (openapi/managed.yaml, IPAllowlist
// rules maxItems and IPAllowlistRule label maxLength). Only these two
// are refused locally; CIDR grammar is the server's to refuse.
const (
	allowlistMaxRules    = 50
	allowlistLabelMaxLen = 64
	// allowlistOpenCIDR is the one rule the API reports as state
	// "open". It is a literal match server-side, not a coverage test.
	allowlistOpenCIDR = "0.0.0.0/0"
	// endpointPostgres names the database's own endpoint in output,
	// where a service endpoint is named by its type.
	endpointPostgres = "postgres"
)

// normalizeCIDR returns the form the server stores: a bare IPv4 address
// as a /32, a block masked to its network address. ok is false for
// anything that is not IPv4, and s comes back untouched so the caller
// can send it as typed and relay the server's refusal.
func normalizeCIDR(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		if !a.Is4() {
			return s, false
		}
		return a.String() + "/32", true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		if !p.Addr().Is4() {
			return s, false
		}
		return p.Masked().String(), true
	}
	return s, false
}

// ruleIndex finds s in rules, as typed first and then by normalised
// form, so "203.0.113.7" finds the stored "203.0.113.7/32". -1 when
// absent.
func ruleIndex(rules []api.IPAllowlistRule, s string) int {
	s = strings.TrimSpace(s)
	for i, r := range rules {
		if r.Cidr == s {
			return i
		}
	}
	n, ok := normalizeCIDR(s)
	if !ok {
		return -1
	}
	for i, r := range rules {
		if rn, _ := normalizeCIDR(r.Cidr); rn == n {
			return i
		}
	}
	return -1
}

// labelPtr is nil for an empty label, so the field is omitted rather
// than sent as "".
func labelPtr(label string) *string {
	if label == "" {
		return nil
	}
	return &label
}

// newRules builds rules from inputs as typed (trimmed), each carrying
// label, with duplicates by normalised form dropped after the first.
func newRules(inputs []string, label string) []api.IPAllowlistRule {
	rules := make([]api.IPAllowlistRule, 0, len(inputs))
	for _, in := range inputs {
		in = strings.TrimSpace(in)
		if ruleIndex(rules, in) >= 0 {
			continue
		}
		rules = append(rules, api.IPAllowlistRule{
			Cidr: in, Label: labelPtr(label)})
	}
	return rules
}

// mergeRules appends the inputs not already in current. skipped names
// the inputs already in current, as typed, for the caller to report; an
// input repeated within one call is dropped silently.
func mergeRules(
	current []api.IPAllowlistRule, inputs []string, label string,
) (merged []api.IPAllowlistRule, skipped []string) {
	merged = make([]api.IPAllowlistRule, 0, len(current)+len(inputs))
	merged = append(merged, current...)
	for _, in := range inputs {
		in = strings.TrimSpace(in)
		if ruleIndex(current, in) >= 0 {
			skipped = append(skipped, in)
			continue
		}
		if ruleIndex(merged, in) >= 0 {
			continue // duplicate within this call; nothing to report
		}
		merged = append(merged, api.IPAllowlistRule{
			Cidr: in, Label: labelPtr(label)})
	}
	return merged, skipped
}

// checkAllowlistBounds refuses, before any request, the two limits the
// contract declares.
func checkAllowlistBounds(rules []api.IPAllowlistRule, label string) error {
	if n := utf8.RuneCountInString(label); n > allowlistLabelMaxLen {
		return newExitError(fmt.Sprintf(
			"--label is %d characters; the maximum is %d",
			n, allowlistLabelMaxLen), ExitUsage)
	}
	if len(rules) > allowlistMaxRules {
		return newExitError(fmt.Sprintf(
			"the allowlist would hold %d rules; the maximum is %d",
			len(rules), allowlistMaxRules), ExitUsage)
	}
	return nil
}

// endpointName is what output calls an endpoint: "postgres" for the
// database's own, the service type otherwise.
func endpointName(svcType api.ServiceConfigServiceType) string {
	if svcType == "" {
		return endpointPostgres
	}
	return string(svcType)
}

// resolveEndpoint turns the --service value into the endpoint a verb
// edits: "" is the Postgres endpoint, anything else names a deployed
// service by type. It returns the current rules as a non-nil slice so
// a caller can send them back closed ([]) rather than omitted (nil).
func resolveEndpoint(
	db *api.ManagedDatabase, typed, dbID string,
) (api.ServiceConfigServiceType, *api.ServiceConfig,
	[]api.IPAllowlistRule, error) {
	if typed == "" {
		return "", nil, nonNilRules(db.IpAllowlist.Rules), nil
	}
	svcType, err := parseServiceType(typed)
	if err != nil {
		return "", nil, nil, err
	}
	svc := findService(db, svcType)
	if svc == nil {
		return "", nil, nil, newExitError(fmt.Sprintf(
			"no %q service deployed on database %s", typed, dbID),
			ExitNotFound)
	}
	if svc.IpAllowlist == nil {
		return svcType, svc, []api.IPAllowlistRule{}, nil
	}
	return svcType, svc, nonNilRules(svc.IpAllowlist.Rules), nil
}

func nonNilRules(r []api.IPAllowlistRule) []api.IPAllowlistRule {
	if r == nil {
		return []api.IPAllowlistRule{}
	}
	return r
}

// applyAllowlist sends rules as the endpoint's whole list.
//
// The Postgres endpoint PATCHes ip_allowlist alone, so saas runs
// update-managed-ip-allowlists. A service endpoint's list lives in its
// services entry, so that write sends the whole array through
// applyServices and saas runs the services job.
//
// rules is never sent nil: [] closes the endpoint, nil would omit the
// field and leave it unchanged, and those are different requests.
func applyAllowlist(
	rt *module.Runtime, client *api.ClientWithResponses,
	db *api.ManagedDatabase, svc *api.ServiceConfig,
	rules []api.IPAllowlistRule, what, dbID string,
) error {
	rules = nonNilRules(rules)
	if svc != nil {
		services := make([]api.ServiceConfig, 0)
		if db.Services != nil {
			for _, s := range *db.Services {
				if s.ServiceType == svc.ServiceType {
					s.IpAllowlist = &api.IPAllowlist{Rules: rules}
				}
				services = append(services, s)
			}
		}
		return applyServices(rt, client, db, services, what, dbID)
	}

	id, err := uuid.Parse(db.Id)
	if err != nil {
		return newExitError(fmt.Sprintf(
			"invalid database ID %q: %v", db.Id, err), ExitGeneral)
	}
	body := api.UpdateManagedDatabaseJSONRequestBody{
		IpAllowlist: &api.IPAllowlist{Rules: rules},
	}
	base := captureTaskBaseline(client, db.Id)
	resp, err := client.UpdateManagedDatabaseWithResponse(
		context.Background(), id, body)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}
	if err := printUpdatedDatabase(rt, resp.JSON200); err != nil {
		return err
	}
	fmt.Fprintf(rt.Stderr, "%s on database %s.\n",
		output.Sanitize(what), output.Sanitize(dbID))
	return trackMutation(rt, client, db.Id, base)
}

// warnIfOpen prints the open warning when rules carry the open CIDR.
// The server has no gate on it; the CLI names the consequence.
func warnIfOpen(
	rt *module.Runtime, rules []api.IPAllowlistRule,
	typ api.ServiceConfigServiceType, dbID string,
) {
	if ruleIndex(rules, allowlistOpenCIDR) < 0 {
		return
	}
	fmt.Fprintf(rt.Stderr, "The %s endpoint of database %s is now open "+
		"to every address. Postgres roles and passwords are the only "+
		"guard.\n", output.Sanitize(endpointName(typ)), output.Sanitize(dbID))
}

// confirmClose is the lockout guard: a write that leaves an endpoint
// with no rules asks first, because nobody will be able to connect. It
// is deliberately not an own-IP check; the operator's laptop is rarely
// the address that needs to connect.
func confirmClose(
	rt *module.Runtime, typ api.ServiceConfigServiceType,
	dbID string, force bool,
) error {
	return cli.Confirm(rt, fmt.Sprintf(
		"Close the %s endpoint of database %s? No address will be able "+
			"to connect; open sessions stay up.",
		endpointName(typ), dbID), force)
}
