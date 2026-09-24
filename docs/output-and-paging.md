# Output formats and paging

Four rules govern how a result reaches you: three output formats, a
guarantee that binds two of them together, successes that print
nothing at all, and the `--limit` and `--offset` contract on the
eleven commands that page.

## Choosing a format

`-o text` is the default and renders tables and human sentences.
`-o json` and `-o yaml` render the API's own object. Those three are
the formats `-o` offers, and an unrecognized value is a usage error
at exit 2 on every command, whether or not that command renders
anything.

A persistent default lives in the config file, outside the profile
block, so it applies to every profile:

    output:
      format: json

An explicit `-o` on the command line overrides that, and omitting
the flag does not. Color is separate again: `--no-color` and the
`NO_COLOR` environment variable both disable it, and the CLI
suppresses color anyway when stdout is not a terminal.

Diagnostics never enter the result. `--verbose` and `--debug` write
to stderr, so `-o json | jq` stays clean with either one enabled.

## YAML keys and JSON keys

The renderer normalizes every payload through its JSON tags before
encoding YAML, so the two formats are one object in two encodings
and a `yq` path always matches the equivalent `jq` path. There is no
separate YAML shape to learn, and `omitempty` behaves identically,
so an unset optional field is absent from both rather than rendered
as null in YAML alone.

That covers all three kinds of payload the CLI emits:

- generated API types, which carry JSON tags and render them, so a
  field is `has_credentials` rather than a lowercased Go name.
- `pgedge version`, the one output struct with explicit YAML tags,
  which declares them identical to its JSON tags and so renders
  `build_date` either way.
- map payloads such as `pgedge controlplane doctor` and `pgedge
  controlplane config view`, whose keys marshal verbatim, so
  snake_case survives.

## Some successes print nothing

A success that carries no usable response body prints nothing to
stdout, in text, JSON and YAML alike. The acknowledgment is exit 0,
and the human sentence goes to stderr, which is what keeps a `jq`
pipe clean. Eighteen commands behave this way. Most are deletes, and
the rest are both modules' `rotate-password`, `managed database
resize`, `byoc backup create`, `byoc database restore`,
`byoc ingress service deregister` and `controlplane cluster join`.

The rule follows the response, not the command name, so do not infer
it from the spelling. `controlplane database delete`, `controlplane
host remove` and both modules' `database service remove` each return
a real object and print it under `-o json` and `-o yaml`. In text
they narrate on stderr like everything else, so the difference is
invisible there. Read `-o json` to tell which kind a command is.

One read prints nothing too. `byoc backup-repository get` answers
empty when the repository holds no data for that node, and empty
stdout is not the `[]` an empty list prints, so a script piping it
into `jq` gets no document rather than an empty one. Test the output
for emptiness before parsing it, or branch on the exit status.

Control Plane inverts the rule for writes. Every `controlplane`
command that spawns a task writes exactly one accepted-response
object to stdout under `-o json` and `-o yaml`, in every wait mode,
with progress and the terminal verdict on stderr. In text mode it
writes nothing to stdout.

`--dry-run` is the other exception: its report is the result, so it
goes to stdout in the requested format even on a command that would
otherwise print nothing. See the [dry run guide](dry-run.md).

## Status and state are different fields

A resource reports its lifecycle in `.status`, while a service
deployed on a database reports its own in `.state`. The two are
different field names with different value sets, and a script that
reads one where the other lives finds nothing. Both fields draw on a
short vocabulary in byoc and managed:

| Field | Ready | In progress | Failed |
|---|---|---|---|
| Cluster, database, backup and backup-store `.status` | available | queued, creating, modifying, deleting | failed, degraded |
| Service `.state` | running | pending | failed |

A resource is ready at `available`, never at "active". A service is
healthy at `running`, but the field is not a readiness signal: the
[tasks and async operations guide](tasks-and-async.md) covers what to
wait on instead. The value
`degraded` is terminal: a resource that reaches it will not recover
on its own, so stop polling and report it. Control Plane has its own
vocabulary, which its
[command reference](reference/controlplane.md) documents per field.

## Paging

`--limit` and `--offset` mean the same thing wherever a command has
them, and most commands do not have them. Exactly eleven carry
`--limit`, each with the page its endpoint applies when the flag is
omitted:

| Command | Default page | Ceiling |
|---|---|---|
| `starfleet byoc backup-repository get` | 100 | none published |
| `starfleet byoc backup-repository list` | 10 | clamped at 100 |
| `starfleet byoc backup-store list` | 10 | clamped at 100 |
| `starfleet byoc cluster list` | 10 | clamped at 100 |
| `starfleet byoc database list` | 10 | clamped at 100 |
| `starfleet byoc ingress list` | 10 | clamped at 100 |
| `starfleet byoc task list` | 25 | clamped at 100 |
| `starfleet managed backup list` | 100 | refused above 100 |
| `starfleet managed database list` | no page applied | refused above 1000 |
| `starfleet managed task list` | 25 | clamped at 100 |
| `controlplane task list` | server default | none published |

In the ceiling column, "clamped at N" means the server silently
returns at most N rows and the CLI sends the larger value anyway,
while "refused above N" means the CLI rejects the value locally at
exit 2 and sends nothing.

Every other list command in every module has neither flag, so an
`unknown flag` error there means the flag does not exist rather than
this contract refusing a value. `controlplane task list` is the only
Control Plane command with a paging flag at all, and it takes
`--limit` without `--offset`.

Omitting a flag is how you ask for the server's default. A value the
flag cannot mean is refused locally at exit 2 with no request sent:
`--limit 0`, a negative `--limit`, and any negative `--offset`.
Writing `--limit "$N"` with the variable unset is the usual way that
happens. `--offset 0` passes, because the zeroth row is an ordinary
first page.

A maximum belongs to an endpoint rather than to a flag name, which
is why the table above differs column by column. Where the API
publishes a bound the CLI enforces it and the error names the
number. Where the API publishes none the CLI enforces none, even on
an endpoint the server is known to clamp, because refusing a value
the contract permits would be wrong the day the cap moves. Only
`managed` declares any maximum, on two operations.

The CLI refuses this request before sending anything, because
`/managed/v1/databases` declares a maximum of 1000:

    pgedge starfleet managed database list --limit 2000

## Reading the truncation hint

The CLI still reports a clamp it does not pre-empt. Every byoc and
managed command that takes `--limit` prints a line beginning
`Showing first` to stderr when a page comes back full, so `managed
task list --limit 500` returning 100 rows says so. The API reports
no total, so the hint can only say more rows might exist, never
confirm it. Page with `--offset` to find out. The hint goes to
stderr, so it leaves machine-readable output alone.

Two paged commands print no hint, for different reasons:

- `managed database list` applies no default page, so an unbounded
  read returns every row and cannot have been truncated. Set
  `--limit` yourself and the hint comes back.
- `controlplane task list` never prints one, on any read. The hint
  is a byoc and managed behavior.

The CLI neither pre-empts a clamp on Control Plane's `limit` nor
reports one afterward, and there is no `--offset` to page past a full
page. Treat a full page as possibly truncated and narrow the read with
`--database`, `--host` or `--scope` instead.

## Next steps

- The [CI and automation guide](ci.md) covers scripting against
  output and exit codes.
- The [exit codes guide](exit-codes.md) explains the code a refused
  `--limit` produces and every other class of failure.
- The [configuration guide](configuration.md) covers the config file
  the `output.format` default lives in.
