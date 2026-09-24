# Reading Logs and Metrics from a pgEdge Starfleet Managed Database

Two commands inspect a running database without changing it. Use
`database logs` for its Postgres log records. Use `database metrics`
for what the platform records about Postgres itself and about the
container around it.

Three terms run through this page. A sample is the whole set of metrics read at
one instant, and one arrives every 30 seconds. A series is the full table
behind a metrics read, with a column for each metric. A task is what a managed
write hands its work to, and where the result of that write ends up.

## Before You Start

You need the database's ID:

- `pgedge starfleet managed database list` prints the ID of every
  managed database in the account. The log command and the metrics
  command each want the whole ID.

## Reading the Log Records

Give the log command the database ID on its own to read the most recent log
records:

    pgedge starfleet managed database logs <database-id>

Each record takes a single line in text mode: a timestamp, a level in
a padded column, then the message body. Because nothing ever breaks
across two lines, you can pipe the result straight into grep. The
command prints the records in the order the API sent them, adding no
ordering of its own. Timestamps travel as millisecond epochs. Text mode
converts each one to RFC3339 in UTC. Under `-o json` and `-o yaml` you
get the untouched number.

The timestamp, the level and the message were each observed on the wire, not
contracted, so a record may turn up short of one. In that position, and where a
value is blank or the wrong type, you get `-` instead, and the read still
succeeds.

Bound the read with either of two settings:

- `--max-lines` accepts 1 through 1000, and the API sends 100 when it
  is left off.
- `--start-time` and `--end-time` each want an RFC3339 timestamp. Drop
  either one and that end of the span stays open.

Combine both settings to bound the span and cap how many records come
back:

    pgedge starfleet managed database logs <database-id> \
      --max-lines 500 --start-time 2026-08-17T00:00:00Z

## Reading the Metrics

The metrics command reports on one database at a time. Ask for a
lookback of at least three minutes, because a shorter one often comes
back empty:

    pgedge starfleet managed database metrics <database-id> \
      --window 5,minutes

A series comes back roughly 36 columns wide, running from
pg_database_size_bytes to the replication slot counts. The container's own CPU
and memory readings are among the columns. That is too wide for any terminal,
so text mode turns one sample on its side into two columns, headed METRIC and
VALUE. To see every sample instead of one, ask for `-o json` or `-o yaml`.

You are shown the freshest complete sample, meaning the latest row that has a
value in every column. Completeness wins over recency because the last sample
in a lookback has often not finished collecting. The last sample's container
figures can land empty even when every sample before it is intact. Row order is
not guaranteed, so a row is ranked by its `time` cell instead of by where it
sits. Should none of the rows be complete, you get the latest one exactly as it
arrived.

The `time` row tells you which sample this is, spelled as a
millisecond epoch and left alone. Every other cell appears as the API
sent it, at its original scale and precision.

## Choosing a Metrics Window

`--window` asks for a relative lookback written as `value,unit`, for
example `15,minutes`. Write up to four digits, then a comma, then one
of `second`, `minute`, `hour` and `day`. A trailing s is optional. A
space works in place of the comma, though a shell then needs the whole
value quoted.

A relative lookback decides the span only while both `--start-time`
and `--end-time` are absent. Each of those takes an RFC3339 timestamp.
Supply either one and it decides the span instead, leaving the other
end open. Whenever `--window` is supplied it is validated, whether or
not it is used.

