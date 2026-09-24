# Monitoring and alerts for managed databases

Nothing in this CLI pushes. Every observability surface it carries is
a read you make, so monitoring is built by polling those reads on a
schedule, turning each answer into a verdict, and letting an exit
status carry the verdict out to whatever runs the schedule. The
[logs and metrics guide](logs-and-metrics.md) documents the reading
itself, and this page covers what to build on top of it.

## What the CLI exposes

An alert rule draws on metrics, logs, a resource's own record, the
task list and the connection diagnostic, and every one of them is a
read that prompts for nothing and writes nothing. The following table
describes each command:

| Command | What it reads |
|---|---|
| `pgedge starfleet managed database metrics` | Postgres and container metrics for one managed database, over a window set by `--window`, `--start-time` and `--end-time`. |
| `pgedge starfleet managed database logs` | Postgres log records, newest first, capped by `--max-lines` and bounded by `--start-time` and `--end-time`. |
| `pgedge starfleet managed database get` | The database record, whose `status` field carries the lifecycle value. |
| `pgedge starfleet managed task list` | The tasks behind asynchronous writes, filtered by `--subject-id`, `--name` and `--status`. |
| `pgedge starfleet doctor` | The Starfleet connection, at exit 0 whatever it finds, so a rule reads its fields under `-o json` rather than its status. |

Managed takes `--window`, in `value,unit` form with second, minute,
hour or day as the unit, singular or plural, and a comma or a space
between the two. The command refuses a zero value at exit 2 before any
request leaves. Managed validates `--window` whenever you pass it, and
the API gives the value effect only when neither `--start-time` nor
`--end-time` is present.

## A metrics poll with a threshold

A threshold rule reads the metrics as JSON, picks one metric out of
the series, and compares it against a number you chose. Under `-o
json` and `-o yaml` managed answers with the API's own object, a
container of series where each series carries a name, its column names
and its rows, so a filter selects a metric by finding its name among
the columns and reading that position out of a row.

Row order is not published, so select the newest sample by the `time`
column rather than by taking the last row. Size the window above the
collector's publication lag as well, because a window shorter than the
lag ends before the newest sample exists and comes back empty at exit
0. The [logs and metrics guide](logs-and-metrics.md) carries the lag
and the window guidance behind that rule.

