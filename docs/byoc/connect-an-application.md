# Connecting an Application to a pgEdge Starfleet BYOC Database

`pgedge starfleet byoc database connection-string` assembles a libpq
URI for one node of a pgEdge Starfleet BYOC database and prints it. No
command on this page changes the database.

Three terms run through the page:

- A node is one Postgres server in the database. A BYOC database has
  one node or several, and each node has an address of its own.
- A connection block is the object each node has in `database get`. The
  block holds an address, a port, a database name, a role name and that
  role's password.
- The connection string is the single line the command assembles out of
  one node's connection block.

## Before You Start

Two things have to be true before the command prints anything useful.

The first is the database's full UUID. `pgedge starfleet byoc database
list` prints the UUID of every database in the tenant. The argument
takes the whole UUID, and a prefix is refused at exit status 2.

The second is a network path from the application to the database.
Reachability on BYOC belongs to the cluster, not the database, so no
per-database control exists. A cluster admits traffic from the source
addresses its firewall rules name. The security groups and VPC
configuration in your own cloud account gate the same traffic. A
private cluster has no external address at all, and an application
whose address no rule admits fails at the network layer, before
Postgres answers. The error your client reports then describes a
connection that never opened, not a database that refused one.

The [Networking and TLS](../networking-and-tls.md) guide describes the
firewall rules and the two node locations. The
[Manage BYOC clusters](clusters.md) guide has the keys each rule takes.

## Printing the Connection String

The command takes the database's UUID, and on a single-node database it
needs nothing else. Print the string without the credential first, to
confirm the host and the role before anything writes them down:

    pgedge starfleet byoc database connection-string <db-id> \
        --no-password

The output is one line and nothing beside it, in this shape:

    postgresql://<role>@<host>:<port>/<database>?sslmode=require

Without `--no-password` the same line holds the role's password after
a colon, between the role name and the `@`. The form above is the one
to paste into a ticket.

Capture the real string into a file, not onto the terminal:

    umask 077
    pgedge starfleet byoc database connection-string <db-id> \
        --node <node-name> > db-uri.txt || exit 1

`umask 077` leaves the new file readable only by you, and stopping on a
failed read matters here. On a database of several nodes, a call with
no `--node` writes the node listing into the file instead of a string.
The call then exits with status 2, saying so. Delete the file when the
application's
own configuration holds the string. Proving that the string connects
takes psql, and the
[ORM and framework integration](../orm-and-frameworks.md) guide
describes that check.

The role name and the password are percent-encoded into the URI. A
password containing `@`, `:`, `/` or `?` therefore reaches the server
unchanged. Reading a password out of the URI by eye gives you the
encoded form, which fails to authenticate until it is decoded. Where
you need the password on its own, take it from `--format env` or from
the `password` key under `-o json`. Both print the server's own bytes.

The command appends `?sslmode=require` to every URI it prints. Keep
the query string on the end, because a URI trimmed back to its host and
database drops the setting and reports nothing. The
[Networking and TLS](../networking-and-tls.md) guide describes what
`require` verifies and what it leaves to the client.

## Choosing the Node and the Network

Each node of a BYOC database has its own connection block, so a
connection string names a node and not the database. The command ranks
no node above another. With one node the command prints that node's
string. With several nodes and no `--node`, the command lists them in a
table and then exits with status 2.

That listing is output, not an error message. The table goes to
stdout, and only the sentence naming `--node` goes to stderr. A
pipeline that captures stdout without reading the exit status writes
the whole table into the configuration value it meant to fill. Read the
exit status, or pass `--node` and never reach the listing.

The table has five columns, `NODE`, `NAME`, `HOST`, `INTERNAL HOST` and
`PORT`, and no credential appears in any of them. `NODE` is the node's
label: its logical name where one is set, otherwise its own name. The
cell for the address the node lacks is empty. `--node` accepts a value
from the `NODE` column or the `NAME` column, matched exactly:

    pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --no-password

Two nodes can answer to one logical name. A label shared by two nodes
is exit status 2 under `--node`. The node's own name, from the `NAME`
column, resolves that collision. A value matching no node is exit
status 2 as well, and that message lists the labels the database has.

The text output of `database get` does not show node names. That view
is one row of `ID`, `NAME`, `STATUS`, `PG VERSION`, `CLUSTER` and
`CREATED`. Node names reach you through the `nodes` array under
`database get -o json`, or through the listing the command prints
above.

Each node advertises one address and never both. A public cluster's
node has `host`, and the command builds the string from `host` by
default. On a private cluster, `internal_host` stands in for a host,
and `--internal` is the flag that reaches that address:

    pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --internal --no-password

The flag follows the node's cluster, not your own location.
`--internal` on a public cluster's node exits with status 1, and its
absence on a private cluster's node does the same. Either message names
the flag that reaches the address the node does have. A string is never
quietly built from the network you did not ask for. Reaching a private
cluster's node from outside takes a regional ingress plus a service
registration, which the [Expose a BYOC service](expose-service.md)
guide describes.

## Choosing the Role

A BYOC database ships three built-in roles, admin, app and app_read_only.
The role admin is a real Postgres superuser. The role app is the one an
application connects as day to day, and app_read_only is the safe default
for anything serving untrusted callers. The
[Manage BYOC databases](databases.md) guide has the table describing all
three.

An application pointed at admin holds the whole server, every database
and every role on it included.

The command registers no flag for choosing a role, and BYOC's `database
get` registers none either. The connection block names one role in its
`username` key, so read that key instead of guessing which role the URI
uses:

    pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --no-password -o json

A `roles` array sits on the database beside the nodes, under
`database get -o json`. Each entry names a role and reports whether
that role is a superuser.

No command prints another built-in role's password, so the connection
string uses the role the block names and no other. `rotate-password`
issues a built-in role a new password and prints nothing, so a
rotation reaches no further. app_read_only therefore has no connection
string of its own.

Name app_read_only where a component takes a role name, not a URI.
`postgrest deploy` takes one in `--db-anon-role`, and then serves
unauthenticated requests as that role without a password.

Connect once with the string this page prints where the application
opens its own connection. Then create a role carrying only the
privileges the application uses. That role is also the answer where
the block's own role is broader than the application needs. The
[Manage BYOC databases](databases.md) guide describes such a role for
an exposed service.

## Output Formats

`--format` shapes the text output and takes two values, `uri` and
`env`. The default is `uri`, which prints the single line described
above. A third value is refused at exit status 2, before any request is
sent.

`--format env` prints one shell assignment per line instead of a URI:
`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` and `PGSSLMODE`,
which is set to `require`. Every value is wrapped in single quotes, the
one POSIX quoting form in which nothing but a single quote is special.
The output can be sourced, or written to an environment file:

    umask 077
    pgedge starfleet byoc database connection-string <db-id> \
        --node n1 --format env > db.env || exit 1

A refusal leaves `db.env` empty, so `|| exit 1` stops a client
starting with no connection settings. Each line is a plain assignment
and carries no `export`. A shell that sources the file holds the six
values and hands none of them to a client. Export the six names after
sourcing, which the
[ORM and framework integration](../orm-and-frameworks.md) guide shows
in full.

Under `--no-password` the whole `PGPASSWORD` assignment is absent, not
empty, so a password the shell already exports survives the file being
sourced. Under `--format env` a value holding a control character is
refused at exit status 1, not rewritten. A rewritten value would be a
credential the server never issued.

`-o json` and `-o yaml` outrank `--format`, and both print one object
holding `uri`, `node`, `host`, `port`, `database`, `username`,
`password` and `sslmode`. A caller takes the assembled URI, or the
individual values behind it, without a second call. The `host` key
holds the address the URI was built from, so `--internal` changes that
key as well. `--no-password` omits the `password` key instead of
blanking it. On the several-nodes path these two formats hold the node
listing as an array of rows, still at exit status 2. The
[Output formats and paging](../output-and-paging.md) guide describes
both machine encodings.

## Handling the Live Password

The output holds the role's live password in every format. With a
password in the output and a terminal on stdout, the CLI writes one
line to stderr:

    Warning: this output carries <role>'s password. Pass --no-password to leave it out.

Redirected into a file or a pipe, the command prints the output and
nothing else. The absence of that warning is not the absence of a
password.

A password written into a CI log stays valid until the role is rotated.

Five habits keep the password out of places that outlive its use:

- Keep the string off the command line. An argument list is readable
  through `ps` on a shared host.
- Do not echo the string. A shell keeps what it echoed in the
  scrollback and in the history file after the session ends.
- Pass `--no-password` for anything a person will read, such as a
  ticket, a document or a log.
- Read the string again after a rotation. `rotate-password` prints no
  replacement, and a captured string stops authenticating, as the
  [Rotate a BYOC database password](rotate-credentials.md) guide
  describes.
- Keep the output out of a build log. A job that echoes the block, or
  runs with tracing switched on, leaves the password in CI output that
  outlives it.

The [CI and automation](../ci.md) guide describes running the CLI
unattended, including where the credentials it reads should live.

## Reading the Connection Block Directly

`connection-string` assembles the URI so that no caller has to build
one by hand. Read the block itself for what the command does not
print, such as the address the string was not built from. Read the
block also for a URI you assemble yourself.

Every entry in the database's `nodes` array has one block, and only a
machine format renders it:

    umask 077
    pgedge starfleet byoc database get <db-id> -o json > db.json \
        || exit 1

`umask 077` leaves the new file readable only by you, and stopping on a
failed read shows a mistyped identifier here, not several steps later.
Without the redirection the command prints the password to your
terminal, and `database get` issues no warning when it does.

Four keys are guaranteed on every block: `username`, `password` and
`database` hold strings, and `port` holds an integer. The password is
cleartext, so a file holding this output needs the same care as a file
holding the URI. Every other key is optional, and an unset key is
absent, not null, so test for a key before reading it:

- `host`, on a node whose cluster is public.
- `internal_host`, on a node whose cluster is private.
- `external_ip_address` and `internal_ip_address`, when the API has
  them.

The command builds a string from `host` or `internal_host`, and from no
other key here. The database object has a `connection` block of its own
and three further fields, `domain`, `private_domain` and a top-level
`port`. The command reads none of them, because a node's block is the
source. A string assembled from them therefore differs from the one
the command prints.

Assembling a URI out of a block leaves the percent-encoding to you. A
password containing `@`, `:`, `/` or `?` breaks the URI into different
parts when it is copied in unencoded. The address the URI then names is
not the one you meant. The `@uri` filter encodes the two keys that
need encoding. `select` stops a node with no `host` producing a URI of
`null` values:

    jq -e -r '.nodes[] | select(.name == "n1") | .connection
        | select(.host != null)
        | "postgresql://\(.username|@uri):\(.password|@uri)"
        + "@\(.host):\(.port)/\(.database)?sslmode=require"' \
        db.json || exit 1

Read `.internal_host` in place of `.host` on a private cluster's node.
A node name that matches nothing prints nothing and exits non-zero, so
the script stops there.

## Refusals and Exit Statuses

A run that prints a string exits with status 0. Among the failures,
exit status 1 is a general runtime failure, 2 is a usage error, and 4
is a resource that does not exist. The following table describes what
the command refuses and the status each refusal exits with:

| What happened | Exit status |
|---|---|
| The argument is not a full UUID | 2 |
| No database in the tenant has that ID | 4 |
| `--node` was given an empty value | 2 |
| `--node` names no node of the database | 2 |
| `--node` names a label two nodes answer to | 2 |
| The database has several nodes and `--node` is absent | 2, after the node listing on stdout |
| `--format` was given a value other than `uri` or `env` | 2 |
| The node has only the address the other flag reaches | 1 |
| The node has no address yet, being still in creation | 1 |
| The database has no nodes yet, being still in creation | 1 |
| A connection value holds a control character under `--format env` | 1 |

A credential that does not resolve, and a request the API rejects,
fail here the way they fail on every other command. The
[Exit codes](../exit-codes.md) guide has the contract behind the
numbers. The [Error catalog](../error-catalog.md) collects the
messages callers meet most often. Each entry there routes to the guide
describing the behavior behind it.

## Next Steps

- The [ORM and framework integration](../orm-and-frameworks.md) guide
  describes where each framework reads the URI, and how to prove the
  string with psql.
- The [Networking and TLS](../networking-and-tls.md) guide describes
  the firewall rules, the two node locations and what `sslmode=require`
  verifies.
- The [Manage BYOC databases](databases.md) guide describes the
  built-in roles and reading a database's credentials back.
- The [Schema migrations on a BYOC database](schema-migrations.md)
  guide describes pointing a migration tool at the string this page
  prints.
- The [CI and automation](../ci.md) guide describes running the CLI
  from a pipeline.
