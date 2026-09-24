package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/spf13/cobra"
)

// instanceListColumns is this command's table. It names the owning
// database, and drops the ADDRESSES/PORT that `database get` carries:
// those are for acting on one instance, not surveying a fleet, and the
// table is already eight columns wide.
var instanceListColumns = []string{
	"DATABASE", "ID", "NODE", "HOST", "STATE", "ROLE", "PG", "SPOCK"}

// instanceListRow renders one instance under `database instance list`.
// It projects the same instanceFields that `database get` renders, so
// the ID a reader sees here is the ID they pass to the other verbs.
type instanceListRow struct{ f instanceFields }

func (r instanceListRow) Columns() []string {
	return []string{r.f.database, r.f.id, r.f.node, r.f.host,
		output.ColorStatus(r.f.state), r.f.role, r.f.pg, r.f.spock}
}

// dbInstance pairs one instance with the database that owns it. The
// pairing is the whole reason this command exists: the API models
// instances only as a field of a database, so nothing on the wire
// carries both.
type dbInstance struct {
	dbID string
	inst api.Instance
}

// instanceOutput is one entry of `instance list` under -o json/-o
// yaml. The embedded api.Instance promotes its fields, so an entry is
// the API instance plus the owning database's ID.
//
// This is a CLI view, not an API resource. The settled rule is "no
// fabricated API RESOURCE objects" — --dry-run already emits a
// manufactured object — and there is no endpoint returning instances
// across databases for this to be mistaken for.
//
// -o yaml needs no tags of its own: the renderer derives YAML from the
// JSON shape, which is what makes "-o yaml key = -o json key" true by
// construction rather than by convention.
type instanceOutput struct {
	DatabaseID string `json:"database_id"`
	api.Instance
}

// flattenInstances pairs every instance with its database, in a stable
// order (database ID, then node name, then instance ID). The Control
// Plane promises no order for either list.
func flattenInstances(dbs []api.DatabaseSummary) []dbInstance {
	out := make([]dbInstance, 0, len(dbs))
	for _, d := range dbs {
		if d.Instances == nil {
			continue
		}
		for _, inst := range *d.Instances {
			out = append(out, dbInstance{dbID: d.Id, inst: inst})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dbID != out[j].dbID {
			return out[i].dbID < out[j].dbID
		}
		if out[i].inst.NodeName != out[j].inst.NodeName {
			return out[i].inst.NodeName < out[j].inst.NodeName
		}
		// Instance IDs are unique by construction, so this third key
		// makes the ordering TOTAL. sort.Slice is not stable, and
		// (database, node) alone leaves two instances sharing both
		// keys free to swap between runs — unreachable on valid data,
		// since Spock node names are unique within a database, but a
		// guarantee that holds only while the data is well-formed is
		// not the guarantee this sort exists to make.
		return out[i].inst.Id < out[j].inst.Id
	})
	return out
}

func newInstanceListCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List database instances",
		Long: `list shows every instance across every database, or one
database's instances with --database.

This is where the other instance verbs get their <instance_id>: the
Control Plane generates instance IDs, and they cannot be constructed
by hand. 'controlplane database get' shows the same IDs for one database.

Example:
  pgedge controlplane database instance list
  pgedge controlplane database instance list --database storefront
  pgedge controlplane database instance list -o json`,
		Args: cobra.NoArgs,
	}
	var database string
	cmd.Flags().StringVar(&database, "database", "",
		"Database ID whose instances to show (default: all)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := clientFromCmd(rt, cmd)
		if err != nil {
			return err
		}
		resp, err := client.ListDatabasesWithResponse(
			context.Background(), &api.ListDatabasesParams{})
		if err != nil {
			return networkError("list instances", err)
		}
		if err := checkResponse(resp.StatusCode(),
			string(resp.Body)); err != nil {
			return err
		}
		var dbs []api.DatabaseSummary
		if resp.JSON200 != nil {
			dbs = resp.JSON200.Databases
		}
		if database != "" {
			dbs, err = onlyDatabase(dbs, database)
			if err != nil {
				return err
			}
		}
		pairs := flattenInstances(dbs)

		if rt.Output.Structured() {
			items := make([]instanceOutput, 0, len(pairs))
			for _, p := range pairs {
				items = append(items, instanceOutput{
					DatabaseID: p.dbID, Instance: p.inst})
			}
			return rt.Output.Print(items, nil)
		}
		if len(pairs) == 0 {
			fmt.Fprintln(rt.Stderr, "No instances found.")
			return nil
		}
		rows := make([]output.Row, 0, len(pairs))
		for _, p := range pairs {
			rows = append(rows, instanceListRow{
				f: newInstanceFields(p.dbID, p.inst)})
		}
		return rt.Output.Print(rows, instanceListColumns)
	}
	return cmd
}

// onlyDatabase narrows dbs to the one named id, and reports a
// not-found rather than an empty table when the ID names nothing.
//
// The check is client-side because it can be: the list response
// carries every database that exists, so "no such database" and
// "exists but has no instances" are distinguishable from one
// response. A plain filter would exit 0 on a typo, which reads
// exactly like an empty database.
func onlyDatabase(dbs []api.DatabaseSummary, id string) (
	[]api.DatabaseSummary, error,
) {
	for _, d := range dbs {
		if d.Id == id {
			return []api.DatabaseSummary{d}, nil
		}
	}
	return nil, &ExitError{
		msg:  fmt.Sprintf("resource not found: no database %q", id),
		code: ExitNotFound,
	}
}
