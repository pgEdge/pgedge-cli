package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
)

// twoDatabasesBody carries two databases whose instances arrive in a
// deliberately unsorted order, so the command's ordering guarantee is
// exercised rather than inherited from the fixture.
const twoDatabasesBody = `{"databases":[` +
	`{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"storefront-n2-689qacsi","host_id":"host-1",` +
	`"node_name":"n2","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"replica","version":"18.4"},` +
	`"spock":{"version":"5.0.10"}},` +
	`{"id":"storefront-n1-689qacsi","host_id":"host-1",` +
	`"node_name":"n1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"18.4"},` +
	`"spock":{"version":"5.0.10"}}]},` +
	`{"id":"analytics","state":"creating",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"analytics-n1-4kd8mzqp","host_id":"host-2",` +
	`"node_name":"n1","state":"creating",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"spock":{}}]}]}`

// noInstancesBody is a real database with an empty instance list — the
// case that must stay distinguishable from a typo'd --database.
const noInstancesBody = `{"databases":[` +
	`{"id":"emptydb","state":"creating",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z","instances":[]}]}`

func TestInstanceListText(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"DATABASE", "ID", "NODE", "SPOCK",
		"analytics-n1-4kd8mzqp", "storefront-n1-689qacsi",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output: %q", want, s)
		}
	}
}

// columnPositionsBody carries one instance whose ROLE, PG and SPOCK
// values are mutually distinguishable (and distinguishable from every
// other column's value), so a swapped mapping in
// instanceListRow.Columns() shows up as a value under the wrong
// header rather than two values that happen to already match.
const columnPositionsBody = `{"databases":[` +
	`{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"storefront-n1-689qacsi","host_id":"host-1",` +
	`"node_name":"n1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"18.4"},` +
	`"spock":{"version":"5.0.10"}}]}]}`

// TestInstanceListColumnPositions pins the value-to-column mapping.
// instanceListColumns and instanceListRow.Columns() are two separate,
// hand-written slices; nothing enforces they agree in order, so
// swapping two fields in Columns() (e.g. ROLE and PG) would compile,
// run, and print a plausible-looking table with values under the
// wrong headers. This test indexes into the row by looking up each
// column's position in instanceListColumns, rather than hard-coding
// an index, so inserting a new column shifts both sides together
// instead of silently misaligning the assertion.
func TestInstanceListColumnPositions(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, columnPositionsBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want a header and one data row: %q",
			len(lines), out.String())
	}
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
	if len(header) != len(instanceListColumns) {
		t.Fatalf("header = %v, want %d fields matching "+
			"instanceListColumns %v",
			header, len(instanceListColumns), instanceListColumns)
	}
	want := map[string]string{
		"DATABASE": "storefront",
		"ID":       "storefront-n1-689qacsi",
		"NODE":     "n1",
		"HOST":     "host-1",
		"STATE":    "available",
		"ROLE":     "primary",
		"PG":       "18.4",
		"SPOCK":    "5.0.10",
	}
	if len(want) != len(instanceListColumns) {
		t.Fatalf("test covers %d columns, instanceListColumns has %d: "+
			"%v", len(want), len(instanceListColumns), instanceListColumns)
	}
	for name, wantVal := range want {
		idx := -1
		for i, h := range instanceListColumns {
			if h == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Fatalf("instanceListColumns %v has no %q column",
				instanceListColumns, name)
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

// TestInstanceListOrderIsStable pins the sort. The Control Plane
// promises no order for either databases or instances, and a table
// that reshuffles between runs cannot be diffed.
func TestInstanceListOrderIsStable(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	ids := []string{}
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && strings.Contains(f[1], "-") &&
			f[0] != "DATABASE" {
			ids = append(ids, f[1])
		}
	}
	want := []string{
		"analytics-n1-4kd8mzqp",
		"storefront-n1-689qacsi",
		"storefront-n2-689qacsi",
	}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids = %v, want %v", ids, want)
			break
		}
	}
}

// TestInstanceListJSONCarriesDatabaseID pins the manufactured key. The
// Control Plane has no endpoint returning instances across databases,
// so the owning database is attached here rather than implied by the
// request path.
func TestInstanceListJSONCarriesDatabaseID(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out.String(), err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %v", len(got), got)
	}
	if got[0]["database_id"] != "analytics" {
		t.Errorf("database_id = %v, want analytics",
			got[0]["database_id"])
	}
	// The API instance's own fields must be promoted alongside it, not
	// nested under a wrapper key.
	if got[0]["id"] != "analytics-n1-4kd8mzqp" {
		t.Errorf("id = %v, want the instance id", got[0]["id"])
	}
}

// TestInstanceListEmptyJSONIsAnArray pins [] rather than null. A
// script doing `| jq length` must not fail on an empty fleet.
func TestInstanceListEmptyJSONIsAnArray(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "json")
	url := newServer(t, jsonHandler(200, noInstancesBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("output = %q, want []", got)
	}
}

