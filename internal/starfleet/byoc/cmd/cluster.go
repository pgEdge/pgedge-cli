package cmd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// clusterColumns are the table headers shared by cluster list and get.
var clusterColumns = []string{"ID", "NAME", "STATUS", "REGIONS", "CREATED"}

// NewClusterCmd builds the `pgedge starfleet byoc cluster` command group. The
// plural "clusters" is kept as a plural alias (unlisted in help) so
// existing scripts keep working.
func NewClusterCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster",
		Aliases: []string{"clusters"},
		Short:   "Manage pgEdge BYOC clusters",
		Long: `cluster manages the pgEdge BYOC clusters that host your
databases: the cloud infrastructure a database runs on.

Use these commands to list, inspect, create, update, and delete
clusters, and to manage the shares carved out of them.

Example:
  pgedge starfleet byoc cluster list
  pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newClusterListCmd(rt),
		newClusterGetCmd(rt),
		newClusterCreateCmd(rt),
		newClusterDeleteCmd(rt),
		newClusterUpdateCmd(rt),
		newClusterMetricsCmd(rt),
		NewClusterShareCmd(rt),
	)
	return cmd
}

// --- list ---

func newClusterListCmd(rt *module.Runtime) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List clusters",
		Long: `list shows the clusters in the active account.

Use it to find a cluster's ID before running get, update, or
delete. Pass --limit and --offset to page through large accounts.

Example:
  pgedge starfleet byoc cluster list
  pgedge starfleet byoc cluster list --limit 20 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client: a bad --limit is knowable locally, so
			// it answers 2 rather than 5 for credentials it never needed.
			// byoc.yaml declares no paging bounds on any list endpoint,
			// hence NoUpperBound: the server clamps at 100 today, but a
			// measured clamp is not a published contract and the CLI must
			// not refuse a value the API would accept.
			limit, sendLimit, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "limit", cli.LimitLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			offset, sendOffset, err := cli.OptionalIntFlagInRange(
				cmd.Flags(), "offset", cli.OffsetLowest, cli.NoUpperBound)
			if err != nil {
				return err
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListClustersParams{}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}

			resp, err := client.ListClustersWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list clusters: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			clusters := resp.JSON200
			if clusters == nil || len(*clusters) == 0 {
				fmt.Fprintln(rt.Stderr, "No clusters found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*clusters))
			for _, c := range *clusters {
				rows = append(rows, clusterRow{
					id:      c.Id,
					name:    c.Name,
					status:  c.Status,
					regions: joinStrings(c.Regions),
					created: output.FormatTime(c.CreatedAt),
				})
			}
			if err := rt.Output.Print(rows, clusterColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*clusters), limit,
				clusterDefaults, "results")
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(clusterDefaults))
	cmd.Flags().IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	return cmd
}

// --- get ---

func newClusterGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster_id>",
		Short: "Show cluster details",
		Long: `get shows the details of a single cluster.

Use it to check a cluster's status, regions, and creation date.
The argument takes a full UUID.

Example:
  pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890
  pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetClusterWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get cluster: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			c := resp.JSON200
			if c == nil {
				fmt.Fprintln(rt.Stderr, "No cluster data returned.")
				return nil
			}
			rows := []output.Row{clusterRow{
				id:      c.Id,
				name:    c.Name,
				status:  c.Status,
				regions: joinStrings(c.Regions),
				created: output.FormatTime(c.CreatedAt),
			}}
			return rt.Output.Print(rows, clusterColumns)
		},
	}
}

// --- create ---

// clusterCreateOpts collects the create-cluster flag values so
// buildClusterCreateBody can assemble the request without reaching for
// package-level state.
type clusterCreateOpts struct {
	name           string
	cloudAccountID string
	nodeLocation   string
	instanceType   string
	volumeSize     int
	// volumeSizeSet records whether --volume-size was given at all.
	// volumeSize alone cannot say: 0 is both the flag's zero value and
	// a value an operator can type, and the two are different requests
	// (#256).
	volumeSizeSet  bool
	regions        []string
	backupStoreIDs []string
	firewallRules  []string
	networks       []string
	nodes          []string
}

func newClusterCreateCmd(rt *module.Runtime) *cobra.Command {
	opts := &clusterCreateOpts{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cluster",
		Long: `create provisions a new cluster from the given regions,
nodes, and network settings.

Use it to stand up the infrastructure a database will run on.
Attach at least one backup store (--backup-store-id) so the cluster
can host a database. Pass --wait to block until provisioning
finishes.

--node-location and --cloud-account-id are checked before anything is
provisioned: a location outside the contract's enum is exit 2, and a
cloud account that does not exist stops the create rather than being
refused after the fact. --regions is not checked, because byoc
publishes no region list to check it against -- a region that does not
exist is refused by the API.

