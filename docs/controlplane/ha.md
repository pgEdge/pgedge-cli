# High availability operations

Use the commands on this page to keep a Control Plane database
available. You can move its leader between nodes, take single Postgres
instances in and out of service, and remove a host that is never
coming back. Every operation here acts on a database a Control Plane
already runs. Each one hands the real work to a task, which you then
watch.

You need a Control Plane cluster the CLI can reach, and a database on
it. The [Control Plane standup workflow](local-server.md)
covers getting the server running and the cluster initialized, and
the [Control Plane databases guide](databases.md) covers writing a
spec and creating the database. This CLI supports
Control Plane 0.10.0 and later.
`pgedge controlplane doctor` turns its Reachable row into a warning
when a server reports a version below that floor.

## Reading Instance IDs Before You Act

The commands below take either a node name or an instance ID, and
each command accepts only one of the two. `node switchover` and
`node failover` name a node, such as `n1`. The three instance
commands name an instance, and so does `--candidate` on both node
commands.

The server generates each instance ID as `<database>-<node>-<suffix>`
with a random suffix. Read the IDs from the fleet instead of building
them from a database and a node name:

    pgedge controlplane database instance list --database storefront

The table carries DATABASE, ID, NODE, HOST, STATE, ROLE, PG and SPOCK
columns. The ID column holds the value every other instance command
wants. Drop `--database` to list every instance in the cluster.
`pgedge controlplane database get storefront` shows the same IDs for
one database.

A `--database` that names nothing exits with status 4. A real database
that holds no instances reports that on `stderr` and exits with status
0. The exit status therefore tells a mistyped name apart from an empty
database. Under `-o json`, an empty result prints `[]`, so a filter
reading it always gets a document.

## Running a Planned Switchover

A switchover gracefully promotes a replica to leader, on a database
that is healthy. It drops the connections the old leader was holding,
so a connected application notices.

The following command promotes a named replica and blocks until the
task finishes:

    pgedge controlplane database node switchover storefront n1 \
        --candidate storefront-n2-9ptayhma --wait

The command asks you to confirm, and you type `y` to proceed:

    Switch over node n1 in database storefront? This drops the connections the current leader is holding. [y/N]: y

`--candidate` chooses which replica the Control Plane promotes. The
CLI sends the value exactly as you typed it. A wrong or misspelled
instance ID therefore surfaces from the server after the call, not as
a usage error before it. Take the value from `instance list`. Omit the
flag to leave the choice to the Control Plane.

`--scheduled-at` defers the change to an RFC3339 time. The CLI parses
the time before it prompts. A malformed timestamp is a usage error
with exit status 2, reported before any prompt appears.

Switchover always prompts. Run non-interactively without `--force`,
it stops with exit status 2. The message says the operation is
destructive and asks for `--force` or an interactive terminal.

## Running an Unplanned Failover

A failover is the unplanned move, for a leader that is already
unhealthy. It can interrupt writes, so prefer a switchover whenever
the current leader still answers.

The following command fails a node over and streams the task log:

    pgedge controlplane database node failover storefront n1 --follow

The command asks you to confirm, and you type `y` to proceed:

    Fail over node n1 in database storefront? This is an unplanned promotion. [y/N]: y

The Control Plane runs health checks that block a failover on a
healthy cluster, and `--skip-validation` bypasses them. Reach for that
flag only when you know the leader is gone and the checks disagree.

Failover sends every value to the server unchecked. As on switchover,
the CLI passes `--candidate` through as typed. Failover takes no
`--scheduled-at`, so every rejection comes from the server or from
inside the task. Like switchover, it always prompts.

## Starting, Stopping and Restarting an Instance

The instance commands act on the per-host Postgres container behind
one node. The node's replication role stays as it is. The following
table shows what each command does and how it treats confirmation:

