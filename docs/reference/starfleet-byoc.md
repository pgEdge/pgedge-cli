# pgedge starfleet byoc command reference

This page lists the usage, description and declared flags for every
command in the byoc sub-tree, which runs pgEdge databases in your own
cloud account, on clusters and nodes you create.

The byoc tree shares the starfleet module's one connection: the flags on
[pgedge starfleet](starfleet.md) and the login it describes apply here
unchanged. Global flags are declared on the root command and listed
on the [pgedge reference page](pgedge.md).

For workflows and behavioral detail, run `pgedge llms starfleet byoc`
against your installed binary.

<!-- BEGIN GENERATED PAGE: pgedge starfleet byoc -->

## pgedge starfleet byoc

**Usage:** `pgedge starfleet byoc <command>`

Manage pgEdge BYOC resources

**Example:**

```
pgedge starfleet auth login
pgedge starfleet byoc cluster list
```

### pgedge starfleet byoc backup

**Usage:** `pgedge starfleet byoc backup <command>`

Manage pgEdge BYOC backups

**Example:**

```
pgedge starfleet byoc backup create --database-id <database_id> \
  --provider pgbackrest
```

#### pgedge starfleet byoc backup create

**Usage:** `pgedge starfleet byoc backup create [flags]`

Create a backup

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database-id string` | Yes |  | Database to back up (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | No |  | Optional backup name |
| `--provider string` | Yes |  | Backup provider |
| `--target-nodes strings` | No |  | Comma-separated list of target nodes |
| `--type string` | No |  | Optional backup type |

**Example:**

```
pgedge starfleet byoc backup create --database-id <database_id> \
  --provider pgbackrest
pgedge starfleet byoc backup create --database-id <database_id> \
  --provider pgbackrest --type full --name nightly
```

### pgedge starfleet byoc backup-repository

**Usage:** `pgedge starfleet byoc backup-repository <command>`

Inspect pgEdge BYOC backup repositories

**Example:**

```
pgedge starfleet byoc backup-repository list --database-id <database_id>
pgedge starfleet byoc backup-repository get <repository_id> n1
```

#### pgedge starfleet byoc backup-repository get

**Usage:** `pgedge starfleet byoc backup-repository get <repository_id> <node_name> [flags]`

Show a backup repository's backups

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--descending` | No |  | Sort backups in descending order |
| `--limit int` | No |  | Maximum number of backups to return |
| `--offset int` | No |  | Offset into the backups for pagination |
| `--type string` | No |  | Filter backups by type: full, diff, or incr |

**Example:**

```
pgedge starfleet byoc backup-repository get <repository_id> n1
pgedge starfleet byoc backup-repository get <repository_id> n1 -o json
```

#### pgedge starfleet byoc backup-repository list

**Usage:** `pgedge starfleet byoc backup-repository list [flags]`

