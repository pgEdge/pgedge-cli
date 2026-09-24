package cmd

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// instanceColumns is `database get`'s instance table: the ID leads,
// because it is the value every other instance verb needs as an
// argument, and connection detail trails because you are already
// looking at one database.
var instanceColumns = []string{
	"ID", "NODE", "HOST", "STATE", "ROLE", "PG", "SPOCK",
	"ADDRESSES", "PORT"}

// instanceFields holds every rendered value for one instance. It is
// kept separate from instanceRow so that other row types can project
// the same fields through newInstanceFields without recomputing
// anything — the point of the ID column is that the value read in one
// view is the value passed as an argument to another.
type instanceFields struct {
	database, id, node, host, state, role, pg, spock string
	addresses, port                                  string
}

// newInstanceFields flattens one API instance into rendered strings.
//
// Anything the Control Plane has not filled in renders as "-". That is
// not defensive padding: while a database is `creating` the CP sends
// `postgres` with no version and `spock` as `{}` (verified live,
// 2026-08-09), so the version columns are empty precisely when someone
// is watching a database come up.
func newInstanceFields(
	databaseID string, inst api.Instance,
) instanceFields {
	f := instanceFields{
		database:  databaseID,
		id:        inst.Id,
		node:      inst.NodeName,
		host:      inst.HostId,
		state:     string(inst.State),
		role:      "-",
		pg:        "-",
		spock:     "-",
		addresses: "-",
		port:      "-",
	}
	if inst.Postgres != nil {
		f.role = derefOr(inst.Postgres.Role, "-")
		f.pg = derefOr(inst.Postgres.Version, "-")
	}
	if inst.Spock != nil {
		f.spock = derefOr(inst.Spock.Version, "-")
	}
	// ConnectionInfo is the address/port a caller dials to reach this
	// Postgres instance directly. It never carries credentials — a
	// database user's password is a separate field on the spec's user
	// list, and the CP never populates it in a response anyway
	// (verified live) — so there is nothing here to redact.
	if ci := inst.ConnectionInfo; ci != nil {
		if ci.Addresses != nil && len(*ci.Addresses) > 0 {
			f.addresses = joinStrings(*ci.Addresses)
		}
		if ci.Port != nil {
			f.port = fmt.Sprintf("%d", *ci.Port)
		}
	}
	return f
}

// instanceRow renders one instance under `database get`.
type instanceRow struct{ f instanceFields }

func (r instanceRow) Columns() []string {
	return []string{r.f.id, r.f.node, r.f.host,
		output.ColorStatus(r.f.state), r.f.role, r.f.pg, r.f.spock,
		r.f.addresses, r.f.port}
}
