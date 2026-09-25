# pgedge starfleet managed command reference

This page lists the usage, description and declared flags for every
command in the managed sub-tree, which runs single-master databases
pgEdge hosts and operates. There is no cluster or node to choose.

The managed tree shares the starfleet module's one connection: the flags
on [pgedge starfleet](starfleet.md) and the login it describes apply here
unchanged. Global flags are declared on the root command and listed
on the [pgedge reference page](pgedge.md).

For workflows and behavioral detail, run
`pgedge llms starfleet managed` against your installed binary.

<!-- BEGIN GENERATED PAGE: pgedge starfleet managed -->

## pgedge starfleet managed

**Usage:** `pgedge starfleet managed <command>`

Manage pgEdge Managed databases

**Example:**

```
pgedge starfleet auth login
pgedge starfleet managed database list
```

### pgedge starfleet managed backup

**Usage:** `pgedge starfleet managed backup <command>`

Inspect, take and restore pgEdge Managed backups

**Example:**

```
pgedge starfleet managed backup list --database-id <database_id>
pgedge starfleet managed backup create --database-id <database_id> --kind hot
pgedge starfleet managed backup restore <backup_id>
```

#### pgedge starfleet managed backup create

**Usage:** `pgedge starfleet managed backup create [flags]`

Take an on-demand backup

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database-id string` | Yes |  | Database to back up (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--kind string` | Yes |  | Backup tier to take: hot or durable |

**Example:**

```
pgedge starfleet managed backup create \
  --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind hot
pgedge starfleet managed backup create \
  --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind durable
```

#### pgedge starfleet managed backup get

**Usage:** `pgedge starfleet managed backup get <backup_id>`

Show backup details

**Example:**

```
pgedge starfleet managed backup get <backup_id>
pgedge starfleet managed backup get b8c9d0e1-f2a3-4567-bcde-678901234567 -o yaml
```

#### pgedge starfleet managed backup list

**Usage:** `pgedge starfleet managed backup list [flags]`

List backups

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--created-after string` | No |  | Only backups created at or after this RFC3339 time |
| `--created-before string` | No |  | Only backups created at or before this RFC3339 time |
| `--database-id string` | No |  | Filter by database (full UUID) |
| `--descending` | No | `true` | Sort newest first (--descending=false for oldest first) |
| `--kind string` | No |  | Filter by kind (e.g. hot, durable) |
| `--limit int` | No |  | Maximum number of results to return (1-100, and 100 is also the default) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet managed backup list
pgedge starfleet managed backup list \
  --database-id e5f6a7b8-c9d0-1234-efab-567890123456 --kind durable
pgedge starfleet managed backup list --created-after 2026-08-01T00:00:00Z
```

#### pgedge starfleet managed backup restore

**Usage:** `pgedge starfleet managed backup restore <backup_id> [flags]`

Restore a database from a backup

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed backup restore <backup_id>
pgedge starfleet managed backup restore <backup_id> --force --wait
```

### pgedge starfleet managed client-ip

**Usage:** `pgedge starfleet managed client-ip`

Show the source address the API sees for you

**Example:**

```
pgedge starfleet managed client-ip
pgedge starfleet managed database allowlist add <database_id> --my-ip
```

### pgedge starfleet managed database

**Usage:** `pgedge starfleet managed database <command>`

Manage pgEdge Managed databases

**Example:**

```
pgedge starfleet managed database list
pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456
```

#### pgedge starfleet managed database allowlist

**Usage:** `pgedge starfleet managed database allowlist <command>`

Manage which addresses may reach a database

**Example:**

```
pgedge starfleet managed database allowlist get <database_id>
pgedge starfleet managed database allowlist add <database_id> --my-ip
pgedge starfleet managed database allowlist add <database_id> \
    203.0.113.0/24 --label office --service mcp
```

##### pgedge starfleet managed database allowlist add

**Usage:** `pgedge starfleet managed database allowlist add <database_id> [<cidr>...] [flags]`

Allow addresses to reach an endpoint

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--label string` | No |  | Note stored on each rule added by this call, at most 64 characters |
| `--my-ip` | No |  | Also allow the address the API sees this command arriving from |
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database allowlist add <database_id> --my-ip
pgedge starfleet managed database allowlist add <database_id> \
    203.0.113.0/24 198.51.100.9 --label office
