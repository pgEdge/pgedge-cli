package cmd

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

// NewClientIPCmd builds `pgedge starfleet managed client-ip`, a leaf
// rather than a resource: there is nothing to list, create or delete.
func NewClientIPCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "client-ip",
		Short: "Show the source address the API sees for you",
		Long: `client-ip prints the IPv4 address the pgEdge API observed
this request arriving from, so you can allow it on a database without
asking a third-party address-echo service.

The value is a convenience, not a fact about your database traffic. It
is the address of the machine running this command as the API saw it,
which is not necessarily the address your Postgres client will connect
from, and a caller that sets its own forwarding headers changes what is
reported. It is never used to decide who may connect; only the rules
you set are. Verify by connecting.

Text output is the bare address on stdout, so it can be scripted; the
caveat goes to stderr. -o json prints the API's {"ip_address"} object.
Exit 1 when the API reports no address or an IPv6 one, which no
allowlist can hold.

Example:
  pgedge starfleet managed client-ip
  pgedge starfleet managed database allowlist add <database_id> --my-ip`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetManagedClientIPWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("client-ip: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return newExitError(
					"The API could not determine your address.",
					ExitGeneral)
			}
			cidr, err := observedIPv4(rt, resp.JSON200.IpAddress)
			if err != nil {
				return err
			}
			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}
			fmt.Fprintln(rt.Output.Out, strings.TrimSuffix(cidr, "/32"))
			return nil
		},
	}
}

// observedIPv4 validates the API's reported address, prints the caveat
// and returns the address as a /32 ready for an allowlist. Exit 1 on an
// empty value or anything that is not IPv4: the ingress the rules are
// enforced at is reachable over IPv4 only, so an IPv6 address could
// never be allowed.
func observedIPv4(rt *module.Runtime, reported string) (string, error) {
	ip := strings.TrimSpace(reported)
	if ip == "" {
		return "", newExitError(
			"The API could not determine your address.", ExitGeneral)
	}
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return "", newExitError(fmt.Sprintf(
			"the API saw %s, which is not an IPv4 address; an "+
				"allowlist holds IPv4 only", output.Sanitize(ip)),
			ExitGeneral)
	}
	// The contract is explicit that the value is advisory and never an
	// input to enforcement; the CLI must not let it look authoritative.
	fmt.Fprintf(rt.Stderr, "The API saw %s. That is the address this "+
		"request arrived from, not necessarily the one your Postgres "+
		"client connects from. Verify by connecting.\n",
		output.Sanitize(a.String()))
	return a.String() + "/32", nil
}

// fetchClientIP is the --my-ip path: one GET, then observedIPv4.
func fetchClientIP(
	rt *module.Runtime, client *api.ClientWithResponses,
) (string, error) {
	resp, err := client.GetManagedClientIPWithResponse(
		context.Background())
	if err != nil {
		return "", fmt.Errorf("client-ip: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", newExitError(
			"The API could not determine your address.", ExitGeneral)
	}
	return observedIPv4(rt, resp.JSON200.IpAddress)
}
