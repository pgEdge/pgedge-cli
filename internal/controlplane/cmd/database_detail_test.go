package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// databaseSpecBody carries every spec section the text renderer has a
// table for, with a sentinel in every field that must never be
// printed: the user's password, the service config (where LLM API
// keys live), each repository's keys, and custom_options. The Control
// Plane strips the key fields from its responses, so the sentinels
// exercise the renderer's own omission rather than the API's.
const databaseSpecBody = `{"id":"storefront","state":"available",` +
	`"tenant_id":"tenant-9","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"spec":{"database_name":"shopdb","postgres_version":"16.14",` +
	`"spock_version":"5","cpus":"2","memory":"4Gi","port":5432,` +
	`"nodes":[{"name":"n1","host_ids":["host-a","host-b"],` +
	`"port":6432,"cpus":"500m"},` +
	`{"name":"n2","host_ids":["host-c"],"source_node":"n1"}],` +
	`"database_users":[{"username":"app","password":"SENTINEL-PW",` +
	`"db_owner":true,"roles":["pgedge_application"],` +
	`"attributes":["LOGIN"]},{"username":"ro"}],` +
	`"services":[{"service_id":"mcp","service_type":"mcp",` +
	`"version":"latest","host_ids":["host-a"],"port":8080,` +
	`"connect_as":"app","config":{"llm_provider":"openai",` +
	`"openai_api_key":"SENTINEL-CFG"}}],` +
	`"backup_config":{"repositories":[{"id":"primary","type":"s3",` +
	`"s3_bucket":"bkt","s3_region":"us-east-1","base_path":"/pg",` +
	`"s3_key":"SENTINEL-S3KEY","s3_key_secret":"SENTINEL-S3SECRET",` +
	`"retention_full":2,"retention_full_type":"count",` +
	`"custom_options":{"x":"SENTINEL-CUSTOM"}},` +
	`{"id":"local","type":"posix","base_path":"/mnt/backups"},` +
	`{"id":"az","type":"azure","azure_account":"acct",` +
	`"azure_container":"ctr","azure_key":"SENTINEL-AZKEY"},` +
	`{"id":"g","type":"gcs","gcs_bucket":"gbkt","gcs_key":"SENTINEL-GCSKEY"}],` +
	`"schedules":[{"id":"nightly","type":"full",` +
	`"cron_expression":"0 2 * * *"}]},` +
	`"restore_config":{"source_database_id":"olddb",` +
	`"source_database_name":"olddbname","source_node_name":"n1",` +
	`"repository":{"id":"primary","type":"s3","s3_bucket":"bkt",` +
	`"s3_key_secret":"SENTINEL-RESTORE"}}},` +
	`"service_instances":[{"service_instance_id":"si-1",` +
	`"database_id":"storefront","service_id":"mcp","host_id":"host-a",` +
	`"state":"available","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"status":{"service_ready":true,"image_version":"1.2.3",` +
	`"health_check":{"status":"healthy","checked_at":"2025-06-18T00:00:00Z"},` +
	`"addresses":["10.0.0.9"],` +
	`"ports":[{"name":"http","host_port":8080,"container_port":80},` +
	`{"name":"metrics","container_port":9090}]}},` +
	`{"service_instance_id":"si-2","database_id":"storefront",` +
	`"service_id":"rag","host_id":"host-b","state":"creating",` +
	`"created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z"}]}`