pgedge starfleet managed database allowlist add <database_id> \
    203.0.113.7 --service mcp --wait
```

##### pgedge starfleet managed database allowlist clear

**Usage:** `pgedge starfleet managed database allowlist clear <database_id> [flags]`

Close an endpoint to every address

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database allowlist clear <database_id>
pgedge starfleet managed database allowlist clear <database_id> \
    --service rag --force
```

##### pgedge starfleet managed database allowlist get

**Usage:** `pgedge starfleet managed database allowlist get [<database_id>] [flags]`

Show an endpoint's allowlist and its state

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |

**Example:**

```
pgedge starfleet managed database allowlist get <database_id>
pgedge starfleet managed database allowlist get <database_id> --service mcp
```

##### pgedge starfleet managed database allowlist open

**Usage:** `pgedge starfleet managed database allowlist open <database_id> [flags]`

Let every address reach an endpoint

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database allowlist open <database_id>
pgedge starfleet managed database allowlist open <database_id> --service mcp
```

##### pgedge starfleet managed database allowlist remove

**Usage:** `pgedge starfleet managed database allowlist remove <database_id> <cidr>... [flags]`

Stop addresses reaching an endpoint

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt when removing the last rule |
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database allowlist remove <database_id> 203.0.113.7
pgedge starfleet managed database allowlist remove <database_id> \
    198.51.100.0/24 --service rag
```

##### pgedge starfleet managed database allowlist set

**Usage:** `pgedge starfleet managed database allowlist set <database_id> <cidr>... [flags]`

Replace an endpoint's allowlist

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--label string` | No |  | Note stored on every rule set by this call, at most 64 characters |
| `--service string` | No |  | Edit this service's own allowlist instead of the Postgres endpoint's: mcp, rag or postgrest |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database allowlist set <database_id> \
    203.0.113.0/24 198.51.100.9 --label ci
```

#### pgedge starfleet managed database branch

**Usage:** `pgedge starfleet managed database branch <command>`

Manage branches of a managed database

**Example:**

```
pgedge starfleet managed database branch list <database_id>
pgedge starfleet managed database branch create <database_id> \
  --display-name dev-copy
pgedge starfleet managed database branch get <database_id> <branch_id>
pgedge starfleet managed database branch metrics <database_id> <branch_id>
pgedge starfleet managed database branch logs <database_id> <branch_id>
pgedge starfleet managed database branch delete <database_id> <branch_id>
```

##### pgedge starfleet managed database branch create

**Usage:** `pgedge starfleet managed database branch create <database_id> [flags]`

Create a branch of a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--display-name string` | No |  | Display name for the branch, at most 25 characters |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database branch create e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database branch create e5f6a7b8-c9d0-1234-efab-567890123456 \
  --display-name dev-copy
```

##### pgedge starfleet managed database branch delete

**Usage:** `pgedge starfleet managed database branch delete <database_id> <branch_id> [flags]`

Delete a branch

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database branch delete <database_id> <branch_id>
pgedge starfleet managed database branch delete <database_id> <branch_id> \
  --force
```

##### pgedge starfleet managed database branch get

**Usage:** `pgedge starfleet managed database branch get <database_id> <branch_id> [flags]`

Show branch details

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--user-type string` | No |  | Role whose credentials to return: admin, app or app_read_only (default app) |

**Example:**

```
pgedge starfleet managed database branch get <database_id> <branch_id>
pgedge starfleet managed database branch get <database_id> <branch_id> \
  --user-type admin -o yaml
```

##### pgedge starfleet managed database branch list

**Usage:** `pgedge starfleet managed database branch list [<database_id>] [flags]`

List a database's branches

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--descending` | No |  | List the newest branches first |
| `--include-deleted` | No |  | Also show deleted branches |
| `--limit int` | No |  | Maximum number of results to return (1-100, and 100 is also the default) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet managed database branch list e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database branch list e5f6a7b8-c9d0-1234-efab-567890123456 \
  --include-deleted
```

##### pgedge starfleet managed database branch logs

**Usage:** `pgedge starfleet managed database branch logs <database_id> <branch_id> [flags]`

