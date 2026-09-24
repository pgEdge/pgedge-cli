# Back up and restore a BYOC database

A BYOC backup runs through pgBackRest: the CLI triggers the backup,
finds it again in storage, and restores the database from it. Backups
land in cloud object storage inside your own account, which adds one
step a managed database skips: you register that storage yourself.
The CLI also reads a BYOC backup back out of pgBackRest, rather than
out of a backup table.

You need the database's full UUID and a cloud account already
registered on the tenant.

## BYOC and managed backup surfaces

On BYOC, no command lists, inspects, downloads or deletes a backup.
`backup create` is the whole backup command group.

The following table compares the two products' backup surfaces:

| Question | BYOC | Managed |
|---|---|---|
| Take a backup | `starfleet byoc backup create` | `starfleet managed backup create` |
| List backups | No command. Read the repository inventory instead. | `starfleet managed backup list` |
| Inspect one backup | No command. | `starfleet managed backup get` |
| Delete or download a backup | No command. | No command. |
| Restore | `starfleet byoc database restore`, which names the database | `starfleet managed backup restore`, which names the backup |
| Where a backup lives | A backup store you register in your own cloud account | Storage the platform owns |
| Watching a backup finish | No wait flag, and no task to read repeatedly | Read the backup record repeatedly. The task outruns a read that tries to catch it in progress. |

The
[Backing up and Restoring a pgEdge Starfleet Managed Database](../managed/backup-restore.md)
guide covers the right-hand column. Nothing on this page applies
there, and nothing there applies here.

## Backup stores

A backup store is the cloud object storage a cluster writes its
backups to, registered against one of your cloud accounts. A cluster
needs at least one store attached before it can host a database, so
in practice a store exists before its database does.

Creating a backup store takes a name and a cloud account, and
provisions real storage. Find the cloud account ID with
`cloud-account list`. Pass `--wait` to block the command until
provisioning finishes:

    pgedge starfleet byoc backup-store create \
        --name nightly-bkp \
        --cloud-account-id <cloud-account-id> \
        --region us-east-1 --wait

No update command exists for a backup store. Both the name and the
region are decided at creation:

- The name is at most 14 characters, lowercase alphanumeric and
  hyphens. This limit is tighter than the API's other name rules
  because the store name is embedded in an S3 bucket name. The limit
  appears in neither the published schema nor a client-side check, so
  the refusal arrives from the server, reading `name must be 1-14
  characters long`.
- The region cannot change after creation, and the store record
  carries no region field at all, so no command reads the region back
  afterward. Pass `--region` explicitly if you need to know which one
  you got. A store created in the wrong region has to be replaced.
  Omit `--region` to let the API choose the region. Passing the flag
  with an empty value, which is what `--region "$REGION"` does when
  the variable is unset, is refused at exit 2, not accepted silently.

`backup-store list` and `backup-store get` share five columns: `ID`,
`NAME`, `STATUS`, `CLOUD ACCOUNT ID` and `CREATED AT`. `backup-store
get` prints a one-row table carrying those same columns, not a field
and value block. Read a store with `-o json` for the fields no column
shows, including `cloud_account_type`, `cluster_ids` and the computed
`properties` object that names the bucket.

`backup-store list` pages with `--limit` and `--offset`, and narrows
by creation time with `--created-after` and `--created-before`. The
[Output formats and paging](../output-and-paging.md) guide carries
the paging contract for every list command in the CLI.

### Delete a backup store

Deleting a store is destructive, so it prompts for confirmation
unless you pass `--force`, and it succeeds only against an empty
store. A store still holding backup data answers `400 bucket is not
empty`.

Deletion is also asynchronous and takes `--wait`, `--follow`,
`--wait-timeout` and `--wait-interval`, the same as creation. Exit 0
with none of those flags means the platform accepted the request, not
that it tore down the store. When the next step depends on the
teardown finishing, pass `--wait`.

pgEdge never deletes customer backup data, and deleting a database
leaves its backups intact. Clearing a store is a two-step operation
you own:

1. Read the bucket name from the store record:

        pgedge starfleet byoc backup-store get <store-id> -o json

    Take `properties.bucket_name` from the output.

2. Delete the backup data you no longer want from that bucket
   yourself, using the cloud account's own console or CLI. Delete
   every object version too, because the buckets are versioned. Then
   retry the store delete:

        pgedge starfleet byoc backup-store delete <store-id> \
            --force --wait

## Backup repositories

