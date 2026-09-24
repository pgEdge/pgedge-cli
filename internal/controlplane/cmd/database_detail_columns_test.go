package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// Every cell in this body is a single distinct token, so a value under
// the wrong header is unambiguous. Each column slice and its Columns()
// (or cellsRow literal) are two hand-written lists nothing forces to
// agree; swapping two adjacent values, or dropping a header, compiles
// and prints a plausible table. The review of #458 landed three such
// mutations unnoticed, which is what these tests now catch.
const databaseColumnsBody = `{"id":"storefront","state":"available",` +
	`"tenant_id":"tenant-9","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-19T00:00:00Z",` +
	`"spec":{"database_name":"shopdb","postgres_version":"16.14",` +
	`"spock_version":"5","cpus":"3","memory":"4Gi","port":5432,` +
	`"patroni_port":8008,` +
	`"nodes":[{"name":"n1","host_ids":["host-a"],"postgres_version":"16.9",` +
	`"port":6432,"patroni_port":8009,"cpus":"500m","memory":"2Gi",` +
	`"source_node":"n0"}],` +
	`"database_users":[{"username":"app","db_owner":true,` +
	`"roles":["role-x"],"attributes":["LOGIN"]}],` +
	`"services":[{"service_id":"svc-1","service_type":"postgrest",` +
	`"version":"12.2","host_ids":["host-b"],"port":3001,` +
	`"connect_as":"app"}],` +
	`"backup_config":{"repositories":[{"id":"repo-1","type":"gcs",` +
	`"gcs_bucket":"gbkt","retention_full":7,"retention_full_type":"time"}],` +
	`"schedules":[{"id":"sched-1","type":"incr",` +
	`"cron_expression":"@hourly"}]},` +
	`"restore_config":{"source_database_id":"src-id",` +
	`"source_database_name":"src-name","source_node_name":"src-node",` +
	`"repository":{"id":"repo-r","type":"posix","base_path":"/bk"}}},` +
	`"service_instances":[{"service_instance_id":"si-1",` +
	`"database_id":"storefront","service_id":"svc-1","host_id":"host-b",` +
	`"state":"running","created_at":"2025-06-18T00:00:00Z",` +
	`"updated_at":"2025-06-18T00:00:00Z",` +
	`"status":{"service_ready":true,"image_version":"img:1.2.3",` +
	`"health_check":{"status":"healthy","checked_at":"2025-06-18T00:00:00Z"},` +
	`"addresses":["10.0.0.9"],` +
	`"ports":[{"name":"http","host_port":8080,"container_port":80}]}}],` +
	`"available_upgrades":[{"image":"img:17.1","postgres_version":"17.1",` +
	`"spock_version":"6"}]}`

var cellGap = regexp.MustCompile(`\s{2,}`)

// splitCells splits one tabwriter line at its column gaps, so a header
// like PATRONI PORT stays one cell.
func splitCells(line string) []string {
	return cellGap.Split(strings.TrimSpace(line), -1)
}

// assertColumns checks a one-row section: the header line is exactly
// the declared columns, the row has as many cells, and every wanted
// value sits under its named header.
func assertColumns(
	t *testing.T, sec string, columns []string, want map[string]string,
) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(sec), "\n")
	if len(lines) != 3 {
		t.Fatalf("want title, header and one row, got %d lines:\n%s",
			len(lines), sec)
	}
	header, row := splitCells(lines[1]), splitCells(lines[2])
	if strings.Join(header, "|") != strings.Join(columns, "|") {
		t.Fatalf("header %v, declared %v", header, columns)
	}
	if len(row) != len(columns) {
		t.Fatalf("row has %d cells under %d headers: %v", len(row),
			len(columns), row)
	}
	if len(want) != len(columns) {
		t.Fatalf("test covers %d columns, table has %d: %v", len(want),
			len(columns), columns)
	}
	for name, wantVal := range want {
		idx := -1
		for i, h := range header {
			if h == name {
				idx = i
			}
		}
		if idx == -1 {
			t.Fatalf("no %q column in %v", name, header)
		}
		if row[idx] != wantVal {
			t.Errorf("column %s = %q, want %q", name, row[idx], wantVal)
		}
	}
}

