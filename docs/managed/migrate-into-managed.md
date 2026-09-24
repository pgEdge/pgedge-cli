# Migrating an Existing Database to a pgEdge Starfleet Managed Database

This page copies an existing Postgres database into a pgEdge Starfleet Managed
database. The last step repoints your application at the new database.

This page gives two copy methods. Dumping and restoring moves everything in one
pass, and the source takes no writes throughout. Logical replication copies the
data while the source continues working, so the source stops only for the
cutover.

Both methods run from your own machine. You use psql, pg_dump and pg_restore
against both databases.

A Managed database holds two Postgres roles. admin holds CREATEROLE and
CREATEDB, and installs the extensions app cannot. app owns the database. Your
application and your restore both connect as app. Neither one has Postgres
superuser rights.

## Before You Start

To create the Managed database first, see
[Provision a managed database](provision.md).

Collect these before you start either copy method:

- The Managed database's ID. Run `pgedge starfleet managed database list` and
  read the ID column. Every ID argument takes the full UUID, and no prefix
  resolves.
- The Managed database's status and Postgres version, from the `STATUS` and `PG
  VERSION` columns of that same listing. Wait until the status reads
  `available`.
- Your own address on the database's allowlist. A Managed database created
  without allowlist rules is closed, and reaches nobody until a rule is added.
- The addresses your application connects from, on the same allowlist. Your
  application cannot reach the Managed database without them.
- Both connection URIs, admin's and app's, held in shell variables.
- The source database's connection URI, in a third variable.
- A role at the source holding SELECT on every table you copy.
- pg_dump, pg_restore and psql, no older than either Postgres server.

Add your own address and your application's addresses to the allowlist:

    pgedge starfleet managed database allowlist add <database-id> \
        --my-ip --wait
    pgedge starfleet managed database allowlist add <database-id> \
        <app-cidr> --label app --wait

Read both connection URIs into shell variables. Write the source's own URI into
a third:

    ADMIN_URL=$(pgedge starfleet managed database connection-string \
        <database-id> --user-type admin)
    APP_URL=$(pgedge starfleet managed database connection-string \
        <database-id> --user-type app)
    SOURCE_URL='postgresql://<user>:<password>@<host>:5432/<database>'

Check that each of the first two printed a URI. The capture hides a failed
command's exit status, so an empty variable is the only sign that the read did
not work.

Each URI holds a password, and a command that takes one as an argument shows
that password in the process list while it runs. Where that matters, pass
`--no-password` and supply the password through a `.pgpass` file instead.

Logical replication needs five more things at the source:

- Logical write-ahead logging, `wal_level = logical`.
- Spare WAL senders and replication slots.
- Access that can create a role and a publication.
- A login role holding replication rights.
- A Postgres port the Managed database can reach, kept open until the cutover.

One action during a logical replication copy cannot be undone. The Managed
database accepts any write you make while the copy runs. That row is never sent
to the source. The two databases differ from then on, and nothing reports the
difference.

## Choosing a Copy Method

The two methods differ in how long the source stops and what it must support:

| Copy method | The source stops for | The source needs |
|---|---|---|
| Dump and restore | The whole copy | A role holding SELECT everywhere |
| Logical replication | The cutover alone | Logical WAL, a replication role, a publication |

A source one Postgres major version behind the Managed database copies across
without change.

## Copying by Dump and Restore

Steps 1 and 2 need no downtime. Your application cannot use the source from
step 3 until the cutover finishes.

1.  Take a schema-only dump from the source, and list the extensions it
    carries:

        pg_dump -Fc --schema-only -f schema.dump "$SOURCE_URL"
        pg_restore -l schema.dump | grep EXTENSION

    Do this while the source is still live. A missing extension found during
    downtime adds the whole install to your outage.

2.  Install each of those extensions at the Managed database. Install as app,
    and use admin for the ones app refuses:

        psql "$APP_URL" -c 'CREATE EXTENSION pg_trgm'
        psql "$ADMIN_URL" -c 'CREATE EXTENSION vector'

    `Must be superuser to create this extension` as app means the extension is
    privileged, so run it as admin instead. The [Installing Supported
    Extensions on a pgEdge Starfleet Managed Database](extensions.md) page
    lists the role that installs each supported extension. A schema loaded
    before its extensions exist fails on the first object that needs one.

3.  Stop the source taking writes, by shutting your application down or by
    revoking its write access. Downtime starts here.

4.  Take a custom-format dump, connecting as the role holding SELECT on every
    table:

        pg_dump -Fc -f source.dump "$SOURCE_URL"

    A role that cannot read one table fails at the first lock. The dump file is
    then empty.

5.  Restore as app, suppressing the dump's ownership and grant entries:

        pg_restore -d "$APP_URL" --no-owner --no-privileges source.dump

    Those role definitions live only at the source. An owner or grant entry
    naming one errors here. `--no-owner` and `--no-privileges` leave every
    object owned by app.

6.  Read the errors pg_restore printed. pg_restore exits 1 and reports `must be
    owner of extension` once for each extension you installed as admin, because
    admin's extensions belong to `postgres`. Every other entry restores, and
    any other message means something did not.

Continue at
[Cutting Over to the Managed Database](#cutting-over-to-the-managed-database).

## Copying with Logical Replication

Your application continues using the source while this runs.

1.  Check that the source runs at `wal_level = logical`, with spare WAL senders
    and replication slots.

    On Amazon RDS, set `rds.logical_replication = 1` in the instance's
    parameter group. An instance created with that setting in place needs no
    reboot.

2.  Create a login role at the source, and grant it read access to the tables
    you are copying:

        CREATE ROLE repl LOGIN PASSWORD '<password>';
        GRANT USAGE ON SCHEMA public TO repl;
        GRANT SELECT ON ALL TABLES IN SCHEMA public TO repl;

3.  Give that role replication rights. On Amazon RDS, run `GRANT
    rds_replication TO repl` as the master user. That user needs no superuser
    rights of its own. On a source you manage yourself, give the role the
    REPLICATION attribute.

4.  List the tables that cannot replicate an update or a delete:

        psql "$SOURCE_URL" -c "SELECT n.nspname, c.relname
        FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relreplident = 'd'
        AND n.nspname NOT IN ('pg_catalog', 'information_schema')
        AND NOT EXISTS (SELECT 1 FROM pg_index i
        WHERE i.indrelid = c.oid AND i.indisprimary)"

    A table this returns refuses every UPDATE and DELETE from the moment step 5
    publishes it, on the source your application is still using. Give each one
    a primary key, or set a replica identity, before you go on. An extension
    can create such a table without your knowing.

5.  Create a publication over the tables you are copying:

        CREATE PUBLICATION <publication> FOR ALL TABLES;

6.  Take a schema-only dump, and list the extensions it carries:

        pg_dump -Fc --schema-only -f schema.dump "$SOURCE_URL"
        pg_restore -l schema.dump | grep EXTENSION

7.  Install each of those extensions. Then restore only the schema, as app:

        psql "$ADMIN_URL" -c 'CREATE EXTENSION vector'
        pg_restore -d "$APP_URL" --schema-only --no-owner \
            --no-privileges schema.dump

    `Must be superuser to create this extension` as app means the extension is
    privileged, so run it as admin instead. The restore then exits 1 and
    reports `must be owner of extension` once for each extension admin
    installed, and every other entry restores.

8.  Open the source's Postgres port so the Managed database can reach it. The
    rule stays open until step 7 of the cutover removes it.

9.  As admin, create the subscription. That role already holds every right this
    needs:

        psql "$ADMIN_URL" -c "CREATE SUBSCRIPTION <subscription>
        CONNECTION 'host=<host> port=5432 dbname=<database>
        user=repl password=<password> sslmode=require'
        PUBLICATION <publication>"

    psql reports that it created a replication slot on the publisher.

10. Run this until every table shows `r`. That value means the table has
    finished its initial copy:

        psql "$ADMIN_URL" -c 'SELECT srrelid::regclass, srsubstate
        FROM pg_subscription_rel ORDER BY 1'

    Any other value means that table is not ready, and the first column names
    it. A database of 120 MB over four tables finishes in about 100 seconds.

11. Read the subscription's progress:

        pgedge starfleet managed database inspect <database-id> \
            subscriptions --user-type admin

    Each row shows one subscription's name, its worker's process ID, and four
    columns tracking received LSN, last message sent, last message received and
    latest end LSN. A row proves the worker exists, and the LSN columns advance
    as changes arrive.

Leave the subscription running while you prepare the cutover. A write you make
to the Managed database now is accepted, is never sent to the source, and
leaves the two databases different from then on.

## Cutting Over to the Managed Database

Both copy methods end here. Steps 2, 4 and 7 apply to logical replication
alone.

1.  Stop the source taking writes, if it has not stopped already. Shut your
    application down, or revoke its write access.

2.  Run the progress read from step 11 until the received LSN matches the
    latest end LSN and stops advancing. Changes still in flight are updates and
    deletes as well as inserts, and step 3 cannot see the first two.

3.  Compare row counts table by table, between the source and the Managed
    database. Counts that differ mean the copy is short, so stop here rather
    than going on.

4.  As admin, drop the subscription. The replication slot at the source goes
    with it:

        psql "$ADMIN_URL" -c 'DROP SUBSCRIPTION <subscription>'

5.  Check every sequence against the largest value in the column it feeds. A
    dump carries each sequence's position, and logical replication copies rows
    alone and leaves each sequence at 1. Set the ones that are behind:

        psql "$APP_URL" -c "SELECT setval('<sequence>',
        (SELECT coalesce(max(<column>), 0) + 1 FROM <table>), false)"

6.  Repoint your application at the URI `$APP_URL` holds. When your application
    writes to the Managed database, the source is no longer a fallback.

7.  Remove the firewall rule you opened at the source, and drop the replication
    role you created there.

8.  Clear the shell variables holding your credentials.

## Troubleshooting

Each entry below names what pg_restore or the subscription reported:

### A Restore Reporting "must be owner of extension"

pg_restore exits 1 once for each extension that admin installed and the dump
also carries. An extension admin installs is owned by `postgres`, not by either
role. app cannot claim that extension. An extension app installed belongs to
app, and produces no error. Every other entry restored, so read the rest of the
errors and carry on.

### A Restore Reporting That a Type Does Not Exist

The extension that provides the type is not installed at the Managed database.
Every table, index and constraint using that type is skipped. Each one adds
more errors. Install the extension, then restore again.

`Must be superuser to create this extension` as admin means no role on the
database can install that extension. Drop that dependency from your source
before you copy.

### A Restore Reporting That a Role Does Not Exist

The dump names a role that exists only at the source. Restore with
`--no-owner` and `--no-privileges`, which leaves every object owned by
app. To rebuild the source's roles here instead, see [Creating and
Managing Roles on a pgEdge Starfleet Managed Database](roles.md).

### An Initial Copy That Never Finishes

Read the progress from step 11. A row whose LSN columns do not advance means
the subscription reached the Managed database but cannot get changes from the
source. Check that the source's firewall still admits the Managed database, and
that the replication role still logs in.

### An Update or a Delete Refused at the Source

The table has no primary key and no replica identity, and a publication now
covers it. Run the check from step 4 of the replication procedure to list every
table in that state.

### A First Insert That Collides with a Copied Row

The sequence behind that column is still at 1. That sequence hands out a value
a copied row already holds. Set the sequence with step 5 of Cutting Over to the
Managed Database.

## Next Steps

Four tasks follow the cutover:

- Point your application at the new database:
  [Connecting an Application to a pgEdge Starfleet Managed Database](connect-an-application.md)
- Back it up when it is live:
  [Backing up and Restoring a pgEdge Starfleet Managed Database](backup-restore.md)
- Recreate the roles and grants your source had:
  [Creating and Managing Roles on a pgEdge Starfleet Managed Database](roles.md)
- Load more data later:
  [Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