| Command | What it does | Prompts | Other flags |
|---|---|---|---|
| `start` | Brings a stopped instance back online. | No | `--force-unmodifiable` |
| `stop` | Takes an instance out of the database until it is started again. | Yes | `--force`, `--force-unmodifiable` |
| `restart` | Bounces the instance's Postgres process, dropping its connections. | Yes | `--force`, `--scheduled-at` |

The following command restarts one instance during a maintenance
window:

    pgedge controlplane database instance restart storefront \
        storefront-n1-689qacsi --scheduled-at 2026-09-01T22:00:00Z

The command asks you to confirm, and you type `y` to proceed:

    Restart instance storefront-n1-689qacsi in database storefront? This drops its connections. [y/N]: y

Only `restart` takes `--scheduled-at`, and the API sets that limit.
Start and stop are query-parameter endpoints with no body to carry a
schedule. Restart takes a body with a `scheduled_at` field. You
confirm a scheduled restart when you queue it, and it then runs at the
scheduled time.

`instance list` is a plain read that prints its table directly, with
no task to watch. Run it without `--wait` or `--follow`.

## Choosing Between the Three Force Flags

Three flags in this tree start with `--force`, and each one does a
different thing. The following table shows which commands across the
whole controlplane module take each one, and what it changes:

| Flag | Commands | What it changes |
|---|---|---|
| `--force` | `node switchover`, `node failover`, `instance stop`, `instance restart`, `host remove`, `database restore`, `database delete`, `database update`, `database upgrade`, `task cancel` | Skips the confirmation prompt in the CLI. The flag stays in the CLI. |
| `--force-unmodifiable` | `instance start`, `instance stop`, `node backup`, `database restore`, `database delete` | Sets the API's own force parameter, so the server waives its unmodifiable-state check. |
| `--force-lost` | `host remove` | Sets that same API parameter on host removal, so the server waives the instance and quorum checks. |

Of the three flags, only `--force-unmodifiable` and `--force-lost`
override a server-side check. A scripted `host remove --force` skips
the question, and the quorum checks still apply unless you add
`--force-lost`. A command that runs without a prompt rejects
`--force`. For example, `node backup` and `instance start` both report
it as an unknown flag and exit with status 2.

## Removing a Host

`host list` and `host get` show the cluster's hosts with ID,
ORCHESTRATOR, STATE and UPDATED columns. STATE is one of `healthy`,
`unreachable`, `degraded` or `unknown`. UPDATED shows the date only.
To see the time of day of the last update, read `-o json`. `host
get` prints a single row with the same columns as the list:

    pgedge controlplane host list
    pgedge controlplane host get host-3

`host remove` takes a host out of the cluster. The removal affects
every database with instances on that host, so the command prompts
unless you pass `--force`.

The server then applies a precondition of its own, whether or not the
host still answers. It refuses the removal while instances live on the
host, or when losing the host would break quorum. `--force-lost`
waives both checks. It can leave a database short of an instance and
a cluster short of a voter. That is the disaster-recovery path, for a
host that is permanently gone. An unreachable host may come back, so
keep `--force-lost` for a host that will stay gone.

The following command removes a lost host and waits for the removal
task:

    pgedge controlplane host remove host-3 --force-lost --wait

The command asks you to confirm, and you type `y` to proceed:

    Remove host host-3? [y/N]: y

The removal runs as a task, like everything else here. It is a host
task, so when you look for it afterward, read it by host with
`pgedge controlplane task get --host host-3 <task_id>`.
Under `-o json` or `-o yaml`, the accepted response carries the
removal task under `task`. It carries any database updates the removal
spawned under `update_database_tasks`.

## Watching Tasks

Every command on this page except `instance list`, `host list` and
`host get` spawns a task. Each returns as soon as the server accepts
that task. Acceptance is a line on `stderr` reading `Switchover task
<id> accepted (<status>).`, with the command's own name in front.
`host remove` words it differently, as `Host removal task <id>
accepted (<status>).`.

