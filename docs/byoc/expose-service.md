# Expose a BYOC service

An ingress is a regional load balancer in front of a cluster. A
service registration is one route through that ingress. Together,
they make an MCP, RAG or PostgREST server on a private cluster
reachable from outside the cluster.

You need the database's full UUID and at least one service already
deployed on the database.

## Collect the two IDs first

A registration names a database and a service. The ID the platform
assigns identifies the service, not its type. Read the service ID
from the database's service list:

    pgedge starfleet byoc database service list <db-id> -o json

Read `service_id` from the output. A service ID is 8 characters of
hex, so it is one of the byoc tree's few non-UUID inputs. The CLI
does not check the service ID's shape before sending it.

An ingress belongs to a cluster, not to a database, so read the
database record for the cluster behind it:

    pgedge starfleet byoc database get <db-id> -o json

Read `cluster_id` from the output. The database record does not hold
a region of its own, and the ingress needs one, so read the cluster
for the regions it spans:

    pgedge starfleet byoc cluster get <cluster-id>

## Create the ingress

Check whether the cluster already has one before creating a second:

    pgedge starfleet byoc ingress list -o json

Read the exit status before reading the result. A failed read prints
nothing on stdout. In table output an empty account also prints
nothing there, so "no ingress for this cluster" reads the same as a
credential that no longer mints or an unreachable API. Under
`-o json` the two are further apart, because an empty account prints
`[]` and a failure still prints nothing. The exit status settles the
difference in either format. This branch provisions a regional load
balancer, so on any non-zero exit, stop and fix the failure instead
of creating one.

Creating an ingress requires all three of `--name`, `--cluster-id`
and `--region`, and the call provisions cloud infrastructure. Pass
`--wait` when you want the command to block until provisioning
finishes:

    pgedge starfleet byoc ingress create \
        --name prod-ingress \
        --cluster-id <cluster-id> \
        --region us-east-1 --wait

Without `--wait` or `--follow` the command returns as soon as the API
accepts the request. That return happens well before the load
balancer answers traffic. In table and text output, the CLI prints
the `task list --subject-id` command for the new task on stderr, so
you can pick up the work later. That hint is a table and text
courtesy, so `-o json` and `-o yaml` do not print it. `--wait` blocks
until the ingress's task reaches a terminal state and prints one
status line per check. `--follow` blocks the same way and streams
the task's step messages instead. Both take `--wait-timeout`, 600
seconds by default, and `--wait-interval`, 5 seconds by default. Both
exit 0 when the task succeeded, 1 when the task failed and 3 when
the wait ran out with the work still running.

`ingress list` and `ingress get` share seven columns: `ID`, `NAME`,
`STATUS`, `CLUSTER ID`, `REGION`, `DOMAIN` and `CREATED`. Reading
with `-o json` adds fields no column shows, among them
`cloud_account_id`, `domain_prefix` and `updated_at`.

`Ingress.status` is a bare string in the contract, so its values are
the platform's vocabulary, not a fixed set. A script should not gate
on one particular value. `DOMAIN` renders the ingress's full
wildcard domain and can be blank on a failed ingress. Read the URL a
registration reports as the address to dial, not an address read off
this column.

## Register the service

Registration takes the ingress as its argument and the database and
service as flags:

    pgedge starfleet byoc ingress service register <ingress-id> \
        --database-id <db-id> \
        --service-id <service-id>

Repeat the command once per service you want reachable. Registration
is synchronous. There is no `--wait` and no task, because the API
answers with the finished registration. In text output the
confirmation goes to stderr and names the service, the ingress and
the URL the service now answers on. Under `-o json` or `-o yaml` the
registration object goes to stdout instead, and the CLI prints no
confirmation line there.

Verify by listing what the ingress holds:

    pgedge starfleet byoc ingress service list <ingress-id>

The table has three columns, `SERVICE ID`, `DATABASE ID` and `URL`.
A service registration has no status or state field of any kind. The
object holds only `database_id`, `service_id`, `url` and `hosts`.
Presence in this list with a populated URL is the success signal,
and nothing needs checking for readiness.

Read the exit status before reading the absence, for the same reason
as on the ingress list above. On a non-zero exit, the registration is
unverified, not missing. Re-registering then would be a write made
off a read that never ran. On exit 0 with the service absent, check
the ingress ID first. Because a cluster can hold more than one
ingress, reading the wrong one answers with an empty array, not a
not-found error. If the ID is the one the register used, the absence
is real, so run the register again and verify again.

### When a register times out

Exit 3 means a deadline expired: either the per-request bound, which
`--timeout` sets and defaults to 30 seconds, or the server answering
408 or 504. This command is not task-backed. The command prints its
confirmation only after the API answers. As a result, a timeout
leaves the outcome unknown. The registration may have been made
anyway.

Treat a timeout as a read-then-decide, never as a blind retry:

    pgedge starfleet byoc ingress service list <ingress-id> -o json

If that read exits non-zero, stop and fix the connection first. If
the read exits 0 and the service ID is listed, the registration went
through. There is nothing to repeat. If the read exits 0 and the
service ID is absent, run the register again.

## Remove a registration or the ingress

Deregistering takes both the ingress and the service as positional
arguments. It stops the service answering at the ingress domain, and
prompts for confirmation unless you pass `--force`:

    pgedge starfleet byoc ingress service deregister \
        <ingress-id> <service-id> --force

The API returns an empty body. The acknowledgment is exit 0 and a
sentence on stderr. Stdout stays empty in text, JSON and YAML alike.
Gate a script on the exit status, not on the output.

Deleting the ingress tears down the load balancer and every route
through it. It prompts unless you pass `--force`, and it is
asynchronous, so it takes the same wait flags as create:

    pgedge starfleet byoc ingress delete <ingress-id> --force --wait

## Exit codes

The following table lists the exit codes these commands produce and
what to do about each:

| Exit code | Meaning | What to do |
|---|---|---|
| 0 | The call succeeded | Continue |
| 1 | The API refused the request, or the awaited task failed | Read stderr, then check the resource's state |
| 2 | The command line was wrong: an unknown flag, a missing required flag, or a malformed UUID | Fix the command line and rerun |
| 3 | A deadline expired: the request bound, a wait, or a 408 or 504 from the server | Check what actually happened before retrying |
| 4 | The ingress, database or service was not found | Confirm the IDs against the lists above |
| 5 | Authentication failed, or the tenant's plan does not cover the capability | Run `pgedge starfleet doctor` |

The [Exit codes](../exit-codes.md) guide covers the contract in full,
including why an empty byoc list proves nothing about entitlement.

## Next steps

- The [Tasks and async operations](../tasks-and-async.md) guide
  covers following an ingress create or delete after the fact.
- The [Output formats and paging](../output-and-paging.md) guide
  covers the paging flags on `ingress list` and the commands whose
  success prints nothing.
- The [Dry runs](../dry-run.md) guide covers what `--dry-run` checks
  before a write, and what it cannot check.
- The [Logs and metrics on BYOC](logs-and-metrics.md) guide covers
  reading a database's logs when a service behind the ingress is
  reachable but not answering.
- The [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