Example:
  pgedge starfleet byoc cluster create --name prod \
    --cloud-account-id <cloud_account_id> \
    --regions us-east-1 --node-location public \
    --instance-type r7g.medium --volume-size 30 \
    --backup-store-id <backup_store_id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.volumeSizeSet = cmd.Flags().Changed("volume-size")
			body, err := buildClusterCreateBody(rt, opts)
			if err != nil {
				// Passed through unchanged, never rebuilt. cli.ExitCode
				// maps *cli.UsageError to 2 and consults the coder
				// interface for *ExitError, so an error arriving here
				// already carries the code it should exit with --
				// re-wrapping it could only lose one. The old form
				// special-cased *ExitError and flattened everything
				// else to ExitGeneral, which would have silently
				// returned a *cli.UsageError to exit 1 the moment a
				// parse moved inside this builder (#309).
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			// One read, on the verb where it is worth the most: a dry
			// run that passes a nonexistent --cloud-account-id gives
			// the strongest possible false confidence right before the
			// CLI provisions real cloud infrastructure (#256).
			//
			// A missing account answers exit 4 rather than 2 -- the
			// value is well-formed and names nothing, which is what
			// the unknown-resource code is for.
			//
			// --regions gets no such check, and the reason is worth
			// recording so nobody looks for the missing half: byoc
			// publishes no region list. The only region-shaped
			// endpoint takes a region as INPUT
			// (/cloud-accounts/{id}/regions/{region}/availability-
			// zones) and answers an empty list for a region that does
			// not exist, so it cannot tell a bad region from a real
			// one with no zones. Measured against a BYOC dev tenant: `mars-1`
			// returns an empty list at exit 0.
			if err := checkCloudAccountExists(
				context.Background(), rt, client,
				opts.cloudAccountID); err != nil {
				return err
			}

			// Printed only once the request is actually going out, so a
			// rejection above doesn't also spam a warning about a
			// request that was never sent. That is why it sits below
			// the cloud-account check and not above it: the check
			// answers exit 4 and sends nothing, and a warning about
			// the backup store of a cluster that was never created
			// reads as though it had been.
			if w := backupStoreWarning(opts.backupStoreIDs); w != "" {
				fmt.Fprintln(rt.Stderr, w)
			}

			resp, err := client.CreateClusterWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create cluster: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			c := resp.JSON200
			if c == nil {
				// Accepted, but no body to read an id from — nothing to
				// track.
				fmt.Fprintln(rt.Stderr,
					"Cluster created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(c, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Cluster %q created (id: %s, status: %s).\n",
					c.Name, output.Sanitize(c.Id), output.Sanitize(c.Status))
			}
			return trackMutation(rt, client, c.Id, "")
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.name, "name", "", "Cluster name")
	f.StringVar(&opts.cloudAccountID, "cloud-account-id", "",
		"Cloud account UUID; checked to exist before the cluster is "+
			"created")
	f.StringSliceVar(&opts.regions, "regions", nil,
		"Comma-separated list of regions")
	f.StringVar(&opts.nodeLocation, "node-location", "",
		"Node location: "+strings.Join(nodeLocations, " or "))
	f.StringSliceVar(&opts.backupStoreIDs, "backup-store-id", nil,
		"Backup store ID to attach (repeatable; required to host a DB)")
	f.StringArrayVar(&opts.firewallRules, "firewall-rule", nil,
		"Firewall rule (repeatable). name must be one of "+
			"http, https, postgres, ssh. "+
			"e.g. name=https,port=443,sources=0.0.0.0/0")
	f.StringArrayVar(&opts.networks, "network", nil,
		"Network settings (repeatable, one per region). Keys: region, "+
			"cidr, public-subnets, private-subnets, subnets, external, "+
			"external-id, name. AWS and Azure use public-subnets and "+
			"private-subnets; GCP uses subnets. "+
			"e.g. region=us-east-1,cidr=10.4.0.0/16,"+
			"public-subnets=10.4.1.0/24,private-subnets=10.4.128.0/24")
	f.StringArrayVar(&opts.nodes, "node", nil,
		"Node settings (repeatable). "+
			"e.g. name=n1,region=us-east-1,instance-type=r7g.medium,"+
			"volume-size=30")
	f.StringVar(&opts.instanceType, "instance-type", "",
		"Instance type for all nodes (shorthand for --node; "+
			"creates one node per region)")
	// The floor is interpolated, never spelled out: the generated
	// reference and llms.txt both quote this string, so a hardcoded "1"
	// would go on claiming 1 after the constant moved and docs-check
	// would still pass. Deriving it makes `make docs` a second gate on
	// volumeSizeMin.
	f.IntVar(&opts.volumeSize, "volume-size", 0,
		fmt.Sprintf("Volume size in GB for all nodes, %d or more "+
			"(shorthand for --node; creates one node per region)",
			volumeSizeMin))
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cloud-account-id")
	_ = cmd.MarkFlagRequired("regions")
	_ = cmd.MarkFlagRequired("node-location")
	addWaitFlags(cmd)
	// Fed from nodeLocations, in its order, so the help text and the
	// completion cannot list the two values differently -- and so a
	// third location added to the contract reaches both at once.
	_ = cmd.RegisterFlagCompletionFunc(
		"node-location", completeFixed(nodeLocations...))
	_ = cmd.RegisterFlagCompletionFunc(
		"firewall-rule", completeFirewallRuleName)
	cli.MarkMutating(cmd)

	return cmd
}

