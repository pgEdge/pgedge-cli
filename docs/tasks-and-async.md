# Tasks and async operations

Most writes in this CLI are asynchronous: the API accepts the request,
spawns a background task, and answers immediately. The command exits
when the request is accepted, not when the work finishes, so an
accepted request is not finished work. Without a wait flag, a create
that later fails to provision still exits 0, and anything scripted
should wait.

## The wait flags

Four flags appear on every asynchronous command in all three modules:

- `--wait` blocks until the operation's task reaches a terminal state,
  printing one status line per poll to `stderr`.
- `--follow` blocks the same way but streams the task's step messages
  instead of bare status lines, one line per step transition.
- `--wait-timeout` bounds the wait, in seconds.
- `--wait-interval` sets the polling interval, in seconds.

The defaults and the bounds each module applies:

| Module | `--wait-timeout` | `--wait-interval` | Effect on `--follow` |
|---|---|---|---|
| byoc | 600 | 5 | Both flags apply, as they do to `--wait` |
| managed | 600 | 5 | Both flags apply, as they do to `--wait` |
| controlplane | 600 | 3 | Neither flag applies. See below |

The Control Plane's `--follow` ignores both flags. It has no overall
bound at all, and it polls on a fixed two-second cycle rather than on
`--wait-interval`. Each individual log poll gets its own fixed 30
seconds, independent of `--timeout`, so `--timeout 0` still stops a
poll at 30 seconds and only a `--timeout` below 30 seconds shortens
one. Both flags do reach `--wait` on those same commands, so read the
pair as `--wait`'s alone.

`--timeout` is a different bound again: it caps a single HTTP request
in every module and defaults to 30 seconds. A wait's own polls are
capped by the shorter of `--timeout` and the remaining wait deadline,
so a hung request cannot outlive `--wait-timeout` and does not spend
all of it either.

## Exit codes when waiting

`--wait` and `--follow` share one contract: exit 0 when the task
succeeds, 1 when it fails, and 3 when the bound expires. Read `stderr`
for which bound fired, and the [exit codes guide](exit-codes.md) for
what those three numbers mean everywhere else.

The failure path differs in two ways. `--wait` prints the task's own
error message alongside the failure, while a Control Plane `--follow`
reports only that the task failed and leaves the reason to `task get`.
And on the Control Plane a task can also end `canceled`, a third
terminal outcome that exits 1 like a failure everywhere except
`task cancel` itself, where reaching it is the point.

Exit 3 does not mean the command is safe to repeat. The write already
reached the server, and the task is probably still running. Read the
resource, or the task, before retrying anything.

Under `-o json`, a byoc or managed mutating command writes either
nothing at all or the resource as it stood when the request was
accepted. The writes whose API answer carries no body produce nothing,
in every output format: every `delete`, plus byoc `database restore`
and `rotate-password` and managed `database resize` and
`rotate-password`. The rest write the accepted resource and never
refresh it, so a failed create still shows the status it had on
acceptance, carries no error, and prints `stdout` that is
byte-identical whether the task succeeds or fails. Either way, gate on
the exit code, or on a separate `task get`, and not on that object.
Control Plane mutating commands write their accepted-response object
in every wait mode, which is the same caution in a different shape.
The [output formats and paging guide](output-and-paging.md) covers
which successes print nothing more generally.

## Which writes are asynchronous

Every command below spawns a task and accepts `--wait` and `--follow`.
Every read is synchronous, and so are the byoc metadata resources
(`ssh-key`, `cloud-account` and `cluster share`) and both Starfleet
modules' metadata-only `database update`. None of those carries either
flag. byoc `cluster update` is asynchronous while byoc `database
update` is not, because the first changes infrastructure and the
second only changes a record.

The asynchronous commands in each module:

| Module | Asynchronous commands |
|---|---|
| byoc | `cluster create/update/delete`, `database create/delete/restore/rotate-password`, `database mcp/rag/postgrest deploy` and `update`, `database service remove`, `ingress create/delete`, `backup-store create/delete` |
| managed | `database create/delete/resize/rotate-password`, `backup restore`, `database mcp/rag/postgrest deploy` and `update`, `database service remove` |
| controlplane | `database create/update/delete/restore/upgrade`, `database node backup/switchover/failover`, `database instance start/stop/restart`, `host remove`, `task cancel` |

Two writes look asynchronous and are not served by a wait flag:

- byoc `backup create` takes no wait flag and spawns no task the API
  exposes. The work runs through the Control Plane, the response
  carries no body, and there is nothing to poll from this CLI.
- managed `backup create` takes no wait flag either, for a sharper
  reason. The write does spawn a task, but that task reaches its
  terminal state seconds before the backup record leaves `pending`, so
  waiting on it would report success over a backup still running.

## Picking the right signal

