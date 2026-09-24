# Manage Control Plane databases

A self-hosted Control Plane creates, reads, changes and removes a
distributed Postgres database from a spec you write. This page picks
up where [standing up a Control Plane](local-server.md) leaves off: a
server runs somewhere, `cluster init` has succeeded, and every host
you want nodes on has joined.

A Control Plane database is spec-file driven from end to end. You
generate a spec, edit it, and apply it. There are no per-field flags
for the database body, so the spec file is the interface.

## Connecting to a Control Plane

The controlplane module has no login and stores no credentials, so
every command needs a running server and a URL that points at it.
`--base-url` names that server and defaults to
`http://localhost:3000`. The flag is repeatable, and with several URLs
the CLI probes each in the order listed and uses the first live one.
`pgedge controlplane config set --base-url <url>` writes it into the
active profile so you stop repeating it.

Where a deployment authenticates the CLI at all, it does so with
mutual TLS through `--ca-cert`, `--client-cert` and `--client-key`.
Reserve `--insecure` for a development server. The join token that
`cluster init` prints is not a CLI credential, it is what a new host
presents to join the cluster, and a rejected one is the only auth
failure the API declares. Every connection requires TLS 1.3, and no
flag lowers that floor, so a handshake error naming a protocol version
means whatever terminates TLS for the Control Plane is downlevel and
needs upgrading.

This CLI supports Control Plane 0.10.0 and later. Against an older
server, `controlplane doctor` turns its Reachable row to `warning` and
names both versions, and `controlplane version` prints the same
warning once on `stderr`. A version string the CLI cannot parse, a
`dev` build for instance, counts as unknown, never as too old.

## Listing and inspecting databases

Two read commands cover the resource, and they print different
shapes. `database list` prints one row per database with ID, STATE,
CREATED and UPDATED columns. `database get` opens with a one-row
table carrying exactly those columns, then adds an Instances section
and a Services section when the database has services. The instance
ID in that first column is the argument every other instance command
and `node switchover --candidate` take, and it is server-generated,
so you read it rather than construct it.

Neither read pages, and neither takes a flag. `task list --limit` is
the only paging flag in the module, there is no `--offset` anywhere,
and controlplane prints no truncation hint on any read. byoc and
managed both print a line beginning `Showing first` to `stderr` when a
page comes back full. Here a full page may or may not be the whole
result, and on `task
list` the way to narrow one is `--database`, `--host` or `--scope`.
The [output and paging guide](../output-and-paging.md) has the
contract for every module.

An Instance errors or Service errors section follows its table
whenever something has a reason attached. Read those as runtime health
and nothing more. The Control Plane's instance monitor writes them
when it cannot reach Patroni or open a database connection, so a
stopped instance reports one and a database that never finished
provisioning usually does not. A failed create records no reason on
any instance. A spec that fails during planning creates no instance at
all, so `database get` shows a failed database with nothing underneath
it, while a failure after the instances exist leaves the database at
`failed` with its instance rows still present, so read the database's
own state rather than an instance's. The reason for that class of
failure lives on the task.

`database get -o yaml` emits the whole database, spec included, and
`database update -f` reads the `spec:` section out of that document.
The round trip needs no restructuring. YAML keys are the JSON keys
throughout, so a `jq` path and a `yq` path address the same field.

## Generating a spec

`database init` prints a starter spec to `stdout`, ready to redirect
and edit. `database_name`, `nodes` and `database_users` come out live,
the last carrying an admin user whose password is a `CHANGE-ME`
placeholder. Every other section is an annotated stub, commented out
but complete: `backup_config`, `restore_config`, `services`,
`postgresql_conf`, `pg_hba_conf` and the per-node overrides on the
`nodes` block. The file doubles as inline reference.

Generate a three-node template and redirect it:

    pgedge controlplane database init --nodes 3 > storefront.yaml

`--nodes` defaults to 3, and a value below 1 is refused as a usage
error at exit 2. `database init` sends no write and accepts no
`--dry-run`.

Before it prints anything, `init` tries to detect the target Control
Plane's orchestrator over the same resolved connection every other
controlplane command uses, and shapes the template to match. A whole
multi-URL walk shares one short deadline rather than paying it per
server, and a shorter `--timeout` wins over it.

On Docker Swarm `port` is optional, and omitting it leaves the
database unexposed, while `patroni_port` is ignored, so a swarm
template leaves both commented.
A systemd Control Plane requires both on every node, so a systemd
template writes both, uncommented, with distinct values: the port
sequence starts at 5432 and the Patroni sequence at 8008. Under `-i` a
port you answer for one node is kept and the allocation walks past it.
The values differ per node because two nodes sharing a host cannot
share a port.

When detection is inconclusive, `init` falls back to a generic
template with both port fields commented and marked required on
systemd. Nothing configured, an unreachable `--base-url`, no hosts,
mixed orchestrators and an unrecognized one all take that path.
Detection never fails the command, so `init` stays usable offline, and
every mode names what it found on `stderr` while `stdout` stays YAML.

