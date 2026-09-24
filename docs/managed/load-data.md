# Loading a Schema and Data into a pgEdge Starfleet Managed Database

A Postgres schema and its data reach a Managed database by restoring a
dump you already hold. Restore as `app`. That way, every object
belongs to the role the application and its migrations connect as.

`admin` and `app` both hold CREATE on the database. Either one can
create a schema or an object in `public`. An object belongs to the
role that created it. Neither role is a superuser. A restore recipe that
depends on a superuser statement therefore fails.

Two role names recur on this page:

- `admin` is the built-in role that installs the extensions the
  platform reserves.
- `app` is the built-in role that owns the database and the objects an
  application creates.

## Before You Start

You need:

- The database ID, `<db-id>` below. Run
  `pgedge starfleet managed database list` to find it.
- A database reporting `available`. An allowlist write is refused at
  any other status. A database still being created has no host
  yet, so `connection-string` exits with status 1 against one. The
  [Provision a managed database](provision.md) page describes reaching
  that point.
- A custom-format dump of the source, taken with
  `pg_dump -Fc -f source.dump` and the source's own connection flags.
- psql, pg_restore and jq on the machine you load from.
- An allowlist rule for the address your Postgres client connects from.

A Managed database admits a connection only from an address on its
allowlist. Nothing connects until you allow the address you load
from. A psql connection to a closed database fails within a second
with `SSL error: unexpected eof while reading`. It does not fail with
a timeout. That message reads as a certificate fault and is a
firewall one. Allow the address you connect from:

    pgedge starfleet managed database allowlist add <db-id> \
        --my-ip --wait

The change is asynchronous, so `--wait` blocks until the rule is in
force. Without it, exit status 0 means only that the API accepted the
change. A connection made straight afterward fails the same way a
closed database does. `--my-ip` adds the address the API sees this
command arriving from. That may not be where your Postgres client
connects from. Verify by connecting. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) page
describes every allowlist command.

## Capturing Credentials for Both Roles

`database get` returns one role's connection block, chosen with
`--user-type`, and `app` when the flag is omitted. The text output
holds no password, so read the block under `-o json`. The CLI hands out
credentials for `admin` and `app`, and the load needs both.

Write both passwords into a `.pgpass` file. This keeps them out of any
argument list or the environment:

    umask 077
    pgedge starfleet managed database get <db-id> --user-type admin \
        -o json > admin.json || exit 1
    pgedge starfleet managed database get <db-id> --user-type app \
        -o json > app.json || exit 1
    jq -r '.connection
        | "\(.host):\(.port):\(.database):\(.username):\(.password)"' \
        admin.json app.json >> ~/.pgpass
    chmod 600 ~/.pgpass
    DB_HOST=$(jq -r '.connection.host' app.json)
    DB_PORT=$(jq -r '.connection.port' app.json)
    DB_NAME=$(jq -r '.connection.database' app.json)
    ADMIN_USER=$(jq -r '.connection.username' admin.json)
    APP_USER=$(jq -r '.connection.username' app.json)
    rm -f admin.json app.json

`umask 077` keeps the captured files private. `|| exit 1` stops the
capture on a failed read rather than four steps later. The client
ignores `~/.pgpass` unless its permissions exclude group and world
access, which `chmod 600` does. Both roles use the same host, port and
database name. Only the username and the password differ.

A `.pgpass` entry is a live password on disk. It stops authenticating
the moment that role's password is rotated. Delete the two entries
when the load is finished. Capture them again after a rotation rather
than editing them.

`pgedge starfleet managed database connection-string <db-id> --user-type app`
prints that role's URI instead, holding the host, the port, the
database name, the username, the password and `sslmode=require`.

## Installing the Extensions the Schema Needs

Which role can install an extension depends on the extension, and not
on what the extension does. A privileged extension is one the platform
reserves, and a trusted extension is one Postgres itself marks trusted.
The [Installing Supported Extensions on a pgEdge Starfleet Managed
Database](extensions.md) page lists every supported extension and the
role that installs it.

| Set | Examples | Which role installs it |
|---|---|---|
| Privileged | vector, postgis, postgis_raster, postgis_sfcgal | `admin` only. The extension is then owned by `postgres`. |
| Trusted | pgcrypto, citext, hstore, ltree, pg_trgm | Either role. The installing role owns it, and a migration can only manage an extension its own role owns, so install these as `app`. |
| Neither | dblink, file_fdw, postgres_fdw, amcheck | No role on the database. |

A schema that depends on an extension no role can install cannot load.
Drop the dependency at the source first. A fresh database ships
plpgsql, pg_stat_statements and pgaudit installed, and vector available
but not installed. Install everything the schema
depends on before the restore. A schema loaded before its extensions
fails on the first object that needs one.

