# Manage BYOC databases

A BYOC database is Postgres running on a cluster in your own cloud
account. The cluster owns the nodes and the network. The database is
what an application connects to, and what the MCP, RAG and PostgREST
services attach to later.

You need a tenant whose plan covers BYOC, and the full UUID of a
cluster that already reports `available`. The
[Provision a BYOC cluster and database](provision.md) workflow covers
getting that far. The
[Register a BYOC cloud account](cloud-accounts.md) document covers the
account a cluster is built in. The
[Manage BYOC clusters](clusters.md) document covers the cluster
itself.

Every database argument and every `--cluster-id` takes a full UUID. A
name, a prefix or a truncated identifier is exit 2 with nothing sent,
so resolve a name through `database list` first. The one identifier on
this page that is not a UUID is the argument to `config-version get`,
which is a version name such as `15.6.0`.

## The commands

The following table describes the database commands:

| Command | What it does |
|---|---|
| `database list` | Lists databases as ID, NAME, STATUS, CLUSTER and CREATED. Filter with `--cluster-id`. |
| `database get <db>` | Shows one database, adding a PG VERSION column the list does not have. |
| `database create` | Provisions a database on a cluster. Asynchronous. |
| `database update <db>` | Changes the display name or the option list. Synchronous. |
| `database delete <db>` | Tears the database down. Destructive and asynchronous. |
| `database rotate-password <db>` | Issues a new password for one built-in role. Destructive and asynchronous. |
| `database restore <db>` | Restores from a backup repository. Destructive and asynchronous. |
| `database logs <db>` | Reads a component's log lines from named nodes. |
| `database metrics <db>` | Reads the Postgres and container metrics series. |
| `database service`, `mcp`, `rag`, `postgrest` | Manage the services deployed alongside the database. |

The [Deploy BYOC services](services.md) workflow covers the last row.
The [Logs and metrics on BYOC](logs-and-metrics.md) workflow covers
the two read commands above it. The
[Back up and restore a BYOC database](backup-restore.md) workflow
covers `database restore` and the repositories it reads from.

## Name rules

Both name flags are checked locally before anything is sent. Neither
follows the same rule as the cluster the database sits on.

The following table describes the two name flags:

| Flag | Command | Rules | Changeable later |
|---|---|---|---|
| `--name` | `create` | At most 63 bytes counted after lowercasing. Starts with a letter or an underscore. Letters, digits and underscores only. | No |
| `--display-name` | `update` | Any text, at most 25 characters. | Yes |

Underscores are legal in a database name, and hyphens are not. That is
the reverse of cluster, node and backup-store names in this same
module. The refusal names the hyphen.

"Letters" means Unicode letters, not ASCII, so an accented name is
legal. The 63 is a count of bytes. The API lowercases the name before
it stores and measures it, which is why the length message quotes the
lowercased size. The API also trims surrounding whitespace,
so `--name "  MyDB  "` is accepted and creates `mydb`.

Managed databases follow a different rule again: 50 characters, no
underscores, because a managed name is also a Kubernetes object name.
A name legal in one module can be refused by the other. The
[Provision a managed database](../managed/provision.md) workflow
carries that rule.

`create` has no `--display-name` flag at all. Set the display name
afterward with `database update`.

## Create the database

The Postgres version is fixed for the life of the database, so choose
it here or accept the API's default.

1. Read the version catalog:

        pgedge starfleet byoc config-version list

    The PG VERSIONS column lists the Postgres majors each
    configuration version supports. A configuration version also pins
    the container images a cluster runs, which `config-version get
    <version> -o yaml` shows.

2. Create the database, waiting for the work instead of the
   acknowledgment:

        pgedge starfleet byoc database create \
            --name mydb \
            --cluster-id <cluster-id> \
            --pg-version 16 \
            --wait

    In text output the confirmation sentence goes to `stderr` and
    carries the new identifier. Under `-o json` or `-o yaml` the
    database object goes to stdout instead, which is where a script
    reads `.id` from.

3. Confirm the result, whether you waited or not:

        pgedge starfleet byoc database get <db-id> -o json

    Expect `"status": "available"`. A task reaching `succeeded`
    reports that the platform's own work finished, not that the
    database is ready to connect to. Branch on the status, not the
    task.

`--pg-version` is validated only for emptiness, so `--pg-version 99` is
the API's to refuse, while `--pg-version ""` is exit 2 with nothing
sent. Omit the flag to take the API's default deliberately.

`--pg-version` takes a bare Postgres major, such as `16`, not the
dotted configuration-version name `config-version get` takes, such as
`15.6.0`. Nothing local catches a value from one flag typed into the
other, because `--pg-version`'s only local check is emptiness.

