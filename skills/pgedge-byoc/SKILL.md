---
name: pgedge-byoc
description: "Use this skill when the user wants to manage pgEdge BYOC
  infrastructure, pgEdge clusters and databases running in the user's
  own cloud account, through the pgedge CLI. Triggers on: pgEdge BYOC,
  pgedge starfleet byoc, creating a cluster, cloud accounts, backup
  stores, a database on a BYOC cluster, deploying MCP, RAG or PostgREST
  on a BYOC database, ingresses, exposing a service on a private
  cluster, SSH keys, node logs, or restoring a BYOC database. For
  databases pgEdge hosts use the pgedge-managed skill, for a
  self-hosted Control Plane use pgedge-controlplane, and for
  authentication, team invites or memberships use pgedge-starfleet."
---

# pgEdge BYOC Skill

The CLI embeds the reference for its own version, and that reference
is the source of every command, flag, output field and behavior this
skill relies on. This skill routes you to the right page and names the
rules an agent must not miss. The pages carry the detail.

## Read the reference first

Before you run a byoc command, read the index:

```bash
pgedge llms starfleet byoc
```

The index carries authentication, exit codes, IDs, paging, plan
entitlement, the eleven verbs that need `--force`, how asynchronous
writes report, resource status and the built-in roles. Then read the
one page your task needs, from the table below. The reference states
refusals, orderings and edge cases that `--help` omits, so work from it
rather than from `--help` or memory.

## Set up

Confirm the binary runs, then authenticate through `starfleet`, which
owns the connection that `byoc` borrows. The pgedge-starfleet skill
says how to read `doctor`'s output:

```bash
pgedge version
pgedge starfleet auth login
pgedge starfleet doctor
```

`--profile <name>` selects the tenant on any command. A profile is a
tenant, and the tenant's plan decides which byoc verbs work.

## Find the page for your task

Each page prints with `pgedge llms starfleet byoc <page>`:

| Task | Page |
|---|---|
| Go from nothing to a database with services | `full-stack-setup` |
| Register a cloud account, read its CloudFormation template or zones | `cloud-account` |
| Create, find or delete a backup store | `backup-store` |
| Create, update, share or delete a cluster; read cluster metrics | `cluster` |
| List a cluster's nodes, or read a node's journald log | `node` |
| Create, list, read, update or delete a database | `database` |
| Rotate a built-in role's password, or restore a database | `database` |
| Print a connection string | `database connection-string` |
| Read Postgres logs | `database logs` |
| Read database metrics | `database metrics` |
| Deploy or update an MCP server | `database mcp` |
| Deploy or update a RAG pipeline | `database rag` |
| Deploy or update PostgREST | `database postgrest` |
| List, read or remove a deployed service | `database service` |
| Expose a service on a private cluster | `ingress` |
| Take a backup | `backup` |
| Find a database's backup repository and its backups | `backup-repository` |
| Choose a configuration version | `config-version` |
| Manage SSH keys for node access | `ssh-key` |
| Find out why an operation failed, or watch one | `task` |

A task that spans several rows needs each page. `full-stack-setup`
names them in order.

## Rules an agent must not miss

Each rule is stated in full on the page named beside it.

- **Pass `--wait` on every write in a script.** A write exits 0 when
  the API accepts it, not when the work finishes, and no byoc response
  carries a task ID. See the index's "Asynchronous operations".
- **Run writes one at a time.** A database accepts one change at a
  time, so deploy services one by one, each with `--wait`. See the
  index's "Resource status".
- **Pass `--force` only when the user asked for an unattended run.**
  Eleven verbs prompt, four of them not spelled `delete`. See the
  index's "Destructive operations".
- **An empty list proves nothing about entitlement.** Some verbs are
  plan-gated and some answer an empty array on any plan. See the
  index's "Plan entitlement".
- **Check a read's exit status before acting on its absence.** A
  failed read prints nothing on stdout, so a script that creates when
  a list comes back empty creates on every failure. See
  `full-stack-setup` and `ingress`.
- **`deploy` creates a service, and `update` changes one.** Each
  refuses the other's job, and `update` is partial. See `database`.
- **byoc's admin role is a real Postgres superuser.** Never hand it to
  `postgrest deploy --db-anon-role`, and do not carry a byoc data-load
  recipe to a managed database. See the index's "Built-in roles" and
  `database postgrest`.
- **Keep credentials out of the transcript.** Pass `--no-password` to
  `database connection-string` whenever the output is shown to a
  person. See `database connection-string`.
- **A backup cannot be read back.** `backup create` is the only backup
  verb, so do not suggest `backup list` or `backup get`. See `backup`.
