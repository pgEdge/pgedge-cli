package cmd

import (
	"context"
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

var clusterColumns = []string{"ID", "STATE", "HOSTS"}
var tokenColumns = []string{"TOKEN", "SERVER-URLS"}

// NewClusterCmd builds `pgedge controlplane cluster`.
func NewClusterCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster",
		Aliases: []string{"clusters"},
		Short:   "Manage the Control Plane cluster",
		Long: `cluster inspects and forms the control-plane cluster: its
hosts, initialization, and join tokens.

Example:
  pgedge controlplane cluster info
  pgedge controlplane cluster init`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		newClusterInfoCmd(rt),
		newClusterInitCmd(rt),
		newClusterJoinTokenCmd(rt),
		newClusterJoinCmd(rt),
	)
	return cmd
}

func newClusterInfoCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show cluster information",
		Long: `info shows the cluster id, state, and member hosts.

Example:
  pgedge controlplane cluster info
  pgedge controlplane cluster info -o yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetClusterWithResponse(
				context.Background())
			if err != nil {
				return networkError("get cluster", err)
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
				id:    c.Id,
				state: string(c.Status.State),
				hosts: fmt.Sprintf("%d", len(c.Hosts)),
			}}
			return rt.Output.Print(rows, clusterColumns)
		},
	}
}

type clusterRow struct{ id, state, hosts string }

func (r clusterRow) Columns() []string {
	return []string{r.id, output.ColorStatus(r.state), r.hosts}
}

func printJoinToken(
	rt *module.Runtime, tok *api.ClusterJoinToken,
) error {
	if rt.Output.Structured() {
		return rt.Output.Print(tok, nil)
	}
	if tok == nil {
		fmt.Fprintln(rt.Stderr, "No join token returned.")
		return nil
	}
	rows := []output.Row{tokenRow{
		token: tok.Token, urls: joinStrings(tok.ServerUrls),
	}}
	return rt.Output.Print(rows, tokenColumns)
}

type tokenRow struct{ token, urls string }

func (r tokenRow) Columns() []string { return []string{r.token, r.urls} }

func newClusterInitCmd(rt *module.Runtime) *cobra.Command {
	var clusterID string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new cluster",
		Long: `init initializes a new control-plane cluster on the
targeted server and prints a join token other hosts use to join.

Example:
  pgedge controlplane cluster init
  pgedge controlplane cluster init --cluster-id prod`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			params := &api.InitClusterParams{}
			if clusterID != "" {
				params.ClusterId = &clusterID
			}
			resp, err := client.InitClusterWithResponse(
				context.Background(), params)
			if err != nil {
				return networkError("init cluster", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			return printJoinToken(rt, resp.JSON200)
		},
	}
	cmd.Flags().StringVar(&clusterID, "cluster-id", "",
		"Optional cluster ID (default server-generated)")
	cli.MarkMutating(cmd)

	return cmd
}

func newClusterJoinTokenCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "join-token",
		Short: "Print the cluster join token",
		Long: `join-token prints the token and server URLs a new host
uses to join this cluster.

Example:
  pgedge controlplane cluster join-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetJoinTokenWithResponse(
				context.Background())
			if err != nil {
				return networkError("get join token", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}
			return printJoinToken(rt, resp.JSON200)
		},
	}
}

func newClusterJoinCmd(rt *module.Runtime) *cobra.Command {
	var token string
	var serverURLs []string
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Join this host to a cluster",
		Long: `join adds THIS host (the one --base-url points at, which
must be a new, uninitialized control-plane) to an existing cluster.
--token comes from 'cluster init' or 'cluster join-token' on an
existing member; --server-url (repeatable) lists existing cluster
members to contact.

Example:
  pgedge controlplane cluster join --base-url http://new-host:3000 \
    --token PGEDGE-abc --server-url http://existing-1:3000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if token == "" || len(serverURLs) == 0 {
				return &ExitError{
					msg: "--token and at least one --server-url " +
						"are required",
					code: ExitUsage,
				}
			}
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}
			body := api.JoinClusterJSONRequestBody{
				Token:      token,
				ServerUrls: serverURLs,
			}
			resp, err := client.JoinCluster(
				context.Background(), body)
			if err != nil {
				return networkError("join cluster", err)
			}
			if err := checkEmptyBodyResponse(
				resp, "join cluster"); err != nil {
				return err
			}
			fmt.Fprintf(rt.Stderr, "Join request accepted.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "",
		"Cluster join token (required)")
	cmd.Flags().StringArrayVar(&serverURLs, "server-url", nil,
		"Existing cluster member to contact (repeatable, "+
			"at least one required)")
	cli.MarkMutating(cmd)

	return cmd
}
