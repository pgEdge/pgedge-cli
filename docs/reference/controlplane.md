# pgedge controlplane command reference

This page lists the usage, description and declared flags for every
command in the controlplane module, which targets a self-hosted pgEdge
Control Plane. There is no login: commands target `--base-url`
directly, and the Control Plane is one you run yourself.

Global flags are declared on the root command and listed on the
[pgedge reference page](pgedge.md).

For workflows, standing up a Control Plane and behavioral detail, run
`pgedge llms controlplane` against your installed binary.

<!-- BEGIN GENERATED PAGE: pgedge controlplane -->

## pgedge controlplane

**Usage:** `pgedge controlplane <command> [flags]`

Manage a pgEdge Control Plane

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--base-url stringArray` | No |  | Control Plane base URL; repeat for HA failover (default http://localhost:3000) |
| `--ca-cert string` | No |  | Path to the CA certificate (mTLS) |
| `--client-cert string` | No |  | Path to the client certificate (mTLS) |
| `--client-key string` | No |  | Path to the client key (mTLS) |
| `--insecure` | No |  | Skip TLS certificate verification (dev only) |
| `--timeout duration` | No | `30s` | Per-request timeout (Go duration; 0 disables) |

**Example:**

```
pgedge controlplane config set --base-url http://localhost:3000
pgedge controlplane version
pgedge controlplane database list
```

### pgedge controlplane api

**Usage:** `pgedge controlplane api <method> <path> [flags]`

Call any Control Plane API path over the connection

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-d, --data string` | No |  | JSON request body: a literal, @file, or - for stdin |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `-H, --header stringArray` | No |  | Request header as Name: value (repeatable) |
| `-i, --include` | No |  | Print the status and response headers on stderr |
| `--query stringArray` | No |  | Query parameter as key=value (repeatable) |

**Example:**

```
pgedge controlplane api GET /v1/databases
pgedge controlplane api GET /v1/hosts -o json
pgedge controlplane api POST /v1/databases --data @spec.json --dry-run
```

### pgedge controlplane cluster

**Usage:** `pgedge controlplane cluster <command>`

Manage the Control Plane cluster

**Example:**

```
pgedge controlplane cluster info
pgedge controlplane cluster init
```

#### pgedge controlplane cluster info

**Usage:** `pgedge controlplane cluster info`

Show cluster information

**Example:**

```
pgedge controlplane cluster info
pgedge controlplane cluster info -o yaml
```

#### pgedge controlplane cluster init

**Usage:** `pgedge controlplane cluster init [flags]`

Initialize a new cluster

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cluster-id string` | No |  | Optional cluster ID (default server-generated) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |

**Example:**

```
pgedge controlplane cluster init
pgedge controlplane cluster init --cluster-id prod
```

#### pgedge controlplane cluster join

**Usage:** `pgedge controlplane cluster join [flags]`

Join this host to a cluster

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--server-url stringArray` | No |  | Existing cluster member to contact (repeatable, at least one required) |
| `--token string` | No |  | Cluster join token (required) |

**Example:**

```
pgedge controlplane cluster join --base-url http://new-host:3000 \
  --token PGEDGE-abc --server-url http://existing-1:3000
```

#### pgedge controlplane cluster join-token

**Usage:** `pgedge controlplane cluster join-token`

Print the cluster join token

**Example:**

```
pgedge controlplane cluster join-token
```

### pgedge controlplane config

**Usage:** `pgedge controlplane config <command>`

Manage the Control Plane connection

**Example:**

```
pgedge controlplane config set --base-url http://localhost:3000
pgedge controlplane config view
```

#### pgedge controlplane config set

**Usage:** `pgedge controlplane config set`

Save Control Plane connection settings

**Example:**

```
pgedge controlplane config set --base-url https://cp-1:3000 \
  --ca-cert ~/.pgedge/certs/ca.crt \
  --client-cert ~/.pgedge/certs/client.crt \
  --client-key ~/.pgedge/certs/client.key
```

#### pgedge controlplane config view

**Usage:** `pgedge controlplane config view`

Show the resolved Control Plane connection

**Example:**

```
pgedge controlplane config view
pgedge controlplane config view -o yaml
```

### pgedge controlplane database

**Usage:** `pgedge controlplane database <command>`

Manage Control Plane databases

**Example:**

```
pgedge controlplane database list
pgedge controlplane database get storefront
pgedge controlplane database delete storefront --force
```

#### pgedge controlplane database create

**Usage:** `pgedge controlplane database create <database_id> [flags]`

Create a database from a spec file

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `-f, --file string` | No |  | Spec file path, or - for stdin (required) |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--tenant-id string` | No |  | Optional tenant ID override |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database create storefront -f spec.yaml --wait
```

#### pgedge controlplane database delete

