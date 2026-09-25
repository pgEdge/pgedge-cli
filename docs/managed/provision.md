# Provision a managed database

A managed database runs on infrastructure pgEdge operates. There is no
cluster to build, no nodes to place and no cloud account to attach, so
the placement decision is a size and a region, and the lifecycle is
create, resize and delete. A new database is closed to every address
until you allow one, and the create step below allows yours.

## Before You Start

You need a tenant with a managed plan. The account also needs a
payment method: the API refuses a create without one. No client-side
check can see that. `<db-id>` is the id `create` reports, and
`pgedge starfleet managed database list` shows it.

## The catalogs

Three catalogs publish the only values `create` accepts. Two of the
three choices are permanent:

    pgedge starfleet managed pg-version list
    pgedge starfleet managed size list --pricing
    pgedge starfleet managed region list

`pg-version list` prints a VERSION and a DEFAULT column. The command
publishes 18, 17 and 16, with 18 the default. The major is set for the
life of the database, because the API refuses an update naming
`pg_version`. Omit `--pg-version` to take the default. An empty value
is exit status 2, not the default.

`size list` prints NAME, DISPLAY, CPU, MEMORY, STORAGE and STATUS, plus
PRICE with `--pricing`. Only `active` sizes are worth creating at. The
size you pick is a floor, because a resize never shrinks.

`region list` prints a single REGION column. `--region` is optional
while the API publishes exactly one region. The CLI reads the list and
sends that value for you. With more than one published, omitting the
flag is an error naming them, because a region is also set for the
life of the database. A region reads as a short identifier such as
`us-east-1`, passed as `--region us-east-1`.

## Name rules

Both name flags are checked locally before anything is sent. `--name`
is tighter than Postgres's own rules because the name is also a
Kubernetes object name. The two flags differ:

| Flag | Rules | Changeable later |
|---|---|---|
| `--name` | Lowercase letters and digits, starting with a letter. No hyphens or underscores. 50 characters. | No |
| `--display-name` | Any text, 25 characters at most. | Yes, with `database update` |

A value that breaks either rule is exit status 2 with nothing sent.
`mydb` is fine and `my-db` never reaches the API. A `--name` the
tenant already uses passes that check. The API refuses that name
instead, before anything is provisioned: HTTP 400 and exit status 1,
with the message naming the database on stderr.

## Create the database

1. Create the database, allow your own address, and wait for the work
   rather than for the acknowledgment:

        pgedge starfleet managed database create \
            --name mydb \
            --size small \
            --my-ip \
            --wait

    The allowlist is the list of IPv4 addresses and blocks that may
    connect to the database. `--my-ip` adds the address the API sees
    this command arriving from. The CLI prints that address on
    stderr, because your Postgres client may connect from another
    address. `--allow <cidr>` adds a known address or block instead,
    and repeats. A bare address is stored as a `/32`:
    `--allow 203.0.113.7`. A block needs an explicit prefix length,
    masked to its network address: `--allow 203.0.113.0/24`.
    `--open` admits every address and cannot be combined with the
    other two. With none of the three the database is created closed
    and nothing can connect. The CLI prints as much on stderr and
    names
    `pgedge starfleet managed database allowlist add <db-id> --my-ip`
    to allow yours. The
    [Controlling Network Access to a pgEdge Starfleet Managed
    Database](network-access.md) guide
    describes every allowlist command.

    Add `--link` to also link the current folder to the new database,
    so `pgedge env pull` can write its `DATABASE_URL` next.

2. Confirm what the create produced:

        pgedge starfleet managed database get <db-id> -o json

    Expect `"status": "available"`. A succeeded task says the
    operation finished, not that the database is usable. The status
    is what to branch on.

    Text mode reports the new id on `stderr`, in the sentence
    acknowledging the database. Under `-o json` or `-o yaml` the whole
    database object goes to stdout instead, where a script reads `.id`.

## The nine statuses

`status` is a bare string in the contract. These nine are the
vocabulary the platform uses, and a value outside them is possible:

| Status | Meaning |
|---|---|
| `creating` | Being provisioned. Not yet usable. |
| `available` | Ready. The only status every write is admissible from. |
| `modifying` | A restore, resize, services write, password rotation or allowlist change is in flight, or a services write failed and never cleared. |
| `deleting` | Being torn down. |
| `failed` | The last operation failed: a create, a teardown, a suspend or resume, or a restore that reported success without a committed cutover. The row is kept either way. |
| `degraded` | A resize or an allowlist change failed after the database was already up. |
| `suspending` | Being hibernated. |
| `suspended` | Hibernated. |
| `resuming` | Coming back from hibernation. |

Wait for `available` rather than for "not `creating`", because a
database can reach `failed` or `degraded` without passing through
`creating` again. No command in this CLI suspends or resumes a
database, so the last three arrive only from elsewhere. A script
treating an unrecognized status as an error breaks on them.

## Writes that require an available database

Six writes are admissible only from `available`, including against a
database already `modifying` because of an earlier one of the six:

- `database resize`
- `backup restore`
- `database rotate-password`
- a services write: `database mcp`, `database rag` or `database
  postgrest` with `deploy` or `update`, plus `database service remove`
