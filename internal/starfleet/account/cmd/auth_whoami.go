package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	api "github.com/pgEdge/pgedge-cli/internal/starfleet/account/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// whoamiReport is the identity `auth whoami` renders. ClientID means
// what it means in authStatusReport, the value --client-id takes;
// the API client record's UUID, which `client get` takes, is
// ClientRecordID.
type whoamiReport struct {
	ClientID       string `json:"client_id"`
	ClientName     string `json:"client_name,omitempty"`
	ClientRecordID string `json:"client_record_id,omitempty"`
	// Not bare `description`: this object flattens two resources.
	ClientDescription string `json:"client_description,omitempty"`
	TenantName        string `json:"tenant_name,omitempty"`
	TenantID          string `json:"tenant_id,omitempty"`
	Plan              string `json:"plan,omitempty"`
	PlanTrial         bool   `json:"plan_trial,omitempty"`
	// TenantCount is doctor.go's tenantInfo.Count.
	TenantCount int    `json:"tenant_count,omitempty"`
	APIURL      string `json:"api_url"`
}

func newAuthWhoamiCmd(
	rt *module.Runtime, f *conn.Flags,
) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the identity the API accepts you as",
		Long: `whoami reports the identity the pgEdge Starfleet API
answers your credential as: the named API client, and the tenant and
plan it is scoped to.

Use it to tell one profile's credential from another's, and to read
the plan behind an entitlement refusal. This is the question
'auth status' cannot answer: status resolves credentials locally and
calls nothing, so it reports what is configured, while whoami reads
the API and reports what the server accepts.

It reads two lists, the API clients of your tenant and the tenants
your credential can reach, and creates no resource. It does cache the
access token it mints, like every command that calls the API, so
prefer 'starfleet doctor' when a broken credential is what you are
diagnosing.

Example:
  pgedge starfleet auth whoami
  pgedge starfleet auth whoami -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var report whoamiReport

			// Resolved before the client is built, so these codes are
			// the ones returned; they match `auth status` and
			// conn.Resolve.
			res, err := conn.ResolveCredentials(
				rt, f.ClientID, f.ClientSecret, f.APIURL)
			if err != nil {
				code := ExitAuth
				if conn.IsUsageError(err) {
					code = ExitUsage
				}
				return newExitError(err.Error(), code)
			}
			creds := res.Creds
			report.APIURL = res.APIURL
			report.ClientID = creds.ClientID

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			ctx := context.Background()

			matched, err := whoamiClientRecord(
				ctx, client, creds.ClientID)
			if err != nil {
				return err
			}
			if matched != nil {
				report.ClientName = matched.Name
				report.ClientRecordID = matched.Id
				report.ClientDescription = matched.Description
			}

			tenants, err := whoamiTenants(ctx, client)
			if err != nil {
				return err
			}
			if len(tenants) > 0 {
				t := tenants[0]
				report.TenantName = t.Name
				report.TenantID = t.Id
				report.Plan = output.DerefString(t.Plan)
				if t.PlanTrial != nil {
					report.PlanTrial = *t.PlanTrial
				}
			}
			if len(tenants) > 1 {
				report.TenantCount = len(tenants)
			}

			return printWhoami(rt, report, matched == nil,
				len(tenants) == 0)
		},
	}
}

// whoamiClientRecord returns the API client record whose auth0_id is
// the credential now in use, or nil when the tenant's client list
// carries no such record.
//
// Matching is on auth0_id, not id: the credential's ID is the Auth0
// client ID, while `id` is the record's UUID. On four live credentials
// on 2026-09-01 the caller's own record was present and matched every
// time.
//
// A nil result is not an error: the record is there in practice, but
// this separate read could answer without it, and the tenant half of
// the report is worth printing either way.
func whoamiClientRecord(
	ctx context.Context, client *api.ClientWithResponses, clientID string,
) (*api.ApiClient, error) {
	resp, err := client.ListClientsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	for _, c := range *resp.JSON200 {
		if c.Auth0Id == clientID {
			return &c, nil
		}
	}
	return nil, nil
}

// whoamiTenants reads the tenants the credential can reach: one, the
// token's own, on four live credentials on 2026-09-01, but the length
// is reported rather than assumed.
func whoamiTenants(
	ctx context.Context, client *api.ClientWithResponses,
) ([]api.Tenant, error) {
	resp, err := client.ListTenantsWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

// printWhoami renders report through rt.Output for json/yaml and as a
// label block for text. Values go through output.Sanitize: a name
// carrying a newline would otherwise forge a line of the block.
func printWhoami(
	rt *module.Runtime, report whoamiReport, noClient, noTenant bool,
) error {
	if noClient {
		fmt.Fprintln(rt.Stderr,
			"No API client in this tenant carries this client ID.")
	}
	if noTenant {
		fmt.Fprintln(rt.Stderr, "No tenant returned for this credential.")
	}

	if rt.Output.Structured() {
		return rt.Output.Print(report, nil)
	}

	fmt.Fprintf(rt.Stdout, "Client ID:     %s\n",
		output.Sanitize(report.ClientID))
	if report.ClientName != "" {
		fmt.Fprintf(rt.Stdout, "Client name:   %s\n",
			output.Sanitize(report.ClientName))
	}
	if report.ClientDescription != "" {
		fmt.Fprintf(rt.Stdout, "Description:   %s\n",
			output.Sanitize(report.ClientDescription))
	}
	if report.ClientRecordID != "" {
		fmt.Fprintf(rt.Stdout, "Client record: %s\n",
			output.Sanitize(report.ClientRecordID))
	}
	if report.TenantName != "" {
		fmt.Fprintf(rt.Stdout, "Tenant:        %s\n",
			output.Sanitize(report.TenantName))
	}
	if report.TenantID != "" {
		fmt.Fprintf(rt.Stdout, "Tenant ID:     %s\n",
			output.Sanitize(report.TenantID))
	}
	// Two calls, not one composed string: a Sanitize wrap hidden
	// behind a local is invisible to the stderr-sanitize gate.
	if report.Plan != "" {
		if report.PlanTrial {
			fmt.Fprintf(rt.Stdout, "Plan:          %s (trial)\n",
				output.Sanitize(report.Plan))
		} else {
			fmt.Fprintf(rt.Stdout, "Plan:          %s\n",
				output.Sanitize(report.Plan))
		}
	}
	if report.TenantCount > 1 {
		fmt.Fprintf(rt.Stdout, "Tenants:       %d\n", report.TenantCount)
	}
	fmt.Fprintf(rt.Stdout, "API URL:       %s\n",
		output.Sanitize(report.APIURL))
	return nil
}
