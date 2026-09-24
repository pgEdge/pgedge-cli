# Running a Schema Migration on a pgEdge Starfleet Managed Database

You make a schema change on a Managed database with the migration
tool you already run. The CLI ships no migration command, so you rely
on that tool instead. You run one CLI command to get the connection
string your tool points at. You run your own migration tool
separately. Then you run another CLI command yourself to read the
database back when the tool finishes. Alembic, Flyway, Prisma Migrate
and Rails migrations are migration tools in that sense. So is a
directory of numbered SQL files run through psql.

Every schema change after the first load belongs here. The
[Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
describes the first load.

Three terms recur throughout this page:

- `app` is the built-in role that owns the database and the objects
  the application creates.
- `admin` is the built-in role that creates roles and installs the
  extensions the platform reserves.
- A migration is one schema change your own tool applies over a
  Postgres connection.

## Before You Start

You need:

- The database ID, `<db-id>` below, which is a full UUID. Run
  `pgedge starfleet managed database list` to read it.
- A database reporting `available`. Run
  `pgedge starfleet managed database get <db-id>` to read the status.
- The address the migration connects from on the database's
  allowlist, and the address you run `inspect` from, which opens its
  own connection. A Managed database admits a new connection only
  from an allowed address. Each of those addresses then needs a rule.
  The [Controlling Network Access to a pgEdge Starfleet Managed
  Database](network-access.md) page
  describes allowing one.

Two migration statements succeed with no error and cost you later:

- `CREATE ROLE` run over an `admin` connection, not a role manager,
  costs you control of the new role.
- `ALTER ROLE app PASSWORD` changes the password Postgres accepts and
  leaves the platform's stored copy wrong.

## Choosing the Role to Migrate As

Run the migration as `app`. The role in the connection string owns
every object the migration creates, and `app` is the role the
application connects as.

`app` also owns any trusted extension the migration installs, and a
migration can manage only an extension its own role owns. Connect as
`admin` only for the extensions the platform reserves to `admin`. The
[Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
describes which extension installs as which role.

A role the migration creates over an `admin` connection is a role the
platform's next reconcile orphans. `admin` loses the ADMIN OPTION on
it, so nothing on the database can alter, grant or drop that role. The
role keeps logging in, so nothing warns you. A role of your own
holding CREATEROLE is the role manager that avoids this. The
[Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md)
describes creating one.

`ALTER ROLE app PASSWORD` runs without error and takes effect. As a
result, nothing warns you that the password the CLI hands out no
longer logs in. `pgedge starfleet managed database rotate-password`
is what changes a built-in password.

## Reading the Connection String

A Managed database presents one connection covering the whole
database. One command prints it:

    pgedge starfleet managed database connection-string <db-id>

Text output is a single libpq URI, in the form
`postgresql://<username>:<password>@<host>:<port>/<database>?sslmode=require`.
The username and password are percent-encoded. As a result, a
password holding `@`, `:`, `/` or `?` produces a URI that parses back
to what was meant. A tool that takes one URL takes this default
output.

The command has three flags of its own: `--format`, `--no-password`
and `--user-type`. `--format` sets `uri`, the default, or `env`.
`--user-type` sets the role, `admin`, `app` or app_read_only, and
defaults to `app` when the flag is omitted. An empty `--user-type` is
exit status 2, not the default. Do not point a migration at
app_read_only, because the database refuses every write that role
attempts.

The output carries a live password in every format. When stdout is a
terminal, the CLI warns once on stderr. Into a pipe or a file, the
CLI prints only the output. `--no-password` leaves the password out,
for a document or a paste.

Passing a URI on a command line puts the password where every process
on the host can read it. Write the output to a file the tool reads
instead, and delete the file afterward.

`--format env` prints one single-quoted `PG*` assignment per line:
`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` and
`PGSSLMODE`. psql and anything else built on libpq read those
variables out of the environment. Those tools then need no connection
argument. With `--no-password`, the `PGPASSWORD` line is absent, not
empty, so a shell that already exports a password retains it.

Set a restrictive umask and capture that output to a file. Stop on a
failed read, so a mistyped ID surfaces here instead of part way
through the migration:

    umask 077
    pgedge starfleet managed database connection-string <db-id> \
        --format env > db.env || exit 1
    set -a
    . ./db.env
    set +a
    rm -f db.env

The printed lines carry no `export` keyword. `set -a` is what puts
the values in the environment psql inherits instead. Sourcing the
file without it leaves psql with no connection details at all.

## Running the Migration

With the `PG*` variables in the environment, the migration tool needs
no connection argument. This runs one SQL file as `app`:

    psql -f 001_add_orders_table.sql

A tool that does not read the `PG*` variables takes the URI instead.
The same command prints that URI under the default `--format uri`.

`rotate-password` takes a required `--role` and changes that one
role's password. A captured file or URI stops authenticating when the
role it carries is rotated. Read the connection string again after a
rotation rather than editing the file you captured.

The database ID comes first and `--role` follows it. This rotates
`app`'s password and prompts for confirmation, since rotation
invalidates any connection still using the old password:

    pgedge starfleet managed database rotate-password <db-id> --role app

`--force` skips the prompt, for a script that already accounted for
the invalidated connections.

## Reading the Database Back

One command runs a read-only diagnostic against the database and
writes nothing:

    pgedge starfleet managed database inspect <db-id> table-sizes

`table-sizes` and `index-sizes` list the tables and indexes the
migration left. `unused-indexes` and `seq-scans` report how the
application reads them. The command connects with the string
`connection-string` prints, so `--user-type` chooses `admin` or `app`
here as well. `--user-type` follows the analysis. This runs
`unused-indexes` as `admin` instead of the default `app`:

    pgedge starfleet managed database inspect <db-id> unused-indexes \
        --user-type admin

The [Inspect a database](../inspect-a-database.md) describes every
analysis.

## Troubleshooting

- A connection that fails within a second with
  `SSL error: unexpected eof while reading` is an address the
  allowlist does not hold. The
  [Controlling Network Access to a pgEdge Starfleet Managed
  Database](network-access.md) page
  describes adding one.
- `connection-string` that exits with exit status 1 and a sentence on
  stderr, and no string, is a database whose connection block has no
  host yet. That is the state while the database is still being
  created.
- `--format env refused: the connection block carries a control
  character, which cannot be written as a shell assignment` is exit
  status 1. The value is refused rather than rewritten, so take the
  default `uri` output instead.

## Next Steps

- The
  [Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
  describes the first load, the `admin` and `app` roles and the pg_restore
  traps.
- The
  [Connecting an Application to a pgEdge Starfleet Managed Database](connect-an-application.md)
  describes every flag on `connection-string` and the connection
  object underneath it.
- The [CI and automation](../ci.md) describes running these commands
  from a pipeline.