Read a branch's logs

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--end-time string` | No |  | Window end as an RFC3339 timestamp |
| `--max-lines int` | No |  | Maximum log records to return, 1 to 1000 (API default 100) |
| `--start-time string` | No |  | Window start as an RFC3339 timestamp |

**Example:**

```
pgedge starfleet managed database branch logs e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901
pgedge starfleet managed database branch logs e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901 \
  --max-lines 500
```

##### pgedge starfleet managed database branch metrics

**Usage:** `pgedge starfleet managed database branch metrics <database_id> <branch_id> [flags]`

Read a branch's metrics

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--end-time string` | No |  | Window end as an RFC3339 timestamp |
| `--start-time string` | No |  | Window start as an RFC3339 timestamp |
| `--window string` | No |  | Relative lookback window, in value,unit form (1 or more) |

**Example:**

```
pgedge starfleet managed database branch metrics e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901
pgedge starfleet managed database branch metrics e5f6a7b8-c9d0-1234-efab-567890123456 b1c2d3e4-f5a6-7890-bcde-f12345678901 \
  --window 5,minutes
```

#### pgedge starfleet managed database connection-string

**Usage:** `pgedge starfleet managed database connection-string [<database_id>] [flags]`

Print a connection string for a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--format string` | No | `uri` | Shape of the text output: uri or env |
| `--no-password` | No |  | Leave the password out of the string |
| `--user-type string` | No |  | Role whose credentials to use: admin, app or app_read_only (default app) |

**Example:**

```
pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456 \
  --user-type admin --no-password
pgedge starfleet managed database connection-string e5f6a7b8-c9d0-1234-efab-567890123456 \
  --format env > .env
```

#### pgedge starfleet managed database create

**Usage:** `pgedge starfleet managed database create [flags]`

Create a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allow stringArray` | No |  | IPv4 address or CIDR block allowed to reach the Postgres endpoint; repeat for more than one |
| `--display-name string` | No |  | Display name for the database, at most 25 characters |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--link` | No |  | Link the current folder to the new database (needs --wait or --follow) |
| `--my-ip` | No |  | Also allow the address the API sees this command arriving from |
| `--name string` | Yes |  | Database name |
| `--open` | No |  | Admit every address (one 0.0.0.0/0 rule); never the default |
| `--options strings` | No |  | Comma-separated list of options |
| `--pg-version string` | No |  | Postgres major version to create the database on: 18, 17, 16 (fixed for the life of the database; defaults to the newest supported version) |
| `--region string` | No |  | Region to provision the database in (e.g. us-east-1); checked against 'region list'; optional while the API publishes only one, and required as soon as it publishes more |
| `--size string` | Yes |  | Managed size name (e.g. small, large); checked against 'size list' |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database create --name mydb \
  --region us-east-1 --size small
pgedge starfleet managed database create --name mydb \
  --region us-east-1 --size large --pg-version 16
pgedge starfleet managed database create --name mydb \
  --region us-east-1 --size small --my-ip
pgedge starfleet managed database create --name myapp \
  --size small --my-ip --wait --link
```

#### pgedge starfleet managed database delete

**Usage:** `pgedge starfleet managed database delete <database_id> [flags]`

Delete a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--delete-branches` | No |  | Also delete every branch first; data cannot be recovered |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456 \
  --force
pgedge starfleet managed database delete e5f6a7b8-c9d0-1234-efab-567890123456 \
  --delete-branches --force
```

#### pgedge starfleet managed database env

**Usage:** `pgedge starfleet managed database env <command>`

Write a database's connection into a .env file

**Example:**

```
pgedge starfleet managed database env pull
```

##### pgedge starfleet managed database env pull

**Usage:** `pgedge starfleet managed database env pull [<database_id>] [flags]`

Write DATABASE_URL for a database into .env

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--file string` | No |  | File to write (default .env beside .pgedge/, or in the current folder when an ID is given) |
| `--user-type string` | No |  | Role whose credentials to use: admin, app or app_read_only (default app) |
| `--var string` | No | `DATABASE_URL` | Variable name to set |

**Example:**

```
pgedge starfleet managed database env pull
pgedge starfleet managed database env pull e5f6a7b8-c9d0-1234-efab-567890123456 \
  --file .env.local --user-type app_read_only
