# Troubleshooting the Control Plane

The Control Plane produces symptoms the exit code alone does not
name, and the [troubleshooting guide](../troubleshooting.md) carries
the exit-code contract itself.

## Timeouts on a log follow

One exit 3 timeout is specific to the Control Plane:

- A controlplane `--follow` poll hitting its own fixed 30-second
  bound. The follow as a whole has no bound and ignores
  `--wait-timeout`, but each of its log polls stops at 30 seconds
  regardless of `--timeout`.

## A failed asynchronous operation

One Control Plane write leaves the reason somewhere other than the
resource it created:

- A create that appeared to succeed and produced nothing usable. Read
  the task, not the resource. A failed Control Plane `database create`
  in particular leaves a failed database with no reason attached to
  it, so `pgedge controlplane task get --database <id> <task-id>` is
  what prints the reason, and `task logs` prints the step trace.

The [tasks and async operations guide](../tasks-and-async.md) covers
finding that task and reading its outcome, and
[managing Control Plane databases](databases.md) covers creating the
database. The [incident runbooks](incident-runbooks.md) take a failed
operation, a stopped instance, a full disk, slow queries, replication
lag and an unreachable host or Control Plane through the reads that
narrow each one, and the
[monitoring and alerts guide](monitoring-and-alerts.md) covers
catching them on a schedule.
