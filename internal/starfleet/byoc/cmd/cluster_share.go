package cmd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/spf13/cobra"
)

// clusterShareColumns are the table headers shared by share list and
// get.
var clusterShareColumns = []string{
	"ID", "NAME", "STATUS", "TENANCY", "CAPACITY", "CREATED AT",
}

// NewClusterShareCmd builds the `pgedge starfleet byoc cluster share` command
// group, which manages the shares carved out of a cluster.
func NewClusterShareCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "share",
		Aliases: []string{"shares"},
		Short:   "Manage cluster shares",
		Long: `share manages the shares of a cluster: slices of cluster
capacity that can be handed to other tenants.

Use these commands to list, inspect, create, and delete the shares
of a given cluster.

Example:
  pgedge starfleet byoc cluster share list <cluster_id>
  pgedge starfleet byoc cluster share get <cluster_id> <share_id>`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newClusterShareListCmd(rt),
		newClusterShareGetCmd(rt),
		newClusterShareCreateCmd(rt),
		newClusterShareDeleteCmd(rt),
	)
	return cmd
}

// parseUUIDArg parses arg as a UUID the caller typed, naming the
// resource kind in the message (e.g. "cluster ID").
//
// It delegates to cli.ParseUUIDArg so every module reaches exit 2 for
// a malformed argument through one function. It used to return
// ExitGeneral, which made byoc exit 1 where cp exited 2 for the same
// mistake.
func parseUUIDArg(arg, kind string) (uuid.UUID, error) {
	return cli.ParseUUIDArg(arg, kind)
}

// --- list ---

func newClusterShareListCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list <cluster_id>",
		Short: "List shares for a cluster",
		Long: `list shows the shares that belong to a cluster.

Use it to find a share's ID before running get or delete. The
argument is the cluster's UUID.

Example:
  pgedge starfleet byoc cluster share list 3fa85f64-5717-4562-b3fc-2c963f66afa6`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ListClusterSharesWithResponse(
				context.Background(), clusterID)
			if err != nil {
				return fmt.Errorf("list cluster shares: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			shares := resp.JSON200
			if shares == nil || len(*shares) == 0 {
				fmt.Fprintln(rt.Stderr, "No shares found.")
				return nil
			}

			rows := make([]output.Row, 0, len(*shares))
			for _, s := range *shares {
				rows = append(rows, shareRowFrom(s))
			}
			return rt.Output.Print(rows, clusterShareColumns)
		},
	}
}

// --- get ---

func newClusterShareGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster_id> <share_id>",
		Short: "Show cluster share details",
		Long: `get shows the details of a single cluster share.

Use it to check a share's status, tenancy, and capacity. The
arguments are the cluster's UUID and the share's UUID.

Example:
  pgedge starfleet byoc cluster share get <cluster_id> <share_id> -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}
			shareID, err := parseUUIDArg(args[1], "share ID")
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.ReadClusterShareWithResponse(
				context.Background(), clusterID, shareID)
			if err != nil {
				return fmt.Errorf("get cluster share: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			s := resp.JSON200
			if s == nil {
				fmt.Fprintln(rt.Stderr, "No share data returned.")
				return nil
			}
			rows := []output.Row{shareRowFrom(*s)}
			return rt.Output.Print(rows, clusterShareColumns)
		},
	}
}

// --- create ---

func newClusterShareCreateCmd(rt *module.Runtime) *cobra.Command {
	var (
		name           string
		capacity       int
		tenancy        string
		allowedTenants []string
	)
	cmd := &cobra.Command{
		Use:   "create <cluster_id>",
		Short: "Create a cluster share",
		Long: `create carves a new share out of a cluster.

Use it to allocate capacity for another tenant. Set --tenancy to
"same" or "allowlist"; with allowlist, list the permitted tenants
via --allowed-tenants. The argument is the cluster's UUID.

