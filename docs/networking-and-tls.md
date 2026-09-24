# Networking and TLS

Two questions decide whether an application can use a pgEdge database:
who can reach it, and how the connection is encrypted. Reachability has
a different answer on each edition, because a pgEdge Starfleet Managed
database runs on infrastructure the platform owns and a pgEdge Starfleet
BYOC database runs inside your own cloud account. Encryption has one
answer for both. The commands that change reachability are `database
create` and the `database allowlist` commands on Managed, and `cluster
create` and `cluster update` on BYOC.

## Network access on Managed

A Managed database listens on an externally reachable host and port,
its Postgres endpoint, and each service deployed on the database
listens on an endpoint of its own. An allowlist on each endpoint
decides which addresses can open a new connection to it. Only an
address matching a rule connects, and a rule is an IPv4 address or CIDR
block. Each endpoint has its own list and nothing is shared between
them. An endpoint with no rules is closed and admits nobody.

A new database starts closed: `database create` sends no rules unless
you pass `--allow`, `--my-ip` or `--open`, and nothing connects until a
rule is added. `database allowlist get`
reads one endpoint's rules, `add`, `remove`, `set`, `open` and `clear`
change them, and `--service` names a service's own list. The
[Controlling Network Access to a pgEdge Starfleet Managed
Database](managed/network-access.md)
guide describes each command.

## Network access on BYOC

A BYOC database inherits the network its cluster was built with, so
every reachability decision is made at cluster level rather than per
database.

### Firewall rules

`--firewall-rule` is a repeatable structured flag on `cluster create`
and `cluster update`, written as comma-separated key=value pairs. Name
and port are required on every rule. The name is one of four rule types
the API recognizes, not a label of your choosing, and the CLI holds
those four names, so a typo is a usage error before anything is sent.
The source keys take a CIDR, a provider prefix list or a provider
security group. Each source key repeats to add an element rather than a
comma-separated list, because commas already separate the pairs.

Open the Postgres port to one office range at create time:

    pgedge starfleet byoc cluster create \
        --name prod \
        --cloud-account-id <cloud-account-id> \
        --regions us-east-1 \
        --node-location public \
        --backup-store-id <backup-store-id> \
        --node name=n1,instance-type=r7g.medium,volume-size=30 \
        --network cidr=10.4.0.0/16,public-subnets=10.4.1.0/24 \
        --firewall-rule name=postgres,port=5432,sources=203.0.113.0/24

Rules append on an update. The CLI reads the cluster, adds what you
passed to the rules it already holds, and sends the whole set, so an
existing rule survives:

    pgedge starfleet byoc cluster update <cluster-id> \
        --firewall-rule name=postgres,port=5432,sources=198.51.100.0/24

The [Manage BYOC clusters](byoc/clusters.md) guide has the full tables
of keys `--firewall-rule` and `--network` accept, and the four rule
names.

### Public and private clusters

`--node-location` takes public or private, is required at create time,
and cannot change for the life of the cluster. A public cluster gets
external hostnames automatically, so a node on one has a host, and a
database running there is reachable from outside once the firewall
rules allow it. A private cluster has no external address of its own.
A node on a private cluster has `internal_host` in place of a host, and
`database connection-string` builds its URI from that address when you
pass `--internal`:

    pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --internal

Ask for the network a node does not have, and the command exits with
status 1 naming the flag that would reach it, rather than printing a
string for a network you did not choose. Reaching a service on a
private cluster from outside takes a regional ingress plus a service
registration behind it, which the
[Expose a BYOC service](byoc/expose-service.md) guide describes.

### Attach a cluster to a VPC you own

`--network` describes one VPC per region, and three of its keys attach
the cluster to a VPC that already exists in your account rather than to
one pgEdge creates. Set external to true, give the existing VPC's
provider ID under external-id, and name the network:

    pgedge starfleet byoc cluster create \
        --name prod \
        --cloud-account-id <cloud-account-id> \
        --regions us-east-1 \
        --node-location private \
        --backup-store-id <backup-store-id> \
        --node name=n1,instance-type=r7g.medium,volume-size=30 \
        --network external=true,external-id=<vpc-id>,name=prod-vpc

The CLI passes all three keys through without interpretation, because
which combinations are meaningful is per-cloud knowledge the API owns.
A cluster's networks are set once and an update cannot reach them. The
three parts of a cluster an update does change are its firewall rules,
its backup stores and its regions.

### The controls in your own cloud account

pgEdge provisions BYOC infrastructure inside your own AWS, Azure or GCP
account, using the credential a registered cloud account holds, so that
account's own network controls govern the same traffic the cluster's
firewall rules do. Security groups and the VPC configuration around the
cluster apply alongside the rules you declared, and a rule opened at
cluster level still has to pass them. `--firewall-rule` accepts a
provider security group under its security-groups key for that reason.
The [Register a BYOC cloud account](byoc/cloud-accounts.md) guide
describes the credential and the access it grants.

## Transport encryption

Connections to a pgEdge database are encrypted in transit, and the
connection strings the CLI prints ask for that explicitly. `database
connection-string` always appends sslmode=require to the URI it prints,
on Managed and BYOC alike, and `--format env` prints the same value
as PGSSLMODE. Managed hosts serve TLS with a certificate that verifies,
so require works from every client. Keep the query string on the end
of the URI, because a string trimmed back to its host and database
drops the setting without saying so.

### What require verifies

sslmode is a libpq setting, and every driver on the
[ORM and framework integration](orm-and-frameworks.md) page reads it
the same way, whether it takes the whole URI or the discrete parameters
the env format prints. At require, a driver refuses a connection the
server will not encrypt, which protects the session from an observer on
the network path. The driver does not check the server's certificate
against a certificate authority, and does not check that the
certificate matches the host it dialled. Thus require encrypts the
connection without proving that the server on the other end is the one
you meant to reach.

### Stricter verification modes

Two stricter libpq modes, verify-ca and verify-full, add the checks
that require leaves out. Both need the client to trust the authority
that signed the server's certificate, and a stricter mode is yours to
add on the client side. There is no separate CA certificate to fetch
from the CLI or the API, and a certificate that verifies against the
client's own trust store needs none. The connect an application
guides for [Managed](managed/connect-an-application.md) and
[BYOC](byoc/connect-an-application.md) describe where the setting goes
in the connection string, the connection-string command's flags, and
how to handle the live password its output holds.

## Next steps

- The [ORM and framework integration](orm-and-frameworks.md) guide
  describes where each framework reads the URI and the TLS setting on
  the end of it.
- The [Rotate a managed database password](managed/rotate-credentials.md)
  and [Rotate a BYOC database password](byoc/rotate-credentials.md)
  guides describe changing the role passwords that guard a database.
