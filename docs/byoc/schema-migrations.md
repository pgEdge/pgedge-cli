# Schema migrations on a BYOC database

The CLI ships no migration tool. Schema changes go through whatever
you already run: Alembic, Prisma Migrate, Rails migrations, Flyway, or
a directory of numbered SQL files fed to psql. The CLI owns the two
ends of that job. It resolves the connection string your tool points
at, and it reads the database back once the tool has finished.

The [manage BYOC databases workflow](databases.md) covers the roles a
first load runs as. This page covers every change after that first
load.

## BYOC connection strings

A BYOC database carries one connection block per node, so the command asks
which node you mean before it prints anything.

`--node` takes the node's name. With one node the flag is not needed. With
several and no flag, the command prints the node list and exits 2, so a script
passes `--node` rather than parsing that listing:

    umask 077
    if ! pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --format env > db.env; then
        echo "connection-string failed, nothing was written" >&2
        exit 1
    fi
    . ./db.env
    rm -f db.env

There is no `--user-type` on the byoc command. The block carries whichever role
the API returns, so read `username` out of `-o json` rather than assuming which
role you have. The delivered `admin` role on BYOC is a real Postgres superuser,
as the [manage BYOC databases workflow](databases.md) sets out, so a migration
that installs an extension or rewrites an object it does not own needs that
role and not app.

## DDL replication

A BYOC database running on more than one node is replicated by Spock,
and Spock treats a schema change differently from a row change. Rows
replicate. A CREATE TABLE or an ALTER TABLE applied through one node's
connection string stays on that node unless DDL replication is turned
on for the database.

The migration exits 0, the application talking to that node works, and
the other nodes carry the old schema until something reads from
them.

pgEdge's Platform documentation describes the mechanism as three Spock
settings, applied on every node and followed by a configuration
reload. The same page sets a precondition: the schema on every node
must match exactly when the settings are turned on, and on a cluster
that already has tables, those tables must be in the default
replication set first. Turning the settings on mid-project, on a
cluster whose tables are not in a replication set, is the case that
documentation warns against:

    ALTER SYSTEM SET spock.enable_ddl_replication=on;
    ALTER SYSTEM SET spock.include_ddl_repset=on;
    ALTER SYSTEM SET spock.allow_ddl_from_functions=on;
    SELECT pg_reload_conf();

The third setting is optional and covers DDL issued from inside
functions and anonymous code blocks. The
[automatic DDL replication page](https://docs.pgedge.com/platform/managing/autoddl)
carries the rest, including the statements that stay per-node even
with the settings on:

- CREATE DATABASE, ALTER DATABASE and DROP DATABASE do not replicate.
- ALTER SYSTEM does not replicate.
- ALTER TABLE ... DETACH CONCURRENTLY does not replicate.
- CREATE INDEX ... CONCURRENTLY does not replicate.
- CREATE TABLE AS replicates the statement and not the rows, so each
  node runs the SELECT again and can end up with different data.

The same page warns that DROP TABLE and CREATE TABLE AS can break
replication on a cluster that is already serving traffic, and
recommends a maintenance window for DDL on one.

Do not assume the settings are already on, in either direction.
pgEdge Cloud turns automatic DDL replication on as a node joins a
cluster, described on the
[add a node page](https://docs.pgedge.com/cloud/mod_cluster/add), and
the same documentation set describes databases where the feature is
off. Read the values on every node before the migration, and read the
schema back afterward through a node the migration did not run
against:

    umask 077
    if ! pgedge starfleet byoc database connection-string <db-id> \
        --node n2 --format env > n2.env; then
        echo "connection-string failed, n2 was not checked" >&2
        exit 1
    fi
    . ./n2.env
    psql -c '\d orders'
    rm -f n2.env

A migration that reached one node out of three looks identical, from
that node, to one that reached all three.

## Single-node databases

Where a database runs on one node, a migration that succeeds is
finished, because there is no second copy of the schema to reach.

A BYOC database on one node behaves the same way, so the read-back
above is a single read.

## Next steps

- The [manage BYOC databases workflow](databases.md) covers the three
  BYOC roles and what each one may do.
- The [connect an application guide](connect-an-application.md) covers
  every flag on `connection-string` and the shape of the connection
  block underneath it.
- The [CI and automation guide](../ci.md) covers running these commands
  from a pipeline.
