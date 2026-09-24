package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// cloudAccountColumns are the table headers shared by cloud-account
// list and get.
var cloudAccountColumns = []string{"ID", "NAME", "TYPE", "CREATED"}

// NewCloudAccountCmd builds the `pgedge starfleet byoc cloud-account` command
// group. The plural "cloud-accounts" is kept as a plural alias
// (unlisted in help) so existing scripts keep working.
func NewCloudAccountCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cloud-account",
		Aliases: []string{"cloud-accounts"},
		Short:   "Manage pgEdge BYOC provider accounts",
		Long: `cloud-account manages the cloud provider accounts (AWS,
Azure, GCP) that pgEdge BYOC deploys clusters into.

Use these commands to list, inspect, create, and delete cloud
accounts, and to fetch the CloudFormation template that grants
pgEdge the IAM role it needs on AWS.

Example:
  pgedge starfleet byoc cloud-account list
  pgedge starfleet byoc cloud-account create --type aws --role-arn <arn>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newCloudAccountListCmd(rt),
		newCloudAccountGetCmd(rt),
		newCloudAccountCreateCmd(rt),
		newCloudAccountDeleteCmd(rt),
		newCloudAccountCFTemplateCmd(rt),
		newCloudAccountZonesCmd(rt),
	)
	return cmd
}

// --- list ---

func newCloudAccountListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cloud accounts",
		Long: `list shows the cloud provider accounts on the active
account.

Use it to find a cloud account's ID before running get or delete.

