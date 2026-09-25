# Working with AI Agents

You can let an AI agent run the `pgedge` CLI, and you can use the CLI
to give an agent query access to a database. These are two separate
jobs:

- **Driving the CLI:** the agent runs `pgedge` commands and reads the
  results. It needs an accurate description of the CLI to do this.
- **Querying a database:** `pgedge` deploys pgEdge's own MCP server
  beside a database. The MCP server gives the agent a query interface
  over that database. The CLI takes part only at deploy time.

Both jobs apply to the two editions of pgEdge Starfleet, pgEdge
Starfleet Managed and pgEdge Starfleet BYOC, and to the self-hosted
Control Plane.

## Letting an Agent Drive the CLI

An agent that builds commands from `--help` output is guessing. The CLI
ships two things that give the agent an exact description: a
machine-readable reference inside the binary, and a set of agent
skills.

### Reading the Built-In Reference

`pgedge llms` prints the index. The index covers what holds across the
whole CLI:

- global flags
- configuration and profiles
- environment variables
- output formats and exit codes

Its tables route to each module's own reference. They also route to a
page per top-level command group, such as `pgedge llms inspect`. Point
an agent at the index first, because the index tells the agent which
read it needs next.

`pgedge llms <module>` prints one module's index. The index covers what
holds across the module, then a Pages table routes to one page per
resource. A page's path is its command's path: for example,
`pgedge llms starfleet byoc cluster` documents
`pgedge starfleet byoc cluster` and its commands.

The `database` page routes once more, to its sub-resources, such as
`pgedge llms starfleet managed database mcp`. The `starfleet` module
carries two product sub-trees, `byoc` and `managed`, and each has an
index of its own. An agent reaches any command in three short reads.
A sub-resource of a product sub-tree takes four. At each step the agent
loads only the resource it is working on.

The whole reference ships inside the binary. An installed copy prints
it offline, with the binary alone. If you name a module the binary
does not carry, the command fails with exit status 2. The message lists
the modules the binary does carry.

The same content is on this site in the
[pgedge command reference](reference/pgedge.md), generated from the
same command tree. An agent with a terminal should use `pgedge llms`,
because it describes the binary that is installed.

### Installing the Agent Skills

pgEdge ships five agent skills in the `skills/` directory of the
repository. They follow the Agent Skills open standard. The following
table describes what each one covers:

| Skill | Covers |
|---|---|
| `pgedge` | CLI orientation, choosing a module, profiles and installation. |
| `pgedge-starfleet` | pgEdge Starfleet authentication, API clients, team invites and memberships. |
| `pgedge-byoc` | BYOC clusters, databases, services, ingresses, backups and SSH keys. |
| `pgedge-managed` | Managed databases, their services and their backups. |
| `pgedge-controlplane` | A self-hosted Control Plane: cluster formation, databases, backups and failover. |

One command installs all five and detects your agent:

    npx skills add pgEdge/pgedge-cli

The installer writes to `.agents/skills/`, the shared location that
supporting agents read. For agents with a convention of their own, it
adds a symlink to that location. As a result, Claude Code, Cursor,
Copilot, Amp, Antigravity and a dozen others pick up the skills from
one install.

Project scope is the default. The skills land in the repository you run
the command from, so teammates and cloud agents share the setup. After
the install, the same tool's `list`, `update` and `remove` commands
manage the skills. `skills-lock.json` pins each skill by content hash.

Skills are a layer above the CLI, so you install them separately from
the `pgedge` binary, with `npx skills`. The README's "Installing the
AI-Agent Skills" section covers three more cases:

- installing a single skill
- installing for your user in place of a project
- copying the skills by hand for an agent without Node

Some agents read neither `.agents/skills/` nor a convention the
installer links. For such an agent, copy the directories under
`skills/` to the place that agent reads. Confirm that place in the
agent's own documentation. The rest of the setup stays the same.

### Parsing Command Output

`-o json` and `-o yaml` render the API's own object. The renderer
passes both through the same JSON tags, so a `yq` path always matches
the equivalent `jq` path. `--verbose` and `--debug` write their
diagnostics to `stderr`, so `stdout` carries only the result.

Some successes return an empty `stdout` in all three formats. For
these, the acknowledgment is exit status 0 and a sentence on `stderr`.
An agent that reads an empty `stdout` as failure misreads them, so
judge success by the exit status. The
[Output formats and paging](output-and-paging.md) page has the full
contract. The [Exit codes](exit-codes.md) page covers what each status
lets an agent conclude.

## Deploying an MCP Server

The MCP server runs beside a Postgres database and gives an LLM a
query interface over it. The server is read-only unless you grant more.
The commands that deploy one depend on the product, and each of the
three below has its own recipe.

### Deploying on a Managed Database

The write commands live under `database mcp`. A deploy with no flags
set yields a read-only server with an API-generated bearer token:

    pgedge starfleet managed database mcp deploy <database-id> --wait

If an MCP service is already deployed, `deploy` refuses to run and
names `mcp update` as the fix. The
[Deploy managed services](managed/services.md) page covers every
configuration flag. It also says which flags the API treats as
secrets, and exactly what a wait proves.

### Deploying on a BYOC Database

The commands read the same. The service runs on the cluster's own nodes
in your cloud account, so your own networking stands in front of its
endpoint:

    pgedge starfleet byoc database mcp deploy <database-id> --wait

On a multi-node cluster, pass `--target-nodes`, because the CLI refuses
to choose the placement. The [Deploy BYOC services](byoc/services.md)
page covers placement and the configuration flags. It also covers the
regional ingress a private cluster needs before anything outside the
VPC can reach the service.

### Deploying on a Self-Hosted Control Plane

