# Monitoring and alerts for Control Plane databases

You monitor a Control Plane by asking it questions on a timer. Five
reads report fields that change when something is wrong:

- the database record
- the instance records, which carry the Control Plane's own view of
  replication
- the host records
- the task list
- the connection diagnostic, `controlplane doctor`

Each script on this page runs one of those reads, decides whether the
answer is good or bad, and exits 0 or 1. Whatever runs the script on
a timer, such as cron, turns that exit status into an alert, and
[scheduling a poll](../ci.md#scheduling-a-poll) covers running one
unattended. Every script needs `jq`, and needs to reach the Control
Plane, so give it `--base-url` or run it as a user whose profile
carries one. The [configuration guide](../configuration.md) covers
profiles.

Two things this CLI does not do. It never pushes anywhere, so nothing
here sends data to a monitoring system on its own. And it carries no
metrics command and no log command, because the Control Plane serves
no metrics through its API, and each instance's Postgres log is a file
on the host that runs it. To collect those logs, run a log-shipping
agent on the host.

## What the CLI exposes

Every read below prompts for nothing and writes nothing:

| Command | What it reads |
|---|---|
| `pgedge controlplane database list` | Every database with its `state`. |
| `pgedge controlplane database get` | One database: its `state`, and for each instance its `state`, its Postgres role, the state reported by Patroni (the process manager inside each instance), and the status of the Spock subscription it consumes. |
| `pgedge controlplane database instance list` | Every instance in the cluster, or one database's with `--database`, with the same per-instance fields `database get` shows, as a flat list. |
| `pgedge controlplane host list` | Every host with its `state` and the health of its Docker daemon and etcd store. |
| `pgedge controlplane task list` | Tasks newest first with their `status`, and on a failed task the `error` text. |
| `pgedge controlplane doctor` | The Control Plane connection, at exit 0 whatever it finds, so a script reads its fields under `-o json` rather than its status. |
| `pgedge inspect` | One read-only analysis inside a Postgres server the machine can reach, given a connection string. |

A Control Plane database is reached through a connection string you
assemble: `database get -o json` carries each instance's addresses
and port under `connection_info`, and the spec carries the users.
The [incident runbooks](incident-runbooks.md) open with how to build
it, and the [inspect a database guide](../inspect-a-database.md)
covers the analyses and what each one reads.

## Instance state, not database state

A database's own `state` does not change when one of its instances
stops. A two-node database with one instance stopped reads
`available` on `database list` and on the first line of `database
get`, while the Instances table underneath shows the stopped one at
`stopped` with its role, versions, address and port all blank. A
script that reads the database state alone misses a lost node, so
read the instances.

The following script reads every instance of one database and fails
when any of them is in a state the database cannot serve from:

    #!/bin/sh
    set -u

    db_id="$1"

    pgedge controlplane database instance list --database "$db_id" \
        --timeout 30s -o json > instances.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "instance list exited $rc" >&2
        exit "$rc"
    fi

    jq -r '.[] | select(.state != "available")
        | "\(.id) is \(.state)"' instances.json > bad.txt
    if [ -s bad.txt ]
    then
        cat bad.txt >&2
        exit 1
    fi

`instance list -o json` is a bare array of instance objects, so the
filter iterates the document itself rather than a key inside it. An
instance reports one of nine states, and they fall into three groups
a script treats differently:

- `available` is the serving value.
- `creating`, `modifying`, `deleting` and `backing_up` describe work
  in flight. An instance passes through `modifying` during a restart,
  and a `--wait` on `instance restart` returns when the restart task
  completes, which is before the instance is `available` again, so a
  script running right after a restart sees `modifying` for a short
  while.
- `stopped`, `degraded`, `failed` and `unknown` are the values worth
  alerting on. `stopped` is included because a stop is an operator's
  action, and a script that stays quiet through one cannot tell an
  intended stop from an unintended one.

The script above alerts on the in-flight group too, because it treats
anything but `available` as a failure. When your schedule is likely
to land during maintenance, let those four pass by replacing the
filter with this one:

    jq -r '.[] | select(.state != "available"
        and .state != "creating" and .state != "modifying"
        and .state != "deleting" and .state != "backing_up")
        | "\(.id) is \(.state)"' instances.json > bad.txt

The database record has its own vocabulary, and a script over
`database list` catches what the instances cannot, such as a create
that failed before any instance existed. The following script fails
when any database in the cluster is `failed`, `degraded` or
`unknown`:

    #!/bin/sh
    set -u

    pgedge controlplane database list --timeout 30s -o json > dbs.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "database list exited $rc" >&2
        exit "$rc"
    fi

    jq -r '.databases[] | select(.state == "failed"
        or .state == "degraded" or .state == "unknown")
        | "\(.id) is \(.state)"' dbs.json > bad.txt
    if [ -s bad.txt ]
    then
        cat bad.txt >&2
        exit 1
    fi

`database list -o json` wraps its rows in a `databases` key, and a
row's `state` there is one of `creating`, `modifying`, `available`,
`deleting`, `degraded`, `failed`, `backing_up`, `restoring` and
`unknown`. A script reading `database get` instead never sees
`backing_up`, which that command does not publish. A create that
fails after the server accepted it leaves the database at `failed`,
and the database stays there until it is deleted.

## Replication health from the Control Plane

Each instance record carries a `spock` block, and inside it a
`subscriptions` list with one entry per subscription the instance
consumes, naming the `provider_node` it pulls from and a `status`.
While the provider is up the status reads `replicating`. When the
provider's instance is stopped, the status on the surviving instance
reads `down`, and the stopped instance reports no subscriptions of
its own. A value other than those two is possible, so treat anything
other than `replicating` as a fault.