Example:
  pgedge starfleet byoc cloud-account list
  pgedge starfleet byoc cloud-account list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListCloudAccountsWithResponse(
				context.Background())
			if err != nil {
				return fmt.Errorf("list cloud accounts: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			accounts := resp.JSON200
			if accounts == nil || len(*accounts) == 0 {
				fmt.Fprintln(rt.Stderr, "No cloud accounts found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*accounts))
			for _, a := range *accounts {
				rows = append(rows, cloudAccountRowFrom(a))
			}
			return rt.Output.Print(rows, cloudAccountColumns)
		},
	}
}

// --- get ---

func newCloudAccountGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <cloud_account_id>",
		Short: "Show cloud account details",
		Long: `get shows the details of a single cloud account.

Use it to confirm a cloud account's name and provider type. The
argument is the account's UUID.

Example:
  pgedge starfleet byoc cloud-account get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc cloud-account get b0c1d2e3-f4a5-6789-bcde-890123456789 \
    -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "cloud account ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetCloudAccountWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get cloud account: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			a := resp.JSON200
			if a == nil {
				fmt.Fprintln(rt.Stderr, "No cloud account data returned.")
				return nil
			}
			rows := []output.Row{cloudAccountRowFrom(*a)}
			return rt.Output.Print(rows, cloudAccountColumns)
		},
	}
}

// --- create ---

func newCloudAccountCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		accountType string
		name        string
		description string

		// AWS
		roleARN string

		// Azure
		tenantID       string
		subscriptionID string
		clientID       string
		clientSecret   string
		resourceGroup  string

		// GCP
		projectID      string
		serviceAccount string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cloud account",
		Long: `create registers a new cloud provider account with pgEdge
BYOC.

Use it to connect an AWS, Azure, or GCP account so clusters can be
deployed into it. The credential flags required depend on --type:
aws needs --role-arn; azure needs --tenant-id, --subscription-id,
--azure-client-id, and --azure-client-secret; gcp needs
--project-id and --service-account. A missing one, or a --type
outside aws, azure and gcp, is refused locally with exit 2, before
any request is sent.

Example:
  pgedge starfleet byoc cloud-account create --type aws --role-arn <arn>
  pgedge starfleet byoc cloud-account create --type gcp \
    --project-id my-proj --service-account svc@my-proj.iam`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			providerType := strings.ToLower(accountType)

			// Before clientFromCmd, which resolves credentials: a
			// check after it would exit 5 "no credentials found" for a
			// command-line mistake.
			var creds api.CreateCloudAccountInput_Credentials

			switch providerType {
			case "aws":
				if roleARN == "" {
					return newExitError("--role-arn is required for --type aws", ExitUsage)
				}
				if err := creds.FromAwsCredentials(api.AwsCredentials{
					RoleArn: roleARN,
				}); err != nil {
					return fmt.Errorf("build AWS credentials: %w", err)
				}

			case "azure":
				missing := []string{}
				if tenantID == "" {
					missing = append(missing, "--tenant-id")
				}
				if subscriptionID == "" {
					missing = append(missing, "--subscription-id")
				}
				if clientID == "" {
					missing = append(missing, "--azure-client-id")
				}
				if clientSecret == "" {
					missing = append(missing, "--azure-client-secret")
				}
				if len(missing) > 0 {
					return newExitError(fmt.Sprintf(
						"%s required for --type azure",
						strings.Join(missing, ", ")), ExitUsage)
				}
				// A pointer because the field is writeOnly in byoc.yaml,
				// not optional: the schema still requires it.
				azCreds := api.AzureCredentials{
					TenantId:       tenantID,
					SubscriptionId: subscriptionID,
					ClientId:       clientID,
					ClientSecret:   &clientSecret,
				}
				if resourceGroup != "" {
					azCreds.ResourceGroup = &resourceGroup
				}
				if err := creds.FromAzureCredentials(azCreds); err != nil {
					return fmt.Errorf("build Azure credentials: %w", err)
				}

			case "gcp":
				missing := []string{}
				if projectID == "" {
					missing = append(missing, "--project-id")
				}
				if serviceAccount == "" {
					missing = append(missing, "--service-account")
				}
				if len(missing) > 0 {
					return newExitError(fmt.Sprintf(
						"%s required for --type gcp",
						strings.Join(missing, ", ")), ExitUsage)
				}
				// A pointer for the same reason as Azure's ClientSecret.
				if err := creds.FromGoogleCredentials(
					api.GoogleCredentials{
						ProjectId:      projectID,
						Provider:       "gcp",
						ServiceAccount: &serviceAccount,
					}); err != nil {
					return fmt.Errorf("build GCP credentials: %w", err)
				}

			default:
				return newExitError(fmt.Sprintf(
					"unknown provider type %q: must be aws, azure, "+
						"or gcp", accountType), ExitUsage)
			}

			body := api.CreateCloudAccountJSONRequestBody{
				Type:        providerType,
				Credentials: creds,
			}
			if name != "" {
				body.Name = &name
			}
			if description != "" {
				body.Description = &description
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.CreateCloudAccountWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create cloud account: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			a := resp.JSON200
			if rt.Output.Structured() {
				return rt.Output.Print(a, nil)
			}
			if a == nil {
				fmt.Fprintln(rt.Stderr,
					"Cloud account created (no details returned).")
				return nil
			}
			fmt.Fprintf(rt.Stderr,
				"Cloud account %q created (id: %s, type: %s).\n",
				a.Name, output.Sanitize(a.Id), output.Sanitize(a.Type))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&accountType, "type", "",
		"Cloud provider type: aws, azure, or gcp")
	f.StringVar(&name, "name", "",
		"Display name for the cloud account")
	f.StringVar(&description, "description", "", "Optional description")
	f.StringVar(&roleARN, "role-arn", "",
		"AWS IAM Role ARN (required for --type aws)")
	f.StringVar(&tenantID, "tenant-id", "",
		"Azure tenant ID (required for --type azure)")
	f.StringVar(&subscriptionID, "subscription-id", "",
		"Azure subscription ID (required for --type azure)")
	f.StringVar(&clientID, "azure-client-id", "",
		"Azure client/application ID (required for --type azure)")
	f.StringVar(&clientSecret, "azure-client-secret", "",
		"Azure client secret (required for --type azure)")
	f.StringVar(&resourceGroup, "resource-group", "",
		"Azure resource group (optional for --type azure)")
	f.StringVar(&projectID, "project-id", "",
		"GCP project ID (required for --type gcp)")
	f.StringVar(&serviceAccount, "service-account", "",
		"GCP service account email (required for --type gcp)")
	_ = cmd.MarkFlagRequired("type")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newCloudAccountDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <cloud_account_id>",
		Short: "Delete a cloud account",
		Long: `delete removes a cloud provider account from pgEdge BYOC.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument is the account's UUID.

Example:
  pgedge starfleet byoc cloud-account delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc cloud-account delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
    --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "cloud account ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete cloud account %s? This cannot be undone.",
				args[0])
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteCloudAccount(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete cloud account: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete cloud account"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Cloud account %s deleted.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- cloudformation-template ---

func newCloudAccountCFTemplateCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "cloudformation-template",
		Short: "Show the AWS CloudFormation template URL",
		Long: `cloudformation-template prints the CloudFormation template
URL used to grant pgEdge the AWS IAM role it needs.

Use it before creating an AWS cloud account: deploy the template
in your AWS account, then pass the resulting role ARN to create.

Example:
  pgedge starfleet byoc cloud-account cloudformation-template`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// Untyped call: the generated parser decodes 200 into the
			// array the spec declares, and the API sends a bare object
			// (measured 2026-08-30), so the typed call fails.
			resp, err := client.GetCloudFormationTemplate(
				context.Background())
			if err != nil {
				return fmt.Errorf("get cloudformation template: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("get cloudformation template: %w", err)
			}
			if err := checkResponse(resp.StatusCode, string(body)); err != nil {
				return err
			}

			payload, templates, err := decodeCloudFormationTemplates(body)
			if err != nil {
				return fmt.Errorf("get cloudformation template: %w", err)
			}

			if rt.Output.Structured() {
				return rt.Output.Print(payload, nil)
			}

			printed := 0
			for _, tmpl := range templates {
				if tmpl.Url == "" {
					continue
				}
				// One URL per line, same shape as availability-zones.
				fmt.Fprintln(rt.Stdout, output.Sanitize(tmpl.Url))
				printed++
			}
			if printed == 0 {
				fmt.Fprintln(rt.Stderr, "No template returned.")
			}
			return nil
		},
	}
}

