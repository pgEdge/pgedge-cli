# Rotating a Password on a pgEdge Starfleet Managed Database

`rotate-password` issues a new password for a built-in role on a
Managed database. The rotation breaks every session still connecting
with the old password. The command prompts for confirmation before
it runs.

Rotating a password on a Managed database involves three terms.

- `admin`, `app` and app_read_only are the roles a Managed database
  delivers. app_read_only is the read-only role the RAG service
  connects as, and the MCP service unless its writes are allowed.
- A reconcile is the platform resetting `admin`'s memberships to the
  platform's own list.
- A role manager is a role you create with CREATEROLE, from which
  every other role is created.

## Before You Start

You need the database's full UUID and a database reporting `available`.

- Run `pgedge starfleet managed database list` to find the database's
  ID. `rotate-password` takes a full UUID. An ID prefix or a
  database name is refused at exit status 2, before any request is
  sent.
- A rotation needs status `available`. A database in any other
  status, including one already `modifying` from an earlier change,
  refuses the rotation.

A rotation reconciles the database. The reconcile costs you
permanent control of every role `admin` created directly. At the
reconcile, `admin` loses the ADMIN OPTION on each of those roles.
`admin` then can no longer grant, drop, or alter anything about those
roles except their passwords. Each role keeps logging in and retains
its grants, so nothing warns you.

A role manager retains the ADMIN OPTION on every role it creates,
across every reconcile. That holds because a role manager is not a
platform role. Only control of a role `admin` created directly is lost.
`admin` keeps the ability to set that role's password.

[Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md)
describes creating a role manager. It also describes creating every
other role from it.

