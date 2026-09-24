package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/spf13/cobra"
)

// ingressColumns are the table headers shared by ingress list and get.
var ingressColumns = []string{
	"ID", "NAME", "STATUS", "CLUSTER ID", "REGION", "DOMAIN", "CREATED",
}

// NewIngressCmd builds the `pgedge starfleet byoc ingress` command
// group.
func NewIngressCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ingress",
		Aliases: []string{"ingresses"},
		Short:   "Manage pgEdge BYOC ingresses",
		Long: `ingress manages the ingresses that expose a cluster's
databases to the network.

Use these commands to list, inspect, create, and delete ingresses,
and to manage the services registered on each one.

Example:
  pgedge starfleet byoc ingress list
  pgedge starfleet byoc ingress create --name web --cluster-id <id> \
    --region us-east-1`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newIngressListCmd(rt),
		newIngressGetCmd(rt),
		newIngressCreateCmd(rt),
		newIngressDeleteCmd(rt),
		NewIngressServiceCmd(rt),
	)
	return cmd
}

// --- list ---

func newIngressListCmd(rt *module.Runtime) *cobra.Command {
	var (
		limit         int
		offset        int
		createdAfter  string
		createdBefore string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List ingresses",
		Long: `list shows the ingresses on the active account.

Use it to find an ingress's ID before running get, delete, or a
service command. Page through large accounts with --limit and
--offset, and filter by creation time with --created-after and
--created-before.

Example:
  pgedge starfleet byoc ingress list
  pgedge starfleet byoc ingress list --limit 20 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Before the client, so a bad value exits 2, not 5 for
			// credentials it never needed. NoUpperBound because byoc.yaml
			// declares no paging bounds: the server clamps at 100 today,
			// but a measured clamp is not a published contract.
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
			params := &api.ListIngressesParams{}
			if sendLimit {
				params.Limit = &limit
			}
			if sendOffset {
				params.Offset = &offset
			}
			if err := conn.ApplyCreatedRange(cmd.Flags(),
				&params.CreatedAfter, &params.CreatedBefore); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListIngressesWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list ingresses: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			ingresses := resp.JSON200
			if ingresses == nil || len(*ingresses) == 0 {
				fmt.Fprintln(rt.Stderr, "No ingresses found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*ingresses))
			for _, ing := range *ingresses {
				rows = append(rows, ingressRowFrom(ing))
			}
			if err := rt.Output.Print(rows, ingressColumns); err != nil {
				return err
			}
			cli.PrintTruncationHint(rt, len(*ingresses), limit,
				ingressDefaults, "results")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0,
		cli.LimitFlagHelp(ingressDefaults))
	f.IntVar(&offset, "offset", 0,
		"Offset into the results for pagination")
	f.StringVar(&createdAfter, "created-after", "",
		"Filter: created after this RFC3339 timestamp")
	f.StringVar(&createdBefore, "created-before", "",
		"Filter: created before this RFC3339 timestamp")
	return cmd
}

// --- get ---

func newIngressGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ingress_id>",
		Short: "Show ingress details",
		Long: `get shows the details of a single ingress.

Use it to read an ingress's status, cluster, region, and domain.
The argument is the ingress's UUID.

Example:
  pgedge starfleet byoc ingress get b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc ingress get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "ingress ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetIngressWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get ingress: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			ing := resp.JSON200
			if ing == nil {
				fmt.Fprintln(rt.Stderr, "No ingress data returned.")
				return nil
			}
			rows := []output.Row{ingressRowFrom(*ing)}
			return rt.Output.Print(rows, ingressColumns)
		},
	}
}

// --- create ---

func newIngressCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name      string
		clusterID string
		region    string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an ingress",
		Long: `create provisions a new ingress for a cluster.

Use it to expose a cluster's databases to the network. Pass --wait
to block until provisioning finishes.

Example:
  pgedge starfleet byoc ingress create --name web --cluster-id <id> \
    --region us-east-1
  pgedge starfleet byoc ingress create --name web --cluster-id <id> \
    --region us-east-1 --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// cluster_id is a bare string in the contract, so an
			// unchecked ID prefix fails naming the cluster, not the ID.
			cluster, err := parseUUIDArg(clusterID, "cluster ID")
			if err != nil {
				return err
			}
			// Recorded so a dry run does not report "no client-side
			// checks" for this verb. Most parseUUIDArg sites record
			// nothing; uneven coverage is a smaller fault than a false
			// report.
			rt.DryRun.Pass("cluster ID %s well-formed", cluster)
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateIngressJSONRequestBody{
				Name:      name,
				ClusterId: cluster.String(),
				Region:    region,
			}

			resp, err := client.CreateIngressWithResponse(
				context.Background(), body)
			if err != nil {
				return fmt.Errorf("create ingress: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			ing := resp.JSON200
			if ing == nil {
				// No body means no id, so nothing to track.
				fmt.Fprintln(rt.Stderr,
					"Ingress created (no details returned).")
				return nil
			}

			if rt.Output.Structured() {
				if err := rt.Output.Print(ing, nil); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(rt.Stderr,
					"Ingress %q created (id: %s, status: %s).\n",
					ing.Name, output.Sanitize(ing.Id), output.Sanitize(ing.Status))
			}
			return trackMutation(rt, client, ing.Id, "")
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Ingress name")
	f.StringVar(&clusterID, "cluster-id", "",
		"Cluster to associate with the ingress (full UUID)")
	f.StringVar(&region, "region", "", "Cloud region for the ingress")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("cluster-id")
	_ = cmd.MarkFlagRequired("region")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newIngressDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <ingress_id>",
		Short: "Delete an ingress",
		Long: `delete tears down an ingress.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The argument is the ingress's UUID. Pass --wait
to block until teardown finishes.

Example:
  pgedge starfleet byoc ingress delete b0c1d2e3-f4a5-6789-bcde-890123456789
  pgedge starfleet byoc ingress delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
    --force --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUUIDArg(args[0], "ingress ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete ingress %s? This cannot be undone.", id)
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

			resp, err := client.DeleteIngress(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("delete ingress: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete ingress"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Ingress %s deleted.\n", id)
			return trackMutation(rt, client, id.String(), priorTaskID)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	addWaitFlags(cmd)
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type ingressRow struct {
	id, name, status, clusterID, region, domain, created string
}

func (r ingressRow) Columns() []string {
	return []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
		r.clusterID,
		r.region,
		r.domain,
		r.created,
	}
}

// ingressRowFrom adapts an api.Ingress into a table row.
func ingressRowFrom(ing api.Ingress) ingressRow {
	return ingressRow{
		id:        ing.Id,
		name:      ing.Name,
		status:    ing.Status,
		clusterID: ing.ClusterId,
		region:    output.DerefString(ing.Region),
		domain:    output.DerefString(ing.Domain),
		created:   output.FormatTime(ing.CreatedAt),
	}
}
