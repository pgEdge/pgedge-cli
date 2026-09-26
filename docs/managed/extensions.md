# Installing Supported Extensions on a pgEdge Starfleet Managed Database

A pgEdge Starfleet Managed database supports the Postgres extensions
listed on this page. Each one installs through a built-in role, and an
application can then use it.

Three terms recur on the page:

- `app` is the built-in role that owns the database. An application
  and its migrations connect as `app`.
- `admin` is the built-in role that installs the extensions the
  platform reserves.
- A supported extension is one this page lists. An extension the page
  does not list is not supported on a Managed database.

## Before You Start

You need the database ID, `<db-id>` below. Run
`pgedge starfleet managed database list` to find it.

The database must report `available`, and an allowlist rule must admit
the address you connect from. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](network-access.md) page describes adding a rule.

## Finding the Supported Extensions

Every Managed database already has three extensions installed:

| Extension | What it provides |
|---|---|
| plpgsql | The PL/pgSQL procedural language. |
| pg_stat_statements | Query statistics, readable as `app` or `admin`. |
| pgaudit | Audit logging, which the platform configures. |

Install the following extensions as `app`. `app` then owns each one,
so a migration running as `app` can alter or drop it:

| Extension | What it provides |
|---|---|
| btree_gin | GIN operator classes for common data types. |
| btree_gist | GiST operator classes for common data types. |
| citext | A case-insensitive text type. |
| cube | A multidimensional cube type. |
| dict_int | A text-search dictionary for integers. |
| fuzzystrmatch | String similarity and phonetic matching. |
| hstore | A key-value store type. |
| intarray | Functions and operators for integer arrays. |
| isn | Types for international product numbers. |
| lo | Large-object maintenance. |
| ltree | A type for hierarchical label paths. |
| pg_trgm | Trigram similarity search. |
| pgcrypto | Cryptographic functions. |
| pgmq | A lightweight message queue. |
| seg | A type for line segments and float intervals. |
| tablefunc | Crosstab and other table functions. |
| tcn | Triggered change notifications. |
| tsm_system_rows | A `TABLESAMPLE` method that samples by row count. |
| tsm_system_time | A `TABLESAMPLE` method that samples by time limit. |
| unaccent | Accent removal for text search. |
| uuid-ossp | UUID generation. |

Install the following extensions as `admin`. Each one is then owned by
`postgres`, and `app` can use it:

| Extension | What it provides |
|---|---|
| vector | Vector types, distance operators and HNSW indexes. |
| postgis | Spatial types and functions. |
| postgis_raster | Raster types and functions for PostGIS. |
| postgis_sfcgal | 3D geometry functions for PostGIS. |
| postgis_topology | Topology types and functions for PostGIS. |
| postgis_tiger_geocoder | US address geocoding for PostGIS. |
| address_standardizer | Address parsing into its parts. Install address_standardizer_data_us with it. |
| address_standardizer_data_us | US rules and lexicons for address_standardizer. |
| pg_cron | Scheduled jobs, run in the database. |
| pg_tokenizer | Text tokenizers for full-text search. |
| vchord_bm25 | BM25 ranking and indexes for full-text search. |

## Installing an Extension

Install each extension as the role the tables above name. Install
every extension a schema depends on before loading that schema, since a
schema load fails on the first object that needs a missing extension.

1. Print the `app` connection string, which carries a live password:

        pgedge starfleet managed database connection-string <db-id> \
            --user-type app

2. Install an extension from the `app` table with that string:

        psql "<app-uri>" -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto'

    Use the URI step 1 printed in place of `<app-uri>`. Install pgmq
    this way too. Installed as `admin`, pgmq refuses `app` access to its
    queues.

3. Print the `admin` connection string:

        pgedge starfleet managed database connection-string <db-id> \
            --user-type admin

4. Install an extension from the `admin` table with that string:

        psql "<admin-uri>" -c 'CREATE EXTENSION IF NOT EXISTS vector'

    Use the URI step 3 printed in place of `<admin-uri>`.

5. List the installed extensions and their owners:

        psql "<app-uri>" \
            -c 'SELECT extname, extowner::regrole FROM pg_extension'

    An extension from the `admin` table shows `postgres` as its owner.

## Troubleshooting

The entries below cover the refusals an install can produce.

### Install Refused as app with "Must be superuser"

psql exits with status 1 and prints
`permission denied to create extension` with the hint
`Must be superuser to create this extension`. The extension is in the
`admin` table, or it is not supported. Where the `admin` table lists
it, install it as `admin` instead.

### Install Refused as admin with "Must be superuser"

psql exits with status 1 with the same message, run as `admin`. The
extension is not supported on a Managed database, and no role can
install it. Remove the dependency from the schema before loading it.

### Extension Not Available

psql exits with status 1 and prints
`extension "<name>" is not available`. The extension is not part of
the Managed database image. Remove the dependency from the schema
before loading it.

## Next Steps

The
[Loading a Schema and Data into a pgEdge Starfleet Managed
Database](load-data.md) page restores a dump after its extensions are
installed.