The Control Plane has no `mcp` command group. Declare the service in
the database spec instead, under the spec's `services` block. The
service reaches the Control Plane when you create the database:

    pgedge controlplane database init -i > storefront.yaml
    pgedge controlplane database create storefront -f storefront.yaml --wait

`database init -i` interviews you for the spec. Its guided services
step collects these values:

- the service type
- a service id
- the connect-as database user
- host IDs and version
- a guided LLM configuration, for an mcp service

The interview writes every credential as a placeholder. The provider
credential lands as `CHANGE-ME`, and `create` refuses the spec until
you replace it. The
[Manage Control Plane databases](controlplane/databases.md) page covers
the three service shapes. It also covers the bootstrap credentials the
Control Plane accepts on create and rejects on update.

## Connecting an Agent to a Deployed Server

On the Control Plane, you configure a service through the spec's
`config` block, and its init token field holds the bearer token. The
[Manage Control Plane databases](controlplane/databases.md) page covers
reading a service back.

On Managed and BYOC, an MCP client needs two values. Both come from a
read of a single database, so take neither from the deploy output.

The endpoint is the first value, and the two products report it in
different ways. The following table shows where each one leaves it:

| Product | The URL to give the client |
|---|---|
| Managed | The service's `uri`, exactly as reported. It already carries the version segment, so it reads `https://<domain>/mcp/v1`. |
| BYOC | ENDPOINT is a bare origin, so append `/mcp/v1` to it. |

Take that value from the API, because the platform chooses the path
segment. On Managed, `service list`, `service get` and `database get`
all print it as ENDPOINT, and `-o json` carries the field itself. On
BYOC, the CLI builds ENDPOINT for the table from the service's domains.
Under `-o json`, a script builds the same locator from the raw fields.

The bearer token is the second value. It holds one of three things:

- the value passed to `--init-tokens` at deploy time
- a token the API generated, when the deploy omitted that flag
- a token rotated afterward with `mcp update`

A token typed as a flag lands in your shell history. Anyone on the same
host can also see it in `ps`. Let the API generate the token instead,
and read it back.

Table views leave out every secret, in any format. Read the token with
`-o json` on a read that hits the single database. `database get`,
`service get` and `service list` all carry it, and `database list`
leaves it out. The token sits in the `services` entry whose
`service_type` is `mcp`, under that entry's `mcp_config` block.

The following script reads both values for a managed database and stops
if either read fails. The file holds a working bearer token, so the
script creates it under a restrictive umask. You remove it when the
client has the values:

    umask 077
    if ! pgedge starfleet managed database get <database-id> -o json \
        > mydb.json
    then
        echo "database get failed; mydb.json holds nothing" >&2
        exit 1
    fi
    uri=$(jq -r '.services[] | select(.service_type=="mcp") | .uri' \
        mydb.json)
    token=$(jq -r '.services[] | select(.service_type=="mcp") |
        .mcp_config.init_tokens' mydb.json)
    if [ -z "$uri" ] || [ "$uri" = null ] ||
       [ -z "$token" ] || [ "$token" = null ]
    then
        echo "mydb.json carries no MCP uri or token" >&2
        exit 1
    fi

The script checks both cases for a reason. The redirection empties the
file before the command runs, so a failed read leaves an empty file,
and `jq` reads it happily. A service with no token yields the four
characters `null`. A client would then send `null` as its bearer token.

Re-run the read each time, and avoid reusing the file later. A file
written for another database makes a successful call against the wrong
server, and the call looks correct. When the client has the values,
delete the file:

    rm -f mydb.json

### Configuring the MCP Client

Each agent keeps its MCP client configuration in its own place, and its
own documentation names that place. Every agent's configuration holds
the same two entries:

- the URL from the table above, as a streamable-HTTP MCP server.
- an `Authorization` header carrying `Bearer` and the token.

The transport wants an `Accept` header naming both JSON and the event
stream. The first request on a connection must be an `initialize` call.
A `tools/list` sent first is a lifecycle violation. A client that
speaks streamable HTTP handles both of these itself. A server may
answer with an `Mcp-Session-Id` header, which later requests on the
same session echo back.

### Checking Readiness After a Deploy

Exit status 0 on a deploy means the API accepted the write. The server
may still be unable to answer. On Managed, the endpoint returns 503 for
a while after the deploy. During that window, the service `state` field
already reads `running`.

A successful `initialize` call is the true readiness signal. Both
services pages carry a `curl` recipe that makes one. Point the agent at
the server when that call succeeds, and ignore the deploy's return.

## Setting Query Limits and Write Access

Managed and BYOC deploy the same server, and it behaves as follows.

The query tool takes a `limit` and an `offset` to continue from. The
`limit` defaults to 100 rows and tops out at 1000. The server appends
both to a query that lacks them. A truncated response says more rows
are available, and names the offset to continue from. A complete
result of the same size reports only its row count.

All of that depends on the server supplying the limit. The server
checks for an existing limit with a substring test over the query text.
Any `LIMIT` in the text stops the server appending anything, and turns
off the truncation notice with it. A column name or an alias such as
`unlimited` counts. An agent should pass `limit` as a parameter, and
keep the clause out of the SQL.

Write access is off by default. On Managed and BYOC, `--allow-writes`
grants the LLM insert, update and delete access through the query tool.

## Next Steps

These pages cover the next tasks:

- The [Deploy managed services](managed/services.md) page covers
  deploying and configuring an MCP server on a managed database.
- The [Deploy BYOC services](byoc/services.md) page covers the same on
  a BYOC database. Enough differs there that a Managed recipe needs
  changes before it works.
- The [Manage Control Plane databases](controlplane/databases.md) page
  covers declaring services in a Control Plane database spec.
- The [Getting started](getting-started.md) page covers installing the
  CLI and its skills from scratch.