Start at three minutes. Samples land at 30-second intervals, and the freshest
one has trailed real time by 72 to 101 seconds. A lookback shorter than the
current lag closes before that sample exists, so it comes back empty. Sixty
seconds never finds anything. Ninety seconds clears the shortest lag seen and
not the longest, so it finds rows only sometimes. Two minutes clears the lag
every time, though about half of those reads hold a lone sample. That is the
case described under
[Fewer Metric Rows than Expected](#fewer-metric-rows-than-expected).

Zero is rejected locally, at exit status 2. The API would accept zero and
answer with a series holding nothing, which looks like a database that has
never reported.

`--wait-interval` is a different flag entirely, and it sets how many seconds
pass between the checks a command makes while it waits on a task.

## Reading Around a Failed Operation

A managed write that hands off to a task exits 0 on acceptance, well
before the work is done. Whatever happens next lives on the task. Add
`--wait` and the command blocks until the task settles. A task that
then fails exits 1, and a wait that runs past its deadline exits 3.
Add `--follow` for the same block plus a live stream of the steps.

The managed commands that hand off this way are:

- `database create`, `database delete`, `database resize` and
  `database rotate-password`
- `database backup restore`
- `database allowlist add`, `remove`, `set`, `open` and `clear`
- `database service remove`
- `deploy` and `update` under `database mcp`, `database rag` and
  `database postgrest`

`database update` completes inside the command. `backup create` takes neither
route: the API accepts the backup and returns it immediately, so the command
reports acceptance rather than completion. Read `backup get <backup-id>` until
the backup reaches a terminal state.

When a command that hands off has already exited, locate its task and point
both reads on this page at the period that task covered.

1.  List the database's failed tasks. A task's subject is the
    resource it acted on:

        pgedge starfleet managed task list --subject-id <database-id> \
          --status failed

2.  Take an ID from that list and open the task:

        pgedge starfleet managed task get <task-id>

    A summary row comes first, followed by Created, Updated, the most
    recent step message, and an Error block where one is set. The two
    timestamps are written out in full, which the row's CREATED column
    is not. They are the only bounds that tie a later read to this
    task rather than to a neighboring one.

3.  Read the log records over that period, passing the Created and
    Updated values from step 2:

        pgedge starfleet managed database logs <database-id> \
          --start-time <created> --end-time <updated>

4.  Read the metrics across that same period, with the same two
    values. A span under 30 seconds can fall between samples, so widen
    it at both ends when nothing comes back:

        pgedge starfleet managed database metrics <database-id> \
          --start-time <created> --end-time <updated>

`task get` reports only the most recent step. To watch the whole
sequence as it happens, run
`pgedge starfleet managed task wait <task-id> --follow`.

## Reading a Branch's Logs and Metrics

A branch has logs and metrics of its own. Its log records never
appear under its source database's logs, and the source's records
never appear under the branch. Read a branch by passing the database
ID and then the branch ID:

    pgedge starfleet managed database branch logs <database-id> \
      <branch-id> --max-lines 500
    pgedge starfleet managed database branch metrics <database-id> \
      <branch-id> --window 5,minutes

`pgedge starfleet managed database branch list <database-id>` prints
the branch IDs. Both commands take the same flags as their database
counterparts, check them the same way before sending a request, and
print their results the same way.
[Branching a pgEdge Starfleet Managed Database](branching.md) covers
creating and deleting branches.

## Troubleshooting

Only the last entry below is a failure. The other three describe a
successful read that surprised you.

### No Logs or Metrics Came Back

An empty read is a success to both commands. Each writes one sentence
to stderr, exits 0, and leaves stdout with nothing in it. From the log
command you get `No logs found.` The metrics command repeats the span
you asked for, and where that span runs up to now, it adds that the
freshest sample trails behind.

Three conditions produce that result:

- The period really does hold no record and no sample.
- The metrics lookback is shorter than the collector's delay, so
  widen it to three minutes.
- The environment cannot read the metrics store at all. Every lookback
  is then empty at exit 0, whatever you ask for.

All three exit 0, so neither command's exit status reports whether the
platform is healthy. Widen the lookback and read again. With
`--start-time` in play, push that flag back instead of reaching for
`--window`, because the API disregards a lookback alongside a start
time.

### Fewer Metric Rows than Expected

Where a metric holds nothing across the whole period, the reply omits it
instead of sending a blank. Your table is shorter, and no marker shows where
the row would have been. Pinned to one sample that was still being collected,
a read has come back with 29 rows and with 34, where 36 is normal. The rows
for CPU and for memory were among the ones absent.

A period holding a single sample is the worst case. There, "nothing
in this row" and "nothing anywhere" amount to the same claim, so text
mode warns about such a period on stderr.

The read still exits 0. Widen the lookback to three minutes, then
inspect `-o json` for the exact set of metrics that arrived.

### Two Samples Share One Timestamp

A restore or a resize swaps the database's instance. Through the
handover, one `time` value holds two samples, one from each instance.
Text mode picks one, warns on stderr and still exits 0. The warning
always counts the rows sharing the timestamp, and quotes the
instance_name of the row it chose where the series carries that
column. The warning may then add one more fact. Where the row shown is
itself partial the warning says so, and where other tied rows were
incomplete the warning counts them.

Which instance is arriving is not something the warning can tell you.
Fetch both rows with `-o json`, which prints no warning of its own, so
watch for a repeated `time` value yourself. Before you compute a rate
or a delta over a series crossing a swap, group it by the instance
column. The restore that causes this is described in
[Backing up and Restoring a pgEdge Starfleet Managed Database](backup-restore.md).

### The Command Exits 2 Before Connecting

A `--max-lines` outside its bounds, and a `--window` that is malformed
or zero, are both rejected before a request leaves the machine. Exit
status 2 means bad usage, so the fault is in the value you typed and
not in the database. Fix the value and run the command again.

## Next Steps

- [pgedge starfleet managed command reference](../reference/starfleet-managed.md)
  carries every flag both commands take.
- [Tasks and async operations](../tasks-and-async.md) explains
  `--wait` and `--follow` at length.
- [Monitoring and alerts for managed databases](monitoring-and-alerts.md)
  turns these reads into a recurring check.
- [Troubleshooting managed databases](troubleshooting.md) is keyed by
  symptom, and one of its entries is an empty log or metrics read.
- [Error catalog](../error-catalog.md) is keyed by message text,
  and it holds the two exit-2 lines these commands print.