// sameNodeNameBody puts two instances in the same database under the
// same node name, arriving out of ID order. This shape is unreachable
// on valid data — Spock node names are unique within a database — but
// it is exactly the case the sort's third key exists to make total.
const sameNodeNameBody = `{"databases":[` +
	`{"id":"db1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"db1-n1-zzz","host_id":"host-1",` +
	`"node_name":"n1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"spock":{}},` +
	`{"id":"db1-n1-aaa","host_id":"host-1",` +
	`"node_name":"n1","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"spock":{}}]}]}`

// TestInstanceListOrderTiebreaksOnInstanceID pins the sort's third key
// directly: with (database, node) tied, sort.Slice — which is not
// stable — is otherwise free to swap the two instances between runs.
func TestInstanceListOrderTiebreaksOnInstanceID(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, sameNodeNameBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	ids := []string{}
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && strings.Contains(f[1], "-") &&
			f[0] != "DATABASE" {
			ids = append(ids, f[1])
		}
	}
	want := []string{"db1-n1-aaa", "db1-n1-zzz"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids = %v, want %v", ids, want)
			break
		}
	}
}

func TestInstanceListNoInstancesText(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, noInstancesBody))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	if !strings.Contains(errb.String(), "No instances found.") {
		t.Errorf("want the empty notice on stderr: %q", errb.String())
	}
}

// TestInstanceListNoDatabasesAtAll is a distinct case from a database
// with no instances: here the tenant is empty, so the loop never runs
// at all. Unscoped, that is not an error.
func TestInstanceListNoDatabasesAtAll(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, `{"databases":[]}`))
	if err := runControlplane(t, rt, out, url,
		"database", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}
	if !strings.Contains(errb.String(), "No instances found.") {
		t.Errorf("want the empty notice on stderr: %q", errb.String())
	}
}

// requireNotFound asserts the not-found exit code rather than merely a
// non-nil error, so a filter that fails for some unrelated reason
// cannot pass as a correct refusal.
func requireNotFound(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := cli.ExitCode(err); got != ExitNotFound {
		t.Fatalf("exit code = %d, want %d (not found): %v",
			got, ExitNotFound, err)
	}
}

// TestInstanceListUnknownDatabaseIsNotFound is the reason the filter
// is not a plain filter. A typo'd --database returning zero rows and
// exit 0 is indistinguishable from a database that has no instances,
// which is the class of silent wrong answer this repo has paid for
// more than once.
func TestInstanceListUnknownDatabaseIsNotFound(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	err := runControlplane(t, rt, out, url, "database", "instance", "list",
		"--database", "storefrnot")
	requireNotFound(t, err)
}

// TestInstanceListEmptyDatabaseExitsZero is the other half of the
// pair: a REAL database with no instances is not an error. Run
// alongside the test above, the two prove the distinction is made.
func TestInstanceListEmptyDatabaseExitsZero(t *testing.T) {
	rt, out, errb := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, noInstancesBody))
	if err := runControlplane(t, rt, out, url, "database", "instance", "list",
		"--database", "emptydb"); err != nil {
		t.Fatalf("instance list --database emptydb: %v", err)
	}
	if !strings.Contains(errb.String(), "No instances found.") {
		t.Errorf("want the empty notice on stderr: %q", errb.String())
	}
}

func TestInstanceListScopedToOneDatabase(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	if err := runControlplane(t, rt, out, url, "database", "instance", "list",
		"--database", "storefront"); err != nil {
		t.Fatalf("instance list --database storefront: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "storefront-n1-689qacsi") {
		t.Errorf("want storefront instances: %q", s)
	}
	if strings.Contains(s, "analytics-n1-4kd8mzqp") {
		t.Errorf("analytics must be filtered out: %q", s)
	}
	// The DATABASE column stays, redundant though it is when scoped: a
	// table whose columns change with a flag is worse for scripts.
	if !strings.Contains(s, "DATABASE") {
		t.Errorf("want the DATABASE column even when scoped: %q", s)
	}
}

// TestInstanceListUnknownDatabaseOnEmptyTenant is the same refusal
// with nothing to search: an ID that names nothing is not found
// whether the tenant is empty or the ID is a typo.
func TestInstanceListUnknownDatabaseOnEmptyTenant(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, `{"databases":[]}`))
	err := runControlplane(t, rt, out, url, "database", "instance", "list",
		"--database", "storefront")
	requireNotFound(t, err)
}

// TestInstanceListEmptyDatabaseFlagMeansAbsent: an empty value is not a
// request for a database named "". An empty --profile, by contrast, is
// a usage error. The divergence is deliberate: --database narrows a read,
// while --profile selects a tenant, so falling through costs nothing
// here and an account there. Do not "align" this with --profile without
// that argument changing.
func TestInstanceListEmptyDatabaseFlagMeansAbsent(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, twoDatabasesBody))
	if err := runControlplane(t, rt, out, url, "database", "instance", "list",
		"--database="); err != nil {
		t.Fatalf("instance list --database=: %v", err)
	}
	if !strings.Contains(out.String(), "analytics-n1-4kd8mzqp") {
		t.Errorf("want every database's instances: %q", out.String())
	}
}
