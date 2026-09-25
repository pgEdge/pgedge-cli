# Manage Control Plane databases

You can create, read, change and remove a distributed Postgres database
on a self-hosted Control Plane, from a spec you write. This page picks
up where [Stand up a local Control Plane](local-server.md) leaves off.
A server runs somewhere, `cluster init` has succeeded, and every host
you want nodes on has joined.

A Control Plane database is driven by its spec file from end to end.
You generate a spec, edit it, and apply it. Every field of the database
body lives in that file, so the spec file is the interface.

## Connecting to a Control Plane

Every controlplane command needs a running server and a URL that points
at it. The module has no login command and stores no credentials, so
you point each command at the server. `--base-url` names that server
and defaults to `http://localhost:3000`. The flag is repeatable. With
several URLs, the CLI probes each in the order listed and uses the
first live one. To stop repeating the URL, write it into the active
profile with `pgedge controlplane config set --base-url <url>`.

When a deployment authenticates the CLI, it uses mutual TLS through
`--ca-cert`, `--client-cert` and `--client-key`. Reserve `--insecure`
for a development server. The join token that `cluster init` prints
belongs to hosts: a new host presents it to join the cluster. A
rejected join token is the only auth failure the API declares.

Every connection requires TLS 1.3, and no flag lowers that floor. A
handshake error naming a protocol version means the component that
terminates TLS for the Control Plane is out of date. Upgrade that
component.

This CLI supports Control Plane 0.10.0 and later. Against an older
server, `controlplane doctor` turns its Reachable row to `warning` and
names both versions. `controlplane version` prints the same warning
once on `stderr`. The CLI reports a version string it cannot parse,
such as a `dev` build, as unknown. It keeps the too-old warning for a
version it can read.

## Listing and Inspecting Databases

Two read commands cover the resource, and they print different shapes.
`database list` prints one row per database with ID, STATE, CREATED and
UPDATED columns. `database get` opens with a one-row table carrying
exactly those columns. It then adds an Instances section, and a
Services section when the database has services. Every other instance
command and `node switchover --candidate` take the instance ID from the
first column. The server generates that ID, so read it from the output.

Both reads return the whole result in one response, and each takes
only the global flags. `task list --limit` is the module's only paging
flag, and `--offset` exists on no command. byoc and managed print a
line beginning `Showing first` to `stderr` when a page comes back full.
controlplane prints that line on no read, so a full page may or may not
be the whole result. To narrow `task list`, pass `--database`, `--host`
or `--scope`. [Output formats and paging](../output-and-paging.md) has
the contract for every module.

An Instance errors or Service errors section follows its table whenever
something has a reason attached. Read those sections as runtime health
only. The Control Plane's instance monitor writes them when it cannot
reach Patroni or open a database connection. A stopped instance
therefore reports one, and a database that never finished provisioning
usually has none.

After a failed create, the instance rows carry no reason. When a spec
fails during planning, the Control Plane creates no instance.
`database get` then shows a failed database with nothing listed under it. When
the failure comes after the instances exist, the database shows
`failed` and its instance rows remain. In both cases, read the
database's own state, and skip an instance's. The reason for that class
of failure lives on the task.

`database get -o yaml` emits the whole database, spec included.
`database update -f` reads the `spec:` section out of that document, so
the document applies as it is. YAML keys are the JSON keys throughout,
so a `jq` path and a `yq` path address the same field.

## Generating a Spec

`database init` prints a starter spec to `stdout`, ready to redirect
and edit. `database_name`, `nodes` and `database_users` come out live.
`database_users` carries an admin user whose password is a `CHANGE-ME`
placeholder. Every other section is an annotated stub, commented out
but complete: `backup_config`, `restore_config`, `services`,
`postgresql_conf`, `pg_hba_conf` and the per-node overrides on the
`nodes` block. The file doubles as inline reference.