// buildClusterCreateBody assembles the create-cluster request from the
// cluster create flag values.
func buildClusterCreateBody(
	rt *module.Runtime, o *clusterCreateOpts,
) (api.CreateClusterJSONRequestBody, error) {
	body := api.CreateClusterJSONRequestBody{
		Name:         o.name,
		Regions:      trimSpaces(o.regions),
		NodeLocation: api.CreateClusterInputNodeLocation(o.nodeLocation),
	}

	// First, and before the client is built: node_location is a closed
	// enum in the contract and nothing was enforcing it.
	if err := validateNodeLocation(rt, o.nodeLocation); err != nil {
		return body, err
	}

	// The same UUID contract backup-store create applies to this
	// field. byoc.yaml types the create body's cloud_account_id as a
	// bare string, but /byoc/v1/cloud-accounts/{id} types the path
	// parameter as a UUID and the generated client will not accept
	// anything else -- so this is the spec's own shape for the field,
	// not one the CLI invented. Whether the account EXISTS is a
	// separate question, answered after the client is built.
	//
	// checkCloudAccountExists parses it again rather than being handed
	// the result: it needs the uuid.UUID for the GET, and it is called
	// from a point that cannot reach this one's local. Parsing is
	// idempotent, and the duplication buys the exit-2-before-anything
	// ordering this call site exists for.
	// The parsed value is KEPT and sent, not discarded. uuid.Parse
	// accepts `{uuid}` and `urn:uuid:uuid` as well as the canonical
	// spelling, so discarding it and forwarding the raw local put a
	// braced id on the wire verbatim -- measured, and the API answers
	// that the same way it answered a prefix. Passing the check and
	// then sending something else is the #257/#274 defect in a
	// different spelling. uuid.UUID.String() is canonical by
	// construction, so every one of these sites sends one shape.
	cloudAccountID, err := parseUUIDArg(
		o.cloudAccountID, "cloud account ID")
	if err != nil {
		return body, err
	}
	canonicalAccount := cloudAccountID.String()
	body.CloudAccountId = &canonicalAccount
	// Recorded, like every other check in this function.
	//
	// The premise first given for adding this was wrong and is worth
	// correcting rather than repeating: `cluster create --dry-run` did
	// NOT report "none — this command has no client-side checks".
	// validateNodeLocation and the private-subnet check already
	// recorded unconditionally, so the report was INCOMPLETE, not
	// false -- two lines missing out of four. Measured by deleting
	// this line and reading the ledger.
	rt.DryRun.Pass("cloud account ID is a UUID")

	// A backup store is a pgEdge resource addressed by UUID —
	// `backup-store get <backup_store_id>` demands one — and this slice
	// reached the request body verbatim, so a prefix came back as a
	// server-side failure naming the store rather than the id. Exactly
	// the #257/#274 shape, found by the ID-flag sweep in
	// internal/clitest after byoc's reference had already claimed the
	// flag was checked.
	if len(o.backupStoreIDs) > 0 {
		canonical := make([]string, 0, len(o.backupStoreIDs))
		for _, raw := range o.backupStoreIDs {
			id, err := parseUUIDArg(raw, "backup store ID")
			if err != nil {
				return body, err
			}
			canonical = append(canonical, id.String())
		}
		rt.DryRun.Pass("%d backup store ID(s) are UUIDs",
			len(o.backupStoreIDs))
		body.BackupStoreIds = &canonical
	}
	for _, raw := range o.firewallRules {
		r, err := parseFirewallRule(raw)
		if err != nil {
			return body, err
		}
		if body.FirewallRules == nil {
			body.FirewallRules = &[]api.ClusterFirewallRuleSettings{}
		}
		*body.FirewallRules = append(*body.FirewallRules, r)
	}
	// Reported for the same reason cluster update reports it: this loop
	// is a client-side check that runs before anything is sent, and a
	// dry run that does not mention it invites the reader to conclude
	// the rules were not checked. Guarded on presence, since
	// "0 firewall rule(s) valid" would be noise.
	if body.FirewallRules != nil {
		rt.DryRun.Pass("%d firewall rule(s) valid", len(*body.FirewallRules))
	}

	networks, err := buildCreateNetworks(o.networks, o.regions)
	if err != nil {
		return body, err
	}
	if err := validatePrivateSubnets(o.nodeLocation, networks); err != nil {
		return body, err
	}
	rt.DryRun.Pass("private-subnet configuration valid for %s nodes",
		o.nodeLocation)
	if len(networks) > 0 {
		body.Networks = &networks
	}

	nodes, err := buildCreateNodes(o.nodes, o.instanceType,
		o.volumeSize, o.volumeSizeSet, o.regions)
	if err != nil {
		return body, err
	}
	if o.volumeSizeSet {
		rt.DryRun.Pass("volume size %d GB is %d or more",
			o.volumeSize, volumeSizeMin)
	}
	if len(nodes) > 0 {
		body.Nodes = &nodes
	}
	return body, nil
}

// --- delete ---

func newClusterDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force, cascade bool
	cmd := &cobra.Command{
		Use:   "delete <cluster_id>",
		Short: "Delete a cluster",
		Long: `delete tears down a cluster.

By default it refuses if the cluster still hosts databases; pass
--cascade to also delete those databases and the cloud
infrastructure. Deletion is destructive, so it prompts for
confirmation unless --force is given. Pass --wait to block until
teardown finishes.

Example:
  pgedge starfleet byoc cluster delete a1b2c3d4-e5f6-7890-abcd-ef1234567890
  pgedge starfleet byoc cluster delete a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
    --cascade --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before the prompt. A parse costs nothing, so confirming
			// an operation whose ID cannot name anything wastes the
			// operator's answer -- and, on a scripted run without
			// --force, buries the real fault under a prompt refusal.
			// It also makes the shipped example reachable by
			// TestShippedExamplesAreNotMalformed, which waives the
			// destructive-verb refusal and so cannot see a bad ID
			// sitting behind it.
			id, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete cluster %s? This cannot be undone.", id)
			if cascade {
				prompt = fmt.Sprintf(
					"Force-delete cluster %s AND all its databases and "+
						"cloud infrastructure? This cannot be undone.",
					id)
			}
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, id.String())
				if err != nil {
					return err
				}
			}

			params := &api.DeleteClusterParams{}
			if cascade {
				params.Force = &cascade
			}
			resp, err := client.DeleteCluster(
				context.Background(), id, params)
			if err != nil {
				return fmt.Errorf("delete cluster: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete cluster"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Cluster %s deleted.\n", id)
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cmd.Flags().BoolVar(&cascade, "cascade", false,
		"Also delete all databases and cloud infrastructure, "+
			"bypassing status and database-existence checks")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- update ---

func newClusterUpdateCmd(rt *module.Runtime) *cobra.Command {
	var (
		firewallRules  []string
		backupStoreIDs []string
		regions        []string
	)
	cmd := &cobra.Command{
		Use:   "update <cluster_id>",
		Short: "Update a cluster's firewall rules or stores",
		Long: `update appends firewall rules or backup stores to a
cluster, or replaces its regions.

Use it to open a new port, attach an additional backup store, or
add a region after creation. Specify at least one of
--firewall-rule, --backup-store-id, or --regions. Pass --wait to
block until the change finishes.

--regions replaces the whole set rather than adding to it, so pass
every region the cluster needs. It is refused if it would drop a
region the cluster still has a node or a network in: this command
sends regions, nodes and networks together and has no flag for the
last two, so dropping such a region would send a region list that
contradicts them. Remove the nodes first.

Example:
  pgedge starfleet byoc cluster update a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
    --firewall-rule name=https,port=443,sources=0.0.0.0/0
  pgedge starfleet byoc cluster update a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
    --backup-store-id f2a3b4c5-d6e7-8901-fabc-012345678901`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An explicitly empty --backup-store-id is a usage error,
			// checked BEFORE the at-least-one rule below. pflag parses
			// a StringSlice through CSV, so `--backup-store-id ""`
			// yields an EMPTY slice rather than one blank element --
			// indistinguishable from omitting the flag, so it fell
			// through to "specify at least one of ..." at exit 1. That
			// is the wrong code and the wrong diagnosis for
			// `--backup-store-id "$S"` with the variable unset, and
			// byoc's reference states unqualified that an explicitly
			// empty ID filter is exit 2. Changed is the only thing
			// that can tell the two apart.
			if cmd.Flags().Changed("backup-store-id") &&
				len(backupStoreIDs) == 0 {
				return &cli.UsageError{Msg: "--backup-store-id given " +
					"an empty value: name a backup store, or omit the " +
					"flag"}
			}
			if len(firewallRules) == 0 &&
				len(backupStoreIDs) == 0 &&
				len(regions) == 0 {
				return newExitError("cluster update: specify at least one of "+
					"--firewall-rule, --backup-store-id, --regions", ExitUsage)
			}
			// Unconditional, unlike the rules check below: this one runs
			// on every invocation that gets here, so leaving it
			// unreported let `cluster update --dry-run --backup-store-id
			// X` still print "none — this command has no client-side
			// checks" while a client-side check had just passed.
			rt.DryRun.Pass(
				"at least one of --firewall-rule, --backup-store-id, " +
					"--regions given")

			// Parsed BEFORE the client is built, so a mistyped rule
			// costs no token exchange. This loop used to sit after the
			// cluster argument was resolved, which meant a typo still
			// spent a POST to the token endpoint and, back when a
			// cluster could be named by ID prefix, a GET /clusters as
			// well. --backup-store-id joins it for the same reason: a
			// backup store is addressed by UUID and the slice used to
			// reach the body verbatim.
			canonicalStores := make([]string, 0, len(backupStoreIDs))
			for _, raw := range backupStoreIDs {
				id, err := parseUUIDArg(raw, "backup store ID")
				if err != nil {
					return err
				}
				canonicalStores = append(canonicalStores, id.String())
			}
			if len(backupStoreIDs) > 0 {
				rt.DryRun.Pass("%d backup store ID(s) are UUIDs",
					len(backupStoreIDs))
			}
			//
			// The error is returned UNCHANGED. It already carries
			// ExitUsage from parseFirewallRule; re-wrapping it as
			// ExitGeneral here is what made `cluster update` report a
			// mistyped rule as an API failure while `cluster create`
			// reported the identical value as a usage error.
			rules := make([]api.ClusterFirewallRuleSettings, 0,
				len(firewallRules))
			for _, raw := range firewallRules {
				r, err := parseFirewallRule(raw)
				if err != nil {
					return err
				}
				rules = append(rules, r)
			}
			// Recorded so --dry-run stops reporting "none — this verb
			// has no client-side checks", which stopped being true the
			// moment this loop became a pre-flight check that runs
			// before anything is sent. `cluster create` reports the
			// identical parse the identical way, so the same VALID rule
			// set produces the same dry-run line on both verbs. A
			// mistyped rule produces no line on either — the loop above
			// returns first, which is the point: a report must not list
			// a check as passed on the run where it fired.
			if len(rules) > 0 {
				rt.DryRun.Pass("%d firewall rule(s) valid", len(rules))
			}

			id, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}
			// Recorded for the same reason as the loop above, and it
			// was missed for the same reason: a check that runs and
			// records nothing leaves the report incomplete, and this
			// one sits three lines from the one round 5 did record.
			rt.DryRun.Pass("cluster ID is a UUID")

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			getResp, err := client.GetClusterWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get cluster: %w", err)
			}
			if err := checkResponse(getResp.StatusCode(),
				string(getResp.Body)); err != nil {
				return err
			}
			if getResp.JSON200 == nil {
				return newExitError("cluster not found", ExitNotFound)
			}

			regions = trimSpaces(regions)
			if err := checkRegionsKeepTheirNodes(
				rt, getResp.JSON200, regions); err != nil {
				return err
			}

			body := buildClusterUpdate(getResp.JSON200, rules,
				canonicalStores, regions)

			var priorTaskID string
			if tracking() {
				priorTaskID, err = newestSubjectTaskID(
					context.Background(), client, id.String())
				if err != nil {
					return err
				}
			}

			resp, err := client.UpdateClusterWithResponse(
				context.Background(), id, body)
			if err != nil {
				return fmt.Errorf("update cluster: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() &&
				resp.JSON200 != nil {
				if err := rt.Output.Print(resp.JSON200, nil); err != nil {
					return err
				}
			}
			fmt.Fprintf(rt.Stderr, "Cluster %s updated.\n", id)
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	cmd.Flags().StringArrayVar(&firewallRules, "firewall-rule", nil,
		"Firewall rule to append (repeatable). name must be one of "+
			"http, https, postgres, ssh. "+
			"e.g. name=https,port=443,sources=0.0.0.0/0")
	cmd.Flags().StringSliceVar(&backupStoreIDs, "backup-store-id", nil,
		"Backup store ID to attach (repeatable)")
	cmd.Flags().StringSliceVar(&regions, "regions", nil,
		"Replace the cluster's regions (refused if it would strand a "+
			"node or network)")
	addWaitFlags(cmd)
	_ = cmd.RegisterFlagCompletionFunc(
		"firewall-rule", completeFirewallRuleName)
	cli.MarkMutating(cmd)

	return cmd
}

// trimSpaces strips the whitespace pflag's CSV parser leaves behind.
// StringSliceVar uses csv.Reader with TrimLeadingSpace false, so
// `--regions "us-east-2, eu-west-1"` yields a second element of
// " eu-west-1". Untrimmed it is sent to the API verbatim, and it also
// makes the region guard report a region the user plainly did pass.
// Shared by --regions and every --target-nodes site (#386).
func trimSpaces(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// checkRegionsKeepTheirNodes refuses a --regions value that would drop
// a region the cluster still has a node or a network in.
//
// This verb sends regions, nodes and networks in one body and has no
// --nodes or --networks flag, so dropping a region moves nothing out
// of it: the body would contradict itself. Measured on a BYOC dev tenant
// 2026-08-22, clusters with a node in every region they declare exist
// and are `available`.
//
// What the API does with such a body is not known, and the refusal
// deliberately does not depend on it: the CLI can see the
// inconsistency in what it is about to send.
func checkRegionsKeepTheirNodes(
	rt *module.Runtime, c *api.Cluster, regions []string,
) error {
	if len(regions) == 0 {
		return nil
	}
	keep := make(map[string]bool, len(regions))
	for _, r := range regions {
		keep[r] = true
	}

	var orphanedNodes []string
	if c.Nodes != nil {
		for _, n := range *c.Nodes {
			if keep[n.Region] {
				continue
			}
			orphanedNodes = append(orphanedNodes, describeNode(n))
		}
	}
	// Deduplicated: two networks in one stranded region named that
	// region twice, which reads as two separate problems.
	var orphanedNetworks []string
	seen := map[string]bool{}
	if c.Networks != nil {
		for _, nw := range *c.Networks {
			if keep[nw.Region] || seen[nw.Region] {
				continue
			}
			seen[nw.Region] = true
			orphanedNetworks = append(orphanedNetworks, nw.Region)
		}
	}

	if len(orphanedNodes) == 0 && len(orphanedNetworks) == 0 {
		rt.DryRun.Pass("--regions leaves no node or network stranded")
		return nil
	}

	var b strings.Builder
	b.WriteString("--regions would drop a region this cluster still " +
		"uses, and cluster update cannot move or remove what is in it")
	if len(orphanedNodes) > 0 {
		fmt.Fprintf(&b, "; nodes: %s",
			output.Sanitize(strings.Join(orphanedNodes, ", ")))
	}
	if len(orphanedNetworks) > 0 {
		fmt.Fprintf(&b, "; networks in: %s",
			output.Sanitize(strings.Join(orphanedNetworks, ", ")))
	}
	// No "remove the nodes first": byoc exposes no node-removing
	// endpoint, and the only way to change cluster membership is the
	// very PATCH this refuses to build, so that advice would send the
	// operator nowhere.
	b.WriteString(". Pass every region the cluster needs")
	return &cli.UsageError{Msg: b.String()}
}

// describeNode names a node for the refusal above. Name is OPTIONAL on
// ClusterNodeSettings, and region is required but arrives empty if the
// server breaks its own contract, so neither can be interpolated bare:
// "eu-west-1" alone reads as a node called eu-west-1, and "n1 ()"
// tells the operator nothing.
func describeNode(n api.ClusterNodeSettings) string {
	region := n.Region
	if region == "" {
		region = "no region"
	}
	if n.Name == nil || *n.Name == "" {
		return fmt.Sprintf("an unnamed node in %s", region)
	}
	return fmt.Sprintf("%s (%s)", *n.Name, region)
}

// buildClusterUpdate produces an UpdateClusterInput from the cluster's
// current state, then layers on requested changes. Firewall rules and
// backup store IDs are appended to existing values; regions replace
// only when supplied. Fresh slices are built to avoid aliasing the
// cluster's own slices.
func buildClusterUpdate(c *api.Cluster,
	addRules []api.ClusterFirewallRuleSettings,
	addStoreIDs, regions []string) api.UpdateClusterInput {
	in := api.UpdateClusterInput{
		Regions:         c.Regions,
		Networks:        c.Networks,
		Nodes:           c.Nodes,
		ResourceTags:    c.ResourceTags,
		VpcAssociations: c.VpcAssociations,
	}

	rules := []api.ClusterFirewallRuleSettings{}
	if c.FirewallRules != nil {
		rules = append(rules, *c.FirewallRules...)
	}
	rules = append(rules, addRules...)
	if len(rules) > 0 {
		in.FirewallRules = &rules
	}

	stores := []string{}
	if c.BackupStoreIds != nil {
		stores = append(stores, *c.BackupStoreIds...)
	}
	stores = append(stores, addStoreIDs...)
	if len(stores) > 0 {
		in.BackupStoreIds = &stores
	}

	if len(regions) > 0 {
		in.Regions = regions
	}
	return in
}

// --- structured-flag parsing ---
//
// Every rejection from parseFirewallRule, parseClusterNetwork and
// parseClusterNode carries ExitUsage (2), not the ExitGeneral (1) a
// bare fmt.Errorf would produce.
//
// A malformed structured-flag value is precisely what llms.txt's exit
// table calls "the command was malformed": the user typed the flag
// wrong and nothing was sent, so a script must be able to tell it from
// an API or network failure by the code alone. The three parsers had
// drifted to ExitGeneral while validatePrivateSubnets — a sibling
// check on the same flag, added by #131 — already returned ExitUsage,
// so `--network garbage` and `--network region=x,public-subnets=y`
// under `--node-location private` reported the same class of mistake
// with two different codes (#134).
//
// The alignment covers all three parsers rather than only --network,
// which #134 named: fixing one leaves the same inconsistency between
// siblings on the same command, one flag further along.

// parseFirewallRule parses a repeatable structured flag value of the
// form "name=https,port=443,sources=0.0.0.0/0" into a
// ClusterFirewallRuleSettings. Pairs are comma-separated; list-valued
// keys (sources, prefix-lists, security-groups) are repeated to add
// elements. port is required.
func parseFirewallRule(s string) (api.ClusterFirewallRuleSettings, error) {
	var rule api.ClusterFirewallRuleSettings
	portSet := false
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return rule, newExitError(fmt.Sprintf(
				"firewall-rule: %q is not key=value", pair), ExitUsage)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "name":
			name := api.ClusterFirewallRuleSettingsName(v)
			rule.Name = &name
		case "port":
			p, err := strconv.Atoi(v)
			if err != nil {
				return rule, newExitError(fmt.Sprintf(
					"firewall-rule: port %q is not an integer", v),
					ExitUsage)
			}
			rule.Port = p
			portSet = true
		case "sources":
			rule.Sources = appendStrPtr(rule.Sources, v)
		case "prefix-lists":
			rule.PrefixLists = appendStrPtr(rule.PrefixLists, v)
		case "security-groups":
			rule.SecurityGroups = appendStrPtr(rule.SecurityGroups, v)
		default:
			return rule, newExitError(fmt.Sprintf(
				"firewall-rule: unknown key %q (valid: name, port, "+
					"sources, prefix-lists, security-groups)", k),
				ExitUsage)
		}
	}
	if !portSet {
		return rule, newExitError(
			"firewall-rule: port is required", ExitUsage)
	}
	name := ""
	if rule.Name != nil {
		name = string(*rule.Name)
	}
	switch {
	case name == "":
		return rule, newExitError(fmt.Sprintf(
			"firewall-rule: name is required (one of: %s)",
			validFirewallRuleNames), ExitUsage)
	case !validFirewallRuleName(name):
		return rule, newExitError(fmt.Sprintf(
			"firewall-rule: name %q is not a valid rule type (one of: %s)",
			name, validFirewallRuleNames), ExitUsage)
	}
	return rule, nil
}

