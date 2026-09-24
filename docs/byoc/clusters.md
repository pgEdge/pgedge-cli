# Manage BYOC clusters

A BYOC cluster is the infrastructure a database runs on: a set of
nodes, spread across one or more regions, built inside your own cloud
account. Databases belong to a cluster, backups go to backup stores
attached to a cluster, and every network and firewall decision is made
at cluster level rather than per database.

This document covers the cluster resource itself. The
[Provision a BYOC cluster and database](provision.md) document
walks a first cluster and database end to end, and the
[Register a BYOC cloud account](cloud-accounts.md) document covers
the account a cluster is built in.

## The cluster model

You describe a cluster entirely at creation time, and only three parts
of that description can change afterward.

The following table describes each part of a cluster and whether an
update can reach it:

| Part | Flag | Changeable later |
|---|---|---|
| Regions | `--regions` | Yes, by replacement |
| Nodes | `--node`, or `--instance-type` and `--volume-size` | No |
| Networks | `--network` | No |
| Firewall rules | `--firewall-rule` | Yes, by appending |
| Backup stores | `--backup-store-id` | Yes, by appending |
| Node location | `--node-location` | No |

A cluster created without a backup store provisions cleanly and then
cannot host a database, because the database create call needs a
pgBackRest repository to write to. The CLI warns you at create time
and lets the call through, so the create-then-attach path stays open.

Every ID a cluster command takes is a full UUID, checked locally. A
malformed ID is exit 2 with nothing sent, and an ID that names nothing
is exit 4. Node names are the exception: nodes are named, not
identified, and `node logs` accepts either the name or the UUID.

### Node location and reachability

`--node-location` takes `public` or `private`, and the CLI checks the
value against the contract's enum before it sends anything. Anything
else is exit 2.

Public clusters get external hostnames automatically, so a service
deployed on a database there is reachable once it is running. Private
clusters have no external address of their own, and reaching a service
on one takes a regional ingress plus a service registration behind it.
Choose the location deliberately, because it is fixed for the life of
the cluster.

### Networks and subnets

`--network` is a repeatable structured flag, one value per region,
written as comma-separated `key=value` pairs. On a single-region
cluster the `region=` key may be omitted. An unknown key is a usage
error at exit 2 rather than a setting the CLI quietly drops.

The following table describes the keys `--network` accepts:

| Key | Meaning |
|---|---|
| `region` | The region this network describes |
| `cidr` | The VPC CIDR block |
| `public-subnets` | Public subnet CIDRs, on AWS and Azure |
| `private-subnets` | Private subnet CIDRs, on AWS and Azure |
| `subnets` | Subnet CIDRs, on GCP |
| `external` | True when attaching to a VPC you already own |
| `external-id` | The existing VPC's provider ID |
| `name` | A name for the network |

The subnet keys are per cloud and the wrong one is refused
server-side. AWS and Azure take `public-subnets` and
`private-subnets`. GCP takes `subnets` on public and private clusters
alike, and the API rejects `private_subnets` on a Google cluster with
an instruction to use `subnets` instead, so never send both spellings.

A private cluster on AWS or Azure needs private subnets, and the CLI
refuses a `--network` value that sets `public-subnets` with no
`private-subnets` under `--node-location private`. That refusal is
exit 2, before any request. The check reads the shape of what you
typed rather than the cloud you are aiming at: it leaves alone any
network that carries no `public-subnets`, which is the GCP shape, and
it leaves an omitted `--network` alone, because the API fills in its
own defaults when you send none. A GCP entry that sets
`public-subnets` anyway is refused like any other.

The `external`, `external-id` and `name` keys attach a cluster to a
VPC that already exists in your account rather than one pgEdge
creates. The CLI passes all three through without interpretation,
because which combinations are meaningful is per-cloud knowledge the
API owns.

Pick a CIDR that does not overlap another cluster in the same account.

### Firewall rules

`--firewall-rule` is repeatable and structured the same way, and both
`name` and `port` are required. The list-valued keys repeat to add
elements rather than taking a comma-separated list, because commas
already separate the pairs.

The following table describes the keys `--firewall-rule` accepts:

| Key | Meaning |
|---|---|
| `name` | One of `http`, `https`, `postgres`, `ssh` |
| `port` | The port the rule opens, as an integer |
| `sources` | A source CIDR, repeatable |
| `prefix-lists` | A provider prefix list, repeatable |
| `security-groups` | A provider security group, repeatable |

The name is a rule type the API recognizes, not a label of your
choosing. The CLI holds the four valid names locally so a typo is a
usage error at exit 2 instead of an opaque 400.

### Nodes and volumes

`--node` is repeatable and takes `name`, `region`, `instance-type`,
`volume-size`, `volume-iops`, `volume-type` and `availability-zone`.
On a single-region cluster the `region=` key may be omitted.

