# Incident runbooks for Control Plane databases

Something has gone wrong with a database your Control Plane runs.
Each section on this page is a short sequence of `pgedge` commands
that finds the cause, and working the sequence is faster than
guessing. Start at the heading that matches your symptom:

- [The application cannot connect](#the-application-cannot-connect),
  for an application that has lost its database.
- [A failed operation](#a-failed-operation), for a create, update or
  delete that did not finish.
- [Out of disk](#out-of-disk), for a host running out of space.
- [Slow queries](#slow-queries), for a database that answers, but
  slowly.
- [Replication lag](#replication-lag), for a node falling behind its
  peers.
- [A host is unreachable](#a-host-is-unreachable), for a machine the
  Control Plane cannot see.
- [The Control Plane is unreachable](#the-control-plane-is-unreachable),
  for the server itself being down.

Four words run through every section. A database runs on several
**nodes**. Each node runs as a Postgres **instance**, which is a
container on a **host** machine. Nodes replicate to each other
through **Spock**, the pgEdge extension for multi-master replication.

Almost every command here only reads, so you can follow a runbook
against a database that is still serving traffic. Five commands
change something, and each is called out where it appears: starting
a stopped instance, creating an extension, running ANALYZE, deleting
a database and removing a host.

Collect three things before you start.

**The database id.** It is the name someone passed to `pgedge
controlplane database create`. To see the ids on this Control Plane,
run:

    pgedge controlplane database list

**An instance id**, for any step that acts on a single node. The
server generates these, so you cannot work one out. Copy one from the
ID column of:

    pgedge controlplane database instance list --database <db-id>

**A connection string**, for the `pgedge inspect` steps that read
inside Postgres. You assemble it from three places:

- The address and port come from the instance's `connection_info`
  block in `pgedge controlplane database get <db-id> -o json`.
- The Postgres database name and the user come from the spec the
  database was created from. `pgedge controlplane database get
  <db-id> -o yaml` prints the spec the server holds, with the
  database name under `database_name`.
- The password comes from the spec file the database was created
  from, and from nowhere else. No command in this CLI prints it, and
  the spec the server returns omits it. If you do not have the file,
  ask whoever created the database.

Instances accept `sslmode=require`, so the string has this shape:

    postgresql://<user>:<password>@<address>:<port>/<name>?sslmode=require

`<name>` is the Postgres database name from the spec, which need not
equal the database id. Every `--db-url` below stands for this whole
string.

## The application cannot connect

An application that has lost its database has a problem in one of
three places: the Control Plane, the instance, or the path between
the application and the instance. The first three steps say which.

1. Check that the CLI can reach the Control Plane:

        pgedge controlplane doctor -o json

    `controlplane doctor` exits 0 whatever it finds, so read the
    fields. `reachable` true, with a second key beside it saying a
    cluster exists, means the server answers and is initialized. The
    [health checks guide](../doctor.md) documents every key.
    `reachable` false means the Control Plane is down or unreachable
    from here, which is the
    [Control Plane unreachable](#the-control-plane-is-unreachable)
    runbook. It says nothing about the database, because the
    instances keep serving while their Control Plane is away.

2. Read the database and its instances:

        pgedge controlplane database get <db-id>

    The first line carries the database's `state`. The Instances
    table underneath has one row per instance with ID, NODE, HOST,
    STATE, ROLE, PG, SPOCK, ADDRESSES and PORT. A stopped instance
    reads `stopped` with ROLE, PG, SPOCK, ADDRESSES and PORT blank,
    and an "Instance errors" table follows naming the node and the
    reason, which for a stopped instance says no Postgres container
    was found. The database's own `state` stays `available` while
    one of its instances is stopped, so the first line alone does not
    prove the database healthy. Under `-o json` the stopped instance
    carries an `error` field and no `connection_info` block.

3. Bring a stopped instance back:

        pgedge controlplane database instance start <db-id> \
            <instance-id> --wait

    `instance start` interrupts nothing, so it does not prompt. The
    [high availability operations guide](ha.md) covers the instance
    commands, the force flags and what a wait reports.

4. Ask an instance that accepts connections and then hangs what it
   is waiting on:

        pgedge inspect locks --db-url "<connection-string>"

    `locks` prints one row per blocked session with the pid it is
    blocked by, how long it has waited and the blocked statement. A
    hang that began two minutes ago shows here first, because
    `long-running-queries` only lists sessions active for longer than
    five minutes, and on a multi-node database that analysis always
    carries one row per peer for Spock's replication connection,
    which the [slow queries](#slow-queries) runbook describes. An
    instance that refuses the connection is exit 1 with the driver's
    message, and one that accepts the connection and never answers is
    exit 3 after 30 seconds.

The Postgres log is not reachable through this CLI. It is a file on
the instance's host. Take the `data_dir` value from `pgedge
controlplane host get <host-id> -o json`, log in to that host, and
the log is under `<data_dir>/instances/<instance-id>/data/pgdata/log/`.

A reachable Control Plane, every instance at `available` with an
address and port, and an `inspect` command that connects leave
nothing broken on the Control Plane's side. What is left is the
application's copy of the connection string, the network path between
the application and the host, or a connection pool still holding
sockets an instance restart has closed.

## A failed operation

Every write in the controlplane module runs as a task, and a task
that fails keeps the reason on itself rather than on the resource it
was building. The
[tasks and async operations guide](../tasks-and-async.md) covers the
task commands, and this runbook is the order to read them in.

1. List the database's tasks, newest first:

        pgedge controlplane task list --database <db-id>

    The TYPE column names the operation, such as create, update or
    delete, and STATUS reads `failed` on the one you want.

2. Read the reason:

        pgedge controlplane task get --database <db-id> <task-id>

    Text output prints the task's one-row table and then an Error
    heading with the full error text, which names the resource the
    Control Plane was creating when it failed and carries the
    underlying tool's own output. Under `-o json` the same text is
    the `error` field, present only on a failed task.

3. Read how far the operation got:

        pgedge controlplane task logs --database <db-id> <task-id>

    The log is one line per step, each resource created with the
    time it took, ending with the step that failed. It names the step
    and not the reason, so read `task get` and `task logs` together.

4. Read what the failure left behind:

        pgedge controlplane database get <db-id>

    A create that fails after the server accepted it leaves the
    database at `failed`. Its instance can still read `available`
    with role, versions, address and port blank, and the Postgres
    container behind it can be running with no database of that name
    inside, so an `inspect` command against its port fails naming the
    missing database. Read the database's `state`, not the
    instance's, to decide whether a create succeeded.

Recovery from a failed create is a delete and a corrected create.
Fix whatever the error text named in the spec, then delete the failed
database and its instances:

    pgedge controlplane database delete <db-id> --force --wait

Then create it again from the corrected spec, which the
[databases guide](databases.md) covers. The failed task stays in
`task list` after the delete, under the deleted database's id and in
the global list, so a later check still finds it.

Not every failure produces a task. If the server can spot the problem
the moment you submit the spec, it refuses the request outright,
before any task exists. Three examples:

- a host id the cluster does not have
- a port another database already publishes on that host
- a service version the API does not accept

Each of those comes back as HTTP 400 at exit 1, with the message on
`stderr`, and `task list` for that database shows nothing at all. A
task exists only for a failure the server cannot see until it starts
work, such as a backup repository it cannot reach. So an empty task
list after a failed command means the request never got that far,
and the reason is on `stderr`.

## Out of disk

No Control Plane command reports a disk figure. `pgedge controlplane host get
<host-id> -o json` carries the host's CPU count and memory and its `data_dir`,
and nothing about the space left on it. So the outside view is the host's own
tools: log in to the host and run `df -h` against that directory. The inside
view is what is holding the space, and every step below is an `inspect`
analysis against one instance's connection string.

1. Find what is occupying the space:

        pgedge inspect table-sizes --db-url "<connection-string>"

    `table-sizes` orders tables by total size and reports the table
    and the index size apart, and `index-sizes` lists the indexes on
    their own. A new database shows the Spock catalog tables and
    nothing else.

2. Find the space that is reclaimable rather than occupied:

        pgedge inspect bloat --db-url "<connection-string>"

    `bloat` estimates table bloat from the planner's statistics. A
    table that has never been analyzed has no statistics, so the
    analysis answers no rows for it however many dead rows it holds.
    Either analyze the table first, through psql as the admin user on
    the same connection string:

        psql "<connection-string>" -c 'ANALYZE <schema>.<table>;'

    or read `vacuum-stats`, which counts dead rows directly:

        pgedge inspect vacuum-stats --db-url "<connection-string>"

    `vacuum-stats` prints the live and dead rows per table beside the
    last vacuum times, and a table whose two vacuum columns are empty
    has never been vacuumed.

3. Find the indexes nothing reads:

        pgedge inspect unused-indexes --db-url "<connection-string>"

    `unused-indexes` lists non-unique indexes scanned fewer than 50
    times. A primary key is unique and never appears here.

4. Check the replication slots:

        pgedge inspect replication-slots --db-url "<connection-string>"

    A peer node that has stopped consuming leaves its slot on this
    instance inactive, and the retained WAL climbs with every write
    here. That is the [replication lag](#replication-lag) runbook.

The spec carries no storage size. An instance's data lives under the
host's data directory and grows into the host's disk, so the remedy
for a full disk is on the host.

## Slow queries

A slow database raises several separate questions, and the analyses
below answer them in an order that tells an expensive statement, a
missing index and a neglected table apart. Each one runs against one
instance's connection string, and on a multi-node database each node
keeps its own statistics, so run them on the node the application
talks to.

1. Make sure the statement statistics exist. `outliers` and `calls`
   both read the pg_stat_statements extension, and both exit 1
   naming it while it is missing. The Control Plane's Postgres image
   already loads the library, but a new database does not have the
   extension itself, so create it once. Use psql, as the admin user
   from the spec, on the same connection string the `inspect`
   commands use:

        psql "<connection-string>" \
            -c 'CREATE EXTENSION pg_stat_statements;'

    Spock replicates the statement to the other nodes, so running it
    on one node covers the whole database.

2. Rank statements by the time they consume:

        pgedge inspect outliers --db-url "<connection-string>"

    `outliers` orders statements by total execution time and `calls`
    orders them by call count, which are two different culprits, one
    expensive statement and one cheap statement run far too often:

        pgedge inspect calls --db-url "<connection-string>"

    In `calls`, this CLI's own repeated reads appear once they have
    run, including a row that reads from pg_stat_statements. Ignore
    those rows. They rank by call count there and never reach the
    top of `outliers`.

3. Find the tables the planner reads end to end:

        pgedge inspect seq-scans --db-url "<connection-string>"

    `seq-scans` orders tables by sequential scan count beside the
    rows those scans read and the table's index scans, so a table
    high on the first two counts and at zero on the third is a
    candidate for an index. The Spock catalog tables lead the count
    on a quiet database, and yours appear below them.

4. See what is running right now rather than what has run:

        pgedge inspect long-running-queries --db-url "<connection-string>"

    `long-running-queries` prints the pid, duration, user, state and
    statement of every session active for longer than five minutes.
    On a Control Plane node one row per peer is permanent. Each peer
    node's replication connection shows as an active session for a
    role named pgedge whose statement begins START_REPLICATION SLOT,
    from five minutes after the peer connected until it disconnects.
    Those rows are Spock's feeds to the peers and not slow queries,
    so read past them to the rows that belong to your application.

5. Check the state of the tables underneath those plans:

        pgedge inspect vacuum-stats --db-url "<connection-string>"

`inspect` runs one read-only analysis and prints the rows. It runs
no EXPLAIN, so nothing here says why one query chose the plan it
did. Take the statement text from `outliers` into psql on the same
connection string and run EXPLAIN there. The
[inspect a database guide](../inspect-a-database.md) covers every
analysis and its columns.

## Replication lag

A Control Plane database replicates node to node through Spock, and
each node consumes a subscription from each of the others. Read the
Control Plane's own verdict first, then the positions inside the
node. The first says whether a feed is up, and the second says how
far behind it is.

1. Read the subscription status on every instance:

        pgedge controlplane database instance list --database <db-id> \
            -o json

    Each instance carries a `spock` block with the subscriptions it
    consumes, each naming its `provider_node` and a `status`. A
    healthy feed reads `replicating`. When the provider's instance is
    stopped, the surviving instance's entry for it reads `down`, and
    the stopped instance reports no subscriptions of its own. A value
    other than those two is possible, so treat anything other than
    `replicating` as a fault.

2. Read the connections on the instance the others pull from:

        pgedge inspect replication-lag --db-url "<connection-string>"

    `replication-lag` prints one row per peer connected to this
    instance. The columns are:

    - the client address
    - an application name, which Spock builds from three parts: the
      database, this node, and the node that is subscribing
    - the connection state
    - the sent position and the replay position
    - three lag times
    - the sync state

    When the nodes are keeping up, the state reads `streaming`, the
    sent and replay positions match, and the three lag times are
    empty. A peer whose instance is stopped drops off this analysis
    rather than reporting a growing lag, so no rows here on a
    two-node database means the other node is not connected.

3. Read the slots on the same instance:

        pgedge inspect replication-slots --db-url "<connection-string>"

    `replication-slots` prints one row per slot with whether it is
    active and the WAL it has retained. While the peer is stopped its
    slot reads inactive and the retained figure climbs roughly one
    for one with what is written on this instance. When the peer
    comes back the connection reappears in `replication-lag`, the
    slot reads active, and the retained figure falls back over the
    following minutes rather than at once, so a large figure on an
    active slot a minute after a restart is the tail of the incident
    and not a new one.

4. Read the other node the same way. Each instance reports the peers
   that pull from it, so the picture of a two-node database is two
   reads, one per connection string.

`subscriptions` answers no rows on every node of a Control Plane
database, because Spock keeps its own subscription catalog rather
than using Postgres's, and an empty result there is not evidence
that replication is broken. A table created on one node reaches the
others through Spock's DDL replication, and until the initial copy of
its rows has applied, a read on another node sees neither the table
nor the rows. So a table missing on one node seconds after it was
created on another is the copy in flight rather than a fault.

## A host is unreachable

1. Read the hosts:

        pgedge controlplane host list

    The STATE column reads one of `healthy`, `unreachable`,
    `degraded` and `unknown`. `pgedge controlplane host get
    <host-id> -o json` adds the health of the host's Docker daemon
    and its etcd store under `status`, each with its own error text.

2. Find the instances on that host:

        pgedge controlplane database instance list

    Without `--database` the table lists every instance in the
    cluster. The rows whose HOST column names the affected host are
    the instances that have gone with it, and their DATABASE column
    says which databases have lost a node. Each of those databases
    continues on its other nodes, and their `spock` entries for the
    lost node read `down`, as in the
    [replication lag](#replication-lag) runbook.

3. Decide whether the host will return. A host that reboots, or
   whose network recovers, rejoins the cluster on its own, and
   nothing on this page is needed for that, so wait rather than act
   if there is any chance of it returning. Only when a host is gone
   permanently, remove it:

        pgedge controlplane host remove <host-id> \
            --force --force-lost --wait

    `--force` skips the confirmation prompt. `--force-lost` tells the
    server to waive its instance and quorum checks, so this can leave
    a database short of a node, and it is the only flag in the module
    intended for disaster recovery. The
    [high availability operations guide](ha.md) explains both.

    Losing several hosts at once can cost the Control Plane its own
    quorum, when most of the hosts whose `etcd_mode` is `server` are
    gone. Getting that back is not a `pgedge` job. It is a procedure
    you run on the hosts, using Docker and etcd directly, written up
    as
    [Recovering a Control Plane Cluster](https://docs.pgedge.com/control-plane/v0-10/disaster-recovery/disaster-recovery/)
    in the Control Plane documentation.

## The Control Plane is unreachable

1. Read the diagnostic:

        pgedge controlplane doctor

    The Reachable row reads `error` with the base URL and
    `(unreachable)`, no Cluster row follows, and the exit status is
    still 0. Every other command against an unreachable Control Plane
    exits 1, with the connection error and a hint naming `--base-url`
    and the mTLS flags on `stderr`.

2. Check that the instances are still serving:

        pgedge inspect replication-slots --db-url "<connection-string>"

    The Control Plane manages the instances and does not sit between
    the application and them. With the server stopped, every instance
    keeps serving and every `inspect` command keeps answering, so an
    unreachable Control Plane is an incident for operations, not for
    the application. An `inspect` command that fails too points at
    the host or the network rather than at the Control Plane.

3. Try the other servers. When you give several base URLs, the CLI
   tries each in order and the first that answers serves the whole
   command:

        pgedge controlplane database list -v \
            --base-url http://host-1:3000 --base-url http://host-2:3000

    With `-v` the `stderr` log names each URL tried and the one used,
    which shows whether the server you expected is the one that
    answered. When every URL fails, the error lists them all. The
    [configuration guide](../configuration.md) covers storing the list
    in a profile.

Once the server is started again, `doctor` reports the cluster
verdict beside `reachable`, and `host list`, `database get` and the
instances' subscription statuses read as they did before, with no
action beyond the start.

## Getting support

Where the sequence in a runbook runs out, three guides carry the rest:

- The [troubleshooting guide](../troubleshooting.md) is organized by
  exit code, for when one of these commands has already failed.
- The [error catalog](../error-catalog.md) is keyed on the text a
  failing command prints to `stderr`.
- The [health checks guide](../doctor.md) documents every row the
  doctors print, what each warning means and what fixes it.

[Versions, uninstall and support](../support-versioning-and-uninstall.md)
says where to take a problem that survives all three, and what to
attach so it can be acted on.