Every `database` write takes `--dry-run`, which runs the client-side
checks and stops. The four `database` reads, `list`, `get`, `logs` and
`metrics`, do not accept `--dry-run`, and neither do the
`config-version` commands. On `create` the dry-run report records two
checks passing: the name rule and the cluster identifier's shape.
Neither check asks the API whether the cluster exists. An empty
`--pg-version` is refused before either of them and so appears in no
report at all. The [Dry runs](../dry-run.md) guide covers what a dry
run does and does not prove.

## Naming nodes

Several commands take node names instead of identifiers, because a
node is named, not identified. Read a cluster's node names with:

    pgedge starfleet byoc node list <cluster-id>

The command lists each node's ID, name, region, instance type and IP
address. The NAME column is what `--target-nodes`, `--node-name` and
`--nodes` expect.

The following table describes the flags that take them:

| Flag | Commands |
|---|---|
| `--target-nodes` | `restore`, and every service deploy and update. |
| `--node-name` | `restore`, where it is required, and `database metrics`. |
| `--nodes` | `database logs`, where it is required. |

`--target-nodes` is a list flag wherever it appears. `--target-nodes`
accepts a comma-separated value or repetition, and the two forms are
equivalent, so `--target-nodes n1,n2` and
`--target-nodes n1 --target-nodes n2` send the same pair. The CLI
trims the whitespace the comma parser leaves behind, so
`--target-nodes "n1, n2"` names `n1` and `n2`, not a node called
space-n2. An empty element survives that trim: `--target-nodes n1,,n2`
asks for three nodes, one of them named by the empty string.

What happens to those names differs by command:

- on a service deploy, the CLI resolves each name against the
  cluster's nodes. An unknown name, `--target-nodes n1,,n2`'s empty
  element included, fails at exit status 1 and lists the real names.
  That differs from an empty flag value: `--target-nodes ""` parses
  to no elements at all, which on a single-node cluster silently
  selects that one node.
- on `restore`, the names go straight to the API unresolved, so
  nothing local catches a typo. Omitting the flag there restores onto
  every node instead of auto-selecting one.

`--nodes` on `database logs` is a plain string the CLI passes through
unresolved, the same way `restore` does, and `--component-name` is
required alongside it.

`--repository` on `restore` is repeatable but not comma-separated, so
a comma-joined value there is one malformed identifier, not two.

## Statuses and waiting

A database reports its lifecycle in `status`: ready at `available`,
working at `queued`, `creating`, `modifying` or `deleting`, and
finished badly at `failed` or `degraded`. A `degraded` database does
not recover on its own, so stop checking its status and report it.
The [Output formats and paging](../output-and-paging.md) guide owns
that vocabulary and the separate `state` vocabulary a service reports.

`create`, `delete`, `restore` and `rotate-password` are asynchronous.
The API accepts the request and spawns a background task, and the
command returns exit 0 as soon as the acknowledgment arrives.
`--wait` blocks until that task reaches a terminal state, printing a
status line each time it checks. `--follow` blocks the same way while
streaming the task's step messages. Both report 0 for a succeeded
task, 1 for a failed one and 3 for the timeout, which defaults to 600
seconds.

No byoc response has a task identifier, so a wait finds its task
by subject. On a database that already exists, the CLI reads its
newest task before the write. The CLI then tracks the first task that
differs. A `create` needs no such baseline. That is because a database
that did not exist a moment ago has no earlier task to mistake for
this one. Status checks run every 5 seconds unless `--wait-interval`
says otherwise.

Without a wait flag, text output prints the
`task list --subject-id <db-id>` call to monitor with. `-o json` and
`-o yaml` print nothing there, so a script builds that call itself.
The [Tasks and async operations](../tasks-and-async.md) guide covers
the wait flags and task inspection in full.

`database update` is the exception. The command changes a record, not
infrastructure, so it takes no wait flag and prints no monitoring
line. The status the command echoes is whatever the API returned.

## Update the record

`update` changes only the flags you pass:

    pgedge starfleet byoc database update <db-id> \
        --display-name "My production database"

`--options` takes a comma-separated list. The CLI sends exactly the
list you pass, and does not merge it with the stored one. Build the
whole list, not the difference. A `--display-name` over 25 characters
is exit 2 before the client resolves any credentials, so the refusal
costs no round trip.

## Read the credentials back

The connection details come back with the database object, and only
under a machine format:

    pgedge starfleet byoc database get <db-id> -o json

Each entry of the `nodes` array has a `connection` object. A
`roles` array on the database describes the built-in roles it ships.
The database-level `connection` block is optional, so read a node's.
Only `username`, `password`, `port` and `database` are guaranteed on
the connection object. The connection object also has `host` or
`internal_host`, and `external_ip_address` and `internal_ip_address`,
when the API has them. Read `connection.username` instead of assuming
which role you were handed.

There is no `--user-type` flag here. That flag belongs to managed,
where `database get` selects one role's credentials. The byoc `get`
returns the connection block it has. No table view prints a password
in any format, so a credential read needs `-o json` or `-o yaml`.

