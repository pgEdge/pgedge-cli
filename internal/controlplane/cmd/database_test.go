package cmd

import (
	"net/http"
	"strings"
	"testing"
)

const databasesBody = `{"databases":[{"id":"storefront",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"}]}`

const databaseBody = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"}`

// databaseDetailBody carries instances and service instances so the
// text renderer's Instances/Services sections can be exercised.
const databaseDetailBody = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"inst-1","host_id":"host-a","node_name":"n1",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"16.14"},` +
	`"spock":{"version":"5.0.9"}},` +
	`{"id":"inst-2","host_id":"host-b","node_name":"n2",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"replica","version":"16.14"},` +
	`"spock":{"version":"5.0.9"}}],` +
	`"service_instances":[` +
	`{"service_instance_id":"si-1","database_id":"storefront",` +
	`"service_id":"mcp","host_id":"host-a","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"}]}`

// databaseConnInfoBody carries connection_info on one instance and
// none on the other, so the text renderer's ADDRESSES/PORT columns
// can be exercised for both the populated and absent cases. It also
// carries a spec with a database user's password set to a sentinel
// value — the CP never populates that field in a real response, but
// the fixture sets it anyway so "text never prints a password" is
// exercising an actual guard rather than a fixture with nothing to
// leak in the first place.
const databaseConnInfoBody = `{"id":"storefront","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"spec":{"database_name":"storefront","nodes":[],` +
	`"database_users":[{"username":"app",` +
	`"password":"fake-sentinel-pw"}]},` +
	`"instances":[` +
	`{"id":"inst-1","host_id":"host-a","node_name":"n1",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"16.14"},` +
	`"spock":{"version":"5.0.9"},` +
	`"connection_info":{"addresses":["10.0.1.5","10.0.1.6"],` +
	`"port":5432}},` +
	`{"id":"inst-2","host_id":"host-b","node_name":"n2",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"replica","version":"16.14"},` +
	`"spock":{"version":"5.0.9"}}]}`

// databaseFailedBody carries a failed instance and a failed service,
// each with a reason. The instance message is deliberately multi-line
// and padded, because that is the shape the Control Plane actually
// sends: it folds Postgres DETAIL lines into the field.
const databaseFailedBody = `{"id":"storefront","state":"failed",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"instances":[` +
	`{"id":"inst-1","host_id":"host-a","node_name":"n1",` +
	`"state":"failed","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"error":"could not start postgres\n  DETAIL:  disk full\n"},` +
	`{"id":"inst-2","host_id":"host-b","node_name":"n2",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"postgres":{"role":"primary","version":"16.14"}}],` +
	`"service_instances":[` +
	`{"service_instance_id":"si-1","database_id":"storefront",` +
	`"service_id":"mcp","host_id":"host-a","state":"failed",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"error":"image pull backoff"},` +
	`{"service_instance_id":"si-2","database_id":"storefront",` +
	`"service_id":"rag","host_id":"host-b","state":"available",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z","error":""}]}`

// instanceRowForNode returns the whitespace-split cells of the
// Instances table row whose NODE column is exactly node, so a test
// can assert on a specific column rather than "the line contains a
// dash somewhere" — which every row here already does, since HOST
// values like "host-b" are not the thing under test.
//
// NODE is the SECOND cell: the ID column leads the table. No other
// line in the output can collide: the database summary row and the
// service rows both carry a state in that position, and the two
// error tables (NODE, ERROR / SERVICE, ERROR) carry the first word
// of an error message there instead of a node name.
func instanceRowForNode(t *testing.T, out, node string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[1] == node {
			return fields
		}
	}
	t.Fatalf("no row for node %q in output: %q", node, out)
	return nil
}

// section returns the block of rendered output belonging to one
// titled section. Sections are separated by blank lines, so a check
// scoped to "Service errors" cannot accidentally read the Services
// table sitting a few lines below it.
func section(t *testing.T, out, title string) string {
	t.Helper()
	for _, block := range strings.Split(out, "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(block), title) {
			return block
		}
	}
	t.Fatalf("no %q section in output: %q", title, out)
	return ""
}

func TestDatabaseListRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databasesBody))
		if err := runControlplane(t, rt, out, url,
			"database", "list"); err != nil {
			t.Fatalf("database list: %v", err)
		}
		if !strings.Contains(out.String(), "storefront") {
			t.Errorf("missing database: %q", out.String())
		}
		if !strings.Contains(out.String(), "available") {
			t.Errorf("missing state: %q", out.String())
		}
		if !strings.Contains(out.String(), "2025-06-18") {
			t.Errorf("missing date: %q", out.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		rt, out, errb := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, `{"databases":[]}`))
		if err := runControlplane(t, rt, out, url,
			"database", "list"); err != nil {
			t.Fatalf("database list empty: %v", err)
		}
		if !strings.Contains(errb.String(), "No databases found") {
			t.Errorf("want 'No databases found': %q", errb.String())
		}
	})

	t.Run("json", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, databasesBody))
		if err := runControlplane(t, rt, out, url,
			"database", "list"); err != nil {
			t.Fatalf("database list json: %v", err)
		}
		if !strings.Contains(out.String(), "storefront") {
			t.Errorf("missing database in json: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(500, "boom"))
		if err := runControlplane(t, rt, out, url,
			"database", "list"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestDatabaseGetRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get: %v", err)
		}
		if !strings.Contains(out.String(), "storefront") {
			t.Errorf("missing id: %q", out.String())
		}
		if !strings.Contains(out.String(), "available") {
			t.Errorf("missing state: %q", out.String())
		}
		// A summary-only database must not print section headers.
		if strings.Contains(out.String(), "Instances") ||
			strings.Contains(out.String(), "Services") {
			t.Errorf("unexpected section header: %q", out.String())
		}
	})

	t.Run("text with instances and services", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseDetailBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get detail: %v", err)
		}
		s := out.String()
		for _, want := range []string{
			"Instances", "n1", "n2", "host-a", "host-b",
			"primary", "replica", "16.14", "5.0.9",
			"Services", "mcp",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q in detail output: %q", want, s)
			}
		}
	})

	// The reason a failed instance is reported at all. `get` is the
	// only command that can show either error field — the Control
	// Plane has no node or instance list endpoint — so without this
	// the only way to see why something failed is -o json.
	t.Run("text surfaces instance and service errors", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseFailedBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get failed detail: %v", err)
		}
		s := out.String()
		// The message is flattened, so the doubled space after
		// DETAIL: collapses with everything else.
		for _, want := range []string{
			"Instance errors", "could not start postgres",
			"DETAIL: disk full",
			"Service errors", "image pull backoff",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q in output: %q", want, s)
			}
		}
		// Flattened to one line: a newline in a cell breaks every
		// column to its right for the rest of the table.
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "could not start postgres") &&
				!strings.Contains(line, "disk full") {
				t.Errorf("error was not flattened onto one line: %q",
					line)
			}
		}
		// Healthy rows must not produce an error row. n2 carries no
		// error field at all; rag carries an empty one. Both checks
		// must look at the error section alone — the Services table
		// lists rag legitimately, a few lines further down.
		instErrs := section(t, s, "Instance errors")
		if strings.Contains(instErrs, "n2") {
			t.Errorf("healthy instance n2 listed as an error: %q",
				instErrs)
		}
		svcErrs := section(t, s, "Service errors")
		if strings.Contains(svcErrs, "rag") {
			t.Errorf("empty error string produced a row: %q", svcErrs)
		}
		if !strings.Contains(svcErrs, "mcp") {
			t.Fatalf("control failed: the failing service is missing "+
				"from its own section: %q", svcErrs)
		}
	})

	// Silence when nothing is wrong: an "Instance errors" header over
	// an empty table is noise on every healthy database.
	t.Run("text omits error sections when healthy", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseDetailBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get detail: %v", err)
		}
		s := out.String()
		if !strings.Contains(s, "Instances") {
			t.Fatalf("control failed: no Instances section: %q", s)
		}
		for _, unwanted := range []string{
			"Instance errors", "Service errors",
		} {
			if strings.Contains(s, unwanted) {
				t.Errorf("unexpected %q on a healthy database: %q",
					unwanted, s)
			}
		}
	})

	// CP 0.10.0's connection_info carried
	// addresses/port for a Postgres instance, but text mode never
	// rendered it — a caller had to drop to -o json to see it. This
	// pins that it now shows up in the Instances table, matching that
	// table's existing style, and that an instance with none present
	// still renders "-" instead of a blank cell or a crash.
	t.Run("text shows instance connection info", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseConnInfoBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get conn info: %v", err)
		}
		s := out.String()
		for _, want := range []string{
			"ADDRESSES", "PORT", "10.0.1.5, 10.0.1.6", "5432",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q in output: %q", want, s)
			}
		}
		// n2 carries no connection_info at all: its row's trailing two
		// cells (ADDRESSES, PORT) must be the "-" placeholder, not
		// merely "some cell in this line is a dash" — host-b already
		// satisfies that trivially, so the check has to look at the
		// specific columns.
		instRow := instanceRowForNode(t, s, "n2")
		if len(instRow) < 2 {
			t.Fatalf("n2's row has too few columns: %v", instRow)
		}
		addresses, port := instRow[len(instRow)-2], instRow[len(instRow)-1]
		if addresses != "-" || port != "-" {
			t.Errorf("n2 ADDRESSES/PORT = %q/%q, want -/-",
				addresses, port)
		}
	})

	// A database user's password is never rendered anywhere in text
	// mode: the field exists on the generated spec type, but the CP
	// never populates it in a response (verified live), and
	// connection_info itself carries no credentials at all — only
	// addresses and a port. databaseConnInfoBody sets a real sentinel
	// value on a database user's password, so this test actually
	// exercises the omission rather than a fixture with nothing to
	// leak in the first place.
	t.Run("text never prints a password", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, databaseConnInfoBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get conn info: %v", err)
		}
		if strings.Contains(out.String(), "fake-sentinel-pw") {
			t.Errorf("text output leaked the password sentinel: %q",
				out.String())
		}
		if strings.Contains(strings.ToLower(out.String()), "password") {
			t.Errorf("text output mentions a password: %q", out.String())
		}
	})

	// -o json/yaml marshal the raw struct, so connection_info was
	// already present there before this change. Pin that the text
	// renderer change did not somehow alter the machine output.
	t.Run("json keeps connection_info untouched", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, databaseConnInfoBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get conn info json: %v", err)
		}
		for _, want := range []string{
			`"connection_info"`, `"10.0.1.5"`, `"port":5432`,
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("json output missing %q; got:\n%s",
					want, out.String())
			}
		}
	})

	t.Run("no data", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, plainHandler(200, "ok"))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get no data: %v", err)
		}
		if !strings.Contains(stderr.String(), "No database data returned") {
			t.Errorf("missing no-data notice: %q", stderr.String())
		}
	})

	t.Run("json output", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		url := newServer(t, jsonHandler(200, databaseBody))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("database get json: %v", err)
		}
		if !strings.Contains(out.String(), "storefront") {
			t.Errorf("missing id in json: %q", out.String())
		}
	})

	t.Run("server error", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(500, "boom"))
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestDatabaseDeleteRun(t *testing.T) {
	t.Run("force", func(t *testing.T) {
		rt, out, stderr := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200,
			`{"task":{`+
				`"task_id":"11111111-1111-1111-1111-111111111111",`+
				`"status":"pending","type":"delete",`+
				`"scope":"database","entity_id":"storefront",`+
				`"created_at":"2025-06-18T00:00:00Z"}}`))
		if err := runControlplane(t, rt, out, url,
			"database", "delete", "storefront", "--force"); err != nil {
			t.Fatalf("database delete: %v", err)
		}
		if !strings.Contains(stderr.String(), "Delete task") ||
			!strings.Contains(stderr.String(), "accepted") {
			t.Errorf("missing accepted message: %q", stderr.String())
		}
		if !strings.Contains(stderr.String(),
			"11111111-1111-1111-1111-111111111111") {
			t.Errorf("missing task id: %q", stderr.String())
		}
		if !strings.Contains(stderr.String(), "pending") {
			t.Errorf("missing status: %q", stderr.String())
		}
	})

	t.Run("no force refuses", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "text")
		url := newServer(t, jsonHandler(200, `{}`))
		if err := runControlplane(t, rt, out, url,
			"database", "delete", "storefront"); err == nil {
			t.Fatal("expected refusal without --force")
		}
	})
}