Example:
  pgedge starfleet byoc cluster share create <cluster_id> \
    --name team-a --capacity 2 --tenancy allowlist \
    --allowed-tenants tnt-1,tnt-2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}

			// --capacity refuses a negative as every numeric flag in
			// this module does: --volume-size refuses at exit 2, the
			// structured --node volume-size= refuses with it, and
			// --limit and --offset go through the same helper at all
			// 21 sites. shareCapacityMin stays its
			// own constant because a share's capacity is not a page
			// size and shares no bound with one.
			capacity, sendCapacity, err := cli.OptionalIntFlag(
				cmd.Flags(), "capacity", shareCapacityMin)
			if err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			body := api.CreateClusterShareJSONRequestBody{}
			if name != "" {
				body.Name = &name
			}
			if sendCapacity {
				body.Capacity = &capacity
			}
			if tenancy != "" {
				t := api.CreateClusterShareInputTenancy(tenancy)
				body.Tenancy = &t
			}
			if len(allowedTenants) > 0 {
				body.AllowedTenants = &allowedTenants
			}

			resp, err := client.CreateClusterShareWithResponse(
				context.Background(), clusterID, body)
			if err != nil {
				return fmt.Errorf("create cluster share: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			s := resp.JSON200
			if s == nil {
				fmt.Fprintln(rt.Stderr,
					"Share created (no details returned).")
				return nil
			}
			fmt.Fprintf(rt.Stderr,
				"Share %q created (id: %s, status: %s).\n",
				s.Name, output.Sanitize(s.Id), output.Sanitize(s.Status))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Share name")
	cmd.Flags().IntVar(&capacity, "capacity", 0,
		fmt.Sprintf("Share capacity, %d or more (omit to let the API "+
			"choose)", shareCapacityMin))
	cmd.Flags().StringVar(&tenancy, "tenancy", "",
		"Tenancy mode: same or allowlist")
	cmd.Flags().StringSliceVar(&allowedTenants, "allowed-tenants", nil,
		"Allowed tenant IDs (for allowlist tenancy)")
	cli.MarkMutating(cmd)

	return cmd
}

// --- delete ---

func newClusterShareDeleteCmd(rt *module.Runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <cluster_id> <share_id>",
		Short: "Delete a cluster share",
		Long: `delete removes a share from a cluster.

Deletion is destructive, so it prompts for confirmation unless
--force is given. The arguments are the cluster's UUID and the
share's UUID.

Example:
  pgedge starfleet byoc cluster share delete <cluster_id> <share_id> --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := parseUUIDArg(args[0], "cluster ID")
			if err != nil {
				return err
			}
			shareID, err := parseUUIDArg(args[1], "share ID")
			if err != nil {
				return err
			}

			prompt := fmt.Sprintf(
				"Delete share %s? This cannot be undone.", shareID)
			if err := cli.Confirm(rt, prompt, force); err != nil {
				return err
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.DeleteClusterShare(
				context.Background(), clusterID, shareID)
			if err != nil {
				return fmt.Errorf("delete cluster share: %w", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "delete cluster share"); err != nil {
				return err
			}

			fmt.Fprintf(rt.Stderr, "Share %s deleted.\n", shareID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the confirmation prompt")
	cli.MarkMutating(cmd)

	return cmd
}

// --- row adapter ---

type clusterShareRow struct {
	id, name, status, tenancy, capacity, created string
}

func (r clusterShareRow) Columns() []string {
	return []string{
		r.id,
		r.name,
		output.ColorStatus(r.status),
		r.tenancy,
		r.capacity,
		r.created,
	}
}

// shareRowFrom adapts an api.ClusterShare into a table row.
func shareRowFrom(s api.ClusterShare) clusterShareRow {
	return clusterShareRow{
		id:       s.Id,
		name:     s.Name,
		status:   s.Status,
		tenancy:  s.Tenancy,
		capacity: fmt.Sprintf("%d", s.Capacity),
		created:  output.FormatTime(s.CreatedAt),
	}
}

// shareCapacityMin is the smallest --capacity the CLI will send.
//
// The byoc spec declares capacity as a bare integer with no minimum,
// so this is the CLI's floor rather than a mirrored one: a share of
// zero capacity allocates nothing and a negative is meaningless. It
// matches volumeSizeMin's reasoning and, like it, sets no upper bound
// — one here would refuse capacities the API accepts.
const shareCapacityMin = 1
