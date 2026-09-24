# Incident runbooks for BYOC databases

Something has gone wrong with a BYOC database. Each section on this
page is a short sequence of `pgedge` commands that finds the cause,
and working the sequence is faster than guessing. Start at the heading
that matches your symptom:

- [The application cannot connect](#the-application-cannot-connect),
  for an application that has lost its database.
- [Out of disk](#out-of-disk), for a database running out of space.
- [Slow queries](#slow-queries), for a database that answers, but
  slowly.
- [Replication lag](#replication-lag), for a node falling behind its
  peers.

Three words run through every section. A BYOC database runs on a
**cluster** in your own cloud account, across one or more **nodes**,
and the nodes replicate to each other through **Spock**, the pgEdge
extension for multi-master replication. Every command that reads
inside Postgres names one node with `--node`, and each node keeps its
own statistics, so a picture of the whole database is one run per
node.

Every command here only reads, so you can follow a runbook against a
database that is still serving traffic.

You need two things before you start: the database id, a full UUID
that `pgedge starfleet byoc database list` prints, and the name of a
node, such as `n1`, which `pgedge starfleet byoc node list
<cluster-id>` prints for the cluster the database runs on.

## The application cannot connect

An application that has lost its database has a problem on one of two
sides of the API boundary, and the first two steps say which side.

1. Prove the CLI's own side of the connection:

        pgedge starfleet doctor -o json

    `starfleet doctor` exits 0 whatever it finds, so its exit status
    answers nothing and `pgedge starfleet doctor && <next step>` reads
    as a gate without being one. Read the fields instead.
    `auth.authenticated` and `tenant.resolved` both true mean the
    credential resolved and the server accepts it. When
    `tenant.resolved` is false, read `api.reachable` first, because an
    unreachable endpoint means a fresh login will not help.

2. Read the database's own status:

        pgedge starfleet byoc database get <db-id> -o json

    A BYOC database is ready at `available`, working at `queued`,
    `creating`, `modifying` or `deleting`, and finished badly at
    `failed` or `degraded`.

3. Re-derive the string the application should be using rather than
   trusting the copy in its configuration:

        pgedge starfleet byoc database connection-string <db-id> \
            --node n1

    The connection belongs to a node, so name one with `--node`, and
    add `--internal` for an application running inside the cluster's
    network. The
    [connect an application guide](connect-an-application.md)
    covers the shape of the string.

4. Read the Postgres log across the window the application started
   failing in, where an authentication failure or a connection reset
   appears:

        pgedge starfleet byoc database logs <db-id> \
            --component-name postgres --nodes n1,n2 --max-lines 500

    The command needs both `--component-name` and `--nodes`, and the
    CLI refuses it without them.

5. Ask a database that accepts connections and then hangs what it is
   waiting on:

        pgedge starfleet byoc database inspect <db-id> \
            locks --node n1

    `locks` prints the sessions waiting on a lock beside the sessions
    holding it, and `long-running-queries` prints the sessions active
    for longer than five minutes:

        pgedge starfleet byoc database inspect <db-id> \
            long-running-queries --node n1

    Both take `--node` on a database with several nodes, and
    `--internal` from inside the cluster's network. A database that
    refuses the connection is exit 1 with the driver's message, and one
    that accepts the connection and never answers is exit 3.

Good fields from doctor, a database at `available` and a freshly
derived connection string that connects leave nothing broken on the
pgEdge side of the boundary. What is left is the application's own
copy of the string, the network path between the application and the
host, or a connection pool still holding sockets the database has
already closed.

## Out of disk

Storage pressure is read from outside the database and then from
inside it, because the platform reports how much space is gone and
Postgres reports what is holding it.

1. Read the metrics series for the database:

        pgedge starfleet byoc database metrics <db-id> \
            --interval 15,minute

    The read sets its lookback with `--interval`, and the
    [logs and metrics workflow](logs-and-metrics.md) covers the rules
    that bound the lookback.

2. Find what is occupying the space:

        pgedge starfleet byoc database inspect <db-id> \
            table-sizes --node n1

    `table-sizes` orders tables by total size and reports the table
    size and the index size apart, which separates a table that has
    grown from an index set that has.

3. Find the space that is reclaimable rather than occupied:

        pgedge starfleet byoc database inspect <db-id> \
            bloat --node n1

    `bloat` estimates table bloat from the planner's statistics, so
    its column says estimate rather than reporting a measured size.

4. Find the indexes nothing reads:

        pgedge starfleet byoc database inspect <db-id> \
            unused-indexes --node n1

    `unused-indexes` lists non-unique indexes scanned fewer than 50
    times. Dropping one frees its storage without touching a row of
    data.

BYOC has no resize command. A database's storage is the storage of
the cluster it runs on, in your own cloud account.

## Slow queries

A slow database raises several separate questions, and the analyses
below answer them in an order that tells an expensive statement, a
missing index and a neglected table apart.

1. Rank statements by the time they consume:

        pgedge starfleet byoc database inspect <db-id> \
            outliers --node n1

    `outliers` orders statements by total execution time and `calls`
    orders them by call count, which are two different culprits: one
    expensive statement, and one cheap statement run far too often:

        pgedge starfleet byoc database inspect <db-id> \
            calls --node n1

    Both need the pg_stat_statements extension and both exit 1 naming
    it when the database does not have it. On a database upgraded in
    place, either may fail naming a missing total execution time
    column, which means the extension is at a version below 1.8 there
    and updating the extension resolves it.

2. Find the tables the planner reads end to end:

        pgedge starfleet byoc database inspect <db-id> \
            seq-scans --node n1

    `seq-scans` orders tables by sequential scan count beside their
    index scans, so a table high on the first count and low on the
    second is a candidate for an index.

3. See what is running right now rather than what has run:

        pgedge starfleet byoc database inspect <db-id> \
            long-running-queries --node n1

4. Check the state of the tables underneath those plans:

        pgedge starfleet byoc database inspect <db-id> \
            vacuum-stats --node n1

    `vacuum-stats` prints the live and dead rows per table with the
    last vacuum times, so a table whose dead rows have accumulated
    since its last vacuum shows both facts in one row.

`database inspect` runs one read-only analysis from a fixed set and
prints the rows the server returns. It runs no EXPLAIN, so nothing
here says why one query chose the plan it did. Take the statement text
from `outliers` into psql and run EXPLAIN there, using the string this
command prints:

    pgedge starfleet byoc database connection-string <db-id> --node n1

The [inspect a database guide](../inspect-a-database.md) covers the
analyses and the output every one of them
produces.

## Replication lag

A replica behind its publisher is read from two directions, because
the connection reports how far behind each replica has fallen and the
slot reports what the publisher is keeping for it. Neither reading
answers on its own, so take both in order.

1. Find out which replicas are connected and how far behind each one
   is:

        pgedge starfleet byoc database inspect <db-id> \
            replication-lag --node n1

    `replication-lag` prints one row per replica or subscriber
    connected to this node. The columns are:

    - the client address and the application name that identify it
    - the state of the connection
    - the sent position and the replay position
    - the write, flush and replay lag times
    - the sync state

    Read the distance between the sent and the replay position first.
    A replica working through a backlog reports a state of catchup
    and leaves the three lag times empty, so the two positions are
    what show how far behind it is. The times fill in only while the
    database is taking writes and the replica is keeping up with
    them. A replica that stops consuming altogether drops off this
    analysis rather than reporting a growing lag, so no rows is the
    ordinary reading on a database with no replicas, and equally what
    a departed subscriber leaves behind.

2. Read the slots whatever the first step returned:

        pgedge starfleet byoc database inspect <db-id> \
            replication-slots --node n1

    `replication-slots` prints one row per slot with its type, plugin
    and database, whether the slot is active, its WAL status and the
    WAL it has retained. The retained figure is what the slot holds
    against the database's current write position, and it climbs
    while nothing consumes the slot. An empty first step beside a
    slot whose active reads false and whose retained figure is large
    is the pair that says the replica is gone and the database is
    still holding WAL for it, which takes you into the
    [out of disk](#out-of-disk) runbook next.

3. Read the subscriber's own side on a database that subscribes to
   another:

        pgedge starfleet byoc database inspect <db-id> \
            subscriptions --node n1

    `subscriptions` prints one row per subscription worker with its
    pid, the position it has received, the times of the last message
    sent and received, and the latest end position. The rows belong
    to the database that ran CREATE SUBSCRIPTION rather than to the
    publisher.

A node's slots and the replicas connected to it describe what that
node sends, so a picture of the whole database means one run per node.
Spock keeps its own subscription catalog and never issues CREATE
SUBSCRIPTION, so `subscriptions` answers empty on every node of a
BYOC database, while that node's slots and connected peers still
appear in the other two analyses. An empty result there is not
evidence that replication is broken.

These three analyses report what each replica has received and what
each slot is keeping, and none of them says why a replica is behind.
The [inspect a database guide](../inspect-a-database.md) covers the
output every one of them produces.

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