**Usage:** `pgedge controlplane database delete <database_id> [flags]`

Delete a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--force-unmodifiable` | No |  | Delete even if the database is in an unmodifiable state |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database delete storefront
pgedge controlplane database delete storefront --force --wait
```

#### pgedge controlplane database get

**Usage:** `pgedge controlplane database get <database_id> [flags]`

Show database details

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--upgrades` | No |  | Also list the newer images this database can upgrade to |

**Example:**

```
pgedge controlplane database get storefront
pgedge controlplane database get storefront --upgrades
pgedge controlplane database get storefront -o yaml > spec.yaml
```

#### pgedge controlplane database init

**Usage:** `pgedge controlplane database init [flags]`

Generate a starter database spec

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-i, --interactive` | No |  | Interview for spec values instead of a blank template |
| `--nodes int` | No | `3` | Number of nodes to include in the template |

**Example:**

```
pgedge controlplane database init > spec.yaml
pgedge controlplane database init --nodes 3 | pgedge controlplane database create db -f -
pgedge controlplane database init -i > spec.yaml
```

#### pgedge controlplane database instance

**Usage:** `pgedge controlplane database instance <command>`

Control individual database instances

**Example:**

```
pgedge controlplane database instance list
pgedge controlplane database instance restart storefront storefront-n1-689qacsi
```

##### pgedge controlplane database instance list

**Usage:** `pgedge controlplane database instance list [flags]`

List database instances

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database string` | No |  | Database ID whose instances to show (default: all) |

**Example:**

```
pgedge controlplane database instance list
pgedge controlplane database instance list --database storefront
pgedge controlplane database instance list -o json
```

##### pgedge controlplane database instance restart

**Usage:** `pgedge controlplane database instance restart <database_id> <instance_id> [flags]`

Restart a database instance

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--scheduled-at string` | No |  | Schedule the restart at an RFC3339 time (default now) |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database instance restart storefront storefront-n1-689qacsi
pgedge controlplane database instance restart storefront storefront-n1-689qacsi \
  --force --scheduled-at 2026-07-17T22:00:00Z
```

##### pgedge controlplane database instance start

**Usage:** `pgedge controlplane database instance start <database_id> <instance_id> [flags]`

Start a database instance

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force-unmodifiable` | No |  | Start even if the database is in an unmodifiable state |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database instance start storefront storefront-n1-689qacsi
pgedge controlplane database instance start storefront storefront-n1-689qacsi --wait
```

##### pgedge controlplane database instance stop

**Usage:** `pgedge controlplane database instance stop <database_id> <instance_id> [flags]`

Stop a database instance

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--force-unmodifiable` | No |  | Stop even if the database is in an unmodifiable state |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database instance stop storefront storefront-n1-689qacsi
pgedge controlplane database instance stop storefront storefront-n1-689qacsi \
  --force --wait
```

#### pgedge controlplane database list

**Usage:** `pgedge controlplane database list`

List databases

**Example:**

```
pgedge controlplane database list
pgedge controlplane database list -o json
```

#### pgedge controlplane database node

**Usage:** `pgedge controlplane database node <command>`

Run per-node database operations

**Example:**

```
pgedge controlplane database node backup storefront n1 --type full
pgedge controlplane database node switchover storefront n1
```

##### pgedge controlplane database node backup

**Usage:** `pgedge controlplane database node backup <database_id> <node_name> [flags]`

Back up a database node

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force-unmodifiable` | No |  | Attempt the backup even in an unmodifiable state |
| `--type string` | No | `full` | Backup type: full, diff, or incr |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database node backup storefront n1 --type full
pgedge controlplane database node backup storefront n1 --type incr --wait
```

##### pgedge controlplane database node failover

**Usage:** `pgedge controlplane database node failover <database_id> <node_name> [flags]`

Fail over a database node

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--candidate string` | No |  | Instance ID of the replica to promote |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--skip-validation` | No |  | Skip health checks that block failover on a healthy cluster |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database node failover storefront n1
pgedge controlplane database node failover storefront n1 --force --wait
```

##### pgedge controlplane database node switchover

**Usage:** `pgedge controlplane database node switchover <database_id> <node_name> [flags]`

Switch over a database node

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--candidate string` | No |  | Instance ID of the replica candidate to promote |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--scheduled-at string` | No |  | Schedule the switchover at an RFC3339 time (default now) |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database node switchover storefront n1
pgedge controlplane database node switchover storefront n1 \
  --candidate storefront-n2-9ptayhma --force --wait
```

#### pgedge controlplane database restore

**Usage:** `pgedge controlplane database restore <database_id> <command> [flags]`

Restore a database from a spec file

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `-f, --file string` | No |  | Restore spec file path, or - for stdin (required) |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--force-unmodifiable` | No |  | Restore even if the database is in an unmodifiable state |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database restore storefront -f restore.yaml
pgedge controlplane database restore storefront -f restore.yaml --force --wait
```

##### pgedge controlplane database restore template

**Usage:** `pgedge controlplane database restore template [flags]`

Generate a starter restore spec

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-i, --interactive` | No |  | Interview for restore values instead of a blank template |

