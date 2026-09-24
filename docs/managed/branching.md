# Branching a pgEdge Starfleet Managed Database

You can branch a pgEdge Starfleet Managed database to get a separate
copy of it for development, testing or a trial change. You connect to
the branch on its own, and you delete it when you are done.

Three terms run through the page:

- The source database is the Managed database you branch.
- A branch is a separate database that starts as a copy of the
  source's data, size, Postgres version, options and services. Changes
  never pass between a branch and its source, in either direction.
- A task is the platform's record of one asynchronous operation, such
  as creating or deleting a branch.

## Before You Start

You need three things before you create a branch:

- The source database's id, `<db-id>`, which
  `pgedge starfleet managed database list` shows. Every branch command
  takes the full UUID.
- A source database that is available and below its branch limit. Run
  `pgedge starfleet managed database get <db-id> -o json` to read
  `branch_count` and `branch_limit`. A null `branch_limit` means the
  database has no limit.
- Your client's address on the source database's Postgres allowlist.
  The branch copies the source's rules when you create it. After that,
  nobody can change the branch's allowlist, and changes to the source
  never reach it. The same holds for the allowlist of each service,
  such as MCP, that you plan to reach on the branch.

To add an address to the source database, see
[Controlling Network Access to a pgEdge Starfleet Managed Database](network-access.md).

## Creating a Branch

`branch create` makes the branch and starts a task that builds it:

1. Create the branch from the source database, and wait for the task:

        pgedge starfleet managed database branch create <db-id> \
            --display-name <display-name> --wait

    `--display-name` is optional and takes at most 25 characters.
    `--wait` returns when the task reaches a terminal state, and
    `--follow` also streams the task's steps. Without either flag, the
    command returns at once and prints a `task list` command to
    monitor the task with. The wait gives up after 600 seconds, and
    `--wait-timeout <seconds>` changes that limit.

2. Record the new branch's id, `<branch-id>`, from the command's
   report.

    In text mode, the report names the branch id first and the
    database id second. Under `-o json`, a script reads the branch id
    from `.id` on stdout.

To check a create without sending it, add `--dry-run`. The command
runs its client-side checks and stops before the request.

A branch bills from the moment it becomes connectable until you
request its deletion. Delete a branch as soon as you no longer need
it.

## Connecting to a Branch

A branch has its own hostname and credentials. A connection string
for the source database never reaches the branch. The Postgres
database name is the same as the source's:

1. Read the branch's connection details as JSON:

        pgedge starfleet managed database branch get \
            <db-id> <branch-id> -o json

    The `connection` object holds `host`, `port`, `database`,
    `username` and `password`. It is absent until the branch can
    accept connections, so run the command again if it is missing.
    Text mode never shows it. The credentials belong to the
    application role unless you add `--user-type admin`.

2. Connect with the values from `connection`, and keep
   `sslmode=require`:

        psql "host=<host> port=<port> dbname=<database> \
            user=<username> sslmode=require"

    At the prompt, enter the `<password>` value from `connection`.

## Connecting to a Branch's MCP Server

A branch copies the source's MCP server with its own address and
bearer token. The source's token never works on the branch, so a
client set up for the source fails there with status 401:

1. Read the branch's MCP address and token as JSON:

        pgedge starfleet managed database branch get \
            <db-id> <branch-id> -o json

    In the `services` entry whose `service_type` is `mcp`, `uri` is the
    address and `mcp_config.init_tokens` is the token.

2. Point your MCP client at `uri`, and send the branch's token as its
   bearer token.

    The endpoint returns status 503 for a short time while the server
    starts, so retry until it answers.

A client whose address is missing from the source's MCP allowlist
when you create the branch gets status 403. Allow the address on the
source's MCP service, then create a new branch.

## Listing a Database's Branches

`branch list` shows every branch of a database that has not been
deleted:

    pgedge starfleet managed database branch list <db-id>

Each row shows the ID, DATABASE, NAME, STATUS, REGION, SIZE, DEPTH and
CREATED columns. NAME is the name the platform assigns, which also
labels the branch's hostname. The display name you chose appears only
under `-o json` or `-o yaml`.

