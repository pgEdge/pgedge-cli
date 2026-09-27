# Creating and Managing Roles on a pgEdge Starfleet Managed Database

A Managed database delivers three roles. You create every other role
yourself in SQL. You keep the ability to alter, grant and drop the
role.

Four terms recur on the page:

- `admin` is the built-in role that creates roles and reads and
  writes every table.
- `app` is the built-in role that owns the database and the objects
  the application creates.
- A role manager is a role you create with CREATEROLE. Every role you
  add is created from the role manager.
- A reconcile is the platform resetting `admin`'s memberships to the
  platform's own list.

## Before You Start

You need the database's full UUID and a database reporting
`available`. The [Provision a managed database](provision.md) page
describes reaching that state.

Two actions cost you permanent control of a role:

- Creating a role while connected as `admin`, rather than from the
  role manager.
- Rotating a built-in password, which reconciles the database.

Both are easy to take before the role manager exists.

## Creating the Role Manager

Create one role manager, as `admin`, before creating any other role.
A reconcile resets only `admin`'s memberships, so the role manager
keeps its own. The role manager keeps the ADMIN OPTION on every role
it creates.

Creating a role as `admin`, rather than from the role manager, costs
you control of it permanently. At the next reconcile, `admin` loses
the ADMIN OPTION on that role. Nothing on the database can then alter,
grant or drop the role. The role keeps logging in and keeps its
grants, so nothing warns you.

1.  Read a connection string for `admin`:

        pgedge starfleet managed database connection-string <db-id> \
            --user-type admin

    The string holds the host, the port, the database name, the
    password and `sslmode=require`.
2.  Connect as `admin` with `psql`, passing that connection string.
3.  Run `CREATE ROLE` to create the role manager, with a password of
    your own:

        CREATE ROLE rolemgr LOGIN CREATEROLE PASSWORD '<password>';

4.  Store the role manager's password. `admin` loses the ADMIN
    OPTION on the role manager at the next reconcile.

`admin` can still reset that password. That is because `admin` sets
the password of any role that is not reserved, with or without the
ADMIN OPTION.

## Creating a Role

You create every other role from the role manager, in SQL, with a
password of your own.

1.  Read the connection details in structured form, with
    `--no-password`, since only the host, port and database name are
    needed:

        pgedge starfleet managed database connection-string <db-id> \
            --user-type admin --no-password -o json > connection.json
        DB_HOST=$(jq -r '.host' connection.json)
        DB_PORT=$(jq -r '.port' connection.json)
        DB_NAME=$(jq -r '.database' connection.json)
        rm -f connection.json

2.  Connect as the role manager with `psql`, naming it as the user.
    Every role uses the same host, port and database name:

        psql -h "$DB_HOST" -p "$DB_PORT" -U rolemgr -d "$DB_NAME"

    `psql` prompts for a password. Type the one you set for the role
    manager.
3.  Run `CREATE ROLE` to create the role:

        CREATE ROLE reporting LOGIN PASSWORD '<password>';

The new role connects to the same host, port and database name as
`admin` and `app`, over TLS. The CLI returns credentials for the
built-in roles only, so keep the new role's password yourself.

`admin` grants the CREATEDB and CREATEROLE attributes. SUPERUSER,
REPLICATION and BYPASSRLS are refused, because only a role holding an
attribute can grant it.

`ALTER ROLE ... SET` sets a per-role setting, applied at the next
login:

    ALTER ROLE reporting SET statement_timeout = '30s';

## Understanding the Built-in Roles

The following table describes the three roles with CLI credentials:

| Role | Attributes | Memberships | Use it for |
|---|---|---|---|
| `admin` | CREATEROLE, CREATEDB | `app`, and the Postgres roles pg_read_all_data, pg_write_all_data, pg_monitor, pg_signal_backend, pg_maintain, pg_checkpoint, pg_create_subscription and pg_use_reserved_connections | Reading and writing every table, creating roles, installing the extensions the platform reserves, subscribing to another database |
| `app` | None | None | Owning the database and every object the application creates |
| app_read_only | None | `app` | Reading data. The database refuses every write from it, even after `SET ROLE app`. RAG connects as it, and MCP unless its writes are allowed |

