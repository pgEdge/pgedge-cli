# Controlling Network Access to a pgEdge Starfleet Managed Database

A pgEdge Starfleet Managed database accepts a new connection only from
an address on its allowlist. You decide which addresses those are, and
you can change the list at any time with the `allowlist` commands.

Three terms recur on the page:

- An endpoint is an address a client connects to. Every database has a
  Postgres endpoint, and each service you deploy on it (MCP, RAG or
  PostgREST) adds an endpoint of its own.
- A rule is one IPv4 address or CIDR block on an endpoint's allowlist.
  An endpoint holds at most 50 rules.
- The state of an endpoint sums up its rules. A `closed` endpoint has
  no rules and accepts no one, a `restricted` endpoint accepts the
  addresses its rules name, and an `open` endpoint accepts every
  address.

A new database starts closed. Nobody can connect to it, including you,
until you add a rule.

## Before You Start

You need two values:

- The database ID, which `pgedge starfleet managed database list`
  shows.
- The public IPv4 address that your Postgres client or application
  connects from. IPv6 addresses cannot go on an allowlist.

Every allowlist change needs the database to report `available`. At
any other status, the change is refused with exit status 1. An
accepted change moves the database to `modifying` until the new rules
apply.

## Checking Who Can Connect

Run `database get` to see every endpoint on the database at once:

    pgedge starfleet managed database get <db-id>

The output ends with an `Allowlists` table. The table has one row for
the Postgres endpoint and one row for each deployed service, with the
state and the number of rules on each.

To see the rules themselves, run `allowlist get`:

    pgedge starfleet managed database allowlist get <db-id>

The command prints the endpoint, its state, and a table of each rule
with its label. For an endpoint with no rules, the command prints
the line `No rules; the endpoint is closed.` in place of the table.

## Allowing Your Own Address

The quickest way to reach a new database from your own machine is
`--my-ip`. The flag adds the address that the pgEdge API sees your
command arriving from:

    pgedge starfleet managed database allowlist add <db-id> --my-ip

That address is not always the one your Postgres client uses. A VPN,
a proxy or a corporate network can send API traffic and database
traffic out through different addresses. After you add the rule,
connect with `psql` to confirm that the database accepts you. To see
the address before you add it, run `pgedge starfleet managed
client-ip`.

## Allowing an Application or Office Network

Pass each address or block to `allowlist add`, with a label that says
what the rule is for:

    pgedge starfleet managed database allowlist add <db-id> \
        203.0.113.0/24 198.51.100.9 --label office

The label is stored on every rule the command adds, and is at most 64
characters. The command leaves existing rules in place. An address
that is already on the list is skipped, and the command names it.

The allowlist stores a single address as a /32 block, and stores a
block by its network address. For example, `203.0.113.7` is stored as
`203.0.113.7/32`, and `203.0.113.99/24` is stored as `203.0.113.0/24`.

A deployment script usually knows the whole list. In that case, run
`allowlist set` to replace every rule on the endpoint in one request:

    pgedge starfleet managed database allowlist set <db-id> \
        203.0.113.0/24 198.51.100.9 --label ci

Any rule that you leave out of `allowlist set` is removed.

## Removing an Address

Run `allowlist remove` with each rule to drop:

    pgedge starfleet managed database allowlist remove <db-id> \
        203.0.113.7

You can name a rule in the form it is stored in, or in the form you
typed when you added it. Removing the last rule closes the endpoint,
so the command asks you to confirm first. In a script, pass `--force`
to skip the prompt.

A removed address cannot open a new connection. A session that is
already open from that address stays connected until the session
ends. The platform does not restart Postgres for an allowlist change,
so no connection drops.

## Controlling Access to a Service

A service endpoint has its own allowlist, separate from the Postgres
endpoint. An address that can reach Postgres cannot reach the MCP
server until you allow it there as well. A newly deployed service
starts closed.

Pass `--service` with `mcp`, `rag` or `postgrest` to work on that
service's list. Every `allowlist` command accepts the flag:

    pgedge starfleet managed database allowlist add <db-id> \
        203.0.113.7 --service mcp

The service must be deployed on the database. For a service that is
not deployed, the command exits with status 4.

## Opening or Closing an Endpoint

Run `allowlist open` to accept connections from every address:

    pgedge starfleet managed database allowlist open <db-id>

The command replaces every rule with the single rule `0.0.0.0/0`. On
an open endpoint, the Postgres role and password are the only guard,
and the CLI prints a warning that says so. To restrict the endpoint
again, run `allowlist set` with the addresses that need access.

Run `allowlist clear` to close an endpoint to every address:

    pgedge starfleet managed database allowlist clear <db-id>

The command asks you to confirm, because no client can open a new
connection afterward. Pass `--force` to skip the prompt. The
`allowlist` commands still work on a closed endpoint, so you can add a
rule again at any time.

## Allowing Addresses When You Create the Database

`database create` can set the Postgres endpoint's allowlist, so the
database is reachable as soon as it is available. Pass `--allow` once
for each address or block, `--my-ip` for your own address, or both:

    pgedge starfleet managed database create --name mydb --size small \
        --allow 203.0.113.0/24 --my-ip

`--open` admits every address instead, and cannot be combined with
the other two flags. A database created with none of the three is
closed, and the CLI prints the `allowlist add` command to run next.
`--allow` stores no label. `allowlist add` skips an address that is
already on the list, so to label those rules later, run `allowlist
set` with the full list and `--label`.

## Waiting for a Change to Apply

By default, an `allowlist` command returns when the platform accepts
the change, before the new rules apply. The command then prints a
`task list` command that follows the change. A script that connects next
can fail if it runs too soon. Pass `--wait` so that the command
returns only when the change has finished:

    pgedge starfleet managed database allowlist add <db-id> \
        203.0.113.7 --wait

`--wait` gives up after 600 seconds by default. Pass
`--wait-timeout` with a number of seconds to change the limit.

## Troubleshooting

### The Command Exits With Status 2 Before Changing Anything

The CLI checks two limits before it sends a change. The allowlist
would hold more than 50 rules, or the `--label` value is longer than
64 characters. Remove rules the endpoint no longer needs, or shorten
the label, and run the command again.

`allowlist set` also exits with status 2 when you give it no address.
To close an endpoint, run `allowlist clear`.

### The Command Exits With Status 4

`allowlist remove` exits with status 4 when a rule you named is not
on the endpoint. Nothing is removed, including the rules that did
match. Run `allowlist get` to read the rules as stored, then run the
command again with those values.

### The API Refuses an Address

The API refuses an IPv6 address and any value that is not a valid
IPv4 address or CIDR block. `--my-ip` exits with status 1 when the
API sees your command arrive over IPv6. In that case, find your IPv4
address from your network and pass it to `allowlist add`.

### The Database Still Refuses a Connection

Run `allowlist get` and compare each rule with the address your
client connects from. For a service, pass `--service`, because the
Postgres rules do not apply to it. If the change was recent, run
`database get` until the database reports `available`.

## Next Steps

- [Connecting an Application to a pgEdge Starfleet Managed
  Database](connect-an-application.md)
- [Deploy managed services](services.md)
- [Networking and TLS](../networking-and-tls.md)