The `--instance-type` and `--volume-size` shorthand is the alternative
for the simple case. It creates one node per region, named `n1`, `n2`
and so on in the order the regions were given, and those names are
what later commands target. Pass either the shorthand or `--node`,
never both, because giving both is a usage error.

A volume must be 1 GB or more, in both spellings. The CLI refuses zero
and negatives at exit 2, and this check has no server-side twin: sent
a negative size the API substitutes an undocumented 100 GB default and
answers 200, provisioning a volume nobody asked for. Omit the size to
let the API choose, which is a different request from asking for zero.
There is no upper bound.

Neither `--instance-type` nor `--regions` is checked against anything,
so a value your provider does not serve is refused only by the API, on
the real call. `--name` is not checked either, unlike a database name.

## Create a cluster

`cluster create` runs its client-side checks first, then confirms the
cloud account exists, then provisions. The response is the cluster
itself rather than a task wrapper, so the ID you need comes straight
back.

Create a single-region public cluster with one node:

    pgedge starfleet byoc cluster create \
        --name prod \
        --cloud-account-id <cloud-account-id> \
        --regions us-east-1 \
        --node-location public \
        --backup-store-id <backup-store-id> \
        --node name=n1,instance-type=r7g.medium,volume-size=30 \
        --network cidr=10.4.0.0/16,public-subnets=10.4.1.0/24 \
        --firewall-rule name=postgres,port=5432,sources=0.0.0.0/0 \
        --wait --wait-timeout 3600

A private cluster on AWS or Azure adds the private subnets the check
above insists on:

    pgedge starfleet byoc cluster create \
        --name prod-private \
        --cloud-account-id <cloud-account-id> \
        --regions us-east-1 \
        --node-location private \
        --backup-store-id <backup-store-id> \
        --node name=n1,instance-type=r7g.medium,volume-size=30 \
        --network cidr=10.3.0.0/16,public-subnets=10.3.1.0/24,private-subnets=10.3.128.0/24 \
        --firewall-rule name=postgres,port=5432,sources=0.0.0.0/0

The same cluster on GCP replaces both subnet keys with one:

    pgedge starfleet byoc cluster create \
        --name prod-gcp \
        --cloud-account-id <cloud-account-id> \
        --regions us-central1 \
        --node-location private \
        --backup-store-id <backup-store-id> \
        --node name=n1,instance-type=n2-standard-2,volume-size=30 \
        --network cidr=10.6.0.0/16,subnets=10.6.1.0/24 \
        --firewall-rule name=postgres,port=5432,sources=0.0.0.0/0

In text output the confirmation goes to stderr and names the new
cluster's ID and status. Under `-o json` the cluster record goes to
stdout, which is where a script should read the ID from.

### Dry runs on cluster create

`cluster create --dry-run` checks `--node-location` against the enum,
`--cloud-account-id` as a UUID that names an account that exists, the
backup store IDs as UUIDs, the firewall rule names, the volume size,
the key names inside `--node` and `--network`, and the private-subnet
requirement.

It does not check `--name`, `--regions` or `--instance-type`, and it
performs no server-side validation of anything, so a clean dry run
means those checks passed and nothing more. The account-existence
check costs a read, so a dry run with no credentials configured is
exit 5 rather than a pass. The [dry runs](../dry-run.md) guide covers
the contract across all three modules.

## Waiting on cluster tasks

Cluster create, update and delete each spawn a background task, and
all three take `--wait`, `--follow`, `--wait-timeout` (600 seconds by
default) and `--wait-interval` (5 seconds by default). Without either
of the first two the command returns when the API accepts the request,
not when the work finishes.

`--wait` blocks until the task reaches a terminal state, printing a
status line per poll. `--follow` blocks the same way and streams the
task's step messages instead. Both exit 0 when the task succeeded, 1
when it failed and 3 when the deadline passed.

The wait tracks the task, not the resource, so a successful wait tells
you the work finished rather than that the cluster is serving. Confirm
with a read:

    pgedge starfleet byoc cluster get <cluster-id> -o json

Without a wait flag, text output prints a `Monitor with:` line on
stderr naming the `task list --subject-id` command for that resource.
Machine output stays silent so stdout remains parseable. The tasks and
async operations guide covers task inspection, which is where a failed
create explains itself.

Metadata commands are synchronous and have no wait flags at all. That
covers cloud accounts, SSH keys and cluster shares.

## Cluster statuses

A cluster's `.status` field is what tells you whether the cluster is
usable. Services embedded in a database report `.state` instead, with
a different vocabulary, so read the field name before the value.

The following table describes the statuses a cluster reports:

| Status | Meaning |
|---|---|
| `available` | Ready, and the only status a database create expects |
| `queued` | Accepted, waiting to start |
| `creating` | Provisioning is under way |
| `modifying` | An update is being applied |
| `deleting` | Teardown is under way |
| `failed` | The work failed and the cluster is not usable |
| `degraded` | Terminal, and it will not recover on its own |