The list takes these flags:

- `--include-deleted` adds branches that have already been deleted.
- `--limit` narrows the page to between 1 and 100 rows. The list
  returns at most 100 rows, so a larger value is refused.
- `--offset` skips that many rows, to page through a longer list.
- `--descending` lists the newest branches first. Without it, the list
  starts with the oldest branch.

## Reading a Branch's Logs and Metrics

A branch keeps logs and metrics of its own. The source database's
logs and metrics never include the branch's, and the branch's never
include the source's. Pass the database id and then the branch id:

    pgedge starfleet managed database branch logs <db-id> <branch-id>
    pgedge starfleet managed database branch metrics <db-id> <branch-id>

Both commands take the same flags as their database counterparts. For
those flags, see
[Reading Logs and Metrics from a pgEdge Starfleet Managed Database](logs-and-metrics.md).

## Deleting a Branch

Deleting a branch stops its billing and frees its place under the
branch limit at once. The platform then tears down the branch and its
data, and nothing can recover them:

1. Check that `<branch-id>` is the branch you mean, in the ID column:

        pgedge starfleet managed database branch list <db-id>

2. Delete the branch, and answer the prompt:

        pgedge starfleet managed database branch delete \
            <db-id> <branch-id>

    The command asks
    `Delete branch <branch-id>? This cannot be undone. [y/N]:`, and
    only `y` or `Y` deletes. The prompt names only the id, so step 1
    is your one check. `--force` skips the prompt, so a scripted
    delete loses the branch's data with no check at all.

A branch that is still being created cannot be deleted. The delete
also takes `--wait`, `--follow` and `--dry-run`, as the create does.
A dry run of the delete sends no request and asks no question.

## Deleting a Database That Has Branches

`database delete` refuses a database that still has branches. Its
`--force` flag only skips the prompt, so it does not change that
refusal.

To delete the database and every one of its branches, add
`--delete-branches`:

    pgedge starfleet managed database delete <db-id> --delete-branches

The prompt then reads
`Delete database <db-id> and every one of its branches? This cannot be undone. [y/N]:`.
Answering `y` loses the data in every branch, as well as in the
database. Before you answer, copy out anything a branch holds that you
still need.

## Troubleshooting

These entries cover the failures a branch command reports.

### Branch Creation Is Refused

`branch create` returns an error, and no new branch appears in
`branch list`. The platform refuses a branch when the source database
is not available or is at its branch limit. It also refuses one while
your subscription has a failed payment.

At the branch limit, the command exits with status 1 and prints
`a branch of this database refuses the request, and waiting will not clear it`.
Run `pgedge starfleet managed database get <db-id> -o json`. If
`branch_count` equals `branch_limit`, delete a branch you no longer
need, and its place frees at once.

### A New Branch Cannot Be Deleted

`branch delete` exits with status 1 for a branch you created moments
ago, and reports that the resource is busy with another operation. A
branch still being created cannot be deleted. Wait until
`branch get` returns a `connection` object for the branch, then run
the delete again.

### The Delete Refuses to Prompt

A delete exits with status 2 and prints
`this operation is destructive; run with --force to confirm, or run interactively to be prompted`.
The command ran without a terminal, so it could not ask for
confirmation. Run the command from an interactive terminal and answer
the prompt.

### The Branch Refuses a Connection

A client that reaches the source database cannot reach the branch.
The branch's allowlist holds the source's rules from the moment you
created the branch, and it never changes. Add the client's address to
the source database, then create a new branch. The new branch copies
the source's data again, so it holds none of the old branch's changes.

## Next Steps

These pages cover the tasks around a branch:

- [Controlling Network Access to a pgEdge Starfleet Managed Database](network-access.md)
  describes the source database's allowlist, which each new branch
  copies.
- [Reading Logs and Metrics from a pgEdge Starfleet Managed Database](logs-and-metrics.md)
  describes the flags on `branch logs` and `branch metrics`.
- [pgedge starfleet managed command reference](../reference/starfleet-managed.md)
  lists every flag on the branch commands.