A task finishing and a resource being usable are different facts, and
the gap between them differs by command. The signal that terminates
honestly for each managed write:

| Write | Signal to trust |
|---|---|
| `database create` | `--wait`, then `database get` for the status it produced. |
| `database delete` | `--wait`, then `database get` answering exit 4 as the confirmation. |
| `database resize` | `--wait`. The database's own status moves to `modifying`, then settles to `available` or `degraded`. |
| `database rotate-password` | `--wait`, with the timeout caveat below. |
| Any services write | `--wait`, which reports the write's task and not the database's status (see below). Then ask the service itself, because `state` is not readiness. |
| `backup create` | `backup get <id>` polled to a terminal state. Nothing else. |
| `backup restore` | `--wait`, `--follow` or `task get`. Never the status. |

A succeeded task does mean the database's status has been set: the
task record and the database row commit together, so no caller sees a
succeeded task beside a status the operation had not applied. What the
task does not say is which status resulted, so branch on `database
get` when that is the question.

A restore is the case where status cannot help at all. The API answers
by putting the database into `modifying` before the work starts, and
the mandatory pre-restore backup can refuse the whole restore after
that point. That refusal arrives on the task, not as an error from the
command, so a script sees it only through `--wait`, `--follow` or
`task get`.

What `--wait` reports on a services write is that write's task, found
by subject the way every byoc and managed wait finds one, and not the
database's `status`. The status reaches a terminal value either way,
so a poll does stop, but it stops on the wrong question. A failure
that touched only the service leaves the database `available`, which
is the same status a clean write produces. On managed a service that
failed to roll out then reads `state: failed`. On BYOC a failed
deploy records no `state` at all, so the entry keeps whatever an
earlier write stored, or none. A failure that reached wider leaves
the database `degraded`. So `available` after a services write does
not mean the services came up. Take the task from `--wait`, then read
the service's own `state`.

A service's `state` field is not a readiness signal in either module.
It reads `running` as soon as the deploy completes, while the server
itself is still answering 503. On BYOC the platform records the value
when a write succeeds and does not refresh it afterward, so it
reports on the last successful write rather than on the service now. Its one
useful value is `failed`, and a `running` one proves nothing. To know
a deployed service is ready, ask the service, and retry until it
answers.

`backup create` on managed never moves the database's status, so
polling the status after one proves nothing at all: it reads
`available` immediately and always.

byoc and the Control Plane are simpler. Every asynchronous command in
both takes `--wait` and `--follow`, and the task is the signal for all
of them. The one byoc write with no task at all is `backup create`,
above.

## How a wait finds its task

The Control Plane hands the task back. Its mutating commands answer
with an accepted response carrying the task, print `<command> task
<id> accepted (<status>).` to `stderr`, and poll exactly that task, so
there is nothing to discover. Two commands word that line differently:
`host remove` prints `Host removal task <id> accepted (<status>).`
and `task cancel` prints `Cancellation of task <id> requested.`

No byoc or managed response carries a task identifier at all. Waiting
there discovers the task by subject instead: before sending the write,
the CLI reads the subject's newest task, and then tracks the first
task that is not that one. That capture is also why `--wait` on the
mutating command beats running `task wait` afterward: it is tracking
the task from before the write, where `task wait` needs an ID that may
not have appeared yet.

The two modules differ in what happens when that pre-mutation read
fails:

- byoc refuses the write. The read failed, so the write is not sent.
- managed sends the write anyway, because you asked to write and not
  to read a task list, and falls back to an age floor: only a task
  created at or after the moment of the failed read, less a one-minute
  margin, is accepted as this write's.

That margin covers the API's one-second timestamp resolution and any
clock skew between your machine and the server. If the real task never
appears, the wait times out at exit 3 with a message naming the floor
and saying the pre-mutation read failed. That message is also what a
clock skewed by more than a minute looks like, so check the clock if
you see it after a write that plainly succeeded.

## Inspecting a task afterward

Every module has a `task` group, and tasks are scoped per product: a
managed task is not visible under `pgedge starfleet byoc task`, and
neither is visible from `controlplane`. Reach for one of these groups
when no wait flag was passed, when the operation was someone else's,
or when a wait ended in a timeout.

In byoc and managed the group is `list`, `get` and `wait`:

    pgedge starfleet managed task list --subject-id <database-id>
    pgedge starfleet managed task get <task-id>
    pgedge starfleet byoc task wait <task-id> --follow

`task list` takes `--subject-id`, `--subject-kind`, `--name`,
`--status`, `--limit` and `--offset`, and returns tasks newest first.
In both modules the SUBJECT column reads `<kind>/<id>` rather than a
bare ID, and the CREATED column carries a date with no time, so two
tasks on one subject minutes apart look alike in the list. Use `task
get` for the full timestamps.

