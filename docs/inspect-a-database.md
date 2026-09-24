# Inspect a database

`database inspect` runs one read-only diagnostic against a database
and prints the rows. It is the one place the CLI connects to Postgres
rather than to a pgEdge API. For a managed or BYOC database the
connection is the same block `connection-string` resolves, so nothing
is typed in, and for a Control Plane database or any other Postgres
the top-level `pgedge inspect` takes the connection string on
`--db-url`. Every analysis reads catalog and statistics views only
and writes nothing.

## The analyses

The following table describes each analysis and what it reads:

| Analysis | What it reads |
|---|---|
| `table-sizes` | Tables by total size, with table and index size apart. |
| `index-sizes` | Indexes by size. |
| `unused-indexes` | Non-unique indexes scanned fewer than 50 times. |
| `seq-scans` | Tables by sequential scan count, beside their index scans. |
| `long-running-queries` | Sessions active for longer than five minutes. |
| `locks` | Sessions waiting on a lock, and the sessions holding it. |
| `vacuum-stats` | Live and dead rows per table, and the last vacuum times. |
| `bloat` | An estimate of table bloat from planner statistics. |
| `calls` | Statements by call count. Needs the pg_stat_statements extension. |
| `outliers` | Statements by total execution time. Needs the pg_stat_statements extension. |
| `replication-slots` | Replication slots, with the WAL each one retains. |
| `replication-lag` | Replicas and subscribers connected, with their positions and lag times. |
| `subscriptions` | Subscription workers, with the positions and message times each reports. |

Every analysis but `calls` and `outliers` needs nothing beyond a
connection, and those two exit 1 naming the extension when the
database does not have it. `bloat` is the estimate PgHero popularised,
computed from the planner's statistics view, so its column says
estimate rather than reporting a measured size. A database that
replicates nothing returns no rows on `replication-slots`,
`replication-lag` and `subscriptions`, which is exit 0 rather than a
failure. No rows from `replication-lag` does not run the other way,
because a replica that stops consuming drops off that analysis while
its slot stays on `replication-slots`.

## Managed

One command runs an analysis against a managed database:

    pgedge starfleet managed database inspect <db-id> table-sizes

`--user-type` chooses the role, admin, app or app_read_only, and app
is the default for most analyses. `long-running-queries`, `locks`, `calls`,
`outliers` and `replication-lag` connect as admin unless you say
otherwise: Postgres nulls other sessions' rows in the statistics views
for a role without the pg_read_all_stats privilege, so as app the
first two answer empty and read as a quiet database, the next two show
"<insufficient privilege>" in place of every query, and
`replication-lag` prints one row per connected replica with every
column blank but application. A row of blanks is not an empty result.
The empty result says no replica is connected, and the row says one is
and the role cannot see its positions. `replication-slots` and
`subscriptions` return their full content to either role.

## BYOC

A BYOC database has one connection block per node, and statistics are
per node too, so the node is chosen the way `connection-string`
chooses it:

    pgedge starfleet byoc database inspect <db-id> locks --node n2

One node needs no flag. Several and no flag lists the nodes at exit 2.
A node on a private cluster carries `internal_host` and no `host`, so pass
`--internal` from inside the cluster's network, or the command exits 1 naming
the flag.

The replication analyses are per node in the same sense. A node's
slots and the replicas connected to it describe what that node sends,
so a picture of the whole database means one run per node.

Spock replicates over Postgres's own logical replication slots and
opens its connections over the replication protocol, so a Spock node's
slots and its connected peers appear in `replication-slots` and
`replication-lag`. Spock keeps its own subscription catalog and never
issues CREATE SUBSCRIPTION, so `subscriptions` answers empty on every
node of a Spock database. Empty there says nothing about whether
replication is working.

## Any other Postgres

The top-level form takes a connection string, which is how a Control
Plane database, or any Postgres the machine can reach, is inspected:

    pgedge inspect unused-indexes --db-url "$DATABASE_URL"

## Connection and timeout failures

A database that refuses the connection is exit 1 with the driver's
message. One that accepts the connection and never answers is exit 3
after 30 seconds, the same code every other expired deadline uses. A
`calls` or `outliers` on a database upgraded in place may fail naming
a missing total execution time column: the pg_stat_statements extension
is still at a version below 1.8 there, and updating the extension
resolves it.

## Output

Text prints a table with the analysis's own columns. `-o json` prints
one object per row keyed by column, in column order, and `-o yaml` the
same shape. Every cell is the server's own text, so a size reads
`415 MB` and a null is an empty string. No rows is exit 0, one
sentence on stderr and, under `-o json`, `[]`.

## Next steps

- The connect an application guides for
  [Managed](managed/connect-an-application.md) and
  [BYOC](byoc/connect-an-application.md) cover the connection block these
  commands resolve.
- The [managed](managed/logs-and-metrics.md) and
  [BYOC](byoc/logs-and-metrics.md) logs and metrics guides cover what
  the platform reports about a database from outside it.