// validFirewallRuleNames is the set of rule names the BYOC API accepts for a
// firewall rule (saas internal/starfleet/clusters/cluster_validator.go). The
// OpenAPI spec types this field as a free-form string with no enum, so the CLI
// hardcodes the set to give a clear client-side error instead of an opaque API
// 400. Keep in sync with the server; tracked by CLOUD spec bug.
const validFirewallRuleNames = "http, https, postgres, ssh"

func validFirewallRuleName(name string) bool {
	switch name {
	case "postgres", "https", "http", "ssh":
		return true
	default:
		return false
	}
}

// defaultRegionFor returns the region to assume when a structured flag
// omits region=: the sole --regions value, or "" when the cluster spans
// multiple regions and the key must be explicit.
func defaultRegionFor(regions []string) string {
	if len(regions) == 1 {
		return regions[0]
	}
	return ""
}

// parseClusterNetwork parses a repeatable --network flag value of the
// form "region=us-east-1,cidr=10.4.0.0/16,public-subnets=10.4.1.0/24"
// into ClusterNetworkSettings. List-valued keys (public-subnets,
// private-subnets, subnets) are repeated to add elements. region may be
// omitted only on single-region clusters.
//
// Every key ClusterNetworkSettings declares is accepted, and that
// completeness is the point rather than a convenience (#134). The flag
// used to parse region, cidr, public-subnets and private-subnets only,
// which left a GCP network spec inexpressible: saas's Google validator
// rejects BOTH public_subnets and private_subnets outright ("use
// subnets instead", cluster_validator_google.go), so the only GCP path
// through this CLI was to omit --network entirely and accept whatever
// defaults the server filled in. subnets is what closes that.
//
// external / external-id / name describe a pre-existing customer VPC
// the cluster attaches to rather than one pgEdge creates. They are
// passed through without interpretation: which combinations are
// meaningful is per-cloud server-side knowledge (an external network
// needs its external_id, a fresh one must not carry one), and the CLI
// duplicating that judgement would go stale against saas the first time
// a starfleet's rules moved. The keys existing at all is what this flag
// owes the user; validating them is the API's job.
func parseClusterNetwork(s, defaultRegion string) (
	api.ClusterNetworkSettings, error) {
	var n api.ClusterNetworkSettings
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return n, newExitError(fmt.Sprintf(
				"network: %q is not key=value", pair), ExitUsage)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "region":
			n.Region = v
		case "cidr":
			cidr := v
			n.Cidr = &cidr
		case "public-subnets":
			n.PublicSubnets = appendStrPtr(n.PublicSubnets, v)
		case "private-subnets":
			n.PrivateSubnets = appendStrPtr(n.PrivateSubnets, v)
		case "subnets":
			n.Subnets = appendStrPtr(n.Subnets, v)
		case "external":
			// strconv.ParseBool, not a v == "true" test: it is what the
			// rest of this CLI parses booleans with, so "1"/"t"/"TRUE"
			// behave here exactly as they do for a --flag=value bool.
			// A bare "external" with no value never reaches this arm —
			// strings.Cut above rejects it as "not key=value" — so the
			// key always carries an explicit choice.
			b, err := strconv.ParseBool(v)
			if err != nil {
				return n, newExitError(fmt.Sprintf(
					"network: external %q is not a boolean "+
						"(use external=true or external=false)", v),
					ExitUsage)
			}
			n.External = &b
		case "external-id":
			id := v
			n.ExternalId = &id
		case "name":
			name := v
			n.Name = &name
		default:
			return n, newExitError(fmt.Sprintf(
				"network: unknown key %q (valid: region, cidr, "+
					"public-subnets, private-subnets, subnets, external, "+
					"external-id, name)", k), ExitUsage)
		}
	}
	if n.Region == "" {
		n.Region = defaultRegion
	}
	if n.Region == "" {
		return n, newExitError(
			"network: region is required on multi-region clusters",
			ExitUsage)
	}
	return n, nil
}