```

#### pgedge starfleet managed database get

**Usage:** `pgedge starfleet managed database get [<database_id>] [flags]`

Show managed database details

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--user-type string` | No |  | Role whose credentials to return: admin, app or app_read_only (default app) |

**Example:**

```
pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database get e5f6a7b8-c9d0-1234-efab-567890123456 \
  --user-type admin -o yaml
```

#### pgedge starfleet managed database inspect

**Usage:** `pgedge starfleet managed database inspect [<database_id>] <analysis> [flags]`

Run a read-only diagnostic against a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--user-type string` | No |  | Role to connect as: admin, app or app_read_only (default app; admin for the statistics analyses) |

**Example:**

```
pgedge starfleet managed database inspect e5f6a7b8-c9d0-1234-efab-567890123456 table-sizes
pgedge starfleet managed database inspect e5f6a7b8-c9d0-1234-efab-567890123456 \
  long-running-queries --user-type admin -o json
```

#### pgedge starfleet managed database link

**Usage:** `pgedge starfleet managed database link <database_id> [flags]`

Link the current folder to a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--branch string` | No |  | Link a branch of the database instead of the database itself |
| `--force` | No |  | Replace a link to another database |

**Example:**

```
pgedge starfleet managed database link e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database link e5f6a7b8-c9d0-1234-efab-567890123456 \
  --branch 0a1b2c3d-4e5f-6789-abcd-ef0123456789
```

#### pgedge starfleet managed database list

**Usage:** `pgedge starfleet managed database list [flags]`

List managed databases

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--descending` | No |  | Sort in descending order |
| `--limit int` | No |  | Maximum number of results to return (1-1000) |
| `--offset int` | No |  | Offset into the results for pagination |
| `--region string` | No |  | Filter by region |

**Example:**

```
pgedge starfleet managed database list
pgedge starfleet managed database list --region us-east-1 -o json
```

#### pgedge starfleet managed database logs

**Usage:** `pgedge starfleet managed database logs [<database_id>] [flags]`

Read a managed database's logs

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--end-time string` | No |  | Window end as an RFC3339 timestamp |
| `--max-lines int` | No |  | Maximum log records to return, 1 to 1000 (API default 100) |
| `--start-time string` | No |  | Window start as an RFC3339 timestamp |

**Example:**

```
pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 \
  --max-lines 500
pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 -o json
pgedge starfleet managed database logs e5f6a7b8-c9d0-1234-efab-567890123456 \
  --start-time 2026-08-17T00:00:00Z
```

#### pgedge starfleet managed database mcp

**Usage:** `pgedge starfleet managed database mcp <command>`

Manage the MCP server on a database

**Example:**

```
pgedge starfleet managed database mcp deploy <database_id>
pgedge starfleet managed database mcp update <database_id> --allow-writes
```

##### pgedge starfleet managed database mcp deploy

**Usage:** `pgedge starfleet managed database mcp deploy <database_id> [flags]`

Deploy an MCP server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allow-writes` | No |  | Grant the MCP service read-write access (WARNING: allows LLM to modify data) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-api-key string` | No |  | API key for the embedding provider (required when --embedding-provider is set on deploy; the stored key is reused if omitted on update) |
| `--embedding-model string` | No |  | Embedding model identifier (required when --embedding-provider is set) |
| `--embedding-provider string` | No |  | Embedding provider: openai or voyage |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--init-tokens string` | No |  | Bearer token forwarded to the MCP server as INIT_TOKENS |
| `--init-users string` | No |  | Comma-separated username:password pairs forwarded as INIT_USERS |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database mcp deploy <database_id>
pgedge starfleet managed database mcp deploy <database_id> \
  --embedding-provider openai \
  --embedding-model text-embedding-3-small \
  --embedding-api-key sk-...
```

##### pgedge starfleet managed database mcp update

**Usage:** `pgedge starfleet managed database mcp update <database_id> [flags]`

