// Package inspect runs read-only diagnostic queries against a Postgres
// database and renders the rows. It is the one place the CLI connects
// to Postgres rather than to a pgEdge API (#431).
//
// Every query is a literal with no interpolated input, reads only
// catalog and statistics views, and writes nothing. The SQL follows
// the PgHero-derived inspect queries other Postgres CLIs ship,
// adapted to plain Postgres; each analysis that follows one names it.
package inspect

// Analysis is one diagnostic: the columns it returns, in order, and
// the statement that produces them.
type Analysis struct {
	Name string
	// Short is the one-line description shown in the vocabulary list.
	Short string
	// Extension is a required extension, or "" when catalog views
	// suffice. Its absence is reported by name rather than as the
	// driver's "relation does not exist".
	Extension string
	// NeedsStats marks an analysis that reads other sessions' rows in
	// pg_stat_activity or pg_stat_statements. Postgres nulls those
	// columns for a role without pg_read_all_stats, so a filter on
	// them drops every row and the analysis answers empty. The
	// managed verb connects as admin for these unless told otherwise.
	NeedsStats bool
	Columns    []string
	SQL        string
}

// Analyses is the closed vocabulary, in the order the help lists it.
var Analyses = []Analysis{
	{
		Name:    "table-sizes",
		Short:   "Tables by total size, largest first",
		Columns: []string{"schema", "table", "size", "table_size", "index_size"},
		// Source: PgHero-derived table-stats and table-sizes.
		SQL: `SELECT n.nspname AS schema, c.relname AS table,
  pg_size_pretty(pg_total_relation_size(c.oid)) AS size,
  pg_size_pretty(pg_table_size(c.oid)) AS table_size,
  pg_size_pretty(pg_indexes_size(c.oid)) AS index_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'm', 'p')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY pg_total_relation_size(c.oid) DESC
LIMIT 50`,
	},
	{
		Name:    "index-sizes",
		Short:   "Indexes by size, largest first",
		Columns: []string{"schema", "table", "index", "size"},
		// Source: PgHero-derived index-sizes.
		SQL: `SELECT n.nspname AS schema, t.relname AS table,
  i.relname AS index, pg_size_pretty(pg_relation_size(i.oid)) AS size
FROM pg_index x
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = i.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY pg_relation_size(i.oid) DESC
LIMIT 50`,
	},
	{
		Name:    "unused-indexes",
		Short:   "Non-unique indexes scanned fewer than 50 times",
		Columns: []string{"schema", "table", "index", "size", "scans"},
		// Source: PgHero-derived unused-indexes (idx_scan < 50, non-unique).
		SQL: `SELECT s.schemaname AS schema, s.relname AS table,
  s.indexrelname AS index,
  pg_size_pretty(pg_relation_size(s.indexrelid)) AS size,
  s.idx_scan AS scans
FROM pg_stat_user_indexes s
JOIN pg_index x ON x.indexrelid = s.indexrelid
WHERE NOT x.indisunique AND s.idx_scan < 50
ORDER BY pg_relation_size(s.indexrelid) DESC
LIMIT 50`,
	},
	{
		Name:    "seq-scans",
		Short:   "Tables by sequential scan count",
		Columns: []string{"schema", "table", "seq_scans", "seq_rows_read", "index_scans"},
		// Source: PgHero-derived seq-scans.
		SQL: `SELECT schemaname AS schema, relname AS table,
  seq_scan AS seq_scans, seq_tup_read AS seq_rows_read,
  COALESCE(idx_scan, 0) AS index_scans
FROM pg_stat_user_tables
ORDER BY seq_scan DESC
LIMIT 50`,
	},
	{
		Name:       "long-running-queries",
		Short:      "Active queries running longer than five minutes",
		NeedsStats: true,
		Columns:    []string{"pid", "duration", "user", "state", "query"},
		// Source: PgHero-derived long-running-queries (> 5 min).
		SQL: `SELECT pid, (now() - query_start)::text AS duration,
  usename AS user, state, query
FROM pg_stat_activity
WHERE state <> 'idle' AND pid <> pg_backend_pid()
  AND query_start < now() - interval '5 minutes'
ORDER BY query_start`,
	},
	{
		Name:       "locks",
		Short:      "Sessions waiting on a lock, and who holds it",
		NeedsStats: true,
		Columns:    []string{"blocked_pid", "blocked_by", "blocked_duration", "blocked_query"},
		// Source: PgHero-derived blocking and stalled-queries, via
		// pg_blocking_pids().
		SQL: `SELECT a.pid AS blocked_pid,
  array_to_string(pg_blocking_pids(a.pid), ',') AS blocked_by,
  (now() - a.query_start)::text AS blocked_duration,
  a.query AS blocked_query
FROM pg_stat_activity a
WHERE cardinality(pg_blocking_pids(a.pid)) > 0
ORDER BY a.query_start`,
	},
	{
		Name:    "vacuum-stats",
		Short:   "Dead rows and last vacuum per table",
		Columns: []string{"schema", "table", "live_rows", "dead_rows", "last_vacuum", "last_autovacuum"},
		// Source: PgHero-derived vacuum-stats; postgres-mcp
		// vacuum_health_calc.py.
		SQL: `SELECT schemaname AS schema, relname AS table,
  n_live_tup AS live_rows, n_dead_tup AS dead_rows,
  COALESCE(last_vacuum::text, '') AS last_vacuum,
  COALESCE(last_autovacuum::text, '') AS last_autovacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC
LIMIT 50`,
	},
	{
		Name:    "bloat",
		Short:   "Estimated table bloat from planner statistics",
		Columns: []string{"schema", "table", "bloat_estimate", "waste"},
		// Source: the PgHero bloat estimate. It is an
		// estimate from pg_stats, not a measurement, and the column
		// name says so.
		SQL: `WITH constants AS (
  SELECT current_setting('block_size')::numeric AS bs, 23 AS hdr, 4 AS ma
), bloat_info AS (
  SELECT ma, bs, schemaname, tablename,
    (datawidth + (hdr + ma - (CASE WHEN hdr % ma = 0 THEN ma ELSE hdr % ma END)))::numeric AS datahdr,
    (maxfracsum * (nullhdr + ma - (CASE WHEN nullhdr % ma = 0 THEN ma ELSE nullhdr % ma END))) AS nullhdr2
  FROM (
    SELECT schemaname, tablename, hdr, ma, bs,
      SUM((1 - null_frac) * avg_width) AS datawidth,
      MAX(null_frac) AS maxfracsum,
      hdr + (SELECT 1 + count(*) / 8 FROM pg_stats s2
             WHERE null_frac <> 0 AND s2.schemaname = s.schemaname
               AND s2.tablename = s.tablename) AS nullhdr
    FROM pg_stats s, constants
    GROUP BY 1, 2, 3, 4, 5
  ) AS foo
), table_bloat AS (
  SELECT schemaname, tablename, cc.relpages, bs,
    CEIL((cc.reltuples * ((datahdr + ma -
      (CASE WHEN datahdr % ma = 0 THEN ma ELSE datahdr % ma END)) + nullhdr2 + 4)) / (bs - 20::float)) AS otta
  FROM bloat_info
  JOIN pg_class cc ON cc.relname = bloat_info.tablename
  JOIN pg_namespace nn ON cc.relnamespace = nn.oid
    AND nn.nspname = bloat_info.schemaname
    AND nn.nspname NOT IN ('pg_catalog', 'information_schema')
)
SELECT schemaname AS schema, tablename AS table,
  ROUND(CASE WHEN otta = 0 THEN 0.0 ELSE relpages / otta::numeric END, 1) AS bloat_estimate,
  pg_size_pretty((CASE WHEN relpages < otta THEN 0 ELSE bs * (relpages - otta)::bigint END)::bigint) AS waste
FROM table_bloat
WHERE relpages > 10
ORDER BY (CASE WHEN relpages < otta THEN 0 ELSE bs * (relpages - otta)::bigint END) DESC
LIMIT 50`,
	},
	{
		Name:       "calls",
		Short:      "Statements by call count (needs pg_stat_statements)",
		Extension:  "pg_stat_statements",
		NeedsStats: true,
		Columns:    []string{"calls", "total_time", "mean_time", "query"},
		// Source: PgHero-derived calls.
		SQL: `SELECT calls, ROUND(total_exec_time::numeric, 1)::text AS total_time,
  ROUND(mean_exec_time::numeric, 1)::text AS mean_time, query
FROM pg_stat_statements
ORDER BY calls DESC
LIMIT 20`,
	},
	{
		Name:       "outliers",
		Short:      "Statements by total execution time (needs pg_stat_statements)",
		Extension:  "pg_stat_statements",
		NeedsStats: true,
		Columns:    []string{"total_time", "calls", "mean_time", "query"},
		// Source: PgHero-derived outliers.
		SQL: `SELECT ROUND(total_exec_time::numeric, 1)::text AS total_time, calls,
  ROUND(mean_exec_time::numeric, 1)::text AS mean_time, query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20`,
	},
	{
		Name:    "replication-slots",
		Short:   "Replication slots, and the WAL each one retains",
		Columns: []string{"slot", "type", "plugin", "database", "active", "wal_status", "retained_wal"},
		// Source: PgHero replication_slots. retained_wal is the WAL the
		// slot holds against the write position, which is what fills
		// a disk when a slot stops being consumed; the CASE reads the
		// standby's replay position instead, because
		// pg_current_wal_lsn() raises on a database in recovery.
		SQL: `SELECT slot_name AS slot, slot_type AS type, plugin, database, active::text AS active,
  wal_status, pg_size_pretty(pg_wal_lsn_diff(
    CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn()
      ELSE pg_current_wal_lsn() END, restart_lsn)) AS retained_wal
FROM pg_replication_slots
ORDER BY slot_name`,
	},
	{
		Name:       "replication-lag",
		Short:      "Connected replicas and subscribers, and their lag",
		NeedsStats: true,
		Columns: []string{"client", "application", "state", "sent_lsn",
			"replay_lsn", "write_lag", "flush_lag", "replay_lag", "sync_state"},
		// Source: PgHero replication_lag.
		// NeedsStats because pg_stat_replication degrades rather than
		// empties: measured on 18.6, a role without pg_read_all_stats
		// gets its rows with every column blank but application, which
		// reads as a broken query rather than as a missing privilege.
		SQL: `SELECT COALESCE(host(client_addr), '') AS client, application_name AS application,
  state, sent_lsn::text AS sent_lsn, replay_lsn::text AS replay_lsn,
  write_lag::text AS write_lag, flush_lag::text AS flush_lag,
  replay_lag::text AS replay_lag, sync_state
FROM pg_stat_replication
ORDER BY application_name`,
	},
	{
		Name:  "subscriptions",
		Short: "Logical replication subscriptions and their progress",
		Columns: []string{"subscription", "pid", "received_lsn", "last_msg_sent",
			"last_msg_received", "latest_end_lsn"},
		// pg_stat_subscription reads the native pg_subscription
		// catalog, so it answers for a database subscribing with
		// CREATE SUBSCRIPTION and is empty on one replicating another
		// way. Measured on 18.6: full for a role without
		// pg_read_all_stats, so no NeedsStats.
		SQL: `SELECT subname AS subscription, pid::text AS pid,
  received_lsn::text AS received_lsn,
  last_msg_send_time::text AS last_msg_sent,
  last_msg_receipt_time::text AS last_msg_received,
  latest_end_lsn::text AS latest_end_lsn
FROM pg_stat_subscription
ORDER BY subname`,
	},
}

// Lookup returns the analysis named, or false.
func Lookup(name string) (Analysis, bool) {
	for _, a := range Analyses {
		if a.Name == name {
			return a, true
		}
	}
	return Analysis{}, false
}

// Names returns the vocabulary in help order.
func Names() []string {
	out := make([]string, len(Analyses))
	for i, a := range Analyses {
		out[i] = a.Name
	}
	return out
}
