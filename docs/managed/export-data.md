# Exporting Data from a pgEdge Starfleet Managed Database

A Managed database exports through pg_dump. The dump is a file you hold. You
restore the dump into a Postgres you run yourself. No built-in role on a
Managed database is a superuser. The role you dump as, and the extensions the
dump names, together decide how the file behaves at the other end. The
following terms recur throughout:

- `admin` is the built-in role that reads and writes every table.
- `app` is the built-in role that owns the database and the objects an
  application creates.
- The target is the Postgres server you restore the dump into.

A platform backup is not an export route. The Managed backup commands
are `create`, `get`, `list` and `restore`, none of which downloads a
backup. A restore replaces the database's own data in place, so only a
dump gives you a file.

## Before You Start

You need:

- The database ID, `<db-id>` below. Run
  `pgedge starfleet managed database list` to find the database ID, a
  full UUID for which an ID prefix and a database name are both
  refused.
- A database reporting `available`. A database still being created has
  no host yet, so `connection-string` exits with status 1 against one.
  The [Provision a managed database](provision.md) guide describes
  reaching that point.
- An allowlist rule for the address your Postgres client connects from.

A Managed database admits a connection only from an address on its
allowlist. pg_dump cannot connect until you allow the address it runs
from. A psql connection to a closed database, and any client built on
the same libpq, fails within a second with `SSL error: unexpected eof
while reading`, not with a timeout. That message reads as a
certificate fault and is a firewall one. Allow the address your
client connects from:

    pgedge starfleet managed database allowlist add <db-id> \
        --my-ip --wait

The change is asynchronous. `--wait` blocks until the rule is in
force. Without `--wait`, exit status 0 means only that the API
accepts the change. A connection made straight afterward fails the
same way a closed database does. `--my-ip` adds the address the API
sees this command arriving from, which may not be where pg_dump
connects from. Verify by connecting. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) guide
describes every allowlist command.

## Reading the Connection Details

`database connection-string` prints one built-in role's credentials:

    pgedge starfleet managed database connection-string <db-id> \
        --user-type admin

`--user-type` takes `admin` or `app`. App's credentials come back
when the flag is omitted. `pgedge starfleet managed database get
<db-id> --user-type admin -o json` returns the same credentials.

`--format` sets `uri` or `env`, and defaults to `uri`. The `uri` form
is one line, `postgresql://user:password@host:port/db?sslmode=require`,
with the user name and the password percent-encoded. The `env` form
prints `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` and
`PGSSLMODE`, one per line, each value single-quoted for a POSIX shell.
`--no-password` leaves the password out, so the `PGPASSWORD` line is
absent rather than empty. Under `-o json` or `-o yaml` the URI comes
back alongside the parts it was built from.

## Taking the Dump

Take the dump as `admin`, which reads every table whatever role owns
it. pg_dump run as `app` fails with `permission denied for table` at
the first table `app` cannot read.

1. Set a restrictive umask. Write admin's environment to a file,
   rather than reading the values on the terminal. Stop on a failed
   read. A mistyped ID shows here, not three steps later:

        umask 077
        pgedge starfleet managed database connection-string <db-id> \
            --user-type admin --format env > admin.env || exit 1

    The CLI warns on stderr, naming the role, whenever it prints a
    password to a terminal. Redirecting to a file suppresses that
    warning, because stdout is then not a terminal.

2. Load the variables into your shell. Then delete the file:

        set -a
        . ./admin.env
        set +a
        rm -f admin.env

    pg_dump reads those variables from the environment. The password
    never reaches an argument list.

3. Write a custom-format dump of the whole database:

        pg_dump -Fc -f export.dump

    The custom format holds a table of contents that pg_restore can
    list and edit. The custom format restores into a database of any
    name.

4. Capture the roles, which a single-database dump does not hold.
   Take the file now. The target retains the dump's ownership and
   grants only when those roles exist there:

        pg_dumpall --globals-only --no-role-passwords \
            -l "$PGDATABASE" > roles.sql

    Roles are cluster-wide, so they sit outside the dump. `roles.sql`
    recreates the built-in roles, any role you added and their
    memberships. `--no-role-passwords` leaves out the password hashes
    `admin` can read.

`export.dump` holds the whole database and `roles.sql` names every
role, so protect both files as you would a credential.

## Understanding What the Dump Holds

The dump holds the schema, the data, the extension declarations, the
object ownership and every grant on the objects it holds. None of
those statements grants anything on the database itself, or on the
roles they name.

pg_dump emits `CREATE EXTENSION` for every non-system extension
installed on the database. Every Managed database has plpgsql,
pg_stat_statements and pgaudit installed, but plpgsql is a system
extension that pg_dump skips. Every dump names the other two, and
anything you installed yourself. vector is available on the platform
and is not installed on a fresh database.

An extension the target does not have fails on its own entry. Where
nothing in the schema uses that extension, the failure costs you an
error to ignore. A column whose type comes from that extension
(vector is the case to watch) leaves its table uncreated, and that
table's data unloaded.

pg_dump also emits `ALTER ... OWNER TO` for each object and `GRANT`
for each privilege, naming `admin`, `app` and any role you created. An
object belongs to the role that created it. At a target without those
roles, every one of those statements errors. Each object then
restores owned by the restoring role instead.

## Restoring into Your Own Postgres

Restoring into another Managed database is a different procedure. A
Managed target has its own rules about which role installs which
extension. The
[Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
guide describes that direction.

1. Install the extensions the schema depends on, as a role on the
   target that can install them. A schema loaded before its extensions
   fails on the first object that needs one:

        psql -h <target-host> -U <target-user> -d <target-db> \
            -c 'CREATE EXTENSION IF NOT EXISTS vector'

2. List the dump's table of contents, dropping the entries the target
   cannot take. An extension the target lacks costs you every table
   with a column of a type that extension defines. pgaudit is the one
   a self-run Postgres is least likely to have:

        pg_restore -l export.dump | grep -v pgaudit > export.list

3. Restore from the edited list:

        pg_restore -h <target-host> -U <target-user> -d <target-db> \
            --no-owner --no-privileges -L export.list export.dump

    Those two flags leave every object owned by the restoring role
    with no grants. That is the right result at a target holding no
    `admin`, `app` or role of your own. To keep the ownership and the
    grants instead, load `roles.sql` first and drop both flags.

4. Check the row counts against the source before trusting the copy:

        psql -h <target-host> -U <target-user> -d <target-db> \
            -c 'SELECT count(*) FROM <table>'

    The restore returns exit status 0 only when every entry in the
    list applies. A pg_restore that returns exit status 1 has still
    written everything that did not error. Exit status 1 says only
    that something is skipped. The error list says what.

To run the same restore again over the same target, add `--clean` and
`--if-exists`. `--clean` drops each object before recreating it, and
`--if-exists` stops a drop of something absent from erroring.

## Exporting a Single Table as CSV

`psql`'s `\copy` writes one table to a local CSV file through the
ordinary connection, with no server-side file access. Run `\copy` in
the shell the dump ran in, or repeat steps 1 and 2 to load admin's
variables again. Connect as `admin` for any table. Connect as `app`
for the tables `app` owns:

    psql -c "\copy <table> TO '<table>.csv' WITH (FORMAT csv, HEADER)"

## Next Steps

- The
  [Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
  guide describes the same dump going the other way, into a Managed
  database.
- The
  [Backing up and Restoring a pgEdge Starfleet Managed Database](backup-restore.md)
  guide describes the platform's own backups.
- The
  [Rotating a Password on a pgEdge Starfleet Managed Database](rotate-credentials.md)
  guide describes changing the password used above.
