# Troubleshooting managed databases

A managed database produces symptoms the exit code alone does not
name. The [Troubleshooting](../troubleshooting.md) page carries the
exit-code contract itself. Four symptoms follow:

- [A refused connection to a new database](#a-refused-connection-to-a-new-database)
- [A database degraded after an allowlist change](#a-database-degraded-after-an-allowlist-change)
- [A failed asynchronous operation](#a-failed-asynchronous-operation)
- [An empty log or metrics read](#an-empty-log-or-metrics-read)

An allowlist is the list of source addresses that may open a new
connection to one endpoint of the database. A task is the platform's
record of one asynchronous operation. The task holds the outcome the
database's own status does not. Find `<db-id>` with
`pgedge starfleet managed database list`. `allowlist add` is the only
command below that changes the database.

## A refused connection to a new database

A new database starts closed: it has no allowlist rule, so no address
can open a connection until you add one. A psql connection to the
closed database fails within a second, with exit status 2 and the
message `SSL error: unexpected eof while reading`. That message means
the address matches no allowlist rule. TLS did not fail, and the
attempt was not a timeout. Check the allowlist first:

    pgedge starfleet managed database allowlist get <db-id>

The State line reads `closed`, `restricted` or `open`. `closed` means
the list has no rules and nobody can connect. That is not a fault.
`restricted` means only an address matching a rule connects, so
compare the client's address with the rules listed. Allow the CLI's
own machine, or the client's address as an IPv4 address or CIDR
block:

    pgedge starfleet managed database allowlist add <db-id> --my-ip
    pgedge starfleet managed database allowlist add <db-id> <address>

`--my-ip` adds the address the API received this command from. That
address is not always the address your Postgres client connects from,
so verify by connecting. The change needs the database `available` and
moves it through `modifying`. A service deployed on the database has
its own allowlist, read and changed with `--service <type>`, where
`<type>` is `mcp`, `rag` or `postgrest`. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) page
describes every allowlist command.

## A database degraded after an allowlist change

An allowlist change settles the database at `available` or at
`degraded`, and `pgedge starfleet managed database get <db-id>` shows
which. `degraded` means the platform failed to push the new rules to
the ingress. The ingress is the network entry point that enforces
them. The database holds no reason. The reason is on the task. The
first command below lists the database's tasks newest first, and
`<task-id>` for the second is its ID column. The second prints the
failure reason on a line of its own:

    pgedge starfleet managed task list --subject-id <db-id>
    pgedge starfleet managed task get <task-id>

A change to the Postgres endpoint's list runs as a task named
`update-managed-ip-allowlists`. `degraded` is terminal: the database
will not recover from it on its own. Escalate the failure reason to
pgEdge support, using the route the
[Versions, uninstall and support](../support-versioning-and-uninstall.md)
page gives. The
[Tasks and async operations](../tasks-and-async.md) page describes
reading a task.

## A failed asynchronous operation

One managed command needs more care after a timeout than finding the
task and reading its state:

- A managed `rotate-password` that timed out. Do not re-rotate that
  command: the failed task never turns terminal, so the timeout means
  the outcome is unknown rather than failed.

A timed-out rotation returns no task ID, so find it by name, then
check its timestamps, not its status:

    pgedge starfleet managed task list --subject-id <db-id> \
        --name rotate-password-managed
    pgedge starfleet managed task get <task-id>

Compare `updated_at` against the current time, not against
`created_at`. A rotation completes within seconds, so a task whose
`updated_at` is minutes old has stopped instead of running long.

Confirm whether the password itself changed by reading the role's
current credentials back, instead of re-rotating:

    pgedge starfleet managed database get <db-id> --user-type admin
    pgedge starfleet managed database get <db-id> --user-type app
    pgedge starfleet managed database get <db-id> --user-type app_read_only

The [Tasks and async operations](../tasks-and-async.md) page describes
that command, and the `task list`, `get` and `wait` commands Managed
offers.

## An empty log or metrics read

Two managed cases account for most empty reads:

- A metrics window shorter than the lag before the collector posts a
  bucket. The managed collector posts a bucket every 30 seconds. The
  newest bucket lags behind the wall clock, so a one-minute window
  ends before any sample exists. Set `--window` on `database metrics`
  to `3,minutes` or more.
- A metric can be null across the whole window. The managed API omits
  that metric from the response instead of sending it as null. The
  table is shorter rather than blank, and nothing in the answer names
  what is missing.

The
[Reading Logs and Metrics from a pgEdge Starfleet Managed Database](logs-and-metrics.md)
page describes both.