**Example:**

```
pgedge controlplane database restore template > restore.yaml
pgedge controlplane database restore template -i > restore.yaml
pgedge controlplane database restore template -i | pgedge controlplane database restore db -f -
```

#### pgedge controlplane database update

**Usage:** `pgedge controlplane database update <database_id> [flags]`

Update a database from a spec file

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `-f, --file string` | No |  | Spec file path, or - for stdin (required) |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database get storefront -o yaml > spec.yaml
pgedge controlplane database update storefront -f spec.yaml --wait
```

#### pgedge controlplane database upgrade

**Usage:** `pgedge controlplane database upgrade <database_id> [flags]`

Upgrade a database to a new image

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--image string` | No |  | Target container image reference (required) |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane database upgrade storefront --image pgedge/pgedge:16.4-1
pgedge controlplane database upgrade storefront --image pgedge/pgedge:16.4-1 \
  --force --wait
```

### pgedge controlplane doctor

**Usage:** `pgedge controlplane doctor`

Diagnose the Control Plane connection

**Example:**

```
pgedge controlplane doctor
pgedge controlplane doctor -o json
```

### pgedge controlplane host

**Usage:** `pgedge controlplane host <command>`

Manage Control Plane hosts

**Example:**

```
pgedge controlplane host list
pgedge controlplane host get host-1
```

#### pgedge controlplane host get

**Usage:** `pgedge controlplane host get <host_id>`

Show host details

**Example:**

```
pgedge controlplane host get host-1
pgedge controlplane host get host-1 -o yaml
```

#### pgedge controlplane host list

**Usage:** `pgedge controlplane host list`

List hosts

**Example:**

```
pgedge controlplane host list
pgedge controlplane host list -o json
```

#### pgedge controlplane host remove

**Usage:** `pgedge controlplane host remove <host_id> [flags]`

Remove a host from the cluster

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--force-lost` | No |  | Remove a permanently lost host even if instances exist or quorum would be violated (disaster recovery only) |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane host remove host-3
pgedge controlplane host remove host-3 --force
pgedge controlplane host remove host-3 --force --wait
```

### pgedge controlplane task

**Usage:** `pgedge controlplane task <command>`

Inspect Control Plane tasks

**Example:**

```
pgedge controlplane task list
pgedge controlplane task get --database my-db <task_id>
```

#### pgedge controlplane task cancel

**Usage:** `pgedge controlplane task cancel <task_id> [flags]`

Cancel a running database task

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database string` | No |  | Database ID that owns the task (required) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task log until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--host string` | No |  | Host ID (cancel is not supported for host tasks) |
| `--wait` | No |  | Wait for the task to reach a terminal state |
| `--wait-interval int` | No | `3` | Polling interval in seconds when --wait is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait is set (--follow is unbounded) |

**Example:**

```
pgedge controlplane task cancel --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
pgedge controlplane task cancel --database my-db \
  019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90 --force --wait
```

#### pgedge controlplane task get

**Usage:** `pgedge controlplane task get <task_id> [flags]`

Show a task's status

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database string` | No |  | Database ID that owns the task (required unless --host) |
| `--host string` | No |  | Host ID that owns the task (required unless --database) |

**Example:**

```
pgedge controlplane task get --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
pgedge controlplane task get --host host-1 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
```

#### pgedge controlplane task list

**Usage:** `pgedge controlplane task list [flags]`

List tasks

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database string` | No |  | List tasks for a single database |
| `--entity-id string` | No |  | Filter the global list by entity (database_id or host_id) |
| `--host string` | No |  | List tasks for a single host |
| `--limit int` | No |  | Maximum tasks to return |
| `--scope string` | No |  | Filter the global list by scope: database or host |

**Example:**

```
pgedge controlplane task list
pgedge controlplane task list --database my-db
pgedge controlplane task list --scope host --limit 20
```

#### pgedge controlplane task logs

**Usage:** `pgedge controlplane task logs <task_id> [flags]`

Show a task's log

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database string` | No |  | Database ID that owns the task (required unless --host) |
| `--host string` | No |  | Host ID that owns the task (required unless --database) |

**Example:**

```
pgedge controlplane task logs --database my-db 019783f4-7e21-7c3a-9f5e-2b1d4c6a8e90
```

### pgedge controlplane version

**Usage:** `pgedge controlplane version`

Show the Control Plane server version

**Example:**

```
pgedge controlplane version
pgedge controlplane version -o json
```

<!-- END GENERATED PAGE -->
