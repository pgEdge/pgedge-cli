---
name: pgedge-managed
description: "Use this skill when the user wants to manage pgEdge
  Managed databases, single-master Postgres that pgEdge hosts and
  operates, through the pgedge CLI. Triggers on: pgEdge Managed, pgedge
  starfleet managed, managed database, creating or resizing a hosted
  pgEdge database, connecting to one, a refused connection, IP
  allowlist, allowing an address, deletion protection, managed regions
  or sizes, deploying MCP or RAG on a managed database, branching a
  managed database, a copy-on-write branch, or a managed database's
  backups. There are no clusters or nodes to place a managed database
  on. For databases in your own cloud account use the pgedge-byoc
  skill, for a self-hosted Control Plane use pgedge-controlplane, and
  for authentication use pgedge-starfleet."
---

# pgEdge Managed Skill

The CLI embeds the reference for its own version, and that reference
is the source of every command, flag, output field and behavior this
skill relies on. This skill routes you to the right page and names the
rules an agent must not miss. The pages carry the detail.

## Read the reference first

Before you run a managed command, read the index:

```bash
pgedge llms starfleet managed
```

The index carries authentication, exit statuses, IDs, paging, the
differences from BYOC, database status, and how asynchronous writes
report. Then read the one page your task needs, from the table below.
The reference states refusals, orderings and edge cases that `--help`
omits, so work from it rather than from `--help` or memory.

## Set up

Confirm the binary runs, then authenticate through `starfleet`, which
owns the connection that `managed` borrows:

```bash
pgedge version
pgedge starfleet auth login
pgedge starfleet doctor
```

`--profile <name>` selects the tenant on any command. A profile is a
tenant, so a database name means different databases under different
profiles. Identify a database by its UUID, never by its name.

## Find the page for your task

Each page prints with `pgedge llms starfleet managed <page>`:

| Task | Page |
|---|---|
| Create, list, read, resize, update or delete a database | `database` |
| Connect a client, or print a connection string | `database` |
| Let an address reach Postgres or a service | `database allowlist` |
| Take, list or delete a branch | `database branch` |
| Deploy or update an MCP server | `database mcp` |
| Deploy or update a RAG pipeline | `database rag` |
| List, read or remove a deployed service | `database service` |
| Call a deployed MCP or RAG service from a client | `connecting-a-client` |
| Load a schema and data, or install an extension | `loading-data` |
| Read metrics | `database metrics` |
| Read Postgres logs | `database logs` |
| Take or restore a backup | `backup` |
| Find out why an operation failed, or watch one | `task` |
| Choose a region, size or Postgres version | `region`, `size`, `pg-version` |
| Find the address the API sees you arriving from | `client-ip` |

A task that spans several rows needs each page. Creating a database
and connecting to it reads `database` and then `database allowlist`.

## Rules an agent must not miss

Each rule is stated in full on the page named beside it.

- **Pass `--wait` on every write in a script.** A write exits 0 when
  the API accepts it, not when the work finishes. The index's
  "Asynchronous verbs" section has the exit statuses.
- **Run writes one at a time.** Most writes need the database
  `available` and refuse a busy one. Several branch creates sent
  together can fail, so create branches one at a time, each with
  `--wait`. See the index's "Database status" section and the
  `database branch` page.
- **A new database admits no connection.** Every service deployed on
  it starts closed too, with an allowlist of its own. A refused
  connection is an allowlist question first. See `database allowlist`.
- **Pass full UUIDs, and discover accepted values.** A name or an ID
  prefix is refused. Read `region list`, `size list` and `pg-version
  list` before you choose a value.
- **`--force` only skips the confirmation prompt.** It never deletes
  branches: `--delete-branches` does that, and branch data cannot be
  recovered. Pass `--force` only when the user asked for an unattended
  run.
- **Keep credentials out of the transcript.** Pass `--no-password` to
  `database connection-string` whenever the output is shown to a
  person. Otherwise write the output to a file or a variable, never to
  the conversation.
- **Ask a service whether it is ready.** A service's `state` and a
  succeeded task do not mean the endpoint answers. See
  `connecting-a-client`.
- **Attach to a known task with `task wait`.** Do not run `task list`
  in a shell loop to follow a task whose ID you already have. See
  `task`.
- **Offer only `mcp` and `rag` as services.** The platform does not
  accept `postgrest` on a managed database. See `database postgrest`.