List backup repositories

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database-id string` | No |  | Filter to one database's repositories (full UUID) |
| `--descending` | No |  | Sort in descending order |
| `--limit int` | No |  | Maximum number of results to return (API default 10) |
| `--offset int` | No |  | Offset into the results for pagination |
| `--type string` | No |  | Filter by repository type (for example s3) |

**Example:**

```
pgedge starfleet byoc backup-repository list --limit 100
pgedge starfleet byoc backup-repository list --database-id <database_id>
```

### pgedge starfleet byoc backup-store

**Usage:** `pgedge starfleet byoc backup-store <command>`

Manage pgEdge BYOC backup stores

**Example:**

```
pgedge starfleet byoc backup-store list
pgedge starfleet byoc backup-store create --name store1 --cloud-account-id <account_id>
```

#### pgedge starfleet byoc backup-store create

**Usage:** `pgedge starfleet byoc backup-store create [flags]`

Create a backup store

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cloud-account-id string` | Yes |  | Cloud account to attach (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--name string` | Yes |  | Backup store name |
| `--region string` | No |  | Region for the backup store; cannot be changed later (omit to let the API choose) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc backup-store create --name store1 \
  --cloud-account-id <account_id>
pgedge starfleet byoc backup-store create --name store1 \
  --cloud-account-id <account_id> --region us-east-1 --wait
```

#### pgedge starfleet byoc backup-store delete

**Usage:** `pgedge starfleet byoc backup-store delete <backup_store_id> [flags]`

Delete a backup store

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
pgedge starfleet byoc backup-store delete <backup_store_id>
pgedge starfleet byoc backup-store delete <backup_store_id> --force --wait
```

#### pgedge starfleet byoc backup-store get

**Usage:** `pgedge starfleet byoc backup-store get <backup_store_id>`

Show backup store details

**Example:**

```
pgedge starfleet byoc backup-store get <backup_store_id> -o yaml
```

#### pgedge starfleet byoc backup-store list

**Usage:** `pgedge starfleet byoc backup-store list [flags]`

List backup stores

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--created-after string` | No |  | Filter: created after this RFC3339 timestamp |
| `--created-before string` | No |  | Filter: created before this RFC3339 timestamp |
| `--limit int` | No |  | Maximum number of results to return (API default 10) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet byoc backup-store list
pgedge starfleet byoc backup-store list --limit 20 -o json
```

### pgedge starfleet byoc cloud-account

**Usage:** `pgedge starfleet byoc cloud-account <command>`

Manage pgEdge BYOC provider accounts

**Example:**

```
pgedge starfleet byoc cloud-account list
pgedge starfleet byoc cloud-account create --type aws --role-arn <arn>
```

#### pgedge starfleet byoc cloud-account availability-zones

**Usage:** `pgedge starfleet byoc cloud-account availability-zones <cloud_account_id> [flags]`

List availability zones in a region

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--region string` | Yes |  | Provider region to list zones in (e.g. us-east-2) |

**Example:**

```
pgedge starfleet byoc cloud-account availability-zones \
  b0c1d2e3-f4a5-6789-bcde-890123456789 --region us-east-2
pgedge starfleet byoc cloud-account availability-zones \
  b0c1d2e3-f4a5-6789-bcde-890123456789 --region us-east-2 -o json
```

#### pgedge starfleet byoc cloud-account cloudformation-template

**Usage:** `pgedge starfleet byoc cloud-account cloudformation-template`

Show the AWS CloudFormation template URL

**Example:**

```
pgedge starfleet byoc cloud-account cloudformation-template
```

#### pgedge starfleet byoc cloud-account create

**Usage:** `pgedge starfleet byoc cloud-account create [flags]`

Create a cloud account

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--azure-client-id string` | No |  | Azure client/application ID (required for --type azure) |
| `--azure-client-secret string` | No |  | Azure client secret (required for --type azure) |
| `--description string` | No |  | Optional description |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | No |  | Display name for the cloud account |
| `--project-id string` | No |  | GCP project ID (required for --type gcp) |
| `--resource-group string` | No |  | Azure resource group (optional for --type azure) |
| `--role-arn string` | No |  | AWS IAM Role ARN (required for --type aws) |
| `--service-account string` | No |  | GCP service account email (required for --type gcp) |
| `--subscription-id string` | No |  | Azure subscription ID (required for --type azure) |
| `--tenant-id string` | No |  | Azure tenant ID (required for --type azure) |
| `--type string` | Yes |  | Cloud provider type: aws, azure, or gcp |

**Example:**

```
pgedge starfleet byoc cloud-account create --type aws --role-arn <arn>
pgedge starfleet byoc cloud-account create --type gcp \
  --project-id my-proj --service-account svc@my-proj.iam
```

#### pgedge starfleet byoc cloud-account delete

**Usage:** `pgedge starfleet byoc cloud-account delete <cloud_account_id> [flags]`

Delete a cloud account

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet byoc cloud-account delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc cloud-account delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
  --force
```

#### pgedge starfleet byoc cloud-account get

**Usage:** `pgedge starfleet byoc cloud-account get <cloud_account_id>`

Show cloud account details

**Example:**

```
pgedge starfleet byoc cloud-account get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc cloud-account get b0c1d2e3-f4a5-6789-bcde-890123456789 \
  -o yaml
```

#### pgedge starfleet byoc cloud-account list

**Usage:** `pgedge starfleet byoc cloud-account list`

List cloud accounts

**Example:**

```
pgedge starfleet byoc cloud-account list
pgedge starfleet byoc cloud-account list -o json
```

### pgedge starfleet byoc cluster

**Usage:** `pgedge starfleet byoc cluster <command>`

Manage pgEdge BYOC clusters

**Example:**

```
pgedge starfleet byoc cluster list
pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

#### pgedge starfleet byoc cluster create

**Usage:** `pgedge starfleet byoc cluster create [flags]`

Create a cluster

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--backup-store-id strings` | No |  | Backup store ID to attach (repeatable; required to host a DB) |
| `--cloud-account-id string` | Yes |  | Cloud account UUID; checked to exist before the cluster is created |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--firewall-rule stringArray` | No |  | Firewall rule (repeatable). name must be one of http, https, postgres, ssh. e.g. name=https,port=443,sources=0.0.0.0/0 |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--instance-type string` | No |  | Instance type for all nodes (shorthand for --node; creates one node per region) |
| `--name string` | Yes |  | Cluster name |
| `--network stringArray` | No |  | Network settings (repeatable, one per region). Keys: region, cidr, public-subnets, private-subnets, subnets, external, external-id, name. AWS and Azure use public-subnets and private-subnets; GCP uses subnets. e.g. region=us-east-1,cidr=10.4.0.0/16,public-subnets=10.4.1.0/24,private-subnets=10.4.128.0/24 |
| `--node stringArray` | No |  | Node settings (repeatable). e.g. name=n1,region=us-east-1,instance-type=r7g.medium,volume-size=30 |
| `--node-location string` | Yes |  | Node location: private or public |
| `--regions strings` | Yes |  | Comma-separated list of regions |
| `--volume-size int` | No |  | Volume size in GB for all nodes, 1 or more (shorthand for --node; creates one node per region) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc cluster create --name prod \
  --cloud-account-id <cloud_account_id> \
  --regions us-east-1 --node-location public \
  --instance-type r7g.medium --volume-size 30 \
  --backup-store-id <backup_store_id>
```

#### pgedge starfleet byoc cluster delete

**Usage:** `pgedge starfleet byoc cluster delete <cluster_id> [flags]`

Delete a cluster

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cascade` | No |  | Also delete all databases and cloud infrastructure, bypassing status and database-existence checks |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc cluster delete a1b2c3d4-e5f6-7890-abcd-ef1234567890
pgedge starfleet byoc cluster delete a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
  --cascade --force
```

#### pgedge starfleet byoc cluster get

**Usage:** `pgedge starfleet byoc cluster get <cluster_id>`

Show cluster details

**Example:**

```
pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890
pgedge starfleet byoc cluster get a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o yaml
```

#### pgedge starfleet byoc cluster list

**Usage:** `pgedge starfleet byoc cluster list [flags]`

List clusters

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--limit int` | No |  | Maximum number of results to return (API default 10) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet byoc cluster list
pgedge starfleet byoc cluster list --limit 20 -o json
```

#### pgedge starfleet byoc cluster metrics

**Usage:** `pgedge starfleet byoc cluster metrics <cluster_id> [flags]`

Read a cluster's host metrics

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--end-time string` | No |  | End of the window, as an RFC3339 timestamp |
| `--start-time string` | No |  | Start of the window, as an RFC3339 timestamp |

**Example:**

```
pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890
pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o json
pgedge starfleet byoc cluster metrics a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
  --start-time 2026-08-04T00:00:00Z --end-time 2026-08-04T01:00:00Z
```

#### pgedge starfleet byoc cluster share

**Usage:** `pgedge starfleet byoc cluster share <command>`

Manage cluster shares

**Example:**

```
pgedge starfleet byoc cluster share list <cluster_id>
pgedge starfleet byoc cluster share get <cluster_id> <share_id>
```

##### pgedge starfleet byoc cluster share create

**Usage:** `pgedge starfleet byoc cluster share create <cluster_id> [flags]`

Create a cluster share

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allowed-tenants strings` | No |  | Allowed tenant IDs (for allowlist tenancy) |
| `--capacity int` | No |  | Share capacity, 1 or more (omit to let the API choose) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | No |  | Share name |
| `--tenancy string` | No |  | Tenancy mode: same or allowlist |

**Example:**

```
pgedge starfleet byoc cluster share create <cluster_id> \
  --name team-a --capacity 2 --tenancy allowlist \
  --allowed-tenants tnt-1,tnt-2
```

##### pgedge starfleet byoc cluster share delete

**Usage:** `pgedge starfleet byoc cluster share delete <cluster_id> <share_id> [flags]`

Delete a cluster share

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet byoc cluster share delete <cluster_id> <share_id> --force
```

##### pgedge starfleet byoc cluster share get

**Usage:** `pgedge starfleet byoc cluster share get <cluster_id> <share_id>`

Show cluster share details

**Example:**

```
pgedge starfleet byoc cluster share get <cluster_id> <share_id> -o json
```

##### pgedge starfleet byoc cluster share list

**Usage:** `pgedge starfleet byoc cluster share list <cluster_id>`

List shares for a cluster

**Example:**

```
pgedge starfleet byoc cluster share list 3fa85f64-5717-4562-b3fc-2c963f66afa6
```

#### pgedge starfleet byoc cluster update

**Usage:** `pgedge starfleet byoc cluster update <cluster_id> [flags]`

Update a cluster's firewall rules or stores

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--backup-store-id strings` | No |  | Backup store ID to attach (repeatable) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--firewall-rule stringArray` | No |  | Firewall rule to append (repeatable). name must be one of http, https, postgres, ssh. e.g. name=https,port=443,sources=0.0.0.0/0 |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--regions strings` | No |  | Replace the cluster's regions (refused if it would strand a node or network) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc cluster update a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
  --firewall-rule name=https,port=443,sources=0.0.0.0/0
pgedge starfleet byoc cluster update a1b2c3d4-e5f6-7890-abcd-ef1234567890 \
  --backup-store-id f2a3b4c5-d6e7-8901-fabc-012345678901
```

### pgedge starfleet byoc config-version

**Usage:** `pgedge starfleet byoc config-version <command>`

Inspect pgEdge BYOC configuration versions

**Example:**

```
pgedge starfleet byoc config-version list
pgedge starfleet byoc config-version get 15.6.0
```

#### pgedge starfleet byoc config-version get

**Usage:** `pgedge starfleet byoc config-version get <version>`

Show configuration version details

**Example:**

```
pgedge starfleet byoc config-version get 15.6.0
pgedge starfleet byoc config-version get 15.6.0 -o yaml
```

#### pgedge starfleet byoc config-version list

**Usage:** `pgedge starfleet byoc config-version list`

List configuration versions

**Example:**

```
pgedge starfleet byoc config-version list
pgedge starfleet byoc config-version list -o json
```

### pgedge starfleet byoc database

**Usage:** `pgedge starfleet byoc database <command>`

Manage pgEdge BYOC databases

**Example:**

```
pgedge starfleet byoc database list
pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345
```

#### pgedge starfleet byoc database connection-string

**Usage:** `pgedge starfleet byoc database connection-string <database_id> [flags]`

Print a connection string for one node of a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--format string` | No | `uri` | Shape of the text output: uri or env |
| `--internal` | No |  | Use the node's internal_host, for a client inside the cluster |
| `--no-password` | No |  | Leave the password out of the string |
| `--node string` | No |  | Node whose connection to print (required with several nodes) |

**Example:**

```
pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345
pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --node n2 --no-password
pgedge starfleet byoc database connection-string f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --node n1 --internal --format env > .env
```

#### pgedge starfleet byoc database create

**Usage:** `pgedge starfleet byoc database create [flags]`

Create a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cluster-id string` | Yes |  | Cluster to deploy the database on (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--name string` | Yes |  | Database name |
| `--pg-version string` | No |  | Postgres version (e.g. 16) |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id>
pgedge starfleet byoc database create --name mydb --cluster-id <cluster_id> \
  --pg-version 16 --wait
```

#### pgedge starfleet byoc database delete

**Usage:** `pgedge starfleet byoc database delete <database_id> [flags]`

Delete a database

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
pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345
pgedge starfleet byoc database delete f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --force --wait
```

#### pgedge starfleet byoc database get

**Usage:** `pgedge starfleet byoc database get <database_id>`

Show database details

**Example:**

```
pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345
pgedge starfleet byoc database get f6a7b8c9-d0e1-2345-fabc-456789012345 -o yaml
```

#### pgedge starfleet byoc database inspect

**Usage:** `pgedge starfleet byoc database inspect <database_id> <analysis> [flags]`

Run a read-only diagnostic against one node of a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--internal` | No |  | Connect over the node's internal_host, from inside the cluster |
| `--node string` | No |  | Node to connect to (required with several nodes) |

**Example:**

```
pgedge starfleet byoc database inspect f6a7b8c9-d0e1-2345-fabc-456789012345 table-sizes
pgedge starfleet byoc database inspect f6a7b8c9-d0e1-2345-fabc-456789012345 \
  locks --node n2 --internal -o json
```

#### pgedge starfleet byoc database list

**Usage:** `pgedge starfleet byoc database list [flags]`

List databases

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cluster-id string` | No |  | Filter by cluster (full UUID) |
| `--limit int` | No |  | Maximum number of results to return (API default 10) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet byoc database list
pgedge starfleet byoc database list --cluster-id <cluster_id> -o json
```

#### pgedge starfleet byoc database logs

**Usage:** `pgedge starfleet byoc database logs <database_id> [flags]`

Read a database component's logs

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--component-name string` | Yes |  | Component to read logs from, such as postgres |
| `--max-lines int` | No |  | Maximum number of log lines to return (API default 100) |
| `--nodes string` | Yes |  | Comma-separated node names to read logs from |

**Example:**

```
pgedge starfleet byoc database logs f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --component-name postgres --nodes n1
pgedge starfleet byoc database logs f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --component-name postgres \
  --nodes n1,n2 --max-lines 500
```

#### pgedge starfleet byoc database mcp

**Usage:** `pgedge starfleet byoc database mcp <command>`

Manage the MCP server on a database

**Example:**

```
pgedge starfleet byoc database mcp deploy <database_id>
pgedge starfleet byoc database mcp update <database_id> --allow-writes
```

##### pgedge starfleet byoc database mcp deploy

**Usage:** `pgedge starfleet byoc database mcp deploy <database_id> [flags]`

Deploy an MCP server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allow-writes` | No |  | Grant the MCP service read-write access (WARNING: allows LLM to modify data) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-api-key string` | No |  | API key for the embedding provider (required for openai and voyage) |
| `--embedding-model string` | No |  | Embedding model identifier (required when --embedding-provider is set) |
| `--embedding-provider string` | No |  | Embedding provider: ollama, openai, or voyage |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--init-tokens string` | No |  | Bearer token forwarded to the MCP server as INIT_TOKENS |
| `--init-users string` | No |  | Comma-separated username:password pairs forwarded as INIT_USERS |
| `--ollama-url string` | No |  | Endpoint URL for an Ollama server (required when --embedding-provider is ollama) |
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database mcp deploy <database_id>
pgedge starfleet byoc database mcp deploy <database_id> \
  --embedding-provider openai --embedding-model text-embedding-3-small \
  --embedding-api-key sk-... --wait
```

##### pgedge starfleet byoc database mcp update

**Usage:** `pgedge starfleet byoc database mcp update <database_id> [flags]`

Update the MCP server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--allow-writes` | No |  | Grant the MCP service read-write access (WARNING: allows LLM to modify data) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-api-key string` | No |  | API key for the embedding provider (required for openai and voyage) |
| `--embedding-model string` | No |  | Embedding model identifier (required when --embedding-provider is set) |
| `--embedding-provider string` | No |  | Embedding provider: ollama, openai, or voyage |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--init-tokens string` | No |  | Bearer token forwarded to the MCP server as INIT_TOKENS |
| `--init-users string` | No |  | Comma-separated username:password pairs forwarded as INIT_USERS |
| `--ollama-url string` | No |  | Endpoint URL for an Ollama server (required when --embedding-provider is ollama) |
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database mcp update <database_id> --allow-writes
pgedge starfleet byoc database mcp update <database_id> --allow-writes=false
```

#### pgedge starfleet byoc database metrics

**Usage:** `pgedge starfleet byoc database metrics <database_id> [flags]`

Read a database's metrics

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--columns string` | No |  | Comma-separated metric names to return |
| `--interval string` | No |  | How far back to read, as value,unit; omitted, the API reads 15 minutes |
| `--node-name string` | No |  | Only metrics for this node name |

**Example:**

```
pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345
pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --columns pg_database_size_bytes
pgedge starfleet byoc database metrics f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --interval 5,minute -o json
```

#### pgedge starfleet byoc database postgrest

**Usage:** `pgedge starfleet byoc database postgrest <command>`

Manage the PostgREST API on a database

**Example:**

```
pgedge starfleet byoc database postgrest deploy <database_id> \
  --db-schemas public --db-anon-role web_anon
pgedge starfleet byoc database postgrest update <database_id> --max-rows 500
```

##### pgedge starfleet byoc database postgrest deploy

**Usage:** `pgedge starfleet byoc database postgrest deploy <database_id> [flags]`

Deploy a PostgREST API on a database

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
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database postgrest deploy <database_id> \
  --db-schemas public --db-anon-role web_anon
pgedge starfleet byoc database postgrest deploy <database_id> \
  --db-schemas public,api --db-anon-role web_anon \
  --max-rows 500 --db-pool 20 --wait
```

##### pgedge starfleet byoc database postgrest update

**Usage:** `pgedge starfleet byoc database postgrest update <database_id> [flags]`

Update the PostgREST API on a database

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
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database postgrest update <database_id> --max-rows 500
pgedge starfleet byoc database postgrest update <database_id> \
  --db-schemas public,api --wait
```

#### pgedge starfleet byoc database rag

**Usage:** `pgedge starfleet byoc database rag <command>`

Manage the RAG server on a database

**Example:**

```
pgedge starfleet byoc database rag deploy <database_id> \
  --embedding-llm-provider openai \
  --embedding-llm-model text-embedding-3-small \
  --embedding-llm-api-key "$OPENAI_API_KEY" \
  --completion-llm-provider openai \
  --completion-llm-model gpt-4o \
  --completion-llm-api-key "$OPENAI_API_KEY" \
  --pipeline-config pipelines.json
pgedge starfleet byoc database rag update <database_id> --top-n 10
```

##### pgedge starfleet byoc database rag deploy

**Usage:** `pgedge starfleet byoc database rag deploy <database_id> [flags]`

Deploy a RAG server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--completion-llm-api-key string` | Yes |  | API key for the completion LLM provider |
| `--completion-llm-model string` | Yes |  | Completion LLM model identifier |
| `--completion-llm-provider string` | Yes |  | Completion LLM provider (e.g. openai) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-llm-api-key string` | Yes |  | API key for the embedding LLM provider |
| `--embedding-llm-model string` | Yes |  | Embedding LLM model identifier |
| `--embedding-llm-provider string` | Yes |  | Embedding LLM provider (openai or anthropic) |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--pipeline-config string` | Yes |  | Path to a JSON file containing pipeline definitions |
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--token-budget int` | No |  | Default max completion tokens across all pipelines |
| `--top-n int` | No |  | Default number of results to retrieve per pipeline |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database rag deploy <database_id> \
  --embedding-llm-provider openai \
  --embedding-llm-model text-embedding-3-small \
  --embedding-llm-api-key sk-... \
  --completion-llm-provider openai --completion-llm-model gpt-4o \
  --completion-llm-api-key sk-... --pipeline-config pipelines.json
```

##### pgedge starfleet byoc database rag update

**Usage:** `pgedge starfleet byoc database rag update <database_id> [flags]`

Update the RAG server on a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--completion-llm-api-key string` | No |  | API key for the completion LLM provider |
| `--completion-llm-model string` | No |  | Completion LLM model identifier |
| `--completion-llm-provider string` | No |  | Completion LLM provider (e.g. openai) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--embedding-llm-api-key string` | No |  | API key for the embedding LLM provider |
| `--embedding-llm-model string` | No |  | Embedding LLM model identifier |
| `--embedding-llm-provider string` | No |  | Embedding LLM provider (openai or anthropic) |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--pipeline-config string` | No |  | Path to a JSON file containing pipeline definitions |
| `--target-nodes strings` | No |  | Node names to deploy on (e.g. n1,n2). Auto-selects if cluster has one node |
| `--token-budget int` | No |  | Default max completion tokens across all pipelines |
| `--top-n int` | No |  | Default number of results to retrieve per pipeline |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database rag update <database_id> --top-n 10
pgedge starfleet byoc database rag update <database_id> \
  --pipeline-config pipelines.json --wait
```

#### pgedge starfleet byoc database restore

**Usage:** `pgedge starfleet byoc database restore <database_id> [flags]`

Restore a database from a backup

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--delta` | No |  | pgBackRest delta restore: only replace files that differ |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--force` | No |  | Skip the confirmation prompt |
| `--node-name string` | Yes |  | Node whose backup is restored |
| `--pgbackrest-force` | No |  | pgBackRest force option (not the confirmation --force) |
| `--provider string` | No | `pgbackrest` | Backup provider to restore from |
| `--repository stringArray` | Yes |  | Backup repository UUID from backup-repository list (repeatable) |
| `--set string` | No |  | pgBackRest backup set to restore, for example 20240619-195803F |
| `--target string` | No |  | pgBackRest recovery target, paired with --type; an RFC3339 time with offset for --type time |
| `--target-exclusive` | No |  | Stop recovery before the target rather than including it |
| `--target-nodes strings` | No |  | Nodes to restore onto (comma-separated or repeatable; defaults to all nodes) |
| `--type string` | No |  | pgBackRest recovery type, for example time or immediate |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc database restore f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --node-name n1 --repository 9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d
pgedge starfleet byoc database restore f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --node-name n1 --repository 9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d \
  --type time --target "2024-06-19T20:00:00Z" --force
```

#### pgedge starfleet byoc database rotate-password

**Usage:** `pgedge starfleet byoc database rotate-password <database_id> [flags]`

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
pgedge starfleet byoc database rotate-password f6a7b8c9-d0e1-2345-fabc-456789012345 --role app
pgedge starfleet byoc database rotate-password f6a7b8c9-d0e1-2345-fabc-456789012345 --role admin --force
```

#### pgedge starfleet byoc database service

**Usage:** `pgedge starfleet byoc database service <command>`

Manage services deployed on a database

**Example:**

```
pgedge starfleet byoc database service list <database_id>
pgedge starfleet byoc database service get <database_id> <service_id>
```

##### pgedge starfleet byoc database service get

**Usage:** `pgedge starfleet byoc database service get <database_id> <service_id>`

Show details of a service

**Example:**

```
pgedge starfleet byoc database service get <database_id> <service_id> -o json
```

##### pgedge starfleet byoc database service list

**Usage:** `pgedge starfleet byoc database service list <database_id>`

List services deployed on a database

**Example:**

```
pgedge starfleet byoc database service list <database_id>
pgedge starfleet byoc database service list <database_id> -o json
```

##### pgedge starfleet byoc database service remove

**Usage:** `pgedge starfleet byoc database service remove <database_id> <type> [flags]`

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
pgedge starfleet byoc database service remove <database_id> mcp
pgedge starfleet byoc database service remove <database_id> postgrest --force
```

#### pgedge starfleet byoc database update

**Usage:** `pgedge starfleet byoc database update <database_id> [flags]`

Update a database

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--display-name string` | No |  | Display name for the database, at most 25 characters |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--options strings` | No |  | Comma-separated list of options |

**Example:**

```
pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --display-name "My Database"
pgedge starfleet byoc database update f6a7b8c9-d0e1-2345-fabc-456789012345 \
  --options key1,key2
```

### pgedge starfleet byoc ingress

**Usage:** `pgedge starfleet byoc ingress <command>`

Manage pgEdge BYOC ingresses

**Example:**

```
pgedge starfleet byoc ingress list
pgedge starfleet byoc ingress create --name web --cluster-id <id> \
  --region us-east-1
```

#### pgedge starfleet byoc ingress create

**Usage:** `pgedge starfleet byoc ingress create [flags]`

Create an ingress

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--cluster-id string` | Yes |  | Cluster to associate with the ingress (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--follow` | No |  | Stream the task's step messages until it reaches a terminal state |
| `--name string` | Yes |  | Ingress name |
| `--region string` | Yes |  | Cloud region for the ingress |
| `--wait` | No |  | Wait for the operation's task to reach a terminal state |
| `--wait-interval int` | No | `5` | Polling interval in seconds when --wait/--follow is set |
| `--wait-timeout int` | No | `600` | Max seconds to wait when --wait/--follow is set |

**Example:**

```
pgedge starfleet byoc ingress create --name web --cluster-id <id> \
  --region us-east-1
pgedge starfleet byoc ingress create --name web --cluster-id <id> \
  --region us-east-1 --wait
```

#### pgedge starfleet byoc ingress delete

**Usage:** `pgedge starfleet byoc ingress delete <ingress_id> [flags]`

Delete an ingress

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
pgedge starfleet byoc ingress delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc ingress delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
  --force --wait
```

#### pgedge starfleet byoc ingress get

**Usage:** `pgedge starfleet byoc ingress get <ingress_id>`

Show ingress details

**Example:**

```
pgedge starfleet byoc ingress get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc ingress get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
```

#### pgedge starfleet byoc ingress list

**Usage:** `pgedge starfleet byoc ingress list [flags]`

List ingresses

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--created-after string` | No |  | Filter: created after this RFC3339 timestamp |
| `--created-before string` | No |  | Filter: created before this RFC3339 timestamp |
| `--limit int` | No |  | Maximum number of results to return (API default 10) |
| `--offset int` | No |  | Offset into the results for pagination |

**Example:**

```
pgedge starfleet byoc ingress list
pgedge starfleet byoc ingress list --limit 20 -o json
```

#### pgedge starfleet byoc ingress service

**Usage:** `pgedge starfleet byoc ingress service <command>`

Manage services registered on an ingress

**Example:**

```
pgedge starfleet byoc ingress service list <ingress_id>
pgedge starfleet byoc ingress service register <ingress_id> \
  --database-id <id> --service-id <id>
```

##### pgedge starfleet byoc ingress service deregister

**Usage:** `pgedge starfleet byoc ingress service deregister <ingress_id> <service_id> [flags]`

Deregister a service from an ingress

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet byoc ingress service deregister <ingress_id> <service_id>
pgedge starfleet byoc ingress service deregister <ingress_id> <service_id> \
  --force
```

##### pgedge starfleet byoc ingress service list

**Usage:** `pgedge starfleet byoc ingress service list <ingress_id>`

List services registered on an ingress

**Example:**

```
pgedge starfleet byoc ingress service list <ingress_id>
pgedge starfleet byoc ingress service list <ingress_id> -o json
```

##### pgedge starfleet byoc ingress service register

**Usage:** `pgedge starfleet byoc ingress service register <ingress_id> [flags]`

Register a service on an ingress

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--database-id string` | Yes |  | Database to register (full UUID) |
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--service-id string` | Yes |  | Service ID to expose |

**Example:**

```
pgedge starfleet byoc ingress service register <ingress_id> \
  --database-id <id> --service-id <id>
```

### pgedge starfleet byoc node

**Usage:** `pgedge starfleet byoc node <command>`

Inspect cluster nodes

**Example:**

```
pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890
pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 postgresql
```

#### pgedge starfleet byoc node list

**Usage:** `pgedge starfleet byoc node list <cluster_id>`

List a cluster's nodes

**Example:**

```
pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890
pgedge starfleet byoc node list a1b2c3d4-e5f6-7890-abcd-ef1234567890 -o json
```

#### pgedge starfleet byoc node logs

**Usage:** `pgedge starfleet byoc node logs <cluster_id> <node> <log_name> [flags]`

Read a journald log from a cluster node

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--case-sensitive` | No |  | Make --grep case-sensitive |
| `--dmesg` | No |  | Show only kernel entries |
| `--grep string` | No |  | Only entries whose message matches this regular expression |
| `--lines int` | No |  | Maximum number of log lines to return |
| `--priority string` | No |  | Only entries at this journald priority, such as err |
| `--reverse` | No |  | Show the newest entries first |
| `--since string` | No |  | Only entries at or after this RFC3339 timestamp |
| `--until string` | No |  | Only entries at or before this RFC3339 timestamp |

**Example:**

```
pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 system
pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 docker \
  --lines 200 --reverse
pgedge starfleet byoc node logs a1b2c3d4-e5f6-7890-abcd-ef1234567890 n1 system \
  --priority err
```

### pgedge starfleet byoc ssh-key

**Usage:** `pgedge starfleet byoc ssh-key <command>`

Manage pgEdge BYOC SSH keys

**Example:**

```
pgedge starfleet byoc ssh-key list
pgedge starfleet byoc ssh-key create --name laptop \
  --public-key "$(cat ~/.ssh/id_ed25519.pub)"
```

#### pgedge starfleet byoc ssh-key create

**Usage:** `pgedge starfleet byoc ssh-key create [flags]`

Create an SSH key

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--name string` | Yes |  | SSH key name |
| `--public-key string` | Yes |  | SSH public key in authorized_keys form (e.g. "ssh-ed25519 AAAA... comment") |

**Example:**

```
pgedge starfleet byoc ssh-key create --name laptop \
  --public-key "$(cat ~/.ssh/id_ed25519.pub)"
```

#### pgedge starfleet byoc ssh-key delete

**Usage:** `pgedge starfleet byoc ssh-key delete <ssh_key_id> [flags]`

Delete an SSH key

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--dry-run string[="checks"]` | No |  | Run every client-side check, then stop before sending the write and report the request that would have been sent. Checks that read the API do run, so this needs credentials; the server validates nothing until the real write |
| `--force` | No |  | Skip the confirmation prompt |

**Example:**

```
pgedge starfleet byoc ssh-key delete b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc ssh-key delete b0c1d2e3-f4a5-6789-bcde-890123456789 \
  --force
```

#### pgedge starfleet byoc ssh-key get

**Usage:** `pgedge starfleet byoc ssh-key get <ssh_key_id>`

Show SSH key details

**Example:**

```
pgedge starfleet byoc ssh-key get b0c1d2e3-f4a5-6789-bcde-890123456789
pgedge starfleet byoc ssh-key get b0c1d2e3-f4a5-6789-bcde-890123456789 -o yaml
```

#### pgedge starfleet byoc ssh-key list

**Usage:** `pgedge starfleet byoc ssh-key list`

List SSH keys

**Example:**

```
pgedge starfleet byoc ssh-key list
pgedge starfleet byoc ssh-key list -o json
```

### pgedge starfleet byoc task

**Usage:** `pgedge starfleet byoc task <command>`

Inspect pgEdge BYOC tasks

**Example:**

```
pgedge starfleet byoc task list --subject-id <cluster_id>
pgedge starfleet byoc task wait <task_id>
```

#### pgedge starfleet byoc task get

**Usage:** `pgedge starfleet byoc task get <task_id>`

Show task details

**Example:**

```
pgedge starfleet byoc task get <task_id> -o yaml
```

#### pgedge starfleet byoc task list

**Usage:** `pgedge starfleet byoc task list [flags]`

List tasks

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--limit int` | No |  | Maximum number of results to return (API default 25) |
| `--name string` | No |  | Filter by task name (e.g. update, delete) |
| `--offset int` | No |  | Offset into the results for pagination |
| `--status string` | No |  | Filter by status (queued, running, succeeded, failed) |
| `--subject-id string` | No |  | Filter by subject (full UUID) |
| `--subject-kind string` | No |  | Filter by subject kind |

**Example:**

```
pgedge starfleet byoc task list
pgedge starfleet byoc task list --subject-id <cluster_id> --status failed
```

#### pgedge starfleet byoc task wait

**Usage:** `pgedge starfleet byoc task wait <task_id> [flags]`

Wait for a task to complete

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--follow` | No |  | Stream the task's step messages instead of bare status lines |
| `--wait-interval int` | No | `5` | Polling interval in seconds |
| `--wait-timeout int` | No | `600` | Maximum seconds to wait for task completion |

**Example:**

```
pgedge starfleet byoc task wait <task_id> --wait-timeout 600
pgedge starfleet byoc task wait <task_id> --follow
```

<!-- END GENERATED PAGE -->
