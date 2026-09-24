# Backing up and Restoring a pgEdge Starfleet Managed Database

pgEdge Starfleet Managed backs up a database on its own schedule. You
can also take a backup yourself, or restore the database from one.
Managed owns retention, so this CLI has no command that deletes a
backup.

Three terms run through the page:

- A backup is one recovery point, with its own id, kind and status.
- A task is the platform's record of one asynchronous operation, with
  its own status.
- The pre-restore backup is the backup a restore takes of the current
  data before replacing it.

## Before You Start

You need the database's id and a database whose status is `available`:

- `pgedge starfleet managed database list` shows the `<db-id>` every
  command takes. The id is a full UUID, so an id prefix is refused and
  a database name is refused.
- `pgedge starfleet managed database get <db-id>` reads the status.
  `backup create` and `backup restore` are two of the six writes
  admissible only from `available`. Either is refused against a
  database in any other status, such as `creating` or `degraded`. A
  database already `modifying` because of an earlier one of the six is
  refused too.
- A backup still running fails a restore, so let a new backup reach
  `completed` before you restore.

A restore replaces the database's current data with the backup's
contents. It prompts for confirmation first, and two things limit what
that prompt protects you from:

- `--force` skips the prompt, so a scripted restore asks nothing.
- The prompt names only the backup id, so it cannot warn you that the
  id belongs to another database. The backup names its own database.
  That database is the one the restore replaces.

## Taking a Backup

`backup create` takes a backup now, alongside the ones the platform
takes on its schedule.