// assertFields checks a FIELD/VALUE block: exactly the wanted fields,
// in order, each with its value.
func assertFields(t *testing.T, sec string, want [][2]string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(sec), "\n")
	if got := splitCells(lines[1]); strings.Join(got, "|") != "FIELD|VALUE" {
		t.Fatalf("header %v", got)
	}
	rows := lines[2:]
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d:\n%s", len(rows), len(want), sec)
	}
	for i, w := range want {
		cells := splitCells(rows[i])
		if len(cells) != 2 || cells[0] != w[0] || cells[1] != w[1] {
			t.Errorf("row %d = %v, want %v", i, cells, w)
		}
	}
}

func TestDatabaseGetDetailColumnPositions(t *testing.T) {
	rt, out, _ := newTestRuntime(t, "", "text")
	url := newServer(t, jsonHandler(200, databaseColumnsBody))
	if err := runControlplane(t, rt, out, url,
		"database", "get", "storefront", "--upgrades"); err != nil {
		t.Fatalf("database get: %v", err)
	}
	s := out.String()

	assertColumns(t, section(t, s, "Services"), serviceColumns,
		map[string]string{
			"SERVICE": "svc-1", "STATE": "running", "HOST": "host-b",
			"READY": "yes", "HEALTH": "healthy", "IMAGE": "img:1.2.3",
			"ADDRESSES": "10.0.0.9", "PORTS": "http:8080",
		})
	assertFields(t, section(t, s, "Details"), [][2]string{
		{"TENANT", "tenant-9"}, {"DATABASE NAME", "shopdb"},
		{"POSTGRES", "16.14"}, {"SPOCK", "5"}, {"CPUS", "3"},
		{"MEMORY", "4Gi"}, {"PORT", "5432"}, {"PATRONI PORT", "8008"},
	})
	assertColumns(t, section(t, s, "Nodes"), nodeSpecColumns,
		map[string]string{
			"NODE": "n1", "HOSTS": "host-a", "PG": "16.9", "PORT": "6432",
			"PATRONI PORT": "8009", "CPUS": "500m", "MEMORY": "2Gi",
			"SOURCE": "n0",
		})
	assertColumns(t, section(t, s, "Users"), userColumns,
		map[string]string{
			"USERNAME": "app", "OWNER": "yes", "ROLES": "role-x",
			"ATTRIBUTES": "LOGIN",
		})
	assertColumns(t, section(t, s, "Configured services"),
		serviceSpecColumns, map[string]string{
			"SERVICE": "svc-1", "TYPE": "postgrest", "VERSION": "12.2",
			"HOSTS": "host-b", "PORT": "3001", "CONNECT AS": "app",
		})
	assertColumns(t, section(t, s, "Backup repositories"),
		repositoryColumns, map[string]string{
			"REPOSITORY": "repo-1", "TYPE": "gcs", "LOCATION": "gbkt",
			"RETENTION": "7", "RETENTION TYPE": "time",
		})
	assertColumns(t, section(t, s, "Backup schedules"), scheduleColumns,
		map[string]string{
			"SCHEDULE": "sched-1", "TYPE": "incr", "CRON": "@hourly",
		})
	assertFields(t, section(t, s, "Restore"), [][2]string{
		{"SOURCE DATABASE", "src-id"}, {"SOURCE DATABASE NAME", "src-name"},
		{"SOURCE NODE", "src-node"}, {"REPOSITORY", "repo-r"},
		{"REPOSITORY TYPE", "posix"}, {"LOCATION", "/bk"},
	})
	assertColumns(t, section(t, s, "Available upgrades"), upgradeColumns,
		map[string]string{
			"IMAGE": "img:17.1", "PG": "17.1", "SPOCK": "6",
		})
}