// parseClusterNode parses a repeatable --node flag value of the form
// "name=n1,region=us-east-1,instance-type=r7g.medium,volume-size=30"
// into ClusterNodeSettings. region may be omitted only on single-region
// clusters.
func parseClusterNode(s, defaultRegion string) (
	api.ClusterNodeSettings, error) {
	var n api.ClusterNodeSettings
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return n, newExitError(fmt.Sprintf(
				"node: %q is not key=value", pair), ExitUsage)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "name":
			name := v
			n.Name = &name
		case "region":
			n.Region = v
		case "instance-type":
			it := v
			n.InstanceType = &it
		case "volume-size":
			size, err := strconv.Atoi(v)
			if err != nil {
				return n, newExitError(fmt.Sprintf(
					"node: volume-size %q is not an integer", v),
					ExitUsage)
			}
			// The same floor --volume-size carries. One spelling of a
			// field must not accept what the other refuses, and this is
			// the spelling every worked example in llms.txt uses (#282).
			if size < volumeSizeMin {
				return n, newExitError(fmt.Sprintf(
					"node: volume-size %d: expected %d or more GB "+
						"(omit the key to let the API choose)",
					size, volumeSizeMin), ExitUsage)
			}
			n.VolumeSize = &size
		case "volume-iops":
			iops, err := strconv.Atoi(v)
			if err != nil {
				return n, newExitError(fmt.Sprintf(
					"node: volume-iops %q is not an integer", v),
					ExitUsage)
			}
			n.VolumeIops = &iops
		case "volume-type":
			vt := v
			n.VolumeType = &vt
		case "availability-zone":
			az := v
			n.AvailabilityZone = &az
		default:
			return n, newExitError(fmt.Sprintf(
				"node: unknown key %q (valid: name, region, "+
					"instance-type, volume-size, volume-iops, "+
					"volume-type, availability-zone)", k), ExitUsage)
		}
	}
	if n.Region == "" {
		n.Region = defaultRegion
	}
	if n.Region == "" {
		return n, newExitError(
			"node: region is required on multi-region clusters",
			ExitUsage)
	}
	return n, nil
}

