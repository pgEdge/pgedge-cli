# Logs and metrics on BYOC

Two observability surfaces sit behind a BYOC database: a metrics
series the platform collects, and the log the database writes. The
cluster underneath carries its own host metrics and its nodes'
journald logs.

Every command here is a read. Nothing below writes, prompts or starts
a task.

## Host metrics on a BYOC cluster

`pgedge starfleet byoc cluster metrics <cluster_id>` reads the host
metrics collected for a cluster's nodes. The response is an object
keyed by resource, covering cpu, disk, memory, network_recv,
network_sent, procs and status, and each resource carries a current
value per node plus the time series behind that value. The
[BYOC clusters guide](clusters.md) covers building and updating the
cluster itself.

Text output is a five-column table, METRIC, NODE, VALUE, UNIT and
SAMPLES, with one row per node per resource. The SAMPLES column counts
the series rather than showing it. Use `-o json` for the samples
themselves, which arrive as timestamp and value pairs with the
timestamp in seconds. A resource carrying no items still gets a row,
with `-` for node and value, because on a healthy cluster `status`
arrives with no items and dropping the row would read as though the
cluster reported no status at all.

Both `--start-time` and `--end-time` take RFC3339 timestamps, and the
API refuses any other format with `400 failed to parse start_time`.
Values print as the API sends them, neither rounded nor rescaled. The
following command reads a one-hour window:

    pgedge starfleet byoc cluster metrics <cluster-id> \
        --start-time 2026-08-04T00:00:00Z \
        --end-time 2026-08-04T01:00:00Z

## Database metrics on BYOC

BYOC databases carry their own metrics command alongside the cluster
one. `pgedge starfleet byoc database metrics <database_id>` returns a
series of around 80 columns, reaching from database size and table
counts through container CPU and memory to the Spock subscription
counters. The table is METRIC and VALUE, and this command shows the
newest full-length sample.

Narrow the result with `--columns`, restrict it to one node with
`--node-name`, and set how far back to read with `--interval`, which
defaults to 15 minutes and takes `value,unit` with second, minute,
hour, day, week, month or year as the unit. A malformed or zero
interval is exit 2 before the request. The CLI does not check
`--columns`, so a column the API cannot use reaches the API, which
answers `500 failed to read metrics` rather than 400. An unknown
`--node-name` answers 200 with a null series, reported as
`No metrics found.` The sample
timestamps here are epoch milliseconds, not the seconds `cluster
metrics` reports, and nothing in the CLI reinterprets either.

An empty series prints `No metrics found.` on stderr and exits 0, and
the command prints no note about a partly scraped trailing sample.

## Journald logs on a BYOC node

`pgedge starfleet byoc node logs <cluster_id> <node> <log_name>` reads
one journald log from a single node. The cluster argument is a full
UUID. The node argument takes either a full node UUID, used directly,
or a node name as shown by `node list`, which costs an extra call to
look up. The log path needs a UUID and `cluster get` reports none,
which is why the lookup exists.

The log name is a journald selector and the API does not validate the
value. Almost every name answers 200, and an unrecognized one returns
the same `-- No entries --` an idle log returns, printed to stdout
unrewritten like any other line, so nothing tells a typo from an idle
log. `postgres` is the one exception, answering 500. The following
table describes what each log name returns:

| Log name | Result |
|---|---|
| `system`, `docker`, `containerd` | Real entries |
| `postgresql`, `pgedge`, `patroni`, `messages` | `-- No entries --` |
| `postgres` | `500 failed to read log` |

For the Postgres log itself, use `database logs` rather than this
command.

Not every filter works. The following table describes each:

| Flag | State |
|---|---|
| `--priority` | Works: selects one journald priority, such as `err` |
| `--reverse` | Works: returns the newest entries first |
| `--dmesg` | Works: returns kernel entries only |
| `--lines` | Caps the entries returned, per its help |
| `--grep` | Refused server-side with `500 failed to read log` |
| `--case-sensitive` | Modifies `--grep`, so refused with it |
| `--since` | Refused server-side with `500 failed to read log` |
| `--until` | Refused server-side with `500 failed to read log` |

The three refused filters fail even against a log that returns entries
without them, which makes this an API-side fault rather than an
argument problem. The CLI sends all of them unchanged. `--since` and
`--until` take RFC3339 timestamps, and a bare date is refused with
`400 failed to parse since`. Filters travel only when you set them.
The following command asks for the last 200 docker entries, newest
first:

    pgedge starfleet byoc node logs <cluster-id> n1 docker \
        --lines 200 --reverse

Text output prints each entry's raw text one per line and skips every
entry whose raw text is empty, which is how the blank element the API
appends never reaches you. A response that is empty, or whose entries
are all blank, prints `No log entries found.` on stderr at exit 0.
Structured output also carries `level`, `message` and `time`, but the
API leaves all three empty, so only `raw_text` holds anything.

## Postgres logs on a BYOC database

`pgedge starfleet byoc database logs <database_id>` reads log lines
from a component running on a database's nodes. Both `--component-name`
and `--nodes` are required, and the CLI enforces that before making a
request, because the API answers 400 when either is missing.
`--component-name` names the component, and `postgres` is the one every
database runs. `--nodes` takes a comma-separated list of node names as
shown by `node list`. `--max-lines` caps the lines returned, and the
API defaults that cap to 100.

The following command reads two nodes at once:

    pgedge starfleet byoc database logs <database-id> \
        --component-name postgres --nodes n1,n2 --max-lines 500

The API validates neither name. An unknown component and an unknown
node both answer 200 with no log blocks, identical to a real component
that has logged nothing, so `No logs found.` cannot tell you which of
the three happened.

The managed response has a different shape, so a script written
against this one does not read it.

Text output prints each node's lines verbatim under an `==> node <==`
header, so the stream pipes into `grep` unchanged. Choose `-o json` or
`-o yaml` for the blocks structured, one per node.

The API states no ordering for the lines. A timestamp sits inside the
line text and the CLI does not parse it.

## Next steps

- The [provision a BYOC cluster workflow](provision.md) covers
  building the cluster and database this page reads from.
- The [troubleshooting page](troubleshooting.md) keys these empty
reads to the symptom a reader arrives with.
- The [BYOC command reference](../reference/starfleet-byoc.md) lists
  every flag on every command named above.
