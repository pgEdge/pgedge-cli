# Exit codes

Every command in every module reports its outcome with the same six
codes. One number names one class of failure, and stderr says which
member of the class you hit. A script branches on the code. The
message on stderr is for the person reading the log afterward.

## The contract

Every code the CLI produces:

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General runtime failure |
| 2 | The command was malformed |
| 3 | Timed out |
| 4 | Not found |
| 5 | Authentication failed |

No module adds a code of its own, and no code changes meaning
between modules. What differs is which conditions reach which code.

## Exit 2: malformed command

Exit 2 does not distinguish between the ways a command line can be wrong. It
covers an unknown command, an unknown flag, a missing required flag, a missing
or extra argument, an unsupported `--output` value, an empty value for a flag
that needs one, a flag value outside the bounds the flag documents, an update
command given no flag to update with, and `--client-id` supplied without
`--client-secret`. Read stderr to find out which one fired.

Neither of the last two looks like a typo. A value outside its bounds is
spelled correctly and is still refused locally, as `--db-pool 0`, `--max-rows
10001` and a `--jwt-secret` under 32 characters all are. An update command that
names nothing to change is refused for the same reason: the caller described no
change, and nothing is sent to find that out. That covers `tenant update` with
no `--name`, `client update` with neither of its two, and `cluster update` with
none of its three.

An input document counts as part of the command. A spec file passed
with `-f`, a `--pipeline-config` payload, and a certificate named by
`--ca-cert`, `--client-cert` or `--client-key` all exit 2 when the
file is absent, unreadable or unparseable, and no write is sent. The
timing differs: the `controlplane` file flags fail before any request
leaves, while `--pipeline-config` fails after the database read that
precedes it.

The config file is the exception to that rule. `--config` and the
default `~/.pgedge/cli/config.yaml` are resolved on every
invocation, including commands that never mention them, so a config
file that is missing, unreadable or unparseable is exit 1. An empty
`--config` value is still exit 2, because that is the flag token
rather than the file. The
[configuration guide](configuration.md) covers both checks.

A stray argument to a group command is exit 2 rather than a help dump, and that
holds with `--help` appended, which makes appending `--help` a reliable test of
whether a command exists. This command exits 2 and prints no help, because
`bogus` is not a cluster command:

    pgedge starfleet byoc cluster bogus --help

A real leaf behaves the other way round.
`pgedge starfleet byoc cluster get --help` prints help and exits 0
with no ID supplied, because a leaf's arguments are checked only when
it runs, and asking for help is how an operator finds out that `get`
needs an ID in the first place. Without `--help` the same line is
exit 2.

## Exit 1: general runtime failure

Exit 1 covers a server that could not be reached, a router-level 404,
an unknown profile name, and every other non-2xx the CLI does not
classify into a sharper code. A 400, 409, 422 or 500 the CLI has no
specific reading for all arrive here, so exit 1 alone does not say
whether retrying is sensible. Read stderr, which quotes what the
server said.

Two conflicts are worth retrying, and neither says so from the code
alone:

- a 409 on a managed mutating write means the resource is busy with
  another operation and this one needs it idle. Wait for it to settle
  and retry. Run `database get` for the resource's current status and
  `task list` for what is running. The exception is a 409 caused by
  the database's branches: a `branch create` at the branch limit, or a
  `database delete` or `database resize` while branches exist. Those
  never settle, and the CLI says so instead: delete a branch first,
  or pass `--delete-branches` to `database delete`.
- a Control Plane 409 saying the cluster is not initialized means exactly
  that. Run `pgedge controlplane cluster init` to create a cluster, or
  `pgedge controlplane cluster join` to join one, then retry. Every command
  that reads or writes cluster state answers this way until a cluster
  exists.

## Exit 3: timed out

Exit 3 is the per-request bound set by `--timeout`, a `--wait`
running out of `--wait-timeout`, the server answering 408 or 504, or
a Postgres database that accepts an `inspect` connection and does not
answer within 30 seconds. A server that could not be reached at all
is exit 1, not 3, and only
that case prints the reachability and mTLS hint. On Control Plane in
particular, a message naming `--timeout` is exit 3 and a message
about reaching the server is exit 1.

Unlike the two conflicts above, exit 3 does not mean the command is
safe to repeat. A read is. A create or a delete may already have
reached the server before the bound fired, so check the resource's
state before retrying a write.

## Exit 4: not found

Exit 4 means the server's handler said the resource does not exist.
Resource identifiers are full UUIDs apart from a handful of named
exceptions, and a name from a `list` command's NAME column is not
one, so verify the identifier with the matching `list` before
reading exit 4 as a missing resource.