Update the MCP server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allow-writes` | No |  | Grant the MCP service read-write access (WARNING: allows LLM to modify data) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-api-key string` | No |  | API key for the embedding provider (required when --embedding-provider is set on deploy; the stored key is reused if omitted on update) |
| `--embedding-model string` | No |  | Embedding model identifier (required when --embedding-provider is set) |
| `--embedding-provider string` | No |  | Embedding provider: openai or voyage |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--init-tokens string` | No |  | Bearer token forwarded to the MCP server as INIT_TOKENS |
| `--init-users string` | No |  | Comma-separated username:password pairs forwarded as INIT_USERS |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database mcp update <database_id> --allow-writes
pgedge starfleet managed database mcp update <database_id> --allow-writes=false
```

#### pgedge starfleet managed database metrics

**Usage:** `pgedge starfleet managed database metrics [<database_id>] [flags]`

Read a managed database's metrics

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--end-time string` | No |  | Window end as an RFC3339 timestamp |
| `--start-time string` | No |  | Window start as an RFC3339 timestamp |
| `--window string` | No |  | Relative lookback window, in value,unit form (1 or more) |

**Example:**

```
pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456
pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 \
  --window 5,minutes
pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 -o json
pgedge starfleet managed database metrics e5f6a7b8-c9d0-1234-efab-567890123456 \
  --start-time 2026-08-17T00:00:00Z
```

#### pgedge starfleet managed database postgrest

**Usage:** `pgedge starfleet managed database postgrest <command>`

Manage PostgREST on a database (not yet supported)

**Example:**

```
pgedge starfleet managed database postgrest deploy <database_id> \
  --db-schemas public --db-anon-role app_read_only
pgedge starfleet managed database postgrest update <database_id> --max-rows 500
```

##### pgedge starfleet managed database postgrest deploy

**Usage:** `pgedge starfleet managed database postgrest deploy <database_id> [flags]`

Deploy PostgREST on a database (not yet supported)

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cors-origins string` | No |  | Comma-separated list of allowed CORS origins |
| `--db-anon-role string` | Yes |  | Postgres role used for unauthenticated requests |
| `--db-pool int` | No |  | Database connections to keep open (1-30, default 10) |
| `--db-schemas string` | Yes |  | Comma-separated schemas to expose as REST (e.g. public,api) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--jwt-audience string` | No |  | JWT audience claim to require |
| `--jwt-role-claim-key string` | No |  | JSONPath to the role claim inside the JWT |
| `--jwt-secret string` | No |  | JWT signing secret (min 32 chars); write-only, never returned |
| `--max-rows int` | No |  | Maximum rows returned per request (1-10000, default 1000) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database postgrest deploy <database_id> \
  --db-schemas public --db-anon-role app_read_only
pgedge starfleet managed database postgrest deploy <database_id> \
  --db-schemas public,api --db-anon-role app_read_only \
  --jwt-secret <32-char-minimum-secret>
```

##### pgedge starfleet managed database postgrest update

**Usage:** `pgedge starfleet managed database postgrest update <database_id> [flags]`

Update PostgREST on a database (not yet supported)

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cors-origins string` | No |  | Comma-separated list of allowed CORS origins |
| `--db-anon-role string` | No |  | Postgres role used for unauthenticated requests |
| `--db-pool int` | No |  | Database connections to keep open (1-30, default 10) |
| `--db-schemas string` | No |  | Comma-separated schemas to expose as REST (e.g. public,api) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--jwt-audience string` | No |  | JWT audience claim to require |
| `--jwt-role-claim-key string` | No |  | JSONPath to the role claim inside the JWT |
| `--jwt-secret string` | No |  | JWT signing secret (min 32 chars); write-only, never returned |
| `--max-rows int` | No |  | Maximum rows returned per request (1-10000, default 1000) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database postgrest update <database_id> --max-rows 500
pgedge starfleet managed database postgrest update <database_id> \
  --db-schemas public,api
```

#### pgedge starfleet managed database rag

**Usage:** `pgedge starfleet managed database rag <command>`

Manage the RAG server on a database

**Example:**

```
pgedge starfleet managed database rag deploy <database_id> \
  --embedding-llm-provider openai \
  --embedding-llm-model text-embedding-3-small \
  --embedding-llm-api-key "$OPENAI_API_KEY" \
  --completion-llm-provider openai \
  --completion-llm-model gpt-4o \
  --completion-llm-api-key "$OPENAI_API_KEY" \
  --pipeline-config pipelines.json