The following script reads one managed database's metrics, applies a
threshold to the newest sample, and reports a breach on its own exit
status:

    #!/bin/sh
    set -u

    db_id="$1"
    limit="$2"

    pgedge starfleet managed database metrics "$db_id" \
        --window 15,minutes --profile monitor --timeout 30s \
        -o json > metrics.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "metrics read exited $rc" >&2
        exit "$rc"
    fi

    jq -e --argjson limit "$limit" '
        (.series[0] // {}) as $s
        | ($s.columns // []) as $c
        | ($c | index("time")) as $t
        | ($c | index("pg_database_size_bytes")) as $m
        | if $t == null or $m == null
              or (($s.values // []) | length) == 0
          then empty
          else ($s.values | max_by(.[$t]))[$m] < $limit
          end
    ' metrics.json > /dev/null
    verdict=$?

    case "$verdict" in
        0)
            exit 0
            ;;
        1)
            echo "$db_id is over the threshold" >&2
            exit 1
            ;;
        *)
            echo "$db_id returned no usable sample" >&2
            exit 0
            ;;
    esac

The read goes to a file and its status is captured before anything
parses it, so a failed read exits with the CLI's own code before the
filter ever runs. Writing the read as `pgedge ... | jq` loses that,
because the pipeline reports the filter's status and a failed read
then reaches the rule as an empty input instead of a failure.

The filter's three outcomes are what the case statement branches on:

- `jq -e` exits 0 when the comparison is true, which is the sample
  sitting under the threshold.
- `jq -e` exits 1 when the comparison is false, which is the breach
  worth alerting on.
- `jq -e` exits 4 when the filter produced no output at all, which is
  an empty window or a metric the response omitted, because a metric
  with no value anywhere in the window is left out of the response
  rather than sent as null.

When the window comes back empty, managed names the window it asked
for and, unless `--end-time` was set, the collector lag behind it.

pg_database_size_bytes is one of the metric names the series carries,
and any other column name substitutes for it directly. Keep the alert
action itself out of the comparison. The script above says what
happened on stderr and exits non-zero, which is the signal cron, a CI
runner or a pager sidecar already knows how to read.

## Alerting on failed operations

Most writes are asynchronous, and without a wait flag a create that
later fails to provision still exits 0. The write's own status is
therefore not the whole story, and a scheduled sweep of the task list
catches the failures a script accepted and walked away from.

`task list` filters server-side on `--status`, which takes queued,
running, succeeded or failed. The following script fails when any task
against one database has failed:

    #!/bin/sh
    set -u

    db_id="$1"

    pgedge starfleet managed task list --subject-id "$db_id" \
        --status failed --profile monitor --timeout 30s \
        -o json > tasks.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "task list exited $rc" >&2
        exit "$rc"
    fi

    if ! jq -e 'length == 0' tasks.json > /dev/null
    then
        jq -r '.[] | "\(.name) \(.id) failed"' tasks.json >&2
        exit 1
    fi

The database record is the second signal, and it answers a different
question: `task list` says an operation ended badly, while `database
get` says what state the database was left in. The following script
reads that state and decides on it:

    #!/bin/sh
    set -u

    db_id="$1"

    pgedge starfleet managed database get "$db_id" \
        --profile monitor --timeout 30s -o json > db.json
    rc=$?
    if [ "$rc" -ne 0 ]
    then
        echo "database get exited $rc" >&2
        exit "$rc"
    fi

    db_status=$(jq -r .status db.json)
    if [ -z "$db_status" ] || [ "$db_status" = "null" ]; then
        echo "db.json carries no status" >&2
        exit 1
    fi
    case "$db_status" in
        available)
            exit 0
            ;;
        failed|degraded)
            echo "$db_id is $db_status" >&2
            exit 1
            ;;
        *)
            echo "$db_id is $db_status, not alerting" >&2
            exit 0
            ;;
    esac

A managed database's status reports one of nine values, and they fall
into three groups an alert rule treats differently:

- `available` is the ready value, and a readiness check compares
  against it rather than against "not `creating`", because a database
  can reach `failed` or `degraded` without passing through `creating`
  again.
- `failed` and `degraded` are the two worth paging on, and `degraded`
  is terminal, so a rule that sees it should report rather than keep
  polling.
- `creating`, `modifying` and `deleting` describe work in flight, while
  `suspending`, `suspended` and `resuming` describe a hibernation no command in
  this CLI performs.

The field is a bare string in the contract, so those nine are the
vocabulary the platform uses rather than a fixed set, and a value
outside the list is possible. A rule that treats every unrecognized
value as a failure pages on one, which is why the script above lets
its default branch pass.

A service deployed on a database reports its own lifecycle in `state`
rather than in `status`, on a shorter vocabulary, and a script reading
one field where the other lives finds nothing. `state` is not a
readiness signal: it reads `running` as soon as the deploy completes,
while the server itself may still be refusing requests, so its one
useful value for an alert is `failed`.

Read the exit status of every one of these calls before reading its
output. A plan-entitlement rejection lands on exit 5 alongside a
rejected credential, so a rule that retries authentication failures
spins on a refusal no credential will ever fix.

Running one of these scripts unattended is covered by
[scheduling a poll](../ci.md#scheduling-a-poll).

## Next steps

- The [logs and metrics guide](logs-and-metrics.md) documents the
  metrics and log commands themselves and the window rules.
- The [CI and automation guide](../ci.md) covers unattended
  credentials, complete pipelines on two runners, and scripting
  against output and exit codes.
- The [exit codes guide](../exit-codes.md) carries the contract an
  alert rule branches on and the commands that deliberately depart
  from it.
- The [health checks guide](../doctor.md) covers the diagnostics to
  run when a poll fails for a connection reason rather than an
  operational one.