A resource is ready at `available` and never at "active". Stop polling
when you see `degraded` and report it, because nothing further
happens on its own.

## Update a cluster

`cluster update` changes three things and refuses to run with none of
them: firewall rules, backup stores and regions. Omitting all three is
exit 1, and an explicitly empty `--backup-store-id` is exit 2 rather
than a silently ignored flag.

Firewall rules and backup stores append. The CLI reads the cluster,
adds what you passed to what is already there, and sends the whole
set, so existing rules and stores survive:

    pgedge starfleet byoc cluster update <cluster-id> \
        --firewall-rule name=https,port=443,sources=0.0.0.0/0

Regions replace. Pass every region the cluster needs, not just the new
one. Because the update sends the cluster's nodes and networks
alongside its regions, and has no flag for either, dropping a region
would send a body that contradicts itself. The CLI refuses a
`--regions` value that strands a node or a network at exit 2, naming
what blocks it. Adding a region, resending the current set, and
dropping an empty region all work normally.

## Delete a cluster

Deleting is destructive, so it prompts unless you pass `--force`, and
by default it refuses a cluster that still hosts databases. Tear one
down and block until the teardown finishes:

    pgedge starfleet byoc cluster delete <cluster-id> --force --wait

`--cascade` changes the meaning of the command. It deletes the
cluster's databases and its cloud infrastructure along with the
cluster, and it bypasses the status and database-existence checks that
would otherwise stop you. There is no undo and no confirmation beyond
the prompt, which itself is skipped by `--force`. Reserve the two
flags together for infrastructure you are certain is disposable.

## Cluster shares

A share hands a slice of a cluster's capacity to another tenant. The commands
all sit under `cluster share`, which carries list, get, create and delete, and
every one of them is synchronous.

Create a share with a tenancy mode and a capacity:

    pgedge starfleet byoc cluster share create <cluster-id> \
        --name team-a --capacity 10 --tenancy same

`--tenancy` takes `same` or `allowlist`. With `allowlist`, name the
permitted tenants with `--allowed-tenants`. `--capacity` must be 1 or
more, and omitting it lets the API choose. Share list and get print
ID, name, status, tenancy, capacity and creation time. Deleting a
share prompts unless you pass `--force`, and takes both the cluster ID
and the share ID.

## Nodes, logs and metrics

`node list <cluster-id>` prints each node's ID, name, region, instance
type and IP address. Those node names are what `node logs` and the
various node-targeting flags expect.

`node logs <cluster-id> <node> <log-name>` reads one journald log from
one node, and accepts either a node name or a node UUID. The log name
is a journald selector the API does not validate: every name answers
200, and an unrecognized one returns the same "no entries" marker as a
real log with nothing in it. `system`, `docker` and `containerd`
return entries, `postgresql`, `pgedge`, `patroni` and `messages`
return the empty marker, and `postgres` answers 500. Read Postgres's
own log with `database logs` instead.

`--lines`, `--priority`, `--reverse` and `--dmesg` work on node logs.
`--grep`, `--since` and `--until` are refused server-side with a 500,
even against a log that returns entries without them. The CLI sends
them unchanged.

`cluster metrics <cluster-id>` reads host metrics for the cluster's
nodes: CPU, memory, disk, network and process counts. Text output
gives one row per metric per node with the current value, its unit and
how many samples the series holds. Choose `-o json` or `-o yaml` for
the samples themselves. `--start-time` and `--end-time` narrow the
window and take RFC3339 timestamps, which is the only format the API
accepts.

## Listing and paging

`cluster list` is the only cluster-related list with paging flags. It
takes `--limit` and `--offset`, defaults to the server's page of 10,
and the server clamps any limit above 100 without saying so, which
means `--limit 500` returning 100 rows tells you nothing about how
many clusters exist. When a page comes back full, the CLI prints a
truncation hint on stderr saying there may be more. The API reports no
total, so the hint can only ever say "there may be more", never "there
are".

`--limit 0` and negative values are usage errors at exit 2 with
nothing sent. `node list`, `cluster share list` and the account-level
lists take no paging flags at all and always return the whole set.
The [output formats and paging](../output-and-paging.md) guide covers
the contract across modules.

## Next steps

- The [Register a BYOC cloud account](cloud-accounts.md) document
  covers the account, availability zones and SSH keys.
- The [Provision a BYOC cluster and database](provision.md)
  document walks a first cluster and database end to end.
- The [Rotate credentials](rotate-credentials.md) document covers the
  built-in role passwords on a database once the cluster is running.
- The [starfleet byoc command reference](../reference/starfleet-byoc.md)
  lists every flag on the commands above.