pgedge starfleet managed database rag update <database_id> --top-n 10
```

##### pgedge starfleet managed database rag deploy

**Usage:** `pgedge starfleet managed database rag deploy <database_id> [flags]`

Deploy a RAG server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--completion-llm-api-key string` | Yes |  | API key for the completion LLM provider |
| `--completion-llm-model string` | Yes |  | Completion LLM model identifier |
| `--completion-llm-provider string` | Yes |  | Completion LLM provider (e.g. openai, anthropic) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-llm-api-key string` | Yes |  | API key for the embedding LLM provider |
| `--embedding-llm-model string` | Yes |  | Embedding LLM model identifier |
| `--embedding-llm-provider string` | Yes |  | Embedding LLM provider (openai or anthropic) |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--pipeline-config string` | Yes |  | Path to a JSON file containing pipeline definitions |
| `--token-budget int` | No |  | Default max completion tokens across all pipelines |
| `--top-n int` | No |  | Default number of results to retrieve per pipeline |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database rag deploy <database_id> \
  --embedding-llm-provider openai \
  --embedding-llm-model text-embedding-3-small \
  --embedding-llm-api-key sk-... \
  --completion-llm-provider openai --completion-llm-model gpt-4o \
  --completion-llm-api-key sk-... --pipeline-config pipelines.json
```

##### pgedge starfleet managed database rag update

**Usage:** `pgedge starfleet managed database rag update <database_id> [flags]`

Update the RAG server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--completion-llm-api-key string` | No |  | API key for the completion LLM provider |
| `--completion-llm-model string` | No |  | Completion LLM model identifier |
| `--completion-llm-provider string` | No |  | Completion LLM provider (e.g. openai, anthropic) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-llm-api-key string` | No |  | API key for the embedding LLM provider |
| `--embedding-llm-model string` | No |  | Embedding LLM model identifier |
| `--embedding-llm-provider string` | No |  | Embedding LLM provider (openai or anthropic) |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--pipeline-config string` | No |  | Path to a JSON file containing pipeline definitions |
| `--token-budget int` | No |  | Default max completion tokens across all pipelines |
| `--top-n int` | No |  | Default number of results to retrieve per pipeline |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database rag update <database_id> --top-n 10
pgedge starfleet managed database rag update <database_id> \
  --pipeline-config pipelines.json
```

#### pgedge starfleet managed database resize

**Usage:** `pgedge starfleet managed database resize <database_id> [flags]`

Resize a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--size string` | Yes |  | Target managed size name (e.g. small, large); checked against 'size list' |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database resize e5f6a7b8-c9d0-1234-efab-567890123456 \
  --size large
pgedge starfleet managed database resize e5f6a7b8-c9d0-1234-efab-567890123456 \
  --size large --force --wait
```

#### pgedge starfleet managed database rotate-password

**Usage:** `pgedge starfleet managed database rotate-password <database_id> [flags]`

Rotate a built-in role's password

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--role string` | Yes |  | Built-in role to rotate: admin, app, or app_read_only |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database rotate-password e5f6a7b8-c9d0-1234-efab-567890123456 --role app
pgedge starfleet managed database rotate-password e5f6a7b8-c9d0-1234-efab-567890123456 --role admin --force
```

#### pgedge starfleet managed database service

**Usage:** `pgedge starfleet managed database service <command>`

Manage services deployed on a database

**Example:**

```
pgedge starfleet managed database service list <database_id>
pgedge starfleet managed database service get <database_id> mcp
```

##### pgedge starfleet managed database service get

**Usage:** `pgedge starfleet managed database service get <database_id> <type>`

Show details of a service

**Example:**

```
pgedge starfleet managed database service get <database_id> mcp
pgedge starfleet managed database service get <database_id> rag -o json
```

##### pgedge starfleet managed database service list

**Usage:** `pgedge starfleet managed database service list <database_id>`

List services deployed on a database

**Example:**

```
pgedge starfleet managed database service list <database_id>
pgedge starfleet managed database service list <database_id> -o json
```

##### pgedge starfleet managed database service remove

**Usage:** `pgedge starfleet managed database service remove <database_id> <type> [flags]`

Remove a service type from a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet managed database service remove <database_id> mcp
pgedge starfleet managed database service remove <database_id> rag --force
```

