package cmd

import (
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/controlplane/api"
)

// strptr is a local helper: the generated client models every optional
// string as *string.
func strptr(s string) *string { return &s }

// TestNewInstanceFieldsRendersPlaceholders pins the creating-database
// case. While a database is creating the Control Plane sends postgres
// without a version and spock as {}, so every version column is empty
// exactly when someone is most likely watching. Verified live on the
// dev Control Plane, 2026-08-09.
func TestNewInstanceFieldsRendersPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		name string
		inst api.Instance
		want instanceFields
	}{
		{
			name: "bare instance renders dashes",
			inst: api.Instance{
				Id: "storefront-n1-689qacsi", HostId: "host-1",
				NodeName: "n1", State: "creating",
			},
			want: instanceFields{
				database: "storefront", id: "storefront-n1-689qacsi",
				node: "n1", host: "host-1", state: "creating",
				role: "-", pg: "-", spock: "-",
				addresses: "-", port: "-",
			},
		},
		{
			name: "empty spock object still renders a dash",
			inst: api.Instance{
				Id: "storefront-n1-689qacsi", HostId: "host-1",
				NodeName: "n1", State: "creating",
				Postgres: &api.InstancePostgresStatus{
					Role: strptr("primary"),
				},
				Spock: &api.InstanceSpockStatus{},
			},
			want: instanceFields{
				database: "storefront", id: "storefront-n1-689qacsi",
				node: "n1", host: "host-1", state: "creating",
				role: "primary", pg: "-", spock: "-",
				addresses: "-", port: "-",
			},
		},
		{
			name: "fully populated instance",
			inst: api.Instance{
				Id: "storefront-n1-689qacsi", HostId: "host-1",
				NodeName: "n1", State: "available",
				Postgres: &api.InstancePostgresStatus{
					Role: strptr("primary"), Version: strptr("18.4"),
				},
				Spock: &api.InstanceSpockStatus{
					Version: strptr("5.0.10"),
				},
				ConnectionInfo: &api.InstanceConnectionInfo{
					Addresses: &[]string{"10.0.1.5", "10.0.1.6"},
					Port:      int64ptr(5432),
				},
			},
			want: instanceFields{
				database: "storefront", id: "storefront-n1-689qacsi",
				node: "n1", host: "host-1", state: "available",
				role: "primary", pg: "18.4", spock: "5.0.10",
				addresses: "10.0.1.5, 10.0.1.6", port: "5432",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newInstanceFields("storefront", tc.inst)
			if got != tc.want {
				t.Errorf("newInstanceFields() = %+v, want %+v",
					got, tc.want)
			}
		})
	}
}

// int64ptr mirrors strptr for the one numeric optional in play.
// InstanceConnectionInfo.Port is *int64, NOT *int — verified against
// the generated client, 2026-08-09.
func int64ptr(i int64) *int64 { return &i }

// TestDatabaseGetShowsInstanceID drives the COMMAND, not the renderer.
// The ID column exists so that a reader of `get` can act on what they
// just saw; a renderer-level test cannot see a RunE that renders the
// wrong column set.
func TestDatabaseGetShowsInstanceID(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, databaseDetailBody))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "storefront"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "inst-1") {
		t.Errorf("want inst-1 in output: %q", s)
	}
	// The Instances table's header must lead with ID, NODE — not
	// merely contain the word ID somewhere: the database summary
	// table's header also starts with ID (ID, STATE, CREATED,
	// UPDATED), so only the column order tells the two apart.
	var sawInstanceHeader bool
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == instanceColumns[0] &&
			fields[1] == instanceColumns[1] {
			sawInstanceHeader = true
			break
		}
	}
	if !sawInstanceHeader {
		t.Errorf("want Instances table header starting %s, %s: %q",
			instanceColumns[0], instanceColumns[1], s)
	}
	// The ID must lead the instance row, not merely appear somewhere.
	row := instanceRowForNode(t, s, "n1")
	if row[0] != "inst-1" {
		t.Errorf("instance row = %v, want inst-1 first", row)
	}
}

// columnPositionsGetBody carries one instance whose ID, HOST, STATE,
// ROLE, PG, SPOCK, ADDRESSES and PORT values are all mutually
// distinguishable, so a swapped mapping in instanceRow.Columns() (the
// same class of risk TestInstanceListColumnPositions guards against
// for `instance list`, but for `database get`'s own instance table)
// shows up as a value under the wrong header rather than two values
// that already happen to match. A single address is used, not two:
// joined addresses render as "a, b" and would split into two fields
// under strings.Fields, throwing off column counting.
const columnPositionsGetBody = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"storefront-n1-689qacsi","host_id":"host-1",` +
	`"node_name":"n1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"18.4"},` +
	`"spock":{"version":"5.0.10"},` +
	`"connection_info":{"addresses":["10.0.1.5"],"port":5432}}]}`

// TestDatabaseGetInstanceColumnPositions pins the value-to-column
// mapping in instanceRow.Columns(). instanceColumns and Columns() are
// two separate, hand-written slices; nothing enforces they agree in
// order, so swapping two fields in Columns() (e.g. ROLE and PG, or
// HOST and STATE) would compile, run, and print a plausible-looking
// table with values under the wrong headers. This indexes into the
// row by looking up each column's position in instanceColumns rather
// than hard-coding a number, so inserting a new column moves the
// lookup and the assertion together instead of silently misaligning
// them.
func TestDatabaseGetInstanceColumnPositions(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, columnPositionsGetBody))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "storefront"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	row := instanceRowForNode(t, out.String(), "n1")
	want := map[string]string{
		"ID":        "storefront-n1-689qacsi",
		"NODE":      "n1",
		"HOST":      "host-1",
		"STATE":     "available",
		"ROLE":      "primary",
		"PG":        "18.4",
		"SPOCK":     "5.0.10",
		"ADDRESSES": "10.0.1.5",
		"PORT":      "5432",
	}
	if len(want) != len(instanceColumns) {
		t.Fatalf("test covers %d columns, instanceColumns has %d: %v",
			len(want), len(instanceColumns), instanceColumns)
	}
	for name, wantVal := range want {
		idx := -1
		for i, h := range instanceColumns {
			if h == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Fatalf("instanceColumns %v has no %q column",
				instanceColumns, name)
		}
		if idx >= len(row) {
			t.Fatalf("row %v has no field at index %d for column %q",
				row, idx, name)
		}
		if row[idx] != wantVal {
			t.Errorf("column %s (index %d) = %q, want %q",
				name, idx, row[idx], wantVal)
		}
	}
}