A repository is not a store. A store is the cloud bucket you
register, and a repository is the per-database pgBackRest stanza
written into one. BYOC creates a repository automatically when it
builds the database, so no create or delete command exists. The two
reads below are the entire surface.

Find a database's repositories, and the repository ID a restore
needs, by filtering the list:

    pgedge starfleet byoc backup-repository list \
        --database-id <db-id> -o json

The list columns are `ID`, `DATABASE ID`, `TYPE`, `LOCATION`,
`RETENTION` and `CREATED`. `LOCATION` is the bucket or container
behind the repository, and `RETENTION` is the count and its unit.
Omitting `--database-id` lists across every database. Passing
`--database-id` with an empty value is refused at exit 2, instead of
widening the read. This command also takes `--type` to filter by
repository type, `--descending` to reverse the order, and `--limit`
and `--offset` to page.

Reading one repository's inventory takes the repository UUID and a
node name. Find a database's node names with `database get <db-id>
-o json`, reading `nodes[].name`. This read is the closest thing BYOC
has to a backup list:

    pgedge starfleet byoc backup-repository get <repository-id> n1

The backups table carries `LABEL`, `TYPE`, `SIZE`, `DATABASE SIZE`,
`STARTED` and `FINISHED`. The repository, node, status and Postgres
version are written to stderr above the table. A label is what
`database restore --set` takes.

This command differs from the list commands in three ways:

- The read contacts pgBackRest inside the backup store, instead of
  reading a database row. The read answers `500 failed to connect to
  backup repository` when the store is unreachable or the database
  behind the store is gone. That is a live-storage failure, not a
  missing record.
- A repository holding no data for that node is not an error. The API
  answers with an empty body, so the command exits 0 and reports this
  on stderr. Stdout prints nothing at all, in any output format.
  Empty stdout is not the `[]` an empty list prints, so a `jq` pipe
  gets no document to parse. The
  [Output formats and paging](../output-and-paging.md) guide lists
  the other commands that behave this way. A repository that does
  answer, but holds no backups matching the read, is a different
  shape. `-o json` prints whatever the server returned for the
  repository, and only the table view reports the emptiness on
  stderr.
- `--type` accepts `full`, `diff` or `incr`. Anything else is refused
  locally at exit 2, before the request. `--descending`, `--limit`
  and `--offset` apply to the backups the read returns, not to
  repositories, and this read returns 100 backups when `--limit` is
  omitted.

## Take a backup

`backup create` triggers an on-demand backup, requiring the database
and a provider. `pgbackrest` is what BYOC runs, and it is also what
`database restore` defaults to:

    pgedge starfleet byoc backup create \
        --database-id <db-id> --provider pgbackrest \
        --type full --name pre-migration

`--name` and `--type` are optional and travel in the request body. No
BYOC command reads a backup back, so neither label is visible from
the CLI afterward. `--target-nodes` restricts the backup to named
nodes. The flag passes straight through: the names reach the request
body with only surrounding whitespace trimmed. The CLI never resolves
or checks the names against the cluster, so a typo produces a
server-side failure, not a local one. `--dry-run` reports the request
that would have been sent and stops before sending it, covered in the
[Dry runs](../dry-run.md) guide.

The API answers with an empty body, so the acknowledgment is exit 0
and a `Backup initiated.` line on stderr. Stdout stays empty in text,
JSON and YAML alike, so a script should read the exit status, not the
output.

No task exists to wait on either, which is why this command has no
`--wait`. A BYOC backup runs through the Control Plane instead of
creating a task in the API, so nothing appears under
`starfleet byoc task list` for the backup. Confirm the backup landed
by reading the repository inventory afterward and looking for a new
label:

    pgedge starfleet byoc backup-repository get <repository-id> n1

## Before you restore

A restore overwrites the database's current contents. Anything
written since the backup was taken is gone. The CLI cannot reverse the
operation, so complete the following checks before a restore:

- Confirm nothing else is running against the database or its
  cluster. When the API refuses on that ground, the CLI prints its
  own message instead of the server's. The message says the resource
  is busy and tells you to wait and retry. The CLI also points to a
  `get` command for the current status and to `task list` for what is
  running. The server's own sentence is quoted underneath, on a line
  beginning `(server said`. Several of the refusals behind that
  message, restore included, key on the CLUSTER, not the database, so
  `database get` can report `available` while the refusal still
  stands. Check the cluster's tasks too, with `task list --subject-id
  <cluster-id>`.
- Choose the repository. `--repository` is required, takes a
  repository UUID from `backup-repository list`, and repeats, so you
  can name more than one. `--repository` is the only UUID-carrying
  flag in the byoc tree whose name does not end in `-id`.
- When you do not want the latest backup, choose the backup set.
  `--set` is optional and takes a pgBackRest backup set label, such as
  `20240619-195803F`, taken straight from the repository inventory.
- Decide where the data lands. `--node-name` names the node whose
  backup is restored and is required. `--target-nodes` names the
  nodes restored onto, and defaults to all of them.
- Know which force you mean. `--force` skips the CLI's confirmation
  prompt and nothing else. pgBackRest's own force option is exposed
  separately as `--pgbackrest-force`, and `--force` never sets it.
- Plan for the prompt. Restore is one of the eleven byoc commands that
  confirm before acting, and the test is whether stdin is a terminal.
  In a script or a CI job, the restore fails with a usage error asking
  for `--force` instead of hanging. An unattended run must pass the
  flag. The other ten are `backup-store delete`, `cloud-account
  delete`, `cluster delete`, `cluster share delete`, `database
  delete`, `database rotate-password`, `database service remove`,
  `ingress delete`, `ingress service deregister`, and `ssh-key
  delete`.

## Run the restore

The restore names the database, takes the repository and node, and
blocks until its task reaches a terminal state when you pass
`--wait`. The command looks like this:

    pgedge starfleet byoc database restore <db-id> \
        --node-name n1 \
        --repository <repository-id> \
        --force --wait

Pair `--type` with `--target` to recover to a point in time instead
of to the end of the backup. `--target` must be RFC3339 with an
explicit UTC offset, such as `Z` for UTC or `-04:00` for a fixed
offset. The platform converts the value to UTC and rejects one that
omits the offset. Recovery optionally stops before the target, not
including it:

    pgedge starfleet byoc database restore <db-id> \
        --node-name n1 \
        --repository <repository-id> \
        --type time --target "2024-06-19T20:00:00Z" \
        --target-exclusive --force --wait

Add `--delta` to have pgBackRest replace only the files that differ,
instead of the whole data directory.

`--provider` defaults to `pgbackrest` here and rarely needs setting.
`--dry-run` runs the client-side checks, reports the request that
would have been sent, and stops before sending it, covered in the
[Dry runs](../dry-run.md) guide.

Like `backup create`, the restore returns an empty body. Stderr
carries the `Restore started for database <id>.` line, and stdout
stays empty. Unlike `backup create`, the restore does create a task,
so `--wait` and `--follow` have a task to track. Run the restore with
neither flag, in table or text output. The CLI then prints the `task
list --subject-id` command for the new task on stderr, so you can
pick up the work later. That hint is a table and text courtesy, so
`-o json` and `-o yaml` do not print it.

## Wait on a restore

`--wait` runs a check against the database's task until the task
reaches a terminal state, printing a status line at each check.
`--follow` runs the same way but streams the task's step messages
instead. Both bound themselves with `--wait-timeout`, 600 seconds by
default, and check again every `--wait-interval` seconds, 5 by
default.

The three outcomes map to exit codes: 0 when the task succeeded, 1
when it failed, and 3 when the wait ran out before the task finished.
Exit 3 means the restore is still running, not that it failed. The
[Tasks and async operations](../tasks-and-async.md) guide covers
picking the task up again afterward.

A database accepts one change at a time. While a change is settling,
its `.status` reads `modifying`. The API refuses a second write
against the database with the busy message described above. That
message is the CLI's own wait and retry guidance, with the server's
sentence quoted underneath. The database returns to `available` when
the work completes. The values `failed` and `degraded` are terminal,
so a database that reaches either state does not recover on its own,
and the task record holds the reason. The
[Output formats and paging](../output-and-paging.md) guide carries
the full status vocabulary and the `.status` against `.state`
distinction.

## Next steps

- The
  [Backing up and Restoring a pgEdge Starfleet Managed Database](../managed/backup-restore.md)
  guide covers the same job on a managed database, with different
  commands and signals.
- The [Tasks and async operations](../tasks-and-async.md) guide
  covers reading a restore's task after the fact.
- The [Logs and metrics on BYOC](logs-and-metrics.md) guide covers
  reading a database's Postgres logs. A restore that failed on the
  data, not on the request, explains itself there.
- The [Exit codes](../exit-codes.md) guide covers what each failure
  code means when one of these commands refuses.
- The
  [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