`create` and `update` run the other half of that check, warning on
`stderr` when a spec leaves a port unset on a node that targets a
systemd host, then sending the spec anyway, because the rule is the
server's to enforce. Set the ports per node rather than once at the
top level: the Control Plane folds a top-level pair into every node
that does not override it, which collides the moment two nodes share
a host. The warning advises the top-level form, so follow the per-node
form here rather than the remedy the warning prints.

## The interview

`database init -i` interviews you for values instead of printing a
blank template. Prompts go to `stderr` and the spec goes to `stdout`,
so redirecting the output is still safe:

    pgedge controlplane database init -i > storefront.yaml

Interactive mode needs a terminal and exits 2 without one. `--nodes`
seeds the node-count question rather than fixing it, and `-o json`
emits the populated spec as JSON for piping straight into
`create -f -`. `-o json` is the one flag here that requires `-i`, and
exits 2 without it, because a non-interactive `init` emits YAML only.

The interview collects the scaffold first, meaning the database name,
node count, per-node name and host IDs, admin username and port, then
offers guided backups, services, `restore_config` and per-node
override steps. Nodes you do not customize keep the cluster defaults.

No credential is ever prompted for or written as a real value. Backup
repository credentials become a commented note, restore repository
credentials fall back to the instance credential chain, and the LLM
provider credential is written as a `CHANGE-ME` placeholder.
Configuring LLM settings for an mcp service sets `llm_enabled: true`,
because the Control Plane rejects the provider, model and credential
keys unless it is set, and asks the provider as an enum of
`anthropic`, `openai` and `ollama`. The credential key follows from
that answer, as `anthropic_api_key`, `openai_api_key` or `ollama_url`.

## Service config keys

A spec's `services` block carries one entry per service, and each
service type has its own `config` shape. The CLI mirrors the Control
Plane's key sets at the 0.10.0 support floor, so `database init`'s
example for each type names only keys the Control Plane knows. The
following table describes the three shapes:

| Service | Shape | Is `config: {}` valid? |
|---|---|---|
| mcp | 27 flat keys, all optional on their own | Yes |
| rag | `pipelines` and `defaults` over a closed nested tree | No, `pipelines` is required with at least one entry |
| postgrest | 8 flat keys | No, `db_anon_role` is required |

Three rules sit behind that table:

- mcp gates whole groups of keys behind a switch. With `llm_enabled`
  false or absent the LLM keys are rejected rather than merely
  optional, and the four `kb_*` keys behave the same way under
  `kb_enabled`.
- mcp's `init_token` and `init_users` are bootstrap credentials the
  Control Plane accepts on create and rejects on update. The interview
  offers `init_token`, and `init_users` goes in by hand.
- rag's tree is closed at every level, not only at the top. A key
  nested three deep inside a pipeline is rejected exactly as a bad
  top-level key is.

## Creating a database

`database create` takes a spec with `-f`, and returns as soon as the
Control Plane accepts the work. Create a database and block until the
task finishes:

    pgedge controlplane database create storefront -f storefront.yaml --wait

`-f -` reads the spec from `stdin` instead. Feed it by redirect rather
than by pipe: a pipeline reports only the last command's status, so a
failed generator reaches `create` as an empty spec and takes the blame.

Four client-side checks run before any request leaves the machine, and
each one is a usage error at exit 2 naming the offending field:

- A spec still holding the `CHANGE-ME` placeholder is refused. An
  unedited template would otherwise create a real database whose
  password is a string printed in the template.
- A `postgres_version`, cluster-wide or per-node, must be in
  major.minor form such as `16.14`. A bare `16` is refused.
- Spec files decode strictly, so an unrecognized key is refused by
  name.
- A file that parses but carries no `database_name` or no `nodes` is
  refused as not a database spec.

The placeholder scan refuses a `CHANGE-ME` left anywhere the
template writes one, naming the field:

- each node's `host_ids`.
- each database user's password.
- a backup repository's credentials under
  `backup_config.repositories[]`, such as `s3_key` and `gcs_key`.
- `restore_config`'s `source_*` fields and repository credentials.
- a service's own `host_ids`, which sit beside `config`, not inside.
- a service `config` value at any depth, lists included. A rag
  pipeline's `embedding_llm.api_key` is the deepest case.

`--tenant-id` overrides the owning tenant. `--dry-run` runs every
client-side check, reports the request it would have sent, and stops
without sending it, which the [dry run guide](../dry-run.md) covers in
full. It stops writes by path rather than by HTTP method, which
matters here because the Control Plane declares two state-changing
operations as GET: initializing a cluster, and canceling a database
task. Anyone driving the API directly needs the same rule, because a
method is not proof that a call is read-only.