// validatePrivateSubnets rejects a --node-location private cluster
// whose parsed --network settings are unambiguously AWS/Azure-shaped
// (they carry public-subnets) but omit private-subnets, with exit
// code ExitUsage, before any API call.
//
// skills/pgedge-byoc/SKILL.md documents the dependency directly:
// "Private clusters on AWS/Azure: add private-subnets=... to
// --network." The cloud qualifier in that sentence is part of the
// quote, not a gloss — GCP is governed by the next line of the same
// block, and an unqualified version of this rule is exactly what #134
// had to delete. Without this check the CLI sends the request anyway
// and the server 400s with "no private subnet for availability zone"
// — a round trip for a rule the CLI already knows, for AWS and Azure.
//
// This deliberately does NOT reject a private cluster with zero
// --network entries, or a --network entry that carries neither
// public-subnets nor private-subnets: that is a valid GCP shape. GCP
// clusters use --network's subnets key instead — the server rejects
// private_subnets there outright, "use subnets instead" — so a GCP
// private cluster must be able to pass through this check untouched.
//
// The subnets key exists as of #134, so a GCP private cluster is now
// expressible rather than merely tolerated here. That does NOT make
// this check able to demand subnets on a private cluster: --network is
// optional, the server fills defaults when it is omitted, and nothing
// in the flag values says which cloud the cloud-account-id belongs to.
// Rejecting a private cluster for missing subnets would therefore
// break the valid omit-and-default path for every cloud.
func validatePrivateSubnets(
	nodeLocation string, networks []api.ClusterNetworkSettings,
) error {
	if nodeLocation != "private" {
		return nil
	}
	for _, n := range networks {
		hasPublic := n.PublicSubnets != nil && len(*n.PublicSubnets) > 0
		hasPrivate := n.PrivateSubnets != nil && len(*n.PrivateSubnets) > 0
		if !hasPublic || hasPrivate {
			// Not AWS/Azure-shaped (no public-subnets — could be a GCP
			// entry using subnets instead), or already carries
			// private-subnets. Either way, nothing to reject.
			continue
		}
		region := n.Region
		if region == "" {
			region = "(unspecified)"
		}
		// The GCP clause is not a nicety. This branch fires on an entry
		// carrying public-subnets, which a GCP user can reach — saas
		// rejects public_subnets on a Google cluster too — and the
		// AWS/Azure remedy is one saas would refuse a second time. The
		// CLI cannot tell which cloud is meant (nothing in the flag
		// values names it, and the cloud-account-id is opaque here), so
		// it names both remedies rather than guessing.
		// "the subnet keys", not "both": this branch only fires when
		// public-subnets is set and private-subnets is not, so exactly
		// one key is present at the point the message prints.
		return newExitError(fmt.Sprintf(
			"--node-location private requires private-subnets in "+
				"--network, but the network for region %q sets "+
				"public-subnets and no private-subnets; add "+
				"private-subnets=<cidr> to that --network value — or, "+
				"on GCP, replace the subnet keys with subnets=<cidr>",
			region), ExitUsage)
	}
	return nil
}

// buildCreateNetworks parses all --network values, defaulting region on
// single-region clusters.
func buildCreateNetworks(raw, regions []string) (
	[]api.ClusterNetworkSettings, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	defaultRegion := defaultRegionFor(regions)
	networks := make([]api.ClusterNetworkSettings, 0, len(raw))
	for _, s := range raw {
		n, err := parseClusterNetwork(s, defaultRegion)
		if err != nil {
			return nil, err
		}
		networks = append(networks, n)
	}
	return networks, nil
}

// nodeLocations are the values byoc.yaml declares for node_location,
// sorted so the "expected one of" message reads the same every run.
//
// The enum was declared and not enforced: `--node-location sideways`
// reached the request body unparsed, so a dry run passed it and only
// the API refused it -- on the most expensive verb in the CLI, right
// before it provisions real infrastructure (#256). The flag's own help
// text and its shell completion both already named the two values.
//
// TestNodeLocationsMatchTheSpecEnum reads byoc.yaml and is what fails
// if the contract grows a third: the generated enum type carries no
// way to enumerate its members, so a Valid()-only check could not
// notice, and the CLI would go on refusing a value the API accepts.
var nodeLocations = []string{"private", "public"}