func TestDatabaseGetRendersSpecSections(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, databaseSpecBody))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "storefront"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	s := out.String()

	t.Run("details block", func(t *testing.T) {
		det := section(t, s, "Details")
		for _, want := range []string{"TENANT", "tenant-9",
			"DATABASE NAME", "shopdb", "POSTGRES", "16.14", "SPOCK",
			"CPUS", "2", "MEMORY", "4Gi", "PORT", "5432", "PATRONI PORT"} {
			if !strings.Contains(det, want) {
				t.Errorf("Details missing %q:\n%s", want, det)
			}
		}
		pp := strings.Fields(rowStartingWith(t, det, "PATRONI"))
		if pp[len(pp)-1] != "-" {
			t.Errorf("unset patroni port should render -: %v", pp)
		}
	})

	t.Run("nodes with overrides and inheritance", func(t *testing.T) {
		nodes := section(t, s, "Nodes")
		n1 := rowStartingWith(t, nodes, "n1")
		n2 := rowStartingWith(t, nodes, "n2")
		if !strings.Contains(n1, "host-a, host-b") ||
			!strings.Contains(n1, "6432") || !strings.Contains(n1, "500m") {
			t.Errorf("n1 overrides missing: %q", n1)
		}
		// n2 inherits everything and names a source node.
		cells := strings.Fields(n2)
		if len(cells) != 8 || cells[len(cells)-1] != "n1" {
			t.Errorf("n2 row = %v, want 8 cells ending in source n1", cells)
		}
		for _, c := range cells[2:7] {
			if c != "-" {
				t.Errorf("n2 inherited cell %q should be -: %v", c, cells)
			}
		}
	})

	t.Run("users", func(t *testing.T) {
		users := section(t, s, "Users")
		app := rowStartingWith(t, users, "app")
		if !strings.Contains(app, "yes") ||
			!strings.Contains(app, "pgedge_application") ||
			!strings.Contains(app, "LOGIN") {
			t.Errorf("app row = %q", app)
		}
		ro := strings.Fields(rowStartingWith(t, users, "ro"))
		if len(ro) != 4 || ro[1] != "no" || ro[2] != "-" || ro[3] != "-" {
			t.Errorf("ro row = %v, want [ro no - -]", ro)
		}
	})

	t.Run("configured services", func(t *testing.T) {
		cs := section(t, s, "Configured services")
		for _, want := range []string{"mcp", "latest", "host-a", "8080",
			"app"} {
			if !strings.Contains(cs, want) {
				t.Errorf("missing %q:\n%s", want, cs)
			}
		}
	})

	t.Run("running services carry status", func(t *testing.T) {
		svcs := section(t, s, "Services")
		mcp := rowStartingWith(t, svcs, "mcp")
		for _, want := range []string{"yes", "healthy", "1.2.3",
			"10.0.0.9", "http:8080", "metrics:9090"} {
			if !strings.Contains(mcp, want) {
				t.Errorf("mcp row missing %q: %q", want, mcp)
			}
		}
		rag := strings.Fields(rowStartingWith(t, svcs, "rag"))
		if len(rag) != 8 {
			t.Fatalf("rag row = %v, want 8 cells", rag)
		}
		for _, c := range rag[3:] {
			if c != "-" {
				t.Errorf("rag has no status; cell %q should be -", c)
			}
		}
	})

	t.Run("backup repositories locate by type", func(t *testing.T) {
		repos := section(t, s, "Backup repositories")
		for _, want := range []string{
			"bkt (us-east-1) /pg", "2", "count",
			"/mnt/backups", "acct/ctr", "gbkt",
		} {
			if !strings.Contains(repos, want) {
				t.Errorf("missing %q:\n%s", want, repos)
			}
		}
		sched := section(t, s, "Backup schedules")
		if !strings.Contains(sched, "nightly") ||
			!strings.Contains(sched, "0 2 * * *") {
			t.Errorf("schedules:\n%s", sched)
		}
	})

	t.Run("restore block", func(t *testing.T) {
		r := section(t, s, "Restore")
		for _, want := range []string{"olddb", "olddbname", "n1",
			"primary", "s3", "bkt"} {
			if !strings.Contains(r, want) {
				t.Errorf("missing %q:\n%s", want, r)
			}
		}
	})

	t.Run("no upgrades section unless asked", func(t *testing.T) {
		if strings.Contains(s, "Available upgrades") {
			t.Errorf("upgrades rendered without --upgrades:\n%s", s)
		}
	})

	// The reason the fixture carries sentinels at all.
	t.Run("never prints a credential", func(t *testing.T) {
		if strings.Contains(s, "SENTINEL") {
			t.Errorf("a credential sentinel reached text output:\n%s", s)
		}
		for _, word := range []string{"password", "api_key", "s3_key",
			"azure_key", "gcs_key", "custom_options"} {
			if strings.Contains(strings.ToLower(s), word) {
				t.Errorf("text output mentions %q:\n%s", word, s)
			}
		}
	})

	t.Run("json still carries the spec verbatim", func(t *testing.T) {
		rt, out, _ := newTestRuntime(t, "", "json")
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront"); err != nil {
			t.Fatalf("json: %v", err)
		}
		if !strings.Contains(out.String(), `"s3_bucket":"bkt"`) {
			t.Errorf("json lost the spec: %s", out.String())
		}
	})
}

