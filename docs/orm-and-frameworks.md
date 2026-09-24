# ORM and framework integration

Every framework on this page connects the same way. The CLI prints a
standard libpq connection URI, and each framework below has one place
that accepts it. The string carries nothing specific to pgEdge, so
these frameworks take it without changes. A driver that expects its
own URI scheme, such as JDBC's `jdbc:postgresql://` prefix, needs the
parts rather than the assembled URI.

The connect an application guides cover the command itself, its flags, and how
to handle the live password the string carries, one for
[Managed](managed/connect-an-application.md) and one for
[BYOC](byoc/connect-an-application.md). This page starts from the string.

## Get the string

On `managed`, one command prints the URI for a database:

    pgedge starfleet managed database connection-string <db-id>

On `byoc` the same command names a node when the database has several, because
a BYOC database carries one connection block per node:

    pgedge starfleet byoc database connection-string <db-id> --node n1

Either prints one line and nothing else:

    postgresql://app:<password>@<host>:<port>/<database>?sslmode=require

Most of the recipes below read that line from an environment file.
Write the file under a restrictive umask and stop on a failed read, so
a mistyped identifier surfaces here rather than when the application
starts:

    umask 077
    if ! pgedge starfleet managed database connection-string <db-id> \
        > uri.txt; then
        echo "connection-string failed; .env was not written" >&2
        exit 1
    fi
    printf "DATABASE_URL='%s'\n" "$(cat uri.txt)" > .env

Single quotes, not double: a password may carry `$`, which the URI
leaves unescaped, and a double-quoted value is expanded by a shell
that sources the file. A single quote itself never appears raw in the
URI, so the single-quoted form is safe.

Remove the temporary file once the env file holds the string:

    rm -f uri.txt

Keep the query string on the end of the URI. The CLI always appends
sslmode=require, and a URI trimmed back to its host and database
drops the setting without saying so.

## psql

`psql` reads the same URI, which makes it the shortest way to prove
the string before a framework is in the picture. Passing the URI as an
argument puts the password in the process list, so take the env format
and let `psql` read the `PG*` variables:

    umask 077
    if ! pgedge starfleet managed database connection-string <db-id> \
        --format env > pg.env; then
        echo "connection-string failed; pg.env holds nothing" >&2
        exit 1
    fi
    . ./pg.env
    export PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD PGSSLMODE
    psql -c 'select version()'
    rm -f pg.env

A row back from that says the host resolves, the TLS handshake
completes and the role authenticates. A framework that fails after
this one succeeded is failing on its own configuration rather than on
the database.

## Prisma

Prisma takes the URL from the datasource block in `schema.prisma`, and
the generated block already points at an environment variable:

    datasource db {
      provider = "postgresql"
      url      = env("DATABASE_URL")
    }

Set `DATABASE_URL` to the string the CLI printed. Prisma's own default
is sslmode=prefer, which accepts a plain-text connection when TLS is
not available, so the sslmode=require the CLI appends is what holds
the connection encrypted. The
[Prisma Postgres connector reference](https://www.prisma.io/docs/orm/overview/databases/postgresql)
lists the other arguments Prisma reads from the query string.

## Drizzle

Drizzle connects through the `pg` driver, which parses the URI itself,
so the string goes straight into the constructor:

    import { drizzle } from 'drizzle-orm/node-postgres';

    const db = drizzle(process.env.DATABASE_URL);

Drizzle Kit holds its own copy for migrations, under `dbCredentials`
in `drizzle.config.ts`:

    import { defineConfig } from 'drizzle-kit';

    export default defineConfig({
      dialect: 'postgresql',
      dbCredentials: { url: process.env.DATABASE_URL },
    });

Both read the same variable, so one env file covers the application
and the migration tool. The
[Drizzle Postgres guide](https://orm.drizzle.team/docs/get-started-postgresql)
covers the driver alternatives.

## SQLAlchemy and Alembic

SQLAlchemy builds an engine from the URI directly:

    import os
    from sqlalchemy import create_engine

    engine = create_engine(os.environ["DATABASE_URL"])

Alembic reads the URL from the `sqlalchemy.url` key of `alembic.ini`,
a file most projects commit, and a live password does not belong in a
committed file. Set the value at run time from `env.py` instead:

    import os
    from alembic import context

    context.config.set_main_option(
        "sqlalchemy.url", os.environ["DATABASE_URL"])

The [Alembic tutorial](https://alembic.sqlalchemy.org/en/latest/tutorial.html)
describes the rest of that file.

## Django

Django reads discrete parameters from the `DATABASES` setting rather
than a URL, so the string has to be split or parsed. The split version
reads the same values the env format prints:

    DATABASES = {
        "default": {
            "ENGINE": "django.db.backends.postgresql",
            "NAME": os.environ["PGDATABASE"],
            "USER": os.environ["PGUSER"],
            "PASSWORD": os.environ["PGPASSWORD"],
            "HOST": os.environ["PGHOST"],
            "PORT": os.environ["PGPORT"],
            "OPTIONS": {"sslmode": "require"},
        }
    }

Django's Postgres backend passes `OPTIONS` to the driver's connection
constructor, which is why the TLS setting sits there rather than
beside the host. The alternative is dj-database-url, which parses a
URI into the same dictionary and reads DATABASE_URL by default:

    import dj_database_url

    DATABASES = {"default": dj_database_url.config()}

The
[Django databases reference](https://docs.djangoproject.com/en/stable/ref/databases/)
covers what else the backend accepts.

## Ruby on Rails

Active Record reads DATABASE_URL from the environment with no
configuration at all, so that variable and an empty
`config/database.yml` are enough to connect. A `url` key in the YAML
takes precedence over the variable, and reading the variable through
ERB pins one environment to one connection without committing the
string:

    production:
      url: <%= ENV['DATABASE_URL'] %>

The
[Rails configuration guide](https://guides.rubyonrails.org/configuring.html#configuring-a-database)
describes how the two sources are merged.

## Go (pgx)

pgx parses the URI itself, so the pool constructor takes the string
with nothing in between:

    pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
    if err != nil {
        return err
    }
    defer pool.Close()

pgx reads sslmode out of the query string the way libpq does, so the
string needs no Go-side TLS setup to connect over TLS. The
[pgxpool documentation](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool)
covers the pool options worth setting beyond the URL.

## Next steps

- The connect an application guides for
  [Managed](managed/connect-an-application.md) and
  [BYOC](byoc/connect-an-application.md) describe the connection-string
  command, its flags and how to handle the password the output carries.
- The [load schema and data guide](managed/load-data.md)
  describes pulling the connection fields out individually and turning
  them into `.pgpass` entries.
- The [rotate a managed database password guide](managed/rotate-credentials.md)
  describes what happens to a string an application already holds once
  the role behind the string is rotated.
- The [CI and automation guide](ci.md) describes running the CLI from
  a pipeline, including where the credentials it needs should live.
