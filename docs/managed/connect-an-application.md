# Connecting an Application to a pgEdge Starfleet Managed Database

`connection-string` builds one libpq URI for a Managed database from
the connection object that `database get` returns. The command takes
flags for the role and the output format. Its output holds the live
password for that role in every format.

Four terms recur throughout:

- The connection object is the host, port, database name, user name
  and password the platform holds for one role.
- `app` is the built-in role that owns the database and every object
  the application creates.
- `admin` is the built-in role that creates roles and reads and writes
  every table.
- An allowlist is the list of source addresses the database's Postgres
  endpoint admits.

## Before You Start

You need:

- The database's full UUID, written `<db-id>` below. Run
  `pgedge starfleet managed database list` to read the UUID.
- A database that has finished being created. Run
  `pgedge starfleet managed database get <db-id>` to read the status,
  which is `available` when the database is ready.
- An allowlist rule admitting the address the application connects
  from. A Managed database accepts a new connection from no other
  address.

The application's address needs a rule of its own. Where no rule
matches, psql reports `SSL error: unexpected eof while reading`. The
message names TLS, and the cause is the allowlist.
Another client reports the same failure differently, so judge the
failure by how fast it arrives. A refused address fails at once and
does not time out.

Add a rule for the address the application connects from, as an IPv4
address or a CIDR block:

    pgedge starfleet managed database allowlist add <db-id> <address>

`--my-ip` stands in for a typed address. The flag allows the one the
API saw the command arrive from, which is not necessarily where the
application connects from. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) guide
describes reading the list and every command that changes it.

## Printing a Connection String

`connection-string` takes the database's full UUID and writes one line
to stdout. That line holds the role's live password. Send it to a
file instead of a terminal:

    umask 077
    pgedge starfleet managed database connection-string <db-id> \
        > uri.txt || exit 1

`umask 077` applies as the file is created. Write to a new path
instead of overwriting an existing file. The redirection creates
`uri.txt` before the command has run, so a refusal leaves an empty file
and no other signal. `|| exit 1` stops the next step from reading that
file. A run that prints a string leaves the file holding the URI and
nothing else:

    postgresql://<username>:<password>@<host>:<port>/<database>?sslmode=require

Every field in the URI comes from the connection object. Read the
port from what the command printed instead of assuming a value.

The command appends `sslmode=require` to every URI it prints. Keep the
query string on the end of the URI. A URI trimmed back to its host and
database name drops the setting and reports nothing. At `require` the
session is encrypted and the server's certificate is not checked. The
[Networking and TLS](../networking-and-tls.md) guide describes the
stricter verification modes.

The CLI percent-encodes the role name and the password before placing
them in the URI. A password containing `@`, `:`, `/` or `?` therefore
survives the round trip intact. The encoding also applies in the other
direction. A password read by eye out of a URI is still encoded. The
password is refused at login until it is decoded. An application that needs
the password alone reads it from `--format env` or from the connection
object. Both hold the password unencoded.

## Choosing the Role

`--user-type` names the role whose credentials the string holds, and
takes `admin`, `app` or app_read_only. Omit the flag and `app`'s
credentials come back. The long forms `application` and
application_read_only are also accepted. One call returns one role's
credentials, so an application needing two roles makes two calls.

Give an application `app`. `admin` holds more privilege than an
application needs, and no built-in role is a superuser. Give a client
that only reads app_read_only, which the database refuses every write
from.

Read `admin`'s string where a task needs it, such as creating a role:

    umask 077
    pgedge starfleet managed database connection-string <db-id> \
        --user-type admin > admin-uri.txt || exit 1

A role you create in SQL has no user type. The CLI prints no string
for it, and its password is yours to keep. The
[Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md)
guide describes creating such a role and granting it read-only access
to `app`'s tables.

## Choosing an Output Format

`--format` shapes the text output and takes `uri` or `env`. `uri` is
the default, and any other value is refused at exit status 2.

`--format env` prints one shell assignment per line, matching the
shape of an env file:

    umask 077
    pgedge starfleet managed database connection-string <db-id> \
        --format env > .env || exit 1

A refusal leaves `.env` empty. `|| exit 1` stops an application
starting with no connection settings. The file holds six assignments:
`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` and
`PGSSLMODE`. The CLI wraps each value in single quotes and escapes a
quote inside a value. A password holding a space, a `$` or a quote
survives. Those are the six variables psql reads. The file carries
plain assignments with no `export`, so a shell that sources the file
keeps the six variables to itself. A command the shell launches sees
none of them. The
[ORM and framework integration](../orm-and-frameworks.md) guide carries
the export step and the psql check.

The global `-o json` and `-o yaml` flags outrank `--format`. With
either one the command prints an object holding `uri` next to the
fields that produced it: `host`, `port`, `database`, `username`,
`password` and `sslmode`. All seven sit at the top level of that
object. A caller reads the whole URI or the separate fields with no
second call. The
[Output formats and paging](../output-and-paging.md) guide describes
both machine-readable formats.

The [ORM and framework integration](../orm-and-frameworks.md) guide
describes where each framework reads the URI, and how psql reads the
`PG*` variables the env format writes.

## Handling the Password