// decodeCloudFormationTemplates accepts the object the API sends and
// the array the spec declares, so the command keeps working if the API
// brings the two into line either way. payload is the value as it
// arrived, untyped so a field the spec does not know survives into the
// structured formats; templates is the typed view for text.
func decodeCloudFormationTemplates(
	body []byte,
) (payload any, templates []api.CloudFormationTemplate, err error) {
	trimmed := bytes.TrimSpace(body)
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, nil, err
	}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []api.CloudFormationTemplate
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, nil, err
		}
		return payload, list, nil
	}
	var one api.CloudFormationTemplate
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, nil, err
	}
	return payload, []api.CloudFormationTemplate{one}, nil
}

// --- availability-zones ---

func newCloudAccountZonesCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "availability-zones <cloud_account_id>",
		Short: "List availability zones in a region",
		Long: `availability-zones lists the availability zones a cloud
account can reach in one region.

Use it before 'cluster create' to check the zone names that
--node accepts, rather than guessing them. The argument is the
cloud account's UUID; --region names a provider region such as
us-east-2, spelled the way every other command spells it.

Example:
  pgedge starfleet byoc cloud-account availability-zones \
    b0c1d2e3-f4a5-6789-bcde-890123456789 --region us-east-2
  pgedge starfleet byoc cloud-account availability-zones \
    b0c1d2e3-f4a5-6789-bcde-890123456789 --region us-east-2 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "cloud account ID")
			if err != nil {
				return err
			}
			// A blank region collapses the path segment, addressing
			// /regions/availability-zones at exit 0. MarkFlagRequired
			// tests only Changed, so this refuses the blank.
			region, err := cli.RequiredStringFlag(cmd.Flags(),
				"region", "name a provider region such as us-east-2")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListAvailabilityZonesWithResponse(
				context.Background(), id, region)
			if err != nil {
				return fmt.Errorf("list availability zones: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			// The API wraps the list in an object rather than
			// returning a bare array, so the zones are one field down.
			body := resp.JSON200
			if body == nil || body.AvailabilityZones == nil ||
				len(*body.AvailabilityZones) == 0 {
				fmt.Fprintln(rt.Stderr, "No availability zones found.")
				return nil
			}
			for _, zone := range *body.AvailabilityZones {
				// One zone per line is the structure, so a newline in a
				// zone would forge another. The renderer is bypassed,
				// so escape here.
				fmt.Fprintln(rt.Stdout, output.Sanitize(zone))
			}
			return nil
		},
	}
	cmd.Flags().String("region", "",
		"Provider region to list zones in (e.g. us-east-2)")
	_ = cmd.MarkFlagRequired("region")

	return cmd
}

// --- row adapter ---

type cloudAccountRow struct {
	id, name, typ, created string
}

func (r cloudAccountRow) Columns() []string {
	return []string{r.id, r.name, r.typ, r.created}
}

// cloudAccountRowFrom adapts an api.CloudAccount into a table row.
func cloudAccountRowFrom(a api.CloudAccount) cloudAccountRow {
	return cloudAccountRow{
		id:      a.Id,
		name:    a.Name,
		typ:     a.Type,
		created: output.FormatTime(a.CreatedAt),
	}
}