- a `database allowlist` change
- `backup create`

`database service remove` names the service by its type, not an ID:

    pgedge starfleet managed database service remove <db-id> mcp

The type is `mcp`, `rag` or `postgrest`. Remove is destructive, so it
prompts unless `--force` is given.

The six share no completion signal. The first five are refused the same way:
exit status 1, with a message saying the resource is busy and to retry when it
settles. An allowlist change's refusal is HTTP 409. Only `backup create`'s
refusal names the current status; for the rest, run `database get` to find it.

A resize or an allowlist change that succeeds settles back on
`available`. One that fails settles on `degraded`, with the reason on
the task. An allowlist change keeps open sessions, because the rules
govern new connections only. A services write that fails leaves the
row at `modifying`. A status still `modifying` when your own timeout
expires is the failure. `backup create` never moves the status. Two
writes take no availability hold. A `database update` carrying only
`--display-name`, `--options` or `--deletion-protection` succeeds
against a busy database. `database delete` is admissible from every
status but `deleting`.

## Waiting on asynchronous work

Create, delete, resize, `backup restore`, `rotate-password`, every
services write and every allowlist change are asynchronous. The API
accepts the request and spawns a task. Without a wait flag, the
command exits 0 on the acceptance, and a create that then fails to
provision still exits 0. Those commands take:

- `--wait`, which blocks until the task reaches a terminal state.
- `--follow`, which blocks the same way and streams the task's step
  messages to `stderr`, one line per transition.
- `--wait-timeout`, which bounds the wait, 600 seconds by default.
- `--wait-interval`, the seconds between status reads, 5 by default.

`--wait` and `--follow` exit 0 on success, 1 on failure carrying the
task's own error, and 3 on timeout. `backup create` takes neither,
because its task reaches `succeeded` while the backup record is still
`pending`. Run `backup get <backup-id>` until `completed` or `failed`.
The [Tasks and async operations](../tasks-and-async.md) describes
reading a task by hand.

## Read a role's credentials back

`database get` returns the connection details for one built-in role:

    pgedge starfleet managed database get <db-id> --user-type admin \
        -o json

`--user-type` takes `admin`, `app` or app_read_only, plus the
long-form aliases `application` and application_read_only. Omit the
flag and app's credentials come back, the role an application
connects as. An empty value is exit status 2. The credentials live in the response's
`connection` object, which reaches you only under `-o json` or
`-o yaml`.

## Resize, protect and delete

A resize moves the database onto different infrastructure, which is
why it is its own command and not a flag on `update`:

    pgedge starfleet managed database resize <db-id> --size large --wait

Resizing is one-way: a database grows and never shrinks, because its
storage cannot. A resize to the same size is refused. On a
per-vCPU plan, a resize also raises the monthly bill, so the command
prompts. An unattended caller passes `--force`, or the command exits 2
saying the operation is destructive.

Deletion protection is a setting on the database, not a flag on
delete. The API enforces the setting. The setting holds for every
other client too:

    pgedge starfleet managed database update <db-id> \
        --deletion-protection

The setting is sent only when you pass the flag. An unrelated
update leaves the setting alone. Turn the setting off with
`--deletion-protection=false` before deleting, or the delete is
refused. Delete is destructive, so it prompts unless `--force` is
given. Delete is also asynchronous, so pass `--wait`:

    pgedge starfleet managed database delete <db-id> --wait

A delete runs a billing teardown first. That teardown is refused while
the database's billing provision is unfinished. Three cases produce
that: a database created seconds ago, and one still resizing, because
a resize reopens the same provision. The third is one whose provision
failed and has not been reconciled yet. An in-flight restore does not
block a delete.

Confirm a delete with `database get`, not the status. A succeeded
delete removes the row, so the answer is not found at exit status 4.
A delete that leaves a row behind is one whose task failed.

## Paging on the lists

The API bounds `--limit` per endpoint, not per flag name. The three
managed list commands page differently:

| Command | Page when `--limit` is omitted | `--limit` accepts |
|---|---|---|
| `database list` | Every row. No default page. | 1 to 1000 |
| `backup list` | 100 rows | 1 to 100, refused above |
| `task list` | 25 rows | Any value, and the server clamps above 100 |

`--limit 0`, a negative `--limit` and a negative `--offset` are usage
errors at exit status 2 with nothing sent. When a page comes back full
at the limit in force, text output prints a hint on `stderr`. The hint
names the number of rows shown and says there may be more. `database
list` with no `--limit` never prints one, because a read with no page
cannot have been truncated. The API reports no total. The hint cannot
say how many more, and you page with `--offset` to find out.

## Next steps

- [Controlling Network Access to a pgEdge Starfleet Managed
  Database](network-access.md) describes
  every allowlist command, including one list per deployed service.
- [Loading a Schema and Data into a pgEdge Starfleet Managed Database](load-data.md)
  describes getting a dump into the database you just created.
- [Backing up and Restoring a pgEdge Starfleet Managed Database](backup-restore.md)
  describes taking a backup and restoring from one.
- [pgedge starfleet managed command reference](../reference/starfleet-managed.md)
  lists every flag on the commands above.