Install a privileged extension as `admin`:

    psql -h "$DB_HOST" -p "$DB_PORT" -U "$ADMIN_USER" -d "$DB_NAME" \
        -c 'CREATE EXTENSION vector'

Install a trusted extension as `app`. Or let the migration's own
`CREATE EXTENSION IF NOT EXISTS` statement install it:

    psql -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" -d "$DB_NAME" \
        -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto'

`Must be superuser to create this extension` is the only permission
refusal an install produces. Seen as `app`, the message means the
extension is privileged or superuser-only. Run the statement again as
`admin`. As `admin`, the same message means no role on the database
can install the extension.

## Restoring the Dump in One Pass

A dump holding both the schema and the data restores in one pass. A
full restore loads the rows before it creates the foreign keys. The
tables therefore need no ordering.

1. Drop the dump's own extension entries, because the restore would
   otherwise create an extension `admin` owns again as `app` and fail
   with `must be owner of extension`:

        pg_restore -l source.dump | grep -v 'EXTENSION' > source.list

2. Restore as `app`. Do this without the source's owners and grants:

        pg_restore -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" \
            -d "$DB_NAME" --no-owner --no-privileges \
            -L source.list source.dump

    `--no-owner` and `--no-privileges` are needed because the dump
    names roles that do not exist on a Managed database. The
    restore's exit status is 0 only when every entry in the list
    applied.

## Loading Data into a Schema That Already Exists

A schema can be loaded on its own, with
`pg_restore --schema-only --no-owner --no-privileges`, or built by a
migration tool. Either way, its foreign keys already exist before any
row arrives. Those keys are enforced for the whole load, and the load
needs an order.

`pg_restore --disable-triggers` cannot remove that enforcement on a
Managed database. The flag emits `ALTER TABLE ... DISABLE TRIGGER ALL`,
which is superuser-only, and so is the usual alternative
`SET session_replication_role = replica`. Every disable statement and
every re-enable statement errors.

pg_restore writes table data in the dump's table-of-contents order, and
not in the order of the `-t` flags. A child table can therefore be
loaded before its parent. Its COPY then aborts on the foreign key.
pg_restore continues with the rest of the restore.

The result is exit status 1, most tables populated, and one table
silently empty.

Read the foreign keys the load has to satisfy, and the definition of
each one. Do this before choosing an approach:

    psql -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" -d "$DB_NAME" \
        -c "SELECT conrelid::regclass, conname,
            pg_get_constraintdef(oid) FROM pg_constraint
            WHERE contype = 'f'"

Two approaches avoid the silent loss. Both end the same way. The
first loads the parent tables in an earlier pass than the children. It
uses one pass for each level of the hierarchy. Tables with no foreign
key between them go in the same pass:

    pg_restore -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" \
        -d "$DB_NAME" --data-only --no-owner --no-privileges \
        -t parent_table -t other_parent source.dump
    pg_restore -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" \
        -d "$DB_NAME" --data-only --no-owner --no-privileges \
        -t child_table source.dump

`app` owns the foreign-key constraints, because `app` created the
schema. The second approach drops each constraint as `app` and loads
the data in one pass. It then adds each one back, from the definition
the query printed:

    ALTER TABLE <table> DROP CONSTRAINT <constraint>;
    ALTER TABLE <table> ADD CONSTRAINT <constraint> <definition>;

## Checking the Load

Check the result whichever path you took. A pg_restore that ends with
exit status 1 has still written everything that did not error. A
restore that mostly succeeded is therefore not a result to act on.
Read the errors first.
Compare row counts with the source, table by table. Then check that
the sequences moved with the rows. psql's `\ds` lists the sequences in
your search path:

    psql -h "$DB_HOST" -p "$DB_PORT" -U "$APP_USER" -d "$DB_NAME" \
        -c 'SELECT count(*) FROM <table>' \
        -c 'SELECT last_value FROM <sequence>'

A custom-format dump holds the sequence values, so the check confirms
that the restore applied them.

## Next Steps

- Add a role beyond the two the CLI hands you credentials for:
  [Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md).
- Every schema change after this first load has its own workflow:
  [Running a Schema Migration on a pgEdge Starfleet Managed Database](schema-migrations.md).
- Back up the database when the data is in:
  [Backing up and Restoring a pgEdge Starfleet Managed Database](backup-restore.md).
- Change the two passwords the load used:
  [Rotating a Password on a pgEdge Starfleet Managed Database](rotate-credentials.md).
- Logical replication keeps the source live while the copy catches up:
  [Migrating an Existing Database to a pgEdge Starfleet Managed Database](migrate-into-managed.md).
