package cmd

import (
	"fmt"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
	"github.com/pgEdge/pgedge-cli/internal/output"
)

// instanceColumns leads with the ID because every other instance verb
// takes it.
var instanceColumns = []string{
	"ID", "NODE", "HOST", "STATE", "ROLE", "PG", "SPOCK",
	"ADDRESSES", "PORT"}

// instanceFields is separate from instanceRow so every view renders
// the same values, the ID above all.
type instanceFields struct {
	database, id, node, host, state, role, pg, spock string
	addresses, port                                  string
}

// newInstanceFields renders anything unfilled as "-". While a database
// is `creating` the Control Plane sends `postgres` with no version and
// `spock` as `{}` (verified live, 2026-08-09).
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
	// Nothing to redact: ConnectionInfo carries no credentials, and
	// the Control Plane never returns a user's password (verified
	// live).
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

type instanceRow struct{ f instanceFields }

func (r instanceRow) Columns() []string {
	return []string{r.f.id, r.f.node, r.f.host,
		output.ColorStatus(r.f.state), r.f.role, r.f.pg, r.f.spock,
		r.f.addresses, r.f.port}
}