`ALTER ROLE app PASSWORD` also changes a built-in password and runs
without error. It leaves the platform's stored copy wrong.
[Recovering a Password Changed in SQL](#recovering-a-password-changed-in-sql)
describes the repair.

## Rotating a Password

A rotation is asynchronous. The command exits 0 when the API accepts
the rotation, not when the running database uses the new password.

1.  Confirm that the database reports `available`:

        pgedge starfleet managed database get <db-id> -o json

    A rotation against any other status is refused at exit status 1.
    The refusal's message says the resource is busy. Retry the
    operation when the resource settles. Do not read the current
    status out of that message. Read the status with `database get`
    instead.

2.  Rotate the role's password, waiting for the rotation's task:

        pgedge starfleet managed database rotate-password <db-id> \
            --role app --wait

    `--role` is required and takes `admin`, `app` or app_read_only.
    A value outside that list is refused at exit status 2, before any
    request is sent. The command prompts for confirmation, naming the
    role it is about to rotate. The prompt is where you check you
    named the role you meant:

        Rotate the "app" password on database <db-id>? Existing
        connections using the old password will fail.

    `--force` skips that prompt.

    `--wait` blocks until the rotation's task reaches a terminal state.
    `--wait-timeout` bounds that wait and defaults to 600 seconds.
    `--wait-interval` sets the interval between status checks and defaults to 5
    seconds. The command exits 0 when the task succeeds and 3 on timeout. A
    failed rotation leaves its task non-terminal rather than `failed`. A
    rotation that fails then reaches the timeout instead of exiting at exit
    status 1. A succeeded task means the new credential reached the running
    database.

    `--wait` is the reliable finish signal. A rotation moves the
    status to `modifying` and back within seconds. A `database get`
    started a few seconds late reads `available` and cannot tell
    finished from not started.

    As soon as the API accepts the rotation, before `--wait` begins, the
    command writes `Password rotated for role "<role>" on database <id>.`
    to stderr. That line appears even when the task then fails or times
    out, so read the exit status for the outcome. Stdout stays empty in
    text, JSON and YAML output.

3.  Read the new password back, when the rotation has finished.

        pgedge starfleet managed database get <db-id> \
            --user-type app -o json

    `--user-type` names the role whose credentials the response
    holds, so pass the role you rotated. The flag takes `admin`,
    `app` or app_read_only, and `app` when the flag is omitted. The response holds the
    password at `connection.password`.

    Until the status turns `available`, `database get` returns the
    old credential. The rotation never prints the new password.

    Rotating app_read_only also restarts the RAG service, and the MCP
    service unless its writes are allowed.

## Switching an Application Over

Switch an application over when the database reports `available`
again. Re-derive each connection string rather than editing the copy
in the application's own configuration.

    pgedge starfleet managed database connection-string <db-id> \
        --user-type app

In a project folder linked to the database, run `pgedge env pull
--user-type <role>` instead, naming the role you rotated. It rewrites
`DATABASE_URL` in `.env` with the new password.

The new credential may still be refused briefly, while the old one is
still accepted. Retry the first reconnection instead of treating that
attempt as final.

Rotating a role restarts the services that connect as it, which
[Deploy managed services](services.md) describes. RAG connects as
app_read_only, and so does MCP unless its writes are allowed, when it
connects as `app`. The database
may hold the new credential while those services still use the old
one. When a restarted service begins using the new credential is
unknown. Retry a service call that fails soon after a rotation.

## Rotating a Password From a Script

`--force` skips the confirmation prompt. Without `--force` and with
no terminal on stdin, the command refuses at exit status 2. It does
not wait for an answer.

Without `--wait` or `--follow`, exit status 0 means only that the API
accepted the rotation and started the work. Text output then prints
how to monitor the task on stderr. `-o json` and `-o yaml` stay
silent so that stdout is parseable.

`--follow` blocks like `--wait`. It streams the task's step messages
to stderr, one line per step transition with the step name, status
and progress percentage.

`--dry-run` runs every client-side check, then stops before sending
the write. It reports the request that would have been sent. The
checks that read the API do run, so a dry run needs credentials. No
server-side validation happens. A dry run never prompts for
confirmation.

## Diagnosing a Stalled Rotation

A rotation that fails leaves its task non-terminal instead of
`failed`. A `--wait` timeout at exit status 3 then means the outcome
is unknown, not failed. The database may already hold the new
credential. Do not rotate again after a timeout. A second
rotation risks replacing a credential already in place.

The rotation returns no task ID, so find the task by name.

    pgedge starfleet managed task list --subject-id <db-id> \
        --name rotate-password-managed

Read that task's timestamps, not its status. A stuck task and
a running one both report `running`.

    pgedge starfleet managed task get <task-id> -o json

Compare `updated_at` against the current time, not against
`created_at`. A rotation completes within seconds, so a task still
`queued` or `running` whose `updated_at` is minutes old has stopped
progressing.

To find out which credential the database holds, read the role's
password back with `database get`. Then try connecting with it, using
psql. No command in this CLI makes that connection attempt. That
connection needs an allowlist rule for the address you make it from.
Otherwise the connection fails within a second with `SSL error:
unexpected eof while reading`, which says nothing about the password.
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) describes
adding one.

The published contract says that a failed rotation leaves the
database `degraded`. It also says a failed rotation refuses another
rotation until the database is recovered. That is the contract's
claim, not confirmed behavior.

## Recovering a Password Changed in SQL

`ALTER ROLE app PASSWORD` runs without error, from `admin` or from
`app`, and takes effect, so nothing warns you. The platform keeps its
own copy of `admin`'s and `app`'s passwords. That copy is what
`database get` and `database connection-string` return. After a
password changed in SQL, both commands hand back a password that no
longer logs in.

`rotate-password` sets a fresh password and updates the stored copy.
That is how a password changed in SQL is recovered.

A role you created yourself has no copy on the platform. An
`ALTER ROLE` statement is the only way to change its password. Run
the `ALTER ROLE` statement in psql, from the role manager that
created the role, or from `admin`. `admin` may lose the ADMIN OPTION
on a role.
Setting that role's password is the one thing `admin` can still do
to it.

## Next Steps

The following guides continue from a rotation.

- [Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md)
  describes creating a role manager. It also describes keeping the
  ability to alter a role.
- [Connecting an Application to a pgEdge Starfleet Managed Database](connect-an-application.md)
  describes the connection strings the CLI builds for the built-in
  roles.
- [Tasks and async operations](../tasks-and-async.md) describes
  reading a task.
- [Troubleshooting managed databases](troubleshooting.md) describes a
  refused connection and a failed asynchronous operation.