Under `-o json` or `-o yaml` the command writes the API's own accepted
response to `stdout` in every wait mode, with the task under `task`.
Progress and the terminal verdict stay on `stderr`, and text mode
writes nothing to `stdout` at all. Redirect that object rather than
piping it, for the reason above:

    if ! pgedge controlplane database create storefront \
        -f storefront.yaml -o json > create.json
    then
        echo "create was not accepted" >&2
        exit 1
    fi

## Updating and upgrading

Two commands change a database that already exists. `database update`
applies a whole spec, so the pattern is to round-trip the live one,
edit it, and apply it:

    pgedge controlplane database get storefront -o yaml > spec.yaml
    pgedge controlplane database update storefront -f spec.yaml --wait

`update` runs the same `CHANGE-ME`, `postgres_version` and strict
decoding checks that `create` does, and reads the `spec:` section of a
`database get` document without any restructuring.

`database upgrade` moves the database to a new container image, which
is how a minor-version upgrade lands. The target must be a newer build
in the same Postgres and Spock major bucket, and it restarts the
database, so it prompts:

    pgedge controlplane database upgrade storefront \
        --image pgedge/pgedge:16.4-1 --force --wait

`--image` is required and is checked at run time, so omitting it is a
usage error rather than a cobra complaint.

## Deleting and the force flags

`database delete` removes a database and all its instances. The
operation is destructive and irreversible, so it prompts unless
`--force`:

    pgedge controlplane database delete storefront --force --wait

Three flags in this module start with `force` and do different things.
`--force` never reaches the wire, while the other two set a parameter
on the request telling the server to waive a check. The following
table describes what each one does:

| Flag | Where it appears | What it does |
|---|---|---|
| `--force` | Every command that prompts, including `database delete`, `database upgrade`, `database restore` and `task cancel` | Skips the confirmation prompt, and nothing else |
| `--force-unmodifiable` | `database delete`, `database restore`, `database node backup`, `database instance start`, `database instance stop` | Tells the server to waive its unmodifiable-state check |
| `--force-lost` | `host remove` only | Tells the server to waive its instance and quorum checks, for a host that is permanently gone |

A command that never prompts carries no `--force` at all, and
passing it there is an unknown flag at exit 2. A scripted `--force`
skips a prompt and waives nothing.

## When a create fails

Reach for the task, not the database. `database get` on a failed
create shows a failed database with no reason anywhere in it, in every
output format, while the task carries both the reason and the trace.
List the database's tasks, newest first, then read the one you want:

    pgedge controlplane task list --database storefront
    pgedge controlplane task get --database storefront <task-id>

`task get` prints the reason under an Error heading, and `task logs`
takes the same arguments and prints the step-by-step trace. Both
require a scope, `--database` or `--host`, naming the value in the
ENTITY column of `task list`. Omitting both is a usage error at exit 2
before any request.

`task cancel` stops a running database task. Host tasks cannot be
canceled, `--database` is required, and the command prompts unless
`--force`. Its response is the one place in the module without a
wrapping envelope, so the task's own keys sit at the top level of the
document and a `jq` path here reads `.task_id` where every other
controlplane command reads `.task.task_id`.

`--wait` polls every 3 seconds and gives up after 600 seconds, both
adjustable with `--wait-interval` and `--wait-timeout`. `--follow`
ignores both of those flags: it streams the task log on a fixed
2-second poll and has no overall bound, so a task that never reaches a
terminal state streams until you stop it. Each individual log poll is
still bounded at 30 seconds, independently of `--timeout`, and a poll
that outlives it exits 3. The
[tasks and async operations guide](../tasks-and-async.md) carries the
full contract.

## Exit codes

The [exit codes guide](../exit-codes.md) owns the contract. Three of
those codes behave in ways specific to controlplane:

- A server that cannot be reached at all is exit 1, and a request
  that outlives `--timeout` is exit 3. The reachability and mTLS hint
  appears only for a connection that was never made.
- A 401 or a 403 is exit 5. The only one the API declares is a
  rejected `cluster join` token, so exit 5 on a database command
  points at a proxy or gateway rather than at the Control Plane.
- A 404 carrying the router's plain text rather than a JSON error is
  exit 1 and means the server does not serve that endpoint, usually
  because it predates the command. A JSON not-found is exit 4.

## Schema migrations

A self-hosted Control Plane database is created with three nodes by
default and is replicated by Spock as well, so the
[DDL replication section of the BYOC schema migrations guide](../byoc/schema-migrations.md#ddl-replication)
applies to one just as it does to a multi-node BYOC database. The CLI
prints no connection string for a Control Plane database.

## Next steps

- The [Control Plane standup guide](local-server.md) covers
  getting a server running and forming the cluster, including the
  failed `cluster init` that leaves a server unable to restart.
- The [tasks and async operations guide](../tasks-and-async.md) covers
  waiting, following and inspecting the tasks every write here spawns.
- The [health checks guide](../doctor.md) covers `controlplane doctor`
  and `controlplane config view` when a command cannot connect.
- The [controlplane command reference](../reference/controlplane.md)
  lists every command in the module and its flags.