// TestRouteMissSurfacesClearError characterizes the integrated path
// from a real controlplane verb down through checkResponse's route-miss
// discriminator (client.go). The stub is a routing mux registering
// ONLY /v1/version, so `database get` — which requests
// /v1/databases/{id}, a path the mux never registered — gets Go's
// default mux's plain "404 page not found" text natively. A
// jsonHandler-style catch-all would answer every path with one body
// and could never reproduce that text, which is exactly what the
// discriminator keys on. The /v1/version registration is realism, not
// mechanism: `database get` never probes it — selectBaseURL's
// single-base-url fast path skips the probe entirely — so any path
// this mux never registered would 404 the same way; ServeMux answers
// every unregistered path with the same plain text regardless of
// which paths happen to be registered.
func TestRouteMissSurfacesClearError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/version", jsonHandler(200,
		`{"version":"v0.9.0","revision":"abc",`+
			`"revision_time":"2026-01-01T00:00:00Z","arch":"amd64"}`))
	url := newServer(t, mux.ServeHTTP)
	err := runControlplane(t, rt, out, url,
		"database", "get", "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected an error from the route miss")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T: %v", err, err)
	}
	if !strings.Contains(ee.Error(),
		"does not serve an endpoint this command needs") {
		t.Errorf("message missing route-miss clause: %q", ee.Error())
	}
	if ee.Code() != ExitGeneral {
		t.Errorf("code = %d, want ExitGeneral(%d)", ee.Code(), ExitGeneral)
	}
}

// TestLegacy404SurfacesNotFoundError is the counterpart to
// TestRouteMissSurfacesClearError: a genuine resource miss — the
// Control Plane's own handler answering 404 with a Goa JSON error
// body — must keep the pre-existing legacy path (ExitNotFound, the
// "resource not found" message) rather than being swept into the
// route-miss branch. The discriminator in checkResponse keys on the
// exact plain-text mux body, so a JSON body must never match it.
func TestLegacy404SurfacesNotFoundError(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(http.StatusNotFound,
		`{"name":"not_found","message":"database not found"}`))
	err := runControlplane(t, rt, out, url,
		"database", "get", "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected an error from the not-found response")
	}
	ee, ok := err.(*ExitError)
	if !ok {
		t.Fatalf("want *ExitError, got %T: %v", err, err)
	}
	wantMsg := `resource not found (404): ` +
		`{"name":"not_found","message":"database not found"}`
	if ee.Error() != wantMsg {
		t.Errorf("message = %q, want %q", ee.Error(), wantMsg)
	}
	if ee.Code() != ExitNotFound {
		t.Errorf("code = %d, want ExitNotFound(%d)", ee.Code(), ExitNotFound)
	}
}