No built-in role is a superuser. A statement that needs SUPERUSER
fails as any of them. `admin` holds its memberships without the ADMIN OPTION,
so `admin` passes none of them on. `GRANT app TO reporting`,
`GRANT pg_read_all_data TO reporting` and
`CREATE ROLE reporting IN ROLE app` each fail with
`permission denied to grant role`. `app` cannot grant membership in
itself either, and `admin` cannot alter `app`'s settings or drop `app`
for the same reason.

The platform reserves postgres, pgedge_admin, streaming_replica
and cnpg_metrics_exporter. An ALTER or a DROP against one of those
roles fails with `is a reserved role, only superusers can modify it`.
A role that is not a superuser cannot grant membership in
pgedge_admin, a superuser role.

## Granting Read-Only Access to Application Tables

A grant on `app`'s tables comes from `app`, which owns them. It also
comes from `admin`, which acts as `app` through its membership. The
role manager does neither, owning nothing and belonging to nothing.
Membership in `app` is closed, so an object grant is the only route.

Three statements, run as `admin` or as `app`, give a role read-only
access to `app`'s tables:

    GRANT USAGE ON SCHEMA public TO reporting;
    GRANT SELECT ON ALL TABLES IN SCHEMA public TO reporting;
    ALTER DEFAULT PRIVILEGES FOR ROLE app IN SCHEMA public
        GRANT SELECT ON TABLES TO reporting;

The `ALTER DEFAULT PRIVILEGES` statement applies to the tables `app`
creates from then on. A table `admin` creates is owned by `admin` and
is not included. That is one more reason to create every object as
`app`. A warning that no privileges were granted for the
pg_stat_statements views is harmless.

With those grants, the role reads `app`'s tables in `public` and
nothing more. An INSERT or an UPDATE is refused with
`permission denied for table`. Creating a table in `public` is
refused, because the schema belongs to the database owner. The role
cannot create a schema of its own unless `admin` grants CREATE ON
DATABASE to the role. With that grant the role creates schemas and
tables inside them, though still nothing in `public`.

## Removing a Role

A role cannot be dropped while the role owns objects or holds grants,
so removing one takes three statements. The role manager takes
membership in the role. The role manager drops what the role owns and
the privileges the role holds. The role manager then drops the role.
Run the statements as the role manager:

    GRANT reporting TO rolemgr;
    DROP OWNED BY reporting;
    DROP ROLE reporting;

`DROP OWNED BY` removes the role's tables and schemas as well as its
grants. Reassign anything worth keeping first.

## Changing a Password

`rotate-password` takes a required `--role` and changes that role's
password, prompting for confirmation first:

    pgedge starfleet managed database rotate-password <db-id> \
        --role app --wait

Pass `--force` to skip that prompt in a script. The new password does
not authenticate until the database is back to `available`. Read the
status before switching an application over. Rotating app_read_only
also restarts the services that connect as it.

A rotation reconciles the database. A rotation taken before the role
manager exists costs you control of every role `admin` created.

`ALTER ROLE app PASSWORD` runs without error, from `admin` or from
`app`, and takes effect, so nothing warns you. The platform keeps its
own copy of the three built-in passwords. That copy is what
`database get` and `database connection-string` return. After a change
made in SQL, both commands hand you a password that no longer logs in.
`rotate-password` sets a fresh password and updates the copy. That is
also how you recover from a password changed in SQL.

A role you created has no copy on the platform. An `ALTER ROLE`
statement is the only way to change the role's password. Run the
`ALTER ROLE` statement from the role manager that created the role, or
from `admin`. `admin` can lose the ADMIN OPTION on a role. Setting a
password is the one thing `admin` can still do to that role.

## Next Steps

- The [Loading a Schema and Data into a pgEdge Starfleet Managed
  Database](load-data.md) page describes loading as `app` so that
  everything the application uses belongs to `app`.
- The [Rotating a Password on a pgEdge Starfleet Managed
  Database](rotate-credentials.md) page describes the built-in
  passwords in detail.
- The [Exporting Data from a pgEdge Starfleet Managed
  Database](export-data.md) page describes the roles a dump names and
  what a target without them does.
- The [Connecting an Application to a pgEdge Starfleet Managed
  Database](connect-an-application.md) page describes connection
  strings for the roles the CLI knows about.