// validateNodeLocation refuses a --node-location outside the contract
// with ExitUsage, and records the pass in the dry-run ledger.
//
// The empty value is named separately. --node-location is a required
// flag, but cobra's MarkFlagRequired tests only whether the flag was
// given, so `--node-location "$LOC"` with the variable unset satisfies
// it -- and "unknown node location \"\"" would send the reader looking
// for a location called nothing.
func validateNodeLocation(rt *module.Runtime, v string) error {
	if strings.TrimSpace(v) == "" {
		return newExitError(fmt.Sprintf(
			"--node-location given an empty value: name a location "+
				"(%s)", strings.Join(nodeLocations, " or ")),
			ExitUsage)
	}
	if !slices.Contains(nodeLocations, v) {
		return newExitError(fmt.Sprintf(
			"unknown node location %q (expected one of: %s)",
			v, strings.Join(nodeLocations, ", ")), ExitUsage)
	}
	rt.DryRun.Pass("node location %q accepted", v)
	return nil
}

// checkCloudAccountExists confirms --cloud-account-id names a real
// cloud account, and records the pass in the dry-run ledger.
//
// The 404 is passed through rather than reworded: checkResponse maps
// it to the unknown-resource code and the API's own message already
// says "cloud account not found". Rebuilding the error here could only
// lose the code it arrives with.
func checkCloudAccountExists(
	ctx context.Context, rt *module.Runtime,
	client *api.ClientWithResponses, id string,
) error {
	parsed, err := parseUUIDArg(id, "cloud account ID")
	if err != nil {
		return err
	}
	resp, err := client.GetCloudAccountWithResponse(ctx, parsed)
	if err != nil {
		return fmt.Errorf("get cloud account: %w", err)
	}
	if err := checkResponse(resp.StatusCode(),
		string(resp.Body)); err != nil {
		return err
	}
	rt.DryRun.Pass("cloud account %s exists", id)
	return nil
}

// volumeSizeMin is the smallest --volume-size the CLI will send.
//
// The bound is needed because nothing else applies one. byoc.yaml
// declares volume_size as a plain optional integer with no minimum and
// no default, and the API accepts a negative: it substitutes its own
// undocumented default of 100 GB and answers 200, so `--volume-size -5`
// provisioned a 100 GB volume at exit 0 (#256).
//
// Zero is refused along with the negatives rather than read as "let the
// API choose". Omitting the flag already says that unambiguously, and a
// second spelling for it would put the CLI back to answering a request
// for 0 GB with 100 GB — the substitution this bound exists to stop.
//
// There is no upper bound to mirror: the spec declares none, and one
// here would leave the CLI refusing sizes the API accepts.
const volumeSizeMin = 1

// buildCreateNodes turns --node values (or the --instance-type /
// --volume-size shorthand) into node settings. The shorthand creates
// one node per region, named n1, n2, ... in region order — the naming
// convention the platform uses for node targeting (e.g.
// `databases mcp deploy --target-nodes n1`).
func buildCreateNodes(raw []string, instanceType string, volumeSize int,
	volumeSizeSet bool, regions []string,
) ([]api.ClusterNodeSettings, error) {
	if volumeSizeSet && volumeSize < volumeSizeMin {
		// ExitUsage, not ExitGeneral: a bad flag value, with nothing
		// sent — the same contract the structured-flag rejections above
		// carry.
		return nil, newExitError(fmt.Sprintf(
			"invalid --volume-size value %d: expected %d or more GB "+
				"(omit the flag to let the API choose)",
			volumeSize, volumeSizeMin), ExitUsage)
	}
	// volumeSizeSet, never volumeSize > 0, is the test from here on: the
	// comparison cannot distinguish "not given" from "given as 0".
	shorthand := instanceType != "" || volumeSizeSet
	if len(raw) > 0 && shorthand {
		// ExitUsage for the same reason parseClusterNode's rejections
		// carry it (see the structured-flag parsing section): two
		// mutually exclusive ways to describe the same nodes were both
		// given, nothing was sent, and a script must not read that as an
		// API failure. Leaving this one at ExitGeneral would put the
		// --node flag's own two rejection paths on different codes.
		return nil, newExitError(
			"use either --node or --instance-type/--volume-size, not both",
			ExitUsage)
	}

	if len(raw) > 0 {
		defaultRegion := defaultRegionFor(regions)
		nodes := make([]api.ClusterNodeSettings, 0, len(raw))
		for _, s := range raw {
			n, err := parseClusterNode(s, defaultRegion)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		}
		return nodes, nil
	}

	if !shorthand {
		return nil, nil
	}
	nodes := make([]api.ClusterNodeSettings, 0, len(regions))
	for i, region := range regions {
		name := fmt.Sprintf("n%d", i+1)
		n := api.ClusterNodeSettings{Name: &name, Region: region}
		if instanceType != "" {
			it := instanceType
			n.InstanceType = &it
		}
		if volumeSizeSet {
			size := volumeSize
			n.VolumeSize = &size
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// backupStoreWarning returns a caution when a cluster is being created with no
// backup store. A storeless cluster provisions fine but cannot host a database
// (database create fails: "at least 1 repository must be defined for provider:
// pgbackrest"), so it is a dead-end until a store is attached. Returns "" when
// at least one store is given. The CLI only warns — the API stays permissive so
// the create-then-attach flow (cluster update --backup-store-id) remains valid.
func backupStoreWarning(storeIDs []string) string {
	if len(storeIDs) > 0 {
		return ""
	}
	return "warning: cluster has no backup store; it cannot host a " +
		"database until one is attached " +
		"(--backup-store-id or `cluster update`)"
}

// appendStrPtr appends v to the slice behind p, allocating if p is nil.
func appendStrPtr(p *[]string, v string) *[]string {
	if p == nil {
		return &[]string{v}
	}
	*p = append(*p, v)
	return p
}

// --- row adapter ---

type clusterRow struct {
	id, name, status, regions, created string
}

func (r clusterRow) Columns() []string {
	return []string{
		r.id, r.name, output.ColorStatus(r.status), r.regions, r.created,
	}
}
