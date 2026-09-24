# Troubleshooting

Failures here are keyed to the exit-code contract: one number names
one class of failure, and stderr says which member of the class you
hit.
Start with the code, then the matching section below. The
[exit codes guide](exit-codes.md) carries the contract itself,
including the commands that deliberately depart from it. The
[error catalog](error-catalog.md) is keyed on the text the CLI
prints.

## Exit 1: general runtime failure

The catch-all for a server or environment problem that is not one of
the sharper classes below. Three common cases:

- An unknown profile. `--profile <name>` and `pgedge profile use
  <name>` both fail at exit 1 when the name is not configured, and
  the error lists the profiles that are. `pgedge starfleet auth login
  --profile <name>` is how a new profile comes to exist.
- A router-level 404. A Starfleet 404 whose body is not a pgEdge error
  means the server does not serve the endpoint at all: the base URL
  is not a Starfleet API root, the server is older than the API
  this CLI was built against, or a proxy answered instead. The CLI
  reports it as "this server does not serve an endpoint this command
  needs" rather than as a missing resource. The controlplane module draws the
  same line against its own router.
- A broken config file. The config is resolved on every invocation,
  so a `--config` path that is missing, unreadable or unparseable is
  exit 1 even on commands that never mention it.

## Exit 2: malformed command

Every way of getting the command line wrong: an unknown command or
flag, a missing required flag, a missing or extra argument, an
unsupported `--output` value, an empty value for a flag that needs
one, or `--client-id` without `--client-secret`. Read stderr for
which. An input document counts as part of the command, so a spec
file or pipeline config that cannot be read or parsed is also exit
2, and no write is sent.

A near miss offers the correction at any depth: `pgedge starfleet tenat`
names `tenant`.

## Exit 3: timed out

One code for the timeouts, with stderr naming the source:

- the per-request bound (`--timeout`, default 30s, 0 disables).
- a `--wait` running out (`--wait-timeout`), which in byoc and
  managed also bounds `--follow`.
- a controlplane `--follow` poll hitting its own fixed 30-second bound. The
  follow as a whole has no bound and ignores `--wait-timeout`, but
  each of its log polls stops at 30 seconds regardless of
  `--timeout`.
- the server answering 408 or 504.

The one timeout that is not exit 3 is a hung token exchange, which
is exit 5: authentication is the call that failed even when a
deadline is what failed it.

Exit 3 does not mean the command is safe to repeat. A read is. A
create or delete may already have reached the server before the
bound fired, so check the resource's state before retrying a write.

A `--wait` that ran out is the same warning in a stronger form: the work is
probably still running. The [tasks and async operations
guide](tasks-and-async.md) covers how to find that task and read its outcome,
and names the one command whose timeout means the result is unknown rather than
failed.

## Exit 4: not found

The server's handler said the resource does not exist. Verify the ID
with the corresponding `list` command. IDs are full UUIDs, and a
name is not an ID: commands take the UUID.

If a whole endpoint seems missing rather than one resource, that is
the router-level 404 under exit 1 above.

## Exit 5: authentication failed

The server or the credential store saying no:

- no credentials resolve at all.
- a rejected client ID or secret.
- a 401 or 403 from the API.
- a plan-entitlement rejection, where the API answers 400 with
  `plan does not allow ...` because the tenant's plan does not cover
  the resource. The CLI classifies it with the credential failures
  because it is an entitlement problem, not a malformed request.
- a token exchange that hangs. Authentication is the call that
  failed, even when a deadline is what failed it.

Run `pgedge starfleet auth login` to re-enter credentials, or check
which profile is active with `pgedge profile list`. A profile is a
tenant, so the right credential for the wrong profile is still the
wrong tenant.

[Troubleshooting BYOC](byoc/troubleshooting.md) covers the
entitlement failures that answer with an empty list rather than an
error.

## A failed asynchronous operation

A write that was accepted and then failed reports nothing at the exit
code, because the command exited when the request was accepted. The
task it spawned is where the reason lives. One symptom is common to
every module:

- A `--wait` that exited 3. The bound expired, not the operation.
  Find the task and read its state before retrying anything.

The [tasks and async operations guide](tasks-and-async.md) covers the task
commands each module offers. The [managed](managed/troubleshooting.md) and
[Control Plane](controlplane/troubleshooting.md) troubleshooting pages carry
the failed operations specific to each.

## An empty log or metrics read

A read that finds nothing exits 0, because an empty window is an
answer rather than a fault. What it prints depends on the command.
The [managed](managed/troubleshooting.md) and
[BYOC](byoc/troubleshooting.md) troubleshooting pages carry the cases
that account for most of them.

## Diagnosing with the doctors

Three commands exist to be run when something is broken, so they
report rather than fail, exiting 0 for the conditions they diagnose.
The exception they share with every command is a broken `--config`,
which fails at exit 1 before anything runs. Read their fields, not
their exit codes:

- `pgedge doctor` checks the installation: version, config file,
  shell integration, and one row per configured connection. It dials
  nothing, so it reports that a connection is configured, never that
  it works.
- `pgedge starfleet doctor` is the verdict on the Starfleet connection,
  byoc and managed included. Read `auth.authenticated` and
  `tenant.resolved` under `-o json`. A resolved tenant proves the
  server accepts the credential. When it is `false`, check
  `api.reachable` first, since an unreachable endpoint means a login
  will not help. Do not gate on `token_valid`: it
  describes the token cache, not the credential, and is `false` on
  any profile that has never cached a token.
- `pgedge controlplane doctor` reports the Control Plane connection, including
  when the server is unreachable or has no cluster. Read `reachable`
  under `-o json`. `reachable` alone is not readiness, so read
  `cluster_initialized` too.
  <!-- doc-gate: correct-for controlplane cluster_initialized — a key controlplane doctor
  builds into its own json map, not a field of any generated struct -->

The [health checks guide](doctor.md) documents every row each of the
three prints, what a warning means, and the fix.

## Request and response logging

`--verbose` logs one line per request and response to stderr, with
the `Authorization` header masked. `--debug` adds headers and bodies,
masks credential-bearing values, and bounds any one body at 8 KB.
Both leave stdout alone, so machine-readable output stays pipeable.
`--debug` exists so that nobody has to handle a raw credential to
see a payload, so reach for it before reaching for `curl`.

## Token cache behavior

The cached token at `~/.pgedge/cli/cache/<profile>-starfleet.json` is bound
to the credential and API base URL that minted it. Changing either
discards the cache and exchanges a fresh token, so a swapped key
inside a profile never runs as the previous tenant. If auth state
looks stale, `pgedge starfleet auth logout` removes the cache and the
stored credentials for the active profile, and a fresh `pgedge starfleet
auth login` rebuilds both.

## Getting support

[Versions, uninstall and support](support-versioning-and-uninstall.md)
says where to take a problem, with the CLI or with a pgEdge product,
and what to attach so it can be acted on.