1. Take the backup, naming the database and the tier:

        pgedge starfleet managed backup create \
            --database-id <db-id> --kind hot

    `--kind` is required and takes `hot` or `durable`, described
    under [Choosing a Backup Tier](#choosing-a-backup-tier).

2. Record the new backup's id, which the command reports.

    Under `-o json` the new backup goes to stdout, and a script reads
    `.id` from there. Text mode names the database and the backup on
    `stderr`, and those are different ids. A backup's `name` embeds
    its database id, so do not read one for the other.

3. Run `backup get` until the backup reaches `completed` or `failed`:

        pgedge starfleet managed backup get <backup-id>

    `backup get` shows one row with ID, DATABASE, KIND, STATUS,
    CREATED and FINISHED columns. `backup create` reports acceptance,
    not completion. The new backup arrives in status `pending` and
    runs in the background. It is listable and gettable from the
    moment `create` returns. Waiting for `completed` alone never
    returns on a backup that failed. `backup list --database-id
    <db-id>` watches the same record and returns up to 100 backups,
    newest first.

`backup create` has no `--wait`, and neither has `backup get`. The
command does spawn a `backup-managed` task, which appears for the
database within seconds. That task reaches `succeeded` while the
backup record is still `pending`, its own steps reading `Configuring
System` then `Taking Backup` at `progress: 100, status: succeeded`. A
`--wait` on that task would therefore report success over a backup
still in progress. `backup create` also never moves the database's
status, so reading `database get` during a backup proves nothing.

## Choosing a Backup Tier

`--kind` takes one of two tiers:

| Tier | What it gives you |
|---|---|
| `hot` | The fastest tier to restore from. |
| `durable` | Kept apart from the database's own storage; slower to restore. |

Any other `--kind` value is exit status 2, refused locally with
nothing sent.

## Restoring from a Backup

A restore replaces the database's contents with those of one
completed backup. The database keeps its id and its connection
details. Everything written since that backup is no longer in the
database.

1. List the database's backups, and read the whole STATUS column:

        pgedge starfleet managed backup list --database-id <db-id>

    The list shows one database's backups, newest first. The backup
    you restore from has to read `completed`, because the API refuses
    any other restore point upfront. A backup id absent from this list
    belongs to another database. That database is the one a restore of
    it rebuilds.

    Any other backup on this database still reading `pending` fails
    the restore when the API has accepted it. Wait for every row to
    reach `completed` or `failed` before you start. The database's own
    status does not move while a backup runs, so `database get` cannot
    show one.

2. Record the current time, in UTC, which is what identifies the
   backup the restore takes of your current data:

        date -u +%Y-%m-%dT%H:%M:%SZ

    The restore takes a `hot` backup of the current data before
    replacing anything. Nothing on that backup marks it as one.
    [Recovering from an Unintended Restore](#recovering-from-an-unintended-restore)
    finds that backup by the time you record here.

3. Start the restore and wait for its task:

        pgedge starfleet managed backup restore <backup-id> --wait

    The command prompts `Restore backup <id>? The database's current
    data will be replaced by the backup's contents.` before sending
    anything. `--force` skips that prompt, so keep it in a script
    instead of at a terminal.

    `--wait` blocks until the restore's task reaches `succeeded` or
    `failed`. `--wait` exits 0 on success, 1 on failure carrying the
    task's own error, and 3 on timeout. `--follow` blocks the same way
    and streams the task's step messages. `--wait-timeout` bounds the
    wait, 600 seconds by default, and `--wait-interval` sets the
    seconds between reads, 5 by default.

    A backup id that matches nothing is exit status 4 with `backup
    "<id>" not found`. A malformed UUID is exit status 2, refused
    before the prompt.

4. Confirm what the restore produced:

        pgedge starfleet managed database get <db-id> -o json

    Expect `"status": "available"`. A restore that reported success
    without a committed cutover leaves the database at `failed`.

`backup restore` is asynchronous. The backup names its own database,
so the database is not an argument to the command. The API answers
with the database at status `modifying`, and the database recovers in
the background. Under `-o json` or `-o yaml` that database goes to
stdout. Text mode reports the start on `stderr`, in the sentence
naming the backup, the database and the status.

Without a wait flag the command exits on the acceptance, so the
restore's own failures arrive on its task. Run
`task list --subject-id <db-id>`, which lists that database's tasks
newest first, then `task wait <task-id>` to attach to one. `task wait`
answers exit status 4 at once on an unknown id. A task read straight
after the restore may need a moment before it can be waited on.

`--dry-run` runs every client-side check and stops before sending the
write. The checks that read the API do run, so `--dry-run` needs
credentials, and no server-side validation is performed.

## Recovering from an Unintended Restore

The restore takes a `hot` backup of the current data before replacing
anything, and that step is mandatory. A restore that cannot take that
backup fails instead of proceeding. One cause is a backup already
running, because the pre-restore backup cannot start while another
backup is running. That refusal arrives on the restore's task rather
than as an error from the command, because the API has already
accepted the request.

The pre-restore backup is an ordinary backup, and you restore from it
the same way. Nothing on the backup marks it as a pre-restore backup.
In `backup list` and `backup get` the pre-restore backup is identical
to a `hot` backup taken by hand. The pre-restore backup does not
appear in `backup list` at once, and appears within about a minute.

Find the pre-restore backup among the `hot` backups taken since the
time you recorded in step 2:

    pgedge starfleet managed backup list --database-id <db-id> \
        --kind hot --created-after <recorded-time> -o json

`--created-after` takes an RFC3339 timestamp, the form step 2 prints.
Each backup in that output holds a `created_at` field, which shows the
date and the time. The pre-restore backup is the one created as the
restore started. The CREATED column of the table shows the date alone,
so it cannot separate two backups taken on one day.

An empty result means the pre-restore backup has yet to appear, or
that the recorded time is later than the platform's clock. Run the
list again with an earlier `--created-after`.

Without a recorded time, read the restore's start time from the
restore's own task:

    pgedge starfleet managed task list --subject-id <db-id> \
        --name restore-managed
    pgedge starfleet managed task get <task-id>

`task get` prints a Created line holding the date and the time. Pass
that time to `--created-after` above.

No retention is published for the pre-restore backup, so treat it as
a way to undo a mistake noticed soon after, not as an archive.

On a database with durable backups the restore also leaves a
`durable` backup, taken from the restored database when it is up.
That backup records the state the restore produced, not the state it
replaced. It is not a way to undo the restore.

## Troubleshooting

A write refused because the database is not `available` is exit
status 1, and the message reads:

    the resource is busy with another operation and this one needs it
    idle. Wait for it to settle and retry — a 'get' on the resource
    shows its current status, and 'task list' shows what is running.
    (server said (<http status>): <body excerpt>)

The message names no command and no resource kind, so it does not say
which operation was refused. Run `database get <db-id>` for the
current status rather than reading the status out of the message.

`backup create` is the exception. Its refusal quotes the server's own
message, which names the status the backup needed and the status it
found:

    backup requires status "available", current status is "creating"

The second status varies with what the database was doing.

## Next Steps

- [Provision a managed database](provision.md) lists the nine
  statuses, the six writes admissible only from `available`, and
  paging on `backup list`.
- [Tasks and async operations](../tasks-and-async.md) describes
  reading a task by hand.
- [Rotating a Password on a pgEdge Starfleet Managed Database](rotate-credentials.md)
  describes the built-in role passwords.
- [CI and automation](../ci.md) describes wait bounds and exit
  statuses for an unattended run.
- [pgedge starfleet managed command reference](../reference/starfleet-managed.md)
  lists every flag on the commands above.