The code does not separate "no such database" from "no such service
on that database" in the `managed` module. Both are not-found and
only the message tells them apart. A malformed identifier is a
different case again: it is refused at exit 2 before any request
leaves.

If a whole endpoint seems to be missing rather than one resource,
that is a router-level 404, which the CLI reports as exit 1 with a
message saying the server does not serve an endpoint this command
needs.

## Exit 5: authentication failed

Exit 5 is the server or the credential store saying no: no
credentials resolve at all, a rejected client ID or secret, a 401 or
403 from the API, or a token exchange that hangs. Plan entitlement
lands here too. When the active tenant's plan does not cover a
resource, the API answers 400 with a body containing `plan does not
allow ...`, and the CLI classifies that with the credential failures
rather than as a malformed request. An auth-retry loop should not
spin on it, because no credential will fix it.

On Control Plane, exit 5 is any 401 or 403 that reaches the CLI's
error handling. The only path the API declares is `cluster join`
refusing a `--token`, but a proxy sitting in front of a Control Plane
can answer either status on any request. Four paths handle a refusal
themselves and never exit 5:

- A profile listing more than one base URL probes each one's
  `/v1/version` in turn. A 401 or 403 there reads as one more
  candidate that did not answer, and when none of them answers the
  command ends at exit 1 naming every URL it tried.
- `controlplane doctor` exits 0 whatever it finds. The reachability
  row carries the refusal. The cluster row is omitted entirely,
  because a refused read cannot tell "no cluster" from "could not
  tell".
- `controlplane database init` detects the orchestrator with a read
  it is willing to lose, so a refusal leaves it writing the generic
  template at exit 0, with a note saying detection was inconclusive.
- `controlplane database create` and `update` read the host list to
  decide whether to warn about missing systemd ports, and drop any
  error from that read so a diagnostic can never fail a write. The
  write itself still answers 5, so the loss is the warning. Under
  `--dry-run` there is no write to answer, and the command exits 0
  with the warning silently absent.

## Empty byoc lists and plan gating

Not every plan-gated byoc capability answers with a 400. `cluster
list` and `ssh-key list` are not plan-gated at all, so they return
200 and an empty array whether the tenant simply owns no clusters or
is excluded from byoc entirely. The two cases are identical in the
response and in the exit code.

A profile carries one credential, so a profile is a tenant, and the tenant's
plan is what gates these byoc capabilities. It gates little else: managed
routes carry no plan gating, `account` commands work on any plan, and
`controlplane` has no login at all, so it is not a tenant's to gate. Run
`pgedge starfleet doctor` to see the active tenant before reading an empty list
as "nothing here yet". The [authentication guide](auth-and-profiles.md) covers
profiles.

## Commands that report instead of failing

Some commands exist to be run when something is already broken, so
failing would defeat their purpose. The deliberate departures from
the contract above:

| Command or condition | Code | Why |
|---|---|---|
| `pgedge starfleet doctor` | 0 | It diagnoses authentication, so it must run when authentication is broken. |
| `pgedge controlplane doctor` | 0 | Same reason, including when the server is unreachable or has no cluster. |
| `pgedge controlplane config view` | 0 | It reports an unresolvable profile field as a problem row rather than refusing to print. |
| An unknown profile name | 1 | A name that is well formed but not configured is not a malformed argument. |
| A hung token exchange | 5 | Authentication is the call that failed, even when a deadline is what failed it. |
| A plan-entitlement 400 | 5 | An entitlement refusal is not a malformed request. |
| A router-level 404 | 1 | The endpoint is absent, which is a different problem from a missing resource. |

Read the doctors' fields under `-o json`, never their exit codes.
The [troubleshooting guide](troubleshooting.md) explains which
fields to trust.

`pgedge starfleet auth status` exits 0 whenever credentials resolve,
even with no cached token, 5 when none resolve, and 2 when you supply
only one half of the `--client-id` and `--client-secret` pair.
`pgedge self update` exits 5 when the `gh` CLI is installed but its
session is not authenticated, 1 when `gh` is missing or fails any
other way, and 3 when one of its network deadlines expires.

## Next steps

- The [troubleshooting guide](troubleshooting.md) is organized by
  exit code and covers what to do after each one.
- The [CI and automation guide](ci.md) covers scripting against
  these codes.
- The [dry run guide](dry-run.md) explains why a clean
  `--dry-run` is not a gate on the real run's exit code.