Under `-o json` or `-o yaml`, the API's accepted response object also
goes to `stdout`, exactly once, in every wait mode. Progress lines and
the terminal verdict stay on `stderr`, and text output leaves `stdout`
empty. The
[output formats and paging guide](../output-and-paging.md) covers that
split across the whole CLI.

`--wait` blocks until the task reaches a terminal state. It prints one
status line to `stderr` for each check. It defaults to a 600-second
bound and a three-second interval. The CLI treats an interval below
one second as one second. A second bound also applies: when one status
request outlasts `--timeout`, 30 seconds by default, the wait stops
with exit status 3. The wait ends there, without a retry on the next
interval.

`--follow` streams the task's log instead, and it ignores both wait
flags. It runs until the task ends, with no overall bound. It reads
the log every two seconds, whatever `--wait-interval` says. It bounds
each log request at 30 seconds, independently of `--timeout`. As a
result, `--timeout 0` still stops a log request at 30 seconds. Only a
`--timeout` below 30 seconds shortens one.

The two modes also report a failure differently. `--wait` prints
`task <id> failed: <reason>` when the task carries a reason.
`--follow` prints a bare `task <id> failed`, and you look up the
reason yourself. In either mode,
`pgedge controlplane task get --database storefront <task_id>` holds
the reason and `task logs` holds the step trace.

A third terminal outcome sits alongside `completed` and `failed`:
`canceled`. Both wait modes treat it as a failure with exit status 1
on every command here, and report that the task was canceled. `task
cancel` is the exception, because there a canceled task is the outcome
you asked for. `canceling` is an in-progress status, so a wait keeps
checking until the task settles. The
[tasks and async operations guide](../tasks-and-async.md) covers task
inspection and cancellation for all three modules.

Each mutating command here also takes `--dry-run`. A dry run runs the
client-side checks, reports the request it would send, and stops
before sending it. A controlplane dry run runs entirely in the CLI. It
validates a spec locally on `database create` and `database update`.
On the commands here, the confirmation is the only check left. A dry
run therefore prints the request it would send and says the server was
not consulted. It skips the prompt too. Unless you also pass
`--force`, it records the confirmation the real run would ask for. The
[dry runs guide](../dry-run.md) has the detail.

## Reading Exit Codes

The following table shows what produces each exit code on this page:

| Code | What produces it here |
|---|---|
| 0 | The server accepted the operation, and under `--wait` or `--follow` the task completed. |
| 1 | The task failed or was canceled, the Control Plane could not be reached, or the server refused the operation with any status other than the four below. |
| 2 | A usage error: a malformed `--scheduled-at`, a destructive command run non-interactively without `--force`, or `--force` passed to a command that runs without a prompt. |
| 3 | A bound expired: `--timeout` on one request, `--wait-timeout` on a wait, or the 30-second limit on each log request under `--follow`. A 408 or 504 from the server lands here too. |
| 4 | The database, node, instance or host does not exist. The server usually says so in a JSON error, though `instance list --database` decides it locally from the database list. |
| 5 | The server answered 401 or 403. In front of a Control Plane, that usually comes from a proxy, not from the Control Plane itself. |

Two further cases exit with status 1. A cluster that was never
initialized answers 409 on every command here. The CLI says so and
points at `cluster init`.
The second case is a 404 carrying the router's plain
`404 page not found` instead of a JSON error. It means the server
lacks the endpoint entirely, which is how a Control Plane below the
0.10.0 floor appears.
Both cases exit with status 1, even where a 404 suggests status 4.

The [exit codes reference](../exit-codes.md) carries the contract for
every module, and the [health checks guide](../doctor.md) explains the
`doctor` rows to read first when a command cannot connect.

## Next Steps

- The [Control Plane backup and restore](backup-restore.md)
  document covers per-node backups and rebuilding a database from a
  repository.
- The [tasks and async operations](../tasks-and-async.md) document
  explains how to inspect and cancel the tasks these commands spawn.
- The [local Control Plane](local-server.md) document covers the
  server, the cluster and adding further hosts.