For a connection string instead of the parts, `database
connection-string <db-id> --node <name>` assembles one node's block
into a libpq URI, or `PG*` assignments under `--format env`. The
[Connecting an Application to a pgEdge Starfleet BYOC Database](connect-an-application.md)
guide covers choosing the node and the network.

`rotate-password` does not print the new password. Rotate the
password, then read it back:

    pgedge starfleet byoc database rotate-password <db-id> \
        --role app --force --wait

`--role` is required, and takes admin, app or app_read_only. `--role`
also accepts application and application_read_only as long-form
aliases, so a value copied from managed still works. Both the database
and its cluster must be `available`, and the database is checked
first.

A rotation refused while the database is busy exits 1 with a message
saying to wait and retry. That advice is right for a `modifying`
database, but wrong for a `failed` one: a `failed` database returns
the same message and never becomes available, because the API's body
does not name the status. Run `database get`
before retrying. A `deleting` database answers 404 instead. A refusal
naming the cluster, not the database, is a different condition with a
different remedy. The refusal comes back as a plain bad request. The
[Rotate a BYOC database password](rotate-credentials.md) guide covers
the rotation step by step.

## Built-in roles

The following table describes the three built-in roles a BYOC database
ships:

| Role | What it is |
|---|---|
| admin | A real Postgres superuser. It can create extensions and objects anywhere, including the public schema. |
| app | The role an application connects as day to day. |
| app_read_only | A read-only role, and the safe default for anything that serves untrusted callers. |

Loading data is easier here. An ordinary one-shot pg_restore works,
`--disable-triggers` included, because a real superuser may issue the
statements it emits. That follows from the superuser property, not
from an observed byoc restore. On managed the same command loses a
table without saying so. The
[Loading a Schema and Data into a pgEdge Starfleet Managed Database](../managed/load-data.md)
workflow covers why. Do not carry a load recipe from managed to BYOC
or back without reading both.

Exposing the database is riskier here. Never hand `admin` to
`postgrest deploy --db-anon-role`: that role serves unauthenticated
requests, and on BYOC it would serve them as a superuser. Use
app_read_only, or a role you create yourself with only the privileges
an anonymous caller should have.

## Delete the database

Deletion is destructive and asynchronous, so it prompts and it spawns
a task:

    pgedge starfleet byoc database delete <db-id> --force --wait

Deleting a database leaves its backups intact. `cluster delete`
refuses while the cluster still hosts databases. `--cascade` deletes
those databases and the cloud infrastructure along with the cluster.
Reach for that flag only when it is exactly what you mean.

## Confirmation prompts

Eleven byoc commands ask for confirmation before they act, and each
takes `--force` to skip the question.

The following table lists them:

| Command | Why it prompts |
|---|---|
| `backup-store delete` | Removes the store a cluster backs up to. |
| `cloud-account delete` | Removes the account clusters are built in. |
| `cluster delete` | Removes the cluster, and with `--cascade` its databases too. |
| `cluster share delete` | Withdraws a share. |
| `database delete` | Removes the database. |
| `database restore` | Overwrites the database from a backup. |
| `database rotate-password` | Breaks every session using the old password. |
| `database service remove` | Discards a service's configuration and credentials, irrecoverably. |
| `ingress delete` | Removes the regional load balancer. |
| `ingress service deregister` | Takes a service off an ingress. |
| `ssh-key delete` | Removes the key. |

Four of those are not spelled `delete`.

The test is on `stdin`. When `stdin` is not a terminal, these commands
fail with a usage error asking for `--force`. The commands do not hang
on a prompt or abort quietly. That condition covers every script, CI job
and agent. Redirecting the output changes nothing, because only
`stdin` is looked at.

## Paging on the lists

`database list` returns 10 rows when `--limit` is omitted, and the
server clamps a `--limit` above 100 without saying that it did. As a
result, `--limit 500` coming back with 100 rows is not evidence that
only 100 exist. When a page comes back full, the list prints a hint on
`stderr` opening `Showing first`. The API reports no total, so the
hint can only say more rows might exist. Page through with `--offset`.
The [Output formats and paging](../output-and-paging.md) guide carries
the per-command table.

`--limit 0`, a negative `--limit` and a negative `--offset` are usage
errors at exit 2 with nothing sent. An explicitly empty filter is a
usage error too. `database list --cluster-id ""` is refused, not
silently widened to every cluster, because `--cluster-id "$C"` with
the variable unset is how that happens.

The two reads print different columns. `get` prints a PG VERSION
column and `list` does not, because the list endpoint does not send
the field at all. `get` can still print the column blank, because a
`failed` database can return an empty version.

## Next steps

- The [Deploy BYOC services](services.md) workflow covers putting an
  MCP, RAG or PostgREST service on the database.
- The [Tasks and async operations](../tasks-and-async.md) guide covers
  waiting on a create or a delete, and reading the task when one
  fails.
- The [Exit codes](../exit-codes.md) guide explains why an empty byoc
  list proves nothing about your plan.
- The
  [pgedge starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
