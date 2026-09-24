---
name: pgedge-controlplane
description: "Use this skill when the user manages a self-hosted pgEdge
  Control Plane through the pgedge CLI: connecting to a Control Plane,
  initializing or joining a cluster, managing hosts, creating or
  updating distributed databases from a spec, backups and restore,
  switchover and failover, starting or stopping an instance, or
  inspecting tasks. Triggers on: pgEdge Control Plane, pgedge
  controlplane, controlplane cluster, controlplane database,
  controlplane host, controlplane task, self-hosted pgEdge, cluster
  init, cluster join, switchover, failover. For pgEdge Starfleet
  databases use the pgedge-byoc or pgedge-managed skill."
---

# pgEdge Control Plane Skill

The CLI embeds the reference for its own version, and that reference
is the source of every command, flag, output field and behavior this
skill relies on. This skill routes you to the right page and names the
rules an agent must not miss. The pages carry the detail.

## Read the reference first

Before you run a controlplane command, read the index:

```bash
pgedge llms controlplane
```

The index carries the connection flags, how to get a Control Plane
running, exit statuses, how `--wait` and `--follow` report, and the
supported server versions. Then read the one page your task needs,
from the table below. The reference states refusals, orderings and
edge cases that `--help` omits, so work from it rather than from
`--help` or memory.

## Set up

Point the CLI straight at a Control Plane you run with `--base-url`,
then check the connection:

```bash
pgedge version
pgedge controlplane doctor --base-url <url> -o json
```

`controlplane config set` saves the base URL, mTLS files and timeout to
a profile. The index's "Getting a Control Plane" section covers
standing a server up, which is a separate step.

## Find the page for your task

Each page prints with `pgedge llms controlplane <page>`:

| Task | Page |
|---|---|
| Check reachability, cluster state or the server version | `doctor`, `version` |
| Save the connection to a profile | `config` |
| Initialize a cluster, join a host, or read the cluster | `cluster` |
| List, inspect or remove a host | `host` |
| Write a spec, or create, read, update or delete a database | `database` |
| Start, stop or restart an instance | `database instance` |
| Back up a node, or run a switchover or failover | `database node` |
| Restore a database from a backup | `database restore` |
| Read, follow or cancel a task | `task` |
| Call a Control Plane API path that no verb covers | `api` |

A task that spans several rows needs each page. Standing up a cluster
reads `cluster`, then `host`. Creating a database reads `database`,
then `task` if something fails.

## Rules an agent must not miss

Each rule is stated in full on the page named beside it.

- **Read `doctor -o json`, not its exit status.** `doctor` exits 0 even
  when the server is unreachable. Check `reachable` and
  `cluster_initialized`, and run `cluster init` or `cluster join` before
  anything that reads cluster state. See `doctor`.
- **Pass `--wait` on every write in a script.** Every mutating verb is
  asynchronous. A task that fails exits 1, and a wait that runs out
  exits 3. See the index.
- **Redirect a write's `-o json` output to a file, then check the exit
  status.** Every mutating verb prints one accepted-response object.
  `task cancel` returns a bare task rather than a wrapped one. See the
  index and `task`.
- **`--force` only skips the confirmation prompt.** A server-side
  waiver is its own flag, `--force-unmodifiable`, or `--force-lost` on
  `host remove`. See `database`, `database instance`, `database node`
  and `host`.
- **Start every database from `database init`.** When it can reach a
  systemd Control Plane, it fills in the distinct ports each node
  needs. The CLI refuses a spec that still carries a `CHANGE-ME`
  placeholder or a bare Postgres major version. See `database`.
- **A restore clears the backup configuration.** Add it back with
  `database update` before the next `database node backup`. See
  `database restore`.
- **Read instance IDs from the server.** Take them from `database
  instance list` or `database get`. See `database instance`.
- **This CLI supports Control Plane 0.10.0 and later.** See the index.
