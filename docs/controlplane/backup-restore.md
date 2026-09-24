# Back up and restore a Control Plane database

pgBackRest backs up a Control Plane database one node at a time. Each
backup goes into a repository that the database spec names, and you own
that storage. A restore rebuilds nodes from the same repository.

A restore rebuilds every node it targets from one node's backup, so one
node's backup is what a restore needs.

This page covers five things:

- [Backup configuration](#backup-configuration) is the `backup_config`
  block that names the repository.
- [Take a backup](#take-a-backup) is the on-demand command.
- [Build a restore spec](#build-a-restore-spec) is the file a restore
  reads.
- [Run the restore](#run-the-restore) is the restore itself.
- [Re-enable backups after a restore](#re-enable-backups-after-a-restore)
  is a required step afterward.

The page uses three terms:

- A **node** is one member of the database, named in the spec. You back
  up and restore nodes.
- An **instance** is the running Postgres for a node on a host.
- A **repository** is the storage that holds the backups. The database
  spec declares it, and pgBackRest writes into it.

## Before You Start

The controlplane module has no login. Every command needs a running
server and a URL that points at it. `--base-url` names that server and
defaults to `http://localhost:3000`. Write it into your profile once
instead of repeating it:

    pgedge controlplane config set --base-url https://cp.example.com

The [configuration guide](../configuration.md) covers profiles, and the
[Control Plane standup workflow](local-server.md) covers the server
itself.

Every command below needs a database ID. If you inherited the database
rather than creating it, list what is there:

    pgedge controlplane database list

That prints an ID, STATE, CREATED and UPDATED for each database. The ID
is what the commands on this page take, and the examples use
`storefront` for it. You choose that ID when you create the database,
and it can differ from the database's own name (`database_name` in its
spec). The backup command also needs a node name, which comes from the
NODE column of:

    pgedge controlplane database instance list --database storefront

Read the current backup configuration before you change anything:

    pgedge controlplane database get storefront -o yaml

The `spec` section of that document holds it. The
[Control Plane databases guide](databases.md) covers the rest of the
spec.

## Backup configuration

`backup_config` names where backups go. It sits in the database spec and
carries a `repositories` list. Every entry takes a `type`, which is one
of `s3`, `gcs`, `azure`, `posix` or `cifs`, and then the fields for that
type:

- `s3` takes `s3_bucket`, `s3_region` and an optional `s3_endpoint`.
- `gcs` takes `gcs_bucket` and an optional `gcs_endpoint`.
- `azure` takes `azure_account`, `azure_container` and an optional
  `azure_endpoint`.
- `posix` and `cifs` take `base_path`.

Four more fields apply to an entry of any type:

- `base_path` is the path within the repository that holds the backups.
  Only `posix` and `cifs` require it.
- `id` is an optional name for the entry.
- `retention_full` and `retention_full_type` set how much history the
  repository keeps. `retention_full_type` is `count` or `time`, and
  `retention_full` is a count of full backups or a length of time to
  keep them. The API does not publish the unit it reads `time` in.
- `custom_options` passes extra options to pgBackRest.

A node under `nodes` can carry a `backup_config` of its own. The block
on the database covers every node that has no block of its own.

pgBackRest writes under the base path, into a directory named for the
database and then one named for the entry's `id`. Changing either name
moves where that repository stores backups.

A minimal `s3` repository looks like this:

    backup_config:
      repositories:
        - type: s3
          id: repo1
          s3_bucket: storefront-backups
          s3_region: us-east-2
          retention_full: 2
          retention_full_type: count

### Schedules

`schedules` is an optional list inside the same block. Recurring backups
are a property of the database, so there is no CLI command that
schedules one. Each entry takes three required fields:

- `id` names the schedule.
- `type` is `full` or `incr`. A `full` backup copies the whole
  database, and `incr` copies changes since the last backup of any
  kind. Only the on-demand command below also accepts `diff`, which
  copies changes since the last `full` backup.
- `cron_expression` sets when it runs.

A nightly full backup and an hourly incremental look like this:

    schedules:
      - id: nightly-full
        cron_expression: "0 1 * * *"
        type: full
      - id: hourly-incr
        cron_expression: "0 * * * *"
        type: incr

### Repository credentials

Credentials are optional, and leaving one out keeps a secret out of the
spec file. pgBackRest then uses the credentials the instance already
carries. The fallback differs by repository type:

- For `s3`, leave out `s3_key` and `s3_key_secret`, because pgBackRest
  then uses the default credential provider chain.
- For `gcs`, leave out `gcs_key`, because pgBackRest then uses the
  service account attached to the instance.
- For `azure`, `azure_key` falls back to the machine's managed identity
  on a restore repository. The API promises no fallback on a backup
  repository, so set `azure_key` there.

## Take a backup

`database node backup` backs up one node, so it takes a database ID and
a node name. This command takes a full backup of node `n1` and waits for
the task to finish:

    pgedge controlplane database node backup storefront n1 \
        --type full --wait

`--type` selects the backup kind from `full`, `diff` and `incr`, and
defaults to `full`. Any other value is a usage error at exit 2, raised
before the CLI sends a request.

The command also takes `--force-unmodifiable`, which asks the server to
attempt the backup while the database is in an unmodifiable state. An
unmodifiable state is not one of the database's published state values
such as `creating` or `degraded`, and the API does not define what
makes a state unmodifiable or when a database leaves one beyond the
flag's own name. This command never prompts, so `--force` is an
unknown flag at exit 2.

The backup runs asynchronously:

- Acceptance prints `Backup task <id> accepted (<status>).` on `stderr`.
- `--wait` and `--follow` track the task from there. The
  [high availability operations](ha.md) page covers how the two modes
  differ.
- `--wait` exits 0 when the backup succeeds and 1 when it fails.
  Without `--wait`, `task get` reports the same outcome.
- Under `-o json` or `-o yaml` the accepted response carries the task
  under `task`.
- The [tasks and async operations guide](../tasks-and-async.md) covers
  reading a task later.

pgBackRest owns the contents of the repository, so the CLI has no
command that lists backups. Use pgBackRest's own tooling against the
repository to see what it holds.

## Build a restore spec

A restore reads a spec file rather than flags. `database restore
template` writes a starter spec to `stdout`:

    pgedge controlplane database restore template > restore.yaml

Every line in that file arrives commented out, and its `source_*`
fields carry example values such as `source-db` and `northwind`.
Uncomment `restore_config` and replace those with your own. Its four
required parts are:

- `repository` takes the same per-type fields a `backup_config`
  repository takes, and the same credential fallback applies.
- `source_database_id` is the ID of the database the backup came from,
  the same ID `database list` prints.
- `source_database_name` is that database's `database_name` from its
  spec, which is not always the same string as the ID.
  `database get <id> -o yaml` shows `spec.database_name`. A restore
  renames the database to the target spec's own `database_name` when
  it finishes.
- `source_node_name` is the node the backup came from.

Two further keys are optional:

- `restore_options` sets a point-in-time target. The template offers
  `time` and `lsn` for its `type`. The field is a free-form map of
  strings that the CLI passes through untouched, so a pgBackRest target
  it does not name, such as `xid`, also reaches the server. Omit the
  block to restore to the latest point in the repository.
- `target_nodes` limits which nodes are restored and holds at most nine
  node names. It sits beside `restore_config`, not inside it. Omit it to
  restore every node. The API does not publish why nine is the limit.

A spec that restores every node to the latest point looks like this:

    restore_config:
      repository:
        type: s3
        id: repo1
        s3_bucket: storefront-backups
        s3_region: us-east-2
      source_database_id: storefront
      source_database_name: storefront
      source_node_name: n1

A spec that restores only node `n1` to a point in time looks like
this:

    restore_config:
      repository:
        type: s3
        id: repo1
        s3_bucket: storefront-backups
        s3_region: us-east-2
      source_database_id: storefront
      source_database_name: storefront
      source_node_name: n1
      restore_options:
        type: time
        target: "2026-01-01 00:00:00+00"
    target_nodes:
      - n1

`-i` interviews you instead of writing a blank template:

    pgedge controlplane database restore template -i > restore.yaml

The interview asks for four things: the repository type and its fields,
the three source identifiers, an optional recovery target, and a
comma-separated node list. It needs a terminal and exits 2 when `stdin`
is not one. Prompts go to `stderr` while the spec goes to `stdout`, so
redirecting `-i` works the same way as the plain form. The interview
leaves repository credentials out of the spec entirely.

## Run the restore

A restore rebuilds the named nodes in place from the repository. The
database keeps its identity and loses two things:

- Everything written since the backup.
- Its backup configuration, including every schedule.

A restore has no undo. To go back, restore again from whatever the
repository still holds under its retention settings.

Save the spec before you restore, because the restore deletes the
backup configuration and you will need its contents to put one back:

    pgedge controlplane database get storefront -o yaml > before-restore.yaml

Then apply the edited restore spec:

    pgedge controlplane database restore storefront -f restore.yaml \
        --force --wait

`-f` is required and takes `-` to read the spec from `stdin`. The
restore overwrites live data, so the command prompts first. `--force`
skips that prompt, and `--force-unmodifiable` asks the server to proceed
while the database is in an unmodifiable state.

A restore can run for longer than `--wait` waits. `--wait-timeout`
defaults to 600 seconds and a timeout exits 3. Exit 3 does not mean the
command is safe to repeat, because the restore is probably still
running. Use `--follow` instead when you expect a long restore.

Three checks run on the spec file before the CLI sends a request, each
at exit 2 and each before the confirmation prompt:

1. An unreadable file, a malformed document, or a key the schema does
   not recognize fails here. The message for an unrecognized key names
   the field.
2. A spec whose `restore_config` is empty fails here. An unedited
   template still contains an empty `restore_config`. The message
   names the file and points back at the template.
3. A `restore_config` that still holds the placeholder `CHANGE-ME` in a
   `source_*` field or a repository credential fails here, naming the
   field.

None of these three confirms the spec is complete. A spec missing one
of the four required parts passes all three, and the server refuses it
instead.

A restore spawns one task per node as well as its own:

- The CLI reports how many on `stderr`, then prints `Restore task <id>
  accepted (<status>).`
- Under `-o json` or `-o yaml` the accepted response carries the parent
  task under `task`, the per-node tasks under `node_tasks`, and the
  database under `database`.

## Re-enable backups after a restore

A restore clears the backup configuration from the database and from
every node it restores. After a restore, `database get` does not return
a `backup_config` block. The Control Plane clears the block on every
restore because a restore can change an instance's system identifier,
and pgBackRest refuses to reuse a repository once that identifier moves.

That removal has three consequences:

- Under `-o json` or `-o yaml` the accepted restore response leaves
  `backup_config` out of the `spec` on its `database` object, so test
  whether the key is present.
- While no configuration is in place, `database node backup` fails at
  exit 1 and reports that backups are not configured for this database
  node.
- No scheduled backup runs during that time.

The acknowledgment the CLI prints on `stderr` mentions none of this.

Wait for the restore to finish, then put a configuration back with a
database update. A `database get` document nests the spec under a
top-level `spec` key, and `database update` reads that section, so the
`backup_config` block goes inside it. Copy the block out of the file you
saved before the restore:

    pgedge controlplane database get storefront -o yaml > spec.yaml
    # copy backup_config from before-restore.yaml into spec.yaml,
    # under the top-level spec key, then change its base_path or id
    pgedge controlplane database update storefront -f spec.yaml --wait

Give the repository a location the restored database can start clean in,
by changing `base_path` or by giving the entry a different `id`, so
pgBackRest is not asked to reuse the old location. The old repository
keeps whatever it already holds.

The update re-enables backups, and `database node backup` succeeds again
once it finishes. Two things need carrying over by hand:

- Schedules live inside the same block, so write them back into it.
- A node that carried a `backup_config` of its own loses that one too.
  The database-level block you put back covers every node that has no
  block of its own.

## Next steps

- The [high availability operations](ha.md) document covers switchover,
  failover, instances and host removal on the same databases.
- The [tasks and async operations](../tasks-and-async.md) document
  explains how to inspect a backup or restore task that failed.