Generate a three-node template and redirect it:

    pgedge controlplane database init --nodes 3 > storefront.yaml

`--nodes` defaults to 3. A value below 1 is refused as a usage error at
exit status 2. `database init` only prints a spec and sends no write,
so it has no `--dry-run` flag.

Before it prints anything, `init` tries to detect the target Control
Plane's orchestrator, and shapes the template to match. It uses the
same resolved connection as every other controlplane command. A whole
multi-URL walk shares one short deadline, and a shorter `--timeout`
wins over it.

On Docker Swarm, `port` is optional, and omitting it leaves the
database unexposed. `patroni_port` is ignored there, so a swarm
template leaves both commented.
A systemd Control Plane requires both on every node. A systemd template
therefore writes both, uncommented, with distinct values. The port
sequence starts at 5432 and the Patroni sequence at 8008. Under `-i`,
a port you answer for one node is kept, and the allocation walks past
it. The values differ per node because two nodes sharing a host cannot
share a port.

When detection is inconclusive, `init` falls back to a generic
template. Both port fields are commented and marked required on
systemd. An empty configuration, an unreachable `--base-url`, a cluster
with no hosts, mixed orchestrators and an unrecognized orchestrator all
take that path. `init` succeeds whatever detection finds, so it works
offline. Every mode names what it found on `stderr`, and `stdout` holds
only YAML.

`create` and `update` run the other half of that check. When a spec
leaves a port unset on a node that targets a systemd host, they warn on
`stderr` and send the spec anyway, because the rule is the server's to
enforce. Set the ports on each node. The Control Plane folds a
top-level pair into every node that leaves them unset, which collides
the moment two nodes share a host. The warning advises the top-level
form, so follow the per-node form here in place of the remedy the
warning prints.

## Running the Interview

`database init -i` interviews you for values and writes the spec from
your answers. Prompts go to `stderr` and the spec goes to `stdout`, so
redirecting the output is still safe:

    pgedge controlplane database init -i > storefront.yaml

Interactive mode needs a terminal, and exits with status 2 without one.
`--nodes` seeds the node-count question, and you can still change the
answer. `-o json` emits the populated spec as JSON, for piping straight
into `create -f -`. `-o json` is the one flag here that requires `-i`.
Without `-i` it exits with status 2, because a non-interactive `init`
emits YAML only.

The interview collects the scaffold first: the database name, node
count, per-node name and host IDs, admin username and port. It then
offers guided backups, services, `restore_config` and per-node override
steps. Nodes you leave uncustomized keep the cluster defaults.

The interview asks for no credential, and writes a stand-in wherever a
credential belongs:

- Backup repository credentials become a commented note.
- Restore repository credentials fall back to the instance credential
  chain.
- The LLM provider credential is written as a `CHANGE-ME` placeholder.

Configuring LLM settings for an mcp service sets `llm_enabled: true`.
The Control Plane rejects the provider, model and credential keys
unless it is set. The interview asks for the provider as an enum of
`anthropic`, `openai` and `ollama`. The credential key follows from
that answer, as `anthropic_api_key`, `openai_api_key` or `ollama_url`.

## Setting Service Config Keys

A spec's `services` block carries one entry per service, and each
service type has its own `config` shape. The CLI mirrors the Control
Plane's key sets at the 0.10.0 support floor. `database init`'s example
for each type therefore names only keys the Control Plane knows. The
following table describes the three shapes:

| Service | Shape | Is `config: {}` valid? |
|---|---|---|
| mcp | 27 flat keys, all optional on their own | Yes |
| rag | `pipelines` and `defaults` over a closed nested tree | No, `pipelines` is required with at least one entry |
| postgrest | 8 flat keys | No, `db_anon_role` is required |

Three rules sit behind that table:

- mcp gates whole groups of keys behind a switch. With `llm_enabled`
  false or absent, the Control Plane rejects the LLM keys. The four
  `kb_*` keys behave the same way under `kb_enabled`.
