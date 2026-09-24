package cmd

import (
	"testing"

	"github.com/oapi-codegen/nullable"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
)

// TestServiceEndpointPrefersPublicDomain pins the internal-port fix:
// the table must never surface the internal port as a dialable
// locator. When a public domain is registered, ENDPOINT is the
// TLS-terminated address a caller can actually dial — the internal
// port is dropped entirely, since dialing it yields
// SSL:WRONG_VERSION_NUMBER.
func TestServiceEndpointPrefersPublicDomain(t *testing.T) {
	svc := api.Service{
		ServiceId:    "svc-1",
		ServiceType:  api.ServiceServiceTypeMcp,
		Port:         nullable.NewNullableWithValue(14052),
		PublicDomain: nullable.NewNullableWithValue("mcp.mydb.a1.pgedge.io"),
		PrivateDomain: nullable.NewNullableWithValue(
			"mcp-a1b2c3d4.mydb.internal.pgedge.cloud"),
	}

	got := serviceEndpoint(svc)
	want := "https://mcp.mydb.a1.pgedge.io"
	if got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
	if got == "14052" {
		t.Fatal("endpoint surfaced the raw internal port")
	}
}

// TestServiceEndpointFallsBackToPrivateDomain covers the case with no
// public exposure: the private DNS name plus internal port IS the
// real locator for a caller on that private network, and the
// "internal." naming convention in the domain itself already marks
// it as such.
func TestServiceEndpointFallsBackToPrivateDomain(t *testing.T) {
	svc := api.Service{
		ServiceId:   "svc-1",
		ServiceType: api.ServiceServiceTypeMcp,
		Port:        nullable.NewNullableWithValue(14052),
		PrivateDomain: nullable.NewNullableWithValue(
			"mcp-a1b2c3d4.mydb.internal.pgedge.cloud"),
	}

	got := serviceEndpoint(svc)
	want := "mcp-a1b2c3d4.mydb.internal.pgedge.cloud:14052"
	if got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}

// TestServiceEndpointLabelsBarePort covers the case with no domain at
// all: the bare port must be labeled, not surfaced as a plain number
// that looks dialable on its own.
func TestServiceEndpointLabelsBarePort(t *testing.T) {
	svc := api.Service{
		ServiceId:   "svc-1",
		ServiceType: api.ServiceServiceTypeMcp,
		Port:        nullable.NewNullableWithValue(14052),
	}

	got := serviceEndpoint(svc)
	want := "14052 (internal port, no domain)"
	if got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}

// TestServiceEndpointHandlesAbsentFields covers the case a service
// carries none of port, public domain or private domain — the empty
// string, not a crash.
func TestServiceEndpointHandlesAbsentFields(t *testing.T) {
	svc := api.Service{ServiceId: "svc-1", ServiceType: api.ServiceServiceTypeMcp}
	if got := serviceEndpoint(svc); got != "" {
		t.Errorf("endpoint = %q, want empty", got)
	}
}

// TestServiceRowColumnCount pins the column count/order after
// dropping PORT and DOMAIN in favor of ENDPOINT.
func TestServiceRowColumnCount(t *testing.T) {
	row := serviceRow(api.Service{
		ServiceId:    "svc-1",
		ServiceType:  api.ServiceServiceTypeMcp,
		State:        nullable.NewNullableWithValue("running"),
		PublicDomain: nullable.NewNullableWithValue("mcp.example.com"),
	})
	cols := row.Columns()
	if len(cols) != len(serviceColumns) {
		t.Fatalf("got %d columns, want %d", len(cols), len(serviceColumns))
	}
	if cols[0] != "svc-1" || cols[1] != "mcp" {
		t.Errorf("id/type columns = %v", cols[:2])
	}
	if cols[3] != "https://mcp.example.com" {
		t.Errorf("endpoint column = %q", cols[3])
	}
}