The output holds a live password in every format. Where a password is
present and stdout is attached to a terminal, the CLI prints one
warning line on stderr naming the role and `--no-password`. Redirected
into a file or a pipe, the command prints the output alone.

`--no-password` leaves the password out, which is what a document, a
log or a shared example needs. Under `--format env` the whole
`PGPASSWORD` assignment is dropped, not emptied, so a password the
shell already exports survives. Under `-o json` and `-o yaml` the
`password` key is absent, not empty.

Three rules keep the password out of places that outlive it:

- Write the output to a file under `umask 077`. Delete any file that
  was only a step on the way to the application.
- Keep the password off every command line, because ps shows an
  argument list to anyone on a shared host.
- Keep the output out of a build log. A job that echoes the object, or
  runs with tracing switched on, leaves the password in CI output that
  outlives the job.

The [CI and automation](../ci.md) guide describes where the
credentials the CLI itself reads should live when it runs unattended.

A rotation replaces the password and prints no replacement. Every
copy of a string stops authenticating when the new password takes
effect. Derive the string again after a rotation instead of editing
the copy an application holds. The first reconnection may still be
refused, and the old password may still be accepted. Retry that
attempt and do not treat it as final. The
[Rotating a Password on a pgEdge Starfleet Managed Database](rotate-credentials.md)
guide describes the rotation itself.

`ALTER ROLE app PASSWORD` runs without error and takes effect, so
nothing warns you. The platform keeps its own copy of the three
built-in passwords, and that copy is what `connection-string` prints. After a
password change in SQL, the command returns a string that no longer
logs in. A rotation sets the two copies back in step.

## Reading the Connection Object

`connection-string` assembles the URI, so nobody has to extract the
object's fields by hand. Read the object directly for a field
`connection-string` never prints, such as `external_ip_address`, or to
assemble a URI yourself.

`database get` returns the object under `-o json` or `-o yaml`, and
takes the same `--user-type` flag. Text output renders no part of the
object and holds no password:

    umask 077
    pgedge starfleet managed database get <db-id> -o json > db.json \
        || exit 1
    jq -e -r '.connection | .host, .port, .database, .username' \
        db.json || exit 1

`jq` exits 0 on a selection that matched nothing, printing `null` for
each field instead. `-e` takes the exit status from the last value
printed, which turns that `null` into exit status 1. A `host` missing
on its own still exits 0, because `username` is the last value
printed.

Four fields are on the object of every database: `username`,
`password` and `database` hold strings, and `port` holds an integer.
`host` and `external_ip_address` are optional, and an unset field is
missing, not empty. Test for `host` instead of assuming it.
`connection-string` refuses on a missing `host`, so any database the
command prints a string for has one. No connection object exists on a
database still being created. A `domain` field and a second `port` sit
on the database outside the object. Build the connection from the
object's own fields.

Assembling a URI out of the object leaves the percent-encoding to you.
A password containing `@`, `:`, `/` or `?` breaks the URI into
different parts when copied in unencoded. The address that broken URI
then names is not the one you meant. The `@uri` filter encodes the two
fields that need encoding. `select` stops a database with no
connection object producing a URI of `null` values:

    jq -e -r '.connection | select(.host != null)
        | "postgresql://\(.username|@uri):\(.password|@uri)"
        + "@\(.host):\(.port)/\(.database)?sslmode=require"' \
        db.json || exit 1

The
[Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
guide describes writing the credentials for both roles into `.pgpass`
entries. Neither password then reaches a command line.

## Troubleshooting

`connection-string` prints each refusal below on stderr, and writes
nothing to stdout when it refuses.

| Message | Exit status | What it means |
|---|---|---|
| `database <id> has no connection host yet; it is still being created, or its status is not available` | 1 | The database has no address yet. Read its status until it reaches `available`. |
| `--user-type given an empty value: name a role (admin, app or app_read_only), or omit the flag to use app` | 2 | The flag arrived with nothing after it. |
| `unknown user type "<value>" (expected one of: admin, app, app_read_only)` | 2 | The value names no built-in role. |
| `--format "<value>" is not one of uri or env` | 2 | The format vocabulary is two words, `uri` and `env`. |
| `--format env refused: the connection block carries a control character, which cannot be written as a shell assignment` | 1 | A value cannot be written as a shell assignment. Pass `uri` or `-o json` instead. |
| `invalid database ID "<value>": <reason>` | 2 | The argument is not a full UUID, and a prefix is not resolved. |
| `resource not found (404): <body>` | 4 | No database on this account has that ID. Read the UUID again from `database list`. |

The [Exit codes](../exit-codes.md) guide describes the status
contract, and the [Error catalog](../error-catalog.md) guide
collects the messages readers meet most often.

A refusal from this command differs from a connection that Postgres
turns away. The
[Troubleshooting managed databases](troubleshooting.md) guide
describes a refused connection.

## Next Steps

- [ORM and framework integration](../orm-and-frameworks.md) describes
  the one place each framework reads a Postgres URL.
- [Networking and TLS](../networking-and-tls.md) describes what
  `require` proves and what a stricter mode adds.
- [Running a Schema Migration on a pgEdge Starfleet Managed Database](schema-migrations.md)
  describes a migration tool reading the string.
- [CI and automation](../ci.md) describes unattended runs.
