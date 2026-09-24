# Diagnosing an Incident on a pgEdge Starfleet Managed Database

pgEdge owns and keeps running the hardware under a Managed database,
so half of any diagnosis is a platform reading and half is a Postgres
one. This page holds four runbooks that read both. Pick the runbook
whose symptom matches yours, and work down it in order.

Three terms recur across these runbooks:

- A status is the database's own lifecycle value, shown in the STATUS
  column of `database list` and `database get`.
- An analysis is one named read-only query that `database inspect`
  runs inside the database.
- admin, app and app_read_only are the roles a Managed database
  delivers. None of them is a Postgres superuser.

One command here changes the database. It is `database resize`, in
[Recovering From a Full Disk](#recovering-from-a-full-disk). Every
other command on this page reads, and writes nothing.

## Before You Start

You need the database's full UUID and psql for the two steps that
open a session.

- Run `pgedge starfleet managed database list` to find the database.
  Take the full UUID from the ID column. Every command here takes a
  full UUID.
- Install psql. Two steps below open a psql session, and EXPLAIN
  runs in one of them.

Five analyses read other sessions' rows: long-running-queries, locks,
calls, outliers and replication-lag. Each connects as admin on its
own, with no flag from you. Pass `--user-type app` to one of them and
it answers wrongly instead of failing, because Postgres blanks
another session's statistics rows for a role that lacks
pg_read_all_stats. Connected as app:

- long-running-queries and locks come back empty, and read as a quiet
  database.
- calls and outliers print `<insufficient privilege>` for every
  query.
- replication-lag returns one row per replica, with every column
  blank but the application name.

`database inspect` exits 1 when Postgres rejects the connection. It
exits 3 when a connection is accepted but returns nothing within
thirty seconds. An analysis with nothing to report exits 0 and says so
on stderr.

A resize cannot be undone, and it is the one command here that
changes the database. A resize grows a database and never shrinks it
again. A resize also moves the database onto different infrastructure
and interrupts service while it runs. On a per-vCPU plan it raises the
monthly bill for as long as the database lives.

## Tracing a Lost Connection

Where an application cannot reach its database, the loss is on the
platform side or on its own side. All five steps below read the
platform side. Where all five read clean, the fault sits in the
application.

1.  Read the connection between this machine and pgEdge:

        pgedge starfleet doctor -o json

    `auth.authenticated` true means a credential resolved from your
    flags or your profile, and nothing more: that check reaches no
    server. `tenant.resolved` true is what says the server took the
    credential. `api.reachable` true means the API base URL answered
    an unauthenticated request.

    Where `tenant.resolved` is false, read `api.reachable` before you
    retry the login. An endpoint that is not answering makes a fresh
    login pointless, so `auth login` is the wrong first move until
    `api.reachable` is true.

    The command exits 0 on every outcome, even with no credential at
    all. Read the fields, not the exit status.

2.  Read the status the platform reports:

        pgedge starfleet managed database get <db-id>

    `available` is ready, and every write is admissible from it.
    `modifying` means a change is in flight, such as a restore, a
    resize or a rotation. `failed` means the last operation did not
    succeed, and the row is kept either way. `degraded` means a
    resize failed after the database was already up.

    The remaining readings are ordinary, not faults.
    `creating` is a database still being provisioned, which is also
    why step 3 can find no host. `deleting` is one being torn down. A
    hibernating database reads suspending, suspended or resuming, and
    comes back on its own. Only `failed` and `degraded` need pgEdge,
    so take either to [Getting Support](#getting-support).

3.  Derive a fresh connection string, instead of reading the one
    your application holds:

        pgedge starfleet managed database connection-string <db-id>

    The command prints a libpq URI carrying `sslmode=require`, built
    from the connection block the API returns now. Pass
    `--user-type admin` for admin's string in place of app's. A
    database whose connection block has no host yet is reported on
    stderr at exit status 1.

    While a password rotation is in flight the status reads
    `modifying`, and this command returns the password that is still
    live. The new password and `available` appear together when the
    rotation lands. Until then, the password your application already
    holds is the one that works.

4.  Connect with the string from step 3, using psql:

        psql "<connection-string>"

    A connection that works here and fails from your application
    points at the application. A session that connects and then
    blocks is a third case, and
    [Finding a Slow Query](#finding-a-slow-query) reads it.

5.  Read the database's log for authentication failures and
    connection resets:

        pgedge starfleet managed database logs <db-id> --max-lines 500

    Postgres records a rejected login here, so run this step even where step 4
    connected. Records arrive newest first, and this command leaves that order
    alone. Text output prints one line per record, timestamp then level then
    message, so it pipes into grep unchanged. `--max-lines` takes 1 to 1000.
    Bound an absolute window with `--start-time` and `--end-time`, both RFC3339
    timestamps. A record missing one of those three fields prints `-` in its
    place.

Five sound readings clear the platform side. Three things are then
left:

- the credential your application has stored,
- the network path from your application to the database,
- a connection pool holding sockets that are already dead.

## Recovering From a Full Disk

Storage fills from two directions: what the platform reports the
database consuming, and what Postgres holds inside it. Read the
platform side first, because it shows the database's size over time.

1.  Read the metric series for the last fifteen minutes:

        pgedge starfleet managed database metrics <db-id> \
            --window 15,minutes

    The series is one column per metric, around thirty-six of them,
    and one row per sample. Database size sits in it beside container
    CPU and memory. Text output cannot print a table that wide, so it
    transposes one sample into METRIC and VALUE columns. That sample
    is the newest complete one, not the newest. The trailing
    bucket is often still being scraped, so its container metrics can
    arrive empty. The time row names the sample you were given.
    Choose `-o json` for every sample in the window.

    `--window` takes a value and a unit, comma-separated. A window
    that ends before the newest published sample returns nothing, and
    a longer `--window` may reach one.

2.  Check whether a replication slot is holding write-ahead log:

        pgedge starfleet managed database inspect <db-id> \
            replication-slots

    A slot fills a disk on its own, so read the slots before the
    tables. Each row carries the slot, its type, its plugin, its
    database, whether it is active, its wal_status and its
    retained_wal. The retained_wal column measures what the slot
    holds against the write position. It climbs for as long as
    nothing consumes the slot.

    An inactive slot with a large retained_wal is the cause, and the
    table reads that follow will only find noise. A disconnected
    replica that is coming back still needs its slot, so read
    [Measuring Replication Lag](#measuring-replication-lag) before
    dropping one.

3.  Find which tables hold the space:

        pgedge starfleet managed database inspect <db-id> table-sizes

    Rows come back largest first by total size, with table size and
    index size split out beside that total.

4.  Look for space that is occupied but not yet reclaimed:

        pgedge starfleet managed database inspect <db-id> vacuum-stats

    Each row carries a table's live rows, its dead rows, and the time
    of its last manual and last automatic vacuum. A large dead-row
    count beside an old vacuum time is the pattern to look for.

5.  Estimate how much of the space is bloat:

        pgedge starfleet managed database inspect <db-id> bloat

    The bloat_estimate column comes from planner statistics instead
    of measurement. Read it as a ranking, not as a figure.

6.  List the indexes the planner barely uses:

        pgedge starfleet managed database inspect <db-id> \
            unused-indexes

    The analysis covers non-unique indexes scanned fewer than fifty
    times, largest first.

7.  Grow the database to a size from `pgedge starfleet managed size
    list`, when the earlier steps have ruled out a slot and a
    reclaimable table:

        pgedge starfleet managed database resize <db-id> \
            --size <size-name> --wait

    A resize is admissible only from `available`. A database in any
    other status, including one already `modifying`, refuses the
    resize at exit status 1 with a message saying the resource is
    busy. That message does not name the current status, so read it
    with `database get`.

    Check that your profile and `<db-id>` name the database you mean,
    because a resize cannot be undone. `--size` is checked against
    that list, and a name the list does not carry is refused at exit
    status 2 before anything is sent. The command prompts for
    confirmation, and `--force` skips that prompt in a script, not at
    a terminal. A non-interactive run without `--force` fails instead
    of prompting.

    Without `--wait` the command returns when the resize is accepted,
    not when it completes. `--wait-timeout` bounds the wait and
    defaults to 600 seconds. A resize that succeeds passes through
    `modifying` and settles on `available`. One that fails after the
    database was already up settles on `degraded`, and a second
    resize from `degraded` is refused, so take that database to
    [Getting Support](#getting-support).
    [Provision a managed database](provision.md) names the sizes and
    the writes that need `available`.

## Finding a Slow Query

A slow database is still answering, so the work is queued, not
refused. Read the sessions first, then the statements behind them.

1.  List the sessions running longest:

        pgedge starfleet managed database inspect <db-id> \
            long-running-queries

    The analysis covers active queries past five minutes. Each row
    carries the backend's pid, its duration, its user, its state and
    its query text.

2.  Find which of those sessions wait on a lock:

        pgedge starfleet managed database inspect <db-id> locks

    Each row pairs one waiting session with whoever holds the lock:
    blocked_pid, blocked_by, blocked_duration and blocked_query. An
    empty answer means nothing is blocked, and the cost is in the
    statements themselves.

3.  Rank the statements by where the time goes:

        pgedge starfleet managed database inspect <db-id> outliers

    `outliers` ranks statements by total execution time, which is
    where a cheap statement run often enough shows up. `calls` ranks
    the same statements by how often each was invoked. Both need the
    pg_stat_statements extension. Without it, both are refused at
    exit status 1 naming that extension. Both also read the total and
    mean execution-time columns, which that extension gained at
    version 1.8. A database upgraded in place may instead fail naming
    a missing column, and running ALTER EXTENSION pg_stat_statements
    UPDATE resolves that.

4.  Check whether a table is scanned instead of searched:

        pgedge starfleet managed database inspect <db-id> seq-scans

    Each row carries a table's sequential scan count, the rows those
    scans read, and its index scan count. The rows are ordered by
    sequential scans, and a large table high in that list wants an
    index.

5.  Take one statement into psql and plan it there:

        psql "<connection-string>" -c "EXPLAIN <statement>"

    EXPLAIN plans the statement without running it. EXPLAIN ANALYZE
    runs it, so it adds load and writes whatever the statement
    writes. `database inspect` runs one analysis from a closed list
    and issues no EXPLAIN of its own. Derive `<connection-string>`
    again with step 3 of
    [Tracing a Lost Connection](#tracing-a-lost-connection), because
    a string stored anywhere else may be stale.

## Measuring Replication Lag

Two places show a replica trailing its publisher: the sender's view of
the connection, and the slot the sender keeps for it. Read both,
because either one alone can look healthy.

1.  Read the sender's view of every connected replica:

        pgedge starfleet managed database inspect <db-id> \
            replication-lag

    Each row carries the client address, the application name and the
    connection state. The rest of the row holds the sent and replay
    positions, the write, flush and replay lag intervals, and the
    sync state.

2.  Compare the sent position against the replay position on each
    row. Equal positions mean the replica has caught up. A sent
    position larger than the replay position marks the distance left
    to close, and it is the reading to trust first.

3.  Read the state column before the three lag intervals. A sender in
    catchup leaves the write, flush and replay intervals empty, so an
    empty interval there is not a lag of zero. Those intervals fill
    only while writes flow and the replica keeps up.

4.  Read what the slot behind that replica retains:

        pgedge starfleet managed database inspect <db-id> \
            replication-slots

    An inactive slot beside an empty replication-lag answer means the
    replica is gone, not slow, and its retained_wal is filling the
    disk.
    [Recovering From a Full Disk](#recovering-from-a-full-disk) reads
    the space that costs.

5.  Read the subscribing side, where this database subscribes to
    another:

        pgedge starfleet managed database inspect <db-id> \
            subscriptions

    Each row carries the subscription, its worker pid, the received
    and latest end positions, and the times of the last message sent
    and received. These rows belong to the subscribing side, so a
    publisher answers with no rows here.

A replica that has gone and a database that never had one both leave
replication-lag empty. That empty answer settles nothing on its own,
and step 4 tells the two apart.

## Getting Support

These guides carry on where the steps above stop:

- [Health checks](../doctor.md) gives each `doctor` row its meaning,
  the cause behind each warning, and the remedy.
- [Error catalog](../error-catalog.md) indexes the messages these
  commands print on stderr.
- [Troubleshooting](../troubleshooting.md) is arranged by exit
  status.
- [Troubleshooting managed databases](troubleshooting.md) describes
  the failures particular to Managed.
- [Inspect a database](../inspect-a-database.md) describes each
  analysis, what it reads, and how to read its output.
- [Logs and metrics on a managed database](logs-and-metrics.md) sets
  out how a metrics window is chosen.

To escalate, open a request with pgEdge.
[Versions, uninstall and support](../support-versioning-and-uninstall.md)
gives the route, and names the output to attach.