`--name` is a free-form filter, not a closed set. An unknown name is
not refused, it simply matches nothing at exit 0, which makes a typo
indistinguishable from no such task. Read the names rather than
guessing them:

    pgedge starfleet managed task list --subject-id <database-id> -o json

Two managed names run against the pattern. A resize is
`update-managed-size`, an infix rather than the suffix the others use,
and every services write is `update-managed`, so the filter cannot
tell an MCP deploy from a RAG update.

`task wait` attaches to a task the CLI did not start. It exits 0 on
success, 1 on failure, 3 on timeout and 4 when no task has that ID.
Exit 4 is immediate rather than a wait for the task to appear, so a
task read straight after a mutation may need a moment before it can be
waited on. `--wait` on the mutating command absorbs that delay and
this command cannot, which is why `--wait` is the better choice for
work you start yourself.

In text mode neither `task wait` nor a mutating command's `--wait`
writes anything to `stdout`, on success or on failure, so gate on the
exit code. Under `-o json` the two differ: `task wait` writes the
task, so its status and error describe the outcome, while the mutating
command writes the resource as it stood on acceptance. When a script
needs the outcome as data rather than as an exit code, read it from
`task get` or `task wait`.

`task get` is also where a failed byoc or managed operation explains
itself. Text output prints the summary row, the full timestamps, the
newest step with its status and progress, and the failure reason on a
line of its own. `-o json` carries the whole per-step trace, of which
the text form shows only the last entry.

## Control Plane tasks

The Control Plane's group adds `logs` and `cancel`, and its scoping
rules differ:

    pgedge controlplane task list --database my-db
    pgedge controlplane task get --database my-db <task-id>
    pgedge controlplane task logs --database my-db <task-id>

`task list` works without a scope and lists everything newest first.
Narrow it with `--scope` and `--entity-id`, or list one resource's
tasks with `--database` or `--host`, which are mutually exclusive with
each other and with the `--scope` pair. `--limit` caps the count and
has no ceiling of its own, though zero and negative values are refused
at exit 2. There is no `--offset`, so narrow with the filters rather
than paging. The
[output formats and paging guide](output-and-paging.md) has the paging
contract for every module.

`task get` and `task logs` are stricter: one of `--database` or
`--host` is required, naming the task's scope entity, which is the
ENTITY column of `task list`. Omitting both is a usage error at exit
2, reported before any request is made.

A failed Control Plane `database create` explains itself only on the
task. The Control Plane records no reason on any instance of a failed
create. A spec that fails during planning never creates an instance
at all, so `database get` shows a failed database with nothing
underneath it, and a failure after the instances exist leaves the
database at `failed` with its instance rows still present. `task get`
prints the reason under an Error heading, and `task logs` carries the
step-by-step trace.

`controlplane task cancel` is the only cancel command in the CLI. It
covers database tasks only, `--host` is rejected, and `--database` is
required. It is disruptive, so it prompts unless `--force`, and it is
itself asynchronous, so `--wait` and `--follow` track the
cancellation. This endpoint also answers with a bare task object
rather than the wrapping envelope every other `controlplane` command
uses, so under `-o json` the task's own keys sit at the top level of
the document and a `jq` path needs one level fewer here than
anywhere else.

## A rotation that times out

Managed `rotate-password` carries a caveat no other command does. A
failed rotation leaves its task non-terminal rather than failed, so a
`--wait` on it can only end in a timeout, and that timeout means the
outcome is unknown rather than failed.

Do not re-rotate on a timeout, because that risks replacing a
credential already in place. The job hands the new credential to the
cluster first and only then waits for confirmation, and the cluster
applies it independently, so a worker that died in between leaves a
stuck task over a rotation the database may already have applied.

`task get <task-id>` is the discriminator, but read its timestamps
rather than its status, because a stuck task and a running one both
report the same status. Compare `updated_at` against the current time,
not against `created_at`. A rotation completes within seconds, so a
task whose `updated_at` is minutes old has stopped progressing.
`created_at` is the wrong baseline in both directions: a task that
finished inside the API's one-second timestamp resolution shows the
two equal and is healthy, while one that advanced once and then died
shows them differing and is not.

An unwaited rotation returns no task identifier, so reach its task by
name:

    pgedge starfleet managed task list --subject-id <database-id> \
        --name rotate-password-managed

Taking the newest task of any name is wrong here: a database that has
had more than one operation carries tasks of several names, and two
stamped in the same second are not unusual.

## Next steps

- The [CI and automation guide](ci.md) covers bounding waits and
  scripting against exit codes.
- The [exit codes guide](exit-codes.md) carries the contract a wait's
  0, 1 and 3 belong to, and the
  [troubleshooting guide](troubleshooting.md) is organized by the same
  codes, including the exit 3 a wait produces.
- The [health checks guide](doctor.md) covers the diagnostics to run
  when a wait fails for a connection reason rather than an operational
  one.