- mcp's `init_token` and `init_users` are bootstrap credentials. The
  Control Plane accepts them on create and rejects them on update. The
  interview offers `init_token`, and you add `init_users` by hand.
- rag's tree is closed at every level. A key nested three deep inside a
  pipeline is rejected exactly as a bad top-level key is.

## Creating a Database

`database create` takes a spec with `-f`, and returns as soon as the
Control Plane accepts the work. Create a database and block until the
task finishes:

    pgedge controlplane database create storefront -f storefront.yaml --wait

`-f -` reads the spec from `stdin` instead. Feed it by redirect. A
pipeline reports only the last command's status, so a failed generator
reaches `create` as an empty spec, and `create` takes the blame.

Four client-side checks run before any request leaves the machine. Each
one is a usage error at exit status 2 naming the offending field:

- A spec still holding the `CHANGE-ME` placeholder is refused. An
  unedited template would otherwise create a real database whose
  password is a string printed in the template.
- A `postgres_version`, cluster-wide or per-node, must be in
  major.minor form such as `16.14`. A bare `16` is refused.
- Spec files decode strictly, so an unrecognized key is refused by
  name.
- A file that parses but carries no `database_name` or no `nodes` is
  refused as not a database spec.

The placeholder scan refuses a `CHANGE-ME` left anywhere the template
writes one, and names the field:

- each node's `host_ids`.
- each database user's password.
- a backup repository's credentials under
  `backup_config.repositories[]`, such as `s3_key` and `gcs_key`.
- `restore_config`'s `source_*` fields and repository credentials.
- a service's own `host_ids`, which sit beside `config`.
- a service `config` value at any depth, lists included. A rag
  pipeline's `embedding_llm.api_key` is the deepest case.

`--tenant-id` overrides the owning tenant. `--dry-run` runs every
client-side check and reports the request it would have sent. It then
stops before sending it, as [Dry runs](../dry-run.md) covers in full.
`--dry-run` identifies a write by its path, because the Control Plane
declares two state-changing operations as GET. Those are initializing a
cluster and canceling a database task. If you drive the API directly,
apply the same rule, because a GET can still change state.

Under `-o json` or `-o yaml`, the command writes the API's own accepted
response to `stdout` in every wait mode, with the task under `task`.
Progress and the terminal verdict stay on `stderr`, and text mode
leaves `stdout` empty. Redirect that object to a file, for the reason
above:

    if ! pgedge controlplane database create storefront \
        -f storefront.yaml -o json > create.json
    then
        echo "create was not accepted" >&2
        exit 1
    fi

## Updating and Upgrading a Database

Two commands change a database that already exists. `database update`
applies a whole spec. Round-trip the live spec, edit it, and apply it:

    pgedge controlplane database get storefront -o yaml > spec.yaml
    pgedge controlplane database update storefront -f spec.yaml --wait

`update` runs the same `CHANGE-ME`, `postgres_version` and strict
decoding checks that `create` does. It reads the `spec:` section of a
`database get` document as it is.

A spec that leaves out a node the database has now removes that node
and its data. Before sending that spec, `update` names each node it
removes and asks you to confirm. Off a terminal, the command exits with
status 2 unless you pass `--force`.

`database upgrade` moves the database to a new container image, which
is how a minor-version upgrade lands. The target must be a newer build
in the same Postgres and Spock major bucket. The upgrade restarts the
database, so it prompts:

    pgedge controlplane database upgrade storefront \
        --image pgedge/pgedge:16.4-1 --wait

The command asks you to confirm, and you type `y` to proceed:

    Upgrade database storefront to pgedge/pgedge:16.4-1? This restarts the database. [y/N]: y

`--image` is required. The CLI checks it at run time, so omitting it is
a usage error rather than a cobra complaint.

## Deleting a Database and Using the Force Flags