// A database with no spec prints the summary and nothing else, and a
// spec with only the required fields prints Details and Nodes only.
func TestDatabaseGetOmitsEmptySpecSections(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, databaseBody))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "storefront"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	for _, h := range []string{"Details", "Nodes", "Users",
		"Configured services", "Backup", "Restore", "Available upgrades"} {
		if strings.Contains(out.String(), h) {
			t.Errorf("section %q on a spec-less database:\n%s", h,
				out.String())
		}
	}

	rt, out, _ = newTestRuntime(t, "", "text")
	url = newServer(t, jsonHandler(200, `{"id":"d","state":"available",`+
		`"created_at":"2025-06-18T00:00:00Z",`+
		`"updated_at":"2025-06-18T00:00:00Z",`+
		`"spec":{"database_name":"d","nodes":[{"name":"n1",`+
		`"host_ids":["h"]}]}}`))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "d"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Details") || !strings.Contains(s, "Nodes") {
		t.Errorf("Details/Nodes missing:\n%s", s)
	}
	for _, h := range []string{"Users", "Configured services", "Backup",
		"Restore"} {
		if strings.Contains(s, h) {
			t.Errorf("section %q with nothing to show:\n%s", h, s)
		}
	}
}

func TestDatabaseGetUpgrades(t *testing.T) {
	// --upgrades is the only thing that sends include=available_upgrades;
	// without it the query string is empty.
	var queries []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		body := databaseBody
		if strings.Contains(r.URL.RawQuery, "available_upgrades") {
			body = strings.TrimSuffix(databaseBody, "}") +
				`,"available_upgrades":[{"image":"pgedge/pgedge:16.15-1",` +
				`"postgres_version":"16.15","spock_version":"5"}]}`
		}
		_, _ = w.Write([]byte(body))
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	rt, out, _ := newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, srv.URL,
		"database", "get", "storefront"); err != nil {
		t.Fatalf("get: %v", err)
	}
	rt, out, _ = newTestRuntime(t, "", "text")
	if err := runControlplane(t, rt, out, srv.URL,
		"database", "get", "storefront", "--upgrades"); err != nil {
		t.Fatalf("get --upgrades: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("control failed: %d requests, want 2", len(queries))
	}
	if queries[0] != "" {
		t.Errorf("plain get sent a query: %q", queries[0])
	}
	if !strings.Contains(queries[1], "include=available_upgrades") {
		t.Errorf("--upgrades did not ask for them: %q", queries[1])
	}
	up := section(t, out.String(), "Available upgrades")
	for _, want := range []string{"IMAGE", "PG", "SPOCK",
		"pgedge/pgedge:16.15-1", "16.15"} {
		if !strings.Contains(up, want) {
			t.Errorf("missing %q:\n%s", want, up)
		}
	}

	// Asked, and none: a sentence on stderr, no header on stdout.
	t.Run("none available", func(t *testing.T) {
		url := newServer(t, jsonHandler(200,
			strings.TrimSuffix(databaseBody, "}")+`,"available_upgrades":[]}`))
		rt, out, stderr := newTestRuntime(t, "", "text")
		if err := runControlplane(t, rt, out, url,
			"database", "get", "storefront", "--upgrades"); err != nil {
			t.Fatalf("get --upgrades: %v", err)
		}
		if strings.Contains(out.String(), "Available upgrades") {
			t.Errorf("empty section rendered:\n%s", out.String())
		}
		if !strings.Contains(stderr.String(), "No upgrades available") {
			t.Errorf("stderr = %q", stderr.String())
		}
	})
}

// rowStartingWith returns the line of a section whose first cell is
// the given value.
func rowStartingWith(t *testing.T, sec, first string) string {
	t.Helper()
	for _, line := range strings.Split(sec, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == first {
			return line
		}
	}
	t.Fatalf("no row starting with %q in:\n%s", first, sec)
	return ""
}