#### pgedge starfleet managed database unlink

**Usage:** `pgedge starfleet managed database unlink`

Remove the current folder's database link

**Example:**

```
pgedge starfleet managed database unlink
```

#### pgedge starfleet managed database update

**Usage:** `pgedge starfleet managed database update <database_id> [flags]`

Update a managed database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--deletion-protection` | No |  | Refuse deletion until this is turned off again |
| `--display-name string` | No |  | Display name for the database, at most 25 characters |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--options strings` | No |  | Comma-separated list of options |

**Example:**

```
pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
  --display-name "My Database"
pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
  --options key1,key2
pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
  --deletion-protection
pgedge starfleet managed database update e5f6a7b8-c9d0-1234-efab-567890123456 \
  --deletion-protection=false
```

### pgedge starfleet managed pg-version

**Usage:** `pgedge starfleet managed pg-version <command>`

Discover Postgres versions for managed databases

**Example:**

```
pgedge starfleet managed pg-version list
```

#### pgedge starfleet managed pg-version list

**Usage:** `pgedge starfleet managed pg-version list`

List available Postgres versions

**Example:**

```
pgedge starfleet managed pg-version list
pgedge starfleet managed pg-version list -o json
```

### pgedge starfleet managed region

**Usage:** `pgedge starfleet managed region <command>`

Discover pgEdge Managed regions

**Example:**

```
pgedge starfleet managed region list
```

#### pgedge starfleet managed region list

**Usage:** `pgedge starfleet managed region list`

List available regions

**Example:**

```
pgedge starfleet managed region list
pgedge starfleet managed region list -o json
```

### pgedge starfleet managed size

**Usage:** `pgedge starfleet managed size <command>`

Discover pgEdge Managed sizes

**Example:**

```
pgedge starfleet managed size list
pgedge starfleet managed size list --pricing
```

#### pgedge starfleet managed size get

**Usage:** `pgedge starfleet managed size get <size_id>`

Show a size's details

**Example:**

```
pgedge starfleet managed size get <size_id>
pgedge starfleet managed size get <size_id> -o yaml
```

#### pgedge starfleet managed size list

**Usage:** `pgedge starfleet managed size list [flags]`

List available sizes

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--pricing` | No |  | Include each size's current price |

**Example:**

```
pgedge starfleet managed size list
pgedge starfleet managed size list --pricing
pgedge starfleet managed size list -o json
```

### pgedge starfleet managed task

**Usage:** `pgedge starfleet managed task <command>`

Inspect pgEdge Managed tasks

**Example:**

```
pgedge starfleet managed task list --subject-id <database_id>
pgedge starfleet managed task wait <task_id>
```

#### pgedge starfleet managed task get

**Usage:** `pgedge starfleet managed task get <task_id>`

Show task details

**Example:**

```
pgedge starfleet managed task get <task_id> -o yaml
```

#### pgedge starfleet managed task list

**Usage:** `pgedge starfleet managed task list [flags]`

List tasks

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--limit int` | No |  | Maximum number of results to return (API default 25) |
| `--name string` | No |  | Filter by task name (e.g. rotate-password-managed) |
| `--offset int` | No |  | Offset into the results for pagination |
| `--status string` | No |  | Filter by status (queued, running, succeeded, failed) |
| `--subject-id string` | No |  | Filter by subject (full UUID) |
| `--subject-kind string` | No |  | Filter by subject kind |

**Example:**

```
pgedge starfleet managed task list
pgedge starfleet managed task list --subject-id <database_id> --status failed
```

#### pgedge starfleet managed task wait

**Usage:** `pgedge starfleet managed task wait <task_id> [flags]`

Wait for a task to complete

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--follow` | No |  | Stream the task's step messages instead of bare status lines |
| `--wait-interval int` | No | `5` | Polling interval in seconds |
| `--wait-timeout int` | No | `600` | Maximum seconds to wait for task completion |

**Example:**

```
pgedge starfleet managed task wait <task_id> --wait-timeout 900
pgedge starfleet managed task wait <task_id> --follow
```

<!-- END GENERATED PAGE -->