`database delete` removes a database and all its instances. The
operation is destructive and irreversible, so it prompts unless
`--force`:

    pgedge controlplane database delete storefront --wait

The command asks you to confirm, and you type `y` to proceed:

    Delete database storefront? [y/N]: y

Three flags in this module start with `force` and do different things.
`--force` acts only inside the CLI. The other two set a parameter on
the request that tells the server to waive a check. The following table
describes what each one does:

| Flag | Where it appears | What it does |
|---|---|---|
| `--force` | Every command that prompts, including `database delete`, `database update`, `database upgrade`, `database restore` and `task cancel` | Skips the confirmation prompt, and nothing else |
| `--force-unmodifiable` | `database delete`, `database restore`, `database node backup`, `database instance start`, `database instance stop` | Tells the server to waive its unmodifiable-state check |
| `--force-lost` | `host remove` only | Tells the server to waive its instance and quorum checks, for a host that is permanently gone |

Only a command that prompts has `--force`. Elsewhere, passing it is an
unknown flag at exit status 2. A scripted `--force` only skips a
prompt, and the server still runs every check.

## Diagnosing a Failed Create

Read the task to find out why a create failed. `database get` on a
failed create shows the failed state with no reason, in every output
format. The task carries both the reason and the trace. List the
database's tasks, newest first, then read the one you want:

    pgedge controlplane task list --database storefront
    pgedge controlplane task get --database storefront <task-id>

`task get` prints the reason under an Error heading. `task logs` takes
the same arguments and prints the step-by-step trace. Both require a
scope, `--database` or `--host`, naming the value in the ENTITY column
of `task list`. Omitting both is a usage error at exit status 2, raised
before any request.

`task cancel` stops a running database task, and only a database task.
`--database` is required, and the command prompts unless `--force`. Its
response is the one place in the module without a wrapping envelope.
The task's own keys sit at the top level of the document, so a `jq`
path here reads `.task_id`. Every other controlplane command reads
`.task.task_id`.

`--wait` checks the task every 3 seconds, and gives up after 600
seconds. `--wait-interval` and `--wait-timeout` adjust both values.
`--follow` ignores both of those flags, and streams the task log every
2 seconds. It keeps streaming until the task reaches a terminal state
or you stop it. Each log request has a fixed limit of 30 seconds,
separate from `--timeout`, and a request that outlives it exits with
status 3. [Tasks and async operations](../tasks-and-async.md) carries
the full contract.

## Reading Exit Codes

[Exit codes](../exit-codes.md) owns the contract. Three of those codes
behave in ways specific to controlplane:

- A server that cannot be reached at all is exit status 1. A request
  that outlives `--timeout` is exit status 3. The reachability and mTLS
  hint appears only when the connection was never made.
- A 401 or a 403 is exit status 5. The only one the API declares is a
  rejected `cluster join` token. Exit status 5 on a database command
  therefore points at a proxy or gateway in front of the Control Plane.
- A 404 carrying the router's plain text, in place of a JSON error, is
  exit status 1. It means the server lacks that endpoint, usually
  because the server predates the command. A JSON not-found is exit
  status 4.

## Running Schema Migrations

A self-hosted Control Plane database has three nodes by default, and
Spock replicates it too. The DDL replication section of
[Schema migrations on a BYOC database](../byoc/schema-migrations.md#ddl-replication)
therefore applies to it, just as it does to a multi-node BYOC database.

## Next Steps

- [Stand up a local Control Plane](local-server.md) covers getting a
  server running and forming the cluster. It includes the failed
  `cluster init` that leaves a server unable to restart.
- [Tasks and async operations](../tasks-and-async.md) covers waiting,
  following and inspecting the tasks every write here spawns.
- [Health checks](../doctor.md) covers `controlplane doctor` and
  `controlplane config view` when a command cannot connect.
- [pgedge controlplane command reference](../reference/controlplane.md)
  lists every command in the module and its flags.