The following script fails when any subscription on any instance of
one database is not replicating:

    #!/bin/sh
    set -u

    db_id="$1"

    pgedge controlplane database instance list --database "$db_id" \
        --timeout 30s -o json > instances.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "instance list exited $rc" >&2
        exit "$rc"
    fi

    jq -r '.[] | . as $i | (.spock.subscriptions // [])[]
        | select(.status != "replicating")
        | "\($i.id) \(.name) from \(.provider_node) is \(.status)"' \
        instances.json > bad.txt
    if [ -s bad.txt ]
    then
        cat bad.txt >&2
        exit 1
    fi

The filter keeps each instance in `$i` so the message can name it,
and `// []` stands in an empty list for an instance that reports no
subscriptions, so a stopped instance produces no error of its own
here. Its state is what the first script catches.

This is a verdict on the feed, not a measure of lag. The Control
Plane reports whether each subscription is replicating and nothing
about how far behind it is. Lag itself is read inside Postgres with
`pgedge inspect replication-lag` and `replication-slots` against one
instance's connection string, and the
[incident runbooks](incident-runbooks.md) cover reading the pair.

## Host state

`host list -o json` wraps its rows in a `hosts` key, and each host
carries `status.state` alongside a per-component health for its
Docker daemon and its etcd store. The following script fails when any
host is not healthy:

    #!/bin/sh
    set -u

    pgedge controlplane host list --timeout 30s -o json > hosts.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "host list exited $rc" >&2
        exit "$rc"
    fi

    jq -r '.hosts[] | select(.status.state != "healthy")
        | "\(.id) is \(.status.state)"' hosts.json > bad.txt
    if [ -s bad.txt ]
    then
        cat bad.txt >&2
        exit 1
    fi

A host's state is one of `healthy`, `unreachable`, `degraded` and
`unknown`. The [incident runbooks](incident-runbooks.md) say what to
read next on an unreachable host and where the recovery procedure
lives.

## Alerting on failed operations

Every write in the controlplane module is asynchronous. Without
`--wait` or `--follow` a create returns as soon as the server accepts
it, at exit 0, and a create that fails a minute later is visible only
on the task and on the database's `state`. `task list` carries no
status filter, so a script filters the JSON itself. The following
script fails when any task in the cluster has failed since the last
time it ran, and records the time of this run for the next one:

    #!/bin/sh
    set -u

    since=$(cat last-check.txt 2>/dev/null || echo 1970-01-01T00:00:00Z)
    now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

    pgedge controlplane task list --limit 500 --timeout 30s \
        -o json > tasks.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "task list exited $rc" >&2
        exit "$rc"
    fi
    echo "$now" > last-check.txt

    jq -r --arg since "$since" '.tasks[]
        | select(.status == "failed" and .created_at > $since)
        | "\(.type) \(.task_id) on \(.entity_id) failed"' \
        tasks.json > bad.txt
    if [ -s bad.txt ]
    then
        cat bad.txt >&2
        exit 1
    fi

`task list -o json` wraps its rows in a `tasks` key, newest first,
and `created_at` is an RFC 3339 time, the same shape the `date`
command above prints, so the two compare as strings. The time bound
matters because tasks outlive their database: after `database delete`
on a database whose create had failed, both the failed create and the
completed delete stay in the global list and under `task list
--database` for the deleted id, so a script without a bound alerts on
the same failure every run.

`--limit` caps the count. The controlplane module prints no
truncation hint on any read and the server's default page size is
unknown, so pass a `--limit` comfortably above the number of tasks
one interval can produce. A task's status is one of `pending`,
`running`, `completed`, `canceling`, `canceled`, `failed` and
`unknown`.

The failed task's `error` field holds the reason, and only a failed
task carries the field. `pgedge controlplane task get --database
<db-id> <task-id>` prints it under an Error heading in text output,
and `task logs` prints the step trace that says how far the operation
got. The [tasks and async operations guide](../tasks-and-async.md)
covers both.

## The Control Plane itself

`controlplane doctor` exits 0 whatever it finds, so a script reads
its JSON. When the server cannot be reached the document carries
`reachable` as false and nothing about the cluster, and when it can,
a second key saying whether a cluster exists appears beside it. The
[health checks guide](../doctor.md) documents every key. Every other
command exits 1 against an unreachable server, with the connection
error and a hint naming `--base-url` and the mTLS flags on `stderr`.

A Control Plane outage is not a database outage. With the server
stopped, every Postgres instance it manages keeps serving, and
`pgedge inspect` against an instance's connection string keeps
answering. So a script that pairs `doctor` with one `inspect` read
can tell the two apart: `reachable` false with `inspect` answering is
the Control Plane, and `reachable` false with `inspect` refusing the
connection is the host or the network.

When a profile carries several base URLs the CLI tries each in order
and the first that answers serves the whole command. With `-v` the
`stderr` log shows each URL it tried and the one it used, which is
how you find out that a server has silently dropped out of a list
that still answers.

Read the exit status of every one of these calls before reading its
output. The [exit codes guide](../exit-codes.md) carries the contract,
and `controlplane database instance list --database` with an id that
names no database is exit 4 rather than an empty array.

## Next steps

- The [incident runbooks](incident-runbooks.md) take each alert these
  scripts raise through the reads that narrow its cause.
- The [high availability operations guide](ha.md) covers the instance
  and node commands an alert leads to, and the tasks they spawn.
- The [CI and automation guide](../ci.md) covers unattended runs and
  scripting against output and exit codes.
- The [health checks guide](../doctor.md) covers the diagnostics to
  run when a script fails for a connection reason rather than an
  operational one.
